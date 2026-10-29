# Design: background work, grants and windows for extensions

Status: proposed. Nothing here is built except where it says so. This is the design record, not user
documentation: what exists today is in [Scheme and themes](extending.md). The Scheme on this page is
proposed syntax and is not run by the tests.

Extensions today are short, synchronous and buffer-only. This design lets them do work that takes
time, reach the network when the person allows it, and show their own windows, without giving up
what makes them safe to install. It is written against Brote v0.5.0, whose tasks and channels are
the foundation: Coigüe adds as little vocabulary of its own as it can.

## Rules that stay true

A change that breaks one of these needs its own decision.

1. **One writer.** The buffer is changed only by a command on the editor's goroutine, working on a
   private copy that is applied in one edit if the command succeeds. That makes a command one undo
   step and a failed command a no-op.
2. **Nothing runs unless the person asked.** A typed command, a key they bound, or the start of the
   editor. Later work runs because a command of theirs started it.
3. **Loading never reaches the network.** `require` reads files; fetching code is `:module-add`.
4. **What a module may reach is shown before it is installed** and is recorded with its hash.
5. **A function never vanishes.** An operation that cannot work where it is called raises an error
   that says why and what to do instead.

## What Brote v0.5.0 gives us

The Brote design record is `docs/design/tasks.md` in its repository. What matters here:

* **Tasks share memory, safely.** `(spawn thunk)` runs in parallel and sees the program's data. Each
  operation on a vector, hash table, variable, atom or channel is atomic. A sequence of operations
  is not; `swap!`, `hash-update!` and `with-lock` are the tools, and `brote-lsp` already warns about
  the mistakes: `set!` of a variable a task shares, a read followed by `set!`, waiting for another
  task while holding a lock, and side effects inside the function given to `swap!`.
* **Channels, `select`, `after`, `with-timeout`, `race`, `pmap-limit`, `task-group`** are in the
  language. `(after secs)` is a channel that receives once, so timers need no new feature.
* **Tasks end with the evaluation that started them.** When the outermost `Apply` returns, running
  tasks are cancelled and waited for. A task cannot outlive its command.
* **A grant.** `WithTasks(TaskLimits{MaxTasks, MaxDepth})` turns them on, as the host's statement
  that its handlers and gate are safe to call concurrently.
* **Task contexts descend from their spawner's.** Anything Coigüe puts in the context of the
  evaluation it starts is visible in every task and every `pmap` worker beneath it, and
  `Call.Ctx` is that context. This is how a gate can know whose code is running.
* **Several evaluations may run at once** on one interpreter. Each call from outside is its own.
* **Deadlock is detected**: all tasks blocked, no timer pending, is an error and not a hang. It
  assumes the interpreter's own tasks are the only things that wake each other (see *Hazards*).
* **Not there yet:** the design notes a `TaskLimits.Background` grant with `(spawn-detached thunk)`,
  owned by the interpreter and ended by `Shutdown`, for a host such as an editor. v0.5.0 does not
  have it.
* **One `std` policy per interpreter.** `std.Install` registers `http-request` once and enforces
  the policy inside it. Two callers cannot have different network scopes.

## The model

Three kinds of code, and Brote's own objects connecting them.

| | Runs | Can read or change the buffer | Can use granted capabilities |
| --- | --- | --- | --- |
| **Command** (typed, bound, or a handler below) | on the editor's goroutine, as a short transaction | yes | no |
| **Background task** | on its own goroutine, for as long as it needs | no | yes, if the module was granted |
| **Handler** | a command that the editor starts when a Brote object fires | yes | no |

Two host operations turn Brote objects into handler calls. They are the whole bridge:

| Operation | Does |
| --- | --- |
| `(on-done task handler)` | When `task` finishes, run `(handler task)` as a command. Inside it `(task-wait task)` gives the value or raises the task's error, so `guard` works as everywhere else. |
| `(on-channel channel handler)` | For every value received on `channel`, run `(handler value)` as a command, until the channel is closed. |

Both are used from a command and return at once. There is no result type, no callback vocabulary
and no copy rule: the objects are Brote's, and what a handler receives is what Brote would give it.

### Example: a rhyme finder

```scheme
(define-module rhymes
  (doc "Rhymes for the word under the cursor")
  (requires (net (allow "api.datamuse.com") (methods get))))

;; Runs in the background, where the network is available.
(define (lookup word)
  (map (lambda (e) (cdr (assoc "word" e)))
       (http-get-json (string-append "https://api.datamuse.com/words?rel_rhy=" word))))

(define-command "rhyme"
  (lambda ()
    (let ((word (word-at-cursor)))
      (message "Looking up rhymes...")
      (on-done (spawn-detached (lambda () (with-timeout 10 (lambda () (lookup word)))))
               show-rhymes))))

;; A command again: the buffer is available.
(define (show-rhymes task)
  (guard (e (#t (message "Rhyme lookup failed")))
    (on-channel (show-list "Rhymes" (task-wait task)) insert-text)))
```

