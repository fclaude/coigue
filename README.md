# Coigüe

A small Rust terminal modal editor. One UTF-8 file at a time; no interpreter integration.

Install a current stable Rust toolchain (Rust 2024 edition), then:

```sh
git clone https://github.com/fclaude/coigue.git
cd coigue
cargo run -- path/to/file
```

Linux and macOS terminals are the intended platforms. The Rust code uses portable standard-library, crossterm, tempfile and unicode-width APIs; Unix-specific permission/symlink tests are gated with `cfg(unix)`. This first version was built and exercised on Linux; macOS compilation and PTY integration checks have also been exercised.

A nonexistent path starts an empty buffer. Parent directories must already exist.

Bindings:

- Normal mode: `h j k l`, arrows, `0`, `$`, `gg`, `G`; `i` insert, `a` append, `o` open a line below; `x` delete a character, `dd` delete a line.
- Insert mode: text, Enter, Tab, Backspace, arrows. Esc returns to normal mode.
- Normal and insert modes: Home / Ctrl-A begins a line; End / Ctrl-E ends a line
  (after the last character in insert mode). Page Up / Ctrl-B and Page Down / Ctrl-F
  move by the current viewport height, excluding the two status/message rows.
- Command mode: `:w`, `:q`, `:wq`, `:q!`, Enter to execute; Esc cancels. `:q` refuses unsaved changes; failed writes never quit.

Additional normal-mode editing commands:

| Keys | Action |
| --- | --- |
| `u` / Ctrl-R | Undo / redo the last edit group. |
| `yy` | Copy the current line into the internal register. |
| `dd` | Cut the current line into the register. |
| `x` | Cut the character under the cursor into the register. |
| `p` / `P` | Paste a copied/cut line below/above, or a cut character after/before the cursor. |
| `r` then a character | Replace the character under the cursor; Esc cancels. Tab is also accepted. |
| `/` then text, Enter | Find the next literal, case-sensitive match in the file. Esc cancels. |
| `n` / `N` | Find the next/previous occurrence of the last search, wrapping at the file boundaries. |
| `>>` / `<<` | Indent by four spaces / remove one leading tab or up to four leading spaces. |
| `A` | Enter insert mode after the last character of the current line. |

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
explorer. It opens at the current file's parent and selects that file, revealing
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

Lines scroll vertically and horizontally; terminal resize triggers a redraw.
Screen updates are buffered and repaint only changed rows, with synchronized updates
on terminals that support them. LF and existing trailing newlines are preserved exactly; opening a file does not add a newline. CRLF bytes round-trip unchanged, but CR is displayed as a replacement glyph and newly inserted line breaks use LF.

Saving compares the file's bytes with the last loaded/saved snapshot, writes and syncs a same-directory temporary file, then atomically replaces the destination. Failed writes leave existing contents intact. New files are created without clobbering an existing destination. Existing symlinks are resolved at opening, so later changes to the original link do not redirect the session. Saves refuse read-only destinations or a resolved target replaced by a symlink. Unix permission bits are preserved; new files use private temporary-file permissions (0600 on Unix). Atomic replacement changes inode identity (including hard-link relationships); ownership, ACLs and extended attributes are not preserved. If another program writes after the final byte comparison but before the atomic rename, its update can still be overwritten without detection; this is not a locking editor. Use one writer per file. No force-write command bypasses conflicts.

Limits: in-memory files, scalar-value rather than grapheme-cluster editing (combining marks may be edited independently), fixed four-column tabs, terminal-dependent emoji/combining display width. No counts, system clipboard, multiple buffers or configuration yet. Raw mode, cursor and alternate screen are restored on normal exits, returned errors and Rust panics; uncatchable termination cannot run cleanup.

Development checks:

```sh
cargo fmt --check
cargo test
cargo clippy --all-targets -- -D warnings
cargo build
python3 tests/pty_smoke.py target/debug/coigue
```

The Unix PTY smoke test uses only disposable files and checks save/reopen, repeated edits, resize, quit safeguards and terminal restoration after errors and unwinding. The additional `/dev/full` output-failure test runs on Linux and is skipped when that device is unavailable (including macOS).

Rust files (`.rs`) receive lightweight lexical highlighting for keywords, comments,
strings, characters, lifetimes, numbers, and macros. Nested block comments and
multiline/raw strings retain state across lines. Other extensions remain plain.
The lexer runs over the buffer after edits and caches the result while navigating;
this favors simplicity over performance on very large files. It does not perform
semantic analysis or use an LSP. Colors use the terminal's ANSI palette.
