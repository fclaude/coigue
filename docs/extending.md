# Extending Coigüe with Scheme

Coigüe embeds [Brote](https://github.com/fclaude/brote), a small Scheme for Go programs. A
Scheme program in your own configuration file can define editor commands: `:name` runs a Scheme
procedure that reads the text you selected and asks the editor to change it. The same file can
define themes and pick the one the editor starts in (see *Themes* below). It can also bind a key
to a procedure (see *Key bindings*), run one procedure when the editor starts, and be read again
without restarting (see *Start-up and reloading*). A walk through writing one is
[Writing an extension](writing.md). This page is the contract for that capability.
Each statement is tested; the last section says where.

Scheme runs in three ways: as a `:name` command you type, from a key you bound, and at start-up.
It is loaded from your `init.scm`, which can require modules that live next to it (see *Modules*),
and it has a small set of host operations. There is no package manager, installer or debugger, no
key sequences, and nothing runs unless you typed a command, pressed a key you bound, or started
the editor.

Brote's language is documented in its own repository (`docs/scheme.md`, `docs/embedding.md`); this
page documents only what Coigüe adds to it.

## A first command

`~/.config/coigue/init.scm` (or `$XDG_CONFIG_HOME/coigue/init.scm`):

```scheme
(define (wrap open close)
  (replace-selection (string-append open (selection-text) close)))

(define-command "wrap" wrap
  "Wrap the selection in OPEN and CLOSE, for example :wrap ( ) or :wrap ** **")
```

A copy of this file is `docs/examples/init.scm`; a test loads it, so the example cannot drift from the
implementation. A command is completed with Tab like a built-in one (`:wr` Tab gives `:wrap`; the
defined commands follow the built-in ones, in the order `init.scm` defined them), and F1 shows
one more guide topic, "Your commands and keys", listing each with its description. Without `init.scm`
the guide is exactly what it was.

`docs/examples/sample.scm` is a complete `init.scm`: this command, the later examples, and two more
keys. A test loads it.

Select text with `v` or `V`, press `!`, type `wrap ( )` and Enter. The selection is now surrounded by
parentheses. (`!` opens the usual command line, shown as `:`, and keeps the selection for the one
command you type. `:` itself is still ignored in Visual mode, as it always was; see *Differences
from the Rust editor*.)

### Exactly what `:wrap OPEN CLOSE` does

* Arguments. The words after the command name, split on whitespace, each passed to the procedure
  as a Scheme string. There is no quoting: `:wrap " "` passes the one-character string `"` twice,
  and an argument cannot contain a space. The procedure here takes exactly two; with another number
  Scheme reports `wrap: expected 2 argument(s), got 1` and nothing changes.
* What is read. The selection, through `selection-text`: the characters from one end of the
  selection to the other, inclusive of both ends, exactly as `d` would cut them in Visual mode.
  A `v` selection can span lines and includes each newline it crosses (and the newline of an
  empty line it ends on); a `V` selection is whole lines joined with `\n`, without a trailing
  newline. Text is read in characters (Unicode scalar values), so multibyte text is never split.
* What is changed. `replace-selection` replaces the selected characters (for `V`: the text of
  the selected lines, leaving the newline after the last one alone) with the given string. Nothing else in
  the buffer changes.
* After the command. The editor is in Normal mode and the selection is gone. The cursor is on
  the first character of the inserted text (like every Normal-mode cursor it stays on a character
  of its line, so for text that starts with a newline it is the last character before it). A
  command that makes no edit leaves the cursor where it was. The status line says `Ran :wrap`, or the last text the
  procedure passed to `message`. The screen is not redrawn while the procedure runs (see
  *Cancellation*).
* Undo. The whole command is one undo step. `u` restores the text exactly as it was before the
  command and puts the cursor where it was when you pressed Enter (the end of the old selection);
  `Ctrl-R` applies the whole command again. A command that leaves the text identical (for
  example, replacing the selection with itself) creates no undo step and keeps the redo history.

### Failures

Every failure has the same outcome: the buffer, the cursor, the undo and redo history, the
registers and the file on disk are exactly as they were, and the editor is back in Normal mode with
the selection gone. The status line says why. The editor stays responsive and the terminal intact.

| Situation | Status line (after the `:wrap` prefix) |
| --- | --- |
| No selection (`:wrap ( )` typed from Normal mode) | `failed: selection-text: no selection; select text with v or V, then press !` |
| Selection longer than 65,536 characters | `failed: selection-text: selection is 70000 characters; the limit is 65536` |
| Scheme error in the procedure (`(error "boom" 1)`, unbound variable, wrong argument count) | `failed: ` and Brote's message |
| Wrong argument type, for example `(replace-selection 42)` | `failed: replace-selection: ` and Brote's type message |
| Edit rejected: text that is not valid UTF-8 (`"\xff"`) | `failed: replace-selection: text is not valid UTF-8` |
| Edit rejected: replacement longer than 65,536 characters | `failed: replace-selection: text is 70000 characters; the limit is 65536` |
| Edit rejected: more than 1,048,576 characters inserted by one command | `failed: insert-text: this command has inserted more than 1048576 characters` |
| Selection used after an edit in the same command | `failed: selection-text: the selection is gone after an earlier edit in this command` |
| `(load "other.scm")` or any other operation that touches the operating system | `failed: load: denied: ...` |
| Interrupted with Ctrl-C | `interrupted; no changes made` |
| Still running after 5 seconds | `stopped after 5s; no changes made` |
| Using more than 128 MiB of memory | `stopped: used more than 128 MiB of memory; no changes made` |
| No file is open (the welcome screen) | `No file open` (the procedure is not run) |
| The procedure panics the interpreter (a bug) | `failed: internal error: ...` |

A rejected edit is an error raised by the host operation, so a Scheme `guard` inside the procedure
can catch it and carry on; the rejected edit was not applied, and the rest of the command still
commits normally or not at all.

## The host interface

These are the only operations Scheme can use beyond Brote's own language (numbers, strings, lists,
vectors, `guard`, `format`, `json-parse`, ...). Everything else Brote offers that reaches outside the
interpreter is refused (see *Trust*).

