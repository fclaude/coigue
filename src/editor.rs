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
    Line(Vec<Vec<char>>, bool),
    Chars(Vec<char>),
}
#[derive(Clone)]
pub enum FilesystemAction {
    CreateFile,
    CreateDirectory,
    Rename(std::path::PathBuf),
    Delete(std::path::PathBuf),
}
#[derive(Clone)]
pub struct FilesystemPrompt {
    pub action: FilesystemAction,
    pub input: String,
}
impl FilesystemPrompt {
    pub fn display(&self) -> String {
        match &self.action {
            FilesystemAction::CreateFile => format!("New file: {}", self.input),
            FilesystemAction::CreateDirectory => format!("New directory: {}", self.input),
            FilesystemAction::Rename(_) => format!("Rename/move (Ctrl-U clears): {}", self.input),
            FilesystemAction::Delete(path) => {
                format!("Trash? [y] yes [Esc/n] cancel | {}", path.display())
            }
        }
    }
}
pub struct Editor {
    pub theme: crate::theme::Theme,
    pub welcome: Option<std::path::PathBuf>,
    pub buffer: Buffer,
    pub file: File,
    pub mode: Mode,
    pub command: String,
    pub message: String,
    pending: Option<char>,
    count: Option<usize>,
    operator_count: Option<usize>,
    register: Option<Register>,
    pub search: String,
    last_search: String,
    pub picker: Option<crate::picker::Picker>,
    pub generation: u64,
    pub explorer: Option<crate::explorer::Explorer>,
    explorer_memory: crate::explorer::Memory,
    pub switch_prompt: Option<std::path::PathBuf>,
    pub filesystem_prompt: Option<FilesystemPrompt>,
}
impl Editor {
    pub fn new(file: File, text: &str) -> Self {
        Self {
            theme: crate::theme::Theme::default(),
            welcome: None,
            buffer: Buffer::new(text),
            file,
            mode: Mode::Normal,
            command: String::new(),
            message: String::new(),
            pending: None,
            count: None,
            operator_count: None,
            register: None,
            search: String::new(),
            last_search: String::new(),
            picker: None,
            generation: 0,
            explorer: None,
            explorer_memory: crate::explorer::Memory::default(),
            switch_prompt: None,
            filesystem_prompt: None,
        }
    }
    pub fn welcome(directory: std::path::PathBuf) -> Self {
        let mut editor = Self::new(File::unattached(), "");
        editor.welcome = Some(directory);
        editor
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
        if self.filesystem_prompt.is_some() {
            self.filesystem_key(key);
            return false;
        }
        if key.modifiers == KeyModifiers::CONTROL
            && key.code == K::Char('o')
            && self.picker.is_none()
            && matches!(self.mode, Mode::Normal | Mode::Insert)
        {
            if self.explorer.is_some() {
                self.close_explorer();
            } else {
                self.open_explorer();
            }
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
        if self.welcome.is_some() && self.mode == Mode::Normal {
            if key.modifiers.is_empty() {
                match key.code {
                    K::Char('-') => self.open_explorer(),
                    K::Char(':') => {
                        self.command.clear();
                        self.mode = Mode::Command;
                    }
                    _ => {}
                }
            }
            return false;
        }
        if self.mode == Mode::Normal
            && key.modifiers == KeyModifiers::CONTROL
            && key.code == K::Char('r')
        {
            let count = self.take_count().0;
            self.reset_pending();
            let mut changed = false;
            for _ in 0..count {
                if !self.buffer.redo() {
                    break;
                }
                changed = true;
            }
            self.message = if changed { "Redone" } else { "Nothing to redo" }.into();
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
            self.reset_pending();
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
        if self.mode == Mode::Normal {
            self.normal_key(key.code, page_rows);
            self.buffer.clamp(self.mode == Mode::Insert);
            return false;
        }
        let b = &mut self.buffer;
        match key.code {
            K::Char(c) => b.insert(c),
            K::Enter => b.insert('\n'),
            K::Tab => b.insert('\t'),
            K::Backspace => b.backspace(),
            _ => {}
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
            _ => {}
        }
        b.clamp(self.mode == Mode::Insert);
        false
    }
    pub fn pending_input(&self) -> String {
        let mut input = String::new();
        if let Some(count) = self.operator_count {
            input.push_str(&count.to_string());
        }
        if let Some(op) = self.pending {
            input.push(op);
        }
        if let Some(count) = self.count {
            input.push_str(&count.to_string());
        }
        input
    }
    fn reset_pending(&mut self) {
        self.pending = None;
        self.count = None;
        self.operator_count = None;
    }
    fn take_count(&mut self) -> (usize, bool) {
        let explicit = self.count.is_some() || self.operator_count.is_some();
        let count = self
            .count
            .take()
            .unwrap_or(1)
            .saturating_mul(self.operator_count.unwrap_or(1))
            .min(9999);
        self.operator_count = None;
        (count, explicit)
    }
    fn normal_key(&mut self, code: K, page_rows: usize) {
        // Replacement data must bypass digit parsing (e.g. 2r3).
        if self.pending == Some('r') {
            let count = self.take_count().0;
            self.reset_pending();
            let ch = match code {
                K::Char(c) => c,
                K::Tab => '\t',
                _ => return,
            };
            let b = &mut self.buffer;
            let start = b.col;
            for col in start..start.saturating_add(count).min(b.lines[b.row].len()) {
                b.col = col;
                b.replace(ch);
            }
            b.col = start;
            return;
        }
        if let K::Char(c @ '0'..='9') = code
            && (c != '0' || self.count.is_some())
        {
            self.count =
                Some((self.count.unwrap_or(0) * 10 + c.to_digit(10).unwrap() as usize).min(9999));
            return;
        }
        let pending = self.pending.take();
        if let K::Char(op @ ('d' | 'c' | 'y' | 'g' | 'r' | '>' | '<')) = code
            && pending != Some(op)
        {
            let (count, explicit) = self.take_count();
            self.operator_count = explicit.then_some(count);
            self.pending = Some(op);
            return;
        }
        let (count, explicit) = self.take_count();
        match (pending, code) {
            (Some(op @ ('d' | 'c' | 'y')), K::Char('w')) => {
                let chars = self.buffer.word_range(op != 'y', op == 'c', count);
                if !chars.is_empty() {
                    self.register = Some(Register::Chars(chars));
                }
                if op == 'c' {
                    self.mode = Mode::Insert;
                }
            }
            (Some(op @ ('d' | 'y')), K::Char(c)) if op == c => {
                let b = &mut self.buffer;
                let start = b.row;
                let end = start.saturating_add(count).min(b.lines.len());
                self.register = Some(Register::Line(
                    b.lines[start..end].to_vec(),
                    end < b.lines.len(),
                ));
                if op == 'd' {
                    // Remove at the original row, never clamp backwards and delete earlier lines.
                    for _ in start..end {
                        b.row = start;
                        b.delete_line();
                    }
                } else {
                    self.message = format!("{} line(s) copied", end - start);
                }
            }
            (Some(op @ ('>' | '<')), K::Char(c)) if op == c => {
                let b = &mut self.buffer;
                let start = b.row;
                let original_col = b.col;
                let mut first_col = original_col;
                for row in start..start.saturating_add(count).min(b.lines.len()) {
                    b.row = row;
                    b.col = original_col;
                    b.indent(op == '>');
                    if row == start {
                        first_col = b.col;
                    }
                }
                b.row = start;
                b.col = first_col;
            }
            (Some('g'), K::Char('g')) | (_, K::Char('G')) => {
                self.buffer.row = if explicit {
                    count - 1
                } else if code == K::Char('G') {
                    self.buffer.lines.len() - 1
                } else {
                    0
                };
                self.buffer.col = 0;
            }
            (_, K::Char('h') | K::Left) => self.buffer.col = self.buffer.col.saturating_sub(count),
            (_, K::Char('l') | K::Right) => self.buffer.col = self.buffer.col.saturating_add(count),
            (_, K::Char('k') | K::Up) => self.buffer.row = self.buffer.row.saturating_sub(count),
            (_, K::Char('j') | K::Down) => self.buffer.row = self.buffer.row.saturating_add(count),
            (_, K::PageUp) => {
                self.buffer.row = self
                    .buffer
                    .row
                    .saturating_sub(page_rows.max(1).saturating_mul(count))
            }
            (_, K::PageDown) => {
                self.buffer.row = self
                    .buffer
                    .row
                    .saturating_add(page_rows.max(1).saturating_mul(count))
            }
            (_, K::Home | K::Char('0')) => self.buffer.col = 0,
            (_, K::End | K::Char('$')) => {
                self.buffer.col = self.buffer.lines[self.buffer.row].len().saturating_sub(1)
            }
            (_, K::Char(motion @ ('w' | 'b' | 'e'))) => {
                for _ in 0..count {
                    let before = (self.buffer.row, self.buffer.col);
                    self.buffer.word_motion(motion);
                    if before == (self.buffer.row, self.buffer.col) {
                        break;
                    }
                }
            }
            (_, K::Char('x')) => {
                let b = &mut self.buffer;
                let end = b.col.saturating_add(count).min(b.lines[b.row].len());
                let chars = b.lines[b.row][b.col..end].to_vec();
                if !chars.is_empty() {
                    for _ in 0..chars.len() {
                        b.delete();
                    }
                    self.register = Some(Register::Chars(chars));
                }
            }
            (_, K::Char('p' | 'P')) => {
                if let Some(register) = &self.register {
                    match register {
                        Register::Line(lines, terminated) => {
                            let repeated: Vec<_> =
                                (0..count).flat_map(|_| lines.iter().cloned()).collect();
                            self.buffer
                                .paste_lines(&repeated, code == K::Char('p'), *terminated);
                        }
                        Register::Chars(chars) => self
                            .buffer
                            .paste_chars(&chars.repeat(count), code == K::Char('p')),
                    }
                } else {
                    self.message = "Nothing to paste".into();
                }
            }
            (_, K::Char('u')) => {
                let mut changed = false;
                for _ in 0..count {
                    if !self.buffer.undo() {
                        break;
                    }
                    changed = true;
                }
                self.message = if changed { "Undone" } else { "Nothing to undo" }.into();
            }
            (_, K::Char('n' | 'N')) => {
                for _ in 0..count {
                    self.find(code == K::Char('n'));
                }
            }
            (_, K::Char('A')) => {
                self.buffer.col = self.buffer.lines[self.buffer.row].len();
                self.mode = Mode::Insert;
            }
            (_, K::Char('i')) => self.mode = Mode::Insert,
            (_, K::Char('a')) => {
                self.buffer.col =
                    (self.buffer.col + 1).min(self.buffer.lines[self.buffer.row].len());
                self.mode = Mode::Insert;
            }
            (_, K::Char('o')) => {
                self.buffer.open_line();
                self.mode = Mode::Insert;
            }
            (_, K::Char('/')) => {
                self.search.clear();
                self.message.clear();
                self.mode = Mode::Search;
            }
            (_, K::Char(':')) => {
                self.command.clear();
                self.mode = Mode::Command;
            }
            (_, K::Char('-')) => self.open_explorer(),
            _ => {}
        }
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
    fn active_path(&self) -> std::io::Result<std::path::PathBuf> {
        let path = if self.file.path.is_absolute() {
            self.file.path.clone()
        } else {
            std::env::current_dir()?.join(&self.file.path)
        };
        let parent = path
            .parent()
            .ok_or_else(|| std::io::Error::other("No parent directory"))?
            .canonicalize()?;
        let name = path
            .file_name()
            .ok_or_else(|| std::io::Error::other("No file name"))?;
        Ok(parent.join(name))
    }
    fn open_explorer(&mut self) {
        self.message.clear();
        if let Some(directory) = &self.welcome {
            self.explorer = Some(crate::explorer::Explorer::new(
                directory.clone(),
                None,
                std::mem::take(&mut self.explorer_memory),
            ));
            self.reset_pending();
            return;
        }
        let path = match self.active_path() {
            Ok(path) => path,
            Err(err) => {
                self.message = err.to_string();
                return;
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
        self.reset_pending();
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
                K::Tab => explorer.accept_filter(),
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
            K::Char(c @ ('a' | 'A' | 'r' | 'd')) => self.start_filesystem_action(c),
            K::Char('u') => self.restore_deleted(),
            K::Char('h' | '-') | K::Left => explorer.parent(),
            K::Char('j') => explorer.move_by(1),
            K::Char('k') => explorer.move_by(-1),
            K::Char('l') | K::Right => self.explorer_navigation(K::Enter, page_rows),
            _ => self.explorer_navigation(key.code, page_rows),
        }
    }
    fn start_filesystem_action(&mut self, key: char) {
        let explorer = self.explorer.as_ref().unwrap();
        let (action, input) = match key {
            'a' => (FilesystemAction::CreateFile, String::new()),
            'A' => (FilesystemAction::CreateDirectory, String::new()),
            'r' | 'd' => {
                let Some(entry) = explorer.entry(explorer.selected) else {
                    return;
                };
                if entry.name == ".." {
                    self.message =
                        "Select a child entry; the parent entry cannot be changed".into();
                    return;
                }
                if key == 'r' {
                    (
                        FilesystemAction::Rename(entry.path.clone()),
                        entry.name.clone(),
                    )
                } else {
                    (FilesystemAction::Delete(entry.path.clone()), String::new())
                }
            }
            _ => return,
        };
        self.filesystem_prompt = Some(FilesystemPrompt { action, input });
    }
    fn filesystem_key(&mut self, key: KeyEvent) {
        if key.code == K::Esc {
            self.filesystem_prompt = None;
            self.message.clear();
            return;
        }
        let prompt = self.filesystem_prompt.as_mut().unwrap();
        if matches!(prompt.action, FilesystemAction::Delete(_)) {
            match key.code {
                K::Char('y') if key.modifiers.is_empty() => self.apply_filesystem_action(),
                K::Char('n') => {
                    self.filesystem_prompt = None;
                    self.message.clear();
                }
                _ => {}
            }
            return;
        }
        if key.modifiers == KeyModifiers::CONTROL && key.code == K::Char('u') {
            prompt.input.clear();
            return;
        }
        if key
            .modifiers
            .intersects(KeyModifiers::CONTROL | KeyModifiers::ALT | KeyModifiers::SUPER)
        {
            return;
        }
        match key.code {
            K::Char(c) => prompt.input.push(c),
            K::Backspace => {
                prompt.input.pop();
            }
            K::Enter => self.apply_filesystem_action(),
            _ => {}
        }
    }
    fn apply_filesystem_action(&mut self) {
        let prompt = self.filesystem_prompt.as_ref().unwrap().clone();
        let directory = self.explorer.as_ref().unwrap().directory.clone();
        let result = (|| -> std::io::Result<Option<std::path::PathBuf>> {
            match &prompt.action {
                FilesystemAction::CreateFile | FilesystemAction::CreateDirectory => {
                    let target = crate::fileops::destination(&directory, &prompt.input)?;
                    crate::fileops::create(
                        &target,
                        matches!(prompt.action, FilesystemAction::CreateDirectory),
                    )?;
                    Ok(Some(target))
                }
                FilesystemAction::Rename(source) => {
                    let target = crate::fileops::destination(&directory, &prompt.input)?;
                    let active = if self.welcome.is_none() {
                        Some(self.active_path()?)
                    } else {
                        None
                    };
                    crate::fileops::move_entry(source, &target)?;
                    if let Some(active) = active
                        && let Ok(suffix) = active.strip_prefix(source)
                    {
                        self.file.path = if suffix.as_os_str().is_empty() {
                            target.clone()
                        } else {
                            target.join(suffix)
                        };
                    }
                    Ok(Some(target))
                }
                FilesystemAction::Delete(source) => {
                    let active = if self.welcome.is_none() {
                        Some(self.active_path()?)
                    } else {
                        None
                    };
                    if active.is_some_and(|path| path.starts_with(source)) {
                        return Err(std::io::Error::other(
                            "Open another file before deleting the current file or its parent",
                        ));
                    }
                    crate::fileops::trash(source)?;
                    Ok(None)
                }
            }
        })();
        match result {
            Ok(focus) => {
                self.filesystem_prompt = None;
                self.explorer.as_mut().unwrap().refresh(focus.clone());
                self.message = focus.map_or_else(
                    || "Moved to recovery storage; u restores the latest deletion here".into(),
                    |path| format!("Done: {}", path.display()),
                );
            }
            Err(err) => self.message = format!("Operation failed: {err}"),
        }
    }
    fn restore_deleted(&mut self) {
        let directory = &self.explorer.as_ref().unwrap().directory;
        match crate::fileops::restore_latest(directory) {
            Ok(path) => {
                self.explorer.as_mut().unwrap().refresh(Some(path.clone()));
                self.message = format!("Restored: {}", path.display());
            }
            Err(err) => self.message = format!("Restore failed: {err}"),
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
                self.welcome = None;
                self.file = file;
                self.buffer = Buffer::new(&text);
                self.mode = Mode::Normal;
                self.command.clear();
                self.search.clear();
                self.reset_pending();
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
        self.reset_pending();
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
        let cmd = cmd.trim();
        let mut words = cmd.split_whitespace();
        if words.next() == Some("theme") {
            use crate::theme::Theme;
            match (words.next(), words.next()) {
                (None, _) => {
                    self.message =
                        format!("Theme: {} | :theme <{}>", self.theme.name(), Theme::CHOICES);
                }
                (Some(name), None) => {
                    if let Some(theme) = Theme::parse(name) {
                        self.theme = theme;
                        self.message = format!("Theme: {}", theme.name());
                    } else {
                        self.message = format!("Unknown theme: {name}; use :theme");
                    }
                }
                _ => self.message = format!("Usage: :theme <{}>", Theme::CHOICES),
            }
            return false;
        }
        if !cmd.is_empty() && cmd.bytes().all(|c| c.is_ascii_digit()) {
            if self.welcome.is_some() {
                self.message = "No file open".into();
            } else if let Ok(line) = cmd.parse::<usize>()
                && line > 0
            {
                self.buffer.row = (line - 1).min(self.buffer.lines.len() - 1);
                self.buffer.col = 0;
                self.message.clear();
            } else {
                self.message = "Invalid line number; use a positive whole number".into();
            }
            return false;
        }
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
    #[test]
    fn theme_commands_preserve_edits_history_and_selection_across_files() {
        use crate::theme::Theme;
        let (dir, mut e) = editing_fixture("first\nsecond");
        assert_eq!(e.theme, Theme::CatppuccinMocha);
        send(&mut e, "jA!");
        press(&mut e, K::Esc);
        let position = (e.buffer.row, e.buffer.col);
        let revision = e.buffer.revision();
        for (name, theme) in [
            ("gruvbox-dark", Theme::GruvboxDark),
            ("gruvbox", Theme::GruvboxDark),
            ("catppuccin-latte", Theme::CatppuccinLatte),
            ("latte", Theme::CatppuccinLatte),
            ("monochrome", Theme::Monochrome),
            ("terminal", Theme::Terminal),
            ("catppuccin-mocha", Theme::CatppuccinMocha),
            ("mocha", Theme::CatppuccinMocha),
        ] {
            send(&mut e, &format!(":theme {name}"));
            assert!(!e.key(KeyEvent::new(K::Enter, KeyModifiers::NONE), 22));
            assert_eq!(e.theme, theme);
            assert_eq!(e.message, format!("Theme: {}", theme.name()));
            assert_eq!(e.mode, Mode::Normal);
            assert_eq!((e.buffer.row, e.buffer.col), position);
            assert_eq!(e.buffer.revision(), revision);
            assert!(e.file.dirty(&e.buffer.text()));
        }
        send(&mut e, ":theme latte");
        press(&mut e, K::Esc);
        assert_eq!(e.theme, Theme::CatppuccinMocha);
        send(&mut e, "u");
        assert_eq!(e.buffer.text(), "first\nsecond");
        e.execute("theme latte");
        let second = dir.path().join("other");
        std::fs::write(&second, "other").unwrap();
        e.request_open(second);
        assert_eq!(e.theme, Theme::CatppuccinLatte);
        assert_eq!(
            std::fs::read_to_string(dir.path().join("fixture")).unwrap(),
            "first\nsecond"
        );
    }

    #[test]
    fn themes_work_on_welcome_reject_bad_arguments_and_reset_on_new_sessions() {
        use crate::theme::Theme;
        let dir = tempfile::tempdir().unwrap();
        let mut e = Editor::welcome(dir.path().into());
        send(&mut e, ":theme gruvbox");
        press(&mut e, K::Enter);
        assert_eq!(e.theme, Theme::GruvboxDark);
        e.execute("theme");
        for name in [
            "gruvbox-dark",
            "mocha",
            "gruvbox",
            "latte",
            "monochrome",
            "terminal",
        ] {
            assert!(e.message.contains(name));
        }
        for command in ["theme unknown", "theme latte extra", "themefoo latte"] {
            e.execute(command);
            assert_eq!(e.theme, Theme::GruvboxDark);
            assert!(e.message.starts_with("Unknown") || e.message.starts_with("Usage:"));
        }
        e.execute("  theme\tlatte  ");
        assert_eq!(e.theme, Theme::CatppuccinLatte);
        assert_eq!(e.buffer.text(), "");
        assert_eq!(std::fs::read_dir(dir.path()).unwrap().count(), 0);
        assert_eq!(
            Editor::welcome(dir.path().into()).theme,
            Theme::CatppuccinMocha
        );
    }

    fn send(e: &mut Editor, s: &str) {
        for c in s.chars() {
            e.key(KeyEvent::new(K::Char(c), KeyModifiers::NONE), 22);
        }
    }
    #[test]
    fn welcome_is_inert_and_browses_launch_directory_until_file_opens() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().join("space café 界");
        std::fs::create_dir(&root).unwrap();
        let path = root.join("hello.txt");
        std::fs::write(&path, "hello").unwrap();
        let mut e = Editor::welcome(root.clone());
        send(&mut e, "iword dwxpu");
        assert_eq!(e.buffer.text(), "");
        assert_eq!(e.mode, Mode::Normal);
        assert!(!e.execute("w"));
        assert!(e.message.contains("No file open"));
        assert_eq!(std::fs::read_dir(&root).unwrap().count(), 1);
        e.open_explorer();
        assert_eq!(e.explorer.as_ref().unwrap().directory, root);
        e.close_explorer();
        assert!(e.welcome.is_some());
        e.request_open(root.join("missing"));
        assert!(e.welcome.is_some());
        e.request_open(path.clone());
        assert!(e.welcome.is_none());
        assert!(e.switch_prompt.is_none());
        assert_eq!(e.buffer.text(), "hello");
        send(&mut e, "i!");
        e.key(KeyEvent::new(K::Esc, KeyModifiers::NONE), 22);
        assert!(!e.execute("q"));
        assert!(e.execute("wq"));
        assert_eq!(std::fs::read_to_string(path).unwrap(), "!hello");
    }
    #[test]
    fn welcome_commands_cancel_and_quit_without_a_file() {
        let dir = tempfile::tempdir().unwrap();
        let mut e = Editor::welcome(dir.path().into());
        send(&mut e, ":wq");
        assert!(!e.key(KeyEvent::new(K::Enter, KeyModifiers::NONE), 1));
        assert_eq!(e.mode, Mode::Normal);
        send(&mut e, ":q");
        e.key(KeyEvent::new(K::Esc, KeyModifiers::NONE), 1);
        assert_eq!(e.mode, Mode::Normal);
        assert_eq!(e.command, "");
        send(&mut e, ":q");
        assert!(e.key(KeyEvent::new(K::Enter, KeyModifiers::NONE), 1));
        assert_eq!(std::fs::read_dir(dir.path()).unwrap().count(), 0);
    }
    #[test]
    fn welcome_filesystem_actions_have_no_active_file_to_protect() {
        let dir = tempfile::tempdir().unwrap();
        let source = dir.path().join("old");
        let target = dir.path().join("new");
        std::fs::write(&source, "contents").unwrap();
        let mut e = Editor::welcome(dir.path().into());
        e.open_explorer();
        e.filesystem_prompt = Some(FilesystemPrompt {
            action: FilesystemAction::Rename(source.clone()),
            input: "new".into(),
        });
        e.apply_filesystem_action();
        assert!(!source.exists());
        assert_eq!(std::fs::read_to_string(&target).unwrap(), "contents");
        assert!(e.file.path.as_os_str().is_empty());
        e.filesystem_prompt = Some(FilesystemPrompt {
            action: FilesystemAction::Delete(target.clone()),
            input: String::new(),
        });
        e.apply_filesystem_action();
        assert!(!target.exists());
        e.restore_deleted();
        assert_eq!(std::fs::read_to_string(target).unwrap(), "contents");
        assert!(e.welcome.is_some());
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
    fn ctrl(e: &mut Editor, c: char) {
        e.key(KeyEvent::new(K::Char(c), KeyModifiers::CONTROL), 22);
    }
    fn explorer_select(e: &mut Editor, query: &str) {
        send(e, "/");
        send(e, query);
        press(e, K::Tab);
    }
    #[test]
    fn counts_move_jump_cancel_and_do_not_leak_between_modes() {
        let (_dir, mut e) = editing_fixture(&"abcdef\n".repeat(30));
        send(&mut e, "12j3l");
        assert_eq!((e.buffer.row, e.buffer.col), (12, 3));
        send(&mut e, "0");
        assert_eq!(e.buffer.col, 0);
        send(&mut e, "10gg");
        assert_eq!(e.buffer.row, 9);
        send(&mut e, "1G");
        assert_eq!(e.buffer.row, 0);
        send(&mut e, "999999999999999999999999999j");
        assert_eq!(e.buffer.row, 30);
        send(&mut e, "gg12d3");
        assert_eq!(e.pending_input(), "12d3");
        press(&mut e, K::Esc);
        send(&mut e, "j");
        assert_eq!(e.buffer.row, 1);
        assert!(e.pending_input().is_empty());
        send(&mut e, "20");
        ctrl(&mut e, 'p');
        press(&mut e, K::Esc);
        send(&mut e, "j");
        assert_eq!(e.buffer.row, 2);
        send(&mut e, "9i123");
        press(&mut e, K::Esc);
        assert!(e.buffer.text().contains("123abcdef"));
        send(&mut e, "/12");
        assert_eq!(e.search, "12");
        press(&mut e, K::Esc);
        send(&mut e, ":123");
        assert_eq!(e.command, "123");
        press(&mut e, K::Esc);
        send(&mut e, "4");
        press(&mut e, K::Down);
        assert_eq!(e.buffer.row, 6);
        send(&mut e, "2");
        ctrl(&mut e, 'f');
        assert_eq!(e.buffer.row, 30);
    }
    #[test]
    fn counted_line_registers_and_indentation_undo_as_one_edit() {
        let original = "a\nb\nc\nd\ne";
        let (_dir, mut e) = editing_fixture(original);
        send(&mut e, "2dd");
        assert_eq!(e.buffer.text(), "c\nd\ne");
        send(&mut e, "p");
        assert_eq!(e.buffer.text(), "c\na\nb\nd\ne");
        send(&mut e, "uu");
        assert_eq!(e.buffer.text(), original);
        send(&mut e, "2yyG2p");
        assert_eq!(e.buffer.text(), "a\nb\nc\nd\ne\na\nb\na\nb");
        send(&mut e, "u");
        assert_eq!(e.buffer.text(), original);
        send(&mut e, "gg4>>");
        assert_eq!(e.buffer.text(), "    a\n    b\n    c\n    d\ne");
        send(&mut e, "u");
        assert_eq!(e.buffer.text(), original);
        redo(&mut e);
        send(&mut e, "2<2<");
        assert_eq!(e.buffer.text(), original);
        send(&mut e, "G999dd");
        assert_eq!(e.buffer.text(), "a\nb\nc\nd");
        send(&mut e, "u");
        assert_eq!(e.buffer.text(), original);
        send(&mut e, "gg999ddP");
        assert_eq!(e.buffer.text(), original);
    }
    #[test]
    fn counted_words_characters_replacement_and_change_preserve_registers() {
        let original = "one two three four five six seven";
        let (_dir, mut e) = editing_fixture(original);
        send(&mut e, "2d3w");
        assert_eq!(e.buffer.text(), "seven");
        send(&mut e, "P");
        assert_eq!(e.buffer.text(), original);
        send(&mut e, "uu2w");
        assert_eq!(e.buffer.col, 8);
        send(&mut e, "2b2ywG$p");
        assert_eq!(e.buffer.text(), format!("{original}one two "));
        send(&mut e, "u0");
        send(&mut e, "2cwNEW");
        press(&mut e, K::Esc);
        assert_eq!(e.buffer.text(), "NEW three four five six seven");
        send(&mut e, "u0");
        assert_eq!(e.buffer.text(), original);
        send(&mut e, "3xP");
        assert_eq!(e.buffer.text(), original);
        send(&mut e, "uu2r3");
        assert_eq!(e.buffer.text(), "33e two three four five six seven");
        send(&mut e, "u0$999x");
        assert_eq!(e.buffer.text(), "one two three four five six seve");
        send(&mut e, "u");
        assert_eq!(e.buffer.text(), original);
        let (_dir, mut empty) = editing_fixture("");
        send(&mut empty, "999x999dw999p");
        assert_eq!(empty.buffer.text(), "");
        assert!(!empty.buffer.undo());
    }
    #[test]
    fn go_to_line_command_jumps_clamps_cancels_and_preserves_history() {
        let (_dir, mut e) = editing_fixture("first\n界 second\n\nlast\n");
        send(&mut e, "i!");
        press(&mut e, K::Esc);
        send(&mut e, ":2");
        assert_eq!(e.buffer.row, 0);
        press(&mut e, K::Enter);
        assert_eq!(e.mode, Mode::Normal);
        assert_eq!((e.buffer.row, e.buffer.col), (1, 0));
        send(&mut e, ":3");
        press(&mut e, K::Enter);
        assert_eq!((e.buffer.row, e.buffer.col), (2, 0));
        send(&mut e, ":999999");
        press(&mut e, K::Enter);
        assert_eq!((e.buffer.row, e.buffer.col), (4, 0));
        send(&mut e, ":1");
        press(&mut e, K::Esc);
        assert_eq!(e.buffer.row, 4);
        for invalid in ["0", "9999999999999999999999999999999999999999", "-1", "2x"] {
            assert!(!e.execute(invalid));
            assert_eq!((e.buffer.row, e.buffer.col), (4, 0));
            assert!(!e.message.is_empty());
        }
        e.execute("  0002  ");
        assert_eq!((e.buffer.row, e.buffer.col), (1, 0));
        assert!(e.message.is_empty());
        assert_eq!(e.buffer.text(), "!first\n界 second\n\nlast\n");
        send(&mut e, "u");
        assert_eq!(e.buffer.text(), "first\n界 second\n\nlast\n");
        redo(&mut e);
        assert_eq!(e.buffer.text(), "!first\n界 second\n\nlast\n");
        let (_dir, mut empty) = editing_fixture("");
        empty.execute("100");
        assert_eq!((empty.buffer.row, empty.buffer.col), (0, 0));
        let dir = tempfile::tempdir().unwrap();
        let mut welcome = Editor::welcome(dir.path().into());
        send(&mut welcome, ":2");
        press(&mut welcome, K::Enter);
        assert_eq!(welcome.message, "No file open");
        assert!(welcome.welcome.is_some());
    }
    #[test]
    fn counted_undo_redo_and_search_repeat_without_editing() {
        let (_dir, mut e) = editing_fixture("a a a a");
        send(&mut e, "xx2u");
        assert_eq!(e.buffer.text(), "a a a a");
        send(&mut e, "2");
        ctrl(&mut e, 'r');
        assert_eq!(e.buffer.text(), "a a a");
        send(&mut e, "2u/a");
        press(&mut e, K::Enter);
        assert_eq!(e.buffer.col, 2);
        send(&mut e, "2n");
        assert_eq!(e.buffer.col, 6);
        send(&mut e, "3N");
        assert_eq!(e.buffer.col, 0);
        assert_eq!(e.buffer.text(), "a a a a");
    }
    #[test]
    fn counted_unicode_ranges_roundtrip_crlf_and_multiple_lines() {
        let original = "café 界\r\n\nlast end";
        let (_dir, mut e) = editing_fixture(original);
        send(&mut e, "3dwP");
        assert_eq!(e.buffer.text(), original);
        send(&mut e, "uu2yyG2p");
        assert_eq!(
            e.buffer.text(),
            "café 界\r\n\nlast end\ncafé 界\r\n\ncafé 界\r\n"
        );
        send(&mut e, "u");
        assert_eq!(e.buffer.text(), original);
    }
    #[test]
    fn word_edits_register_history_and_modes() {
        let (_dir, mut e) = editing_fixture("café_2!! \t\r\n\n界 end");
        send(&mut e, "we");
        assert_eq!((e.buffer.row, e.buffer.col), (0, 7));
        send(&mut e, "wb");
        assert_eq!((e.buffer.row, e.buffer.col), (0, 6));
        send(&mut e, "ggyw");
        assert!(
            matches!(&e.register, Some(Register::Chars(c)) if c.iter().collect::<String>() == "café_2")
        );
        send(&mut e, "u");
        assert!(e.message.contains("Nothing to undo"));
        send(&mut e, "wwdw");
        assert_eq!(e.buffer.text(), "café_2!! \t\r\n\nend");
        assert!(
            matches!(&e.register, Some(Register::Chars(c)) if c.iter().collect::<String>() == "界 ")
        );
        send(&mut e, "u");
        send(&mut e, "ggwdw");
        assert!(
            matches!(&e.register, Some(Register::Chars(c)) if c.iter().collect::<String>() == "!! \t\r\n\n")
        );
        send(&mut e, "P");
        assert_eq!(e.buffer.text(), "café_2!! \t\r\n\n界 end");
        send(&mut e, "ggcwnew");
        press(&mut e, K::Esc);
        assert_eq!(e.buffer.text(), "new!! \t\r\n\n界 end");
        send(&mut e, "u");
        assert_eq!(e.buffer.text(), "café_2!! \t\r\n\n界 end");
        redo(&mut e);
        assert_eq!(e.buffer.text(), "new!! \t\r\n\n界 end");
        send(&mut e, "G$dw");
        assert_eq!(e.buffer.text(), "new!! \t\r\n\n界 en");
        send(&mut e, "icwbywe");
        press(&mut e, K::Esc);
        assert!(e.buffer.text().contains("cwbywe"));
        send(&mut e, ":dwcwbywe");
        assert_eq!(e.command, "dwcwbywe");
        press(&mut e, K::Esc);
        send(&mut e, "/dwcwbywe");
        assert_eq!(e.search, "dwcwbywe");
    }
    #[test]
    fn word_bindings_stay_out_of_explorer_and_prompts() {
        let (_dir, mut e) = editing_fixture("original words");
        ctrl(&mut e, 'o');
        ready_explorer(&mut e);
        send(&mut e, "wbecw");
        assert_eq!(e.buffer.text(), "original words");
        assert_eq!(e.mode, Mode::Normal);
        send(&mut e, "/dwcwbywe");
        assert_eq!(e.explorer.as_ref().unwrap().query, "dwcwbywe");
        press(&mut e, K::Esc);
        press(&mut e, K::Esc);
        e.filesystem_prompt = Some(FilesystemPrompt {
            action: FilesystemAction::CreateFile,
            input: String::new(),
        });
        send(&mut e, "dwcwbywe");
        assert_eq!(e.filesystem_prompt.as_ref().unwrap().input, "dwcwbywe");
        assert_eq!(e.buffer.text(), "original words");
        assert!(e.register.is_none());
        assert_eq!(e.buffer.revision(), 0);
    }
    #[test]
    fn word_empty_whitespace_and_change_spacing() {
        for text in ["", "\n", "  \n\n", "é", "!!"] {
            let (_dir, mut e) = editing_fixture(text);
            send(&mut e, "webwebggdw");
            send(&mut e, "u");
            assert_eq!(e.buffer.text(), text);
        }
        let (_dir, mut e) = editing_fixture("one  two");
        send(&mut e, "cwX");
        press(&mut e, K::Esc);
        assert_eq!(e.buffer.text(), "X  two");
        send(&mut e, "lcwY");
        press(&mut e, K::Esc);
        assert_eq!(e.buffer.text(), "XYtwo");
    }
    #[test]
    fn explorer_creates_renames_and_moves_open_file_without_losing_edits() {
        let (dir, mut e) = editing_fixture("abc");
        send(&mut e, "rx");
        ctrl(&mut e, 'o');
        ready_explorer(&mut e);
        send(&mut e, "aunused");
        press(&mut e, K::Esc);
        assert!(!dir.path().join("unused").exists());
        send(&mut e, "aother");
        press(&mut e, K::Enter);
        ready_explorer(&mut e);
        assert!(dir.path().join("other").is_file());
        send(&mut e, "Abucket");
        press(&mut e, K::Enter);
        ready_explorer(&mut e);
        assert!(dir.path().join("bucket").is_dir());
        explorer_select(&mut e, "fixture");
        send(&mut e, "r");
        ctrl(&mut e, 'u');
        send(&mut e, "bucket/renamed.rs");
        press(&mut e, K::Enter);
        ready_explorer(&mut e);
        assert!(e.file.path.ends_with("bucket/renamed.rs"));
        assert_eq!(e.buffer.text(), "xbc");
        assert!(e.file.dirty(&e.buffer.text()));
        assert!(!dir.path().join("fixture").exists());
        press(&mut e, K::Esc);
        send(&mut e, "u");
        assert_eq!(e.buffer.text(), "abc");
        redo(&mut e);
        e.execute("w");
        assert_eq!(
            std::fs::read_to_string(dir.path().join("bucket/renamed.rs")).unwrap(),
            "xbc"
        );
        ctrl(&mut e, 'o');
        ready_explorer(&mut e);
        send(&mut e, "h");
        ready_explorer(&mut e);
        // The parent containing the active buffer can be renamed as well.
        send(&mut e, "r");
        ctrl(&mut e, 'u');
        send(&mut e, "moved");
        press(&mut e, K::Enter);
        ready_explorer(&mut e);
        assert!(e.file.path.ends_with("moved/renamed.rs"));
        assert_eq!(e.buffer.text(), "xbc");
        assert_eq!(std::fs::read_to_string(&e.file.path).unwrap(), "xbc");
    }
    #[test]
    fn explorer_delete_confirmation_restore_conflict_and_active_file_protection() {
        let (dir, mut e) = editing_fixture("active");
        std::fs::write(dir.path().join("victim"), "recover me").unwrap();
        ctrl(&mut e, 'o');
        ready_explorer(&mut e);
        send(&mut e, "dy");
        assert!(e.message.contains("Open another file"));
        assert!(e.file.path.is_file());
        press(&mut e, K::Esc);
        explorer_select(&mut e, "victim");
        send(&mut e, "d");
        press(&mut e, K::Enter); // Enter never confirms deletion.
        assert!(dir.path().join("victim").is_file());
        press(&mut e, K::Esc);
        send(&mut e, "dyn");
        ready_explorer(&mut e);
        assert!(!dir.path().join("victim").exists());
        std::fs::write(dir.path().join("victim"), "occupied").unwrap();
        send(&mut e, "u");
        assert!(e.message.contains("Restore failed"));
        assert_eq!(
            std::fs::read_to_string(dir.path().join("victim")).unwrap(),
            "occupied"
        );
        std::fs::remove_file(dir.path().join("victim")).unwrap();
        send(&mut e, "u");
        ready_explorer(&mut e);
        assert_eq!(
            std::fs::read_to_string(dir.path().join("victim")).unwrap(),
            "recover me"
        );
        assert_eq!(e.buffer.text(), "active");
    }
    #[test]
    fn explorer_shortcut_keeps_ctrl_e_and_file_operations_cannot_overwrite() {
        let (dir, mut e) = editing_fixture("active");
        ctrl(&mut e, 'e');
        assert_eq!(e.buffer.col, 5);
        send(&mut e, "i");
        ctrl(&mut e, 'o');
        ready_explorer(&mut e);
        ctrl(&mut e, 'o');
        assert!(e.explorer.is_none());
        assert_eq!(e.mode, Mode::Insert);
        press(&mut e, K::Esc);
        ctrl(&mut e, 'o');
        ready_explorer(&mut e);
        send(&mut e, "afixture");
        press(&mut e, K::Enter);
        assert!(e.filesystem_prompt.is_some());
        assert!(e.message.contains("Operation failed"));
        press(&mut e, K::Esc);
        assert_eq!(
            std::fs::read_to_string(dir.path().join("fixture")).unwrap(),
            "active"
        );
        press(&mut e, K::Home);
        send(&mut e, "d");
        assert!(e.filesystem_prompt.is_none()); // ../ is not a mutable entry.
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
