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
impl View {
    pub fn page_rows(&self) -> usize {
        usize::from(self.size.1.saturating_sub(2)).max(1)
    }
    pub fn draw(&mut self, e: &Editor) -> io::Result<()> {
        let mut out = io::stdout().lock();
        self.draw_to(e, terminal::size()?, &mut out)
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
        if self.revision != Some(revision) {
            if self.revision.map(|r| r.0) != Some(revision.0) {
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
        } else {
            e.message.clone()
        };
        queue!(
            frame[usize::from(h - 1)],
            Print(clipped(message.chars(), 0, width))
        )?;
        let (x, y) = if e.mode == Mode::Command {
            (
                e.command.chars().count().saturating_add(1).min(width - 1) as u16,
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
                    clipped(message.chars(), 0, width)
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
