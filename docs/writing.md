# Writing an extension

This page writes a small extension and then splits it into files. The commands are the same ones as [Using it](using.md), and the messages quoted here are what the status line says. The exact limits, and every way a script can fail, are in [Scheme and themes](extending.md).

## The file

Coigüe reads `init.scm` from `~/.config/coigue/`, or from `$XDG_CONFIG_HOME/coigue/` when that variable is set to an absolute path. A missing file is normal and the editor says nothing about it. A file with a mistake is reported in the status line, and the editor still starts.

Nothing is read from the folder of the file you're editing. An `init.scm` that arrives with someone else's project is just a text file.

You don't have to restart to try a change. Edit the file, type `:reload`, and the new commands, keys and themes replace the old ones. If the new file fails, nothing changes and the status line says why, ending with `nothing changed`.

## A command

A command is a Scheme procedure with a name you can type after `:`. This one wraps the selection in the two words you give it:

<!-- include: docs/examples/init.scm scheme -->

Open a file, press `v` and then `e` on a word, and press `!`. The command line opens and the selection stays. Type `wrap ( )` and Enter. The word is now in parentheses, the status line says `Ran :wrap`, and `u` undoes the whole change in one step. `:` in Visual mode does nothing, which is why the key is `!`.

The procedure reads the selection with `selection-text` and replaces it with `replace-selection`. A command can also read other lines (`buffer-text`), the cursor, the path and the mode, and it can insert text, replace or delete lines, and move the cursor. Whatever it changes is one undo step. If it raises an error, or you stop it with `Ctrl-C`, or it runs for more than five seconds, the file is left as it was.

`F1`, then `Tab` until you reach "Your commands and keys", lists the command and the description you gave it.

## A key

A key can call a procedure the same way a command does, with no arguments. This file copies the current line with Ctrl-D, moves a line with Alt-J and Alt-K, and makes a Visual selection upper case with `U`:

<!-- include: docs/examples/keys.scm scheme -->

A key is one character (`"x"`), Ctrl and a lowercase letter (`"<C-d>"`), or Alt and a character (`"<A-j>"`). There are no sequences of keys. A key the editor already uses, such as `x` or `w`, needs `#t` after the description, so you don't take it by accident. `:`, Ctrl-O, Ctrl-P and, in Visual mode, `!` can't be bound at all. `F1` lists the keys next to the commands.

## A theme

The same file can define a theme and choose the one the editor starts in:

<!-- include: docs/examples/theme.scm scheme -->

`:theme dusk` switches to it later. Nothing is saved, so the next start goes back to what `init.scm` says. The colours a theme can change are listed in [Scheme and themes](extending.md#themes).

## One command, its own file

Once you have more than a command or two, put each group in a module. A module is a file in the `modules` directory next to `init.scm`, and it is read only because `init.scm` requires it:

```text
~/.config/coigue/init.scm
~/.config/coigue/modules/text-tools.scm
~/.config/coigue/modules/md-tools.scm
```

`modules/text-tools.scm` starts with a header. The name in the header is the file's name. `export` is the list of names other files may use, and everything else stays in this file. `surround` is private, and `wrap` can still call it, because a procedure keeps the names it was defined with:

<!-- include: docs/examples/modules/text-tools.scm scheme -->

`modules/md-tools.scm` is a second module. It also exports `wrap`, and that `wrap` bolds the selection. It binds the same Visual key, `<A-w>`, and another one, `<A-b>`:

<!-- include: docs/examples/modules/md-tools.scm scheme -->

`init.scm` brings them in. `md:` in front of the second `wrap` keeps the two functions apart. The binding at the bottom is yours, and it wins the key both modules wanted:

<!-- include: docs/examples/modules/init.scm scheme -->

`:reload` reads `init.scm` and both modules again.

Select a word and type `:text-tools/wrap ( )`. The status line says `Ran :text-tools/wrap` and the word is in parentheses. `:md-tools/wrap` bolds it instead, and the status line says `Ran :md-tools/wrap`. The short name `:wrap` belongs to neither module, so it changes nothing and says `wrap is ambiguous: md-tools/wrap, text-tools/wrap`. Defining `wrap` yourself in `init.scm` would make `:wrap` run yours, and both module commands would stay available under their longer names.

`<A-w>` in Visual mode runs the procedure from `init.scm` and the status line says `bolded`. `<A-b>` still runs the module's own binding. `F1` shows a Modules section under your commands and keys: each module's name, what it exports, and a note when a key of yours replaced one of theirs.

The name after `require` is the file's name: a lowercase letter, then lowercase letters, digits and `-`, up to 32 characters. The file is `modules/` plus that name plus `.scm`. `(require 'text-tools)` is the same as `(require text-tools)`. `(only wrap)` imports just that name, `(except wrap)` imports the rest, and `(rename wrap surround)` imports it under another name. `(prefix md:)` is what the file above uses. The options apply from left to right.

## Names

A name you don't export never leaves its file. Writing `surround` at the top of `init.scm` fails the load. `wrap`, in the module, still calls it.

An imported name is a copy. Changing it in your file doesn't change it in the module, and a change the module makes later isn't seen by you. Export a procedure when you want both files to share what it does.

An import has to land on a free name. These are errors, and the status line names both sides:

- `wrap is exported by text-tools and md-tools; use prefix, rename or only` when two modules export it and you didn't rename one.
- `car is a built-in name; use prefix or rename to import it from text-tools` when the name is already part of Scheme or of the editor.
- `wrap is defined in init.scm and also imported from text-tools; use prefix, rename or only` when the file defines the same name.

Defining a built-in name yourself, on purpose, stays inside that file. Other files, and the rest of Scheme, keep the original. `F1` notes it.

Commands, keys and themes are the editor's names, so `init.scm` decides them after every module has loaded. A key bound by two modules, and by no binding in `init.scm`, is an error. The first load leaves that key unbound, and a `:reload` that fails leaves the previous keys in place. A module can't pass `#t` to take a key the editor already uses. Only `init.scm` can. A theme a module defines is named with the module in front, such as `text-tools/dusk`, and only `init.scm` may call `set-theme`.

You can also copy modules from a GitHub repository: `:module-add owner/repo@v1` shows the code and installs it into `modules/` when you press `y`, after which `(require name)` works like any other. [Scheme and themes](extending.md) has the details and what it refuses.

Each file, including `init.scm`, may register one procedure with `on-start`. They run in the order the modules loaded, with yours last, and each is its own undo step. One that fails doesn't skip the others.
