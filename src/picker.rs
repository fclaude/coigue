//! Built-in, cancellable file discovery and deterministic fuzzy path ranking.
use std::{
    path::{Path, PathBuf},
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
        mpsc::{self, Receiver, TryRecvError},
    },
    thread,
};

pub struct Picker {
    pub root: PathBuf,
    pub query: String,
    pub selected: usize,
    pub scanning: bool,
    pub skipped: usize,
    files: Vec<PathBuf>,
    labels: Vec<String>,
    matches: Vec<usize>,
    receiver: Receiver<Batch>,
    cancelled: Arc<AtomicBool>,
}
struct Batch {
    files: Vec<PathBuf>,
    skipped: usize,
}
impl Picker {
    pub fn new(root: PathBuf) -> Self {
        let (sender, receiver) = mpsc::channel();
        let cancelled = Arc::new(AtomicBool::new(false));
        let stop = cancelled.clone();
        let scan_root = root.clone();
        thread::spawn(move || {
            let mut walker = ignore::WalkBuilder::new(scan_root);
            walker
                .hidden(false)
                .ignore(false)
                .git_global(false)
                .require_git(false)
                .follow_links(false)
                .filter_entry(|entry| entry.file_name() != ".git");
            let mut batch = Batch {
                files: Vec::new(),
                skipped: 0,
            };
            for entry in walker.build() {
                if stop.load(Ordering::Relaxed) {
                    return;
                }
                match entry {
                    Ok(entry) => {
                        if entry.error().is_some() {
                            batch.skipped += 1;
                        }
                        if entry.file_type().is_some_and(|kind| kind.is_file()) {
                            batch.files.push(entry.into_path());
                        }
                    }
                    Err(_) => batch.skipped += 1,
                }
                if batch.files.len() >= 128 {
                    if sender.send(batch).is_err() {
                        return;
                    }
                    batch = Batch {
                        files: Vec::new(),
                        skipped: 0,
                    };
                }
            }
            let _ = sender.send(batch);
        });
        Self {
            root,
            query: String::new(),
            selected: 0,
            scanning: true,
            skipped: 0,
            files: Vec::new(),
            labels: Vec::new(),
            matches: Vec::new(),
            receiver,
            cancelled,
        }
    }
    pub fn poll(&mut self) {
        let mut changed = false;
        // Bound work per tick so keyboard input remains responsive during discovery.
        for _ in 0..32 {
            match self.receiver.try_recv() {
                Ok(batch) => {
                    self.skipped += batch.skipped;
                    for path in batch.files {
                        self.labels.push(
                            path.strip_prefix(&self.root)
                                .unwrap_or(&path)
                                .to_string_lossy()
                                .into_owned(),
                        );
                        self.files.push(path);
                        changed = true;
                    }
                }
                Err(TryRecvError::Disconnected) => {
                    self.scanning = false;
                    break;
                }
                Err(TryRecvError::Empty) => break,
            }
        }
        if changed {
            self.filter();
        }
    }
    fn filter(&mut self) {
        let selected = self.matches.get(self.selected).copied();
        let query = self.query.to_lowercase();
        let mut ranked: Vec<_> = self
            .labels
            .iter()
            .enumerate()
            .filter_map(|(i, label)| score(label, &query).map(|score| (i, score)))
            .collect();
        ranked.sort_unstable_by(|(a, sa), (b, sb)| {
            sb.cmp(sa)
                .then_with(|| self.labels[*a].cmp(&self.labels[*b]))
        });
        self.matches = ranked.into_iter().map(|(i, _)| i).collect();
        self.selected = selected
            .and_then(|i| self.matches.iter().position(|&m| m == i))
            .unwrap_or(0);
    }
    pub fn query_changed(&mut self) {
        self.selected = 0;
        self.filter();
        self.selected = 0;
    }
    pub fn move_by(&mut self, delta: isize) {
        self.selected = self
            .selected
            .saturating_add_signed(delta)
            .min(self.matches.len().saturating_sub(1));
    }
    pub fn len(&self) -> usize {
        self.matches.len()
    }
    pub fn total(&self) -> usize {
        self.files.len()
    }
    pub fn label(&self, index: usize) -> Option<&str> {
        self.matches.get(index).map(|&i| self.labels[i].as_str())
    }
    pub fn path(&self) -> Option<&Path> {
        self.matches
            .get(self.selected)
            .map(|&i| self.files[i].as_path())
    }
}
impl Drop for Picker {
    fn drop(&mut self) {
        self.cancelled.store(true, Ordering::Relaxed);
    }
}

// Ordered subsequences, rewarded for consecutive characters, word boundaries,
// and basename matches. Shorter paths win otherwise comparable matches.
fn score(label: &str, query: &str) -> Option<i64> {
    if query.is_empty() {
        return Some(0);
    }
    let lower = label.to_lowercase();
    let chars: Vec<_> = lower.chars().collect();
    let basename = chars.iter().rposition(|&c| c == '/').map_or(0, |i| i + 1);
    let mut previous: Vec<Option<i64>> = vec![None; chars.len()];
    for (query_index, needle) in query.chars().enumerate() {
        let mut current = vec![None; chars.len()];
        let mut prefix_best: Option<i64> = None;
        for (index, &c) in chars.iter().enumerate() {
            if c == needle {
                let bonus =
                    10 + if index == 0 || matches!(chars[index - 1], '/' | '_' | '-' | '.' | ' ') {
                        15
                    } else {
                        0
                    } + if index >= basename { 10 } else { 0 };
                let base = if query_index == 0 {
                    Some(0)
                } else {
                    // Find the best alignment, including matches later in the
                    // path; an earlier directory must not hide a basename match.
                    let consecutive = index
                        .checked_sub(1)
                        .and_then(|i| previous[i])
                        .map(|score| score + 20);
                    prefix_best.max(consecutive)
                };
                current[index] = base.map(|score| score + bonus);
            }
            prefix_best = prefix_best.max(previous[index]);
        }
        previous = current;
    }
    previous
        .into_iter()
        .flatten()
        .max()
        .map(|score| score - chars.len() as i64)
}

