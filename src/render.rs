use crate::editor::{Editor, Mode};
use crossterm::{
    cursor::{Hide, MoveTo, Show},
    queue,
    style::{Color, Print, SetForegroundColor},
    terminal::{self, Clear, ClearType},
};
use std::io::{self, Write};
use unicode_width::UnicodeWidthChar;
#[derive(Default)]
pub struct View {
    top: usize,
    left: usize,
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
    pub fn draw(&mut self, e: &Editor) -> io::Result<()> {
        let (w, h) = terminal::size()?;
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
        let mut out = io::stdout();
        queue!(
            out,
            SetForegroundColor(Color::Reset),
            Hide,
            MoveTo(0, 0),
            Clear(ClearType::All)
        )?;
        let styles = (e.file.path.extension().is_some_and(|ext| ext == "rs"))
            .then(|| crate::syntax::rust(&b.lines));
        for y in 0..rows {
            queue!(out, MoveTo(0, y as u16))?;
            if let Some(line) = b.lines.get(self.top + y) {
                if let Some(styles) = &styles {
                    styled_line(&mut out, line, &styles[self.top + y], self.left, width)?;
                } else {
                    queue!(out, Print(clipped(line.iter().copied(), self.left, width)))?;
                }
            } else {
                queue!(out, Print("~"))?;
            }
        }

        let status = format!(
            "{:?} {}{}  {}:{}",
            e.mode,
            e.file.path.display(),
            if e.file.dirty(&b.text()) { " [+]" } else { "" },
            b.row + 1,
            b.col + 1
        );
        if h >= 2 {
            queue!(
                out,
                MoveTo(0, h - 2),
                Print(clipped(status.chars(), 0, width))
            )?;
        }
        let message = if e.mode == Mode::Command {
            format!(":{}", e.command)
        } else {
            e.message.clone()
        };
        queue!(
            out,
            MoveTo(0, h - 1),
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
        queue!(out, MoveTo(x, y), Show)?;
        out.flush()
    }
}
#[cfg(test)]
mod tests {
    use super::*;
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
