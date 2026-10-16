# Coigüe

A small Rust terminal modal editor. One UTF-8 file at a time; no interpreter integration.

Licensed under the [MIT License](LICENSE).
Built-in theme attribution and license terms are in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md); include these notices with
distributions containing the palettes.

Install a current stable Rust toolchain (Rust 2024 edition), then:

```sh
git clone https://github.com/fclaude/coigue.git
cd coigue
cargo run --                 # welcome in the current directory
cargo run -- path/to/file
```

## Compatibility

Linux and macOS terminals are the intended platforms. [GitHub Actions CI](.github/workflows/ci.yml)
tests Linux x86-64, macOS Intel, and macOS Apple Silicon natively, including
formatting, unit tests, Clippy, builds, and both terminal and language-server
PTY integration suites.

| System | Status | Evidence and limitations |
| --- | --- | --- |
| Linux x86-64 | Tested in CI | Native `ubuntu-24.04` runner; builds, unit tests, and both PTY integration suites pass. |
| macOS Intel | Tested in CI | Native `macos-15-intel` runner; builds, unit tests, and both PTY integration suites pass. |
| macOS Apple Silicon | Tested in CI | Native `macos-15` ARM64 runner; builds, unit tests, and both PTY integration suites pass. |
| Linux ARM64 and other architectures | Unverified | No architecture-specific application code, but compilation and runtime behavior have not been verified. |
| Windows native | Not an intended platform | Core editing is unverified; explorer rename, delete, and restore operations explicitly fail. |
| BSD and other Unix systems | Not an intended platform | Core editing is unverified; explorer rename, delete, and restore operations explicitly fail. |

CI coverage applies to the runner systems listed above; minimum OS versions
are not documented. Unix-specific permission and symlink tests are gated with
`cfg(unix)`; the Python PTY integration tests require Unix.

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
internal to this session and does not access the system clipboard. Searches stay
within a line and report missing matches without moving the cursor. An empty
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
file keeps the selection. There is no config file or persistence: restarting
returns to Mocha. Built-in palettes use 24-bit RGB colors and need a truecolor
terminal for faithful rendering; `terminal` retains the previous ANSI behavior.

Lines scroll vertically and horizontally; terminal resize triggers a redraw.
Screen updates are buffered and repaint only changed rows, with synchronized updates
on terminals that support them. LF and existing trailing newlines are preserved exactly; opening a file does not add a newline. CRLF bytes round-trip unchanged, but CR is displayed as a replacement glyph and newly inserted line breaks use LF.

Saving compares the file's bytes with the last loaded/saved snapshot, writes and syncs a same-directory temporary file, then atomically replaces the destination. Failed writes leave existing contents intact. New files are created without clobbering an existing destination. Existing symlinks are resolved at opening, so later changes to the original link do not redirect the session. Saves refuse read-only destinations or a resolved target replaced by a symlink. Unix permission bits are preserved; new files use private temporary-file permissions (0600 on Unix). Atomic replacement changes inode identity (including hard-link relationships); ownership, ACLs and extended attributes are not preserved. If another program writes after the final byte comparison but before the atomic rename, its update can still be overwritten without detection; this is not a locking editor. Use one writer per file. No force-write command bypasses conflicts.

Limits: in-memory files, scalar-value rather than grapheme-cluster editing (combining marks may be edited independently), fixed four-column tabs, terminal-dependent emoji/combining display width. No system clipboard, multiple buffers or configuration yet. Raw mode, cursor and alternate screen are restored on normal exits, returned errors and Rust panics; uncatchable termination cannot run cleanup.

Development checks:

[GitHub Actions CI](.github/workflows/ci.yml) runs the checks below on pushes,
pull requests, and manual dispatches using stable Rust and Python 3.13. Jobs run
natively on Linux x86-64 (`ubuntu-24.04`), macOS Intel (`macos-15-intel`), and
macOS Apple Silicon (`macos-15`), including both PTY integration suites.

```sh
cargo fmt --check
cargo test
cargo clippy --all-targets -- -D warnings
cargo build
python3 tests/pty_smoke.py target/debug/coigue
python3 tests/lsp_smoke.py target/debug/coigue
```

The Unix PTY smoke test uses only disposable files and checks save/reopen, repeated edits, resize, quit safeguards and terminal restoration after errors and unwinding. The additional `/dev/full` output-failure test runs on Linux and is skipped when that device is unavailable (including macOS).

Rust (`.rs`), Go (`.go`), and Markdown (`.md`, `.markdown`) files receive built-in
lexical highlighting immediately, including when no language server is installed.
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
| `.rs` | `rust-analyzer` | Nearest ancestor containing `Cargo.toml`. |
| `.go` | `gopls` | Nearest ancestor containing `go.work` or `go.mod`. |
| `.md`, `.markdown` (case-insensitive) | `marksman server` | Nearest ancestor containing `.marksman.toml` or `.git`. |

Without a matching workspace marker, the file's parent directory is used.
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

This integration supports diagnostics and Rust/Go semantic highlighting. Completion, hover, navigation,
formatting, rename, workspace edits, file watching, and server configuration are
not implemented. The LSP smoke test uses disposable fake servers for deterministic
PTY checks and does not require these language servers to be installed.
