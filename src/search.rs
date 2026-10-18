//! Cancellable, bounded project search, independent of language servers.
use crate::navigation::{Location, Target};
use std::{
    io::Read,
    path::PathBuf,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
        mpsc::{self, Receiver, TryRecvError},
    },
};

const FILE_LIMIT: u64 = 2 * 1024 * 1024;
const RESULT_LIMIT: usize = 1000;
enum Batch {
    Hits(Vec<Target>),
    Done { skipped: usize, capped: bool },
}
pub struct Search {
    stop: Arc<AtomicBool>,
    receiver: Receiver<Batch>,
}
impl Search {
    pub fn start(root: PathBuf, query: String, active: Option<(PathBuf, String)>) -> Self {
        let (sender, receiver) = mpsc::sync_channel(8);
        let stop = Arc::new(AtomicBool::new(false));
        let canceled = stop.clone();
        std::thread::spawn(move || {
            if query.is_empty() {
                let _ = sender.send(Batch::Done {
                    skipped: 0,
                    capped: false,
                });
                return;
            }
            let mut walker = crate::picker::walker(root.clone());
            walker.sort_by_file_path(|a, b| a.cmp(b));
            let mut skipped = 0;
            let mut count = 0;
            let mut batch = Vec::new();
            for entry in walker.build() {
                if canceled.load(Ordering::Relaxed) {
                    return;
                }
                let entry = match entry {
                    Ok(entry) => entry,
                    Err(_) => {
                        skipped += 1;
                        continue;
                    }
                };
                if !entry.file_type().is_some_and(|kind| kind.is_file()) {
                    continue;
                }
                if entry.error().is_some() {
                    skipped += 1;
                }
                let path = entry.into_path();
                let content = (|| {
                    if let Some((current, text)) = &active
                        && current == &path
                    {
                        return (text.len() as u64 <= FILE_LIMIT).then(|| text.clone());
                    }
                    let file = std::fs::File::open(&path).ok()?;
                    if file.metadata().ok()?.len() > FILE_LIMIT {
                        return None;
                    }
                    let mut text = String::new();
                    file.take(FILE_LIMIT + 1).read_to_string(&mut text).ok()?;
                    (text.len() as u64 <= FILE_LIMIT && !text.contains('\0')).then_some(text)
                })();
                let Some(content) = content else {
                    skipped += 1;
                    continue;
                };
                for (row, line) in content.split('\n').enumerate() {
                    if canceled.load(Ordering::Relaxed) {
                        return;
                    }
                    let Some(byte) = line.find(&query) else {
                        continue;
                    };
                    let mut location = Location::new(path.clone());
                    location.row = row;
                    location.col = line[..byte].chars().count();
                    let label = format!(
                        "{}:{}:{}  {}",
                        path.strip_prefix(&root).unwrap_or(&path).display(),
                        row + 1,
                        location.col + 1,
                        line.trim().chars().take(180).collect::<String>()
                    );
                    batch.push(Target { location, label });
                    count += 1;
                    if batch.len() == 32
                        && sender
                            .send(Batch::Hits(std::mem::take(&mut batch)))
                            .is_err()
                    {
                        return;
                    }
                    if count == RESULT_LIMIT {
                        break;
                    }
                }
                if count == RESULT_LIMIT {
                    break;
                }
            }
            if !batch.is_empty() && sender.send(Batch::Hits(batch)).is_err() {
                return;
            }
            let _ = sender.send(Batch::Done {
                skipped,
                capped: count == RESULT_LIMIT,
            });
        });
        Self { stop, receiver }
    }
    pub fn poll(&self, list: &mut crate::navigation::List) {
        for _ in 0..8 {
            match self.receiver.try_recv() {
                Ok(Batch::Hits(hits)) => list.targets.extend(hits),
                Ok(Batch::Done { skipped, capped }) => {
                    list.busy = false;
                    list.notice = format!(
                        "{} matching lines; {} skipped{}",
                        list.targets.len(),
                        skipped,
                        if capped {
                            "; first 1000 results, refine search"
                        } else {
                            ""
                        }
                    );
                    break;
                }
                Err(TryRecvError::Disconnected) => {
                    list.busy = false;
                    break;
                }
                Err(TryRecvError::Empty) => break,
            }
        }
    }
}
impl Drop for Search {
    fn drop(&mut self) {
        self.stop.store(true, Ordering::Relaxed);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn collect(
        root: &std::path::Path,
        query: &str,
        active: Option<(PathBuf, String)>,
    ) -> crate::navigation::List {
        let search = Search::start(root.into(), query.into(), active);
        let mut list = crate::navigation::List::new("Search", Vec::new());
        list.busy = true;
        let deadline = std::time::Instant::now() + std::time::Duration::from_secs(3);
        while list.busy {
            search.poll(&mut list);
            assert!(std::time::Instant::now() < deadline);
            std::thread::sleep(std::time::Duration::from_millis(2));
        }
        list
    }
    #[test]
    fn search_respects_discovery_limits_and_unsaved_unicode_positions() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();
        std::fs::write(root.join(".gitignore"), "ignored.txt\n").unwrap();
        for name in [".git", crate::fileops::TRASH] {
            std::fs::create_dir(root.join(name)).unwrap();
            std::fs::write(root.join(name).join("private"), "needle").unwrap();
        }
        std::fs::write(root.join("ignored.txt"), "needle").unwrap();
        std::fs::write(root.join("binary"), b"needle\0").unwrap();
        std::fs::write(root.join("invalid"), [255, 254]).unwrap();
        std::fs::write(root.join("large"), vec![b'x'; FILE_LIMIT as usize + 1]).unwrap();
        let active = root.join("active.rs");
        std::fs::write(&active, "disk text").unwrap();
        std::fs::write(root.join("other.rs"), "NEEDLE\nneedle needle\n").unwrap();
        let list = collect(
            root,
            "needle",
            Some((active, "a😀 needle\r\nunsaved".into())),
        );
        assert_eq!(list.targets.len(), 2);
        let hit = list
            .targets
            .iter()
            .find(|t| t.location.path.ends_with("active.rs"))
            .unwrap();
        assert_eq!((hit.location.row, hit.location.col), (0, 3));
        assert!(list.notice.contains("3 skipped"));
        assert!(
            list.targets
                .iter()
                .all(|t| !t.location.path.ends_with("ignored.txt"))
        );
        assert!(collect(root, "", None).targets.is_empty());
    }
    #[test]
    fn search_caps_results_and_drops_a_blocked_producer() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join("many"), "hit\n".repeat(1500)).unwrap();
        let list = collect(dir.path(), "hit", None);
        assert_eq!(list.targets.len(), RESULT_LIMIT);
        assert!(list.notice.contains("first 1000"));
        let search = Search::start(dir.path().into(), "hit".into(), None);
        // More than eight batches fills the bounded channel. Dropping the
        // consumer must release the producer rather than retaining the scan.
        drop(search);
    }
}