#[cfg(test)]
mod tests {
    use super::*;
    fn discovered(root: &Path) -> std::collections::BTreeSet<String> {
        let mut picker = Picker::new(root.into());
        let deadline = std::time::Instant::now() + std::time::Duration::from_secs(3);
        while picker.scanning {
            picker.poll();
            assert!(std::time::Instant::now() < deadline);
            thread::yield_now();
        }
        picker.labels.iter().cloned().collect()
    }
    #[test]
    fn gitignore_patterns_nested_overrides_and_excluded_directories() {
        let dir = tempfile::tempdir().unwrap();
        for name in [".git/info", "src/deep", "target", "cache"] {
            std::fs::create_dir_all(dir.path().join(name)).unwrap();
        }
        std::fs::write(dir.path().join(".gitignore"),
            "*.log\n!keep.log\n/root.txt\ntarget/\ncache/*\n!cache/keep.txt\n**/generated-*.rs\n\\#literal\n\\!literal\nwith space.txt\n").unwrap();
        std::fs::write(
            dir.path().join("src/.gitignore"),
            "!local.log\nprivate.rs\n",
        )
        .unwrap();
        std::fs::write(dir.path().join(".git/info/exclude"), "local-only\n").unwrap();
        for name in [
            "debug.log",
            "keep.log",
            "root.txt",
            "src/root.txt",
            "src/local.log",
            "src/deep/debug.log",
            "src/private.rs",
            "src/main.rs",
            "src/generated-code.rs",
            "target/artifact",
            "cache/drop.txt",
            "cache/keep.txt",
            "#literal",
            "!literal",
            "with space.txt",
            "local-only",
            ".hidden",
            "visible",
        ] {
            std::fs::write(dir.path().join(name), "").unwrap();
        }
        let expected: std::collections::BTreeSet<_> = [
            ".gitignore",
            "src/.gitignore",
            "keep.log",
            "src/root.txt",
            "src/local.log",
            "src/main.rs",
            "cache/keep.txt",
            ".hidden",
            "visible",
        ]
        .into_iter()
        .map(String::from)
        .collect();
        assert_eq!(discovered(dir.path()), expected);
        // Starting below the repo root still honors parent rules, but returns
        // only descendants of the working directory.
        let nested: std::collections::BTreeSet<_> =
            [".gitignore", "root.txt", "local.log", "main.rs"]
                .into_iter()
                .map(String::from)
                .collect();
        assert_eq!(discovered(&dir.path().join("src")), nested);
    }
    #[test]
    fn gitignore_works_without_a_repository_and_reloads_on_reopen() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join(".gitignore"), "excluded\n").unwrap();
        std::fs::write(dir.path().join("excluded"), "").unwrap();
        assert!(!discovered(dir.path()).contains("excluded"));
        std::fs::write(dir.path().join(".gitignore"), "").unwrap();
        assert!(discovered(dir.path()).contains("excluded"));
    }
    #[test]
    fn fuzzy_paths_unicode_and_ranking() {
        assert!(score("src/editor.rs", "s/edr").is_some());
        assert!(score("src/editor.rs", "deri").is_none());
        assert!(score("CAFÉ.rs", "café").is_some());
        assert!(score("rust/main.rs", "rs").unwrap() > score("rust/misc.txt", "rs").unwrap());
        assert!(score("a.rs", "aa").is_none());
        assert!(
            score("src/main.rs", "main").unwrap()
                > score("many/ancient/items/notes.rs", "main").unwrap()
        );
    }
    #[test]
    fn unreadable_root_and_cancellation() {
        let dir = tempfile::tempdir().unwrap();
        let mut picker = Picker::new(dir.path().join("missing"));
        let deadline = std::time::Instant::now() + std::time::Duration::from_secs(3);
        while picker.scanning {
            picker.poll();
            assert!(std::time::Instant::now() < deadline);
            thread::yield_now();
        }
        assert_eq!(picker.skipped, 1);
        assert_eq!(picker.len(), 0);
        let stop = picker.cancelled.clone();
        drop(picker);
        assert!(stop.load(Ordering::Relaxed));
    }
    #[test]
    fn discovery_is_recursive_and_skips_git_and_symlinks() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::create_dir_all(dir.path().join("nested/.git")).unwrap();
        for name in [
            "nested/café.rs",
            "alpha.rs",
            ".hidden",
            "nested/.git/config",
        ] {
            std::fs::write(dir.path().join(name), "").unwrap();
        }
        #[cfg(unix)]
        std::os::unix::fs::symlink(dir.path(), dir.path().join("loop")).unwrap();
        let mut picker = Picker::new(dir.path().into());
        let deadline = std::time::Instant::now() + std::time::Duration::from_secs(3);
        while picker.scanning {
            picker.poll();
            assert!(std::time::Instant::now() < deadline);
            thread::yield_now();
        }
        assert_eq!(picker.total(), 3);
        assert_eq!(picker.label(0), Some(".hidden"));
        picker.query = "N/CF".into();
        picker.query_changed();
        assert_eq!(picker.len(), 1);
        assert_eq!(picker.label(0), Some("nested/café.rs"));
        assert!(picker.path().unwrap().ends_with("nested/café.rs"));
        picker.query = "no-match".into();
        picker.query_changed();
        picker.move_by(100);
        assert!(picker.path().is_none());
    }
}
