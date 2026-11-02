//go:build windows

package fsx

import (
	"reflect"
	"testing"
)

func TestWindowsPathComponentsAndJoin(t *testing.T) {
	for _, tc := range []struct {
		path               string
		components         []string
		parent             string
		parentOK, absolute bool
	}{
		{`C:\Users\alice\..\file.txt`, []string{`C:`, `\`, `Users`, `alice`, `..`, `file.txt`}, `C:\Users\alice\..`, true, true},
		{`C:\`, []string{`C:`, `\`}, "", false, true},
		{`\\server\share`, []string{`\\server\share`, `\`}, "", false, true},
		{`\\server\share\dir`, []string{`\\server\share`, `\`, `dir`}, `\\server\share\`, true, true},
		{`C:relative\file`, []string{`C:`, `relative`, `file`}, `C:relative`, true, false},
		{`\rooted\file`, []string{`\`, `rooted`, `file`}, `\rooted`, true, false},
		{`C:/a//./b`, []string{`C:`, `\`, `a`, `b`}, `C:/a`, true, true},
	} {
		if got := Components(tc.path); !reflect.DeepEqual(got, tc.components) {
			t.Errorf("Components(%q) = %q, want %q", tc.path, got, tc.components)
		}
		if got, ok := Parent(tc.path); got != tc.parent || ok != tc.parentOK {
			t.Errorf("Parent(%q) = %q, %v", tc.path, got, ok)
		}
		if got := IsAbsolute(tc.path); got != tc.absolute {
			t.Errorf("IsAbsolute(%q) = %v", tc.path, got)
		}
	}
	for _, tc := range []struct{ base, part, want string }{
		{`C:\a`, `b\..\c`, `C:\a\b\..\c`},
		{`C:\a`, `D:\b`, `D:\b`},
		{`C:\a`, `\b`, `C:\b`},
		{`C:`, `b`, `C:b`},
	} {
		if got := Join(tc.base, tc.part); got != tc.want {
			t.Errorf("Join(%q, %q) = %q, want %q", tc.base, tc.part, got, tc.want)
		}
	}
	if rest, ok := StripPrefix(`C:\a\b`, `C:/a`); !ok || rest != `b` {
		t.Errorf("StripPrefix = %q, %v", rest, ok)
	}
}