| Operation | Does | Available |
| --- | --- | --- |
| `(define-command name proc [doc])` | Defines `:name` as a command that applies `proc` to its arguments. `name` is 1 to 32 characters: a lowercase letter, then lowercase letters, digits and `-`. It must not be a built-in command or already defined in the same file, and at most 100 commands can be defined. Two modules may use the same name (see *Modules*). `doc` is a one-line description. | only while `init.scm` loads |
| `(define-theme name spec)` | Defines the theme `name`, which `:theme` and `set-theme` can then use. `name` follows the rule for command names, cannot be the name or alias of a built-in theme or of a theme already defined, and at most 16 themes can be defined. A module's theme is stored as `module/name`. `spec` is a list of `("key" . value)` pairs (see *Themes*). | only while `init.scm` loads |
| `(set-theme name)` | Starts the editor in the theme `name`, a built-in one or one defined earlier. An unknown name is an error that lists the choices. Only `init.scm` may call it. | only while `init.scm` loads |
| `(bind-key mode key proc [doc [override]])` | Binds `key` in `mode` (`"normal"` or `"visual"`) to the procedure `proc`, which runs as a command does, with no arguments. See *Key bindings* for the key syntax and for which keys can be bound. At most 100 keys can be bound. Only `init.scm` may pass `#t` for `override`. | only while `init.scm` loads |
| `(on-start proc)` | Registers `proc` to run once, as a command does, when the editor has started and a file is open. Once in `init.scm` and once in each module. See *Start-up and reloading*. | only while `init.scm` loads |
| `(selection?)` | `#t` if the command was started from a Visual selection and no edit has happened yet. | in a command |
| `(selection-text)` | The selected text (see above). Error if there is no selection, it is longer than 65,536 characters, or an edit already happened in this command. | in a command |
| `(buffer-text start [end])` | The text of lines `start` to `end` (1-based, inclusive; `end` defaults to `start`) of the current buffer as it is at this point of the command (edits already made are visible), joined with `\n` and without a trailing newline. Error if a line is outside the buffer or the text is longer than 65,536 characters. | in a command |
| `(line-count)` | The number of lines in the buffer. | in a command |
| `(cursor-line)` | The 1-based line of the cursor (the end of the selection when there is one). | in a command |
| `(cursor-column)` | The 1-based column of the cursor, counted in characters. | in a command |
| `(buffer-path)` | The path of the file being edited, or `""` when the buffer has no file. | in a command |
| `(mode)` | The mode the command was started from: `"normal"`, `"visual"` (`v`) or `"visual-line"` (`V`). A binding reports the mode it fired in, and the start-up procedure reports `"normal"`. | in a command |
| `(selection-range)` | The selection as the list `(start-line start-column end-line end-column)`, 1-based and inclusive at both ends, in document order whichever way the selection was made. A `V` selection runs from column 1 of its first line to the last character of its last. `#f` when there is no selection or an edit has ended it. | in a command |
| `(word-at-cursor)` | The word under the cursor: the letters, digits and underscores around it on its line. `""` when the cursor is on anything else. | in a command |
| `(find text [line [column]])` | The first place `text` occurs at or after `line` and `column` (both default to the start of the buffer), as the list `(line column)`, or `#f`. The search is literal, case-sensitive and does not wrap. `text` is not empty, has no newline and is at most 65,536 characters; a `column` can be one past the end of its line. | in a command |
| `(replace-selection text)` | Replaces the selection with `text` (see above). Error if there is no selection or an edit already happened. Cursor on the first character of `text`. | in a command |
| `(insert-text text)` | Inserts `text` before the cursor character, whether or not there is a selection. The cursor ends on the first character of `text`. A later `selection-text` or `replace-selection` fails. | in a command |
| `(replace-lines start end text)` | Replaces lines `start` to `end` (1-based, inclusive) with `text`, which can hold any number of lines. See *Editing beyond the selection*. | in a command |
| `(delete-lines start end)` | Deletes lines `start` to `end`. | in a command |
| `(insert-at line column text)` | Inserts `text` before the character at `line` and `column`. | in a command |
| `(set-cursor line column)` | Moves the cursor. | in a command |
| `(message text)` | Sets the status line shown when the command finishes successfully; the last call wins. Control characters become spaces and the text is cut at 500 characters. | in a command |

"In a command" means: while the editor is running a `:name` command, a bound key or the start-up
procedure. Called any other way (while `init.scm` loads, or by anything that is not an invocation the
editor started) these operations raise `name: not inside an extension command`; `define-command`,
`define-theme`, `set-theme`, `bind-key` and `on-start` outside loading raise
`define-command: only allowed while init.scm loads` (with their own name).

Line numbers and columns in the interface are 1-based, and every size is in characters. A column counts
characters, so a tab or a wide character is one column, whatever it takes on the screen. The questions
that return a position return it as a list, which `car` and `cadr` take apart.

## Editing beyond the selection

The four edit operations work on any lines, selection or not. They change the command's private copy
of the buffer like `insert-text` does, they count against the same limits (65,536 characters per
text, 1,048,576 per command), and they refuse text that is not valid UTF-8. A line or column outside
the buffer is an error that says what the range is, for example
`failed: insert-at: column 9 is outside 1..4 on line 2`, and changes nothing.

* `(replace-lines start end text)` swaps lines `start` to `end` for `text`, which is the new content of
  those lines: several lines separated by `\n`, with no newline after the last one. `""` leaves one
  empty line, since the lines were replaced rather than deleted. It reads back what `buffer-text`
  returns, so `(replace-lines 3 5 (buffer-text 3 5))` changes nothing. The cursor ends on the first
  character of `text`.
