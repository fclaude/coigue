# Extending Coigüe with Scheme

Coigüe embeds [Brote](https://github.com/fclaude/brote), a small Scheme for Go programs. A
Scheme program in your own configuration file can define editor commands: `:name` runs a Scheme
procedure that reads the text you selected and asks the editor to change it. The same file can
define themes and pick the one the editor starts in (see *Themes* below). This page is the
contract for that capability. Each statement is tested; the last section says where.

This is the first slice. It has one way to run Scheme (a command), one place Scheme is loaded from
(your `init.scm`), and a handful of host operations. It is not a plugin system: there is no package
manager, installer, debugger, key-binding language, or way to run a command on its own.

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
one more guide topic, "Your commands", listing each with its description. Without `init.scm` the
guide is exactly what it was.

Select text with `v` or `V`, press `!`, type `wrap ( )` and Enter. The selection is now surrounded by
parentheses. (`!` opens the usual command line, shown as `:`, and keeps the selection for the one
command you type. `:` itself is still ignored in Visual mode, as it always was; see *Differences
from the Rust editor*.)

### Exactly what `:wrap OPEN CLOSE` does

* **Arguments.** The words after the command name, split on whitespace, each passed to the procedure
  as a Scheme string. There is no quoting: `:wrap " "` passes the one-character string `"` twice,
  and an argument cannot contain a space. The procedure here takes exactly two; with another number
  Scheme reports `wrap: expected 2 argument(s), got 1` and nothing changes.
* **What is read.** The selection, through `selection-text`: the characters from one end of the
  selection to the other, inclusive of both ends, exactly as `d` would cut them in Visual mode.
  A `v` selection can span lines and includes each newline it crosses (and the newline of an
  empty line it ends on); a `V` selection is whole lines joined with `\n`, without a trailing
  newline. Text is read in characters (Unicode scalar values), so multibyte text is never split.
* **What is changed.** `replace-selection` replaces the selected characters (for `V`: the text of
  the selected lines, not the newline after the last one) with the given string. Nothing else in
  the buffer changes.
* **After the command.** The editor is in Normal mode and the selection is gone. The cursor is on
  the first character of the inserted text (like every Normal-mode cursor it stays on a character
  of its line, so for text that starts with a newline it is the last character before it). A
  command that makes no edit leaves the cursor where it was. The status line says `Ran :wrap`, or the last text the
  procedure passed to `message`. The screen is not redrawn while the procedure runs (see
  *Cancellation*).
* **Undo.** The whole command is one undo step. `u` restores the text exactly as it was before the
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
| `(define-command name proc [doc])` | Defines `:name` as a command that applies `proc` to its arguments. `name` is 1 to 32 characters: a lowercase letter, then lowercase letters, digits and `-`. It must not be a built-in command or already defined, and at most 100 commands can be defined. `doc` is a one-line description. | only while `init.scm` loads |
| `(define-theme name spec)` | Defines the theme `name`, which `:theme` and `set-theme` can then use. `name` follows the rule for command names, cannot be the name or alias of a built-in theme or of a theme already defined, and at most 16 themes can be defined. `spec` is a list of `("key" . value)` pairs (see *Themes*). | only while `init.scm` loads |
| `(set-theme name)` | Starts the editor in the theme `name`, a built-in one or one defined earlier. An unknown name is an error that lists the choices. | only while `init.scm` loads |
| `(selection?)` | `#t` if the command was started from a Visual selection and no edit has happened yet. | in a command |
| `(selection-text)` | The selected text (see above). Error if there is no selection, it is longer than 65,536 characters, or an edit already happened in this command. | in a command |
| `(buffer-text start [end])` | The text of lines `start` to `end` (1-based, inclusive; `end` defaults to `start`) of the current buffer as it is at this point of the command (edits already made are visible), joined with `\n` and without a trailing newline. Error if a line is outside the buffer or the text is longer than 65,536 characters. | in a command |
| `(line-count)` | The number of lines in the buffer. | in a command |
| `(cursor-line)` | The 1-based line of the cursor (the end of the selection when there is one). | in a command |
| `(replace-selection text)` | Replaces the selection with `text` (see above). Error if there is no selection or an edit already happened. Cursor on the first character of `text`. | in a command |
| `(insert-text text)` | Inserts `text` before the cursor character, whether or not there is a selection. The cursor ends on the first character of `text`. A later `selection-text` or `replace-selection` fails. | in a command |
| `(message text)` | Sets the status line shown when the command finishes successfully; the last call wins. Control characters become spaces and the text is cut at 500 characters. | in a command |

"In a command" means: while the editor is running a `:name` command. Called any other way (while
`init.scm` loads, or by anything that is not an invocation the editor started) these operations raise
`name: not inside an extension command`; `define-command`, `define-theme` and `set-theme` outside
loading raise `define-command: only allowed while init.scm loads` (with their own name).

Line numbers in the interface are 1-based, columns do not appear (the interface works on text, not
screen positions), and every size is in characters.

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
text). The selection does not survive an edit: the first `insert-text` or `replace-selection` ends
it, because the positions it named no longer mean the same text.

