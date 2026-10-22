package navigation

import (
	"strconv"
	"testing"

	"github.com/fclaude/coigue/internal/key"
)

// The Rust module has no unit tests of its own (its behaviour is exercised
// through the editor tests); these pin the limits and orderings documented in
// docs/port-parity.md NAV-02.
func TestRecentFilesAreMostRecentFirstUniqueAndLimited(t *testing.T) {
	var h History
	for i := 0; i < 60; i++ {
		h.Visit(Location{Path: "/f" + strconv.Itoa(i)})
	}
	h.Visit(Location{Path: "/f30", Row: 7})
	if len(h.Recent) != 50 || h.Recent[0].Path != "/f30" || h.Recent[0].Row != 7 {
		t.Fatalf("recent = %d entries, first %+v", len(h.Recent), h.Recent[0])
	}
	seen := map[string]bool{}
	for _, l := range h.Recent {
		if seen[l.Path] {
			t.Fatalf("duplicate %s", l.Path)
		}
		seen[l.Path] = true
	}
}

func TestBackAndForwardStacksFollowArrivals(t *testing.T) {
	var h History
	a, b, c := Location{Path: "/a"}, Location{Path: "/b"}, Location{Path: "/c"}
	h.Arrived(&a, b, New)
	h.Arrived(&b, c, New)
	if d, ok := h.Destination(Back); !ok || d.Path != "/b" {
		t.Fatalf("back = %+v %v", d, ok)
	}
	h.Arrived(&c, b, Back)
	if d, _ := h.Destination(Forward); d.Path != "/c" {
		t.Fatalf("forward = %+v", d)
	}
	h.Arrived(&b, Location{Path: "/d"}, New)
	if _, ok := h.Destination(Forward); ok {
		t.Fatal("a new jump kept forward history")
	}
	for i := 0; i < 150; i++ {
		x := Location{Path: "/x", Row: i}
		h.Depart(x)
	}
	if len(h.back) != 100 {
		t.Fatalf("back stack = %d", len(h.back))
	}
}

func TestRenameRewritesEveryRememberedPathBelowTheSource(t *testing.T) {
	var h History
	h.Arrived(&Location{Path: "/old/dir/a"}, Location{Path: "/old/dir/b"}, New)
	h.Visit(Location{Path: "/older"})
	h.Rename("/old/dir", "/new")
	if h.Recent[len(h.Recent)-1].Path == "" {
		t.Fatal("empty path")
	}
	for _, l := range h.Recent {
		if l.Path == "/old/dir/a" || l.Path == "/old/dir/b" {
			t.Fatalf("stale path %s", l.Path)
		}
	}
	if d, _ := h.Destination(Back); d.Path != "/new/a" {
		t.Fatalf("back = %s", d.Path)
	}
}

func TestListFiltersByWordsAndKeysMoveSelection(t *testing.T) {
	l := NewList("Recent files", []Target{
		{Label: "/a/main.rs:1:1"}, {Label: "/b/Main.go:2:2"}, {Label: "/c/other.txt:3:3"},
	})
	for _, r := range "MAIN go" {
		l.Key(key.Code(r), 5)
	}
	if got := l.Matches(); len(got) != 1 || got[0].Label != "/b/Main.go:2:2" {
		t.Fatalf("matches = %v", got)
	}
	l.Key(key.Backspace, 5)
	l.Key(key.Backspace, 5)
	l.Key(key.Backspace, 5)
	if len(l.Matches()) != 2 {
		t.Fatalf("matches = %d", len(l.Matches()))
	}
	l.Key(key.Down, 5)
	l.Key(key.Down, 5)
	if l.Selected != 1 {
		t.Fatalf("selection past the end: %d", l.Selected)
	}
	loc, ok := l.Key(key.Enter, 5)
	if !ok || loc.Path != "" {
		t.Fatalf("enter = %+v %v", loc, ok)
	}
	live := NewList("Project search", []Target{{Label: "x"}, {Label: "y"}})
	live.Source = Project
	live.Query = "nothing"
	if len(live.Matches()) != 2 {
		t.Fatal("live lists are not filtered locally")
	}
}
