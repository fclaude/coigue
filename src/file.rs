use std::{
    fs,
    io::{self, Write},
    path::{Path, PathBuf},
};
pub struct File {
    pub path: PathBuf,
    original: Option<Vec<u8>>,
    revision: u64,
}
fn read(path: &Path) -> io::Result<Option<Vec<u8>>> {
    match fs::read(path) {
        Ok(b) => Ok(Some(b)),
        Err(e) if e.kind() == io::ErrorKind::NotFound => Ok(None),
        Err(e) => Err(e),
    }
}
impl File {
    pub fn open(path: PathBuf) -> io::Result<(Self, String)> {
        // Resolve existing symlinks so an atomic save updates their target.
        let path = if path.exists() {
            fs::canonicalize(path)?
        } else {
            path
        };
        let original = read(&path)?;
        let text = String::from_utf8(original.clone().unwrap_or_default())
            .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "file is not UTF-8"))?;
        Ok((
            Self {
                path,
                original,
                revision: 0,
            },
            text,
        ))
    }
    pub fn revision(&self) -> u64 {
        self.revision
    }
    pub fn dirty(&self, text: &str) -> bool {
        self.original.as_deref().unwrap_or_default() != text.as_bytes()
    }
    pub fn save(&mut self, text: &str) -> io::Result<()> {
        self.save_with(text, |file, bytes| file.write_all(bytes))
    }
    fn save_with(
        &mut self,
        text: &str,
        write: impl FnOnce(&mut fs::File, &[u8]) -> io::Result<()>,
    ) -> io::Result<()> {
        if read(&self.path)? != self.original {
            return Err(io::Error::other("file changed externally; save refused"));
        }
        let parent = self
            .path
            .parent()
            .filter(|p| !p.as_os_str().is_empty())
            .unwrap_or(Path::new("."));
        let mut temp = tempfile::NamedTempFile::new_in(parent)?;
        let permissions = if self.original.is_some() {
            let meta = fs::symlink_metadata(&self.path)?;
            if !meta.is_file() || meta.permissions().readonly() {
                return Err(io::Error::other(
                    "destination is not a writable regular file",
                ));
            }
            Some(meta.permissions())
        } else {
            None
        };
        write(temp.as_file_mut(), text.as_bytes())?;
        // Set permissions after writing, since writes can clear special mode bits.
        if let Some(permissions) = permissions {
            temp.as_file().set_permissions(permissions)?;
        }
        temp.as_file().sync_all()?;
        if read(&self.path)? != self.original {
            return Err(io::Error::other("file changed externally; save refused"));
        }
        if self.original.is_none() {
            temp.persist_noclobber(&self.path).map_err(|e| e.error)?;
        } else {
            temp.persist(&self.path).map_err(|e| e.error)?;
        }
        self.original = Some(text.as_bytes().to_vec());
        self.revision += 1;
        Ok(())
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn roundtrip_and_conflict() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("file");
        let (mut f, s) = File::open(path.clone()).unwrap();
        assert_eq!(s, "");
        f.save("é\n\n").unwrap();
        assert!(!f.dirty("é\n\n"));
        assert_eq!(File::open(path.clone()).unwrap().1, "é\n\n");
        fs::write(&path, "external").unwrap();
        assert!(f.save("lost").is_err());
        assert_eq!(fs::read_to_string(path).unwrap(), "external");
    }
    #[test]
    fn failed_save_preserves_content() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("file");
        fs::write(&path, "old").unwrap();
        let (mut f, _) = File::open(path.clone()).unwrap();
        let error = f
            .save_with("new", |file, bytes| {
                file.write_all(&bytes[..1])?;
                Err(io::Error::other("injected partial write failure"))
            })
            .unwrap_err();
        assert!(error.to_string().contains("partial write"));
        assert_eq!(fs::read(&path).unwrap(), b"old");
        assert!(!f.dirty("old"));
        assert!(f.dirty("new"));
        assert_eq!(fs::read_dir(dir.path()).unwrap().count(), 1);
        f.save("new").unwrap();
        assert_eq!(fs::read(&path).unwrap(), b"new");
    }
    #[test]
    fn exact_roundtrips_and_invalid_utf8() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("file");
        for bytes in [b"".as_slice(), b"\n", b"a\n\n", b"a\r\nb\r\n", b"a"] {
            fs::write(&path, bytes).unwrap();
            let (mut f, text) = File::open(path.clone()).unwrap();
            let buffer = crate::buffer::Buffer::new(&text);
            f.save(&buffer.text()).unwrap();
            assert_eq!(fs::read(&path).unwrap(), bytes);
        }
        fs::write(&path, [255]).unwrap();
        assert!(File::open(path.clone()).is_err());
        assert_eq!(fs::read(path).unwrap(), [255]);
    }
    #[test]
    fn external_creation_and_deletion_are_conflicts() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("file");
        let (mut f, _) = File::open(path.clone()).unwrap();
        fs::write(&path, "external").unwrap();
        assert!(f.save("ours").is_err());
        assert_eq!(fs::read(&path).unwrap(), b"external");
        let (mut f, _) = File::open(path.clone()).unwrap();
        fs::remove_file(&path).unwrap();
        assert!(f.save("ours").is_err());
        assert!(!path.exists());
    }
    #[cfg(unix)]
    #[test]
    fn permissions_and_symlinks() {
        use std::os::unix::fs::{PermissionsExt, symlink};
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("target");
        let link = dir.path().join("link");
        fs::write(&path, "old").unwrap();
        fs::set_permissions(&path, fs::Permissions::from_mode(0o640)).unwrap();
        symlink(&path, &link).unwrap();
        let (mut f, _) = File::open(link.clone()).unwrap();
        f.save("new").unwrap();
        assert!(
            fs::symlink_metadata(&link)
                .unwrap()
                .file_type()
                .is_symlink()
        );
        assert_eq!(fs::read(&path).unwrap(), b"new");
        assert_eq!(
            fs::metadata(&path).unwrap().permissions().mode() & 0o777,
            0o640
        );
        fs::set_permissions(&path, fs::Permissions::from_mode(0o440)).unwrap();
        assert!(f.save("forbidden").is_err());
        assert_eq!(fs::read(&path).unwrap(), b"new");
        fs::remove_file(&path).unwrap();
        let other = dir.path().join("other");
        fs::write(&other, "new").unwrap();
        symlink(&other, &path).unwrap();
        assert!(f.save("forbidden").is_err());
        assert_eq!(fs::read(&other).unwrap(), b"new");
        let dangling = dir.path().join("dangling");
        symlink(dir.path().join("absent"), &dangling).unwrap();
        let (mut f, _) = File::open(dangling.clone()).unwrap();
        assert!(f.save("forbidden").is_err());
        assert!(
            fs::symlink_metadata(dangling)
                .unwrap()
                .file_type()
                .is_symlink()
        );
    }
}
