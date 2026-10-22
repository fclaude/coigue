# Rust → Go port: decisions, evidence and incompatibilities

This records how the Go editor replaces each Rust crate and platform primitive, what was
verified against the Rust reference, and every place where the behaviour intentionally or
unavoidably differs. The reference is the Rust editor at commit `87bc248`; the behaviour
checklist is `docs/port-parity.md` on branch `claude/go-port-parity-687a76` (ids such as
`BUF-12` or `ODD-19` below refer to it).

The port is a faithful translation, not a redesign: same algorithms, strings, limits and
screen layout. Reasons for the move are embedding and maintenance, not performance.

## Package layout and seams for later phases

| Go package | Rust origin | Notes |
| --- | --- | --- |
| `cmd/coigue`, `internal/app` | `main.rs` | argument handling, event loop, exit paths |
| `internal/editor` | `editor.rs` | **all editor state in one `Editor` value**; no package-level state |
| `internal/buffer` | `buffer.rs` | every text mutation goes through `Buffer` methods and one `change()` snapshot point; `BeginEdit`/`FinishEdit`/`Edit(fn)` group mutations into undo steps |
| `internal/file` | `file.rs` | atomic save, external-change detection |
| `internal/render`, `internal/term` | `render.rs`, crossterm | byte-exact frames; raw mode, input decoding |
| `internal/actions`, `internal/help`, `internal/navigation` | same names | |
| `internal/picker`, `internal/explorer`, `internal/search`, `internal/fileops`, `internal/walk` | same names, `ignore` crate | background work takes a `context.Context` and has `Close()` |
| `internal/lsp`, `internal/semantic` | `lsp.rs`, `semantic.rs` | session lifetime bounded by the context given to `Client.Enable` |
| `internal/syntax`, `internal/theme`, `internal/reflow`, `internal/clipboard` | same names | |
| `internal/width`, `internal/ucd`, `internal/fsx`, `internal/key`, `internal/guard`, `internal/version` | crate/std replacements | see below |

Seams the Scheme extension uses:

* **One owned state value.** `editor.Editor` holds buffer, file, registers, history, overlays,
  language-server client. Nothing is global except process-wide infrastructure that cannot be
  per-editor: the build identity (`internal/version`, set by the linker) and the panic-cleanup
  registry (`internal/guard`).
* **One edit-transaction API.** Buffer text can only change through `Buffer` methods; each calls
  `change()` first, which captures the pre-edit snapshot lazily and bumps the revision.
  `BeginEdit`/`FinishEdit` bracket a transaction (`Edit(fn)` wraps one); a transaction with no net
  change leaves no undo step and keeps redo history.
* **Commands are looked up by name in one table.** `editor.commandTable` (per editor, ordered as Tab
  completion offers them): `name`, whether it takes arguments, and a `run(e, name, args)` function.
  `:w`, `:theme`, `:justify` and the rest are entries; adding a command is adding an entry.
* **Long operations take a context.** `picker.New`, `explorer.New`, `search.Start`, `lsp.Client.Enable`,
  `clipboard.Copy/Paste` all take `context.Context` and release their goroutines on cancel or `Close()`.

## Crate and platform replacements

| Rust | Go | Observable behaviour verified (details under "Evidence per package") |
| --- | --- | --- |
| `crossterm` 0.29 | own parser and ANSI emitter on `golang.org/x/term` and `golang.org/x/sys/unix` (BSD-3-Clause) | 1107 recorded key sequences; 42 scripted PTY sessions byte-identical |
| `unicode-width` 0.2.2 | `internal/width`, tables generated from the crate | every scalar value; 10.3 million strings |
| Rust std Unicode predicates | `internal/ucd`, range tables dumped from std | every scalar value |
| `ignore` 0.4.33 | `internal/walk`, own implementation of the one configuration the editor uses | 37,391 listings on 3,000 random trees, real trees, 63 ported crate tests |
| `pulldown-cmark` | `github.com/yuin/goldmark` (MIT) wrapped in pulldown's table rules | 5 Rust tests, 2,220 recorded cases; **0.20% disagreement on generated documents, see incompatibility 7** |
| `url` 2.5.8 (`idna` 1.1.0) | `internal/lsp/fileurl.go`, a port of the file-URL path of the WHATWG parser, plus a measured table for non-ASCII host characters | 84,797 URLs; IDNA residual in incompatibility 8 |
| `serde_json` | `encoding/json` for strict decoding, hand-written framing of numbers, escapes and key order | 30,018 documents, 30,012 frames |
| `tempfile` | `os.CreateTemp` (0600, same directory) and the build-tagged no-replace rename | the `file.rs` tests including the injected partial-write failure; permissions, symlinks, special mode bits, external creation and deletion |
| `libc` | `golang.org/x/sys/unix`, build-tagged files | Linux run; macOS compiled and vetted only |
| Rust std `Path`, `fs`, `io::Error` | `internal/fsx` | `parent`, `absolute`, `display`, `extension` shapes from Rust probes; `Describe` renders `io::Error` text |

