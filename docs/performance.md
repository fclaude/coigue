# Performance: editing long files and pasting long text

Status: built. This is the design record for the work that made typing and pasting cost what the
edit touches rather than the size of the file, with the benchmarks that measure it. It is not user
documentation.

## What was slow

Every key press did work proportional to the whole file, and a paste was delivered as one key
press per character:

- **The terminal sent a paste as keys.** The editor never enabled bracketed paste, so pasting
  10,000 characters was 10,000 key presses, each followed by a redraw.
- **Every frame re-highlighted the whole file** (`syntax.ForPath` over every line) and **built the
  whole text** to decide whether the status line shows `[+]`.
- **Every key built the whole text again for the language server.**
- **Pastes, `p`, cuts and word motions flattened the buffer** into one array and split it back into
  lines.
- **Every undo step copied the whole file** when it started and compared the whole file when it
  ended.

## What changed, in five stages

1. **Bracketed paste.** The editor enables it on start (`term.EnterSequence`) and disables it on
   every exit path. A paste arrives as one `PasteEvent`:
   - in Insert mode it is inserted as typed;
   - in Normal mode with nothing pending it goes in like `P`, as one undo step, without touching
     the register;
   - anywhere else (prompts, lists, Visual mode, a half-typed command) it is replayed as the keys
     it is made of, which is what happened before.

   The input parser used to re-parse its whole pending buffer after every byte, which made a
   bracketed paste quadratic in its length. Inside a paste it now looks only at the last six bytes.
   A property test checks the result against the old re-parsing loop.
2. **Work proportional to the change.**
   - Each buffer edit records the first row it can alter (`Buffer.FirstChangedRow`).
   - The Go, Rust and Brote lexers became per-line functions with a small carried state, so the
     renderer relexes from the first changed row, and only as far as the screen shows
     (`syntax.Incremental`). Markdown, whose fences look ahead, is still relexed whole.
   - The modified flag compares the buffer's size with the file's first, and compares bytes only
     when the sizes match (`Buffer.EqualBytes`), without building the text.
   - The language server is sent the text once typing pauses for 50 ms (at most 300 ms late).
     Saves, file switches and navigation still sync at once. Until it is sent, the screen shows no
     diagnostics or semantic colours, as it did right after an edit before.
3. **One frame per burst.** While 16 or more events are already waiting, the loop handles them
   before drawing, and still draws at least every 33 ms. This matters for terminals without
   bracketed paste.
   - A few keys arriving in one read still get a frame each, so a prompt such as `:w` is still
     shown before it runs. The acceptance tests rely on seeing those frames.
   - Ordinary typing is unchanged: every key gets its own frame.
4. **Splice instead of flatten.** `InsertText`, `PasteChars`, `ReplaceSpan`, cutting a selection
   or a word range, and `w`, `b` and `e` work on the lines they touch.
5. **A rope.** The buffer keeps its lines in `internal/rope`, a persistent balanced tree of line
   chunks.
   - An undo snapshot is the old tree, not a copy of the file.
   - An edit rebuilds only the path to the lines it changes.
   - Comparing two versions skips every subtree they share.
   - `Clone` (used to run extensions on a private copy) costs nothing.
   - Lines are never modified in place, because snapshots share them.
   - `Buffer.Lines()` now builds a new slice, so code that runs per key or per frame uses `Line(i)`
     and `LineCount()`.

## How it is measured

`scripts/bench.sh LABEL` builds the benchmark binaries, then runs them with a timeout per package,
and saves the output to `bench-results/LABEL.txt`. `scripts/benchtable.py` turns several saved
runs into one Markdown table with speed-ups against the first.

```sh
scripts/bench.sh mine
python3 scripts/benchtable.py bench-results/before.txt bench-results/mine.txt
python3 scripts/benchtable.py bench-results/*.txt --filter Keystroke --metric ms/key
```

Every benchmark runs on Go-like files of 2,000, 20,000 and 100,000 lines (about 80 bytes a line),
built by `internal/benchdata`, with the cursor on the middle line.

| Benchmark | What it measures |
|---|---|
| `app.Keystroke` | Typing as a person does: one key, the background poll, and the frame that shows it, with nothing else waiting. `ms/key` is keystroke latency. |
| `app.Session/Type` | 50 keys queued at once, then Esc: a burst, so stage 3 draws once for all of them. |
| `app.Session/NormalX` | 20 presses of `x`, each its own undo step. |
| `app.Session/PasteKeys` | 1 KB pasted into a terminal without bracketed paste: 1,024 key presses. |
| `app.Session/Paste` | 1 KB and 64 KB pasted with bracketed paste (skipped before stage 1). |
| `app.Session/Open` | Opening the file and drawing the first frame. |
| `term.ParsePaste` | Decoding a paste from 1,024-byte reads, as keys and bracketed. |
| `buffer.*` | One typed character, one undo step, `p`, undo and redo, offsets, `Text`, building a buffer. |
| `syntax.*` | Relexing a whole file, and relexing after one edit down to the bottom of a 40-row screen. |
| `rope.*` | Comparing a version with the next, and replacing a line. |

