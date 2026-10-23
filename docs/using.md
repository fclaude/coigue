# Using it

This is a walk through the editor with a practice file, one key at a time. Everything here was typed into a real Coigüe in an 80 by 24 terminal, and the messages quoted are what the status line really said. If you'd rather look something up than read it through, [Keys and commands](keys.md) has the same keys in tables.

## A file to practice on

Make a small file and open it:

```sh
cat > notes.txt <<'EOF'
The quick brown fox
jumps over the lazy dog.

Second paragraph here, with a few words
to move around in.
EOF
coigue notes.txt
```

You should see the five lines with their numbers on the left, a `~` for every row below the end of the file, and a bar at the bottom that shows the mode and the path:

```text
1 The quick brown fox
2 jumps over the lazy dog.
3
4 Second paragraph here, with a few words
5 to move around in.
6
~
```

You start in Normal mode, where keys are commands and nothing you type goes into the file. `Esc` always brings you back to it.

## Moving around

`h`, `j`, `k` and `l` move left, down, up and right, and the arrow keys do the same. Press `j` once and the cursor is on the second line. `w` jumps to the start of the next word, `b` to the start of the previous one, and `e` to the end of the current one. `0` goes to the start of the line and `$` to its end. `gg` goes to the first line and `G` to the last, and a number in front of `G` picks a line, so `3G` lands on line 3.

You can also type `:` followed by a number and Enter. `:4` goes to line 4, and a number past the end of the file goes to the last line.

## Typing

`i` starts typing before the cursor and `a` after it. `A` starts typing at the end of the line and `o` opens a new line below and starts typing there. The bar at the bottom changes from Normal to Insert while you type, and `Esc` takes you back. Let's put a word at the end of the first line: press `gg`, then `A`, type ` END`, and press `Esc`. The first line now reads `The quick brown fox END`.

Everything you type between starting and pressing `Esc` is one undo step.

## Fixing mistakes

`x` deletes the character under the cursor and `dd` deletes the whole line. `u` undoes the last change and says `Undone`. `Ctrl-R` puts it back and says `Redone`. Try `x` on the first line, so that `The` becomes `he`, and then `u`.

Counts work in front of most commands. `3x` deletes three characters, `2dd` deletes two lines, and `5j` moves down five. A count before and after multiply, so `2d3w` deletes six words. `r` followed by a character replaces the character under the cursor: `rX` turns the `T` of `The` into an `X`.

## Cut, copy and paste

`dd`, `dw` and `x` cut into the editor's own register, and `yy` and `yw` copy a line or a word into it. `p` pastes after the cursor, or below the current line when you copied a whole line, and `P` pastes before or above. On the first line, `yy` says `1 line(s) copied`, and `jp` puts a copy of the first line below the second. `cw` changes a word: put the cursor on `over`, type `cw` and then `slow`, press `Esc`, and the line reads `jumps slow the lazy dog.`. `>>` indents a line by four spaces and `<<` takes four away.

These commands use the register inside the editor and never touch your system clipboard. That's what `Y` in Visual mode and the `:clipboard-copy` and `:clipboard-paste` commands are for.

## Searching

`/` followed by some text and Enter jumps to the next match in the file. The search is literal and it cares about capitals. Type `/lazy` and Enter and the cursor lands on the `l` of `lazy`. `n` goes to the next match and `N` to the previous one. This file has only one `lazy`, so `n` says `/lazy (wrapped)` and stays where it is, which tells you the search went all the way round.

## Saving and leaving

`:w` saves and says `Written`. `:q` quits, but if the file has unsaved changes it refuses with `Unsaved changes; use :w or :q!`. `:wq` saves and quits, and `:q!` quits and throws the changes away. A file opened by name that doesn't exist yet starts empty, and `:w` creates it.

## Selecting text

`v` starts a selection by character and `V` a selection by whole lines. Move with the keys from before and the selection follows. The bar says Visual, and the message row reminds you of the keys: `y` yanks, `d` deletes, and `Esc` cancels. `o` swaps which end of the selection moves.

Try it on the first two lines: press `gg`, then `Vj`, then `y`, and the status line says `Selection yanked`. `Vjd` instead deletes both lines (`Selection deleted`), and `u` brings them back.