Runtime dependencies are three modules: `github.com/yuin/goldmark` (MIT), `golang.org/x/sys` and
`golang.org/x/term` (BSD-3-Clause). `THIRD_PARTY_NOTICES.md` lists them (it is generated, see `scripts/licenses.py`).

## Terminal adapter

**Decision: no terminal framework. A small input parser and an ANSI emitter of our own on
`golang.org/x/term` (raw mode) and `golang.org/x/sys/unix` (ioctl, signals).**

The renderer mirrors `render.rs`: it builds each row as bytes, remembers the previous frame, and
writes only changed rows inside a synchronized update. The PTY suite asserts exact byte sequences
(`ESC[38;2;203;166;247m` + `fn` + `ESC[38;2;205;214;244m` + ` main() {`, `ESC[1;4H`, the absence of
`ESC[2J`, the presence of `ESC[?2026h/l`, `ESC[0mESC[?1049l` on exit, `ESC[38;5;13m` for the terminal
theme, `ESC[49m` ...). Whatever draws must therefore give us control of every byte.

Candidates were exercised with a spike (kept in the shared notes folder, `terminal-spike/`), on
`TERM=xterm-256color` with a fake TTY:

| Requirement | tcell v2.13 | charmbracelet/ultraviolet (pseudo-version) | own parser + x/term |
| --- | --- | --- | --- |
| Keys with modifiers, Shift-Tab, F1-F9, Alt-arrows | correct, own key model | correct (Esc Esc reported as Alt-Esc, crossterm says Esc) | exactly crossterm's mapping (below) |
| Bracketed paste | only after `EnablePaste` (the editor never enables it) | decodes | recognised and ignored, like crossterm's `Event::Paste` |
| Output control | **owns the output**: on init writes `?1000l ?1002l ?1003l ?1006l ?2004l`, `22;0;0t`, `?1h` (application cursor keys), `ESC[H ESC[2J` (full clear), **`ESC[>4;2m ESC[>1u` (enables modifyOtherKeys and the kitty keyboard protocol)**, `?9001h`, cursor-style `0 q` | cell-diff renderer; `ESC[H ESC[2J` on full-screen render; merges `38;..;48;..` into one SGR and ends with `ESC[m` | every byte chosen by us |
| 24-bit colour with `TERM=xterm-256color` | **downsamples to 256 colours** unless `COLORTERM=truecolor` (`38;5;183;48;5;234` instead of `38;2;203;166;247`), the Rust always writes RGB | can be forced to true colour | always RGB, like crossterm |
| Synchronized update | not written by default | optional | written around every frame |
| Terminal state left behind | would need `Fini`; enabling the keyboard protocol changes how every later program sees keys | similar | exactly `?2026l ?25h ESC[0m ?1049l` |
| Dependencies | ~12 modules | pre-release, ~10 modules | `x/term`, `x/sys` (already needed) |

tcell and ultraviolet both fail the byte-level contract (full-screen clears, keyboard-protocol
enabling, colour downsampling, merged SGR sequences), and a translation layer on top of either
would still need the crossterm quirks below. The parser is about 450 lines.