* `(delete-lines start end)` removes the lines and the newlines between them and the lines around.
  Deleting every line leaves one empty line. The cursor ends on the first column of the line that took
  the place of the first deleted one, or of the last line when there is none.
* `(insert-at line column text)` inserts before the character at that position. The column one past the
  end of a line appends to it, and `text` can contain newlines. The cursor ends on the first character of
  `text`, as with `insert-text`.
* `(set-cursor line column)` moves the cursor, and only that. The column one past the end of a line puts
  the cursor on its last character, since that is as far as the cursor goes in Normal mode. A command that
  only moves the cursor leaves the text, the undo history and the redo history as they were.

Within one command these combine in the order they run, and line numbers always mean the buffer as it
is at that moment: after `(delete-lines 1 1)` the old second line is line 1. The selection ends at the
first change to the text, as it always did. `set-cursor` is not a change to the text, so the selection
survives it and now runs from where it started to the new cursor: `selection-text` and
`selection-range` read that. The whole command, however many of these it makes, is one undo step.

## Key bindings

`bind-key` makes a key run a procedure, as if you had typed a command:

```scheme
(bind-key "normal" "<C-d>" duplicate-line "Copy the current line below it")
```

`docs/examples/keys.scm` has this and two more, and a test loads it. The arguments are the mode, the
key, the procedure, an optional one-line description for the guide, and an optional `#t` to override a
key the editor already uses (see below).

### Modes

There are two: `"normal"`, and `"visual"` for both `v` and `V`. Insert mode, the search prompt and the
command line take every key as text, so a bound key could only get in the way of typing. The welcome
screen, the file explorer, the file picker, the help guide, the menus, the result lists and the
confirmation prompts handle their own keys, and bindings do not apply there.

### Which keys

A key is a string in one of three forms:

| Form | Key | Examples |
| --- | --- | --- |
| one visible character | that character, capitals included | `"x"`, `"X"`, `"ä"`, `"?"` |
| `<C-` letter `>` | Ctrl and a lowercase letter | `"<C-d>"` |
| `<A-` character `>` | Alt and one visible character | `"<A-j>"`, `"<A-X>"` |

Anything else is an error at load time that says what a key looks like. Two things have no form on
purpose.

Key sequences (a chord such as `gx` or `,w`) are left out. After `d`, `g`, `r`, `>`, `<` or a count,
the next key already means something, and `rX` replaces a character with an X. A chord would need
those to give way, and then a key typed to cancel an operator could start running a script. Single
keys can't clash that way, and the editor is no worse off for it: a procedure can do what a chord
would, and a Ctrl or Alt key is free in most places. A binding also never runs while an operator or a
character to replace is pending. The key then does what it always did there, which is to cancel the
operator or be the replacement, and a count typed before a bound key is dropped.

The special keys (arrows, Tab, Enter, Backspace, the function keys) have no form either. Terminals
send Ctrl-I as Tab, Ctrl-M as Enter, Ctrl-J as a newline and often Ctrl-H as Backspace, so a binding
on those would shadow a key you meant something else by. Ctrl-C is how you stop a runaway command,
Ctrl-Z suspends the editor and Ctrl-S and Ctrl-Q belong to the terminal, so `<C-c>`, `<C-z>`, `<C-s>`,
`<C-q>`, `<C-i>`, `<C-m>`, `<C-j>` and `<C-h>` are errors with the reason. The editor keeps F1 to F9 and
Alt-Left and Alt-Right.

### Keys the editor already uses

A key with a built-in meaning in the mode is an error unless the fifth argument is `#t`, and the
message says what the key does:

```
init.scm: failed: bind-key: "x" already has a meaning in normal mode (it is a Normal command); pass #t as the override argument to replace it
```

| Mode | Keys with a meaning |
| --- | --- |
| normal | `0` to `9`, `h j k l w b e $ g G n N`, `d c y g r > <`, `v V x p P u A i a o / -`, Ctrl-R, and Ctrl-A, Ctrl-E, Ctrl-F, Ctrl-B |
| visual | `0` to `9`, `h j k l w b e $ g G n N`, `v V o y Y d`, and Ctrl-A, Ctrl-E, Ctrl-F, Ctrl-B |

Ctrl-A, Ctrl-E, Ctrl-F and Ctrl-B are Home, End, Page Down and Page Up in every mode. Every other
key, including every Alt key and every other Ctrl key, has no meaning and needs no override. A test
presses every one of them in both modes and checks that the lists above are the whole story, so a
change to the editor that gives one of them a meaning fails there until this table says so.

Four keys can't be bound whatever you pass: `:`, Ctrl-O, Ctrl-P and, in Visual mode, `!`. `:` opens the
command line, which is where `:q` and `:w` live (quitting and saving are commands, so keeping `:` keeps
both), and where `:reload` gets you out of a binding you'd rather not have. Ctrl-O and Ctrl-P open the
file explorer and the file picker. Esc and Ctrl-C have no form, as above. `!` is how a Visual selection
gets to a command.

### What a binding does

It calls the procedure with no arguments, under every rule of a command: a private copy of the buffer,
one undo step, a 5 second deadline, Ctrl-C, the memory limit, and the same failures (see the table
above, where `binding <C-d>` takes the place of `:wrap` in the status line). The status line says
`Ran binding <C-d>` or the last text the procedure passed to `message`. A procedure that wants
arguments fails with Brote's message about the count.

From Visual mode the selection goes to the procedure, as it does with `!`, and ends when the key is
pressed, whether or not the procedure succeeds. `(mode)` says `"visual"` or `"visual-line"`.

### Conflicts

