//! Non-overwriting filesystem operations and persistent, same-filesystem recovery.
use std::{
    fs,
    io::{self, Write},
    path::{Component, Path, PathBuf},
};
pub const TRASH: &str = ".coigue-trash";

fn permitted(path: &Path) -> io::Result<()> {
    if path
        .components()
        .any(|c| matches!(c, Component::Normal(name) if name == ".git" || name == TRASH))
    {
        return Err(io::Error::other(
            "Git metadata and recovery storage cannot be modified",
        ));
    }
    Ok(())
}
pub fn destination(directory: &Path, input: &str) -> io::Result<PathBuf> {
    if input.is_empty() || input.chars().any(char::is_control) {
        return Err(io::Error::other(
            "Enter a nonempty path without control characters",
        ));
    }
    let path = directory.join(input);
    permitted(&path)?;
    // Resolve the parent, but never follow an existing destination symlink.
    let name = path
        .file_name()
        .ok_or_else(|| io::Error::other("A file or directory name is required"))?;
    let parent = path
        .parent()
        .ok_or_else(|| io::Error::other("A parent directory is required"))?
        .canonicalize()?;
    let resolved = parent.join(name);
    permitted(&resolved)?;
    Ok(resolved)
}
fn source(path: &Path) -> io::Result<()> {
    permitted(path)?;
    let meta = fs::symlink_metadata(path)?;
    if !meta.is_file() && !meta.is_dir() {
        return Err(io::Error::other(
            "Only regular files and directories can be changed",
        ));
    }
    Ok(())
}
pub fn create(path: &Path, directory: bool) -> io::Result<()> {
    permitted(path)?;
    if directory {
        fs::create_dir(path)
    } else {
        fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(path)?
            .sync_all()
    }
}
// A preflight exists() check followed by std::fs::rename could overwrite a
// concurrently-created destination. Use the OS no-replace rename primitive.
fn rename_exclusive(from: &Path, to: &Path) -> io::Result<()> {
    #[cfg(any(target_os = "macos", target_os = "linux"))]
    {
        use std::{ffi::CString, os::unix::ffi::OsStrExt};
        let from = CString::new(from.as_os_str().as_bytes()).map_err(io::Error::other)?;
        let to = CString::new(to.as_os_str().as_bytes()).map_err(io::Error::other)?;
        // SAFETY: both C strings live through the call and are NUL-terminated.
        #[cfg(target_os = "macos")]
        let result = unsafe { libc::renamex_np(from.as_ptr(), to.as_ptr(), libc::RENAME_EXCL) };
        #[cfg(target_os = "linux")]
        let result = unsafe {
            libc::renameat2(
                libc::AT_FDCWD,
                from.as_ptr(),
                libc::AT_FDCWD,
                to.as_ptr(),
                libc::RENAME_NOREPLACE,
            )
        };
        if result == 0 {
            Ok(())
        } else {
            Err(io::Error::last_os_error())
        }
    }
    #[cfg(not(any(target_os = "macos", target_os = "linux")))]
    {
        let _ = (from, to);
        Err(io::Error::other(
            "Non-overwriting moves require Linux or macOS",
        ))
    }
}
pub fn move_entry(from: &Path, to: &Path) -> io::Result<()> {
    source(from)?;
    permitted(to)?;
    if from == to {
        return Ok(());
    }
    if to.starts_with(from) {
        return Err(io::Error::other("Cannot move a directory into itself"));
    }
    rename_exclusive(from, to)
}
fn trash_root(directory: &Path, create: bool) -> io::Result<PathBuf> {
    let root = directory.join(TRASH);
    if create {
        let mut builder = fs::DirBuilder::new();
        #[cfg(unix)]
        {
            use std::os::unix::fs::DirBuilderExt;
            builder.mode(0o700);
        }
        match builder.create(&root) {
            Ok(()) => {}
            Err(err) if err.kind() == io::ErrorKind::AlreadyExists => {}
            Err(err) => return Err(err),
        }
    }
    let meta = fs::symlink_metadata(&root)?;
    if !meta.is_dir() {
        return Err(io::Error::other(
            "Recovery storage must be a real directory",
        ));
    }
    Ok(root)
}
#[cfg(unix)]
fn encode(path: &Path) -> io::Result<Vec<u8>> {
    use std::os::unix::ffi::OsStrExt;
    Ok(path.as_os_str().as_bytes().to_vec())
}
#[cfg(unix)]
fn decode(bytes: Vec<u8>) -> io::Result<PathBuf> {
    use std::os::unix::ffi::OsStringExt;
    Ok(std::ffi::OsString::from_vec(bytes).into())
}
#[cfg(not(unix))]
fn encode(path: &Path) -> io::Result<Vec<u8>> {
    path.to_str()
        .map(|s| s.as_bytes().to_vec())
        .ok_or_else(|| io::Error::other("Path is not UTF-8"))
}
#[cfg(not(unix))]
fn decode(bytes: Vec<u8>) -> io::Result<PathBuf> {
    String::from_utf8(bytes)
        .map(PathBuf::from)
        .map_err(io::Error::other)
}
pub fn trash(path: &Path) -> io::Result<PathBuf> {
    source(path)?;
    let parent = path
        .parent()
        .ok_or_else(|| io::Error::other("Cannot delete the filesystem root"))?;
    let root = trash_root(parent, true)?;
    let timestamp = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_err(io::Error::other)?
        .as_nanos();
    let record = tempfile::Builder::new()
        .prefix(&format!("entry-{timestamp:039}-"))
        .tempdir_in(root)?;
    let mut manifest = fs::OpenOptions::new()
        .create_new(true)
        .write(true)
        .open(record.path().join("original"))?;
    let name = path
        .file_name()
        .ok_or_else(|| io::Error::other("No entry name"))?;
    manifest.write_all(&encode(Path::new(name))?)?;
    manifest.sync_all()?;
    // Persist BEFORE moving data so TempDir cleanup can never remove the item.
    let record = record.keep();
    let item = record.join("item");
    if let Err(err) = rename_exclusive(path, &item) {
        let _ = fs::remove_file(record.join("original"));
        let _ = fs::remove_dir(record);
        return Err(err);
    }
    Ok(item)
}
pub fn restore_latest(directory: &Path) -> io::Result<PathBuf> {
    let root = trash_root(directory, false).map_err(|err| {
        if err.kind() == io::ErrorKind::NotFound {
            io::Error::other("No deleted entries to restore in this directory")
        } else {
            err
        }
    })?;
    let mut records: Vec<_> = fs::read_dir(root)?.collect::<Result<_, _>>()?;
    records.sort_by_key(|e| std::cmp::Reverse(e.file_name()));
    for entry in records {
        if !entry.file_type()?.is_dir() {
            continue;
        }
        let record = entry.path();
        let item = record.join("item");
        let Ok(meta) = fs::symlink_metadata(&item) else {
            continue;
        };
        if !meta.is_file() && !meta.is_dir() {
            continue;
        }
        let manifest = record.join("original");
        if !fs::symlink_metadata(&manifest)?.is_file() {
            return Err(io::Error::other("Invalid recovery record"));
        }
        let name = decode(fs::read(manifest)?)?;
        if name.is_absolute()
            || name.components().count() != 1
            || name.file_name() != Some(name.as_os_str())
        {
            return Err(io::Error::other("Invalid recovery entry name"));
        }
        let original = directory.join(name);
        permitted(&original)?;
        rename_exclusive(&item, &original)?;
        let _ = fs::remove_file(record.join("original"));
        let _ = fs::remove_dir(record);
        return Ok(original);
    }
    Err(io::Error::other(
        "No deleted entries to restore in this directory",
    ))
}