**Key decoding.** `internal/term/parse.go` is a port of crossterm 0.29's Unix parser: bytes are
appended one at a time and re-parsed; incomplete sequences wait, invalid ones drop the buffer;
a lone `ESC` is Esc only when the read did not fill the 1024-byte buffer; `ESC x` is Alt-x;
`ESC ESC` is one Esc; uppercase letters carry Shift; `\n` is Ctrl-J in raw mode; CSI/SS3/tilde keys,
modifier masks, `CSI u` (kitty) keys, mouse, focus and device-report sequences are all consumed
exactly as crossterm consumes them. **Verified:** `internal/term/testdata/crossterm-keys.txt` holds the
events the real crossterm 0.29 `event::read` produced for 1107 byte strings (every control byte,
Alt-chords, all CSI/tilde numbers with modifiers and kinds, kitty codes, paste, mouse, UTF-8 and invalid
UTF-8, malformed sequences followed by text, sequences split across reads), recorded on a PTY by the
harness in `testdata/keydump`; `TestParserMatchesCrossterm` replays them (0 differences).

**Width** is not the terminal's job; see `internal/width` below.

**Colour fallbacks.** Like the Rust there is no colour-depth detection: the Mocha, Gruvbox, Latte and
Monochrome themes always emit 24-bit colour; the `terminal` theme uses the default colours and the
16 ANSI colours (`38;5;N`, `39`, `49`). `NO_COLOR` (non-empty) is honoured through the same
quirk as crossterm: every colour command becomes a bare `ESC[m` while attributes are still sent
(ODD-19, preserved in `render.paint`).

**Raw mode and cleanup.** `term.Enter` puts the input terminal (stdin if it is a TTY, else `/dev/tty`)
in raw mode with `x/term.MakeRaw` (the same flag set as `cfmakeraw`) and enters the alternate screen;
`Close` writes `ESC[?2026l ESC[?25h ESC[0m ESC[?1049l` and restores the exact saved termios. Exit paths:
normal quit and returned errors (deferred `Close`), panics on the main goroutine (deferred `Close` runs
before the panic message), panics in goroutines started through `internal/guard` (cleanups run, then the
panic proceeds), `SIGHUP`/`SIGTERM`/`SIGINT` delivered from outside (restore, then the signal's default
action), a failed write while entering the alternate screen (`/dev/full`). The Go counterpart of the
Rust unit test `terminal_guard_restores_after_error_and_unwind` is `internal/term/guardcheck`.

## Unicode: `unicode-width`, Rust's `char`/`str` Unicode tables

No Go library matches `unicode-width` 0.2.2: against its per-scalar widths `go-runewidth` differs for 4200
code points, `uniseg` for 792, `x/ansi` for 4217 (spike in the notes folder). Go 1.26 also ships Unicode
15.0 where the Rust standard library of the reference toolchain is 17.0.

* `internal/width` carries tables generated from the crate (Unicode 17.0.0, ambiguous characters narrow)
  and a port of its string-level state machine (`UnicodeWidthStr::width`: emoji presentation
  sequences, ZWJ, regional indicators, Arabic lam-alef, Khmer, Tifinagh ...). See "Verification" in the
  package comment; results are listed in the section "Evidence per package" below.
* `internal/ucd` provides `char::is_alphabetic/numeric/alphanumeric/whitespace/control/uppercase/lowercase`
  and `str::to_lowercase` (U+0130 expansion, final sigma) from range tables dumped from the Rust standard
  library (`testdata/dump.rs`, regenerated with `go generate`). **Verified** for all 0x110000 scalars.

The renderer sums per-scalar widths exactly as `render.rs` does (ODD-21); only reflow uses the string
rules.

## Paths, files and errors (`std::path`, `std::fs`, `tempfile`, `libc`)

* `internal/fsx` implements `Path` semantics that matter (component-wise `starts_with`/`strip_prefix`/
  `ends_with`, `join` that never cleans and lets an absolute operand replace, `parent`, `file_name`,
  `extension`, `absolute` that keeps `..`, a trailing separator and a leading `//`, `canonicalize`, lossy
  `display`) so that lexical Go path cleaning never decides which file an operation touches. Checked
  against Rust for `display` and `extension` shapes, and `parent` and `absolute` against a Rust probe:
  `parent("/a/./b/c")` is `/a/./b` (the spelling is kept, `.` is skipped when counting components),
  `absolute("a/")` keeps the slash and `absolute("//a")` stays `//a` while `///a` becomes `/a`. The
  language-server differential tests found both divergences in the first version.
