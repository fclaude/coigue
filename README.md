# Coigüe

A small terminal modal editor written in Go. One UTF-8 file at a time. Your own commands can be
written in Scheme, run by [Brote](https://github.com/fclaude/brote); see
[Extending with Scheme](#extending-with-scheme).

[Documentation](https://fclaude.github.io/coigue/) ·
[Getting started](https://fclaude.github.io/coigue/setup/) ·
[Keys and commands](https://fclaude.github.io/coigue/keys/) ·
[Writing an extension](https://fclaude.github.io/coigue/writing/)

Licensed under the [MIT License](LICENSE).
Theme attribution and the licenses of the Go libraries linked into the executable
are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md); include it with every
distribution of the executable.

## Build and run

Install Go 1.26 or newer (`go.mod` asks for 1.26.0), then:

```sh
git clone https://github.com/fclaude/coigue.git
cd coigue
make build                   # bin/coigue
bin/coigue                   # welcome in the current directory
bin/coigue path/to/file
go run ./cmd/coigue          # the same without make
```

`make build` also builds `bin/deps/coigue-terminal-guard`, a helper that the
terminal test suite runs; it is not part of the editor. The Go sources are
`cmd/coigue` and `internal/`; `go.mod` lists the three runtime dependencies
(goldmark, `golang.org/x/sys`, `golang.org/x/term`).

## Releases

Pushing a tag `vX.Y.Z` whose numbers match `internal/version.Version` runs the
[release workflow](.github/workflows/release.yml). It waits for CI, then
publishes a GitHub release. Running the workflow again on that tag replaces
the assets.

| System | Files |
| --- | --- |
| Ubuntu, Debian | `.deb` for amd64 and arm64 |
| Fedora, RHEL | `.rpm` for x86_64 and aarch64 |
| macOS | `.tar.gz` for Intel and Apple Silicon |
| Windows | `.zip` for amd64 and arm64 |
| Other Linux | the same binaries as `.tar.gz` |

`SHA256SUMS` lists every file. Archives contain the binary, `LICENSE` and
`THIRD_PARTY_NOTICES.md`. The packages install `coigue` at `/usr/bin/coigue`
and those two files under `/usr/share/doc/coigue/`. Locally,
`make packages VERSION=0.1.0 COMMIT=$(git rev-parse HEAD)` writes the same
files to `dist/`.

## Compatibility

Linux and macOS terminals are the intended platforms. [GitHub Actions CI](.github/workflows/ci.yml)
tests Linux x86-64, macOS Intel, and macOS Apple Silicon natively, including
gofmt, `go vet`, staticcheck, unit tests under the race detector, the build, and
the terminal, language-server, signal-exit and extension PTY suites. The recorded-session
replay runs on Linux only.

| System | Status | Evidence and limitations |
| --- | --- | --- |
| Linux x86-64 | Tested in CI | Native `ubuntu-24.04` runner; builds, unit tests, and all five PTY suites pass, including the replay of the recorded sessions. |
| macOS Intel | Tested in CI | Native `macos-15-intel` runner; builds, unit tests, and the terminal, language-server and signal suites pass. The recorded-session replay skips itself. |
| macOS Apple Silicon | Tested in CI | Native `macos-15` ARM64 runner; builds, unit tests, and the terminal, language-server and signal suites pass. The recorded-session replay skips itself. |
| Linux ARM64 and other architectures | Unverified | No architecture-specific application code, but compilation and runtime behavior have not been verified. |
| Windows | Release builds, unverified | amd64 and arm64 zips. The console uses virtual-terminal sequences (Windows Terminal). Explorer rename, delete, and restore still fail. The terminal test suite does not run here. |
| BSD and other Unix systems | Not an intended platform | Core editing is unverified; explorer rename, delete, and restore operations explicitly fail. |

CI coverage applies to the runner systems listed above; minimum OS versions
are not documented. Platform-specific code and tests sit behind build tags
(`_linux`, `_darwin`, `unix`); the Python PTY integration tests require Unix.

Across supported systems:

- Built-in color themes need a truecolor terminal for faithful rendering;
  `:theme terminal` uses the terminal's default colors and ANSI palette.
- Files must be UTF-8. Existing CRLF bytes round-trip unchanged, but CR displays
  as a replacement glyph and newly inserted line breaks use LF.
- `rust-analyzer`, `gopls`, and Marksman are optional, separately installed
  language servers. Editing and saving work without them.
- Explorer moves must stay on the same filesystem. Non-overwriting rename,
  delete, and restore use OS primitives implemented only for Linux and macOS.

## Usage

Run `coigue` without arguments to see a welcome screen rooted in the directory
where you launched it. Use `Ctrl-O`, `-`, or `:explore` to browse that directory,
or `Ctrl-P` / `:open` to find files recursively. In the explorer, `a` creates a
file; Enter opens the selected file for editing. Esc (or Ctrl-O in the explorer)
returns to welcome until a file is opened. `:q` exits. Welcome does not accept
text edits or save a placeholder; `:w` / `:wq` report that no file is open.
The current directory and key hints appear on the welcome screen; small terminals
clip the display and resizing redraws it. `coigue --help` shows startup usage.

A nonexistent explicit path starts an empty buffer. Parent directories must already exist.

Press **F1** anywhere for help, including while editing, filtering files, typing
commands, or confirming a file operation. Help starts on the relevant topic;
Tab / Shift-Tab switch topics, arrows or Page Up/Down scroll, and Esc / F1
returns to the exact previous state. Opening help during Insert mode keeps the
same undo group. **`:help`** opens the guide from the command line. Welcome
includes a short getting-started guide; the remaining topics cover editing,
finding files, the directory browser, code navigation, and confirmations.

**F2 Files**, **F3 Code**, and **F1 Help** stay visible in the interface.
F2 / `:files` and F3 / `:code` open searchable action menus; **F4** / `:actions`
includes every category, including help topics. Type words to filter, use arrows
or Page Up/Down to choose, Enter to act, and Esc to return unchanged. Tab switches
categories. Direct shortcuts continue to work. Menu cancellation preserves even
an unfinished command or Insert session; choosing a navigation action ends the
Insert group. Pending save/delete confirmations must be resolved before another
navigation action can run.

**Alt-Left / Alt-Right** or **`:back` / `:forward`** return through navigation
history, including file switches, searches, and explicit line jumps. Each location
remembers the file, cursor, and scroll position. **`:recent`** or Files → Recent
files opens a searchable list of the last 50 files visited in this session.
History keeps up to 100 locations in each direction. A new jump clears forward
history. Failed opens and canceled save/discard choices leave history intact;
same-file returns keep edits and undo history without reloading. Switching to a
different file keeps the existing save/discard/cancel protection. History and
recent files are not persisted across editor restarts.

Code navigation is available from **F3 Code**, with direct shortcuts:

| Action | Shortcut | Command |
| --- | --- | --- |
| Search text across the project | F5 | `:project-search` |
| Symbols in the current file | F6 | `:symbols` |
| Go to definition | F7 | `:definition` |
| Find references, including the declaration | F8 | `:references` |
| Search workspace symbols | F9 | `:workspace-symbols` |

Project search is literal and case-sensitive, rooted at the launch directory.
Type to search; arrows choose a result, Enter opens it, and Esc cancels. Results
show one match per line, with file, line, column, and a short text preview.
Search uses the file picker's ignore policy and the active file's unsaved text.
It skips binary, unreadable, non-UTF-8, and over-2-MiB files, reports skips, and
limits results to the first 1000 matching lines. Refine the query when capped.
Queries and discovery run in a cancellable background worker; no external search
program or language server is required.

Symbol, definition, and reference navigation uses the optional language server
for the active file. File symbols and references can be filtered by typing;
workspace symbols are requested as you type a query. A single definition opens
directly; multiple definitions show a list. Every successful jump supports
back/forward history and the existing unsaved-change choices. Closing a result
list keeps your cursor, text, and scroll position. Missing servers, unsupported
features, errors, and timeouts produce a message in the list; Esc returns to
editing. Navigation replies for older requests, edits, or files are ignored.
Only local-file locations are opened. Other-file locations over 2 MiB or without
readable UTF-8 text are omitted. Results are limited to 1000; an empty result means
the server found no locations. Project search remains available independently.

Run `coigue --version` to show the source version, package build, and source commit.
Run `coigue --help` for command-line usage without entering the terminal editor.

Bindings:

- Normal mode: `h j k l`, arrows, `0`, `$`, `gg`, `G`; `i` insert, `a` append, `o` open a line below; `x` delete a character, `dd` delete a line.
- Insert mode: text, Enter, Tab, Backspace, arrows. Esc returns to normal mode.
- Normal and insert modes: Home / Ctrl-A begins a line; End / Ctrl-E ends a line
  (after the last character in insert mode). Page Up / Ctrl-B and Page Down / Ctrl-F
  move by the current viewport height, excluding the two status/message rows.
- Command mode: `:N` jumps to line N (for example `:42` then Enter); `:w`, `:q`, `:wq`, `:q!`, Enter to execute; Esc cancels. `:q` refuses unsaved changes; failed writes never quit.

In the `:` command line, Tab completes command names and `:theme` arguments
(including theme aliases). Repeated Tab cycles through matching choices;
Shift-Tab cycles backward. An empty prompt lists all commands by cycling, and
`:theme ` cycles through all theme names. Typing or Backspace starts a fresh
completion cycle. Completion only changes the prompt; Enter executes it.

Additional normal-mode editing commands:

| Keys | Action |
| --- | --- |
| `u` / Ctrl-R | Undo / redo the last edit group. |
| `w` / `b` / `e` | Next word beginning / previous word beginning / current or next word end. |
| `dw` / `yw` | Cut / copy through the next word beginning (exclusive), or EOF. |
| `cw` | Change the current run and enter insert mode. |
| `yy` | Copy the current line into the internal register. |
| `dd` | Cut the current line into the register. |
| `x` | Cut the character under the cursor into the register. |
| `p` / `P` | Paste a copied/cut line below/above, or characterwise text after/before the cursor. |
| `r` then a character | Replace the character under the cursor; Esc cancels. Tab is also accepted. |
| `/` then text, Enter | Find the next literal, case-sensitive match in the file. Esc cancels. |
| `n` / `N` | Find the next/previous occurrence of the last search, wrapping at the file boundaries. |
| `>>` / `<<` | Indent by four spaces / remove one leading tab or up to four leading spaces. |
| `gq` | Fill the current Markdown paragraph to the session text width. |
| `A` | Enter insert mode after the last character of the current line. |

Normal-mode counts repeat movement and editing commands: `5j`, `3w`, `2dd`,
`2yy`, `4>>`, `3x`, `2p`, and `3r_`. Counts before and after an operator multiply
(`2d3w` cuts six words). `Ngg` and `NG` go to line N; bare `gg` / `G` go to the
first / last line. `:N` also jumps to line N, at its first character; line
numbers beyond EOF go to the last line, while zero and overflowing numbers report
an error. Esc cancels a pending jump. Counts also work with arrows, page movement, `n` / `N`, undo,
and redo. `4>>` indents four lines once each. Ranges stop at the file or line
boundary; each counted edit is one undo step, including a counted `cw` and its
following insert session. Registers retain the entire selected range.
Pending keys appear in the message row; Esc cancels them. A leading `0` still
moves to the start of the line. Counts and multiplied totals are capped at 9999.
Insert-entry commands (`i`, `a`, `A`, `o`) enter once; inserted text is not repeated.
Counts apply only to normal editing, not browser filters, search input, or commands.

Press **`v`** in normal mode for inclusive character-wise visual selection, or
**`V`** for line-wise selection. Move with `h/j/k/l`, arrows, `w/b/e`, `0/$`,
`gg/G`, Home/End, Page Up/Down, or their existing Ctrl aliases; counts work with
these motions. Selected text appears in reverse video, including a cell for
selected line breaks and empty lines. **`o`** swaps the anchor and moving end;
`v`/`V` switches selection type, or cancels when already in that type.
**Esc** cancels. **`y`** yanks and **`d`** deletes the selection, returns to normal
mode and places the cursor at its start. Use **`p`** / **`P`** to paste through
the existing internal register. Character-wise selections retain exact Unicode
scalars, whitespace and line separators; line-wise selections use the same paste
semantics as `yy`/`dd`. Deletion is one undo step; selection and yanking add no
history. An empty character selection at EOF keeps the previous register.
Other editing commands, browser shortcuts and command entry are ignored while
selecting. Rectangular/block selection is intentionally excluded from the
project. Mouse selection is not implemented; combining marks remain separate scalars.

System clipboard access is explicit:

| Command/key | Action |
| --- | --- |
| Visual **`Y`** | Yank the selection internally, then copy it to the system clipboard. |
| **`:clipboard-copy`** | Copy the current internal yank register to the system clipboard. |
| **`:clipboard-paste`** | Paste system clipboard text after the cursor. |
| **`:clipboard-paste-before`** | Paste system clipboard text before the cursor. |

The commands support Tab completion and apply in normal mode. Clipboard paste is
one undo step and keeps the internal yank register. Empty clipboard text does
nothing. Unicode, whitespace and line separators are retained. Text copied by
this editor retains its character/line-wise paste type while the clipboard still
matches that export in the same session; other clipboard text pastes character-wise.
A failed copy keeps the internal yank, and a failed paste keeps text/history intact.
An empty character selection at EOF does not export the previous register.

macOS uses `pbcopy`/`pbpaste`. Linux Wayland needs `wl-copy`/`wl-paste`
(`wl-clipboard`); X11 needs `xclip` or `xsel`, using the CLIPBOARD selection.
Utilities are discovered on PATH. When both displays are set, Wayland is preferred;
a missing utility falls back to the next available backend, while backend errors
are reported directly. No packages are installed automatically. Operations time
out after two seconds and accept at most 8 MiB of UTF-8 text. Private temporary
files carry helper input/output and are removed after the operation.

In SSH/headless sessions, clipboard utilities need access to a desktop display
on the machine running Coigüe (including an explicitly forwarded X11 display).
Without that access, commands report a recoverable error; internal `y`/`p` still
work. Coigüe never exports via OSC 52, invokes a network clipboard service, or
reads/writes the clipboard merely by starting the editor or using ordinary yanks.
Terminal/multiplexer clipboard forwarding is not enabled automatically.

Use **`gq`** in normal mode or **`:justify [width]`** to fill just the paragraph
under the cursor. **`:textwidth 72`** sets the session width (default 80 display
columns); `:textwidth` reports it. Command widths accept 1–10000; a width on
`:justify` applies only to that invocation. Both commands support Tab completion.
This is source reflow, without padded full justification: Markdown collapses added
spaces, so left-and-right alignment belongs in the rendered document's styling.
The commands work on any file, treating its contents as Markdown.

Reflow preserves list/blockquote prefixes, nested list indentation, LF/CRLF and
final newlines. Headings, code blocks, tables, HTML, reference definitions and
paragraphs with intentional hard breaks are protected. Links and inline code are
kept together, so long constructs/words may exceed the width. Ambiguous container
continuations or unbalanced inline constructs are left unchanged; a rendering
comparison also rejects structural changes. Ordinary inter-word whitespace is
normalized. The cursor moves to the paragraph's first line; the entire change
undoes/redoes as one edit with `u` / Ctrl-R. No other paragraph is reflowed.
Width is retained across file switches but is not saved between sessions.

Words are Unicode letters/numbers and underscores; punctuation forms separate
runs and Unicode whitespace separates runs. Word motions cross empty lines.
`cw` on a word or punctuation run removes its remainder, retaining following
whitespace; on whitespace it removes through the next word beginning. Its cut
and following insert session undo together. Characterwise word registers retain
exact whitespace and LF/CRLF bytes, including across lines; `p`/`P` paste them
after/before the cursor. At EOF operators stop; an empty cut keeps the register.
These bindings apply only to normal editing, not explorer, filters, or prompts.
Word operations scan a scalar-value copy of the buffer; very large files may
notice a delay. Combining marks remain separate scalar values, as in other edits.

The register persists across file switches and supports repeated pastes; it is
internal to this session; ordinary `y`, `d`, `p` and `P` never access the system
clipboard. Searches stay within a line and report missing matches without moving the cursor. An empty
search reuses the previous query. The register and search are not saved to disk.

Undo treats each insert-mode session (including `o`, `a`, and `A`) as one edit;
normal-mode edits are separate steps. Navigation, copying, and search do not add
history. A new edit after undo clears redo; unchanged operations do not. Saving
keeps history, and undo/redo restores the cursor and updates the unsaved indicator
against the most recently saved contents. Opening another file starts fresh
history; canceling the picker keeps it. History retains up to 100 edit groups in
memory using whole-buffer snapshots, so large files require more memory. History
is not persisted across sessions. Undo/redo are normal-mode commands.

Press **Ctrl-P** (normal or insert mode) or run **`:open`** to pick another file.
The picker recursively searches the process's working directory, rather than the
current file's parent directory. It uses built-in filesystem discovery and fuzzy
matching; no external commands or tools are needed. Type characters in order
(e.g. `s/edr` matches `src/editor.rs`), Backspace to refine, arrows or Page Up/Down
to select, Enter to open, and Esc to cancel. Matching is case-insensitive and
favors consecutive characters, word boundaries, basenames, and shorter paths.
Files appear while discovery runs in the background; reopening rescans the tree.
Hidden files are included except `.git`; symlinks and special files are skipped.
Nested and parent `.gitignore` files and repository `.git/info/exclude` rules
are honored, including negations; ignored directories are pruned during discovery.
Rules also work outside Git repositories. Global Git excludes and `.ignore` files
are not read. Unreadable entries and ignore-file errors are counted and skipped.
Browsing is allowed with unsaved edits. Selecting a file offers `s` to save and
open, `d` to discard and open, or `c`/Esc to cancel the switch. Failed saves keep
the choice open; failed opens preserve the original buffer and undo history.
Opening a file returns to normal mode at its first character.

Press **Ctrl-O** in normal or insert mode, **`-`** in normal mode, or run **`:explore`** for a full-screen directory
explorer. It opens at the launch directory on welcome, or the current file's parent and selects that file, revealing
it even if hidden or ignored. The process working directory is never changed,
so Ctrl-P keeps searching from its original working-directory root.

| Explorer keys | Action |
| --- | --- |
| `j` / `k`, Up / Down | Move through entries; directories sort first. |
| Enter / `l` / Right | Enter a directory or open a file. |
| `h` / Left / `-` | Go up and select the directory just left. |
| Home / End, Page Up / Down | Jump or page through entries. |
| `/` | Enter a fuzzy filter for names in this directory; type to narrow. |
| Backspace while filtering | Edit the query; arrows and Enter still navigate/open. |
| Tab while filtering | Select the match and leave filtering, ready for file actions. |
| `a` / `A` | Create a file / directory; type a path and Enter. |
| `r` | Rename/move the selected entry; edit its name or enter another path. Ctrl-U clears the input. |
| `d` | Move the selected entry to recovery storage; `y` confirms, Esc/`n` cancels. |
| `u` | Restore the latest deletion in this directory, including after restarting the editor. |
| Ctrl-O | Return to the file; Ctrl-E retains its end-of-line binding. |
| Esc while filtering | Clear the filter and restore the previous selection. |
| `.` outside filtering | Toggle hidden and ignored entries. `.git` and symlinks stay excluded. |
| Ctrl-P | Open the recursive picker; Esc returns to the explorer. |
| Esc outside filtering | Return to the current file at its original cursor and scroll position. |

The explorer remembers directory selection and scroll position for this session.
Directory loading runs in the background. Opening a different file uses the same
save/discard/cancel choices as Ctrl-P. Browsing, filtering, and canceling preserve
edits and undo history.

Creation and rename prompts accept relative paths from the displayed directory
or absolute paths; destination parent directories must already exist. Enter
executes and Esc cancels. Existing destinations (including symlinks) are never
overwritten. Moves use an atomic no-replace rename on Linux/macOS; cross-filesystem
moves are refused and leave the source intact. Moving the open file or an ancestor
updates its save path while preserving unsaved edits, cursor, and undo history.
Open another file before deleting the active file or an ancestor.

Deletion moves the whole entry into a private `.coigue-trash/entry-.../item`
directory beside its original location and records its original name there.
The record is relative to that parent, so recovery also survives renaming the parent.
Recovery is persistent, does not need external tools, and never overwrites an
occupied original path. To recover, browse the original parent directory and
press `u`; repeat to restore older deletions. A collision leaves the deleted data
in recovery storage so it can be retried after freeing the name. Storage is not
automatically purged. Add `.coigue-trash/` to your Git ignore rules if needed;
the editor does not change `.gitignore`. `.coigue-trash` and `.git` are excluded from both browsers
even when showing all entries, and filesystem actions cannot target either.
The recovery directory and its contents are ordinary on-disk files: keep them
until recovery is no longer needed. Buffer undo (`u` outside the explorer) covers
text edits; explorer `u` restores deletions only.

Absolute line numbers are enabled by default, with no configuration option yet.
The gutter grows with the file's line count and stays fixed during horizontal
scrolling. It is hidden in terminals too narrow to leave one text column.
Browsers, welcome, status and command rows do not receive line numbers.

Catppuccin Mocha is the default theme. Use `:theme` to show the current theme and
available choices, or `:theme <name>` to switch immediately:

| Command | Theme |
| --- | --- |
| `:theme mocha` | Catppuccin Mocha (default dark). |
| `:theme gruvbox` | Gruvbox Dark (warm dark). |
| `:theme latte` | Catppuccin Latte (light). |
| `:theme monochrome` | High-contrast dark grayscale, with bold white keywords and muted comments. |
| `:theme terminal` | Use the terminal's default background/foreground and ANSI palette. |

The full names `catppuccin-mocha`, `gruvbox-dark`, and `catppuccin-latte` are also
accepted. Theme selection applies to the whole session, including welcome,
file browsers, syntax, gutter diagnostics, and the status row; opening another
file keeps the selection. Nothing is saved: restarting returns to Mocha, unless your
`init.scm` picks another theme (see below). Built-in palettes use 24-bit RGB colors and need a truecolor
terminal for faithful rendering; `terminal` retains the previous ANSI behavior.

Lines scroll vertically and horizontally; terminal resize triggers a redraw.
Screen updates are buffered and repaint only changed rows, with synchronized updates
on terminals that support them. LF and existing trailing newlines are preserved exactly; opening a file does not add a newline. CRLF bytes round-trip unchanged, but CR is displayed as a replacement glyph and newly inserted line breaks use LF.

Saving compares the file's bytes with the last loaded/saved snapshot, writes and syncs a same-directory temporary file, then atomically replaces the destination. Failed writes leave existing contents intact. New files are created without clobbering an existing destination. Existing symlinks are resolved at opening, so later changes to the original link do not redirect the session. Saves refuse read-only destinations or a resolved target replaced by a symlink. Unix permission bits are preserved; new files use private temporary-file permissions (0600 on Unix). Atomic replacement changes inode identity (including hard-link relationships); ownership, ACLs and extended attributes are not preserved. If another program writes after the final byte comparison but before the atomic rename, its update can still be overwritten without detection; this is not a locking editor. Use one writer per file. No force-write command bypasses conflicts.

Limits: in-memory files, scalar-value rather than grapheme-cluster editing (combining marks may be edited independently), fixed four-column tabs, terminal-dependent emoji/combining display width. No multiple buffers or persistent settings yet; the configuration read at startup is your optional Scheme `init.scm`, and the modules it requires from the `modules` directory next to it (see [Extending with Scheme](#extending-with-scheme)). Raw mode, cursor and alternate screen are restored on normal exits, returned errors, panics, and SIGHUP, SIGTERM and SIGINT; uncatchable termination (SIGKILL) cannot run cleanup.

## Extending with Scheme

Coigüe embeds [Brote](https://github.com/fclaude/brote), a small Scheme for Go programs. A Scheme file
in your own configuration directory, `$XDG_CONFIG_HOME/coigue/init.scm` (or
`~/.config/coigue/init.scm`), can define editor commands that read the selected text and ask the
editor to change it:

```scheme
(define (wrap open close)
  (replace-selection (string-append open (selection-text) close)))

(define-command "wrap" wrap
  "Wrap the selection in OPEN and CLOSE, for example :wrap ( ) or :wrap ** **")
```

Select text with `v` or `V`, press `!`, type `wrap ( )` and Enter. The whole command is one undo
step; a command that fails, or that runs away and is stopped by Ctrl-C or its five-second limit,
changes nothing. Scheme is loaded only from that file and from modules it requires in the `modules`
directory beside it, never from a project directory, and it cannot read other files or run programs. It
reaches the network only to hosts a module's header names and you approved, from a background task.
[docs/extending.md](docs/extending.md) is the
contract: the host operations, the exact behaviour, every failure, and how it is tested.

The same file can define themes. This one starts from Gruvbox, changes two colours and makes
keywords bold, and the editor starts in it:

```scheme
(define-theme "dusk"
  '(("base" . "gruvbox")
    ("keyword" . "#ff79c6")
    ("string" . "#8be9fd")
    ("bold-keywords" . #t)))

(set-theme "dusk")
```

`:theme dusk` switches to it later, and Tab completes its name. The keys a theme can change are
listed in [docs/extending.md](docs/extending.md#themes).

Besides the selection, a command can read the file's path, the cursor column, the word under the
cursor, a search (`find`) and the selection's range, and it can replace or delete whole lines, insert
at a position and move the cursor. All of a command's edits are one undo step, and every operation is
listed in [docs/extending.md](docs/extending.md).

It can bind keys in Normal and Visual mode too. A bound key runs its procedure the way a command does:
on a private copy of the buffer, as one undo step, with the same five-second limit. This one copies the
current line below itself:

```scheme
(define (duplicate-line)
  (let* ((line (cursor-line))
         (column (cursor-column))
         (text (buffer-text line)))
    (insert-at line (+ (string-length text) 1) (string-append "\n" text))
    (set-cursor (+ line 1) column)))

(bind-key "normal" "<C-d>" duplicate-line "Copy the current line below it")
```

A key is one character (`"x"`), Ctrl and a letter (`"<C-d>"`) or Alt and a character (`"<A-j>"`). There are
no key sequences, and a key the editor already uses needs `#t` as the last argument to `bind-key`;
`:`, Esc, Ctrl-C, Ctrl-O and Ctrl-P can't be bound at all. F1 lists your commands and keys.

`(on-start proc)` runs a procedure once when the editor has started and a file is open. `:reload` reads
`init.scm` and its modules again: if the load succeeds, its commands, themes and bindings replace the
old ones, and if it doesn't, nothing changes and the status line says why.

`docs/examples/sample.scm` is one file with these pieces, plus a key that joins the next line and one
that comments the current line.

## Development checks

[GitHub Actions CI](.github/workflows/ci.yml) runs the checks below on pushes,
pull requests, and manual dispatches using Go 1.26 and Python 3.13, natively on
Linux x86-64 (`ubuntu-24.04`), macOS Intel (`macos-15-intel`), and macOS Apple
Silicon (`macos-15`). Go modules, Brote included, are downloaded from their public sources.

```sh
make check        # gofmt, go vet, staticcheck, go test -race, notices and version checks
make build        # bin/coigue and bin/deps/coigue-terminal-guard
make acceptance   # builds, then runs the five Python suites below against bin/coigue
make notices      # rewrite the Go-library section of THIRD_PARTY_NOTICES.md after go.mod changes
```

`make check` finds `staticcheck` on `PATH` or in `~/go/bin`; CI installs
`honnef.co/go/tools/cmd/staticcheck@v0.8.1` (`go install` it the same way).
`staticcheck.conf` allows capitalised error strings in user-visible messages. The PTY suites need `TERM=xterm-256color`, which
`make acceptance` sets. `make build REVISION=123 COMMIT=$(git rev-parse HEAD)` fixes
the identity `coigue --version` prints (`coigue 0.1.0 (main build 123; commit ...)`);
without them it prints `development` and `unknown`.

The acceptance suites take the binary as their argument and import nothing from the
repository:

```sh
TERM=xterm-256color python3 tests/pty_smoke.py bin/coigue
TERM=xterm-256color python3 tests/lsp_smoke.py bin/coigue
TERM=xterm-256color python3 tests/pty_signals.py bin/coigue
TERM=xterm-256color python3 tests/pty_golden.py bin/coigue
TERM=xterm-256color python3 tests/pty_extension.py bin/coigue
```

The Unix PTY smoke test uses only disposable files and checks save/reopen, repeated edits, resize, quit safeguards and terminal restoration after errors and unwinding. Its last step runs `bin/deps/coigue-terminal-guard`, which enters and leaves the terminal by an error and by a panic and checks that the modes are back. The additional `/dev/full` output-failure test runs on Linux and is skipped when that device is unavailable (including macOS). The language-server suite starts itself as a fake server, so no real server is needed. `pty_signals.py` checks the signal exits. `pty_golden.py` replays 42 recorded sessions and requires the same terminal bytes (or at least the same screens); it is Linux only because the status line shows `/tmp` paths.

`pty_extension.py` drives the Scheme command in a real terminal (undo, bound keys, `:reload`, the start-up procedure, failures, cancellation, file
switching, loading rules) with a temporary `XDG_CONFIG_HOME`. The other suites read the caller's real
`~/.config`, so a machine that runs them must not have a `coigue/init.scm` there.

The licenses of the Go libraries are generated, not typed: `python3 scripts/licenses.py`
writes them into `THIRD_PARTY_NOTICES.md` (the `go test` run fails until it has been
run after a dependency changes, and `scripts/licenses.py --check` fails when the file
is stale).

Rust (`.rs`), Go (`.go`), and Markdown (`.md`, `.markdown`) files receive built-in
lexical highlighting immediately, including when no language server is installed. Scheme (`.scm`) also has built-in colouring for forms, comments, ordinary and raw multiline strings, numbers, booleans and punctuation.
Extensions are case-insensitive. The Rust lexer handles keywords,
comments, strings, characters, lifetimes, numbers, and macros; the Go lexer handles
keywords, comments, quoted/raw strings, runes, and numbers. Multiline state is
preserved for block comments and raw strings. Markdown highlights headings, emphasis,
links and images, list/quote markers, task checkboxes, thematic breaks, HTML comments,
and inline/indented/fenced code. Headings and strong emphasis are bold; emphasis is
italic. Top-level Rust and Go fences reuse their respective lexers; other code blocks
use a uniform code color. Markdown highlighting is a lightweight source lexer, not
a full Markdown parser. Other extensions remain plain.
Lexers run over the buffer after edits and cache the result while navigating;
this favors simplicity over performance on very large files.

When `rust-analyzer` or `gopls` advertises full semantic tokens, Coigüe overlays
them on lexical highlighting to distinguish functions, types, variables, fields,
constants, namespaces, and other supported token classes. Unknown token classes
retain their lexical colors. All colors use the selected theme, including Mocha,
Gruvbox, Latte, Monochrome, and the terminal ANSI palette. Monochrome retains bold
keywords and grayscale semantic colors. Switching themes reuses cached tokens
and repaints immediately. Markdown uses built-in highlighting and LSP diagnostics.

Semantic-token requests run in the background after document synchronization.
Edits clear old tokens and use lexical colors until a current response arrives;
responses for older versions or other files are ignored. Missing, crashed,
unsupported, or unresponsive servers leave lexical highlighting usable. Semantic
requests time out after five seconds without stopping diagnostics. Coigüe supports
full token responses and server refresh requests; range and delta token requests
are not used. Gopls semantic tokens are enabled automatically by Coigüe.

Language-server diagnostics start automatically for these file types when the
corresponding executable is on `PATH`:

| Files | Server command | Workspace root |
| --- | --- | --- |
| `.scm` | `brote-lsp` | Coigüe configuration directory for its init/modules; otherwise the file’s parent directory. |
| `.rs` | `rust-analyzer` | Nearest ancestor containing `Cargo.toml`. |
| `.go` | `gopls` | Nearest ancestor containing `go.work` or `go.mod`. |
| `.md`, `.markdown` (case-insensitive) | `marksman server` | Nearest ancestor containing `.marksman.toml` or `.git`. |

Without a matching workspace marker, the file's parent directory is used.
For Scheme, install Brote’s server with `go install github.com/fclaude/brote/cmd/brote-lsp@latest`, or install the `brote` Beauties package. It provides syntax diagnostics and lexical navigation without executing the file. Coigüe sends its generated host API profile only for files in its configuration directory. Module import navigation, completion UI and hover UI are not implemented yet. Regenerate API metadata with `go generate ./internal/lsp` after changing host primitives.

Install whichever servers you need separately. No server is bundled or downloaded
by the editor. Welcome and other file types start no server.

For Rust with a rustup-managed toolchain, install
[rust-analyzer](https://rust-analyzer.github.io/book/rust_analyzer_binary.html#rustup)
and the standard-library sources for the toolchain used by your project:

```sh
rustup component add rust-analyzer rust-src
```

Make sure `$HOME/.cargo/bin` is on `PATH`. If your project uses a toolchain
override, run the command from that project so the components match its toolchain.

For Go, install a current Go toolchain, then install
[gopls](https://go.dev/gopls/#installation):

```sh
go install golang.org/x/tools/gopls@latest
export PATH="$(go env GOPATH)/bin:$PATH"
```

If you set `GOBIN`, add that directory to `PATH` instead. The `go` executable
must also be on `PATH` when running Coigüe.

For Markdown, install [Marksman](https://github.com/artempyanykh/marksman/blob/main/docs/install.md).
On macOS or Linux with Homebrew:

```sh
brew install marksman
```

Without Homebrew, download the binary matching your OS and CPU from the
[Marksman releases](https://github.com/artempyanykh/marksman/releases), rename it
to `marksman`, then run these commands from the download directory:

```sh
mkdir -p "$HOME/.local/bin"
install -m 755 ./marksman "$HOME/.local/bin/marksman"
export PATH="$HOME/.local/bin:$PATH"
```

Keep any needed `PATH` additions in your shell startup file (for example,
`~/.zshrc` or `~/.bashrc`). Before launching Coigüe, check that the servers you
installed are discoverable from the same terminal:

```sh
command -v rust-analyzer
command -v gopls
command -v marksman
```

Restart Coigüe after changing its inherited `PATH`. If a server was installed
into a directory already on its `PATH`, use `:lsp-restart` to retry in the current
session. Coigüe starts each server automatically; do not launch `marksman server`
manually alongside the editor.

The line-number separator becomes a colored `E`, `W`, `I`, or `H` for an error,
warning, information, or hint, without moving the text. The status row shows the
server and error/warning counts. The message row displays the diagnostic under
the cursor, falling back to the highest-severity diagnostic on the current line;
commands, prompts, and editor messages take priority. Diagnostics arrive while
idle as well as during editing. Marksman provides Markdown link/reference
diagnostics; it is not a general Markdown style checker.

The active document is synchronized on open, edits (including undo/redo),
successful saves, file switches, and renames. The client honors the server's
full/incremental synchronization and save capabilities; incremental updates
currently replace the previous document's entire range. UTF-16 protocol positions
are converted to the buffer's Unicode character positions. Editing clears old
diagnostics, and publications with an outdated document version are ignored.
Servers that omit versions cannot always distinguish delayed diagnostics from
the current text. Only the current file's diagnostics are shown.

Servers run in the background, with one active server reused within the same
language/workspace. Missing or crashed servers clear their diagnostics and show
a status message; editing and saving continue. Startup times out after 20 seconds.
Use `:lsp-restart` to retry after installing a server or recovering from a failure.
File switches close the old document; changing language/workspace stops the old
server. Shutdown has a bounded wait and unresponsive children are killed/reaped.
Server output never writes into the terminal UI.

This integration supports diagnostics, Rust/Go semantic highlighting, definition
and reference navigation, file symbols, and workspace-symbol search. Completion,
hover, formatting, rename, workspace edits, file watching, and server configuration
are not implemented. The LSP smoke test uses disposable fake servers for deterministic
PTY checks and does not require these language servers to be installed.
