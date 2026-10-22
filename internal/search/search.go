// Package search is the cancellable, bounded project search, independent of
// language servers. It walks the files the picker would list and looks for a
// literal, case-sensitive text; the active file is searched in its unsaved
// form.
package search

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/guard"
	"github.com/fclaude/coigue/internal/navigation"
	"github.com/fclaude/coigue/internal/picker"
	"github.com/fclaude/coigue/internal/ucd"
	"github.com/fclaude/coigue/internal/walk"
)

const (
	fileLimit   = 2 * 1024 * 1024
	resultLimit = 1000
	// Hits go out 32 at a time through a channel that holds 8 batches.
	hitBatch     = 32
	channelBatch = 8
	labelChars   = 180
)

// Active is the text of the file being edited, which is searched instead of
// the copy on disk.
type Active struct {
	Path string
	Text string
}

// batch is hits, or (when done) the final tally.
type batch struct {
	hits    []navigation.Target
	done    bool
	skipped int
	capped  bool
}

// Search is a running search.
type Search struct {
	receiver chan batch
	cancel   context.CancelFunc
	done     chan struct{}
}

// Start begins searching root for query in the background. Closing the search
// (or cancelling ctx) stops it, also when it is waiting for results to be taken.
func Start(ctx context.Context, root, query string, active *Active) *Search {
	ctx, cancel := context.WithCancel(ctx)
	if active != nil {
		// The editor keeps editing its copy; the search works on this one.
		copied := *active
		active = &copied
	}
	s := &Search{
		receiver: make(chan batch, channelBatch),
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	guard.Go(func() {
		defer close(s.done)
		defer close(s.receiver)
		run(ctx, s.receiver, root, query, active)
	})
	return s
}

// send hands a batch to the consumer, giving up when the search is closed.
func send(ctx context.Context, out chan<- batch, b batch) bool {
	select {
	case out <- b:
		return true
	case <-ctx.Done():
		return false
	}
}

func run(ctx context.Context, out chan<- batch, root, query string, active *Active) {
	if query == "" {
		send(ctx, out, batch{done: true})
		return
	}
	walker := picker.Walker(root) // sorted by path, like sort_by_file_path
	skipped := 0
	count := 0
	var hits []navigation.Target
	for {
		item, ok := walker.Next(ctx)
		if !ok {
			break
		}
		if ctx.Err() != nil {
			return
		}
		if item.Err != nil {
			skipped++
			continue
		}
		entry := item.Entry
		if entry.Kind != walk.KindFile {
			continue
		}
		if entry.Err != nil {
			skipped++
		}
		path := entry.Path
		content, ok := readContent(path, active)
		if !ok {
			skipped++
			continue
		}
		for row, line := range strings.Split(content, "\n") {
			if ctx.Err() != nil {
				return
			}
			byteIndex := strings.Index(line, query)
			if byteIndex < 0 {
				continue
			}
			location := navigation.NewLocation(path)
			location.Row = row
			location.Col = utf8.RuneCountInString(line[:byteIndex])
			relative := path
			if rest, ok := fsx.StripPrefix(path, root); ok {
				relative = rest
			}
			label := fmt.Sprintf("%s:%d:%d  %s", walk.Lossy(relative), row+1, location.Col+1, preview(line))
			hits = append(hits, navigation.Target{Location: location, Label: label})
			count++
			if len(hits) == hitBatch {
				if !send(ctx, out, batch{hits: hits}) {
					return
				}
				hits = nil
			}
			if count == resultLimit {
				break
			}
		}
		if count == resultLimit {
			break
		}
	}
	if ctx.Err() != nil {
		return
	}
	if len(hits) > 0 && !send(ctx, out, batch{hits: hits}) {
		return
	}
	send(ctx, out, batch{done: true, skipped: skipped, capped: count == resultLimit})
}

// readContent is the text of a file to search, or false when it must be
// skipped: unreadable, over the size limit, not UTF-8 or containing NUL.
func readContent(path string, active *Active) (string, bool) {
	if active != nil && samePath(active.Path, path) {
		if len(active.Text) <= fileLimit {
			return active.Text, true
		}
		return "", false
	}
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > fileLimit {
		return "", false
	}
	data, err := io.ReadAll(io.LimitReader(file, fileLimit+1))
	if err != nil || !utf8.Valid(data) || len(data) > fileLimit {
		return "", false
	}
	text := string(data)
	if strings.ContainsRune(text, 0) {
		return "", false
	}
	return text, true
}

// samePath is Path equality: the same components, however spelled.
func samePath(a, b string) bool {
	ac, bc := fsx.Components(a), fsx.Components(b)
	if len(ac) != len(bc) {
		return false
	}
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	return true
}

// preview is the line without surrounding Unicode whitespace, cut to 180
// characters.
func preview(line string) string {
	line = strings.TrimFunc(line, ucd.IsWhitespace)
	if utf8.RuneCountInString(line) <= labelChars {
		return line
	}
	n := 0
	for i := range line {
		if n == labelChars {
			return line[:i]
		}
		n++
	}
	return line
}

// Poll moves what the search has found so far into the list. It takes at most
// 8 batches per call.
func (s *Search) Poll(list *navigation.List) {
	for range 8 {
		select {
		case b, ok := <-s.receiver:
			switch {
			case !ok:
				list.Busy = false
				return
			case b.done:
				list.Busy = false
				capped := ""
				if b.capped {
					capped = "; first 1000 results, refine search"
				}
				list.Notice = fmt.Sprintf("%d matching lines; %d skipped%s", len(list.Targets), b.skipped, capped)
				return
			default:
				list.Targets = append(list.Targets, b.hits...)
			}
		default:
			return
		}
	}
}

// Close stops the search and releases its goroutine even when it is blocked
// handing over results. It may be called more than once.
func (s *Search) Close() { s.cancel() }
