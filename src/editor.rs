use crate::{buffer::Buffer, file::File};
use crossterm::event::{KeyCode as K, KeyEvent, KeyModifiers};
#[derive(Debug, PartialEq)]
pub enum Mode {
    Normal,
    Insert,
    Command,
    Search,
}
#[derive(Clone)]
enum Register {
    Line(Vec<char>, bool),
    Chars(Vec<char>),
}
pub struct Editor {
    pub buffer: Buffer,
    pub file: File,
    pub mode: Mode,
    pub command: String,
    pub message: String,
    pending: Option<char>,
    register: Option<Register>,
    pub search: String,
    last_search: String,
    pub picker: Option<crate::picker::Picker>,
    pub generation: u64,
    pub explorer: Option<crate::explorer::Explorer>,
    explorer_memory: crate::explorer::Memory,
    pub switch_prompt: Option<std::path::PathBuf>,
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
            register: None,
            search: String::new(),
            last_search: String::new(),
            picker: None,
            generation: 0,
            explorer: None,
            explorer_memory: crate::explorer::Memory::default(),
            switch_prompt: None,
        }
    }
    pub fn key(&mut self, key: KeyEvent, page_rows: usize) -> bool {
        self.buffer.begin_edit();
        let quit = self.key_inner(key, page_rows);
        if self.mode != Mode::Insert || self.picker.is_some() || self.explorer.is_some() {
            self.buffer.finish_edit();
        }
        quit
    }
    fn key_inner(&mut self, mut key: KeyEvent, page_rows: usize) -> bool {
        if self.switch_prompt.is_some() {
            self.switch_key(key);
            return false;
        }
        if self.explorer.is_some() && self.picker.is_none() {
            self.explorer_key(key, page_rows);
            return false;
        }
        if self.picker.is_some() {
            self.picker_key(key, page_rows);
            return false;
        }
        if key.modifiers == KeyModifiers::CONTROL
            && key.code == K::Char('p')
            && matches!(self.mode, Mode::Normal | Mode::Insert)
        {
            self.open_picker();
            return false;
        }
        if self.mode == Mode::Normal
            && key.modifiers == KeyModifiers::CONTROL
            && key.code == K::Char('r')
        {
            self.pending = None;
            self.message = if self.buffer.redo() {
                "Redone"
            } else {
                "Nothing to redo"
            }
            .into();
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
            self.search.clear();
            self.buffer.clamp(false);
            return false;
        }
        if key
            .modifiers
            .intersects(KeyModifiers::CONTROL | KeyModifiers::ALT | KeyModifiers::SUPER)
        {
            return false;
        }
        if self.mode == Mode::Search {
            match key.code {
                K::Char(c) => self.search.push(c),
                K::Backspace => {
                    self.search.pop();
                }
                K::Enter => {
                    let query = std::mem::take(&mut self.search);
                    if !query.is_empty() {
                        self.last_search = query;
                    }
                    self.mode = Mode::Normal;
                    self.find(true);
                }
                _ => {}
            }
            return false;
        }
        // A replacement consumes its next key as data, even if it is normally
        // a movement or command (for example rh replaces with h).
        if self.mode == Mode::Normal && self.pending == Some('r') {
            self.pending = None;
            match key.code {
                K::Char(c) => self.buffer.replace(c),
                K::Tab => self.buffer.replace('\t'),
                _ => {}
            }
            return false;
        }
        if self.mode == Mode::Normal && key.code == K::Char('-') {
            self.open_explorer();
            return false;
        }
        if self.mode == Mode::Normal && key.code == K::Char('u') {
            self.pending = None;
            self.message = if self.buffer.undo() {
                "Undone"
            } else {
                "Nothing to undo"
            }
            .into();
            return false;
        }
        if self.mode == Mode::Normal && matches!(key.code, K::Char('n' | 'N')) {
            self.pending = None;
            self.find(key.code == K::Char('n'));
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
                (Some('d'), K::Char('d')) => {
                    self.register = Some(Register::Line(
                        b.lines[b.row].clone(),
                        b.row + 1 < b.lines.len(),
                    ));
                    b.delete_line();
                }
                (Some('y'), K::Char('y')) => {
                    self.register = Some(Register::Line(
                        b.lines[b.row].clone(),
                        b.row + 1 < b.lines.len(),
                    ));
                    self.message = "Line copied".into();
                }
                (Some('>'), K::Char('>')) => b.indent(true),
                (Some('<'), K::Char('<')) => b.indent(false),
                (Some('g'), K::Char('g')) => {
                    b.row = 0;
                    b.col = 0;
                }
                (_, K::Char('d')) => self.pending = Some('d'),
                (_, K::Char('g')) => self.pending = Some('g'),
                (_, K::Char(c @ ('y' | 'r' | '>' | '<'))) => self.pending = Some(c),
                (_, K::Char('p' | 'P')) => {
                    if let Some(register) = &self.register {
                        let after = key.code == K::Char('p');
                        match register {
                            Register::Line(line, terminated) => {
                                b.paste_line(line, after, *terminated)
                            }
                            Register::Chars(chars) => b.paste_chars(chars, after),
                        }
                    } else {
                        self.message = "Nothing to paste".into();
                    }
                }
                (_, K::Char('A')) => {
                    b.col = b.lines[b.row].len();
                    self.mode = Mode::Insert;
                }
                (_, K::Char('/')) => {
                    self.search.clear();
                    self.message.clear();
                    self.mode = Mode::Search;
                }
                (_, K::Char('i')) => self.mode = Mode::Insert,
                (_, K::Char('a')) => {
                    b.col = (b.col + 1).min(b.lines[b.row].len());
                    self.mode = Mode::Insert;
                }
                (_, K::Char('o')) => {
                    b.open_line();
                    self.mode = Mode::Insert;
                }
                (_, K::Char('x')) => {
                    if let Some(&ch) = b.lines[b.row].get(b.col) {
                        self.register = Some(Register::Chars(vec![ch]));
                        b.delete();
                    }
                }
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
    fn find(&mut self, forward: bool) {
        if self.last_search.is_empty() {
            self.message = "No previous search; use / to find text".into();
            return;
        }
        let query: Vec<_> = self.last_search.chars().collect();
        if let Some((row, col)) = self.buffer.find(&query, forward) {
            let wrapped = if forward {
                (row, col) <= (self.buffer.row, self.buffer.col)
            } else {
                (row, col) >= (self.buffer.row, self.buffer.col)
            };
            self.buffer.row = row;
            self.buffer.col = col;
            self.message = format!(
                "/{}{}",
                self.last_search,
                if wrapped { " (wrapped)" } else { "" }
            );
        } else {
            self.message = format!("Not found: {}", self.last_search);
        }
    }
    fn open_explorer(&mut self) {
        self.message.clear();
        let path = if self.file.path.is_absolute() {
            self.file.path.clone()
        } else {
            match std::env::current_dir() {
                Ok(cwd) => cwd.join(&self.file.path),
                Err(err) => {
                    self.message = err.to_string();
                    return;
                }
            }
        };
        let Some(parent) = path.parent() else {
            return;
        };
        self.explorer = Some(crate::explorer::Explorer::new(
            parent.into(),
            Some(path),
            std::mem::take(&mut self.explorer_memory),
        ));
        self.pending = None;
    }
    fn close_explorer(&mut self) {
        if let Some(mut explorer) = self.explorer.take() {
            explorer.remember();
            self.explorer_memory = std::mem::take(&mut explorer.memory);
        }
    }
    fn explorer_key(&mut self, key: KeyEvent, page_rows: usize) {
        self.message.clear();
        if key.modifiers == KeyModifiers::CONTROL && key.code == K::Char('p') {
            self.open_picker();
            return;
        }
        if key
            .modifiers
            .intersects(KeyModifiers::CONTROL | KeyModifiers::ALT | KeyModifiers::SUPER)
        {
            return;
        }
        let explorer = self.explorer.as_mut().unwrap();
        if explorer.filtering {
            match key.code {
                K::Esc => explorer.cancel_filter(),
                K::Backspace => {
                    explorer.query.pop();
                    explorer.filter();
                }
                K::Char(c) => {
                    explorer.query.push(c);
                    explorer.filter();
                }
                _ => self.explorer_navigation(key.code, page_rows),
            }
            return;
        }
        match key.code {
            K::Esc => self.close_explorer(),
            K::Char('/') => explorer.start_filter(),
            K::Char('.') => explorer.toggle_visibility(),
            K::Char('h' | '-') | K::Left => explorer.parent(),
            K::Char('j') => explorer.move_by(1),
            K::Char('k') => explorer.move_by(-1),
            K::Char('l') | K::Right => self.explorer_navigation(K::Enter, page_rows),
            _ => self.explorer_navigation(key.code, page_rows),
        }
    }
    fn explorer_navigation(&mut self, code: K, page_rows: usize) {
        let explorer = self.explorer.as_mut().unwrap();
        match code {
            K::Up => explorer.move_by(-1),
            K::Down => explorer.move_by(1),
            K::PageUp => explorer.move_by(-(page_rows as isize)),
            K::PageDown => explorer.move_by(page_rows as isize),
            K::Home => explorer.selected = 0,
            K::End => explorer.selected = explorer.len().saturating_sub(1),
            K::Enter => {
                if let Some(entry) = explorer.entry(explorer.selected) {
                    let path = entry.path.clone();
                    if entry.name == ".." {
                        explorer.parent();
                    } else if entry.directory {
                        explorer.navigate(path, None);
                    } else {
                        self.request_open(path);
                    }
                }
            }
            _ => {}
        }
    }
    fn request_open(&mut self, path: std::path::PathBuf) {
        self.message.clear();
        if self.file.dirty(&self.buffer.text()) {
            self.switch_prompt = Some(path);
        } else {
            self.open_path(path);
        }
    }
    fn open_path(&mut self, path: std::path::PathBuf) {
        // Validate before replacing the live buffer, even after choosing discard.
        let opened = std::fs::metadata(&path).and_then(|meta| {
            if !meta.is_file() {
                return Err(std::io::Error::other("not a regular file"));
            }
            File::open(path)
        });
        match opened {
            Ok((file, text)) => {
                self.file = file;
                self.buffer = Buffer::new(&text);
                self.mode = Mode::Normal;
                self.command.clear();
                self.search.clear();
                self.pending = None;
                self.message.clear();
                self.generation += 1;
                self.picker = None;
                self.close_explorer();
            }
            Err(err) => self.message = format!("Cannot open file: {err}"),
        }
    }
    fn switch_key(&mut self, key: KeyEvent) {
        if key
            .modifiers
            .intersects(KeyModifiers::CONTROL | KeyModifiers::ALT | KeyModifiers::SUPER)
        {
            return;
        }
        match key.code {
            K::Esc | K::Char('c') => {
                self.switch_prompt = None;
                self.message.clear();
            }
            K::Char('s' | 'd') => {
                if key.code == K::Char('s')
                    && let Err(err) = self.file.save(&self.buffer.text())
                {
                    self.message = format!("Save failed: {err}");
                    return;
                }
                let path = self.switch_prompt.take().unwrap();
                self.open_path(path);
            }
            _ => {}
        }
    }
    fn open_picker(&mut self) {
        self.message.clear();
        match std::env::current_dir() {
            Ok(root) => self.picker = Some(crate::picker::Picker::new(root)),
            Err(err) => self.message = format!("Cannot open picker: {err}"),
        }
        self.pending = None;
    }
    fn picker_key(&mut self, key: KeyEvent, page_rows: usize) {
        self.message.clear();
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
                    self.request_open(path);
                }
            }
            _ => {}
        }
    }
    fn execute(&mut self, cmd: &str) -> bool {
        let text = self.buffer.text();
        match cmd {
            "open" => self.open_picker(),
            "explore" => self.open_explorer(),
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
    fn editing_fixture(text: &str) -> (tempfile::TempDir, Editor) {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("fixture");
        std::fs::write(&path, text).unwrap();
        let (file, text) = File::open(path).unwrap();
        let editor = Editor::new(file, &text);
        (dir, editor)
    }
    fn press(e: &mut Editor, code: K) {
        e.key(KeyEvent::new(code, KeyModifiers::NONE), 22);
    }
    fn redo(e: &mut Editor) {
        e.key(KeyEvent::new(K::Char('r'), KeyModifiers::CONTROL), 22);
    }
    fn ready_explorer(e: &mut Editor) {
        let deadline = std::time::Instant::now() + std::time::Duration::from_secs(3);
        let view = e.explorer.as_mut().unwrap();
        while view.loading {
            view.poll();
            assert!(std::time::Instant::now() < deadline);
            std::thread::yield_now();
        }
        view.fit(4);
    }
    #[test]
    fn explorer_browses_dirty_buffer_and_cancel_preserves_cursor_history_and_cwd() {
        let (_dir, mut e) = editing_fixture("abc\nnext");
        let cwd = std::env::current_dir().unwrap();
        send(&mut e, "lrx-");
        ready_explorer(&mut e);
        assert_eq!(e.buffer.text(), "axc\nnext");
        assert_eq!(
            e.explorer
                .as_ref()
                .unwrap()
                .entry(e.explorer.as_ref().unwrap().selected)
                .unwrap()
                .path,
            e.file.path
        );
        press(&mut e, K::Enter);
        assert!(e.switch_prompt.is_some());
        send(&mut e, "c");
        assert!(e.explorer.is_some());
        send(&mut e, "/no-match");
        assert_eq!(e.explorer.as_ref().unwrap().len(), 0);
        press(&mut e, K::Esc);
        assert!(e.explorer.is_some());
        press(&mut e, K::Esc);
        assert!(e.explorer.is_none());
        assert_eq!((e.buffer.row, e.buffer.col), (0, 1));
        assert_eq!(std::env::current_dir().unwrap(), cwd);
        send(&mut e, "u");
        assert_eq!(e.buffer.text(), "abc\nnext");
        e.execute("explore");
        ready_explorer(&mut e);
        e.key(KeyEvent::new(K::Char('p'), KeyModifiers::CONTROL), 22);
        assert!(e.picker.is_some());
        assert_eq!(e.picker.as_ref().unwrap().root, cwd);
        press(&mut e, K::Esc);
        assert!(e.explorer.is_some());
        assert!(e.picker.is_none());
    }
    #[test]
    fn explorer_saves_or_discards_only_after_explicit_choice() {
        for choice in ['s', 'd'] {
            let (dir, mut e) = editing_fixture("old");
            let source = e.file.path.clone();
            let destination = dir.path().join("destination");
            std::fs::write(&destination, "new").unwrap();
            send(&mut e, "rx-");
            ready_explorer(&mut e);
            send(&mut e, "/destination");
            press(&mut e, K::Enter);
            assert_eq!(e.buffer.text(), "xld");
            assert!(e.switch_prompt.is_some());
            assert_eq!(std::fs::read_to_string(&source).unwrap(), "old");
            send(&mut e, &choice.to_string());
            assert_eq!(e.buffer.text(), "new");
            assert_eq!(
                std::fs::read_to_string(&source).unwrap(),
                if choice == 's' { "xld" } else { "old" }
            );
            assert!(e.explorer.is_none());
            assert!(e.switch_prompt.is_none());
            send(&mut e, "u");
            assert_eq!(e.buffer.text(), "new");
        }
    }
    #[test]
    fn failed_save_or_discard_open_keeps_original_edits_and_history() {
        let (dir, mut e) = editing_fixture("old");
        let destination = dir.path().join("binary");
        std::fs::write(&destination, [0xff]).unwrap();
        send(&mut e, "rx-");
        ready_explorer(&mut e);
        send(&mut e, "/binary");
        press(&mut e, K::Enter);
        std::fs::write(&e.file.path, "external").unwrap();
        send(&mut e, "s");
        assert!(e.switch_prompt.is_some());
        assert!(e.message.contains("Save failed"));
        assert_eq!(e.buffer.text(), "xld");
        send(&mut e, "d");
        assert!(e.message.contains("not UTF-8"));
        assert!(e.explorer.is_some());
        assert_eq!(e.buffer.text(), "xld");
        press(&mut e, K::Esc); // Leave filter.
        press(&mut e, K::Esc); // Return to file.
        send(&mut e, "u");
        assert_eq!(e.buffer.text(), "old");
        assert_eq!(std::fs::read_to_string(&e.file.path).unwrap(), "external");
    }
    #[test]
    fn undo_groups_insert_sessions_including_open_append_and_newlines() {
        let (_dir, mut e) = editing_fixture("é界\n");
        send(&mut e, "lAhello");
        press(&mut e, K::Enter);
        send(&mut e, "world");
        press(&mut e, K::Backspace);
        press(&mut e, K::Esc);
        assert_eq!(e.buffer.text(), "é界hello\nworl\n");
        let edited = e.buffer.text();
        send(&mut e, "u");
        assert_eq!(e.buffer.text(), "é界\n");
        assert_eq!((e.buffer.row, e.buffer.col), (0, 1));
        assert!(!e.file.dirty(&e.buffer.text()));
        redo(&mut e);
        assert_eq!(e.buffer.text(), edited);
        send(&mut e, "u");
        send(&mut e, "oopened");
        press(&mut e, K::Esc);
        assert_eq!(e.buffer.text(), "é界\nopened\n");
        send(&mut e, "u");
        assert_eq!(e.buffer.text(), "é界\n");
        assert_eq!((e.buffer.row, e.buffer.col), (0, 1));
        redo(&mut e);
        assert_eq!(e.buffer.text(), "é界\nopened\n");
    }
    #[test]
    fn undo_redo_each_normal_edit_and_register_remains_usable() {
        let (_dir, mut e) = editing_fixture("é🦀\nsecond\n");
        let mut states = vec![e.buffer.text()];
        for command in ["x", "rQ", ">>", "<<", "dd", "P", "p"] {
            send(&mut e, command);
            states.push(e.buffer.text());
        }
        for text in states[..states.len() - 1].iter().rev() {
            send(&mut e, "u");
            assert_eq!(&e.buffer.text(), text);
        }
        send(&mut e, "u");
        assert!(e.message.contains("Nothing to undo"));
        for text in &states[1..] {
            redo(&mut e);
            assert_eq!(&e.buffer.text(), text);
        }
        redo(&mut e);
        assert!(e.message.contains("Nothing to redo"));
        // Undo does not discard the cut line in the register.
        send(&mut e, "up");
        assert_eq!(e.buffer.text(), states.last().unwrap().as_str());
    }
    #[test]
    fn redo_survives_non_edits_but_is_cleared_by_a_new_edit() {
        let (_dir, mut e) = editing_fixture("abc");
        send(&mut e, "xu");
        send(&mut e, "yyj0/abc");
        press(&mut e, K::Enter);
        send(&mut e, "r");
        press(&mut e, K::Esc);
        send(&mut e, "ra<<"); // Replacement and unindent both leave text unchanged.
        send(&mut e, "iZ");
        press(&mut e, K::Backspace); // A net-zero insert session creates no history entry.
        press(&mut e, K::Esc);
        redo(&mut e);
        assert_eq!(e.buffer.text(), "bc");
        send(&mut e, "u");
        send(&mut e, "rQ");
        redo(&mut e);
        assert_eq!(e.buffer.text(), "Qbc");
        assert!(e.message.contains("Nothing to redo"));
        // The literal replacement key u must not trigger undo.
        send(&mut e, "ru");
        assert_eq!(e.buffer.text(), "ubc");
    }
    #[test]
    fn save_preserves_history_and_dirty_checks_follow_restored_contents() {
        let (_dir, mut e) = editing_fixture("original\n");
        send(&mut e, "A!");
        press(&mut e, K::Esc);
        assert!(!e.execute("w"));
        assert!(!e.file.dirty(&e.buffer.text()));
        send(&mut e, "u");
        assert_eq!(e.buffer.text(), "original\n");
        assert!(e.file.dirty(&e.buffer.text()));
        assert!(!e.execute("q"));
        assert_eq!(
            std::fs::read_to_string(&e.file.path).unwrap(),
            "original!\n"
        );
        redo(&mut e);
        assert!(!e.file.dirty(&e.buffer.text()));
        send(&mut e, "u");
        assert!(!e.execute("w"));
        redo(&mut e);
        assert!(e.file.dirty(&e.buffer.text()));
        send(&mut e, "u");
        assert!(e.execute("q"));
    }
    #[test]
    fn file_switch_resets_history_and_picker_cancel_keeps_it() {
        let (dir, mut e) = editing_fixture("old");
        std::fs::write(dir.path().join("second"), "new").unwrap();
        send(&mut e, "x");
        e.execute("w");
        ready_picker(&mut e, dir.path(), "second");
        press(&mut e, K::Esc);
        send(&mut e, "u");
        assert_eq!(e.buffer.text(), "old");
        redo(&mut e);
        ready_picker(&mut e, dir.path(), "second");
        press(&mut e, K::Enter);
        send(&mut e, "u");
        assert_eq!(e.buffer.text(), "new");
        assert!(e.message.contains("Nothing to undo"));
        redo(&mut e);
        assert!(e.message.contains("Nothing to redo"));
        send(&mut e, "ix");
        redo(&mut e); // Ctrl-R has no history action in insert mode.
        press(&mut e, K::Esc);
        send(&mut e, "u");
        assert_eq!(e.buffer.text(), "new");
    }
    #[test]
    fn copy_cut_and_repeat_paste_preserve_unicode_and_newlines() {
        let (_dir, mut e) = editing_fixture("é界\nbeta\n");
        send(&mut e, "yy");
        assert_eq!(e.buffer.revision(), 0);
        assert!(!e.file.dirty(&e.buffer.text()));
        send(&mut e, "p");
        assert_eq!(e.buffer.text(), "é界\né界\nbeta\n");
        assert_eq!((e.buffer.row, e.buffer.col), (1, 0));
        send(&mut e, "ddP");
        assert_eq!(e.buffer.text(), "é界\né界\nbeta\n");
        send(&mut e, "p");
        assert_eq!(e.buffer.text(), "é界\né界\né界\nbeta\n");
        send(&mut e, "0xP");
        assert_eq!(e.buffer.text(), "é界\né界\né界\nbeta\n");
        send(&mut e, "xp");
        assert_eq!(e.buffer.lines[e.buffer.row], vec!['界', 'é']);
        for text in ["only", "only\n", "é界\n"] {
            let (_dir, mut sole) = editing_fixture(text);
            send(&mut sole, "ddp");
            assert_eq!(sole.buffer.text(), text);
            assert!(!sole.file.dirty(&sole.buffer.text()));
        }
        let (_dir, mut empty) = editing_fixture("");
        send(&mut empty, "p");
        assert_eq!(empty.buffer.text(), "");
        assert!(empty.message.contains("Nothing to paste"));
    }
    #[test]
    fn replacement_consumes_command_characters_and_can_be_cancelled() {
        let (_dir, mut e) = editing_fixture("é🦀\n\nlast");
        send(&mut e, "rj");
        assert_eq!(e.buffer.text(), "j🦀\n\nlast");
        assert_eq!((e.buffer.row, e.buffer.col), (0, 0));
        send(&mut e, "lr/");
        assert_eq!(e.mode, Mode::Normal);
        assert_eq!(e.buffer.lines[0], vec!['j', '/']);
        send(&mut e, "r");
        press(&mut e, K::Esc);
        assert_eq!(e.buffer.lines[0], vec!['j', '/']);
        send(&mut e, "r界");
        assert_eq!(e.buffer.lines[0], vec!['j', '界']);
        send(&mut e, "r");
        press(&mut e, K::Tab);
        assert_eq!(e.buffer.lines[0], vec!['j', '\t']);
        send(&mut e, "jrX");
        assert!(e.buffer.lines[1].is_empty());
        assert_eq!(e.mode, Mode::Normal);
    }
    #[test]
    fn uppercase_append_starts_after_last_character_even_on_empty_lines() {
        let (_dir, mut e) = editing_fixture("abc\n\n界");
        send(&mut e, "lAZ");
        assert_eq!(e.buffer.lines[0], vec!['a', 'b', 'c', 'Z']);
        assert_eq!(e.mode, Mode::Insert);
        press(&mut e, K::Esc);
        send(&mut e, "jAempty");
        press(&mut e, K::Esc);
        send(&mut e, "GAé");
        press(&mut e, K::Esc);
        assert_eq!(e.buffer.text(), "abcZ\nempty\n界é");
        assert_eq!((e.buffer.row, e.buffer.col), (2, 1));
    }
    #[test]
    fn indent_unindent_keeps_cursor_on_text_and_handles_mixed_whitespace() {
        let (_dir, mut e) = editing_fixture("\t  é\n  x\nplain\n");
        e.buffer.col = 3;
        send(&mut e, ">>");
        assert_eq!(e.buffer.lines[0], "    \t  é".chars().collect::<Vec<_>>());
        assert_eq!(e.buffer.col, 7);
        send(&mut e, "<<");
        assert_eq!(e.buffer.col, 3);
        assert!(!e.file.dirty(&e.buffer.text()));
        send(&mut e, "<<");
        assert_eq!(e.buffer.col, 2);
        assert_eq!(e.buffer.lines[0], vec![' ', ' ', 'é']);
        send(&mut e, "j<<");
        assert_eq!(e.buffer.lines[1], vec!['x']);
        send(&mut e, "j<<");
        assert_eq!(e.buffer.lines[2], "plain".chars().collect::<Vec<_>>());
        send(&mut e, "G>><<");
        assert!(e.buffer.lines[3].is_empty());
        assert_eq!(e.buffer.col, 0);
    }
    #[test]
    fn search_repeats_wraps_cancels_and_leaves_text_unchanged() {
        let original = "é界 first\n界.*界\nlast 界";
        let (_dir, mut e) = editing_fixture(original);
        send(&mut e, "/界");
        assert_eq!(e.mode, Mode::Search);
        assert_eq!((e.buffer.row, e.buffer.col), (0, 0));
        press(&mut e, K::Enter);
        assert_eq!((e.buffer.row, e.buffer.col), (0, 1));
        send(&mut e, "n");
        assert_eq!((e.buffer.row, e.buffer.col), (1, 0));
        send(&mut e, "n");
        assert_eq!((e.buffer.row, e.buffer.col), (1, 3));
        send(&mut e, "N");
        assert_eq!((e.buffer.row, e.buffer.col), (1, 0));
        send(&mut e, "NN");
        assert_eq!((e.buffer.row, e.buffer.col), (2, 5));
        assert!(e.message.contains("wrapped"));
        send(&mut e, "n");
        assert_eq!((e.buffer.row, e.buffer.col), (0, 1));
        assert!(e.message.contains("wrapped"));
        send(&mut e, "/absent");
        press(&mut e, K::Esc);
        assert_eq!((e.buffer.row, e.buffer.col), (0, 1));
        send(&mut e, "n");
        assert_eq!((e.buffer.row, e.buffer.col), (1, 0));
        send(&mut e, "/.*");
        press(&mut e, K::Enter);
        assert_eq!((e.buffer.row, e.buffer.col), (1, 1));
        send(&mut e, "/absent");
        press(&mut e, K::Enter);
        assert_eq!((e.buffer.row, e.buffer.col), (1, 1));
        assert!(e.message.contains("Not found"));
        assert_eq!(e.buffer.text(), original);
        assert_eq!(e.buffer.revision(), 0);
        assert!(!e.file.dirty(original));
        let (_dir, mut empty) = editing_fixture("");
        send(&mut empty, "n");
        assert!(empty.message.contains("No previous search"));
        send(&mut empty, "/");
        press(&mut empty, K::Enter);
        assert!(empty.message.contains("No previous search"));
    }
    #[test]
    fn register_survives_switching_files() {
        let (dir, mut e) = editing_fixture("copied");
        std::fs::write(dir.path().join("second"), "destination").unwrap();
        send(&mut e, "yy");
        ready_picker(&mut e, dir.path(), "second");
        press(&mut e, K::Enter);
        send(&mut e, "p");
        assert_eq!(e.buffer.text(), "destination\ncopied");
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
        assert!(e.picker.is_some());
        ready_picker(&mut e, dir.path(), "old");
        e.key(KeyEvent::new(K::Enter, KeyModifiers::NONE), 22);
        assert_eq!(e.buffer.text(), "xfn main() {}");
        assert!(e.switch_prompt.is_some());
        press(&mut e, K::Esc);
        assert!(e.switch_prompt.is_none());
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