The `Session` benchmarks drive the real event loop (`app.loop`) with a fixed 120×40 screen. They
send the bytes a terminal would send through the real input parser, and discard the frames.

## Results

Measured on an Apple M4 Max with Go 1.27. Each end-to-end benchmark ran three times; each
micro-benchmark ran for 300 ms. Differences under about 10% are noise between runs. Every stage was measured with the same benchmark code from a
snapshot of the tree at the end of that stage. Stage 1 was not kept as a snapshot. Its column
comes from its own run at the time, and `Keystroke` did not exist yet. Stage 1 does not touch the
typing path, so it equals the baseline there. Speed-ups are against the baseline.

### Typing, one key at a time (`app.Keystroke`, ms per key)

| File | Before | 2+3 incremental | 4 splice | 5 rope |
|---|---|---|---|---|
| 2,000 lines | 1.75 | 0.078 (22×) | 0.080 (22×) | 0.077 (23×) |
| 20,000 lines | 16.8 | 0.089 (190×) | 0.085 (199×) | 0.077 (218×) |
| 100,000 lines | 84.1 | 0.132 (639×) | 0.124 (677×) | 0.086 (978×) |

A keystroke now costs about the same however long the file is. Stage 2 is where this comes from;
the rope removes what was left at 100,000 lines.

### Pasting

| Paste | Before | 1 bracketed | 2+3 | 4 splice | 5 rope |
|---|---|---|---|---|---|
| 1 KB, terminal without bracketed paste, 20,000 lines | 16.5 s | 16.8 s | 14.8 ms | 13.7 ms | 15.0 ms |
| 1 KB bracketed, 20,000 lines | (arrives as keys: 16.5 s) | 36.8 ms | 11.6 ms | 7.9 ms | 7.2 ms |
| 64 KB bracketed, 100,000 lines | (minutes) | 186 ms | 61.2 ms | 41.7 ms | 37.4 ms |
| `p` of 64 KB, 100,000 lines (`buffer.PasteChars`) | 19 ms | 19 ms | 22.1 ms | 4.05 ms | 0.093 ms |

The paste sessions include the first frame after opening the file. The cursor starts on the middle
line, so that frame lexes from the top of the file down to it: about 35 ms of the 37.4 ms at
100,000 lines (compare `Open`), whatever the size of the paste. The paste itself is the last row.

### Undo steps (`x` in Normal mode, ms per key)

| File | Before | 2+3 | 4 splice | 5 rope |
|---|---|---|---|---|
| 20,000 lines | 18.4 | 1.40 | 1.40 | 0.42 (44×) |
| 100,000 lines | 96.8 | 7.13 | 6.78 | 1.85 (52×) |

| Buffer operation, 100,000 lines | Before | 5 rope |
|---|---|---|
| One undo step (`buffer.EditStep`) | 5.41 ms | 3.4 µs |
| Undo and redo (`buffer.UndoRedo`) | 7.34 ms | 0.48 µs |
| Cursor offset (`buffer.Offset`) | 11.7 µs | 9 ns |

### What got slower

- **One typed character inside an Insert session** (`buffer.InsertChar`): 6 ns before, 0.4–3.6 µs
  with the rope. Every edit now builds a new path through the tree instead of shifting runes in a
  slice. It is a small part of the 0.08 ms a keystroke costs.
- **Bursts of keys on small files**: a 1 KB unbracketed paste into 2,000 lines takes 5.0 ms at
  the end, against 1.9 ms at stage 4. About half of that is the rope's cost per edit, and the
  rest is the last 16 keys of a burst each getting a frame. The baseline was 1.66 s.
- **Building a buffer from a file** (`buffer.New`) is about 25% slower (9.7 ms to 12.7 ms at
  100,000 lines). Since stage 2 it also counts bytes, and since stage 5 it builds the tree.
  Opening is still twice as fast overall, because the first frame lexes only to the screen.

## What is still proportional to the file

- **Opening a file, or jumping far into it**, lexes from the top down to the screen once, because
  a line's colours depend on the comment and string state above it. Keeping lexer states at
  regular checkpoints would make this cost the distance from the nearest checkpoint.
- **Markdown** is relexed whole after every edit, because fences look ahead for their end.
- **Saving, and sending the text to the language server**, build the whole text. The server is
  sent it once per pause in typing, not once per key.
- **`Buffer.Lines()`** builds a slice of every line. It is kept for whole-text consumers (marks,
  navigation requests, Markdown), and the language server and marks call it only when they
  need it.
- **Search** (`/`, `n`) scans from the top of the file, as before.
