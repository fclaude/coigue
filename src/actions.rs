//! The discoverable action surface; shortcuts remain available directly.
use crate::help::Topic;

pub const STRIP: &str = "F2 Files | F3 Code | F1 Help | F4 Actions";

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Group {
    Files,
    Code,
    Help,
    All,
}
impl Group {
    pub fn title(self) -> &'static str {
        match self {
            Self::Files => "Files",
            Self::Code => "Code",
            Self::Help => "Help",
            Self::All => "All actions",
        }
    }
    pub fn next(self) -> Self {
        match self {
            Self::Files => Self::Code,
            Self::Code => Self::Help,
            Self::Help => Self::All,
            Self::All => Self::Files,
        }
    }
}
#[derive(Clone, Copy)]
pub enum Action {
    Back,
    Forward,
    Recent,
    FindFile,
    Browse,
    FindText,
    GoLine,
    FirstLine,
    LastLine,
    Help(Topic),
}
pub struct Entry {
    pub group: Group,
    pub title: &'static str,
    pub shortcut: &'static str,
    pub action: Action,
}
pub const ENTRIES: &[Entry] = &[
    Entry {
        group: Group::Files,
        title: "Recent files",
        shortcut: ":recent",
        action: Action::Recent,
    },
    Entry {
        group: Group::Code,
        title: "Navigate back",
        shortcut: "Alt-Left / :back",
        action: Action::Back,
    },
    Entry {
        group: Group::Code,
        title: "Navigate forward",
        shortcut: "Alt-Right / :forward",
        action: Action::Forward,
    },
    Entry {
        group: Group::Files,
        title: "Find a file",
        shortcut: "Ctrl-P / :open",
        action: Action::FindFile,
    },
    Entry {
        group: Group::Files,
        title: "Browse and create files",
        shortcut: "Ctrl-O / :explore",
        action: Action::Browse,
    },
    Entry {
        group: Group::Code,
        title: "Find text in this file",
        shortcut: "/",
        action: Action::FindText,
    },
    Entry {
        group: Group::Code,
        title: "Go to line",
        shortcut: ":42",
        action: Action::GoLine,
    },
    Entry {
        group: Group::Code,
        title: "First line",
        shortcut: "gg",
        action: Action::FirstLine,
    },
    Entry {
        group: Group::Code,
        title: "Last line",
        shortcut: "G",
        action: Action::LastLine,
    },
    Entry {
        group: Group::Help,
        title: "Getting started",
        shortcut: "F1 / :help",
        action: Action::Help(Topic::Start),
    },
    Entry {
        group: Group::Help,
        title: "Editing and movement",
        shortcut: "F1",
        action: Action::Help(Topic::Editing),
    },
    Entry {
        group: Group::Help,
        title: "Finding files",
        shortcut: "F1",
        action: Action::Help(Topic::Files),
    },
    Entry {
        group: Group::Help,
        title: "Browsing and creating files",
        shortcut: "F1",
        action: Action::Help(Topic::Explorer),
    },
    Entry {
        group: Group::Help,
        title: "Commands and confirmations",
        shortcut: "F1",
        action: Action::Help(Topic::Prompts),
    },
];
pub struct Menu {
    pub group: Group,
    pub query: String,
    pub selected: usize,
}
impl Menu {
    pub fn new(group: Group) -> Self {
        Self {
            group,
            query: String::new(),
            selected: 0,
        }
    }
    pub fn entries(&self) -> Vec<&'static Entry> {
        let query = self.query.to_lowercase();
        ENTRIES
            .iter()
            .filter(|entry| {
                (self.group == Group::All || self.group == entry.group)
                    && query.split_whitespace().all(|word| {
                        format!("{} {}", entry.title, entry.shortcut)
                            .to_lowercase()
                            .contains(word)
                    })
            })
            .collect()
    }
    pub fn key(&mut self, code: crossterm::event::KeyCode, rows: usize) -> Option<Action> {
        use crossterm::event::KeyCode as K;
        match code {
            K::Char(c) => {
                self.query.push(c);
                self.selected = 0;
            }
            K::Backspace => {
                self.query.pop();
                self.selected = 0;
            }
            K::Tab => {
                self.group = self.group.next();
                self.query.clear();
                self.selected = 0;
            }
            K::Up => self.selected = self.selected.saturating_sub(1),
            K::Down => self.selected += 1,
            K::PageUp => self.selected = self.selected.saturating_sub(rows.max(1)),
            K::PageDown => self.selected = self.selected.saturating_add(rows.max(1)),
            K::Home => self.selected = 0,
            K::End => self.selected = self.entries().len().saturating_sub(1),
            K::Enter => return self.entries().get(self.selected).map(|entry| entry.action),
            _ => {}
        }
        self.selected = self.selected.min(self.entries().len().saturating_sub(1));
        None
    }
}