## Threading and reentrancy

* **One interpreter per editor**, created the first time `init.scm` is loaded and used by one
  goroutine at a time. Brote's `*Interp` has no locking.
* **Scheme is evaluated on the editor goroutine**: the goroutine that calls `Editor.Key`, which in
  the `coigue` command is the main goroutine running the event loop. Only `Editor.Key` (running a
  command) and `Editor.LoadInit` evaluate Scheme. The call is synchronous: the editor does nothing
  else until it returns.
* **Host operations run on that same goroutine**, inside Brote's call, and they touch only the
  invocation's private copy (above), never the live buffer, registers, messages or command table.
  The live state is read before the call starts and written after it returns, both on the editor
  goroutine. An extension therefore cannot change editor state concurrently, because it holds no
  reference to it.
* **Other goroutines.** While Scheme runs, two goroutines exist, both started through `guard.Go`:
  the input watcher the `coigue` command supplies (it reads the keyboard and calls a `cancel`
  function when it sees Ctrl-C) and a memory watchdog. They call `cancel` and nothing else.
* **Accidental callbacks are refused.** Every operation first checks three things: the
  evaluation's context carries an invocation the editor started, that invocation is still live
  (it is only between the editor starting the command and the command returning), and the
  operation is running on the goroutine that started it. Go has no portable goroutine identity, so
  the last check reads the goroutine number from the first line of `runtime.Stack`. A procedure
  retained from `init.scm` and applied by anything else, an operation invoked after its command
  ended, one with no context, and one called from another goroutine in the middle of a command
  all fail with `not inside an extension command` and change nothing. (No primitive starts a
  goroutine and Brote has no concurrency of its own, so this is a backstop, tested by calling an
  operation from a second goroutine while the command runs.)
* **No reentrancy.** No operation starts a command or loads a file. As a backstop, starting an
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
  a runaway string unlikely, not impossible.
* The screen is not redrawn while a command runs. A command that takes visible time shows a frozen
  screen until it finishes, is interrupted, or reaches the deadline.

## Loading and trust

* Scheme is loaded from **one place**: `$XDG_CONFIG_HOME/coigue/init.scm`, or `~/.config/coigue/init.scm`
  when `XDG_CONFIG_HOME` is unset, empty or not an absolute path. It is read once, when the editor
  starts. Changes need a restart.
* It is **never** loaded from the directory of the file you open, the working directory, or any
  project directory, and nothing is run because a repository contains a file. A `init.scm` next to
  the file you edit is just a text file.
* A **missing file is normal** and says nothing. A file that cannot be read, or an error while it
  loads (a syntax error, a Scheme error, the deadline, Ctrl-C), is reported in the status line as
  `init.scm: ...` and **does not stop start-up**. Commands defined before the error stay defined.
  The file must be a regular file of at most 1 MiB.
* The program you write is as trusted as your shell's start-up files, but it is limited to the
  host interface above. In particular, Brote's `load` builtin, which reads any file the process can
  read, is declared a host operation and **a gate refuses every primitive declared `Host`** (and
  every `Egress` or `Irreversible` one). `(load "x.scm")` fails with `load: denied: ...` and the
  file is not read. The set of primitives is frozen after registration, so `register-tool` cannot
  add one. No operation gives access to processes, the network, the file system, environment
  variables, credentials or the clipboard.
* The command line is unchanged: `coigue [FILE]`, `--help`, `--version`. No flag was added, so
  there is nothing that would let a command line (and so a project script) pick a different file.

## Not in this slice

Running Scheme from a key or an event, reading or writing other files, LSP data, running a command
from Scheme, reloading `init.scm`, a `:eval` command, defining modes or key bindings, scripts
distributed as packages. Each needs its own decision about trust and threading first.

## Differences from the Rust editor

Visual mode has one new key. `!` opens the command line (it looks like `:`) and keeps the selection,
still highlighted, for the one command typed. Enter runs the command; Esc cancels both the command
and the selection. A built-in command run this way ignores the selection and ends it, and so does
a menu action. The Rust editor ignored `!` in Visual mode.

`:` is deliberately left as it was. In Visual mode the Rust editor ignores it, the recorded
sessions in `tests/pty_golden.py` type `:theme terminal` in Visual-line mode and pin that, and so
does the ported editor test for Visual mode. Making `:` do what `!` does would be the natural
change and a one-line one (`visualKey` in `internal/editor/input.go`) if those expectations are
updated. Nothing else in the editor changed.

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
| Everything above in a real terminal, with a temporary `HOME` and `XDG_CONFIG_HOME` | `tests/pty_extension.py` |
