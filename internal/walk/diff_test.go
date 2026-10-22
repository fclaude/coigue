package walk_test

import (
	"bufio"
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// jobKind says how a job's root is spelled.
type jobKind string

const (
	jobAbs   jobKind = "abs"   // the directory Rel of the tree, absolute
	jobSlash jobKind = "slash" // the same with a trailing slash
	jobLink  jobKind = "link"  // a symbolic link, outside the tree, to Rel
	jobRel   jobKind = "rel"   // the relative path Root with the process in CwdRel
)

// A walkJob is one listing of a tree: a root in one mode.
type walkJob struct {
	Mode   walkMode
	Kind   jobKind
	Rel    string
	Root   string
	CwdRel string
}

// resolve gives the root and working directory (empty when unchanged) for a
// tree built at dir, creating the symlink of a link job.
func (j walkJob) resolve(dir string) (root, cwd string) {
	switch j.Kind {
	case jobSlash:
		return filepath.Join(dir, j.Rel) + "/", ""
	case jobLink:
		link := dir + "-link"
		_ = os.Symlink(filepath.Join(dir, j.Rel), link)
		return link, ""
	case jobRel:
		return j.Root, filepath.Join(dir, j.CwdRel)
	}
	return filepath.Join(dir, j.Rel), ""
}

// goListing runs the job with the Go walker.
func (j walkJob) goListing(t testing.TB, dir string) []string {
	root, cwd := j.resolve(dir)
	if cwd != "" {
		old, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(cwd); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := os.Chdir(old); err != nil {
				t.Fatal(err)
			}
		}()
	}
	return sortedListing(j.Mode, root)
}

// planJobs chooses the roots to list for a tree (which holds "proj"). The
// choice is random but deterministic for the generator state.
func planJobs(r *rand.Rand, spec treeSpec) []walkJob {
	var dirs []string
	for _, e := range spec {
		if e.Kind == 'd' && e.Mode == 0 {
			dirs = append(dirs, e.Path)
		}
	}
	all := []walkMode{modePicker, modeSearch, modeExplorerHidden, modeExplorerAll}
	var jobs []walkJob
	for _, m := range all {
		jobs = append(jobs, walkJob{Mode: m, Kind: jobAbs, Rel: "proj"})
	}
	jobs = append(jobs, walkJob{Mode: modePicker, Kind: jobAbs}, walkJob{Mode: modeExplorerHidden, Kind: jobAbs})
	for i := 1 + r.Intn(2); i > 0; i-- {
		d := dirs[r.Intn(len(dirs))]
		for _, m := range all {
			if m == modeSearch && r.Intn(2) == 0 {
				continue
			}
			jobs = append(jobs, walkJob{Mode: m, Kind: jobAbs, Rel: d})
		}
	}
	if r.Intn(4) == 0 {
		d := dirs[r.Intn(len(dirs))]
		jobs = append(jobs, walkJob{Mode: modePicker, Kind: jobLink, Rel: d}, walkJob{Mode: modeExplorerHidden, Kind: jobLink, Rel: d})
	}
	if r.Intn(10) == 0 {
		jobs = append(jobs, walkJob{Mode: modePicker, Kind: jobSlash, Rel: "proj"}, walkJob{Mode: modeExplorerHidden, Kind: jobSlash, Rel: "proj"})
	}
	if r.Intn(7) == 0 {
		d := dirs[r.Intn(len(dirs))]
		jobs = append(jobs, walkJob{Mode: modePicker, Kind: jobRel, Root: d},
			walkJob{Mode: modeExplorerHidden, Kind: jobRel, Root: d},
			walkJob{Mode: modePicker, Kind: jobRel, Root: ".", CwdRel: d},
			walkJob{Mode: modeExplorerHidden, Kind: jobRel, Root: ".", CwdRel: d})
	}
	return jobs
}

// rustListings runs the reference program over the jobs of the trees built at
// dirs (jobs[i] belongs to dirs[i]).
func rustListings(t testing.TB, bin string, jobs []walkJob, dirs []string) [][]string {
	var in bytes.Buffer
	for i, j := range jobs {
		root, cwd := j.resolve(dirs[i])
		fmt.Fprintf(&in, "%s\t%s\t%s\n", j.Mode, root, cwd)
	}
	cmd := exec.Command(bin)
	cmd.Stdin = &in
	var out bytes.Buffer
	cmd.Stdout = &out
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("reference program: %v\n%s", err, stderr.String())
	}
	var results [][]string
	sc := bufio.NewScanner(&out)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "@@ ") {
			results = append(results, nil)
			continue
		}
		results[len(results)-1] = append(results[len(results)-1], line)
	}
	if len(results) != len(jobs) {
		t.Fatalf("reference program returned %d results for %d jobs", len(results), len(jobs))
	}
	for i, j := range jobs {
		if j.Mode != modeSearch {
			sortStrings(results[i])
		}
	}
	return results
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for k := i; k > 0 && s[k] < s[k-1]; k-- {
			s[k], s[k-1] = s[k-1], s[k]
		}
	}
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil {
		return v
	}
	return def
}

// ancestorsHaveIgnoreFiles reports whether a directory above dir would
// contribute ignore rules: the parents rule makes the walk read them.
func ancestorsHaveIgnoreFiles(dir string) string {
	for d := filepath.Dir(dir); ; d = filepath.Dir(d) {
		for _, name := range []string{".gitignore", ".git/info/exclude"} {
			if _, err := os.Stat(filepath.Join(d, name)); err == nil {
				return filepath.Join(d, name)
			}
		}
		if d == filepath.Dir(d) {
			return ""
		}
	}
}