* `fsx.Describe` renders OS errors the way `io::Error` does (`No such file or directory (os error 2)`):
  Go's `*PathError` text (`open /x: no such file or directory`) would change every message shown in the
  status line.
* `tempfile` → `os.CreateTemp` (mode 0600, same-directory), `renameat2(RENAME_NOREPLACE)` via
  `x/sys/unix` (macOS `renamex_np` with `RENAME_EXCL`), falling back to `link`+`unlink` only on
  `ENOSYS`/`EINVAL`, as the crate does.
* `libc` → `golang.org/x/sys/unix`; platform files carry build tags (`_linux`, `_darwin`, other).
* `serde_json` → `encoding/json` for decoding under serde's rules (what counts as an unsigned
  integer, duplicate keys, escapes) and hand-written output that reproduces its number, escape and key
  order framing.
* `url` → a port of the parts of the WHATWG URL parser the `url` crate runs for `file:` URIs, with the
  crate's percent-encoding sets (Go's `net/url` encodes a different set and cannot be used for URIs that
  are compared as strings); see the `internal/lsp/fileurl.go` comment and its golden test. Host
  characters above U+007F are checked against a table measured from `idna` 1.1.0 for every scalar
  value (`internal/lsp/idna_data.go`), not derived from UTS 46.
* `ignore` → `internal/walk` implements exactly the configuration `src/picker.rs` and `src/explorer.rs`
  use (hidden files, `.gitignore`, `.git/info/exclude` and ignore files of every ancestor; no `.ignore`
  files, no global excludes file, no repository requirement). Listings come in byte order of names, depth
  first, which is the crate's order in practice and what the Rust asks for with `sort_by_file_path`.
* `pulldown-cmark` → `goldmark` is used only to find the paragraph `gq` may fill. goldmark's table rule
  is looser than pulldown's, so `internal/reflow/tables.go` wraps goldmark's table transformer behind
  pulldown's rules; block structure is read from an LF-normalised copy of the text (goldmark misreads
  CRLF) and offsets are mapped back.

## Evidence per package

**Buffer, file, syntax, themes, clipboard.** The 9 buffer/file tests and the 12 tests of syntax, theme and clipboard
are ported. The lexers were compared with the real `src/syntax.rs` and `src/syntax/markdown.rs` on 37,624 cases
(hand-written snippets, CRLF forms, 25,000 random documents, 8,333 character-level fuzz strings) and every
Unicode scalar value, with no difference, plus a 555-case golden file in `go test` and 560 single-edit mutants
(the survivors were equivalent). The palette test is generated from `src/theme.rs`. The clipboard was compared
with the real `src/clipboard.rs` on 35 scenarios (about 80 copy and paste calls); the one difference found, the
wording of OS errors, was fixed by rendering them through `fsx.Describe`. Clipboard helper lookup follows `execvp`, not `exec.LookPath`, because the two disagree
on a non-executable `PATH` entry hiding a later one, empty `PATH` entries and missing interpreters.

**`internal/width`, `internal/ucd`.** `width.Char` agrees with the crate's `char::width()` for all 0x110000 scalar
values, `width.String` for 10,299,885 strings (3,000,000 random, 3,953 fully-qualified and component emoji from
the crate's `emoji-test.txt`, 7,295,932 bounded-exhaustive strings over the 46 symbols that have a state-machine
arm; 1,289,012 of them have a width different from the sum of their characters). A golden file of 2,040 strings
covers every transition and kills the shortest counter-example of each surviving mutation: of 313 mutations of
`widthInStr` 16 survive, 2 provably equivalent and 14 in two arms of the crate that can never be reached
(`VS16_ZWJ_EMOJI_PRESENTATION`, `VS16_KEYCAP_ZWJ_EMOJI_PRESENTATION`), which are ported and commented. The crate's
`set_*` precedence quirk (`a | b & c` is `a | b`) is preserved. 215 string and 46 char assertions of the crate's
own `tests.rs` pass. Statement coverage 98.7%. `internal/ucd` is verified for all scalars against dumps from std.

