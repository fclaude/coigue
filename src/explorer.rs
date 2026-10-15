//! A temporary, single-directory browser. Browsing never changes process cwd.
use std::{
    collections::HashMap,
    path::PathBuf,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
        mpsc::{self, Receiver},
    },
    thread,
};

#[derive(Default)]
pub struct Memory {
    positions: HashMap<PathBuf, Position>,
    pub show_all: bool,
}
struct Position {
    path: Option<PathBuf>,
    top: usize,
}
pub struct Entry {
    pub path: PathBuf,
    pub name: String,
    pub directory: bool,
}
struct Listing {
    entries: Vec<Entry>,
    error: String,
}
pub struct Explorer {
    pub directory: PathBuf,
    pub query: String,
    pub filtering: bool,
    pub selected: usize,
    pub top: usize,
    pub loading: bool,
    pub message: String,
    pub memory: Memory,
    entries: Vec<Entry>,
    matches: Vec<usize>,
    receiver: Receiver<Listing>,
    cancelled: Arc<AtomicBool>,
    focus: Option<PathBuf>,
    filter_origin: Option<(Option<PathBuf>, usize)>,
}
impl Explorer {
    pub fn new(directory: PathBuf, focus: Option<PathBuf>, memory: Memory) -> Self {
        let (_, receiver) = mpsc::channel();
        let mut view = Self {
            directory,
            query: String::new(),
            filtering: false,
            selected: 0,
            top: 0,
            loading: false,
            message: String::new(),
            memory,
            entries: Vec::new(),
            matches: Vec::new(),
            receiver,
            cancelled: Arc::new(AtomicBool::new(false)),
            focus: None,
            filter_origin: None,
        };
        view.load(focus);
        view
    }
    fn load(&mut self, reveal: Option<PathBuf>) {
        self.cancelled.store(true, Ordering::Relaxed);
        let (sender, receiver) = mpsc::channel();
        self.receiver = receiver;
        self.cancelled = Arc::new(AtomicBool::new(false));
        let stop = self.cancelled.clone();
        let directory = self.directory.clone();
        let show_all = self.memory.show_all;
        self.focus = reveal.clone().or_else(|| {
            self.memory
                .positions
                .get(&directory)
                .and_then(|p| p.path.clone())
        });
        self.top = self.memory.positions.get(&directory).map_or(0, |p| p.top);
        self.selected = 0;
        self.entries.clear();
        self.matches.clear();
        self.loading = true;
        self.message.clear();
        thread::spawn(move || {
            let mut entries = Vec::new();
            let mut errors = 0;
            let mut walk = ignore::WalkBuilder::new(&directory);
            walk.max_depth(Some(1))
                .hidden(!show_all)
                .ignore(false)
                .git_global(false)
                .git_ignore(!show_all)
                .git_exclude(!show_all)
                .parents(!show_all)
                .require_git(false)
                .follow_links(false)
                .filter_entry(|e| e.file_name() != ".git");
            for entry in walk.build() {
                if stop.load(Ordering::Relaxed) {
                    return;
                }
                match entry {
                    Ok(entry) => {
                        if entry.error().is_some() {
                            errors += 1;
                        }
                        if entry.depth() == 0 {
                            continue;
                        }
                        if let Some(kind) = entry.file_type()
                            && (kind.is_file() || kind.is_dir())
                        {
                            entries.push(Entry {
                                name: entry.file_name().to_string_lossy().into_owned(),
                                directory: kind.is_dir(),
                                path: entry.into_path(),
                            });
                        }
                    }
                    Err(_) => errors += 1,
                }
            }
            // Reveal the file being edited (or directory just left), even if
            // normally hidden or ignored. The visibility toggle remains explicit.
            if let Some(path) = reveal
                && path.parent() == Some(directory.as_path())
                && !entries.iter().any(|e| e.path == path)
                && path.file_name().is_some_and(|name| name != ".git")
                && let Ok(meta) = std::fs::symlink_metadata(&path)
                && (meta.is_file() || meta.is_dir())
            {
                entries.push(Entry {
                    name: path.file_name().unwrap().to_string_lossy().into_owned(),
                    directory: meta.is_dir(),
                    path,
                });
            }
            if let Some(parent) = directory.parent() {
                entries.push(Entry {
                    name: "..".into(),
                    path: parent.into(),
                    directory: true,
                });
            }
            entries.sort_by(|a, b| {
                b.directory
                    .cmp(&a.directory)
                    .then_with(|| a.name.cmp(&b.name))
            });
            let error = if errors > 0 {
                format!("{errors} unreadable entries or ignore errors")
            } else {
                String::new()
            };
            let _ = sender.send(Listing { entries, error });
        });
    }
    pub fn poll(&mut self) {
        if let Ok(listing) = self.receiver.try_recv() {
            self.entries = listing.entries;
            self.message = listing.error;
            self.loading = false;
            let top = self.top;
            self.filter();
            if self.query.is_empty() {
                self.top = top;
            }
            if let Some(focus) = self.focus.take()
                && let Some(index) = self
                    .matches
                    .iter()
                    .position(|&i| self.entries[i].path == focus)
            {
                self.selected = index;
            }
        }
    }
    pub fn remember(&mut self) {
        if !self.loading {
            self.memory.positions.insert(
                self.directory.clone(),
                Position {
                    path: self.entry(self.selected).map(|e| e.path.clone()),
                    top: self.top,
                },
            );
        }
    }
    pub fn navigate(&mut self, directory: PathBuf, focus: Option<PathBuf>) {
        self.remember();
        self.directory = directory;
        self.query.clear();
        self.filtering = false;
        self.filter_origin = None;
        self.load(focus);
    }
    pub fn parent(&mut self) {
        if let Some(parent) = self.directory.parent() {
            self.navigate(parent.into(), Some(self.directory.clone()));
        }
    }
    pub fn toggle_visibility(&mut self) {
        self.remember();
        self.memory.show_all = !self.memory.show_all;
        self.load(None);
    }
    pub fn start_filter(&mut self) {
        self.filter_origin = Some((self.entry(self.selected).map(|e| e.path.clone()), self.top));
        self.filtering = true;
        self.query.clear();
    }
    pub fn cancel_filter(&mut self) {
        self.query.clear();
        self.filtering = false;
        self.filter();
        if let Some((Some(path), top)) = self.filter_origin.take()
            && let Some(index) = self
                .matches
                .iter()
                .position(|&i| self.entries[i].path == path)
        {
            self.selected = index;
            self.top = top;
        }
    }
    pub fn filter(&mut self) {
        let query = self.query.to_lowercase();
        let mut ranked: Vec<_> = self
            .entries
            .iter()
            .enumerate()
            .filter_map(|(i, e)| crate::picker::score(&e.name, &query).map(|score| (i, score)))
            .collect();
        ranked.sort_by(|(a, sa), (b, sb)| {
            self.entries[*b]
                .directory
                .cmp(&self.entries[*a].directory)
                .then_with(|| sb.cmp(sa))
                .then_with(|| self.entries[*a].name.cmp(&self.entries[*b].name))
        });
        self.matches = ranked.into_iter().map(|(i, _)| i).collect();
        self.selected = 0;
        self.top = 0;
    }
    pub fn move_by(&mut self, delta: isize) {
        self.selected = self
            .selected
            .saturating_add_signed(delta)
            .min(self.len().saturating_sub(1));
    }
    pub fn fit(&mut self, rows: usize) {
        let rows = rows.max(1);
        if self.selected < self.top {
            self.top = self.selected;
        }
        if self.selected >= self.top + rows {
            self.top = self.selected + 1 - rows;
        }
        self.top = self.top.min(self.len().saturating_sub(rows));
    }
    pub fn len(&self) -> usize {
        self.matches.len()
    }
    pub fn entry(&self, index: usize) -> Option<&Entry> {
        self.matches.get(index).map(|&i| &self.entries[i])
    }
}
impl Drop for Explorer {
    fn drop(&mut self) {
        self.cancelled.store(true, Ordering::Relaxed);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn ready(view: &mut Explorer) {
        let deadline = std::time::Instant::now() + std::time::Duration::from_secs(3);
        while view.loading {
            view.poll();
            assert!(std::time::Instant::now() < deadline);
            thread::yield_now();
        }
        view.fit(4);
    }
    fn names(view: &Explorer) -> Vec<&str> {
        (0..view.len())
            .map(|i| view.entry(i).unwrap().name.as_str())
            .collect()
    }
    #[test]
    fn directories_first_visibility_filtering_and_revealing_current_file() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().canonicalize().unwrap();
        std::fs::create_dir(root.join("nested")).unwrap();
        std::fs::create_dir(root.join(".git")).unwrap();
        for name in ["alpha", "café.rs", ".secret", "generated"] {
            std::fs::write(root.join(name), "").unwrap();
        }
        std::fs::write(root.join(".gitignore"), "generated\n").unwrap();
        #[cfg(unix)]
        std::os::unix::fs::symlink(&root, root.join("loop")).unwrap();
        let mut view = Explorer::new(
            root.clone(),
            Some(root.join("generated")),
            Memory::default(),
        );
        ready(&mut view);
        assert_eq!(
            names(&view),
            ["..", "nested", "alpha", "café.rs", "generated"]
        );
        assert_eq!(view.entry(view.selected).unwrap().name, "generated");
        view.toggle_visibility();
        ready(&mut view);
        assert!(names(&view).contains(&".secret"));
        assert!(!names(&view).contains(&".git"));
        assert!(!names(&view).contains(&"loop"));
        view.toggle_visibility();
        ready(&mut view);
        assert!(!names(&view).contains(&"generated"));
        view.selected = 3;
        view.fit(2);
        let origin = (view.selected, view.top);
        view.start_filter();
        view.query = "CF".into();
        view.filter();
        assert_eq!(names(&view), ["café.rs"]);
        view.query = "absent".into();
        view.filter();
        view.move_by(50);
        view.fit(1);
        assert_eq!(view.len(), 0);
        assert_eq!(view.selected, 0);
        view.cancel_filter();
        assert_eq!((view.selected, view.top), origin);
    }
    #[test]
    fn parent_focus_and_directory_selection_scroll_are_remembered() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().canonicalize().unwrap();
        let child = root.join("child");
        std::fs::create_dir(&child).unwrap();
        for i in 0..15 {
            std::fs::write(child.join(format!("file{i:02}")), "").unwrap();
        }
        let mut view = Explorer::new(child.clone(), Some(child.join("file12")), Memory::default());
        ready(&mut view);
        let top = view.top;
        assert!(top > 0);
        view.parent();
        ready(&mut view);
        assert_eq!(view.entry(view.selected).unwrap().path, child);
        view.navigate(child.clone(), None);
        ready(&mut view);
        assert_eq!(view.entry(view.selected).unwrap().name, "file12");
        assert_eq!(view.top, top);
        view.remember();
        let memory = std::mem::take(&mut view.memory);
        let mut reopened = Explorer::new(child, None, memory);
        ready(&mut reopened);
        assert_eq!(reopened.entry(reopened.selected).unwrap().name, "file12");
        assert_eq!(reopened.top, top);
    }
    #[test]
    fn missing_directory_is_reported_and_can_be_left() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().canonicalize().unwrap();
        let mut view = Explorer::new(root.join("missing"), None, Memory::default());
        ready(&mut view);
        assert!(!view.message.is_empty());
        view.parent();
        ready(&mut view);
        assert_eq!(view.directory, root);
    }
}
