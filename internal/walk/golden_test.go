package walk_test

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The golden file records, for a few hundred random trees, what the real Rust
// `ignore` crate lists under the editor's picker / explorer / search
// configurations. TestGoldenListings replays it against the Go walker on every
// run, without needing Rust. TestWriteGolden regenerates it
// (COIGUE_WALK_GOLDEN_OUT=<file> COIGUE_WALKLIST=<reference binary>).
//
// Format, one record per line:
//
//	tree <n>
//	d|f|l|p <quoted path> [<quoted content or link target>]   an entry of the tree
//	run <mode> <kind> <quoted rel> <quoted root> <quoted cwd rel>
//	E <kind> <depth> <e|-> <path>                              expected listing lines
//	X <n>

const goldenFile = "testdata/golden.txt"

func goldenJobs(r *rand.Rand, spec treeSpec) []walkJob {
	planned := planJobs(r, spec)
	var jobs []walkJob
	proj := map[walkMode]bool{}
	extra := 0
	for _, j := range planned {
		switch {
		case j.Kind == jobAbs && j.Rel == "proj" && j.Mode != modeSearch:
			if !proj[j.Mode] {
				proj[j.Mode] = true
				jobs = append(jobs, j)
			}
		case j.Kind == jobAbs && j.Rel == "proj" && r.Intn(3) == 0:
			jobs = append(jobs, j)
		case j.Kind == jobAbs && j.Rel == "":
			if j.Mode == modeExplorerHidden {
				jobs = append(jobs, j)
			}
		case j.Kind == jobAbs && j.Mode != modeSearch && j.Mode != modeExplorerAll && extra < 2:
			extra++
			jobs = append(jobs, j)
		case j.Kind != jobAbs && j.Mode == modePicker:
			jobs = append(jobs, j)
		case j.Kind == jobRel && j.Mode == modeExplorerHidden:
			jobs = append(jobs, j)
		}
	}
	return jobs
}

func quote(s string) string { return strconv.Quote(s) }

func writeSpec(w *bufio.Writer, spec treeSpec) {
	for _, e := range spec {
		switch e.Kind {
		case 'f':
			if e.Content != "" {
				fmt.Fprintf(w, "f %s %s\n", quote(e.Path), quote(e.Content))
			} else {
				fmt.Fprintf(w, "f %s\n", quote(e.Path))
			}
		case 'l':
			fmt.Fprintf(w, "l %s %s\n", quote(e.Path), quote(e.Target))
		default:
			fmt.Fprintf(w, "%c %s\n", e.Kind, quote(e.Path))
		}
	}
}

func TestWriteGolden(t *testing.T) {
	out, bin := os.Getenv("COIGUE_WALK_GOLDEN_OUT"), os.Getenv("COIGUE_WALKLIST")
	if out == "" || bin == "" {
		t.Skip("set COIGUE_WALK_GOLDEN_OUT and COIGUE_WALKLIST to regenerate the golden file")
	}
	wantTrees := envInt("COIGUE_WALK_GOLDEN_TREES", 300)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if found := ancestorsHaveIgnoreFiles(base); found != "" {
		t.Skipf("%s would add rules to every walk", found)
	}
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()
	fmt.Fprintln(w, "# Listings of random trees by the Rust `ignore` crate (see golden_test.go for the format).")
	chosen := 0
	var withErrors, withEntryErrors, withSymlinkRoot int
	for i := 0; chosen < wantTrees && i < 100000; i++ {
		r := rand.New(rand.NewSource(4242_000 + int64(i)))
		spec := genTree(r)
		unreadable := false
		for _, e := range spec {
			if e.Mode != 0 {
				unreadable = true
			}
		}
		if unreadable || len(spec) > 34 {
			continue
		}
		dir := filepath.Join(base, fmt.Sprintf("w%d", chosen))
		spec.build(t, dir)
		jobs := goldenJobs(r, spec)
		dirs := make([]string, len(jobs))
		for k := range dirs {
			dirs[k] = dir
		}
		results := rustListings(t, bin, jobs, dirs)
		fmt.Fprintf(w, "tree %d\n", chosen)
		writeSpec(w, spec)
		for k, j := range jobs {
			fmt.Fprintf(w, "run %s %s %s %s %s\n", j.Mode, j.Kind, quote(j.Rel), quote(j.Root), quote(j.CwdRel))
			for _, line := range results[k] {
				fmt.Fprintln(w, line)
				if line != "X 0" && strings.HasPrefix(line, "X ") {
					withErrors++
				}
				if strings.HasPrefix(line, "E ") && len(line) > 6 && line[6] == 'e' {
					withEntryErrors++
				}
			}
			if j.Kind == jobLink {
				withSymlinkRoot++
			}
		}
		chosen++
	}
	t.Logf("wrote %d trees; %d listings with walk errors, %d entries flagged with ignore-file errors, %d symlink roots", chosen, withErrors, withEntryErrors, withSymlinkRoot)
}

type goldenRun struct {
	job  walkJob
	want []string
}

type goldenTree struct {
	n    string
	spec treeSpec
	runs []goldenRun
}

func parseGolden(t *testing.T, path string) []goldenTree {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var trees []goldenTree
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	unquoteAll := func(s string) []string {
		var out []string
		for s = strings.TrimSpace(s); s != ""; s = strings.TrimSpace(s) {
			q, err := strconv.QuotedPrefix(s)
			if err != nil {
				t.Fatalf("bad quoting in %q", s)
			}
			u, err := strconv.Unquote(q)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, u)
			s = s[len(q):]
		}
		return out
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "#") || line == "":
		case strings.HasPrefix(line, "tree "):
			trees = append(trees, goldenTree{n: line[5:]})
		case strings.HasPrefix(line, "run "):
			fields := strings.SplitN(line, " ", 4)
			args := unquoteAll(fields[3])
			tr := &trees[len(trees)-1]
			tr.runs = append(tr.runs, goldenRun{job: walkJob{Mode: walkMode(fields[1]), Kind: jobKind(fields[2]), Rel: args[0], Root: args[1], CwdRel: args[2]}})
		case strings.HasPrefix(line, "E ") || strings.HasPrefix(line, "X "):
			tr := &trees[len(trees)-1]
			run := &tr.runs[len(tr.runs)-1]
			run.want = append(run.want, line)
		default:
			args := unquoteAll(line[2:])
			e := specEntry{Kind: line[0], Path: args[0]}
			switch line[0] {
			case 'f':
				if len(args) > 1 {
					e.Content = args[1]
				}
			case 'l':
				e.Target = args[1]
			}
			tr := &trees[len(trees)-1]
			tr.spec = append(tr.spec, e)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return trees
}

func TestGoldenListings(t *testing.T) {
	trees := parseGolden(t, goldenFile)
	if len(trees) < 200 {
		t.Fatalf("the golden file holds only %d trees", len(trees))
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if found := ancestorsHaveIgnoreFiles(base); found != "" {
		// The walks read the ignore files of every ancestor directory; the
		// recorded results assume there are none.
		t.Skipf("%s would add rules to every walk", found)
	}
	runs := 0
	for i, tree := range trees {
		dir := filepath.Join(base, fmt.Sprintf("g%d", i))
		tree.spec.build(t, dir)
		for _, run := range tree.runs {
			runs++
			got := run.job.goListing(t, dir)
			if !equalLines(got, run.want) {
				t.Errorf("tree %s: %+v\n%s\ntree:\n%s", tree.n, run.job, diffLines(run.want, got), describeSpec(tree.spec))
			}
		}
	}
	t.Logf("%d trees, %d listings replayed", len(trees), runs)
}
