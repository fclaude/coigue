//go:build windows

package lsp

import "testing"

func TestWindowsFileURLRoundTrips(t *testing.T) {
	for _, tc := range []struct{ path, uri string }{
		{`C:\Users\A B\界 #.rs`, `file:///C:/Users/A%20B/%E7%95%8C%20%23.rs`},
		{`C:\`, `file:///C:/`},
		{`\\server\share\a b.rs`, `file://server/share/a%20b.rs`},
		{`\\192.168.1.5\share\file.rs`, `file://192.168.1.5/share/file.rs`},
		{`\\localhost\share\file.rs`, `file://localhost/share/file.rs`},
		{`\\høst\share\file.rs`, `file://høst/share/file.rs`},
	} {
		got, ok := fileURI(tc.path)
		if !ok || got != tc.uri {
			t.Errorf("fileURI(%q) = %q, %v, want %q", tc.path, got, ok, tc.uri)
		}
		got, ok = uriToPath(tc.uri)
		if !ok || got != tc.path {
			t.Errorf("uriToPath(%q) = %q, %v, want %q", tc.uri, got, ok, tc.path)
		}
	}
}