// TestDifferentialAgainstRustIgnoreCrate lists thousands of random trees with
// the real `ignore` crate (a scratch program configured exactly as the editor
// configures it) and with this package, and requires identical results. It is
// skipped unless COIGUE_WALKLIST names the reference binary, because the
// binary is not part of the repository; the golden test replays recorded
// results of the same comparison.
func TestDifferentialAgainstRustIgnoreCrate(t *testing.T) {
	bin := os.Getenv("COIGUE_WALKLIST")
	if bin == "" {
		t.Skip("set COIGUE_WALKLIST to the walklist reference binary to run")
	}
	trees := envInt("COIGUE_WALK_TREES", 2000)
	seed := int64(envInt("COIGUE_WALK_SEED", 1))
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if found := ancestorsHaveIgnoreFiles(base); found != "" {
		t.Skipf("%s would add rules to every walk", found)
	}
	const group = 100
	mismatches, jobsRun := 0, 0
	var entryLines, errorFlagged, withErrors, symlinks int
	for start := 0; start < trees && mismatches < 8; start += group {
		var jobs []walkJob
		var specs []treeSpec
		var owners []int
		var dirs []string
		for i := start; i < start+group && i < trees; i++ {
			r := rand.New(rand.NewSource(seed*1_000_003 + int64(i)))
			spec := genTree(r)
			dir := filepath.Join(base, fmt.Sprintf("t%d", i))
			spec.build(t, dir)
			planned := planJobs(r, spec)
			for range planned {
				owners = append(owners, len(specs))
				dirs = append(dirs, dir)
			}
			specs = append(specs, spec)
			jobs = append(jobs, planned...)
		}
		want := rustListings(t, bin, jobs, dirs)
		for k, j := range jobs {
			got := j.goListing(t, dirs[k])
			jobsRun++
			for _, l := range want[k] {
				switch {
				case strings.HasPrefix(l, "E l "):
					symlinks++
					entryLines++
				case strings.HasPrefix(l, "E ") && strings.HasPrefix(l[4:], ""):
					entryLines++
					if len(l) > 6 && l[6] == 'e' {
						errorFlagged++
					}
				case l != "X 0":
					withErrors++
				}
			}
			if !equalLines(got, want[k]) {
				mismatches++
				t.Errorf("tree %d: %+v differs\n%s\ntree:\n%s", start+owners[k], j,
					diffLines(want[k], got), describeSpec(specs[owners[k]]))
				if mismatches >= 8 {
					break
				}
			}
		}
	}
	t.Logf("%d trees (seed %d), %d listings compared, %d mismatches (%d entries, %d symlinks, %d entries with ignore-file errors, %d listings with read errors)",
		trees, seed, jobsRun, mismatches, entryLines, symlinks, errorFlagged, withErrors)
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func diffLines(rust, got []string) string {
	var b strings.Builder
	inGot := map[string]int{}
	for _, l := range got {
		inGot[l]++
	}
	inRust := map[string]int{}
	for _, l := range rust {
		inRust[l]++
	}
	for _, l := range rust {
		if inGot[l] == 0 {
			fmt.Fprintf(&b, "  rust only: %s\n", l)
		}
	}
	for _, l := range got {
		if inRust[l] == 0 {
			fmt.Fprintf(&b, "  go only:   %s\n", l)
		}
	}
	if b.Len() == 0 {
		b.WriteString("  same lines in a different order\n")
	}
	return b.String()
}

func describeSpec(spec treeSpec) string {
	var b strings.Builder
	for _, e := range spec {
		switch e.Kind {
		case 'f':
			if e.Content != "" {
				fmt.Fprintf(&b, "  f %s %q\n", e.Path, e.Content)
			} else {
				fmt.Fprintf(&b, "  f %s\n", e.Path)
			}
		case 'l':
			fmt.Fprintf(&b, "  l %s -> %s\n", e.Path, e.Target)
		default:
			fmt.Fprintf(&b, "  %c %s\n", e.Kind, e.Path)
		}
	}
	return b.String()
}

// TestRealTreesAgainstRust lists existing directory trees (COIGUE_REAL_ROOTS,
// colon separated) with the crate and with the port, as a check on real-world
// ignore files.
func TestRealTreesAgainstRust(t *testing.T) {
	bin, roots := os.Getenv("COIGUE_WALKLIST"), os.Getenv("COIGUE_REAL_ROOTS")
	if bin == "" || roots == "" {
		t.Skip("set COIGUE_WALKLIST and COIGUE_REAL_ROOTS to run")
	}
	for _, root := range strings.Split(roots, ":") {
		for _, mode := range []walkMode{modePicker, modeSearch, modeExplorerHidden, modeExplorerAll} {
			t.Run(string(mode)+" "+root, func(t *testing.T) {
				job := walkJob{Mode: mode, Kind: jobRel, Root: root}
				start := time.Now()
				want := rustListings(t, bin, []walkJob{job}, []string{""})[0]
				rustTime := time.Since(start)
				start = time.Now()
				got := sortedListing(mode, root)
				goTime := time.Since(start)
				if !equalLines(got, want) {
					t.Errorf("listings differ\n%s", diffLines(want, got))
				}
				t.Logf("%d lines; crate %v, port %v", len(got), rustTime.Round(time.Millisecond), goTime.Round(time.Millisecond))
			})
		}
	}
}
