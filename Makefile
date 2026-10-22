# Developer shortcuts for the Go editor.
#
#   make build          bin/coigue and the PTY suite helper bin/deps/coigue-terminal-guard
#   make check          gofmt, go vet, staticcheck, go test -race
#   make acceptance     the Python PTY and language-server suites against bin/coigue
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

.PHONY: build check acceptance

build:
	go build -ldflags "$(LDFLAGS)" -o bin/coigue ./cmd/coigue
	go build -o bin/deps/coigue-terminal-guard ./internal/term/guardcheck

check:
	test -z "$$(gofmt -l .)"
	go vet ./...
	$(STATICCHECK) ./...
	go test -race ./...

acceptance: build
	TERM=xterm-256color python3 tests/pty_smoke.py bin/coigue
	TERM=xterm-256color python3 tests/lsp_smoke.py bin/coigue
	TERM=xterm-256color python3 tests/pty_signals.py bin/coigue
	TERM=xterm-256color python3 tests/pty_golden.py bin/coigue