`spawn-detached` is Brote's task that outlives the command (see *Requests for Brote*). The command
ends at once and the editor stays responsive. `with-timeout`, `guard` and `task-wait` are the
language's own. `show-list` returns a channel for the person's answer, as every window does.

### Example: a link checker that reports as it goes

```scheme
(define (reachable? url)
  (guard (e (#t #f))
    (< (cdr (assoc "status" (http-request "HEAD" url))) 400)))

(define (check-all urls progress)
  (pmap-limit 8
    (lambda (u)
      (let ((ok (reachable? u)))
        (channel-send! progress (cons u ok))
        ok))
    urls))

(define-command "check-links"
  (lambda ()
    (let ((urls (links-in-buffer))                 ; the module's own helper, read now, in the command
          (progress (make-channel 64)))
      (on-channel progress note-progress)
      (on-done (spawn-detached (lambda ()
                                 (check-all urls progress)
                                 (channel-close! progress)))
               (lambda (t) (message "Link check finished"))))))
```

`pmap-limit` does eight at a time in parallel. Progress arrives as handler commands, one per
value, each its own undo step and each with a fresh view of the buffer. A mark (below) lets a
handler point at the line a URL came from even after the text has moved.

### Example: a timer, with no new feature

```scheme
(define-command "sprint"
  (lambda ()
    (let ((start (words-in-buffer)))
      (on-channel (after 600)
                  (lambda (_) (message (string-append "10 minutes: +" (number->string (- (words-in-buffer) start)) " words")))))))
```

## Handlers

* A handler is a command: a fresh snapshot of the buffer, its own deadline, one undo step, the usual
  status-line message on success and on failure. It is attributed to the module that registered it.
* Handlers run one at a time on the editor's goroutine, queued and run between keys. They never run
  inside another command.
* Registrations belong to the module. `:reload`, removing the module and quitting cancel its tasks
  and drop its registrations; nothing from old code reaches new code.
* A handler registered on a channel nobody can send to stays registered until the channel is
  closed or the module goes. There is a cap on registrations and on queued values; past it, the
  registering call is an error and a full queue drops the oldest and says so in the status line.
* A background task that fails and that nothing waited for is reported in the status line as
  `module: background task failed: message`. This is the "unobserved error" Brote's design asks the
  host to decide on; loud is the default.
* The editor never blocks on Scheme waiting for a task. A command that calls `task-wait` on a
  running task does block, up to its own deadline, as any long command does today. That is the
  author's choice, and the documentation says to use `on-done` instead.

## Marks

A handler may run seconds after the command that started the work, and the person may have kept
typing. The editor does not refuse an edit for that. A command takes a mark, a position the buffer
keeps current as text around it changes, and a handler uses it:

```scheme
(let ((here (mark-at (cursor-line) (cursor-column))))
  (on-done task (lambda (t) (insert-at-mark here (task-wait t)))))
```

A mark whose surrounding text is deleted collapses to the nearest surviving position.
`(buffer-changed-since? mark)` is there for a module that would rather give up. Marks need the
buffer to adjust live marks on every edit, a contained change in `internal/buffer`.

## Windows

The editor has six full-screen modal views, each its own code: the guide, the action menu, the file
picker, the explorer, location lists and the module review page. They become one abstraction,
built so that a window's answer is a Brote channel and everything above applies unchanged:

| Operation | Returns |
| --- | --- |
| `(show-list title items)` | a channel that receives the chosen item; closed if cancelled |
| `(show-text title lines)` | a channel closed when dismissed |
| `(prompt title initial)` | a channel that receives the typed text; closed if cancelled |

```scheme
(on-channel (prompt "Rename to" (word-at-cursor)) rename-everywhere)
```

Stages:

1. **Modal dialogs, lists and prompts.** No threads needed. The module review and the guide are
   rebuilt on it, which also tests it.
2. **Docked panes.** A read-only pane at the bottom or side that a task can update through a
   channel. Needs a layout step in the renderer, which draws one buffer and a status line today.
3. **Editable sub-buffers.** A scratch buffer with its own cursor, possibly hosting a nested editor
   so the vi keys come for free. Open; decide when stage 2 is in use.

For every stage: the editor sanitises window text (control and bidirectional characters) as the
review page already does, caps windows and their size, and closes a module's windows on `:reload` or
removal. A modal window gets every key; nothing reaches the editor behind it.

## Capabilities

A module says what it needs in its header, in Brote's policy vocabulary so there is one syntax:

```scheme
(requires (net (allow "api.datamuse.com") (methods get)))
```

First grants: `net` (named hosts, no private addresses) and `time`. `fs` and `exec` are not offered.

**Approval is at install.** The review page for `:module-add` and `:module-update` shows the
request beside the code, in words: "can reach api.datamuse.com (GET)". The approved grant is written
to `modules.lock` with the commit and hash. An update that widens a request is reviewed again and
the page marks the change. A module you wrote states its grant in its own header, since you are the
author.

