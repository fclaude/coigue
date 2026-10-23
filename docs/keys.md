# Keys and commands

The same keys as [Using it](using.md), as tables you can scan. The tables come straight from the project's README, so they can't drift from it.

## Modes

Coigüe starts in Normal mode. `i`, `a`, `A` and `o` enter Insert mode, `v` and `V` enter Visual mode, and `:` opens the command line. `Esc` goes back to Normal mode from all of them. In Insert mode you can type text, press Enter, Tab and Backspace, and use the arrow keys. `Home` or `Ctrl-A` goes to the start of a line, and `End` or `Ctrl-E` to the end. `Page Up` or `Ctrl-B` and `Page Down` or `Ctrl-F` move by a screen.

The command line takes `:N` to jump to line N, `:w` to save, `:q` to quit, `:wq` to do both and `:q!` to quit and drop unsaved changes. Tab completes command names and theme names, `Tab` again cycles through the choices, and `Shift-Tab` goes backwards. `F1` or `:help` opens the guide.

## Editing in Normal mode

<!-- readme-table: Keys | Action -->

Counts repeat movements and edits, such as `5j`, `3w`, `2dd` and `4>>`. Counts before and after an operator multiply, so `2d3w` cuts six words, and they stop at 9999.

## Code navigation

<!-- readme-table: Action | Shortcut | Command -->

## The system clipboard

<!-- readme-table: Command/key | Action -->

## The directory browser

<!-- readme-table: Explorer keys | Action -->

## Themes

<!-- readme-table: Command | Theme -->

The names `catppuccin-mocha`, `gruvbox-dark` and `catppuccin-latte` also work. Themes you define in `init.scm` appear in the `:theme` list too, as [Scheme and themes](extending.md) explains.

## Keys from your init.scm

A script in `init.scm` can bind keys in Normal and Visual mode, and `:reload` reads the file again. Neither exists until you write the file, and `F1` lists what you bound. [Scheme and themes](extending.md) has the key syntax, the keys that need an explicit override and the ones that can't be bound.