Binding the same key twice in one mode is an error at load time that names both bindings, by their
number in the file and their description:

```
init.scm: failed: bind-key: normal <C-d> is already bound by binding 1 ("Copy the current line"); binding 3 (no description) cannot also use it
```

The same key in the two modes is two independent bindings. `init.scm` stops at the error, as it does
for any other, and the bindings before it stay.

### In the guide

F1 shows the bindings next to the commands, in the topic "Your commands and keys":

```
Keys bound in your init.scm (see docs/extending.md).

normal <C-d>         Copy the current line below it
visual U             Make the selection upper case
```

Without any binding the editor behaves exactly as before: no key goes anywhere new, and the 42 recorded
sessions in `tests/pty_golden.py` replay byte for byte.

## Start-up and reloading

### The start-up procedure

`(on-start proc)` registers a procedure to run once, with no arguments, after the editor has started and
the first file is open. It is a command in everything but how it starts: a private copy of the buffer,
one undo step, the deadline, Ctrl-C, and every read and edit operation. `(mode)` is `"normal"`.
`docs/examples/start.scm` opens log files at their last line.

If the editor started on the welcome screen, with no file, the procedure waits and runs when the first
file is opened. It runs once per session, so opening a second file does not run it again. `init.scm`
can register one, and so can each module; a second call in the same file is an error at load time.
They run in the order the modules loaded, and `init.scm` last. Each is its own command: its own undo
step, its own deadline and its own failure. A failure is `on-start failed: ...` for `init.scm` and
`on-start name failed: ...` for a module, and it does not stop the procedures after it.

An error in the procedure is reported in the status line and nothing else happens to the editor: it
is up and the file is open as it would have been. The procedure leaves the status line alone when it
has nothing to say, so a message that was already there stays.

### `:reload`

`:reload` reads `init.scm` again. The file is evaluated in a fresh interpreter, and only if that
succeeds does the editor switch to it, replacing as one unit:

* the commands, which are removed and defined again (a command missing from the new file is gone);
* the themes, which are the ones the new file defines;
* the key bindings;
* the modules, which are read again from disk;
* the start-up procedures, which are read but not run (see below).

If the file fails to load, for any reason a first load could fail (a syntax error, a Scheme error, a
refused key, a built-in command's name, a second `on-start`, a module that is missing or clashes on a
key, the deadline, Ctrl-C, a file that is missing or too big), nothing has changed. Every command,
theme and binding is exactly as it was, the theme in use is the same one, and the status line says why:

```
init.scm: failed: bad file 7; nothing changed
```

The same trust applies as at start-up: the file is the one from your configuration directory and no
other. The same deadline applies too, and so does Ctrl-C.

What is and is not run again:

* The whole file runs again from the top in a new interpreter. Variables you defined in the old one are
  gone, and the new file computes its own.
* `set-theme` runs again, so it wins over a `:theme` you typed since start-up. If the new file does not call
  it, you stay in the theme you are in when it still exists, and go to Mocha when it doesn't (the status
  line says so).
* The start-up procedures do not run again. The editor is up, and running them twice would repeat whatever
  they did. If the editor is still on the welcome screen, the procedures from the reloaded file are the
  ones that run when you open a file.
* The buffer, its undo history, the registers, the cursor and the selection are not touched.

The status line says `Reloaded init.scm: 3 commands, 2 key bindings` when it worked, and
`Reloaded init.scm: 2 modules, 5 commands, 3 key bindings` when the file required modules.

`:reload` works from the first start even if `init.scm` didn't exist then, so you can create the file and
reload. Tab completes it only once an init file has been found, which keeps the completion list for an
editor without one exactly as it was.

## Modules

A module is a file `modules/NAME.scm` next to `init.scm`, in the same configuration directory and
nowhere else. `init.scm` pulls one in with `require`. This is `docs/examples/modules/`, and a test
loads it.

`modules/text-tools.scm`:

```scheme
(define-module text-tools
  (doc "Wrap and reshape the selection")
  (export wrap))

(define (surround open close)
  (replace-selection (string-append open (selection-text) close)))

(define (wrap open close)
  (surround open close))

(define-command "wrap" wrap "Wrap the selection in OPEN and CLOSE")
(bind-key "visual" "<A-w>" (lambda () (wrap "(" ")")) "Parenthesize")
```

`modules/md-tools.scm` exports its own `wrap`, which bolds the selection, and binds `<A-w>` and `<A-b>`.
`init.scm`:

```scheme
(require text-tools)
(require md-tools (prefix md:))

(define (bold)
  (md:wrap)
  (message "bolded"))

(bind-key "visual" "<A-w>" bold "Bold the selection")
```

`require` and `define-module` are declarations, not procedures. They are read from the top level of a
file before its code runs, and every `require` in the file runs first, in the order written, whether
it sits above or below the definitions. A module loads once per editor session (and once per
`:reload`, which starts from a fresh interpreter), so requiring it from several places evaluates it
once. `(require 'text-tools)` is the same as `(require text-tools)`.

The first form of a module file is `(define-module NAME ...)`, and `NAME` is the file's name. A copy
or a rename cannot pose as another module. `init.scm` has no header; a `define-module` there is an
error that says where the form belongs. A name is a lowercase letter, then lowercase letters, digits
and `-`, up to 32 characters, so it cannot name a path. `init` and `coigue` are reserved.

The header may carry `(doc "one line")` and `(export name ...)`. Exporting a name the module does not
define is an error, and so is exporting a name twice. Everything that is not exported stays inside
the module. `surround` above is private: `init.scm` cannot mention it, and `wrap` still calls it,
because an exported procedure keeps the environment it was defined in.

Options on `require` narrow or rename what is imported. They apply left to right:

