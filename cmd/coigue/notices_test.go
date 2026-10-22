package main

import (
	"os"
	"strings"
	"testing"
)

// notShipped lists go.mod requirements that no coigue binary links (a module
// only a test or a build tool imports), with the reason. Everything else needs
// a licence in THIRD_PARTY_NOTICES.md.
var notShipped = map[string]string{}

// Every Go module the editor requires has a notice for the version in go.mod:
// a dependency added or upgraded fails this until python3 scripts/licenses.py
// has written its licence into THIRD_PARTY_NOTICES.md.
func TestEveryRequiredModuleHasANotice(t *testing.T) {
	mod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	notices, err := os.ReadFile("../../THIRD_PARTY_NOTICES.md")
	if err != nil {
		t.Fatal(err)
	}
	inRequire := false
	found := 0
	for _, line := range strings.Split(string(mod), "\n") {
		line = strings.TrimSpace(line)
		var requirement string
		switch {
		case line == "require (":
			inRequire = true
		case line == ")":
			inRequire = false
		case inRequire:
			requirement = line
		case strings.HasPrefix(line, "require "):
			requirement = strings.TrimPrefix(line, "require ")
		}
		fields := strings.Fields(requirement)
		if len(fields) < 2 || strings.HasPrefix(requirement, "//") {
			continue
		}
		path, version := fields[0], fields[1]
		if _, skip := notShipped[path]; skip {
			continue
		}
		found++
		if !strings.Contains(string(notices), "### Go library: "+path+" "+version+"\n") {
			t.Errorf("%s %s is required by go.mod but has no licence in THIRD_PARTY_NOTICES.md: run python3 scripts/licenses.py", path, version)
		}
	}
	if found == 0 {
		t.Error("found no requirements in go.mod; the parser is out of step with the file")
	}
}