**`internal/reflow`.** The 5 Rust tests (every byte offset as cursor) pass with the same inputs, plus 2,220 cases
recorded from the real `src/reflow.rs`. A differential over 25,000 generated documents (headings, setext, nested
lists, quotes, tasks, fences, indented code, HTML, tables, reference definitions, inline code and links, escapes,
hard breaks, CRLF, tabs, non-ASCII), 3,775,393 (document, cursor, width) cases with widths 1 to 80, gave **7,655
disagreements (0.20%) in 273 documents** and no panic: see incompatibility 7. 16 recorded cases of two classes that
still disagree were left out of the golden file.

**`internal/walk`, `internal/fileops`, `internal/picker`, `internal/explorer`, `internal/search`.** The 17 Rust tests
(picker 5, explorer 3, fileops 7, search 2) and all 63 gitignore tests of the crate are ported.
3,000 random trees (13,459 symlinks, 3,018 entries with ignore-file errors, 1,854 listings with read errors) gave
37,391 listings and 255,444 entries identical to the `ignore` crate; earlier 2,000- and 6,000-tree runs also had
no difference. `internal/walk/testdata/golden.txt` replays 300 trees and 2,034 recorded listings in plain
`go test`. Of 14 deliberate bugs put into the walker the differential run caught 11, the golden test all but the
BOM and class-byte ones, a unit test the BOM one, and one is an equivalent mutant. End to end against the editor's
own unmodified `picker.rs`, `explorer.rs`, `search.rs`, `navigation.rs`: 300 trees, 600 picker, 1,200 explorer and
600 search runs, no difference (labels, ranking, counts, rows, selection, scroll, filter results, messages,
columns, the 2 MiB skip). Real trees `~/.cargo/registry/src` and `~/go/pkg/mod` match in all four modes, and the
picker configuration on `/usr/share` (504,183 lines), the Go toolchain, `~/.rustup`, `~/Projects`, `/etc`,
`/var/lib` and this repository. The ranking function was compared on 100,000 label/query pairs; `to_string_lossy`
on 200,000 random byte strings. fileops: delete with one implementation and restore with the other, interleaved,
including non-UTF-8 names; 15 error scenarios give identical text. A `git check-ignore --no-index` test agrees
with the walker on 2,343 walks over trees restricted to constructs where git and the crate agree (git 2.56). The
git check is `internal/walk/gitcheck_test.go` (150 trees by default, skipped without `git`); the mutation runs and
the Rust reference programs were scratch work and are not in the repository, so those differential runs can only be
repeated by rebuilding such a harness (the tests read `COIGUE_*` environment variables that name it).

**`internal/lsp`, `internal/semantic`.** The 16 Rust tests are ported. Compared with the unmodified Rust on
generated input: 30,000 semantic-token cases, 84,797 file URLs, 30,018 JSON documents, 30,012 frames, 50,000
UTF-16 positions, 40,210 file-system cases, 7,000 protocol scripts, 800 client scripts, 200 server spawn texts:
no difference. `tests/lsp_smoke.py` drives the client end to end.

**Whole editor (`internal/editor`, `internal/render`, `internal/app`, `cmd/coigue`).**

* All 52 `editor.rs` tests, all 24 `render.rs` tests and the 2 tests of `tests/version.rs` are ported with the same
  scenarios and assertions (Go test names are the Rust names in CamelCase, with a `// Rust:` comment); the 9
  buffer/file tests, 4 `main.rs`-adjacent checks of the CLI contract (CLI-04, CLI-06, CLI-09) and tests for the
  new seams (edit transactions, snapshot aliasing, NO_COLOR) are additional.
* `python3 tests/pty_smoke.py bin/coigue` and `python3 tests/lsp_smoke.py bin/coigue` (unmodified) pass against
  the Go binary with `TERM=xterm-256color`, also against a binary built with `-race` (no race report).
