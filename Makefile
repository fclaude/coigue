# Developer shortcuts for the Go editor.
#
#   make build          bin/coigue and the PTY suite helper bin/deps/coigue-terminal-guard
#   make check          gofmt, go vet, staticcheck, go test -race and the notices check
#   make acceptance     the Python PTY and language-server suites against bin/coigue
#   make notices        rewrite the Go-library section of THIRD_PARTY_NOTICES.md after go.mod changes
#   make packages       dist/ release packages; requires VERSION (matches internal/version.Version) and COMMIT
#
# The build identity reported by --version is fixed at link time:
#   make build REVISION=123 COMMIT=$(git rev-parse --short HEAD)

MODULE := github.com/fclaude/coigue
STATICCHECK ?= $(shell command -v staticcheck || echo $(HOME)/go/bin/staticcheck)
LDFLAGS :=
ifdef REVISION
LDFLAGS += -X $(MODULE)/internal/version.PackageRevision=$(REVISION)
endif
ifdef COMMIT
LDFLAGS += -X $(MODULE)/internal/version.SourceCommit=$(COMMIT)
endif

.PHONY: build check acceptance notices packages

build:
	go build -ldflags "$(LDFLAGS)" -o bin/coigue ./cmd/coigue
	go build -o bin/deps/coigue-terminal-guard ./internal/term/guardcheck

check:
	test -z "$$(gofmt -l .)"
	go vet ./...
	$(STATICCHECK) ./...
	go build -o bin/deps/brote-lsp github.com/fclaude/brote/cmd/brote-lsp
	COIGUE_BROTE_LSP="$(CURDIR)/bin/deps/brote-lsp" go test -race ./...
	python3 scripts/licenses.py --check

notices:
	python3 scripts/licenses.py

packages:
	test -n "$(VERSION)"
	test -n "$(COMMIT)"
	scripts/release-packages.sh "$(VERSION)" "$(COMMIT)" dist

acceptance: build
	TERM=xterm-256color python3 tests/pty_smoke.py bin/coigue
	TERM=xterm-256color python3 tests/lsp_smoke.py bin/coigue
	TERM=xterm-256color python3 tests/pty_signals.py bin/coigue
	TERM=xterm-256color python3 tests/pty_golden.py bin/coigue
	TERM=xterm-256color python3 tests/pty_extension.py bin/coigue
