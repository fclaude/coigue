package walk_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fclaude/coigue/internal/explorer"
	"github.com/fclaude/coigue/internal/navigation"
	"github.com/fclaude/coigue/internal/picker"
	"github.com/fclaude/coigue/internal/search"
)

// The end-to-end differential test runs the editor's own Rust modules
// (picker.rs, explorer.rs, search.rs, copied unchanged into a scratch program)
// and the Go packages over the same random trees, and compares what a user
// would see: the picker's ranked labels and counts, the explorer's rows and
// message, the search's labels, positions and notice.

func hexOf(s string) string { return hex.EncodeToString([]byte(s)) }

var pickerQueries = []string{"", "a", "rs", "main", "é", "x y", "log", "T", "d/a", "ñ", "fooa", "txt"}
var searchQueries = []string{"needle", "a", "foo", "é", "😀", " ", "needleneedle", "x", "NEEDLE"}

func goPicker(t *testing.T, root, query string) []string {
	p := picker.New(context.Background(), root)
	defer p.Close()
	deadline := time.Now().Add(20 * time.Second)
	for p.Scanning {
		p.Poll()
		if time.Now().After(deadline) {
			t.Fatal("picker did not finish")
		}
		time.Sleep(200 * time.Microsecond)
	}
	p.Query = query
	p.QueryChanged()
	lines := []string{
		fmt.Sprintf("skipped %d", p.Skipped),
		fmt.Sprintf("total %d", p.Total()),
		fmt.Sprintf("len %d", p.Len()),
	}
	for i := 0; i < p.Len(); i++ {
		label, _ := p.Label(i)
		lines = append(lines, "L "+escapeBytes(label))
	}
	if path, ok := p.Path(); ok {
		return append(lines, "P "+escapeBytes(path))
	}
	return append(lines, "P none")
}

func goExplorer(t *testing.T, dir string, showAll bool, focus, filter string) []string {
	view := explorer.New(context.Background(), dir, focus, explorer.Memory{ShowAll: showAll})
	defer view.Close()
	deadline := time.Now().Add(20 * time.Second)
	for view.Loading {
		view.Poll()
		if time.Now().After(deadline) {
			t.Fatal("explorer did not finish")
		}
		time.Sleep(200 * time.Microsecond)
	}
	view.Fit(4)
	var lines []string
	dump := func() {
		lines = append(lines, "M "+escapeBytes(view.Message), fmt.Sprintf("S %d T %d", view.Selected, view.Top))
		for i := 0; i < view.Len(); i++ {
			e, _ := view.Entry(i)
			kind := 'f'
			if e.Directory {
				kind = 'd'
			}
			lines = append(lines, fmt.Sprintf("N %c %s %s", kind, escapeBytes(e.Name), escapeBytes(e.Path)))
		}
	}
	dump()
	if filter != "" {
		view.StartFilter()
		view.Query = filter
		view.Filter()
		lines = append(lines, "FILTER")
		dump()
	}
	return lines
}

