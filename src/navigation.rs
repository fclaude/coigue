//! Successful jumps, rather than key presses, define navigation history.
use std::path::{Path, PathBuf};

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Location {
    pub path: PathBuf,
    pub row: usize,
    pub col: usize,
    pub top: usize,
    pub left: usize,
}
impl Location {
    pub fn new(path: PathBuf) -> Self {
        let path = path
            .canonicalize()
            .unwrap_or_else(|_| std::path::absolute(&path).unwrap_or(path));
        Self {
            path,
            row: 0,
            col: 0,
            top: 0,
            left: 0,
        }
    }
}
#[derive(Clone, Copy)]
pub enum Travel {
    New,
    Back,
    Forward,
}
#[derive(Default)]
pub struct History {
    back: Vec<Location>,
    forward: Vec<Location>,
    pub recent: Vec<Location>,
}
impl History {
    pub fn destination(&self, travel: Travel) -> Option<Location> {
        match travel {
            Travel::Back => self.back.last(),
            Travel::Forward => self.forward.last(),
            Travel::New => None,
        }
        .cloned()
    }
    pub fn visit(&mut self, location: Location) {
        self.recent.retain(|old| old.path != location.path);
        self.recent.insert(0, location);
        self.recent.truncate(50);
    }
    fn push(stack: &mut Vec<Location>, location: Location) {
        if stack.last() != Some(&location) {
            stack.push(location);
        }
        if stack.len() > 100 {
            stack.remove(0);
        }
    }
    pub fn depart(&mut self, origin: Location) {
        Self::push(&mut self.back, origin.clone());
        self.forward.clear();
        self.visit(origin);
    }
    pub fn arrived(&mut self, origin: Option<Location>, target: Location, travel: Travel) {
        if let Some(origin) = origin {
            match travel {
                Travel::New if origin != target => {
                    Self::push(&mut self.back, origin.clone());
                    self.forward.clear();
                }
                Travel::Back => {
                    self.back.pop();
                    Self::push(&mut self.forward, origin.clone());
                }
                Travel::Forward => {
                    self.forward.pop();
                    Self::push(&mut self.back, origin.clone());
                }
                Travel::New => {}
            }
            self.visit(origin);
        }
        self.visit(target);
    }
    pub fn rename(&mut self, source: &Path, target: &Path) {
        for location in self
            .back
            .iter_mut()
            .chain(&mut self.forward)
            .chain(&mut self.recent)
        {
            if let Ok(suffix) = location.path.strip_prefix(source) {
                location.path = target.join(suffix);
            }
        }
    }
}
#[derive(Clone)]
pub struct Target {
    pub location: Location,
    pub label: String,
}
#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Source {
    Recent,
    Project,
    Symbols,
    Definition,
    References,
    Workspace,
}
impl Source {
    pub fn live(self) -> bool {
        matches!(self, Self::Project | Self::Workspace)
    }
}
pub struct List {
    pub source: Source,
    pub busy: bool,
    pub notice: String,
    pub title: String,
    pub query: String,
    pub selected: usize,
    pub targets: Vec<Target>,
}
impl List {
    pub fn new(title: &str, targets: Vec<Target>) -> Self {
        Self {
            source: Source::Recent,
            busy: false,
            notice: String::new(),
            title: title.into(),
            query: String::new(),
            selected: 0,
            targets,
        }
    }
    pub fn matches(&self) -> Vec<&Target> {
        let query = self.query.to_lowercase();
        self.targets
            .iter()
            .filter(|target| {
                let label = target.label.to_lowercase();
                self.source.live() || query.split_whitespace().all(|word| label.contains(word))
            })
            .collect()
    }
    pub fn key(&mut self, code: crossterm::event::KeyCode, rows: usize) -> Option<Location> {
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
            K::Up => self.selected = self.selected.saturating_sub(1),
            K::Down => self.selected += 1,
            K::PageUp => self.selected = self.selected.saturating_sub(rows.max(1)),
            K::PageDown => self.selected = self.selected.saturating_add(rows.max(1)),
            K::Home => self.selected = 0,
            K::End => self.selected = self.matches().len().saturating_sub(1),
            K::Enter => {
                return self
                    .matches()
                    .get(self.selected)
                    .map(|t| t.location.clone());
            }
            _ => {}
        }
        self.selected = self.selected.min(self.matches().len().saturating_sub(1));
        None
    }
}
