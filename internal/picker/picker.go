// Package picker is the built-in, cancellable file discovery and the
// deterministic fuzzy path ranking behind Ctrl-P. The scan runs in a
// goroutine that hands batches of files to the UI, which drains them with Poll.
package picker

import (
	"context"
	"sort"
	"sync"

	"github.com/fclaude/coigue/internal/fileops"
	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/guard"
	"github.com/fclaude/coigue/internal/ucd"
	"github.com/fclaude/coigue/internal/walk"
)

// Walker is the discovery policy shared by the file picker and project
// search: hidden files are listed, .git and the recovery directory are never
// entered, .gitignore and .git/info/exclude apply (also from parent
// directories and outside a repository), symlinks are not followed, and
// neither .ignore files nor the global excludes file are read.
func Walker(root string) *walk.Walker {
	return walk.New(root, walk.Options{
		GitIgnore:  true,
		GitExclude: true,
		Parents:    true,
		Filter: func(entry *walk.Entry) bool {
			return entry.Name != ".git" && entry.Name != fileops.Trash
		},
	})
}

// Picker lists the files below Root and ranks them against Query.
type Picker struct {
	Root     string
	Query    string
	Selected int
	Scanning bool
	Skipped  int

	files   []string
	labels  []string
	matches []int
	queue   *queue
	cancel  context.CancelFunc
	done    chan struct{}
}

type batch struct {
	files   []string
	skipped int
}

// queue is the unbounded channel between the scan and Poll (Rust's
// mpsc::channel): the scan never waits for the UI.
type queue struct {
	mu     sync.Mutex
	items  []batch
	closed bool
}

func (q *queue) send(b batch) {
	q.mu.Lock()
	q.items = append(q.items, b)
	q.mu.Unlock()
}

func (q *queue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
}

type received uint8

const (
	receivedBatch received = iota
	receivedEmpty
	receivedDisconnected
)

// tryRecv returns the next batch; a closed queue reports Disconnected only once
// it is drained.
func (q *queue) tryRecv() (batch, received) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) > 0 {
		b := q.items[0]
		q.items[0] = batch{}
		q.items = q.items[1:]
		return b, receivedBatch
	}
	if q.closed {
		return batch{}, receivedDisconnected
	}
	return batch{}, receivedEmpty
}