* **Differential sessions against the Rust binary.** `tests/pty_golden.py` replays 42 scripted PTY sessions (startup
  screens for each file type, every mode, operators and counts, search, command line and completion, themes,
  help, menus, scrolling, tiny and zero-size windows, picker, explorer and file operations, switch prompts,
  history, project search, undo/redo, reflow, clipboard errors, unknown keys, `NO_COLOR`) recorded from the Rust
  editor: the Go editor writes **byte-identical terminal output** for all 42 and leaves identical files.
  A random-session differential (same keys into both binaries, screens and files compared after every key;
  six file kinds, five window sizes, 60 keys per session) ran 600 sessions: 597 identical; the 3 differences
  were timing of a background project search and of process exit at the moment of the snapshot, not logic.
  A second random differential with the overlays switched on (explorer, picker, menus, project search; 50 keys per
  session) ran 300 sessions: 282 identical. The 18 others were harness effects: the status line counts the
  entries of `/tmp/coigue-fuzz`, which both editors list while other sessions create directories there, and a
  background project search was caught before or after its result arrived. Re-run one at a time in private
  directories, all 18 are identical. A further 240 sessions (other seeds) had 0 differences, and 100 sessions
  against the final binary, built after the last code change, had 0 differences. The random-session harness is
  scratch work and is not in the repository; `tests/pty_golden.py` is the repeatable form.
* `internal/term/testdata/crossterm-keys.txt`: 1107 byte strings replayed against what the real crossterm
  0.29 produced (see "Terminal adapter").
* Idle CPU over 5 s with a 5000-line file open: Rust 0.02 s, Go 0.00 s.

## Incompatibilities

Each entry names the Rust behaviour, the Go behaviour and the reason. None is hidden by editing a
test; the Rust tests were ported with the same scenarios and assertions.

1. **Termination signals (ODD-03).** Rust leaves `SIGTERM`/`SIGHUP` at their defaults, so the terminal
   stays in raw mode on the alternate screen. Go restores the terminal on `SIGHUP`, `SIGTERM` and `SIGINT`
   and then re-raises the signal with its default action, so the exit status is the usual one. Reason: the
   port brief requires cleanup on every exit path including signals. Ctrl-C is still an ordinary key.
2. **Terminal size source.** crossterm asks `/dev/tty` first and falls back to stdout. Go asks stdout,
   then stdin, then `/dev/tty`. Reason: when the editor runs in a PTY that is not the controlling
   terminal (as in `tests/pty_smoke.py` started from an interactive shell) crossterm reports the size of
   the *user's* terminal, so the page size and every resize step become non-deterministic; in the normal
   case all three are the same terminal.
3. **Panic exit status.** A Rust panic exits with status 101 after the terminal is restored; a Go panic
   exits with status 2. The terminal is restored first in both.
4. **Blocking on incomplete escape sequences.** In crossterm an unterminated or invalid sequence
   (`ESC [` alone, `ESC O @`) makes `event::poll`/`read` block until another byte arrives, stalling redraws
   and language-server polling. The Go reader runs in its own goroutine, so the 50 ms poll loop keeps running.
   The bytes are still consumed or dropped identically.
5. **Closed input.** When the terminal input is closed (EOF/EIO) crossterm's reader spins (a leftover
   Rust test process on a PTY whose other end had closed was seen using a full core). Go ends the session
   with `coigue: terminal input closed` and exit status 1 after restoring the terminal.
6. **PTY-50 harness.** `tests/pty_smoke.py` ends by running the Rust test binary
   `bin/deps/coigue-*` (`--list`, `--exact tests::terminal_guard_restores_after_error_and_unwind`).
   The file is not edited; the Go build provides `bin/deps/coigue-terminal-guard`
   (`go build -o bin/deps/coigue-terminal-guard ./internal/term/guardcheck`), a program that answers
   the same arguments and performs the Go version of the check.