#[cfg(test)]
mod tests {
    use super::*;
    fn root() -> (tempfile::TempDir, PathBuf) {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().canonicalize().unwrap();
        (dir, root)
    }
    #[test]
    fn create_move_collisions_and_reserved_paths() {
        let (_dir, root) = root();
        let file = destination(&root, "café.txt").unwrap();
        create(&file, false).unwrap();
        fs::write(&file, "keep").unwrap();
        assert!(create(&file, false).is_err());
        assert!(create(&file, true).is_err());
        let directory = destination(&root, "nested").unwrap();
        create(&directory, true).unwrap();
        assert!(create(&directory, true).is_err());
        let moved = destination(&root, "nested/moved").unwrap();
        move_entry(&file, &moved).unwrap();
        assert_eq!(fs::read_to_string(&moved).unwrap(), "keep");
        fs::write(&file, "existing").unwrap();
        assert!(move_entry(&moved, &file).is_err());
        assert_eq!(fs::read_to_string(&file).unwrap(), "existing");
        assert_eq!(fs::read_to_string(&moved).unwrap(), "keep");
        assert!(move_entry(&directory, &directory.join("child")).is_err());
        for name in [
            "",
            ".git",
            ".coigue-trash",
            "nested/.git",
            "missing/child",
            "bad\nname",
        ] {
            assert!(destination(&root, name).is_err(), "{name:?}");
        }
    }
    #[test]
    fn concurrent_moves_never_replace_each_other() {
        use std::sync::{Arc, Barrier};
        let (_dir, root) = root();
        let target = root.join("target");
        let barrier = Arc::new(Barrier::new(2));
        let mut workers = Vec::new();
        for name in ["first", "second"] {
            let source = root.join(name);
            fs::write(&source, name).unwrap();
            let target = target.clone();
            let barrier = barrier.clone();
            workers.push(std::thread::spawn(move || {
                barrier.wait();
                (source.clone(), move_entry(&source, &target).is_ok())
            }));
        }
        let results: Vec<_> = workers.into_iter().map(|w| w.join().unwrap()).collect();
        assert_eq!(results.iter().filter(|(_, success)| *success).count(), 1);
        for (source, success) in results {
            if success {
                assert_eq!(
                    fs::read_to_string(&target).unwrap(),
                    source.file_name().unwrap().to_str().unwrap()
                );
            } else {
                assert!(source.is_file());
            }
        }
    }
    #[test]
    fn recovery_persists_and_restores_directories_in_reverse_order() {
        let (_dir, root) = root();
        let first = root.join("first");
        let second = root.join("second");
        fs::write(&first, [0, 255, 17]).unwrap();
        fs::create_dir(&second).unwrap();
        fs::write(second.join("child"), "nested data").unwrap();
        let saved = trash(&first).unwrap();
        assert_eq!(fs::read(&saved).unwrap(), [0, 255, 17]);
        trash(&second).unwrap();
        assert!(!first.exists());
        assert!(!second.exists());
        // Recovery discovers on-disk records; there is no in-memory history.
        assert_eq!(restore_latest(&root).unwrap(), second);
        assert_eq!(
            fs::read_to_string(second.join("child")).unwrap(),
            "nested data"
        );
        assert_eq!(restore_latest(&root).unwrap(), first);
        assert_eq!(fs::read(first).unwrap(), [0, 255, 17]);
        assert!(restore_latest(&root).is_err());
    }
    #[test]
    fn recovery_survives_renaming_its_parent_and_rejects_forged_paths() {
        let (_dir, root) = root();
        let parent = root.join("parent");
        fs::create_dir(&parent).unwrap();
        let victim = parent.join("victim");
        fs::write(&victim, "recover").unwrap();
        trash(&victim).unwrap();
        let moved = root.join("moved");
        move_entry(&parent, &moved).unwrap();
        assert_eq!(restore_latest(&moved).unwrap(), moved.join("victim"));
        let item = trash(&moved.join("victim")).unwrap();
        fs::write(item.parent().unwrap().join("original"), "../escape").unwrap();
        assert!(restore_latest(&moved).is_err());
        assert!(item.is_file());
        assert!(!root.join("escape").exists());
    }
    #[test]
    fn restore_conflict_keeps_recoverable_data_and_symlinks_are_not_followed() {
        let (_dir, root) = root();
        let path = root.join("file");
        fs::write(&path, "deleted").unwrap();
        let saved = trash(&path).unwrap();
        fs::write(&path, "new occupant").unwrap();
        assert!(restore_latest(&root).is_err());
        assert_eq!(fs::read_to_string(&path).unwrap(), "new occupant");
        assert_eq!(fs::read_to_string(&saved).unwrap(), "deleted");
        fs::remove_file(&path).unwrap();
        assert_eq!(restore_latest(&root).unwrap(), path);
        #[cfg(unix)]
        {
            let link = root.join("link");
            std::os::unix::fs::symlink(&path, &link).unwrap();
            assert!(trash(&link).is_err());
            assert!(move_entry(&link, &root.join("moved")).is_err());
            fs::remove_dir(root.join(TRASH)).unwrap();
            std::os::unix::fs::symlink(&root, root.join(TRASH)).unwrap();
            assert!(trash(&path).is_err());
            assert!(path.is_file());
        }
    }
    #[cfg(unix)]
    #[test]
    fn manifest_round_trip_preserves_native_path_bytes() {
        use std::{ffi::OsString, os::unix::ffi::OsStringExt};
        let path = PathBuf::from(OsString::from_vec(vec![b'/', b'f', 0xff]));
        assert_eq!(decode(encode(&path).unwrap()).unwrap(), path);
    }
    #[cfg(target_os = "linux")]
    #[test]
    fn non_utf8_names_round_trip_through_persistent_recovery() {
        use std::{ffi::OsString, os::unix::ffi::OsStringExt};
        let (_dir, root) = root();
        let path = root.join(OsString::from_vec(vec![b'f', 0xff]));
        fs::write(&path, "data").unwrap();
        trash(&path).unwrap();
        assert_eq!(restore_latest(&root).unwrap(), path);
        assert_eq!(fs::read_to_string(path).unwrap(), "data");
    }
}
