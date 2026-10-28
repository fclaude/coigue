# Setting it up

You need a terminal on Linux or macOS and Go 1.26 or newer. Coigüe is a single program with no runtime dependencies beyond that, and you don't need any other tool for it to work.

## Installing

The quickest way is `go install`:

```sh
go install github.com/fclaude/coigue/cmd/coigue@latest
```

Go puts the program in `$(go env GOPATH)/bin`, which is `~/go/bin` unless you've changed it. If `coigue` isn't found afterwards, that folder isn't on your `PATH` yet.

To build it from the source instead:

```sh
git clone https://github.com/fclaude/coigue.git
cd coigue
make build
```

That makes `bin/coigue`. `go run ./cmd/coigue` starts it without building anything first, which is handy while you're changing the code.

## Checking that it works

```console
$ coigue --version
coigue 0.1.0 (main build development; commit unknown)
$ coigue --help
Usage: coigue [FILE]

Without a file, welcome opens in the current directory.
Ctrl-O / - / :explore browse; Ctrl-P / :open find files; F1 / :help guide; :q quit.

  --version   Show source version, package build, and commit
  --help, -h  Show this help
```

The version line says "development" and "unknown" when nobody gave the build a number or a commit. `make build REVISION=12 COMMIT=$(git rev-parse HEAD)` records both, and `--version` prints them.

## The first start

Run `coigue` with no arguments in a directory and you get a welcome screen:

```text
F2 Files | F3 Code | F1 Help | F4 Actions

Coigüe
Welcome
Directory: /home/you/notes
Ctrl-O / - / :explore   Browse and create files
Ctrl-P / :open         Find a file
F1 / :help            Getting started and key guide
:q                     Quit
```

The welcome screen doesn't edit anything. Press `Ctrl-O` to browse, `Ctrl-P` to find a file, or `:q` and Enter to leave. If you already know the file, give its name:

```console
$ coigue notes.txt
```

A name that doesn't exist yet opens an empty buffer, and the first `:w` creates the file. The folder it lives in has to exist already, or the save fails with "No such file or directory". `:q` quits, and it refuses while there are unsaved changes, so `:wq` saves and quits and `:q!` throws the changes away.

## The terminal

The four built-in colour themes use 24-bit colour. Most terminals on a recent system have it, and `echo $COLORTERM` prints `truecolor` or `24bit` when yours does. If the colours look wrong, type `:theme terminal` and Enter, which uses your terminal's own default colours and palette.

Files have to be UTF-8. A file with Windows line endings opens and saves with them untouched, but the carriage returns show up as a replacement character.

## Your own commands and themes

Coigüe reads one optional file at start-up: `init.scm`, in `$XDG_CONFIG_HOME/coigue/`, or in `~/.config/coigue/` when that variable isn't set. That file can require modules from the `modules` directory next to it, and from nowhere else. A missing file is normal and says nothing. If the file has an error, the status line tells you and the editor still starts. It never reads an `init.scm` from the folder you're working in, so opening someone else's project can't run anything. [Writing an extension](writing.md) walks through a command and a module, [Scheme and themes](extending.md) is the full list of what a script can do, and `:reload` reads the file again without restarting. `docs/examples/sample.scm` is a complete file you can copy to that path.

## Optional extras

Copying to the system clipboard, which is separate from the editor's own yank register, needs a tool on your system. macOS has `pbcopy` and `pbpaste`. On Linux, Wayland needs `wl-clipboard` and X11 needs `xclip` or `xsel`. Without one, `y` and `p` still work inside the editor.

Language servers are separate programs too. Install `gopls` for Go, `rust-analyzer` for Rust or Marksman for Markdown, and make sure they're on your `PATH`.