7. **Markdown reflow parser (goldmark instead of pulldown-cmark).** `gq` and `:justify` decide which lines
   form the paragraph with a Markdown parser. On 3,775,393 generated cases the Go editor and the Rust editor
   disagree on 7,655 (0.20%): 4,992 where Rust fills and Go refuses, 2,286 the other way, 377 where both fill but
   a different range. 6,556 involve a tab inside container indentation. The classes, with the share of a
   5,000-document sample (approximate):
   * a tab inside container indentation (`>\n\t>x`, `*\n  \t1.`): about 51%, both directions;
   * a table inside a block quote followed by an unprefixed line (`>|||\n>-|-\n|`): about 17%, Rust ends the table
     and fills the last line, Go refuses (Go is more conservative);
   * a code span continued on a line with leading spaces (`o```\n l ````): about 13%, Go refuses;
   * empty list items, link reference definitions and list tightness (`5) g\n   >*\n   >\n   *`): about 10%,
     both directions, because looseness decides whether a task marker belongs to the paragraph;
   * a task marker at the end of a line inside a quote without a space after `>` (`>+ [X] w` at narrow widths):
     about 5%, pulldown reads it as a blank-start item and refuses, Go fills (Go is less conservative);
   * a setext underline after a table, and a delimiter row of only `:` (`-|:`).
   Both parsers can be wrong about CommonMark; the Rust behaviour is the reference here. Mitigation: the
   write is one undo step (`u` restores it). Fixing it would mean porting pulldown-cmark's block parser.
8. **File URLs for non-ASCII host names.** The `url` crate maps and validates host labels with UTS 46 (`idna`
   1.1.0). Go has no equivalent in the standard library; `internal/lsp/idna_data.go` is a table of what the
   crate accepted for every scalar value above U+007F. Exotic hosts (mapped characters, Punycode edge cases)
   may produce a different URI. Host names only occur when a path starts with `//host/`.
Further entries are added when the evidence for a new package is collected.

## Rust behaviour that looks like a bug and is preserved

* The walker applies the ignore files of every ancestor up to `/`, with no repository boundary: a stray
  `/tmp/.gitignore` or `~/.gitignore` changes the picker, the explorer and project search. The README says
  parent `.gitignore` files are honoured, not that they are honoured past the repository root.
* Starting a walk inside an ignored directory lists its whole content (the root is never tested), while rules of
  ancestors still apply to entries below it by their relative path.
* `.coigue-trash` and `.git` are pruned by name at any depth, files included; a name ending in `.` is never
  hidden; the error count includes the ignore-file errors of the root directory only; an error in an
  ancestor's ignore file is counted once per walk; depth-1 directories are not opened by the explorer, so
  unreadable ones produce no error.
* Where the `ignore` crate differs from git (and the port follows the crate): `{a,b}` alternation drops empty
  branches, character classes can match `/`, POSIX classes are not supported, an unclosed `[` is a literal,
  a run of three or more `*` is three single stars, trailing Unicode whitespace is trimmed, every leading BOM is
  stripped, a line that is not UTF-8 ends the ignore file, a lone `!` whitelists everything, a symlinked
  `.gitignore` is followed, and `.git/info/exclude` is read in every directory that has one. An 18-case
  table in `internal/walk/deviations_test.go` pins these, checked against the real crate and against git.
* `unicode-width`: the `set_*` precedence quirk and two unreachable match arms (see the width evidence).
* `std::path::absolute` keeps a trailing `/`, so an LSP file argument like `dir/` fails `exists()`.

## Not verified here

macOS (Intel and Apple Silicon): `renamex_np` (in `fsx` and `fileops`), `pbcopy`/`pbpaste` helpers, `LC_CTYPE`,
`PENDIN` after raw mode, `F_FULLFSYNC`, case-insensitive path comparisons, `tests/pty_golden.py` (skips
itself there because the status line shows `/tmp/...` paths). Only `go vet` and compilation were run for
`darwin/amd64` and `darwin/arm64`, and `GOOS=windows go vet` for `fileops`. Windows and other platforms are out
of scope. Real language servers and real clipboard helpers were not driven; the suites use fakes.

Also not verified: Markdown reflow against real-world documents (only generated ones were compared);
`ignore` regex size limit (10 MB, which would drop a whole ignore file), `DT_UNKNOWN` file systems, a clock before
the Unix epoch; other `git` versions than 2.56 for the `check-ignore` test; behaviour when the versions of goldmark or
`unicode-width` change (regenerate with `go generate` in `internal/width` and `internal/ucd`); running the tests as
root (the unreadable-directory cases skip). `internal/walk`'s golden test skips if a `.gitignore` exists above
the temporary directory, because it would change the recorded listings.