**Only background tasks use a grant.** The gate sees every primitive call with the context, and the
context carries the module that started the evaluation into every task beneath it. So the gate can
refuse with a message that names the fix, in a command:

```
http-get: a command cannot use the network; do it in a (spawn-detached ...) task and handle the result with on-done
```

and in a module with no grant:

```
http-get: this module has no network access; add (requires (net (allow "host"))) to its header
```

The same goes the other way: in a background task, `insert-text` fails with
`insert-text: a background task cannot read or edit the buffer; return what you found and apply it in a handler`.
The operations stay defined; the context decides what they do.

**Scope.** `std` takes one policy per interpreter. The plan is to install it with the union of the
grants the person has approved and have the gate narrow each call to its own module's grant, which
needs Brote to expose the check (see the requests).

**What this does not protect.** A module that can read the buffer and reach a host can send the
buffer there. Brote's documentation says the same of any allowed host. The defence is honesty and
narrowness: the review page states what a module can do with the pair, hosts are named, there are no
wildcards by default, and updates re-review.

## Hazards, and what already helps

* **Shared state.** A handler and a task share the module's variables safely at the level of single
  operations. For a counter or a flag use `make-atom` and `swap!`; the language server's
  concurrency diagnostics, already shown for `init.scm`, flag the common mistakes.
* **Deadlock detection and the editor.** Brote reports "all tasks blocked" when none are running and
  no timer is pending. A background task waiting on a channel that only the *editor* will send to,
  for example a handler passing it work, looks deadlocked to that rule when nothing else runs.
  Until Brote can mark a channel as fed from outside, use `select` with `(after secs)`, or have the
  task poll with `channel-try-recv`.
* **Not blocking the editor.** Commands, handlers and windows never wait for the network. A command
  that wants a quick answer can still use `with-timeout` around `task-wait`, and it freezes the
  screen for that long.
* **The cost of a task.** A task is a goroutine. Brote bounds them with `MaxTasks`; Coigüe sets the
  limit per runtime and counts tasks a handler starts.

## What Coigüe has to change

* `Runtime` refuses an evaluation while another is running (`ErrBusy`), because the interpreter used
  to have no locking. It becomes: one transaction at a time, any number of background evaluations.
* Turn `WithTasks` on and make the gate declare itself concurrent-safe (`scheme.ConcurrentGate`); it
  has no state.
* A context key that names the module on every evaluation, read by the gate.
* An event queue drained by `Tick` that runs handlers as commands, and the registrations that feed it.
* The explicit errors above, and a table in the extension documentation of where each operation works.

## Build order

Each step stands alone and ships.

1. **Handlers and the event queue**, with `on-channel` and Brote's own channels and `after`, which
   need no background tasks: a command can open a channel, a timer can fire it, and the
   handler runs as a command. A `(after secs)` timer already gives a useful extension.
2. **Modal windows** returning channels. Port the review page onto them.
3. **Marks.**
4. **Background tasks**: `spawn-detached` from Brote, `on-done`, the busy rule, the explicit errors,
   lifetime and limits, and the operation table.
5. **Grants**: `requires`, the review page, the lock, the gate.
6. **Docked panes**, then sub-buffers if wanted.

Steps 1 to 3 need nothing from Brote beyond v0.5.0. Step 4 needs `spawn-detached`.

## Requests for Brote

* **`TaskLimits.Background` and `(spawn-detached thunk)`**, as sketched in its own design: a task
  owned by the interpreter, ended by `Shutdown`, not by the end of an evaluation, and still
  counted by `MaxTasks`. This is what lets `on-done` take a real Brote task handle. Without it,
  Coigüe would have to start its own evaluations and invent its own handle type, which is the
  parallel vocabulary this design avoids.
* **A way to ask a `std` policy whether a call is allowed**, for example
  `(*Policy).CheckNet(method, url string) error`, so a host narrows by caller without a second
  interpreter. The `fs` and `exec` equivalents if those grants are offered.
* **Host-fed channels**: a way to mark a channel as one the host sends to, so a task waiting on it
  is not reported as deadlocked.
* Confirm that applying a closure from one goroutine while another evaluation runs on the same
  interpreter is intended to be safe with tasks on, as the embedding notes imply.

## Open questions

* How does a person stop background work without `:reload`? A `:tasks` list with a cancel key, or
  only `:reload`.
* May a handler start more background tasks? Probably yes, with `MaxTasks` counting them.
* Is `spawn-detached` the right name next to `spawn`, or is it better written as an option, for
  example `(spawn thunk 'detached)`? It is Brote's call.
* Should a command be allowed a bounded blocking network call, with a short fixed cap, to keep
  simple extensions simple? It would freeze the screen for up to the cap.
* Does `fs` ever become a grant, given the editor already has the person's files open?
