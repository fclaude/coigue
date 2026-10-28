package ext

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// InitPath is where the user's init file lives: coigue/init.scm under
// $XDG_CONFIG_HOME when that is an absolute path, else under ~/.config. It is
// the only place Scheme is ever loaded from. ok is false when the home
// directory is unknown.
func InitPath(getenv func(string) string, homeDir func() (string, error)) (path string, ok bool) {
	if dir := getenv("XDG_CONFIG_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "coigue", "init.scm"), true
	}
	home, err := homeDir()
	if err != nil || !filepath.IsAbs(home) {
		return "", false
	}
	return filepath.Join(home, ".config", "coigue", "init.scm"), true
}

// ReadInit reads the init file. A file that does not exist is normal and
// reported as found == false with no error.
func ReadInit(path string) (source string, found bool, err error) {
	return readScheme(path)
}

// ReadModule reads modules/NAME.scm under the configuration directory, the
// only place a module is ever loaded from. name has already been checked, so
// it cannot name a path outside that directory.
func ReadModule(configDir, name string) (string, error) {
	if err := checkModuleName(name); err != nil {
		return "", err
	}
	path := filepath.Join(configDir, "modules", name+".scm")
	source, found, err := readScheme(path)
	if err != nil {
		return "", fmt.Errorf("module %s: %s", name, err.Error())
	}
	if !found {
		return "", fmt.Errorf("module %s: not found at %s", name, path)
	}
	return source, nil
}

// readScheme reads one Scheme file with the init file's rules: a missing file
// is found == false, anything else must be a regular file of at most
// MaxInitSize bytes.
func readScheme(path string) (source string, found bool, err error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", true, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", true, err
	}
	if !info.Mode().IsRegular() {
		return "", true, errors.New("not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxInitSize+1))
	if err != nil {
		return "", true, err
	}
	if len(data) > MaxInitSize {
		return "", true, fmt.Errorf("larger than %d bytes", MaxInitSize)
	}
	return string(data), true, nil
}
