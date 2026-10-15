use crate::editor::{Editor, Mode};
use crossterm::{
    cursor::{Hide, MoveTo, Show},
    queue,
    style::{Color, Print, SetForegroundColor},
    terminal::{self, BeginSynchronizedUpdate, Clear, ClearType, EndSynchronizedUpdate},
};
use std::io::{self, Write};
use unicode_width::UnicodeWidthChar;
#[derive(Default)]
pub struct View {
    top: usize,
    left: usize,
    size: (u16, u16),
    previous: Vec<Vec<u8>>,
    revision: Option<(u64, u64)>,
    styles: Option<Vec<Vec<crate::syntax::Style>>>,
    dirty: bool,
    generation: u64,
    syntax_path: Option<std::path::PathBuf>,
}
fn cell(c: char) -> (String, usize) {
    if c == '\t' {
        ("    ".into(), 4)
    } else if c.is_control() {
        ("�".into(), 1)
    } else {
        (c.to_string(), c.width().unwrap_or(0))
    }
}
fn clipped(text: impl Iterator<Item = char>, left: usize, width: usize) -> String {
    let mut col = 0;
    let mut out = String::new();
    for c in text {
        let (s, w) = cell(c);
        if col >= left && col + w <= left + width {
            out.push_str(&s);
        } else if col < left + width && col + w > left {
            out.push_str(&" ".repeat((col + w).min(left + width) - col.max(left)));
        }
        col += w;
    }
    out
}
fn color(style: crate::syntax::Style) -> Color {
    use crate::syntax::Style::*;
    match style {
        Plain => Color::Reset,
        Keyword => Color::Magenta,
        Comment => Color::DarkGrey,
        String => Color::Green,
        Number => Color::Cyan,
        Macro => Color::Yellow,
        Punctuation => Color::Reset,
        Lifetime => Color::Blue,
    }
}
// Apply exactly the same cell clipping as plain text; ANSI bytes never count as cells.
fn styled_line(
    out: &mut impl Write,
    line: &[char],
    styles: &[crate::syntax::Style],
    left: usize,
    width: usize,
) -> io::Result<()> {
    let mut col = 0;
    let mut previous = Color::Reset;
    for (c, style) in line.iter().zip(styles) {
        let (s, w) = cell(*c);
        let visible = if col >= left && col + w <= left + width {
            s
        } else if col < left + width && col + w > left {
            " ".repeat((col + w).min(left + width) - col.max(left))
        } else {
            String::new()
        };
        if !visible.is_empty() {
            let next = color(*style);
            if next != previous {
                queue!(out, SetForegroundColor(next))?;
                previous = next;
            }
            queue!(out, Print(visible))?;
        }
        col += w;
    }
    queue!(out, SetForegroundColor(Color::Reset))
}
fn switch_prompt(e: &Editor) -> String {
    format!(
        "Unsaved | [s] save/open  [d] discard/open  [c/Esc] cancel | {}",
        e.switch_prompt.as_ref().unwrap().display()
    )
}
impl View {
    pub fn page_rows(&self) -> usize {
        usize::from(self.size.1.saturating_sub(2)).max(1)
    }
    pub fn draw(&mut self, e: &Editor) -> io::Result<()> {
        let mut out = io::stdout().lock();
        self.draw_to(e, terminal::size()?, &mut out)
    }
    fn draw_picker(&mut self, e: &Editor, out: &mut impl Write) -> io::Result<()> {
        let picker = e.picker.as_ref().unwrap();
        let (_, h) = self.size;
        let rows = usize::from(h.saturating_sub(2));
        let top = picker.selected.saturating_sub(rows.saturating_sub(1));
        let mut lines = Vec::with_capacity(usize::from(h));
        lines.push(format!(
            "Files {}/{}{}{} | Enter open, Esc cancel | {}",
            picker.len(),
            picker.total(),
            if picker.scanning {
                " | scanning..."
            } else {
                ""
            },
            if picker.skipped > 0 {
                format!(" | {} unreadable entries skipped", picker.skipped)
            } else {
                String::new()
            },
            picker.root.display()
        ));
        for index in top..top + rows {
            lines.push(match picker.label(index) {
                Some(label) => format!(
                    "{} {}",
                    if index == picker.selected { ">" } else { " " },
                    label
                ),
                None if index == top => "No matches".into(),
                None => String::new(),
            });
        }
        if !e.message.is_empty() {
            lines[0] = e.message.clone();
        }
        let prompt = if e.switch_prompt.is_some() {
            switch_prompt(e)
        } else {
            format!("Open: {}", picker.query)
        };
        if h == 1 {
            lines.clear();
        } else {
            lines.truncate(usize::from(h - 1));
        }
        lines.push(prompt.clone());
        self.draw_browser_lines(lines, out)
    }
    fn draw_explorer(&mut self, e: &Editor, out: &mut impl Write) -> io::Result<()> {
        let explorer = e.explorer.as_ref().unwrap();
        let (_, h) = self.size;
        let rows = usize::from(h.saturating_sub(2));
        let top = explorer
            .top
            .min(explorer.selected)
            .max(explorer.selected.saturating_sub(rows.saturating_sub(1)))
            .min(explorer.len().saturating_sub(rows));
        let mut lines = vec![format!(
            "{} | {} entries{}{}",
            explorer.directory.display(),
            explorer.len(),
            if explorer.loading {
                " | loading..."
            } else {
                ""
            },
            if explorer.memory.show_all {
                " | show all"
            } else {
                ""
            }
        )];
        if explorer.filtering {
            lines[0] = format!(
                "Filter: Tab selects for actions, Esc cancels | {}",
                explorer.directory.display()
            );
        }
        if !e.message.is_empty() {
            lines[0] = e.message.clone();
        }
        for i in top..top + rows {
            lines.push(match explorer.entry(i) {
                Some(entry) => format!(
                    "{} {}{}",
                    if i == explorer.selected { ">" } else { " " },
                    entry.name,
                    if entry.directory { "/" } else { "" }
                ),
                None if i == top && !explorer.loading => "No matches".into(),
                None => String::new(),
            });
        }
        let prompt = if e.switch_prompt.is_some() {
            switch_prompt(e)
        } else if let Some(prompt) = &e.filesystem_prompt {
            prompt.display()
        } else if explorer.filtering {
            format!("/{}", explorer.query)
        } else if !explorer.message.is_empty() {
            explorer.message.clone()
        } else {
            "a file  A dir  r move  d trash  u restore  / filter  Esc return".into()
        };
        if h == 1 {
            lines.clear();
        } else {
            lines.truncate(usize::from(h - 1));
        }
        lines.push(prompt);
        self.draw_browser_lines(lines, out)
    }
    fn draw_browser_lines(&mut self, lines: Vec<String>, out: &mut impl Write) -> io::Result<()> {
        let (w, h) = self.size;
        let width = usize::from(w);
        let prompt = lines.last().unwrap();
        let mut frame = Vec::new();
        let mut update = Vec::new();
        queue!(
            update,
            BeginSynchronizedUpdate,
            Hide,
            SetForegroundColor(Color::Reset)
        )?;
        for (y, line) in lines.iter().enumerate() {
            // Keep the end of a long query visible while typing.
            let left = if y == usize::from(h - 1)
                && (line.starts_with('/')
                    || line.starts_with("Open:")
                    || line.starts_with("New ")
                    || line.starts_with("Rename/move"))
            {
                line.chars()
                    .map(|c| cell(c).1)
                    .sum::<usize>()
                    .saturating_add(1)
                    .saturating_sub(width)
            } else {
                0
            };
            let visible = clipped(line.chars(), left, width);
            let bytes = visible.as_bytes().to_vec();
            if self.previous.get(y) != Some(&bytes) {
                queue!(update, MoveTo(0, y as u16), Print(&visible))?;
                let cells: usize = visible.chars().map(|c| c.width().unwrap_or(0)).sum();
                if cells < width {
                    queue!(
                        update,
                        MoveTo(cells as u16, y as u16),
                        Clear(ClearType::UntilNewLine)
                    )?;
                }
            }
            frame.push(bytes);
        }
        let x = prompt
            .chars()
            .map(|c| cell(c).1)
            .sum::<usize>()
            .min(width - 1) as u16;
        queue!(update, MoveTo(x, h - 1), Show, EndSynchronizedUpdate)?;
        out.write_all(&update)?;
        out.flush()?;
        self.previous = frame;
        Ok(())
    }
    fn draw_to(&mut self, e: &Editor, size: (u16, u16), out: &mut impl Write) -> io::Result<()> {
        let (w, h) = size;
        if self.size != size {
            self.previous.clear();
            self.size = size;
        }
        if w == 0 || h == 0 {
            return Ok(());
        }
        if self.generation != e.generation {
            self.top = 0;
            self.left = 0;
            self.revision = None;
            self.generation = e.generation;
        }
        if e.picker.is_some() {
            return self.draw_picker(e, out);
        }
        if e.explorer.is_some() {
            return self.draw_explorer(e, out);
        }
        let rows = usize::from(h.saturating_sub(2));
        let width = usize::from(w);
        let b = &e.buffer;
        if b.row < self.top {
            self.top = b.row;
        } else if b.row >= self.top + rows.max(1) {
            self.top = b.row + 1 - rows.max(1);
        }
        let col: usize = b.lines[b.row].iter().take(b.col).map(|c| cell(*c).1).sum();
        if col < self.left {
            self.left = col;
        } else if col >= self.left + width {
            self.left = col + 1 - width;
        }
        let revision = (b.revision(), e.file.revision());
        let path_changed = self.syntax_path.as_ref() != Some(&e.file.path);
        if self.revision != Some(revision) || path_changed {
            if self.revision.map(|r| r.0) != Some(revision.0) || path_changed {
                self.syntax_path = Some(e.file.path.clone());
                self.styles = (e.file.path.extension().is_some_and(|ext| ext == "rs"))
                    .then(|| crate::syntax::rust(&b.lines));
            }
            self.dirty = e.file.dirty(&b.text());
            self.revision = Some(revision);
        }
        let mut frame = vec![Vec::new(); usize::from(h)];
        for (y, line_out) in frame.iter_mut().take(rows).enumerate() {
            if let Some(line) = b.lines.get(self.top + y) {
                if let Some(styles) = &self.styles {
                    styled_line(line_out, line, &styles[self.top + y], self.left, width)?;
                } else {
                    queue!(
                        line_out,
                        Print(clipped(line.iter().copied(), self.left, width))
                    )?;
                }
            } else {
                queue!(line_out, Print("~"))?;
            }
        }

        let status = format!(
            "{:?} {}{}  {}:{}",
            e.mode,
            e.file.path.display(),
            if self.dirty { " [+]" } else { "" },
            b.row + 1,
            b.col + 1
        );
        if h >= 2 {
            queue!(
                frame[usize::from(h - 2)],
                Print(clipped(status.chars(), 0, width))
            )?;
        }
        let message = if e.mode == Mode::Command {
            format!(":{}", e.command)
        } else if e.mode == Mode::Search {
            format!("/{}", e.search)
        } else {
            e.message.clone()
        };
        let prompt_mode = matches!(e.mode, Mode::Command | Mode::Search);
        let prompt_width: usize = message.chars().map(|c| cell(c).1).sum();
        let prompt_left = if prompt_mode {
            prompt_width.saturating_add(1).saturating_sub(width)
        } else {
            0
        };
        queue!(
            frame[usize::from(h - 1)],
            Print(clipped(message.chars(), prompt_left, width))
        )?;
        let (x, y) = if prompt_mode {
            (
                prompt_width.saturating_sub(prompt_left).min(width - 1) as u16,
                h - 1,
            )
        } else {
            (
                (col - self.left).min(width - 1) as u16,
                (b.row - self.top).min(rows.saturating_sub(1)) as u16,
            )
        };
        // Assemble the entire update before touching the terminal. Unchanged rows
        // stay in place; synchronized terminals display changed rows atomically.
        let mut update = Vec::new();
        queue!(
            update,
            BeginSynchronizedUpdate,
            Hide,
            SetForegroundColor(Color::Reset)
        )?;
        for (row, line) in frame.iter().enumerate() {
            if self.previous.get(row) != Some(line) {
                queue!(update, MoveTo(0, row as u16))?;
                update.extend_from_slice(line);
                // Reset a possible pending wrap before clearing the unused tail.
                // A full-width line needs no clearing.
                let cells = if row < rows {
                    b.lines.get(self.top + row).map_or(1, |line| {
                        clipped(line.iter().copied(), self.left, width)
                            .chars()
                            .map(|c| c.width().unwrap_or(0))
                            .sum()
                    })
                } else if h >= 2 && row == usize::from(h - 2) {
                    clipped(status.chars(), 0, width)
                        .chars()
                        .map(|c| c.width().unwrap_or(0))
                        .sum()
                } else {
                    clipped(message.chars(), prompt_left, width)
                        .chars()
                        .map(|c| c.width().unwrap_or(0))
                        .sum()
                };
                if cells < width {
                    queue!(
                        update,
                        MoveTo(cells as u16, row as u16),
                        Clear(ClearType::UntilNewLine)
                    )?;
                }
            }
        }
        queue!(update, MoveTo(x, y), Show, EndSynchronizedUpdate)?;
        out.write_all(&update)?;
        out.flush()?;
        self.previous = frame;
        Ok(())
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn explorer_rename_updates_syntax_without_resetting_cursor_or_viewport() {
        use crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("plain");
        std::fs::write(&path, "fn main() {}\n".repeat(12)).unwrap();
        let (file, text) = crate::file::File::open(path).unwrap();
        let mut e = Editor::new(file, &text);
        e.buffer.row = 7;
        let mut view = View::default();
        view.draw_to(&e, (20, 4), &mut Vec::new()).unwrap();
        let top = view.top;
        assert!(view.styles.is_none());
        e.key(KeyEvent::new(KeyCode::Char('o'), KeyModifiers::CONTROL), 2);
        let explorer = e.explorer.as_mut().unwrap();
        let deadline = std::time::Instant::now() + std::time::Duration::from_secs(3);
        while explorer.loading {
            explorer.poll();
            assert!(std::time::Instant::now() < deadline);
            std::thread::yield_now();
        }
        e.key(KeyEvent::new(KeyCode::Char('r'), KeyModifiers::NONE), 2);
        for size in [(1, 1), (0, 0), (20, 4)] {
            view.draw_to(&e, size, &mut Vec::new()).unwrap();
        }
        e.key(KeyEvent::new(KeyCode::Char('u'), KeyModifiers::CONTROL), 2);
        for c in "code.rs".chars() {
            e.key(KeyEvent::new(KeyCode::Char(c), KeyModifiers::NONE), 2);
        }
        e.key(KeyEvent::new(KeyCode::Enter, KeyModifiers::NONE), 2);
        e.key(KeyEvent::new(KeyCode::Esc, KeyModifiers::NONE), 2);
        view.draw_to(&e, (20, 4), &mut Vec::new()).unwrap();
        assert_eq!(e.buffer.row, 7);
        assert_eq!(view.top, top);
        assert_eq!(
            view.styles.as_ref().unwrap()[0][0],
            crate::syntax::Style::Keyword
        );
    }
    #[test]
    fn explorer_resize_confirmation_and_cancel_preserve_file_viewport() {
        use crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("current");
        let text = "long line with characters to scroll\n".repeat(12);
        std::fs::write(&path, &text).unwrap();
        let (file, text) = crate::file::File::open(path).unwrap();
        let mut e = Editor::new(file, &text);
        e.buffer.row = 5;
        e.buffer.col = 20;
        let mut view = View::default();
        view.draw_to(&e, (10, 4), &mut Vec::new()).unwrap();
        let position = (view.top, view.left);
        for c in ['r', 'Z', '-'] {
            e.key(KeyEvent::new(KeyCode::Char(c), KeyModifiers::NONE), 2);
        }
        let explorer = e.explorer.as_mut().unwrap();
        let deadline = std::time::Instant::now() + std::time::Duration::from_secs(3);
        while explorer.loading {
            explorer.poll();
            assert!(std::time::Instant::now() < deadline);
            std::thread::yield_now();
        }
        for size in [(0, 0), (1, 1), (16, 2), (80, 6)] {
            let mut out = Vec::new();
            view.draw_to(&e, size, &mut out).unwrap();
            assert!(!String::from_utf8_lossy(&out).contains(&Clear(ClearType::All).to_string()));
        }
        e.key(KeyEvent::new(KeyCode::Enter, KeyModifiers::NONE), 2);
        let mut out = Vec::new();
        view.draw_to(&e, (80, 6), &mut out).unwrap();
        assert!(String::from_utf8_lossy(&out).contains("[s] save/open"));
        e.key(KeyEvent::new(KeyCode::Char('c'), KeyModifiers::NONE), 2);
        e.key(KeyEvent::new(KeyCode::Esc, KeyModifiers::NONE), 2);
        view.draw_to(&e, (10, 4), &mut Vec::new()).unwrap();
        assert_eq!((view.top, view.left), position);
        assert_eq!((e.buffer.row, e.buffer.col), (5, 20));
        assert!(view.dirty);
    }
    #[test]
    fn undo_redo_invalidates_multiline_highlighting_and_dirty_status() {
        use crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("history.rs");
        std::fs::write(&path, "/*\nfn main() {}").unwrap();
        let (file, text) = crate::file::File::open(path).unwrap();
        let mut e = Editor::new(file, &text);
        let mut view = View::default();
        view.draw_to(&e, (80, 4), &mut Vec::new()).unwrap();
        assert!(!view.dirty);
        e.key(KeyEvent::new(KeyCode::Char('x'), KeyModifiers::NONE), 2);
        view.draw_to(&e, (80, 4), &mut Vec::new()).unwrap();
        assert_eq!(
            view.styles.as_ref().unwrap()[1][0],
            crate::syntax::Style::Keyword
        );
        assert!(view.dirty);
        e.key(KeyEvent::new(KeyCode::Char('u'), KeyModifiers::NONE), 2);
        view.draw_to(&e, (80, 4), &mut Vec::new()).unwrap();
        assert_eq!(
            view.styles.as_ref().unwrap()[1][0],
            crate::syntax::Style::Comment
        );
        assert!(!view.dirty);
        e.key(KeyEvent::new(KeyCode::Char('r'), KeyModifiers::CONTROL), 2);
        view.draw_to(&e, (80, 4), &mut Vec::new()).unwrap();
        assert_eq!(
            view.styles.as_ref().unwrap()[1][0],
            crate::syntax::Style::Keyword
        );
        assert!(view.dirty);
    }
    #[test]
    fn long_unicode_search_prompt_scrolls_and_cancel_restores_message_row() {
        use crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
        let dir = tempfile::tempdir().unwrap();
        let (file, _) = crate::file::File::open(dir.path().join("fixture")).unwrap();
        let mut e = Editor::new(file, "text");
        e.mode = Mode::Search;
        e.search = "界éabcdefghijkl".into();
        let mut view = View::default();
        let mut out = Vec::new();
        view.draw_to(&e, (6, 4), &mut out).unwrap();
        let rendered = String::from_utf8_lossy(&out);
        assert!(rendered.contains("hijkl"));
        assert!(rendered.contains(&MoveTo(5, 3).to_string()));
        e.key(KeyEvent::new(KeyCode::Esc, KeyModifiers::NONE), 2);
        out.clear();
        view.draw_to(&e, (6, 4), &mut out).unwrap();
        assert!(
            String::from_utf8_lossy(&out).contains(&Clear(ClearType::UntilNewLine).to_string())
        );
        assert_eq!(e.mode, Mode::Normal);
        assert_eq!(e.buffer.text(), "text");
    }
    #[test]
    fn picker_cancel_and_file_switch_reset_rendering_correctly() {
        let dir = tempfile::tempdir().unwrap();
        let (file, _) = crate::file::File::open(dir.path().join("old.rs")).unwrap();
        let mut e = Editor::new(file, "/*\ncomment\nstill comment");
        e.buffer.row = 2;
        let mut view = View::default();
        view.draw_to(&e, (80, 4), &mut Vec::new()).unwrap();
        assert_eq!(view.top, 1);
        e.picker = Some(crate::picker::Picker::new(dir.path().into()));
        e.picker.as_mut().unwrap().query = "absent".into();
        for size in [(80, 4), (1, 1), (16, 2), (0, 0), (80, 4)] {
            view.draw_to(&e, size, &mut Vec::new()).unwrap();
        }
        let mut out = Vec::new();
        e.picker = None;
        view.draw_to(&e, (80, 4), &mut out).unwrap();
        assert_eq!(view.top, 1);
        assert!(String::from_utf8_lossy(&out).contains("still comment"));
        e.generation += 1;
        e.buffer = crate::buffer::Buffer::new("fn new() {}");
        out.clear();
        view.draw_to(&e, (80, 4), &mut out).unwrap();
        assert_eq!(view.top, 0);
        assert_eq!(
            view.styles.as_ref().unwrap()[0][0],
            crate::syntax::Style::Keyword
        );
        assert!(String::from_utf8_lossy(&out).contains("new()"));
    }
    #[test]
    fn redraws_only_changed_rows_and_clears_stale_tails() {
        let dir = tempfile::tempdir().unwrap();
        let (file, _) = crate::file::File::open(dir.path().join("new")).unwrap();
        let mut e = Editor::new(file, "abcdef\nsecond\nthird");
        let mut view = View::default();
        let mut out = Vec::new();
        view.draw_to(&e, (80, 4), &mut out).unwrap();
        assert_eq!(view.page_rows(), 2);
        assert!(!String::from_utf8_lossy(&out).contains(&Clear(ClearType::All).to_string()));
        out.clear();
        e.buffer.col = 1;
        view.draw_to(&e, (80, 4), &mut out).unwrap();
        let rendered = String::from_utf8_lossy(&out);
        assert!(!rendered.contains("abcdef"));
        assert!(!rendered.contains("second"));
        out.clear();
        e.buffer.col = 0;
        e.buffer.delete();
        view.draw_to(&e, (80, 4), &mut out).unwrap();
        let rendered = String::from_utf8_lossy(&out);
        assert!(rendered.contains("bcdef"));
        assert!(rendered.contains(&Clear(ClearType::UntilNewLine).to_string()));
        assert!(view.dirty);
        e.file.save(&e.buffer.text()).unwrap();
        view.draw_to(&e, (80, 4), &mut Vec::new()).unwrap();
        assert!(!view.dirty);
        out.clear();
        e.buffer.row = 2;
        view.draw_to(&e, (80, 4), &mut out).unwrap();
        assert_eq!(view.top, 1);
        assert!(String::from_utf8_lossy(&out).contains("third"));
        for size in [(1, 1), (0, 0), (16, 8), (80, 4)] {
            view.draw_to(&e, size, &mut Vec::new()).unwrap();
        }
        assert_eq!(view.page_rows(), 2);
    }
    #[test]
    fn highlighting_cache_tracks_edits_in_multiline_state() {
        let dir = tempfile::tempdir().unwrap();
        let (file, _) = crate::file::File::open(dir.path().join("new.rs")).unwrap();
        let mut e = Editor::new(file, "/*\nfn main() {}\n*/");
        let mut view = View::default();
        view.draw_to(&e, (80, 4), &mut Vec::new()).unwrap();
        assert_eq!(
            view.styles.as_ref().unwrap()[1][0],
            crate::syntax::Style::Comment
        );
        e.buffer.row = 1;
        view.draw_to(&e, (80, 4), &mut Vec::new()).unwrap();
        assert_eq!(view.revision, Some((0, 0)));
        e.buffer.row = 0;
        e.buffer.delete_line();
        view.draw_to(&e, (80, 4), &mut Vec::new()).unwrap();
        assert_eq!(
            view.styles.as_ref().unwrap()[0][0],
            crate::syntax::Style::Keyword
        );
    }
    #[test]
    fn clipping_controls_and_wide() {
        assert_eq!(clipped("a界b".chars(), 2, 2), " b");
        assert_eq!(clipped("\u{1b}".chars(), 0, 10), "�");
    }
}

#[cfg(test)]
mod styled_tests {
    use super::*;
    #[test]
    fn styled_clipping_preserves_cells_and_resets() {
        let line: Vec<char> = "\t界é\u{1b}".chars().collect();
        let mut out = Vec::new();
        styled_line(
            &mut out,
            &line,
            &vec![crate::syntax::Style::Keyword; line.len()],
            5,
            4,
        )
        .unwrap();
        let rendered = String::from_utf8(out).unwrap();
        assert!(rendered.contains(" é�"));
        assert!(rendered.ends_with(&SetForegroundColor(Color::Reset).to_string()));
    }
}