// New starts scanning root in the background. Closing the picker, or
// cancelling ctx, stops the scan.
func New(ctx context.Context, root string) *Picker {
	ctx, cancel := context.WithCancel(ctx)
	p := &Picker{
		Root:     root,
		Scanning: true,
		queue:    &queue{},
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	guard.Go(func() { scan(ctx, root, p.queue, p.done) })
	return p
}

func scan(ctx context.Context, root string, q *queue, done chan<- struct{}) {
	defer close(done)
	defer q.close()
	walker := Walker(root)
	current := batch{}
	for {
		item, ok := walker.Next(ctx)
		if !ok {
			break
		}
		if item.Err != nil {
			current.skipped++
		} else {
			entry := item.Entry
			if entry.Err != nil {
				current.skipped++
			}
			if entry.Kind == walk.KindFile {
				current.files = append(current.files, entry.Path)
			}
		}
		if len(current.files) >= 128 {
			q.send(current)
			current = batch{}
		}
	}
	if ctx.Err() != nil {
		return
	}
	q.send(current)
}

// Poll takes what the scan has produced so far. It handles a bounded number
// of batches per call so that keyboard input stays responsive during discovery.
func (p *Picker) Poll() {
	changed := false
poll:
	for range 32 {
		b, state := p.queue.tryRecv()
		switch state {
		case receivedBatch:
			p.Skipped += b.skipped
			for _, path := range b.files {
				label := path
				if rest, ok := fsx.StripPrefix(path, p.Root); ok {
					label = rest
				}
				p.labels = append(p.labels, walk.Lossy(label))
				p.files = append(p.files, path)
				changed = true
			}
		case receivedDisconnected:
			p.Scanning = false
			break poll
		default:
			break poll
		}
	}
	if changed {
		p.filter()
	}
}

func (p *Picker) filter() {
	selected, hadSelection := -1, false
	if p.Selected < len(p.matches) {
		selected, hadSelection = p.matches[p.Selected], true
	}
	query := ucd.Lower(p.Query)
	type ranked struct {
		index int
		score int64
	}
	var list []ranked
	for i, label := range p.labels {
		if score, ok := Score(label, query); ok {
			list = append(list, ranked{i, score})
		}
	}
	sort.Slice(list, func(a, b int) bool {
		if list[a].score != list[b].score {
			return list[a].score > list[b].score
		}
		return p.labels[list[a].index] < p.labels[list[b].index]
	})
	p.matches = p.matches[:0]
	for _, r := range list {
		p.matches = append(p.matches, r.index)
	}
	p.Selected = 0
	if hadSelection {
		for position, m := range p.matches {
			if m == selected {
				p.Selected = position
				break
			}
		}
	}
}

// QueryChanged re-ranks after the query was edited and selects the best match.
func (p *Picker) QueryChanged() {
	p.Selected = 0
	p.filter()
	p.Selected = 0
}

// MoveBy moves the selection, staying within the matches.
func (p *Picker) MoveBy(delta int) {
	selected := p.Selected
	if delta < 0 {
		if -delta > selected {
			selected = 0
		} else {
			selected += delta
		}
	} else if selected > int(^uint(0)>>1)-delta {
		selected = int(^uint(0) >> 1)
	} else {
		selected += delta
	}
	p.Selected = min(selected, max(len(p.matches)-1, 0))
}

// Len is the number of files matching the query.
func (p *Picker) Len() int { return len(p.matches) }

// Total is the number of files found so far.
func (p *Picker) Total() int { return len(p.files) }

// Label is the path of the i-th match relative to the root.
func (p *Picker) Label(i int) (string, bool) {
	if i < 0 || i >= len(p.matches) {
		return "", false
	}
	return p.labels[p.matches[i]], true
}

// Path is the full path of the selected match.
func (p *Picker) Path() (string, bool) {
	if p.Selected < 0 || p.Selected >= len(p.matches) {
		return "", false
	}
	return p.files[p.matches[p.Selected]], true
}

// Close stops the scan. It may be called more than once.
func (p *Picker) Close() { p.cancel() }

type optional struct {
	value int64
	some  bool
}

func maxOptional(a, b optional) optional {
	switch {
	case !a.some:
		return b
	case !b.some:
		return a
	case b.value > a.value:
		return b
	}
	return a
}

// Score ranks label against an already lower-cased query: ordered
// subsequences, rewarded for consecutive characters, word boundaries, and
// basename matches. Shorter paths win otherwise comparable matches.
func Score(label, query string) (int64, bool) {
	if query == "" {
		return 0, true
	}
	chars := []rune(ucd.Lower(label))
	basename := 0
	for i := len(chars) - 1; i >= 0; i-- {
		if chars[i] == '/' {
			basename = i + 1
			break
		}
	}
	previous := make([]optional, len(chars))
	for queryIndex, needle := range []rune(query) {
		current := make([]optional, len(chars))
		var prefixBest optional
		for index, c := range chars {
			if c == needle {
				bonus := int64(10)
				if index == 0 || isBoundary(chars[index-1]) {
					bonus += 15
				}
				if index >= basename {
					bonus += 10
				}
				var base optional
				if queryIndex == 0 {
					base = optional{0, true}
				} else {
					// Find the best alignment, including matches later in the
					// path; an earlier directory must not hide a basename match.
					var consecutive optional
					if index > 0 && previous[index-1].some {
						consecutive = optional{previous[index-1].value + 20, true}
					}
					base = maxOptional(prefixBest, consecutive)
				}
				if base.some {
					current[index] = optional{base.value + bonus, true}
				}
			}
			prefixBest = maxOptional(prefixBest, previous[index])
		}
		previous = current
	}
	var best optional
	for _, o := range previous {
		best = maxOptional(best, o)
	}
	if !best.some {
		return 0, false
	}
	return best.value - int64(len(chars)), true
}

func isBoundary(c rune) bool {
	switch c {
	case '/', '_', '-', '.', ' ':
		return true
	}
	return false
}
