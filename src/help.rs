//! Read-only help overlays. They borrow the current editor state, never replace it.
use crossterm::event::KeyCode;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Topic {
    Start,
    Editing,
    Files,
    Explorer,
    Prompts,
    Code,
}
impl Topic {
    const ALL: [Self; 6] = [
        Self::Start,
        Self::Editing,
        Self::Files,
        Self::Explorer,
        Self::Prompts,
        Self::Code,
    ];

    pub fn title(self) -> &'static str {
        match self {
            Self::Start => "Getting started",
            Self::Editing => "Editing and movement",
            Self::Files => "Finding files",
            Self::Explorer => "Browsing and creating files",
            Self::Prompts => "Commands and confirmations",
            Self::Code => "Code navigation",
        }
    }
    pub fn lines(self) -> &'static [&'static str] {
        match self {
            Self::Code => &[
                "F3 Code lists every navigation action and its shortcut.",
                "",
                "F5 / :project-search      Search text across the launch directory",
                "F6 / :symbols             Find a symbol in this file",
                "F7 / :definition          Jump to the definition under the cursor",
                "F8 / :references          Find references to the current symbol",
                "F9 / :workspace-symbols   Search the language server's workspace",
                "Alt-Left / :back          Return to the previous location",
                "Alt-Right / :forward      Return forward through jump history",
                "",
                "Type to search/filter results; arrows choose, Enter opens.",
                "Esc returns without moving. F1 pauses a result list for help.",
                "A single definition opens directly; multiple ones show a list.",
                "Cross-file jumps ask about unsaved changes before opening.",
                "",
                "Project search is literal and case-sensitive, one hit per line.",
                "It respects Git ignore rules and includes the active unsaved text.",
                "Symbols, definitions and references need an installed language server.",
                "Unavailable or unsupported features leave project search usable.",
            ],
            Self::Start => &[
                "Start here",
                "F2 Files / F3 Code open searchable actions; F4 shows all actions.",
                "",
                "1. Ctrl-P finds a file. Type part of its path; Enter opens it.",
                "   Ctrl-O browses folders instead. In the browser, a makes a file.",
                "2. You start in Normal mode: arrows or h j k l move the cursor.",
                "3. Press i to insert text. Esc returns to Normal mode.",
                "4. Type :w then Enter to save. u undoes your last edit.",
                "5. Type :q then Enter to quit. Unsaved edits block quitting.",
                "   :wq saves and quits; :q! deliberately discards changes.",
                "",
                "Try it on a disposable file before editing something important.",
                "The mode, file name, and cursor position appear in the status row.",
                "[+] means the file has unsaved changes.",
                "",
                "F1 opens help anywhere. Esc closes help without changing your work.",
                "Tab changes help topic. Up/Down or Page Up/Down scroll help.",
            ],
            Self::Editing => &[
                "Normal mode: move first, then choose an editing action.",
                "",
                "h j k l / arrows     Move left, down, up, right",
                "w b e                Next word, previous word, word end",
                "0 / Home / Ctrl-A    Beginning of line",
                "$ / End / Ctrl-E     End of line",
                "gg / G               First / last line; :42 Enter goes to line 42",
                "Page Up / Ctrl-B     Move one screen up",
                "Page Down / Ctrl-F   Move one screen down",
                "/text Enter          Find literal text; n / N next / previous",
                "",
                "i / a / A / o        Insert / append / append at end / new line",
                "Esc                  Leave Insert mode",
                "x / dd / dw / cw     Cut character / line / word / change word",
                "yy / yw              Copy line / word into the internal register",
                "p / P                Paste after / before",
                "u / Ctrl-R           Undo / redo (Normal mode)",
                ">> / <<              Indent / unindent",
                "3j, 2dd, 2d3w        Counts repeat motions and editing commands",
                "",
                "An Insert session is one undo step, even if you open help during it.",
                "v / V                Select characters / lines; y copies, d cuts",
                "Visual Y             Copy selection to the system clipboard",
                ":clipboard-paste     Paste from the system clipboard",
                "gq                   Fill a Markdown paragraph; :textwidth sets width",
                "Ordinary y and p use the internal register, not the clipboard.",
            ],
            Self::Files => &[
                "Ctrl-P or :open finds files below the launch directory.",
                "",
                "Type part of a path  Fuzzy matching, e.g. s/edr for src/editor.rs",
                "Backspace            Refine your query",
                "Up / Down            Choose a result",
                "Page Up / Page Down  Move through results",
                "Enter                Open the selected file",
                "Esc                  Cancel; return to your previous screen",
                "",
                "Discovery runs in the background and respects Git ignore rules.",
                "Ctrl-O browses directories, starting beside your current file.",
                "F2 / Recent files lists this session's recently visited files.",
                "Alt-Left / Alt-Right return through successful navigation jumps.",
                ":back / :forward also work when your terminal reserves Alt keys.",
                "Opening a different file asks about unsaved changes first.",
                "Canceling keeps your text, cursor, and undo history.",
            ],
            Self::Explorer => &[
                "Ctrl-O / - / :explore opens the directory browser.",
                "",
                "j k / Up Down        Choose an entry",
                "Enter / l / Right    Enter a directory or open a file",
                "h / Left / -         Go to the parent directory",
                "/                    Filter names in this directory",
                "Tab while filtering  Keep the selection and enable file actions",
                "Esc while filtering  Cancel the filter",
                ".                    Show/hide hidden and ignored entries",
                "a / A                Create a file / directory",
                "r                    Rename or move; Ctrl-U clears the prompt",
                "d then y             Move an entry to recovery storage",
                "u                    Restore the latest deletion in this directory",
                "Ctrl-P               Find files recursively",
                "Esc / Ctrl-O         Return without changing your cursor or edits",
                "",
                "Creation and move paths are relative to the displayed directory.",
                "Existing destinations are never overwritten.",
                "Explorer u restores deleted files; editor u undoes text edits.",
            ],
            Self::Prompts => &[
                "Commands: type : in Normal mode, then a command and Enter.",
                "",
                ":help                Open this guide",
                ":w / :wq             Save / save and quit",
                ":q / :q!             Quit safely / discard changes and quit",
                ":42                  Jump to line 42",
                ":open / :explore     Find files / browse directories",
                ":theme               Show themes; :theme latte changes theme",
                ":lsp-restart         Retry the optional language server",
                "Tab / Shift-Tab      Complete command names forward / backward",
                "Esc                  Cancel a command or search",
                "",
                "When opening another file with unsaved edits:",
                "s saves and opens; d discards and opens; c or Esc cancels.",
                "Failed saves and failed opens preserve your current work.",
                "",
                "In create/move prompts, Enter confirms and Esc cancels.",
                "Deletion needs y to confirm; n or Esc cancels.",
                "F1 pauses any prompt to explain it; Esc returns to that prompt.",
            ],
        }
    }
}