## Other files

`Ctrl-O` opens a browser for the current folder, and from the welcome screen it starts in the folder you launched from. Move with `j` and `k`, press Enter or `l` to go into a folder or open a file, and `h` to go up. `/` filters the names as you type, `a` creates a file and `A` a folder (type the name and press Enter), and `r` renames or moves the one under the cursor. `d` asks `Trash? [y] yes [Esc/n] cancel` and, once you press `y`, moves the file into a `.coigue-trash` folder next to where it was. `u` in the same folder brings the latest one back. `Esc` returns to the file you were editing.

When you know part of the path, `Ctrl-P` is faster. Type a few letters in order, say `tod`, and the list narrows to the files that contain them, such as `sub/todo.txt`:

```text
Files 1/5 | Enter open, Esc cancel
> sub/todo.txt
Open: tod
```

Enter opens it. If you have unsaved changes in the file you're leaving, Coigüe asks first: `s` saves and opens, `d` discards and opens, and `c` or `Esc` cancels. `Alt-Left` goes back to the file you came from and `Alt-Right` forward again, and `:recent` lists the last files you visited.

## Searching a whole project

`F5` searches the text of every file under the folder you launched from. Type `lazy` and the list fills as you type:

```text
Project search 2/2 | 2 matching lines
> notes.txt:2:16  jumps over the lazy dog.
  notes.txt.orig:2:16  jumps over the lazy dog.
```

The numbers after each file name are the line and the column. Enter opens the match and `Esc` closes the list. The search is literal like `/`, and it skips files that aren't text or are bigger than 2 MiB.

`F6` lists the symbols of the file you're in, `F7` jumps to the definition of whatever is at the cursor, `F8` lists its references, and `F9` searches symbols across the project. `F7`, `F8` and `F9` ask a language server, which you have to install yourself (see [Setting it up](setup.md)). Without one you get an empty list, such as `Definitions 0/0 | 0 results`, and `Esc` takes you back to the file.

## When you're stuck

`F1` opens the help guide wherever you are, even in the middle of typing, and `Esc` or `F1` returns you to exactly where you were. `Tab` switches between the topics. `F2` opens a menu of file actions and `F3` one of code actions, and `F4` shows all of them. Type a few letters to filter the list, use the arrows to choose, and press Enter. The menu shows the key for each action, so it's also a way to learn them.

## Colours

`:theme` on its own tells you the theme in use and lists the choices: `mocha` (the default), `gruvbox`, `latte`, `monochrome` and `terminal`. `:theme gruvbox` switches at once, and pressing `Tab` after `:theme ` cycles through the names. Nothing is saved, so the next start is back to `mocha` unless your `init.scm` says otherwise.

## Your own commands

A file called `init.scm` in `~/.config/coigue/` (or in `$XDG_CONFIG_HOME/coigue/`) can add commands, written in Scheme. Here's one that wraps the selected text in whatever you give it:

<!-- include: docs/examples/init.scm scheme -->

Save that, start Coigüe again, and select a word. Press `v` and then `e` on the `The` of the practice file, then press `!`. The command line opens with the selection still in place, and typing `wrap ( )` and Enter turns it into `(The)`. The status line says `Ran :wrap`, and `u` undoes it in one step. Typing `:` in Visual mode does nothing, which is why `!` exists.

The same file can define a theme and make the editor start in it:

<!-- include: docs/examples/theme.scm scheme -->

After that, `:theme dusk` switches to it from any theme.

A key can run a script too. This file binds Ctrl-D to copying the current line below itself, Alt-J and Alt-K to moving a line down or up, and `U` in Visual mode to making the selection upper case:

<!-- include: docs/examples/keys.scm scheme -->

A key is one character, Ctrl and a lowercase letter such as `<C-d>`, or Alt and a character such as `<A-j>`. A key that already does something, like `x` or `w`, needs `#t` after the description, so you don't shadow it by accident. `F1` lists the keys you bound, in the topic after the others.

What else you can do with Scheme, and the exact rules, are in [Scheme and themes](extending.md).