| Option | Does |
| --- | --- |
| `(only name ...)` | import just these |
| `(except name ...)` | import everything else |
| `(rename old new)` | import `old` under the name `new` |
| `(prefix p)` | put `p` in front of each name, as in `(prefix md:)` |

`only` and `except` do not go together in one `require`. A name listed twice, or a name the module
does not export, is an error that names it. Importing the same export of the same module twice is
quiet. At most 64 modules are loaded, and each file is a regular file of at most 1 MiB, the same
limit as `init.scm`.

### Visibility and shadowing

Each module, and `init.scm` itself, is evaluated in its own environment. A `(define filter ...)` or a
`(set! car ...)` in one file changes what that file sees and nothing else: not another module, and
not the procedures the language already defined, which keep the bindings they were born with. The
copy is of the value. `(set! n 9)` on an imported `n` changes the importer's `n`, and a `set!` inside
the module that exported it is not seen by importers. Export a procedure when the value should stay
shared. A hash table or a vector is shared by reference, because that is what those values are.

An import never covers a name quietly.

* A name that is a built-in or a host operation is an error: `car is a built-in name; use prefix or rename to import it from text-tools`.
* A name already imported from somewhere else is an error: `wrap is exported by text-tools and md-tools; use prefix, rename or only`.
* A name the file itself defines is an error, found when the file finishes: `wrap is defined in init.scm and also imported from text-tools; use prefix, rename or only`.

Defining the same name as a built-in on purpose is allowed, and it stays inside that file. The guide
notes it (`defines filter, which hides the built-in of that name inside text-tools`). To use an
imported procedure under a built-in name, rename the import and define the name yourself:
`(require strings (prefix str:))` and `(define join str:join)`.

Commands, keys, themes and the start-up procedure are the editor's, not a module's, so they are
resolved once the whole load has finished. The order of the files does not decide a winner.
`init.scm` does.

* **Commands.** A built-in command name is an error, and the message names the module that used it.
  Every command can be typed with its owner in front: `:text-tools/wrap`, and `:init/wrap` for one
  `init.scm` defined. The short `:wrap` runs the command when exactly one module defined it. When
  more than one did, `:wrap` runs nothing and reports `wrap is ambiguous: md-tools/wrap, text-tools/wrap`.
  A `define-command` in `init.scm` takes the short name whatever else defined it, and the others stay
  reachable with their owner in front. The same command defined twice in one file is still an error.
  The limit of 100 is across the whole load, and the message names the module that reached it.
* **Keys.** A key has no qualified form. The same key bound twice in one file is an error, as before.
  Bound by two modules, the load fails, the message names both (`normal X is bound by a ... and b ...; a key has no qualified name, so it is left unbound`), and the key is left unbound. The commands
  from that load stay, at start-up; `:reload` of a file that clashes changes nothing, as any other
  failure does. A binding in `init.scm` wins the key, and the guide notes the module bindings it
  displaced. A key the editor already uses still needs `override`, and only `init.scm` may pass it.
  From a module, `#t` is an error.
* **Themes.** A module's `(define-theme "dusk" ...)` is stored as `tools/dusk`, so two modules cannot
  collide and a module cannot cover a theme `init.scm` defined. `init.scm` keeps short names.
  `(set-theme "tools/dusk")` from `init.scm` starts the editor in it. A module that calls `set-theme`
  is an error.
* **Start-up.** Described above: one procedure per file, load order, `init.scm` last, and a failure
  in one does not skip the rest.

F1's topic "Your commands and keys" grows a Modules section when a module was loaded or a file hid a
built-in: the name, the doc, the exports, the commands and keys the module registered, and the notes
(a hidden built-in, a command that became ambiguous, a key `init.scm` took). With no modules and no
such note the guide is what it was, and the 42 recorded sessions still replay byte for byte.

### What a load reports

An error names the file. A Scheme error in a module is `init.scm: failed: bad.scm: unbound variable: x`.
A missing module names the path it was looked up at, under `modules/` next to `init.scm`. A cycle
names the chain: `module cycle: a -> b -> a`. The deadline, Ctrl-C and the memory limit cover
`init.scm` and every module it is in the middle of requiring, as one load. A module that fails part
way through leaves the commands defined before it, the same way `init.scm` does; `:reload` still
keeps the previous set when anything in the new load fails.

`(load "other.scm")` is still refused. `require` is not a way past that: the name is not a path, and
the file is read by the editor from the one directory, not by Scheme.

## Themes

A theme is a set of colours. The editor comes with five and `:theme` switches between them.
`define-theme` adds one of your own, built from a built-in theme and a few changes, and
`set-theme` picks the one the editor starts in. This is `docs/examples/theme.scm`:

```scheme
; A theme called dusk. It starts from Gruvbox and changes two colours
; and the weight of keywords.
(define-theme "dusk"
  '(("base" . "gruvbox")
    ("keyword" . "#ff79c6")
    ("string" . "#8be9fd")
    ("bold-keywords" . #t)))

; Start the editor with it. :theme mocha (or any other theme) still switches
; while you're editing, and nothing is saved, so the next start is dusk again.
(set-theme "dusk")
```

The spec is a list of pairs whose keys are strings. The key `"base"` names the built-in theme to
start from (any name or alias `:theme` accepts, and `mocha` when it is left out), and a theme can't
start from one that `init.scm` defined. Every other key changes one entry, and the entries we leave
alone keep the colours of the base.

| Key | What it colours |
| --- | --- |
| `background` | the background behind the text |
| `foreground` | plain text, punctuation and variables |
| `muted` | the line numbers |
| `surface` | the status line, and the title and chosen rows of menus and file browsers |
| `keyword` | keywords and Markdown headings |
| `comment` | comments |
| `string` | strings |
| `number` | numbers |
| `macro` | macros and Markdown strong text |
| `lifetime` | lifetimes and Markdown emphasis |
| `type` | types |
| `function` | functions and Markdown links |
| `property` | properties |
| `constant` | constants |
| `namespace` | namespaces |
| `error`, `warning`, `info`, `hint` | the gutter letter of a language-server diagnostic of that severity |
| `bold-keywords` | `#t` or `#f`: whether keywords and macros are drawn bold (Monochrome does) |