pub struct Help {
    pub topic: Topic,
    pub scroll: usize,
}
impl Help {
    pub fn new(topic: Topic) -> Self {
        Self { topic, scroll: 0 }
    }
    pub fn key(&mut self, key: KeyCode, rows: usize) {
        match key {
            KeyCode::Tab | KeyCode::Right | KeyCode::BackTab | KeyCode::Left => {
                let index = Topic::ALL.iter().position(|t| *t == self.topic).unwrap();
                let backwards = matches!(key, KeyCode::BackTab | KeyCode::Left);
                let length = Topic::ALL.len();
                self.topic = Topic::ALL[(index + if backwards { length - 1 } else { 1 }) % length];
                self.scroll = 0;
            }
            KeyCode::Up | KeyCode::Char('k') => self.scroll = self.scroll.saturating_sub(1),
            KeyCode::Down | KeyCode::Char('j') => self.scroll += 1,
            KeyCode::PageUp => self.scroll = self.scroll.saturating_sub(rows.max(1)),
            KeyCode::PageDown => self.scroll = self.scroll.saturating_add(rows.max(1)),
            KeyCode::Home => self.scroll = 0,
            KeyCode::End => self.scroll = self.topic.lines().len().saturating_sub(rows.max(1)),
            _ => {}
        }
        self.scroll = self.scroll.min(self.topic.lines().len().saturating_sub(1));
    }
}
