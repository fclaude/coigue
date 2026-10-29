package modsrc

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/fclaude/coigue/internal/ext"
)

const (
	// MaxFile bounds one module, as it bounds one loaded from disk.
	MaxFile = ext.MaxInitSize
	// MaxDownload bounds the compressed archive of a whole repository.
	MaxDownload = 32 << 20
	// MaxExpanded bounds what the archive may unpack to, whatever it holds.
	MaxExpanded = 128 << 20
)

// File is one module a repository offers: modules/Module.scm.
type File struct {
	Module string
	Data   []byte
}

// Sum is the hex SHA-256 of the file's bytes, the hash the lockfile records.
func (f File) Sum() string { return Sum(f.Data) }

// Sum is the hex SHA-256 of data.
func Sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

var errExpanded = fmt.Errorf("the archive unpacks to more than %d MiB", MaxExpanded>>20)

type cappedReader struct {
	r    io.Reader
	left int64
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, errExpanded
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}

// Extract reads a repository archive in the layout GitHub serves (a gzipped
// tar with the files under one top directory) and returns the modules in
// top/modules/NAME.scm, sorted by name. It reads them and nothing else: no
// path from the archive is ever used to write a file. Anything that is not a
// plain file where a module should be, a name that cannot be a module's, a
// second copy, too large a file or too many modules is an error that names it.
// A repository without modules returns none and no error.
func Extract(r io.Reader) ([]File, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("the download is not a gzip archive: %w", err)
	}
	defer zr.Close()
	tr := tar.NewReader(&cappedReader{r: zr, left: MaxExpanded})

	var (
		top   string
		files []File
		seen  = map[string]bool{}
	)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading the archive: %w", err)
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		parts := strings.Split(strings.TrimSuffix(h.Name, "/"), "/")
		for _, p := range parts {
			if p == "" || p == "." || p == ".." {
				return nil, fmt.Errorf("the archive holds a path that leaves its directory: %q", h.Name)
			}
		}
		if top == "" {
			top = parts[0]
		} else if parts[0] != top {
			return nil, fmt.Errorf("the archive has more than one top directory: %q and %q", top, parts[0])
		}
		if len(parts) != 3 || parts[1] != "modules" || !strings.HasSuffix(parts[2], ".scm") {
			continue
		}
		file := parts[1] + "/" + parts[2]
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("%s is not a regular file (a link or a special file)", file)
		}
		name := strings.TrimSuffix(parts[2], ".scm")
		if err := ext.CheckModuleName(name); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		if seen[name] {
			return nil, fmt.Errorf("%s appears twice in the archive", file)
		}
		if h.Size > MaxFile {
			return nil, fmt.Errorf("%s is larger than %d bytes", file, MaxFile)
		}
		if len(files) >= ext.MaxModules {
			return nil, fmt.Errorf("the repository has more than %d modules", ext.MaxModules)
		}
		data, err := io.ReadAll(io.LimitReader(tr, MaxFile+1))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", file, err)
		}
		if len(data) > MaxFile {
			return nil, fmt.Errorf("%s is larger than %d bytes", file, MaxFile)
		}
		seen[name] = true
		files = append(files, File{Module: name, Data: data})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Module < files[j].Module })
	return files, nil
}