A colour is a string of six hex digits after a `#`, in either case, such as `"#ff79c6"`, or
`"default"`, which leaves the choice to the terminal. `bold-keywords` takes a boolean and every
other key takes a colour.

A key that isn't in the table, a colour that doesn't parse, a key given twice and a name that is
taken are errors, and the message names the key: `init.scm: failed: define-theme: keyword: "pink"
is not a colour; use #rrggbb or default`. The theme that had the mistake isn't defined, the themes
before it stay, and `init.scm` stops there like it does for any other error.

`set-theme` takes the name of a built-in theme or of one defined above it. Without a `set-theme`
the editor starts in Mocha, as it always did. Nothing is saved: `:theme` still switches while we
edit, and the next start goes back to what `init.scm` says. `:theme` with no argument lists the
defined themes after the built-in ones, and Tab completes their names.

## How edits are applied

All edits one command requests form one undo group, and a failing command changes nothing. The way
this is guaranteed:

1. Before the procedure runs, the editor makes a private copy of the buffer (text and cursor) and
   remembers the selection.
2. The read and edit operations work on that copy. The live buffer is not touched while Scheme runs,
   so a failure, a timeout or an interrupt has nothing to undo: the copy is dropped.
3. If the procedure returns normally, the editor applies the result to the live buffer in a single
   edit transaction (`Buffer.Edit`), which is one undo step. If the result equals the old text, no
   step is recorded.

Rolling edits back after the fact was rejected: it would bump the buffer revision (waking
highlighting and the language-server sync for text that did not change) and needs a second way to
restore the history. Building the result first needs one copy of the buffer per command, which the
undo history already does for every edit.

Reads see earlier edits of the same command (`buffer-text` after `insert-text` shows the inserted
text). The selection does not survive an edit: the first `insert-text`, `replace-selection`,
`replace-lines`, `delete-lines` or `insert-at` ends it, because the positions it named no longer mean
the same text. `set-cursor` leaves it alone (see *Editing beyond the selection*). A command that only
moves the cursor is applied too, but records no undo step.

## Threading and reentrancy

* One interpreter per editor, created the first time `init.scm` is loaded and used by one
  goroutine at a time. Brote's `*Interp` has no locking. `:reload` makes a second interpreter and uses it
  from the same goroutine, and the editor keeps the new one only if the file loaded.
* Scheme is evaluated on the editor goroutine: the goroutine that calls `Editor.Key`, which in
  the `coigue` command is the main goroutine running the event loop. Only `Editor.Key` (running a
  command, a binding or `:reload`), `Editor.LoadInit` and `Editor.RunStart` evaluate Scheme. The call is synchronous: the editor does nothing
  else until it returns.
* Host operations run on that same goroutine, inside Brote's call, and they touch only the
  invocation's private copy (above), never the live buffer, registers, messages or command table.
  The live state is read before the call starts and written after it returns, both on the editor
  goroutine. An extension therefore cannot change editor state concurrently, because it holds no
  reference to it.
* Other goroutines. While Scheme runs, two goroutines exist, both started through `guard.Go`:
  the input watcher the `coigue` command supplies (it reads the keyboard and calls a `cancel`
  function when it sees Ctrl-C) and a memory watchdog. They call `cancel` and nothing else.
* Accidental callbacks are refused. Every operation first checks three things: the
  evaluation's context carries an invocation the editor started, that invocation is still live
  (it is only between the editor starting the command and the command returning), and the
  operation is running on the goroutine that started it. Go has no portable goroutine identity, so
  the last check reads the goroutine number from the first line of `runtime.Stack`. A procedure
  retained from `init.scm` and applied by anything else, an operation invoked after its command
  ended, one with no context, and one called from another goroutine in the middle of a command
  all fail with `not inside an extension command` and change nothing. (No primitive starts a
  goroutine and Brote has no concurrency of its own, so this is a backstop, tested by calling an
  operation from a second goroutine while the command runs.)
* No reentrancy. No operation starts a command or loads a file. As a backstop, starting an
  invocation while another is running on the same interpreter returns an error instead of racing.

## Cancellation and limits

* Each invocation (and each load of `init.scm`) has a **5 second deadline**.
* **Ctrl-C** interrupts a running command. It is the one key the editor reads while Scheme runs;
  every other key typed meanwhile is kept and handled, in order, after the command finishes.
* A cancelled command is dropped as described above: no changes. Brote checks the context at every
  evaluation step, so `(let loop () (loop))` stops at once and the interpreter is usable
  afterwards, with the definitions it had.
* A **memory watchdog** stops a command whose heap has grown by more than 128 MiB since it started
  (for example repeated `(string-append s s)`). Brote does not meter memory; this is a heuristic
  backstop that polls every 10 ms, so the peak can exceed the limit briefly. It makes data loss from
  a runaway string unlikely, and it can't promise more than that.
* The screen is not redrawn while a command runs. A command that takes visible time shows a frozen
  screen until it finishes, is interrupted, or reaches the deadline.

## Loading and trust

* Scheme is loaded from **one directory**: `$XDG_CONFIG_HOME/coigue/`, or `~/.config/coigue/` when
  `XDG_CONFIG_HOME` is unset, empty or not an absolute path. `init.scm` there is read when the editor
  starts and again when you type `:reload`, and a module only when `init.scm` requires it, from
  `modules/NAME.scm` in that same directory. At no other time, and from no other path.
* It is **never** loaded from the directory of the file you open, the working directory, or any
  project directory, and nothing is run because a repository contains a file. A `init.scm` or a
  `modules` directory next to the file you edit is just files.
