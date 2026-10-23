# Coigüe

Coigüe is a small terminal editor written in Go. It opens one file at a time, and if you've used vi the everyday keys will feel familiar: `h j k l` to move, `i` to type, `dd` to delete a line, `u` to undo. You can write your own commands, key bindings and themes in Scheme, which [Brote](https://github.com/fclaude/brote) runs for it. It started life in Rust and was ported to Go, and the Go version reproduces 42 recorded terminal sessions of the Rust one byte for byte.

## What it does

You move around in Normal mode and type in Insert mode. There's undo and redo, counts (`3x`, `2dd`), a few word and line commands, character and line selection with yank and delete, and search within a file. `Ctrl-P` finds a file by typing a few letters of its path, and `Ctrl-O` opens a directory browser where you can create, rename, trash and restore files. `F5` searches the text of the whole project.

If you have a language server installed (`gopls`, `rust-analyzer` or Marksman), Coigüe uses it for diagnostics and to jump to definitions and references. Without one, editing and saving work the same and those lists stay empty.

The colours come from five built-in themes, and `F1` opens a guide inside the editor.

## What it doesn't do

- It edits one file at a time. There are no tabs, splits or multiple buffers.
- It has no settings file. The only file it reads at start-up is `init.scm`, which holds your Scheme commands, key bindings and themes.
- Scheme works on the text of the file you're editing. It can bind single keys in Normal and Visual mode, but there are no key sequences, plugins or packages, and a script can't read other files, run programs or use the network.
- It edits characters, so a letter and its combining accent are two things to the cursor.
- It has no mouse support.
- It's tested on Linux and macOS. Windows and the BSDs aren't targets.

## Where to go from here

[Setting it up](setup.md) installs it and shows what the first start looks like. [Using it](using.md) walks through a practice file, one key at a time, and [Keys and commands](keys.md) is the same material as a reference. If you want your own commands, keys or colours, [Scheme and themes](extending.md) has the rules and the exact list of what a script can do.