func goSearch(t *testing.T, root, query string, active *search.Active) []string {
	s := search.Start(context.Background(), root, query, active)
	defer s.Close()
	list := navigation.NewList("Search", nil)
	list.Busy = true
	deadline := time.Now().Add(20 * time.Second)
	for list.Busy {
		s.Poll(list)
		if time.Now().After(deadline) {
			t.Fatal("search did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	lines := []string{"notice " + escapeBytes(list.Notice)}
	for _, target := range list.Targets {
		lines = append(lines, fmt.Sprintf("T %d %d %s %s", target.Location.Row, target.Location.Col, escapeBytes(target.Label), escapeBytes(target.Location.Path)))
	}
	return lines
}

type e2eJob struct {
	request string // the line for the Rust driver
	run     func(t *testing.T) []string
}

func planE2E(t *testing.T, r *rand.Rand, dir string, spec treeSpec) []e2eJob {
	var dirs, files []string
	for _, e := range spec {
		switch {
		case e.Kind == 'd' && e.Mode == 0 && strings.HasPrefix(e.Path, "proj"):
			dirs = append(dirs, e.Path)
		case e.Kind == 'f' && strings.HasPrefix(e.Path, "proj"):
			files = append(files, e.Path)
		}
	}
	var jobs []e2eJob
	pick := func(list []string) string { return list[r.Intn(len(list))] }
	root := filepath.Join(dir, "proj")
	for i := 0; i < 2; i++ {
		query := pick(pickerQueries)
		r2 := root
		if i == 1 {
			r2 = filepath.Join(dir, pick(dirs))
		}
		jobs = append(jobs, e2eJob{
			request: fmt.Sprintf("picker\t%s\t%s", r2, hexOf(query)),
			run:     func(t *testing.T) []string { return goPicker(t, r2, query) },
		})
	}
	for i := 0; i < 4; i++ {
		d := root
		if i%2 == 1 {
			d = filepath.Join(dir, pick(dirs))
		}
		showAll := r.Intn(2) == 0
		focus := ""
		if r.Intn(3) > 0 && len(files) > 0 {
			// Usually something that is in d, sometimes a hidden or ignored
			// entry, sometimes a path elsewhere.
			focus = filepath.Join(dir, pick(files))
			if r.Intn(2) == 0 {
				entries, _ := os.ReadDir(d)
				if len(entries) > 0 {
					focus = filepath.Join(d, entries[r.Intn(len(entries))].Name())
				}
			}
		}
		filter := ""
		if r.Intn(3) == 0 {
			filter = pick([]string{"a", "rs", "é", ".", "x", "log", "CF", "zz"})
		}
		jobs = append(jobs, e2eJob{
			request: fmt.Sprintf("explorer\t%s\t%d\t%s\t%s", d, map[bool]int{false: 0, true: 1}[showAll], focus, hexOf(filter)),
			run:     func(t *testing.T) []string { return goExplorer(t, d, showAll, focus, filter) },
		})
	}
	for i := 0; i < 2; i++ {
		query := pick(searchQueries)
		sr := root
		if i == 1 {
			sr = filepath.Join(dir, pick(dirs))
		}
		var active *search.Active
		activeArgs := "\t"
		if len(files) > 0 && r.Intn(2) == 0 {
			path := filepath.Join(dir, pick(files))
			text := pick([]string{"a😀 needle\r\nunsaved", "needle\n\nneedle needle", "", "x", strings.Repeat("y", 2*1024*1024+1), strings.Repeat("x", 2*1024*1024)})
			active = &search.Active{Path: path, Text: text}
			activeArgs = "\t" + path + "\t" + hexOf(text)
		}
		jobs = append(jobs, e2eJob{
			request: fmt.Sprintf("search\t%s\t%s%s", sr, hexOf(query), activeArgs),
			run:     func(t *testing.T) []string { return goSearch(t, sr, query, active) },
		})
	}
	return jobs
}

func rustModules(t *testing.T, bin string, jobs []e2eJob) [][]string {
	var in bytes.Buffer
	for _, j := range jobs {
		in.WriteString(j.request + "\n")
	}
	cmd := exec.Command(bin)
	cmd.Stdin = &in
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("reference program: %v\n%s", err, stderr.String())
	}
	var results [][]string
	sc := bufio.NewScanner(&out)
	sc.Buffer(nil, 64<<20)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "@@ ") {
			results = append(results, nil)
			continue
		}
		results[len(results)-1] = append(results[len(results)-1], sc.Text())
	}
	if len(results) != len(jobs) {
		t.Fatalf("%d results for %d jobs", len(results), len(jobs))
	}
	return results
}

// TestEndToEndAgainstRustModules is skipped unless COIGUE_MODULES names the
// scratch program.
func TestEndToEndAgainstRustModules(t *testing.T) {
	bin := os.Getenv("COIGUE_MODULES")
	if bin == "" {
		t.Skip("set COIGUE_MODULES to the scratch modules binary to run")
	}
	trees := envInt("COIGUE_E2E_TREES", 300)
	seed := int64(envInt("COIGUE_WALK_SEED", 1))
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if found := ancestorsHaveIgnoreFiles(base); found != "" {
		t.Skipf("%s would add rules to every walk", found)
	}
	const group = 40
	compared, mismatches := 0, 0
	counts := map[string]int{}
	for start := 0; start < trees && mismatches < 6; start += group {
		var jobs []e2eJob
		var owners []int
		var specs []treeSpec
		for i := start; i < start+group && i < trees; i++ {
			r := rand.New(rand.NewSource(seed*7_000_003 + int64(i)))
			spec := genTreeText(r)
			dir := filepath.Join(base, fmt.Sprintf("e%d", i))
			spec.build(t, dir)
			planned := planE2E(t, r, dir, spec)
			for range planned {
				owners = append(owners, len(specs))
			}
			specs = append(specs, spec)
			jobs = append(jobs, planned...)
		}
		want := rustModules(t, bin, jobs)
		for k, j := range jobs {
			got := j.run(t)
			compared++
			counts[strings.SplitN(j.request, "\t", 2)[0]]++
			if !equalLines(got, want[k]) {
				mismatches++
				t.Errorf("tree %d: %q differs\n%s\ntree:\n%s", start+owners[k], j.request, diffLines(want[k], got), describeSpec(specs[owners[k]]))
				if mismatches >= 6 {
					break
				}
			}
		}
	}
	t.Logf("%d trees (seed %d): %d picker / %d explorer / %d search runs compared, %d mismatches",
		trees, seed, counts["picker"], counts["explorer"], counts["search"], mismatches)
	_ = compared
}