* A **missing init file is normal** and says nothing. A file that cannot be read, or an error while it
  loads (a syntax error, a Scheme error, the deadline, Ctrl-C, a module that fails), is reported in
  the status line as `init.scm: ...` and **does not stop start-up**. Commands defined before the error
  stay defined. (`:reload` is stricter: a file that fails changes nothing.)
  Each file must be a regular file of at most 1 MiB.
* The program you write is as trusted as your shell's start-up files, but it is limited to the
  host interface above. In particular, Brote's `load` builtin, which reads any file the process can
  read, is declared a host operation and **a gate refuses every primitive declared `Host`** (and
  every `Egress` or `Irreversible` one). `(load "x.scm")` fails with `load: denied: ...` and the
  file is not read. The set of primitives is frozen after registration, so `register-tool` cannot
  add one. No operation gives access to processes, the network, the file system, environment
  variables, credentials or the clipboard.
* The command line is unchanged: `coigue [FILE]`, `--help`, `--version`. No flag was added, so
  there is nothing that would let a command line (and so a project script) pick a different file.

## Not here

Key sequences, bindings in Insert mode or in the command line, running Scheme from an event other than
the start of the editor, reading or writing other files, LSP data, running a command from Scheme, a
`:eval` command, defining modes. Modules have no package manager, no nested names (`author/name`),
no re-exports, and `require` is not available while a command runs. Each of those needs its own
decision about trust and threading first.

## Differences from the Rust editor

Visual mode has one new key. `!` opens the command line (it looks like `:`) and keeps the selection,
still highlighted, for the one command typed. Enter runs the command; Esc cancels both the command
and the selection. A built-in command run this way ignores the selection and ends it, and so does
a menu action. The Rust editor ignored `!` in Visual mode.

`:` is deliberately left as it was. In Visual mode the Rust editor ignores it, the recorded
sessions in `tests/pty_golden.py` type `:theme terminal` in Visual-line mode and pin that, and so
does the ported editor test for Visual mode. Making `:` do what `!` does would be the natural
change and a one-line one (`visualKey` in `internal/editor/input.go`) if those expectations are
updated. Without an `init.scm` nothing else in the editor changed: the one new command, `:reload`, is
not offered by Tab completion until an init file has been found, and the 42 recorded sessions replay
byte for byte. With one, a bound key in Normal or Visual mode runs its procedure where the Rust editor
ignored it or acted on it (see *Key bindings* for the keys that need an explicit override).

## Building

Coigüe depends on `github.com/fclaude/brote`, at `v0.1.0` (`go.sum` records its hashes). It is a public module, so a
plain `go build ./cmd/coigue` downloads it. Brote is MIT licensed; its notice is in
`THIRD_PARTY_NOTICES.md`, which `scripts/licenses.py` generates.

## What the tests check

Names are Go tests unless a file is given. `ext` is `internal/ext/ext_test.go`, `editor` is
`internal/editor/extension_test.go`.

