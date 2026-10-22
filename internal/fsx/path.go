// Package fsx provides path helpers with the component-wise semantics of
// Rust's std::path::Path, which the editor was written against. Go's
// path/filepath cleans paths lexically (resolving "..", rewriting "a/../b"),
// whereas Rust only compares and splits components; the two differ for
// non-canonical or not-yet-existing paths, so the Rust behaviour is kept.
package fsx

import (
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const sep = "/"

// Components splits a path the way Path::components does: a leading "/" is a
// root component, "." survives only at the start, repeated separators collapse,
// and ".." is kept as a component.
func Components(p string) []string {
	var out []string
	rooted := strings.HasPrefix(p, sep)
	if rooted {
		out = append(out, sep)
	}
	for i, part := range strings.Split(p, sep) {
		switch part {
		case "":
		case ".":
			if i == 0 && !rooted {
				out = append(out, ".")
			}
		default:
			out = append(out, part)
		}
	}
	return out
}

func isNormal(c string) bool { return c != sep && c != "." && c != ".." }

// Join mirrors Path::join: an absolute operand replaces the base, an empty
// base yields the operand, and nothing is cleaned.
func Join(base string, parts ...string) string {
	for _, part := range parts {
		switch {
		case strings.HasPrefix(part, sep):
			base = part
		case base == "":
			base = part
		case strings.HasSuffix(base, sep):
			base += part
		default:
			base += sep + part
		}
	}
	return base
}

// IsAbsolute reports whether the path starts at the filesystem root.
func IsAbsolute(p string) bool { return strings.HasPrefix(p, sep) }

// componentEnds returns, for every component of p (see Components), the byte
// offset in p just after it. Rust's Path::parent hands back a prefix of the
// original spelling, so the offsets are needed rather than the component text.
func componentEnds(p string) (ends []int, rooted bool) {
	n := len(p)
	i := 0
	if n > 0 && p[0] == '/' {
		rooted = true
		ends = append(ends, 1)
		for i < n && p[i] == '/' {
			i++
		}
	}
	for first := true; i < n; first = false {
		j := i
		for j < n && p[j] != '/' {
			j++
		}
		if seg := p[i:j]; seg != "." || (first && !rooted) {
			ends = append(ends, j)
		}
		for i = j; i < n && p[i] == '/'; i++ {
		}
	}
	return ends, rooted
}

// Parent mirrors Path::parent: the path without its last component, in the
// original spelling ("/a/./b/c" has the parent "/a/./b"). The root and the
// empty path have no parent; a single relative component has the empty path.
func Parent(p string) (string, bool) {
	ends, rooted := componentEnds(p)
	if len(ends) == 0 || (rooted && len(ends) == 1) {
		return "", false
	}
	if len(ends) == 1 {
		return "", true
	}
	return p[:ends[len(ends)-2]], true
}

// FileName mirrors Path::file_name: the last component, if it is a normal one.
func FileName(p string) (string, bool) {
	comps := Components(p)
	if len(comps) == 0 {
		return "", false
	}
	last := comps[len(comps)-1]
	if !isNormal(last) {
		return "", false
	}
	return last, true
}

// Extension mirrors Path::extension: the text after the last dot of the file
// name, absent for names without a dot or whose only dot starts the name.
func Extension(p string) (string, bool) {
	name, ok := FileName(p)
	if !ok {
		return "", false
	}
	idx := strings.LastIndex(name, ".")
	if idx <= 0 {
		return "", false
	}
	return name[idx+1:], true
}

// StripPrefix mirrors Path::strip_prefix, comparing whole components. The
// remainder is relative and may be empty.
func StripPrefix(p, prefix string) (string, bool) {
	pc, fc := Components(p), Components(prefix)
	if len(fc) > len(pc) {
		return "", false
	}
	for i := range fc {
		if pc[i] != fc[i] {
			return "", false
		}
	}
	return strings.Join(pc[len(fc):], sep), true
}

// StartsWith mirrors Path::starts_with.
func StartsWith(p, prefix string) bool {
	_, ok := StripPrefix(p, prefix)
	return ok
}

// EndsWith mirrors Path::ends_with.
func EndsWith(p, suffix string) bool {
	pc, sc := Components(p), Components(suffix)
	if len(sc) > len(pc) {
		return false
	}
	off := len(pc) - len(sc)
	for i := range sc {
		if pc[off+i] != sc[i] {
			return false
		}
	}
	return true
}

// HasComponent reports whether any component equals name.
func HasComponent(p, name string) bool {
	for _, c := range Components(p) {
		if c == name {
			return true
		}
	}
	return false
}

// Absolute mirrors std::path::absolute on Unix: a relative path is joined to
// the current directory, "." components and repeated separators disappear, ".."
// is preserved, a trailing separator and a leading "//" (but not "///") are
// kept. Symlinks are not resolved and the path need not exist.
func Absolute(p string) (string, error) {
	if p == "" {
		return "", &os.PathError{Op: "absolute", Path: p, Err: os.ErrInvalid}
	}
	if !IsAbsolute(p) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		p = Join(cwd, p)
	}
	lead := sep
	if strings.HasPrefix(p, "//") && !strings.HasPrefix(p, "///") {
		lead = "//"
	}
	body := strings.Join(Components(p)[1:], sep)
	if body != "" && strings.HasSuffix(p, sep) {
		body += sep
	}
	return lead + body, nil
}

// Canonicalize mirrors fs::canonicalize: the absolute, symlink-free path of an
// existing file or directory.
func Canonicalize(p string) (string, error) {
	if p == "" {
		return "", &os.PathError{Op: "canonicalize", Path: p, Err: os.ErrNotExist}
	}
	abs := p
	if !IsAbsolute(abs) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		abs = Join(cwd, abs)
	}
	return filepath.EvalSymlinks(abs)
}

// Exists mirrors Path::exists: true when the path resolves (following links).
func Exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Display mirrors Path::display: the path as text, with each maximal invalid
// UTF-8 subsequence replaced by one U+FFFD, as Rust's lossy conversion does.
func Display(p string) string {
	if utf8.ValidString(p) {
		return p
	}
	var b strings.Builder
	for i := 0; i < len(p); {
		r, size := utf8.DecodeRuneInString(p[i:])
		if r != utf8.RuneError || size > 1 {
			b.WriteString(p[i : i+size])
			i += size
			continue
		}
		b.WriteRune(utf8.RuneError)
		i += invalidPrefix(p[i:])
	}
	return b.String()
}

// invalidPrefix is the length of the longest prefix of s that starts a valid
// UTF-8 sequence but is truncated or interrupted, or 1 for a stray byte.
func invalidPrefix(s string) int {
	lead := s[0]
	var need int
	lo, hi := byte(0x80), byte(0xBF)
	switch {
	case lead >= 0xC2 && lead <= 0xDF:
		need = 1
	case lead >= 0xE0 && lead <= 0xEF:
		need = 2
		switch lead {
		case 0xE0:
			lo = 0xA0
		case 0xED:
			hi = 0x9F
		}
	case lead >= 0xF0 && lead <= 0xF4:
		need = 3
		switch lead {
		case 0xF0:
			lo = 0x90
		case 0xF4:
			hi = 0x8F
		}
	default:
		return 1
	}
	n := 1
	for ; n <= need && n < len(s); n++ {
		c := s[n]
		if n == 1 && (c < lo || c > hi) || n > 1 && (c < 0x80 || c > 0xBF) {
			break
		}
	}
	return n
}
