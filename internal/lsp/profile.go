package lsp

import (
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/fclaude/coigue/internal/ext"
)

//go:generate go run ../../cmd/coigue-host-profile coigue-profile.json
//go:embed coigue-profile.json
var coigueProfile []byte

func configRoot(path string) (string, bool) {
	init, ok := ext.InitPath(os.Getenv, os.UserHomeDir)
	if !ok {
		return "", false
	}
	root := filepath.Clean(filepath.Dir(init))
	rel, err := filepath.Rel(root, filepath.Clean(path))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return root, true
}
func schemeInitializationOptions(rootURI string) any {
	path, ok := uriToPath(rootURI)
	if !ok {
		return nil
	}
	root, ok := configRoot(path)
	if !ok || filepath.Clean(path) != root {
		return nil
	}
	var profile any
	if err := json.Unmarshal(coigueProfile, &profile); err != nil {
		panic(err)
	}
	return object{"hostProfile": profile}
}