| Statement | Test |
| --- | --- |
| The example `init.scm` works | `TestExampleInit` (editor) |
| Every host operation is documented here, and nothing else is | `TestHostOperationsAreDocumented` (ext) |
| Reads, edits, cursor and selection results of each operation | `TestHostOperations`, `TestWrapOnMultibyteSelection` (ext); `TestWrapCommand`, `TestWrapLinewise` (editor) |
| `define-command` rules | `TestDefineCommandRules` (ext), `TestLoadInit` (editor) |
| One undo group, one redo; a no-op leaves no step | `TestWrapIsOneUndoStep`, `TestSeveralEditsOfOneCommandAreOneUndoStep`, `TestUnchangedResultLeavesNoUndoStep` (editor); `TestCloneIsIndependentAndAdoptIsOneUndoStep` (buffer) |
| Every failure leaves text, cursor, revision and history alone | `TestFailuresChangeNothing`, `TestFailuresLeaveNoUndoStep` (editor) |
| Accidental callbacks are refused | `TestOperationsRefuseOutsideInvocation`, `TestOperationsRefuseOtherGoroutinesEvenDuringTheInvocation` (ext) |
| The gate refuses `load` and every `Host` primitive; registration is frozen | `TestGateRefusesLoadAndEverythingThatTouchesTheSystem` (ext), `TestLoadInit/load_is_refused_in_the_init_file` (editor) |
| Deadline, Ctrl-C, parent cancellation and the memory limit; the interpreter survives each | `TestDeadlineStopsARunawayLoopAndTheRuntimeSurvives`, `TestInterruptStopsARunawayLoop`, `TestInterruptedLoadKeepsEarlierDefinitions`, `TestParentCancellationStopsARun`, `TestMemoryLimitStopsAStringThatDoublesForever` (ext); `TestDeadlineAndInterruptLeaveTheEditorUsable` (editor) |
| The terminal loop keeps type-ahead while watching for Ctrl-C | `TestWatcherInterruptsOnCtrlCAndKeepsTheOtherEventsInOrder` (`internal/app`) |
| One interpreter, one user at a time; a panic is an error | `TestRunRefusesReentry`, `TestAPanicBecomesAnErrorAndTheRuntimeSurvives`, `TestDeepRecursionIsAnErrorNotACrash` (ext); `go test -race ./...` |
| Where `init.scm` is read from; missing, broken, runaway | `TestInitPath`, `TestReadInit` (ext); `TestLoadInit` (editor) |
| `!` in Visual mode keeps the selection for one command; `:` is still ignored | `TestBangInVisualModeKeepsTheSelectionForOneCommand` (editor) |
| A command survives switching files and edits the active buffer | `TestCommandsSurviveFileSwitchAndUseTheActiveBuffer`, `TestSwitchingFilesWhileACommandLineHoldsASelection` (editor) |
| Completion and the guide treat commands like built-in ones | `TestCompletionAndHelpTreatExtensionCommandsLikeBuiltIns`, `TestHelpListsExtensionCommandsOnlyWhenThereAreSome` (editor) |
| The new read operations, and each edit operation: results, cursor, bounds, limits, UTF-8 | `TestHostOperations` (ext), `TestOnlySetCursorLeavesTheTextAlone`, `TestSeveralEditOperationsInOneCommand` (ext), `TestEditOperationsFromACommandAreOneUndoStep`, `TestSetCursorAloneMovesTheCursorWithoutAnUndoStep`, `TestReadOperationsSeeTheFileAndTheCursor` (editor); `TestSessionPathAndMode` (ext); `TestWordAtIsTheWordRunUnderTheCursor` (buffer) |
| Key syntax and the keys that cannot be written | `TestParseSpec`, `TestParseSpecRefusals` (`internal/key`) |
| `bind-key` rules: modes, conflicts naming both bindings, limit, load-time only | `TestBindKeyRecordsBindingsInOrder`, `TestBindKeyRules`, `TestBindKeyLimit`, `TestBindKeyIsDeniedOutsideLoading` (ext) |
| A binding runs as a command does; failures change nothing; the selection goes to a Visual binding and ends | `TestBindingsRunInNormalMode`, `TestVisualBindingGetsTheSelectionAndEndsIt`, `TestFailedBindingChangesNothing`, `TestRunawayBindingIsStoppedAndTheEditorStaysUsable` (editor); `TestBindingsRunAsACommandDoes` (ext) |
| Bindings stay out of typing, pending operators, the welcome screen and overlays | `TestBindingsStayOutOfTheWayOfTypingAndPendingKeys`, `TestBindingsDoNotRunOnTheWelcomeScreenOrInOverlays`, `TestNormalBindingDoesNotSeeAVisualSelectionAndTheOtherWayRound` (editor) |
| Built-in keys need `override`; `:`, Ctrl-O, Ctrl-P and Visual `!` never bind | `TestBuiltInKeysNeedOverrideAndNeverBoundKeysCannotBeOverridden`, `TestOverrideShadowsTheBuiltInMeaning`, `TestNeverBoundKeysKeepTheirMeaning` (editor) |
| The lists of built-in keys are the whole story, both ways | `TestUnlistedKeysDoNothing`, `TestListedKeysDoSomething` (editor) |
| The start-up procedure: once, after the first file, errors, deadline | `TestStartProcedureRunsOnceWhenTheFirstFileIsOpen`, `TestStartProcedureWaitsForAFileOnTheWelcomeScreen`, `TestStartProcedureFailureIsReportedAndBlocksNothing`, `TestStartProcedureHonoursTheDeadline` (editor); `TestOnStart` (ext) |
| `:reload` replaces commands, themes and bindings as a unit, and a failure changes nothing | `TestReloadReplacesCommandsThemesAndBindingsAsAUnit`, `TestReloadKeepsTheThemeYouSwitchedToWhenItStillExists`, `TestFailedReloadLeavesEverythingAsItWas`, `TestReloadFailureMessagesSayWhy`, `TestReloadDeadlineAndNoLocation`, `TestReloadCreatesTheFirstInit` (editor) |
| Without an init file nothing is visible: `:reload` is not completed, no binding path | `TestReloadCompletesOnlyOnceThereIsAnInit`, `TestBindingsDoNotMoveTheGoldenPaths` (editor); `tests/pty_golden.py` |
| The guide lists bindings next to commands | `TestHelpListsBindingsNextToCommands` (editor) |
| The three example scripts load and do what the page says | `TestExampleInit` (editor) |
| A module's environment is its own: `set!` and `define` of a built-in stay inside it, and the prelude is unchanged | `TestModulesKeepTheirOwnCopyOfTheLanguage` (ext) |
| An export is visible, a private name is not, and the exported procedure still sees its private helpers | `TestAnExportedProcedureKeepsItsPrivateNames` (ext) |
| An import is a copy of the value | `TestAnImportIsACopy` (ext) |
| `require` options, collisions, reserved names, a missing module | `TestRequireOptionsAndCollisions` (ext) |
| The header matches the file; a cycle names the chain; a dangling export; size and a non-file | `TestModuleHeadersAndCycles` (ext) |
| At most 64 modules | `TestAtMostSixtyFourModules` (ext) |
| Commands are qualified, ambiguous, or taken by `init.scm`; a clashing key is unbound; only `init.scm` overrides a key or calls `set-theme`; a module theme is `module/name` | `TestCommandsAndKeysResolveAcrossModules` (ext); `TestAClashingKeyIsUnboundAtStartupAndBlocksReload`, `TestAModuleThemeIsQualifiedAndChosenFromInit` (editor) |
| Start-up procedures run in load order, `init.scm` last, each its own invocation | `TestStartProceduresRunInLoadOrder` (ext); `TestStartProceduresFromModulesRunSeparately` (editor) |
| A module error names the file, and commands defined before it stay | `TestAModuleErrorNamesTheFileAndEarlierCommandsStay` (ext) |
| The deadline and Ctrl-C cover a module required by `init.scm` | `TestTheDeadlineAndInterruptCoverARequiredModule` (ext) |
| The example modules load; `:module/name`, the ambiguous short name, `init.scm` winning a key, Tab and the guide | `TestExampleModules` (editor) |
| `:reload` re-reads modules, and a broken module changes nothing | `TestModuleReloadReplacesTheModulesOrChangesNothing` (editor) |
| Everything above in a real terminal, with a temporary `HOME` and `XDG_CONFIG_HOME` | `tests/pty_extension.py` |
| `docs/examples/sample.scm` loads and does what its comments say | `TestSampleInit` (editor) |
