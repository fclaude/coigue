package clipboard

import (
	"errors"
	"io/fs"
	"os"
	"slices"
	"testing"
)

// Rust: clipboard::tests::desktop_choice_is_explicit_and_never_uses_terminal_export
func TestDesktopChoiceIsExplicitAndNeverUsesTerminalExport(t *testing.T) {
	if len(backends(false, false, false)) != 0 {
		t.Error("no display must mean no backend")
	}
	if got := backends(true, false, false)[0].copy; got != "pbcopy" {
		t.Errorf("macOS copy = %q", got)
	}
	both := backends(false, true, true)
	var copies []string
	for _, b := range both {
		copies = append(copies, b.copy)
	}
	if !slices.Equal(copies, []string{"wl-copy", "xclip", "xsel"}) {
		t.Errorf("copy programs = %q", copies)
	}
	if !slices.Contains(both[0].pasteArgs, "--no-newline") {
		t.Errorf("wl-paste arguments = %q", both[0].pasteArgs)
	}
}

func TestBackendTables(t *testing.T) {
	wayland := Backend{copy: "wl-copy", copyArgs: []string{"--type", "text/plain;charset=utf-8"}, paste: "wl-paste", pasteArgs: []string{"--no-newline", "--type", "text"}}
	xclip := Backend{copy: "xclip", copyArgs: []string{"-selection", "clipboard", "-in"}, paste: "xclip", pasteArgs: []string{"-selection", "clipboard", "-out"}}
	xsel := Backend{copy: "xsel", copyArgs: []string{"--clipboard", "--input"}, paste: "xsel", pasteArgs: []string{"--clipboard", "--output"}}
	mac := Backend{copy: "pbcopy", paste: "pbpaste"}
	for _, c := range []struct {
		macos, wayland, x11 bool
		want                []Backend
	}{
		{false, false, false, nil},
		{false, true, false, []Backend{wayland}},
		{false, false, true, []Backend{xclip, xsel}},
		{false, true, true, []Backend{wayland, xclip, xsel}},
		{true, false, false, []Backend{mac}},
		// macOS never looks at the display variables.
		{true, true, true, []Backend{mac}},
	} {
		got := backends(c.macos, c.wayland, c.x11)
		if len(got) != len(c.want) {
			t.Errorf("backends(%v, %v, %v) = %+v, want %+v", c.macos, c.wayland, c.x11, got, c.want)
			continue
		}
		for i := range got {
			if !backendEqual(got[i], c.want[i]) {
				t.Errorf("backends(%v, %v, %v)[%d] = %+v, want %+v", c.macos, c.wayland, c.x11, i, got[i], c.want[i])
			}
		}
	}
	// The result is built fresh: callers cannot corrupt what the next call returns.
	backends(false, true, false)[0].copyArgs[0] = "changed"
	if backends(false, true, false)[0].copyArgs[0] != "--type" {
		t.Error("backends shares its argument slices between calls")
	}
}

func backendEqual(a, b Backend) bool {
	return a.copy == b.copy && a.paste == b.paste &&
		slices.Equal(a.copyArgs, b.copyArgs) && slices.Equal(a.pasteArgs, b.pasteArgs)
}

func TestTimedOutErrorKind(t *testing.T) {
	var err error = timedOutError{}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Error("timedOutError is not os.ErrDeadlineExceeded")
	}
	var timeout interface{ Timeout() bool }
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Error("timedOutError does not report Timeout()")
	}
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
		t.Error("timedOutError matches unrelated kinds")
	}
}
