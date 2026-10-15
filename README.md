# Coigüe

A small Rust terminal modal editor. One UTF-8 file per session; no interpreter integration.

Install a current stable Rust toolchain (Rust 2024 edition), then:

```sh
git clone https://github.com/fclaude/coigue.git
cd coigue
cargo run -- path/to/file
```

Linux and macOS terminals are the intended platforms. The Rust code uses portable standard-library, crossterm, tempfile and unicode-width APIs; Unix-specific permission/symlink tests are gated with `cfg(unix)`. This first version was built and exercised on Linux; macOS compilation and runtime have not been tested.

A nonexistent path starts an empty buffer. Parent directories must already exist.

Bindings:

- Normal mode: `h j k l`, arrows, `0`, `$`, `gg`, `G`; `i` insert, `a` append, `o` open a line below; `x` delete a character, `dd` delete a line.
- Insert mode: text, Enter, Tab, Backspace, arrows. Esc returns to normal mode.
- Command mode: `:w`, `:q`, `:wq`, `:q!`, Enter to execute; Esc cancels. `:q` refuses unsaved changes; failed writes never quit.

Lines scroll vertically and horizontally; terminal resize triggers a redraw. LF and existing trailing newlines are preserved exactly; opening a file does not add a newline. CRLF bytes round-trip unchanged, but CR is displayed as a replacement glyph and newly inserted line breaks use LF.

Saving compares the file's bytes with the last loaded/saved snapshot, writes and syncs a same-directory temporary file, then atomically replaces the destination. Failed writes leave existing contents intact. New files are created without clobbering an existing destination. Existing symlinks are resolved at opening, so later changes to the original link do not redirect the session. Saves refuse read-only destinations or a resolved target replaced by a symlink. Unix permission bits are preserved; new files use private temporary-file permissions (0600 on Unix). Atomic replacement changes inode identity (including hard-link relationships); ownership, ACLs and extended attributes are not preserved. If another program writes after the final byte comparison but before the atomic rename, its update can still be overwritten without detection; this is not a locking editor. Use one writer per file. No force-write command bypasses conflicts.

Limits: in-memory files, scalar-value rather than grapheme-cluster editing (combining marks may be edited independently), fixed four-column tabs, terminal-dependent emoji/combining display width. No undo, counts, clipboard, search, multiple buffers or configuration yet. Raw mode, cursor and alternate screen are restored on normal exits, returned errors and Rust panics; uncatchable termination cannot run cleanup.

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
The lexer runs over the buffer on each redraw to keep edits and scrolling correct;
this favors simplicity over performance on very large files. It does not perform
semantic analysis or use an LSP. Colors use the terminal's ANSI palette.
