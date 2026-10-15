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
        }
    }
    pub fn key(&mut self, mut key: KeyEvent, page_rows: usize) -> bool {
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
    fn execute(&mut self, cmd: &str) -> bool {
        let text = self.buffer.text();
        match cmd {
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
