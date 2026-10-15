use crate::{buffer::Buffer, file::File};
use crossterm::event::{KeyCode as K, KeyEvent, KeyModifiers};
#[derive(Debug, PartialEq)]
pub enum Mode {
    Normal,
    Insert,
    Command,
}
pub struct Editor {
    pub buffer: Buffer,
    pub file: File,
    pub mode: Mode,
    pub command: String,
    pub message: String,
    pending: Option<char>,
    pub picker: Option<crate::picker::Picker>,
    pub generation: u64,
}
impl Editor {
    pub fn new(file: File, text: &str) -> Self {
        Self {
            buffer: Buffer::new(text),
            file,
            mode: Mode::Normal,
            command: String::new(),
            message: String::new(),
            pending: None,
            picker: None,
            generation: 0,
        }
    }
    pub fn key(&mut self, mut key: KeyEvent, page_rows: usize) -> bool {
        if self.picker.is_some() {
            self.picker_key(key, page_rows);
            return false;
        }
        if key.modifiers == KeyModifiers::CONTROL && key.code == K::Char('p') {
            self.open_picker();
            return false;
        }
        if key.modifiers == KeyModifiers::CONTROL {
            key.code = match key.code {
                K::Char('a') => K::Home,
                K::Char('e') => K::End,
                K::Char('f') => K::PageDown,
                K::Char('b') => K::PageUp,
                _ => key.code,
            };
            if matches!(key.code, K::Home | K::End | K::PageDown | K::PageUp) {
                key.modifiers = KeyModifiers::NONE;
            }
        }
        if key.code == K::Esc {
            if self.mode == Mode::Insert {
                self.buffer.col = self.buffer.col.saturating_sub(1);
            }
            self.mode = Mode::Normal;
            self.pending = None;
            self.command.clear();
            self.buffer.clamp(false);
            return false;
        }
        if key
            .modifiers
            .intersects(KeyModifiers::CONTROL | KeyModifiers::ALT | KeyModifiers::SUPER)
        {
            return false;
        }
        if self.mode == Mode::Command {
            match key.code {
                K::Char(c) => self.command.push(c),
                K::Backspace => {
                    self.command.pop();
                }
                K::Enter => {
                    let cmd = std::mem::take(&mut self.command);
                    self.mode = Mode::Normal;
                    return self.execute(&cmd);
                }
                _ => {}
            }
            return false;
        }
        let b = &mut self.buffer;
        if self.mode == Mode::Insert {
            match key.code {
                K::Char(c) => b.insert(c),
                K::Enter => b.insert('\n'),
                K::Tab => b.insert('\t'),
                K::Backspace => b.backspace(),
                _ => {}
            }
        } else {
            let pending = self.pending.take();
            match (pending, key.code) {
                (Some('d'), K::Char('d')) => b.delete_line(),
                (Some('g'), K::Char('g')) => {
                    b.row = 0;
                    b.col = 0;
                }
                (_, K::Char('d')) => self.pending = Some('d'),
                (_, K::Char('g')) => self.pending = Some('g'),
                (_, K::Char('i')) => self.mode = Mode::Insert,
                (_, K::Char('a')) => {
                    b.col = (b.col + 1).min(b.lines[b.row].len());
                    self.mode = Mode::Insert;
                }
                (_, K::Char('o')) => {
                    b.open_line();
                    self.mode = Mode::Insert;
                }
                (_, K::Char('x')) => b.delete(),
                (_, K::Char('0')) => b.col = 0,
                (_, K::Char('$')) => b.col = b.lines[b.row].len().saturating_sub(1),
                (_, K::Char('G')) => {
                    b.row = b.lines.len() - 1;
                    b.col = 0;
                }
                (_, K::Char(':')) => {
                    self.command.clear();
                    self.mode = Mode::Command;
                }
                _ => {}
            }
        }
        match key.code {
            K::Home => b.col = 0,
            K::End => b.col = b.lines[b.row].len(),
            K::PageUp => b.row = b.row.saturating_sub(page_rows.max(1)),
            K::PageDown => {
                b.row = b
                    .row
                    .saturating_add(page_rows.max(1))
                    .min(b.lines.len() - 1)
            }
            K::Left => b.col = b.col.saturating_sub(1),
            K::Right => b.col += 1,
            K::Up => b.row = b.row.saturating_sub(1),
            K::Down => b.row = (b.row + 1).min(b.lines.len() - 1),
            K::Char('h') if self.mode == Mode::Normal => b.col = b.col.saturating_sub(1),
            K::Char('l') if self.mode == Mode::Normal => b.col += 1,
            K::Char('k') if self.mode == Mode::Normal => b.row = b.row.saturating_sub(1),
            K::Char('j') if self.mode == Mode::Normal => b.row = (b.row + 1).min(b.lines.len() - 1),
            _ => {}
        }
        b.clamp(self.mode == Mode::Insert);
        false
    }
    fn open_picker(&mut self) {
        if self.file.dirty(&self.buffer.text()) {
            self.message = "Unsaved changes; use :w before opening another file".into();
            return;
        }
        match std::env::current_dir() {
            Ok(root) => self.picker = Some(crate::picker::Picker::new(root)),
            Err(err) => self.message = format!("Cannot open picker: {err}"),
        }
        self.pending = None;
    }
    fn picker_key(&mut self, key: KeyEvent, page_rows: usize) {
        if key.code == K::Esc {
            self.picker = None;
            return;
        }
        if key
            .modifiers
            .intersects(KeyModifiers::CONTROL | KeyModifiers::ALT | KeyModifiers::SUPER)
        {
            return;
        }
        let picker = self.picker.as_mut().unwrap();
        match key.code {
            K::Char(c) => {
                picker.query.push(c);
                picker.query_changed();
            }
            K::Backspace => {
                picker.query.pop();
                picker.query_changed();
            }
            K::Up => picker.move_by(-1),
            K::Down => picker.move_by(1),
            K::PageUp => picker.move_by(-(page_rows.max(1) as isize)),
            K::PageDown => picker.move_by(page_rows.max(1) as isize),
            K::Home => picker.selected = 0,
            K::End => picker.selected = picker.len().saturating_sub(1),
            K::Enter => {
                if let Some(path) = picker.path().map(std::path::Path::to_path_buf) {
                    // Recheck rather than allowing an overlay to discard a dirty buffer.
                    if self.file.dirty(&self.buffer.text()) {
                        self.message = "Unsaved changes; use :w before opening another file".into();
                        self.picker = None;
                    } else {
                        match File::open(path) {
                            Ok((file, text)) => {
                                self.file = file;
                                self.buffer = Buffer::new(&text);
                                self.mode = Mode::Normal;
                                self.command.clear();
                                self.message.clear();
                                self.generation += 1;
                                self.picker = None;
                            }
                            Err(err) => {
                                self.message = format!("Cannot open file: {err}");
                                self.picker = None;
                            }
                        }
                    }
                }
            }
            _ => {}
        }
    }
    fn execute(&mut self, cmd: &str) -> bool {
        let text = self.buffer.text();
        match cmd {
            "open" => self.open_picker(),
            "w" | "wq" => match self.file.save(&text) {
                Ok(()) => {
                    self.message = "Written".into();
                    return cmd == "wq";
                }
                Err(e) => self.message = e.to_string(),
            },
            "q" if self.file.dirty(&text) => self.message = "Unsaved changes; use :w or :q!".into(),
            "q" | "q!" => return true,
            _ => self.message = format!("Unknown command: {cmd}"),
        }
        false
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    fn send(e: &mut Editor, s: &str) {
        for c in s.chars() {
            e.key(KeyEvent::new(K::Char(c), KeyModifiers::NONE), 22);
        }
    }
    fn ready_picker(e: &mut Editor, root: &std::path::Path, query: &str) {
        let mut picker = crate::picker::Picker::new(root.into());
        let deadline = std::time::Instant::now() + std::time::Duration::from_secs(3);
        while picker.scanning {
            picker.poll();
            assert!(std::time::Instant::now() < deadline);
            std::thread::yield_now();
        }
        picker.query = query.into();
        picker.query_changed();
        e.picker = Some(picker);
    }
    #[test]
    fn picker_opens_files_and_preserves_buffer_on_cancel_or_failure() {
        let dir = tempfile::tempdir().unwrap();
        let old = dir.path().join("old");
        std::fs::write(&old, "old text").unwrap();
        std::fs::write(dir.path().join("new.rs"), "fn main() {}").unwrap();
        std::fs::write(dir.path().join("binary"), [0xff]).unwrap();
        let (f, text) = File::open(old.clone()).unwrap();
        let mut e = Editor::new(f, &text);
        e.buffer.col = 3;
        e.mode = Mode::Insert;
        ready_picker(&mut e, dir.path(), "newrs");
        e.key(KeyEvent::new(K::Esc, KeyModifiers::NONE), 22);
        assert_eq!(e.mode, Mode::Insert);
        assert_eq!(e.buffer.col, 3);
        assert_eq!(e.buffer.text(), "old text");
        ready_picker(&mut e, dir.path(), "binary");
        e.key(KeyEvent::new(K::Enter, KeyModifiers::NONE), 22);
        assert_eq!(e.buffer.text(), "old text");
        assert_eq!(e.file.path, old.canonicalize().unwrap());
        assert!(e.message.contains("not UTF-8"));
        ready_picker(&mut e, dir.path(), "newrs");
        e.key(KeyEvent::new(K::Enter, KeyModifiers::NONE), 22);
        assert_eq!(e.buffer.text(), "fn main() {}");
        assert_eq!(
            e.file.path,
            dir.path().join("new.rs").canonicalize().unwrap()
        );
        assert_eq!(e.mode, Mode::Normal);
        assert_eq!(e.generation, 1);
        assert!(e.picker.is_none());
        e.buffer.insert('x');
        e.key(KeyEvent::new(K::Char('p'), KeyModifiers::CONTROL), 22);
        assert!(e.picker.is_none());
        assert!(e.message.contains("Unsaved"));
        ready_picker(&mut e, dir.path(), "old");
        e.key(KeyEvent::new(K::Enter, KeyModifiers::NONE), 22);
        assert_eq!(e.buffer.text(), "xfn main() {}");
        assert!(e.file.path.ends_with("new.rs"));
    }
    #[test]
    fn picker_root_is_process_working_directory() {
        let dir = tempfile::tempdir().unwrap();
        let (file, text) = File::open(dir.path().join("outside-working-directory")).unwrap();
        let mut e = Editor::new(file, &text);
        e.key(KeyEvent::new(K::Char('p'), KeyModifiers::CONTROL), 22);
        assert_eq!(
            e.picker.as_ref().unwrap().root,
            std::env::current_dir().unwrap()
        );
        e.key(KeyEvent::new(K::Esc, KeyModifiers::NONE), 22);
        assert!(e.picker.is_none());
        e.execute("open");
        assert_eq!(
            e.picker.as_ref().unwrap().root,
            std::env::current_dir().unwrap()
        );
    }
    #[test]
    fn line_and_page_navigation_in_both_modes() {
        let dir = tempfile::tempdir().unwrap();
        let (f, _) = File::open(dir.path().join("new")).unwrap();
        let mut e = Editor::new(f, &"é界abc\n".repeat(60));
        for mode in [Mode::Normal, Mode::Insert] {
            e.mode = mode;
            let end = if e.mode == Mode::Insert { 5 } else { 4 };
            for (home, end_key, modifiers) in [
                (K::Home, K::End, KeyModifiers::NONE),
                (K::Char('a'), K::Char('e'), KeyModifiers::CONTROL),
            ] {
                e.key(KeyEvent::new(end_key, modifiers), 22);
                assert_eq!(e.buffer.col, end);
                e.key(KeyEvent::new(home, modifiers), 22);
                assert_eq!(e.buffer.col, 0);
            }
            for (down, up, modifiers) in [
                (K::PageDown, K::PageUp, KeyModifiers::NONE),
                (K::Char('f'), K::Char('b'), KeyModifiers::CONTROL),
            ] {
                e.buffer.row = 0;
                e.buffer.col = 2;
                e.key(KeyEvent::new(down, modifiers), 22);
                assert_eq!((e.buffer.row, e.buffer.col), (22, 2));
                e.key(KeyEvent::new(up, modifiers), 6);
                assert_eq!(e.buffer.row, 16);
                e.key(KeyEvent::new(up, modifiers), 22);
                assert_eq!(e.buffer.row, 0);
                e.key(KeyEvent::new(down, modifiers), usize::MAX);
                assert_eq!((e.buffer.row, e.buffer.col), (60, 0));
                e.key(KeyEvent::new(up, modifiers), 0);
                assert_eq!(e.buffer.row, 59);
                e.buffer.row = 0;
            }
        }
        let original = e.buffer.text();
        e.mode = Mode::Command;
        e.command = "w".into();
        for code in [K::Home, K::End, K::PageDown, K::PageUp] {
            e.key(KeyEvent::new(code, KeyModifiers::NONE), 22);
        }
        assert_eq!(e.command, "w");
        assert_eq!(e.buffer.text(), original);
        assert_eq!(e.buffer.row, 0);
    }
    #[test]
    fn modes_edits_and_quit() {
        let dir = tempfile::tempdir().unwrap();
        let (f, s) = File::open(dir.path().join("new")).unwrap();
        let mut e = Editor::new(f, &s);
        send(&mut e, "ié🦀");
        assert_eq!(e.mode, Mode::Insert);
        e.key(KeyEvent::new(K::Esc, KeyModifiers::NONE), 22);
        assert_eq!(e.buffer.col, 1);
        assert!(!e.execute("q"));
        assert!(e.message.contains("Unsaved"));
        send(&mut e, "0x");
        assert_eq!(e.buffer.text(), "🦀");
        send(&mut e, "ohello");
        e.key(KeyEvent::new(K::Esc, KeyModifiers::NONE), 22);
        send(&mut e, "ggdd");
        assert_eq!(e.buffer.text(), "hello");
        assert!(e.execute("wq"));
        assert!(!e.file.dirty("hello"));
    }
    #[test]
    fn command_and_failed_write_quit() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("new");
        let (f, s) = File::open(path.clone()).unwrap();
        let mut e = Editor::new(f, &s);
        send(&mut e, ":w");
        assert_eq!(e.mode, Mode::Command);
        e.key(KeyEvent::new(K::Enter, KeyModifiers::NONE), 22);
        assert!(path.exists());
        send(&mut e, "ia");
        std::fs::write(&path, "external").unwrap();
        assert!(!e.execute("wq"));
        assert!(e.execute("q!"));
    }
    #[test]
    fn movement_insert_newline_and_cancel() {
        let dir = tempfile::tempdir().unwrap();
        let (f, _) = File::open(dir.path().join("new")).unwrap();
        let mut e = Editor::new(f, "abc\n\né界");
        send(&mut e, "$j");
        assert_eq!((e.buffer.row, e.buffer.col), (1, 0));
        send(&mut e, "G$");
        assert_eq!((e.buffer.row, e.buffer.col), (2, 1));
        send(&mut e, "ggllh");
        assert_eq!((e.buffer.row, e.buffer.col), (0, 1));
        e.key(KeyEvent::new(K::Down, KeyModifiers::NONE), 22);
        assert_eq!((e.buffer.row, e.buffer.col), (1, 0));
        send(&mut e, "aé");
        e.key(KeyEvent::new(K::Enter, KeyModifiers::NONE), 22);
        send(&mut e, "🦀");
        e.key(KeyEvent::new(K::Backspace, KeyModifiers::NONE), 22);
        e.key(KeyEvent::new(K::Backspace, KeyModifiers::NONE), 22);
        assert_eq!(e.buffer.text(), "abc\né\né界");
        e.key(KeyEvent::new(K::Esc, KeyModifiers::NONE), 22);
        send(&mut e, ":q!");
        e.key(KeyEvent::new(K::Esc, KeyModifiers::NONE), 22);
        assert_eq!(e.mode, Mode::Normal);
        assert!(e.command.is_empty());
        assert!(!e.execute("q"));
        assert!(e.execute("q!"));
        assert!(!e.file.path.exists());
    }
}
