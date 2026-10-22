# Removing the Rust reference

A checklist for the day the Go editor is accepted. Nothing here has been done: the Rust
sources stay buildable (`cargo test --locked` passes) until then, because they are what the
Go port was checked against (see [port-decisions.md](port-decisions.md)).

Before deleting anything, tag the last commit that contains the Rust code so it stays
reachable, for example `git tag rust-reference <commit>`; `// Rust:` comments in the Go
sources and the evidence in `port-decisions.md` refer to it.

## Delete

| What | Where | Notes |
| --- | --- | --- |
| Cargo project | `Cargo.toml`, `Cargo.lock` | |
| Rust sources | `src/` (18 files plus `src/lsp/navigation.rs`, `src/syntax/markdown.rs`) | |
| Rust integration test | `tests/version.rs` | Its two cases are ported in `cmd/coigue/main_test.go`. |
| Rust CI job | `rust-reference` in `.github/workflows/ci.yml`, with the comment above it | Also `CARGO_TERM_COLOR`. If branch protection requires "Rust reference (...)" checks, remove those requirements first. |
| Rust ignore rule | `/target` in `.gitignore` | |
| Version agreement check | `tests/version_agreement.py`; the `python3 tests/version_agreement.py` line in the `check` target of `Makefile`; the CI step "Check the Go and Cargo versions agree"; the README sentence that mentions it | Once `Cargo.toml` is gone it only checks the format of `internal/version.Version`. |
| Rust dump programs (optional) | `internal/term/testdata/keydump/main.rs`, `internal/ucd/testdata/dump.rs`, `internal/width/testdata/dump/` | They regenerate the committed data from the Rust crates and need a Rust toolchain; `go generate` and every test do not. Keep them if the Unicode tables or the crossterm recordings may be refreshed; otherwise delete them and say so in the package comments. |

## Change

* **The PTY-50 workaround.** `tests/pty_smoke.py` ends (the block that begins with the comment
  "Exercise guard unwinding for returned errors and panics in a real terminal") by looking for an
  executable `coigue-*` in `<binary dir>/deps/` that lists the Rust test name
  `tests::terminal_guard_restores_after_error_and_unwind` and accepts cargo's test-harness arguments.
  `internal/term/guardcheck` is the Go program that answers it, and `make build` puts it at
  `bin/deps/coigue-terminal-guard`. Pick one:
  1. Keep it as it is. Nothing to do; the Rust-shaped protocol is only a name.
  2. Make the protocol Go-shaped: change the end of `tests/pty_smoke.py` to run the helper under the PTY with
     plain arguments, and change `guardcheck`'s command line to match.
  3. Delete it: remove that step from `tests/pty_smoke.py`, `internal/term/guardcheck`, the second `go build`
     line of the `build` target in `Makefile`, and the README sentences about the helper. Only do this with a
     replacement for the restore-after-error check, which needs a real terminal and so cannot be a plain `go test`.
* **`tests/pty_golden.py` and `tests/golden/pty-sessions.json`** stay: they are the byte-level regression
  suite for the Go editor. Update the module docstring and the `--record` help, which say the goldens are
  recorded from "the Rust build": after the removal they can only be re-recorded from the Go build, and a
  re-recording then records the Go editor's own output, not the reference's.
* **`README.md`**: delete the "The Rust reference" subsection and the "Rust reference" checks block under
  "Development checks"; drop "A separate job runs the Rust reference's own checks" in Compatibility.
  Keep everything about `rust-analyzer`: highlighting and language-server support for `.rs` files is a
  product feature, not a leftover.
* **`THIRD_PARTY_NOTICES.md`**: remove the sentence about the Rust reference sources in the introduction.
* **`staticcheck.conf`**: reword the comment ("copied verbatim from the Rust editor"); the settings stay.
* **`docs/port-decisions.md`**: keep as the record of the port. Add a line at the top that the Rust sources were
  removed and which tag holds them. `// Rust:` comments in Go sources and tests can stay as provenance.
* **`.github/workflows/ci.yml`**: after removing the Rust job, nothing else refers to Cargo.

## Stay

* `LICENSE`, `go.mod`, `go.sum`, `cmd/`, `internal/` (including every `testdata/` directory of recorded
  results: crossterm key recordings, walk and reflow goldens, width and Unicode tables), `Makefile`,
  `staticcheck.conf`.
* `tests/pty_smoke.py`, `tests/lsp_smoke.py`, `tests/pty_signals.py`, `tests/pty_golden.py`,
  `tests/pty_extension.py`, `tests/golden/`.
* `THIRD_PARTY_NOTICES.md` with its generated Go-library section, `scripts/licenses.py`, and the Go test that
  fails when `go.mod` changes without it.
* `internal/version` and the `-ldflags -X` identity (`Version`, `PackageRevision`, `SourceCommit`).
* The Go job of the CI workflow, with the same three runners.

## Check afterwards

```sh
git ls-files | grep -E '(^|/)(Cargo\.|.*\.rs$)'      # nothing, or only the dump programs you kept
git grep -n -i -E 'cargo|rustc|rustup' -- . ':!docs/port-decisions.md'   # nothing outside provenance notes
make check && make acceptance
```
