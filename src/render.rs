use crate::editor::{Editor, Mode};
use crate::theme::{Palette, Theme};
use crossterm::{
    cursor::{Hide, MoveTo, Show},
    queue,
    style::{Attribute, Print, SetAttribute, SetBackgroundColor, SetForegroundColor},
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
    cursor: Option<(u16, u16)>,
    revision: Option<(u64, u64)>,
    styles: Option<Vec<Vec<crate::syntax::Style>>>,
    semantic_revision: u64,
    dirty: bool,
    generation: u64,
    syntax_path: Option<std::path::PathBuf>,
    theme: Option<Theme>,
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
fn row_colors(out: &mut impl Write, palette: Palette, surface: bool) -> io::Result<()> {
    queue!(
        out,
        SetForegroundColor(palette.foreground),
        SetBackgroundColor(if surface {
            palette.surface
        } else {
            palette.background
        })
    )
}
// Apply exactly the same cell clipping as plain text; ANSI bytes never count as cells.
fn styled_line(
    out: &mut impl Write,
    line: &[char],
    styles: &[crate::syntax::Style],
    left: usize,
    width: usize,
    palette: Palette,
    selection: Option<std::ops::Range<usize>>,
) -> io::Result<()> {
    let mut col = 0;
    let mut previous = palette.foreground;
    let mut previous_bold = false;
    let mut previous_italic = false;
    let mut reversed = false;
    for (index, (c, style)) in line.iter().zip(styles).enumerate() {
        let (s, w) = cell(*c);
        let visible = if col >= left && col + w <= left + width {
            s
        } else if col < left + width && col + w > left {
            " ".repeat((col + w).min(left + width) - col.max(left))
        } else {
            String::new()
        };
        if !visible.is_empty() {
            let selected = selection
                .as_ref()
                .is_some_and(|range| range.contains(&index));
            if selected != reversed {
                queue!(
                    out,
                    SetAttribute(if selected {
                        Attribute::Reverse
                    } else {
                        Attribute::NoReverse
                    })
                )?;
                reversed = selected;
            }
            let next = palette.syntax(*style);
            if next != previous {
                queue!(out, SetForegroundColor(next))?;
                previous = next;
            }
            let bold = palette.bold(*style);
            if bold != previous_bold {
                queue!(
                    out,
                    SetAttribute(if bold {
                        Attribute::Bold
                    } else {
                        Attribute::NormalIntensity
                    })
                )?;
                previous_bold = bold;
            }
            let italic = *style == crate::syntax::Style::Emphasis;
            if italic != previous_italic {
                queue!(
                    out,
                    SetAttribute(if italic {
                        Attribute::Italic
                    } else {
                        Attribute::NoItalic
                    })
                )?;
                previous_italic = italic;
            }
            queue!(out, Print(visible))?;
        }
        col += w;
    }
    if reversed {
        queue!(out, SetAttribute(Attribute::NoReverse))?;
    }
    if selection
        .as_ref()
        .is_some_and(|range| range.contains(&line.len()))
        && col >= left
        && col < left + width
    {
        queue!(
            out,
            SetAttribute(Attribute::Reverse),
            Print(" "),
            SetAttribute(Attribute::NoReverse)
        )?;
    }
    if previous_bold {
        queue!(out, SetAttribute(Attribute::NormalIntensity))?;
    }
    if previous_italic {
        queue!(out, SetAttribute(Attribute::NoItalic))?;
    }
    queue!(out, SetForegroundColor(palette.foreground))
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
        self.draw_browser_lines(lines, e, out)
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
        self.draw_browser_lines(lines, e, out)
    }
    fn draw_browser_lines(
        &mut self,
        lines: Vec<String>,
        e: &Editor,
        out: &mut impl Write,
    ) -> io::Result<()> {
        let palette = e.theme.palette();
        let (w, h) = self.size;
        let width = usize::from(w);
        let prompt = lines.last().unwrap();
        let mut frame = Vec::new();
        let mut update = Vec::new();
        queue!(update, BeginSynchronizedUpdate, Hide)?;
        for (y, line) in lines.iter().enumerate() {
            // Keep the end of a long query visible while typing.
            let left = if y == usize::from(h - 1)
                && (line.starts_with('/')
                    || line.starts_with(':')
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
                let surface = e.welcome.is_none() || e.picker.is_some() || e.explorer.is_some();
                row_colors(
                    &mut update,
                    palette,
                    surface && y < usize::from(h - 1) && (y == 0 || line.starts_with("> ")),
                )?;
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
        if self.previous == frame && self.cursor == Some((x, h - 1)) {
            return Ok(());
        }
        row_colors(&mut update, palette, false)?;
        queue!(update, MoveTo(x, h - 1), Show, EndSynchronizedUpdate)?;
        out.write_all(&update)?;
        out.flush()?;
        self.previous = frame;
        self.cursor = Some((x, h - 1));
        Ok(())
    }
    fn draw_to(&mut self, e: &Editor, size: (u16, u16), out: &mut impl Write) -> io::Result<()> {
        let (w, h) = size;
        if self.size != size {
            self.previous.clear();
            self.size = size;
        }
        if self.theme != Some(e.theme) {
            // Plain rows and empty tails must repaint too, even when their text
            // and the cached syntax tokens are unchanged.
            self.previous.clear();
            self.theme = Some(e.theme);
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
        if let Some(directory) = &e.welcome {
            let mut lines = vec![String::new(); usize::from(h)];
            let content = [
                "Coigüe".to_owned(),
                "Welcome".to_owned(),
                format!("Directory: {}", directory.display()),
                "Ctrl-O / - / :explore   Browse and create files".to_owned(),
                "Ctrl-P / :open         Find a file".to_owned(),
                ":q                     Quit".to_owned(),
            ];
            let available = usize::from(h.saturating_sub(1));
            let start = available.saturating_sub(content.len()) / 2;
            for (line, text) in lines.iter_mut().skip(start).take(available).zip(content) {
                *line = text;
            }
            lines[usize::from(h - 1)] = if e.mode == Mode::Command {
                format!(":{}", e.command)
            } else if !e.message.is_empty() {
                e.message.clone()
            } else {
                "Ctrl-O browse | Ctrl-P find | :q quit".into()
            };
            return self.draw_browser_lines(lines, e, out);
        }
        let palette = e.theme.palette();
        let rows = usize::from(h.saturating_sub(2));
        let width = usize::from(w);
        let b = &e.buffer;
        // Keep at least one text cell on narrow terminals; omit the gutter if necessary.
        let number_width = b.lines.len().to_string().len() + 1;
        let gutter = if width > number_width {
            number_width
        } else {
            0
        };
        let text_width = width - gutter;
        if b.row < self.top {
            self.top = b.row;
        } else if b.row >= self.top + rows.max(1) {
            self.top = b.row + 1 - rows.max(1);
        }
        let col: usize = b.lines[b.row].iter().take(b.col).map(|c| cell(*c).1).sum();
        if col < self.left {
            self.left = col;
        } else if col >= self.left + text_width {
            self.left = col + 1 - text_width;
        }
        let revision = (b.revision(), e.file.revision());
        let path_changed = self.syntax_path.as_ref() != Some(&e.file.path);
        let semantic_changed = self.semantic_revision != e.lsp.semantic_revision;
        if self.revision != Some(revision) || path_changed || semantic_changed {
            if self.revision.map(|r| r.0) != Some(revision.0) || path_changed || semantic_changed {
                self.syntax_path = Some(e.file.path.clone());
                self.styles = crate::syntax::for_path(&e.file.path, &b.lines);
                if let Some(styles) = &mut self.styles {
                    for token in &e.lsp.semantic_tokens {
                        if let Some(line) = styles.get_mut(token.row)
                            && let Some(span) = line.get_mut(token.start..token.end)
                        {
                            span.fill(token.style);
                        }
                    }
                }
                self.semantic_revision = e.lsp.semantic_revision;
            }
            self.dirty = e.file.dirty(&b.text());
            self.revision = Some(revision);
        }
        let mut frame = vec![Vec::new(); usize::from(h)];
        for (y, line_out) in frame.iter_mut().take(rows).enumerate() {
            if let Some(line) = b.lines.get(self.top + y) {
                if gutter > 0 {
                    let severity = e.lsp.line_severity(self.top + y);
                    let mut number = format!("{:>digits$}", self.top + y + 1, digits = gutter - 1);
                    if severity.is_none() {
                        number.push(' ');
                    }
                    queue!(line_out, SetForegroundColor(palette.muted), Print(number),)?;
                    if let Some(severity) = severity {
                        queue!(
                            line_out,
                            SetForegroundColor(palette.diagnostic(severity)),
                            Print(match severity {
                                1 => "E",
                                2 => "W",
                                3 => "I",
                                _ => "H",
                            }),
                        )?;
                    }
                    queue!(line_out, SetForegroundColor(palette.foreground))?;
                }
                if let Some(styles) = &self.styles {
                    styled_line(
                        line_out,
                        line,
                        &styles[self.top + y],
                        self.left,
                        text_width,
                        palette,
                        e.selection(self.top + y),
                    )?;
                } else if let Some(selection) = e.selection(self.top + y) {
                    styled_line(
                        line_out,
                        line,
                        &vec![crate::syntax::Style::Plain; line.len()],
                        self.left,
                        text_width,
                        palette,
                        Some(selection),
                    )?;
                } else {
                    queue!(
                        line_out,
                        Print(clipped(line.iter().copied(), self.left, text_width))
                    )?;
                }
            } else {
                queue!(line_out, Print("~"))?;
            }
        }

        let mut status = format!(
            "{:?} {}{}  {}:{}",
            e.mode,
            e.file.path.display(),
            if self.dirty { " [+]" } else { "" },
            b.row + 1,
            b.col + 1
        );
        if !e.lsp.status.is_empty() {
            let (errors, warnings) = e.lsp.counts();
            status.push_str(&format!(" | {} E:{errors} W:{warnings}", e.lsp.status));
        }
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
        } else if !e.pending_input().is_empty() {
            e.pending_input()
        } else if e.message.is_empty() {
            e.lsp
                .at_cursor(b.row, b.col)
                .or_else(|| e.lsp.failure())
                .unwrap_or("")
                .into()
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
                (gutter + col - self.left).min(width - 1) as u16,
                (b.row - self.top).min(rows.saturating_sub(1)) as u16,
            )
        };
        // Assemble the entire update before touching the terminal. Unchanged rows
        // stay in place; synchronized terminals display changed rows atomically.
        if self.previous == frame && self.cursor == Some((x, y)) {
            return Ok(());
        }
        let mut update = Vec::new();
        queue!(update, BeginSynchronizedUpdate, Hide)?;
        for (row, line) in frame.iter().enumerate() {
            if self.previous.get(row) != Some(line) {
                row_colors(&mut update, palette, h >= 2 && row == usize::from(h - 2))?;
                queue!(update, MoveTo(0, row as u16))?;
                update.extend_from_slice(line);
                // Reset a possible pending wrap before clearing the unused tail.
                // A full-width line needs no clearing.
                let cells = if row < rows {
                    b.lines.get(self.top + row).map_or(1, |line| {
                        let end_cell = line.iter().map(|c| cell(*c).1).sum::<usize>();
                        let selected_end = usize::from(
                            e.selection(self.top + row)
                                .is_some_and(|range| range.contains(&line.len()))
                                && end_cell >= self.left
                                && end_cell < self.left + text_width,
                        );
                        gutter
                            + selected_end
                            + clipped(line.iter().copied(), self.left, text_width)
                                .chars()
                                .map(|c| c.width().unwrap_or(0))
                                .sum::<usize>()
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
        row_colors(&mut update, palette, false)?;
        queue!(update, MoveTo(x, y), Show, EndSynchronizedUpdate)?;
        out.write_all(&update)?;
        out.flush()?;
        self.previous = frame;
        self.cursor = Some((x, y));
        Ok(())
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn visual_selection_repaints_empty_lines_scrolls_and_cancels() {
        use crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
        let dir = tempfile::tempdir().unwrap();
        let (file, _) = crate::file::File::open(dir.path().join("selection.md")).unwrap();
        let mut e = Editor::new(file, "a界\n\nlast\nfour\nfive\nsix\nseven");
        let mut view = View::default();
        let mut out = Vec::new();
        view.draw_to(&e, (14, 5), &mut out).unwrap();
        for c in "v2jl".chars() {
            e.key(KeyEvent::new(KeyCode::Char(c), KeyModifiers::NONE), 3);
        }
        view.draw_to(&e, (14, 5), &mut out).unwrap();
        let reverse = SetAttribute(Attribute::Reverse).to_string();
        for row in &view.previous[..3] {
            assert!(String::from_utf8_lossy(row).contains(&reverse));
        }
        // The selected empty line has an actual highlighted cell, retained after clearing.
        assert!(String::from_utf8_lossy(&view.previous[1]).contains(&format!("{reverse} ")));
        e.key(KeyEvent::new(KeyCode::Char('G'), KeyModifiers::NONE), 3);
        view.draw_to(&e, (14, 5), &mut out).unwrap();
        assert!(view.top > 0);
        assert!(String::from_utf8_lossy(&view.previous[0]).contains(&reverse));
        e.key(KeyEvent::new(KeyCode::Esc, KeyModifiers::NONE), 3);
        view.draw_to(&e, (14, 5), &mut out).unwrap();
        assert!(
            view.previous
                .iter()
                .all(|row| !String::from_utf8_lossy(row).contains(&reverse))
        );
        for c in "Vgg".chars() {
            e.key(KeyEvent::new(KeyCode::Char(c), KeyModifiers::NONE), 3);
        }
        view.draw_to(&e, (5, 3), &mut out).unwrap();
        view.draw_to(&e, (1, 1), &mut out).unwrap();
        assert_eq!(e.buffer.text(), "a界\n\nlast\nfour\nfive\nsix\nseven");
    }
    #[test]
    fn visual_plain_text_horizontal_scroll_and_theme_reset() {
        use crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
        let dir = tempfile::tempdir().unwrap();
        let (file, _) = crate::file::File::open(dir.path().join("selection.txt")).unwrap();
        let mut e = Editor::new(file, "abcdefghi\t界éxyz");
        let mut view = View::default();
        let mut out = Vec::new();
        for c in "v$".chars() {
            e.key(KeyEvent::new(KeyCode::Char(c), KeyModifiers::NONE), 2);
        }
        for theme in [Theme::Terminal, Theme::Monochrome, Theme::CatppuccinMocha] {
            e.theme = theme;
            view.draw_to(&e, (9, 4), &mut out).unwrap();
            assert!(view.left > 0);
            assert!(
                String::from_utf8_lossy(&view.previous[0])
                    .contains(&SetAttribute(Attribute::Reverse).to_string())
            );
        }
        e.key(KeyEvent::new(KeyCode::Esc, KeyModifiers::NONE), 2);
        view.draw_to(&e, (9, 4), &mut out).unwrap();
        assert!(
            !String::from_utf8_lossy(&view.previous[0])
                .contains(&SetAttribute(Attribute::Reverse).to_string())
        );
        assert!(view.styles.is_none());
    }
    #[test]
    fn visual_highlight_clips_tabs_unicode_and_resets_attributes() {
        let mut out = Vec::new();
        let line: Vec<_> = "\t界é".chars().collect();
        styled_line(
            &mut out,
            &line,
            &vec![crate::syntax::Style::Keyword; line.len()],
            5,
            2,
            Theme::Terminal.palette(),
            Some(1..3),
        )
        .unwrap();
        let rendered = String::from_utf8(out).unwrap();
        assert!(rendered.contains(&SetAttribute(Attribute::Reverse).to_string()));
        assert!(rendered.contains(" é"));
        assert!(rendered.contains(&SetAttribute(Attribute::NoReverse).to_string()));
        assert!(
            rendered.ends_with(&SetForegroundColor(crossterm::style::Color::Reset).to_string())
        );
    }
    #[test]
    fn markdown_highlighting_repaints_themes_and_tracks_fence_edits_and_history() {
        use crate::syntax::Style;
        use crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
        let dir = tempfile::tempdir().unwrap();
        let (file, _) = crate::file::File::open(dir.path().join("test.MD")).unwrap();
        let mut e = Editor::new(
            file,
            "# Heading\n*italic* plain **bold**\n```rust\nfn main() {}\n```",
        );
        let mut view = View::default();
        for theme in [
            Theme::CatppuccinMocha,
            Theme::GruvboxDark,
            Theme::CatppuccinLatte,
            Theme::Monochrome,
            Theme::Terminal,
        ] {
            e.theme = theme;
            let mut out = Vec::new();
            view.draw_to(&e, (80, 8), &mut out).unwrap();
            let rendered = String::from_utf8(out).unwrap();
            for attribute in [
                Attribute::Bold,
                Attribute::NormalIntensity,
                Attribute::Italic,
                Attribute::NoItalic,
            ] {
                assert!(rendered.contains(&SetAttribute(attribute).to_string()));
            }
            assert!(rendered.contains(&format!(
                "{}{}# Heading",
                SetForegroundColor(theme.palette().syntax(Style::Heading)),
                SetAttribute(Attribute::Bold)
            )));
            let styles = view.styles.as_ref().unwrap();
            assert_eq!(styles[0][0], Style::Heading);
            assert_eq!(styles[1][0], Style::Emphasis);
            assert_eq!(styles[3][0], Style::Keyword);
            let mut unchanged = Vec::new();
            view.draw_to(&e, (80, 8), &mut unchanged).unwrap();
            assert!(unchanged.is_empty());
        }
        e.buffer.row = 2;
        for key in [KeyCode::Char('d'), KeyCode::Char('d')] {
            e.key(KeyEvent::new(key, KeyModifiers::NONE), 6);
        }
        view.draw_to(&e, (80, 8), &mut Vec::new()).unwrap();
        assert!(
            view.styles.as_ref().unwrap()[2]
                .iter()
                .all(|s| *s == Style::Plain)
        );
        e.key(KeyEvent::new(KeyCode::Char('u'), KeyModifiers::NONE), 6);
        view.draw_to(&e, (80, 8), &mut Vec::new()).unwrap();
        assert_eq!(view.styles.as_ref().unwrap()[3][0], Style::Keyword);
        e.key(KeyEvent::new(KeyCode::Char('r'), KeyModifiers::CONTROL), 6);
        view.draw_to(&e, (80, 8), &mut Vec::new()).unwrap();
        assert_eq!(view.styles.as_ref().unwrap()[2][0], Style::Plain);
    }

    #[test]
    fn semantic_highlighting_uses_every_theme_and_reverts_to_lexical_colors() {
        use crate::syntax::Style;
        let dir = tempfile::tempdir().unwrap();
        for (extension, text, start, end) in
            [("rs", "fn main() {}", 3, 7), ("go", "func main() {}", 5, 9)]
        {
            let (file, _) =
                crate::file::File::open(dir.path().join(format!("test.{extension}"))).unwrap();
            let mut e = Editor::new(file, text);
            let mut view = View::default();
            view.draw_to(&e, (100, 6), &mut Vec::new()).unwrap();
            assert_eq!(view.styles.as_ref().unwrap()[0][0], Style::Keyword);
            assert_eq!(view.styles.as_ref().unwrap()[0][start], Style::Plain);
            e.lsp.semantic_tokens = vec![crate::semantic::Token {
                row: 0,
                start,
                end,
                style: Style::Function,
            }];
            e.lsp.semantic_revision += 1;
            for theme in [
                Theme::CatppuccinMocha,
                Theme::GruvboxDark,
                Theme::CatppuccinLatte,
                Theme::Monochrome,
                Theme::Terminal,
            ] {
                e.theme = theme;
                let mut out = Vec::new();
                view.draw_to(&e, (100, 6), &mut out).unwrap();
                assert_eq!(view.styles.as_ref().unwrap()[0][start], Style::Function);
                assert_eq!(view.styles.as_ref().unwrap()[0][0], Style::Keyword);
                let rendered = String::from_utf8_lossy(&out);
                assert!(rendered.contains(&format!(
                    "{}main",
                    SetForegroundColor(theme.palette().syntax(Style::Function))
                )));
                if theme == Theme::Terminal {
                    assert!(!rendered.contains("38;2;"));
                    assert!(!rendered.contains("48;2;"));
                }
                if theme == Theme::Monochrome {
                    assert!(rendered.contains(&SetAttribute(Attribute::Bold).to_string()));
                    assert!(
                        rendered.contains(&SetAttribute(Attribute::NormalIntensity).to_string())
                    );
                    assert!(!theme.palette().bold(Style::Function));
                }
                out.clear();
                view.draw_to(&e, (100, 6), &mut out).unwrap();
                assert!(out.is_empty());
            }
            e.lsp.semantic_tokens.clear();
            e.lsp.semantic_revision += 1;
            view.draw_to(&e, (100, 6), &mut Vec::new()).unwrap();
            assert_eq!(view.styles.as_ref().unwrap()[0][start], Style::Plain);
            assert_eq!(view.styles.as_ref().unwrap()[0][0], Style::Keyword);
            for size in [(1, 1), (2, 3), (4, 5), (100, 6)] {
                view.draw_to(&e, size, &mut Vec::new()).unwrap();
            }
        }
    }
    #[test]
    fn theme_switch_repaints_all_rows_and_tails_without_resetting_viewport_or_syntax() {
        for extension in ["txt", "rs"] {
            let dir = tempfile::tempdir().unwrap();
            let (file, _) =
                crate::file::File::open(dir.path().join(format!("test.{extension}"))).unwrap();
            let text = ["fn main() { let x = 42; }"; 10].join("\n");
            let mut e = Editor::new(file, &text);
            e.buffer.row = 9;
            e.buffer.col = 20;
            e.lsp.diagnostics = vec![crate::lsp::Diagnostic {
                start: (9, 0),
                end: (9, 1),
                severity: 2,
                message: "warning".into(),
            }];
            let mut view = View {
                top: 9,
                ..View::default()
            };
            view.draw_to(&e, (18, 6), &mut Vec::new()).unwrap();
            let position = (view.top, view.left, view.cursor);
            let revision = view.revision;
            let styles = view.styles.clone();
            for theme in [
                Theme::GruvboxDark,
                Theme::CatppuccinLatte,
                Theme::Monochrome,
                Theme::Terminal,
                Theme::CatppuccinMocha,
            ] {
                e.theme = theme;
                let palette = theme.palette();
                let mut out = Vec::new();
                view.draw_to(&e, (18, 6), &mut out).unwrap();
                let rendered = String::from_utf8(out).unwrap();
                for row in 0..6 {
                    assert!(
                        rendered.contains(&MoveTo(0, row).to_string()),
                        "stale row {row}"
                    );
                }
                assert!(rendered.contains(&SetBackgroundColor(palette.background).to_string()));
                assert!(rendered.contains(&SetBackgroundColor(palette.surface).to_string()));
                assert!(rendered.contains(&SetForegroundColor(palette.diagnostic(2)).to_string()));
                assert!(rendered.contains(&Clear(ClearType::UntilNewLine).to_string()));
                assert!(!rendered.contains(&Clear(ClearType::All).to_string()));
                if theme == Theme::Terminal {
                    assert!(
                        !rendered.contains(";2;"),
                        "terminal theme leaked RGB colors"
                    );
                }
                assert_eq!((view.top, view.left, view.cursor), position);
                assert_eq!(view.revision, revision);
                assert_eq!(view.styles, styles);
                let mut idle = Vec::new();
                view.draw_to(&e, (18, 6), &mut idle).unwrap();
                assert!(idle.is_empty());
            }
            assert_eq!(e.buffer.text(), text);
        }
    }

    #[test]
    fn welcome_and_browsers_repaint_themes_including_selected_rows_after_resize() {
        use crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join("file.txt"), "hello").unwrap();
        let mut e = Editor::welcome(dir.path().into());
        let mut view = View::default();
        view.draw_to(&e, (80, 8), &mut Vec::new()).unwrap();
        e.theme = Theme::CatppuccinLatte;
        let mut out = Vec::new();
        view.draw_to(&e, (80, 8), &mut out).unwrap();
        let rendered = String::from_utf8(out).unwrap();
        assert!(rendered.contains(&SetBackgroundColor(e.theme.palette().background).to_string()));
        assert!(rendered.contains(&MoveTo(0, 0).to_string()));
        e.key(KeyEvent::new(KeyCode::Char('-'), KeyModifiers::NONE), 6);
        let explorer = e.explorer.as_mut().unwrap();
        let deadline = std::time::Instant::now() + std::time::Duration::from_secs(3);
        while explorer.loading {
            explorer.poll();
            assert!(std::time::Instant::now() < deadline);
            std::thread::yield_now();
        }
        for picker in [false, true] {
            if picker {
                e.picker = Some(crate::picker::Picker::new(dir.path().into()));
            }
            for theme in [
                Theme::GruvboxDark,
                Theme::Monochrome,
                Theme::Terminal,
                Theme::CatppuccinMocha,
            ] {
                e.theme = theme;
                for size in [(0, 0), (1, 1), (80, 8)] {
                    view.draw_to(&e, size, &mut Vec::new()).unwrap();
                }
                e.theme = Theme::CatppuccinLatte;
                let mut out = Vec::new();
                view.draw_to(&e, (80, 8), &mut out).unwrap();
                let rendered = String::from_utf8(out).unwrap();
                for row in 0..8 {
                    assert!(rendered.contains(&MoveTo(0, row).to_string()));
                }
                assert!(
                    rendered.contains(&SetBackgroundColor(e.theme.palette().surface).to_string())
                );
                let mut idle = Vec::new();
                view.draw_to(&e, (80, 8), &mut idle).unwrap();
                assert!(idle.is_empty());
            }
        }
    }

    #[test]
    fn diagnostics_redraw_while_idle_and_preserve_gutter_width_and_prompt_priority() {
        let dir = tempfile::tempdir().unwrap();
        let (file, _) = crate::file::File::open(dir.path().join("test.rs")).unwrap();
        let mut e = Editor::new(file, "a😀b\nsecond\nthird");
        let mut view = View::default();
        view.draw_to(&e, (200, 6), &mut Vec::new()).unwrap();
        e.lsp.status = "rust-analyzer".into();
        e.lsp.diagnostics = vec![crate::lsp::Diagnostic {
            start: (0, 1),
            end: (1, 0),
            severity: 2,
            message: "Unicode warning\nwith control\u{1b}".into(),
        }];
        let mut out = Vec::new();
        view.draw_to(&e, (200, 6), &mut out).unwrap();
        assert!(String::from_utf8_lossy(&view.previous[0]).contains("W"));
        assert!(String::from_utf8_lossy(&view.previous[1]).contains("2 "));
        assert!(String::from_utf8_lossy(&view.previous[4]).contains("rust-analyzer E:0 W:1"));
        assert!(
            String::from_utf8_lossy(&view.previous[5]).contains("Unicode warning�with control�")
        );
        assert!(String::from_utf8_lossy(&out).contains(&MoveTo(2, 0).to_string()));
        out.clear();
        view.draw_to(&e, (200, 6), &mut out).unwrap();
        assert!(
            out.is_empty(),
            "idle polling should not write an unchanged frame"
        );
        e.mode = Mode::Command;
        e.command = "w".into();
        view.draw_to(&e, (200, 6), &mut Vec::new()).unwrap();
        assert_eq!(view.previous[5], b":w");
        e.mode = Mode::Normal;
        e.message = "Save failed".into();
        view.draw_to(&e, (200, 6), &mut Vec::new()).unwrap();
        assert_eq!(view.previous[5], b"Save failed");
        e.lsp.diagnostics.clear();
        for size in [(1, 1), (2, 3), (3, 4), (200, 6)] {
            view.draw_to(&e, size, &mut Vec::new()).unwrap();
        }
        assert!(String::from_utf8_lossy(&view.previous[0]).contains("1 "));
    }
    #[test]
    fn line_numbers_track_scroll_cursor_and_decimal_width_changes() {
        let dir = tempfile::tempdir().unwrap();
        let (file, _) = crate::file::File::open(dir.path().join("numbers")).unwrap();
        let mut e = Editor::new(file, &["text"; 9].join("\n"));
        let mut view = View::default();
        let mut out = Vec::new();
        view.draw_to(&e, (12, 5), &mut out).unwrap();
        assert!(String::from_utf8_lossy(&view.previous[0]).contains("1 "));
        assert!(String::from_utf8_lossy(&out).contains(&MoveTo(2, 0).to_string()));
        e.buffer.row = 8;
        e.buffer.open_line();
        out.clear();
        view.draw_to(&e, (12, 5), &mut out).unwrap();
        assert_eq!(view.top, 7);
        assert!(String::from_utf8_lossy(&view.previous[0]).contains(" 8 "));
        assert!(String::from_utf8_lossy(&view.previous[2]).contains("10 "));
        assert!(String::from_utf8_lossy(&out).contains(&MoveTo(3, 2).to_string()));
        e.buffer.delete_line();
        view.draw_to(&e, (12, 5), &mut Vec::new()).unwrap();
        assert!(String::from_utf8_lossy(&view.previous[0]).contains("8 "));
        assert_eq!(view.previous[2], b"~");
        // Neither status nor command prompts inherit the number gutter.
        e.mode = Mode::Command;
        e.command = "w".into();
        out.clear();
        view.draw_to(&e, (12, 5), &mut out).unwrap();
        assert_eq!(view.previous[4], b":w");
        assert!(String::from_utf8_lossy(&out).contains(&MoveTo(2, 4).to_string()));
    }
    #[test]
    fn line_number_gutter_stays_fixed_with_unicode_horizontal_scroll_and_resize() {
        let dir = tempfile::tempdir().unwrap();
        let (file, _) = crate::file::File::open(dir.path().join("numbers.rs")).unwrap();
        let mut e = Editor::new(file, "a\t界xyz");
        e.buffer.col = 4;
        let mut view = View::default();
        let mut out = Vec::new();
        view.draw_to(&e, (10, 4), &mut out).unwrap();
        assert_eq!(view.left, 1);
        let row = String::from_utf8_lossy(&view.previous[0]);
        assert!(row.contains("1 "));
        assert!(row.contains("    界xy"));
        assert!(String::from_utf8_lossy(&out).contains(&MoveTo(9, 0).to_string()));
        for size in [(1, 4), (2, 4), (3, 4), (0, 0), (80, 4)] {
            view.draw_to(&e, size, &mut Vec::new()).unwrap();
        }
        e.buffer.col = 0;
        view.draw_to(&e, (80, 4), &mut Vec::new()).unwrap();
        assert_eq!(view.left, 0);
        assert!(String::from_utf8_lossy(&view.previous[0]).contains("1 "));
        out.clear();
        view.draw_to(&e, (80, 4), &mut out).unwrap();
        assert!(!String::from_utf8_lossy(&out).contains("1 "));
    }
    #[test]
    fn welcome_renders_directory_hints_and_commands_across_resizes() {
        let mut e = Editor::welcome(std::path::PathBuf::from("/tmp/space café 界"));
        let mut view = View::default();
        let mut out = Vec::new();
        view.draw_to(&e, (80, 24), &mut out).unwrap();
        let text = String::from_utf8_lossy(&out);
        for expected in [
            "Coigüe",
            "Welcome",
            "/tmp/space café 界",
            "Ctrl-O",
            "Ctrl-P",
            ":q",
        ] {
            assert!(text.contains(expected), "{expected}");
        }
        for size in [(0, 0), (1, 1), (12, 2), (80, 24)] {
            view.draw_to(&e, size, &mut Vec::new()).unwrap();
        }
        e.mode = Mode::Command;
        e.command = "explore".into();
        out.clear();
        view.draw_to(&e, (80, 24), &mut out).unwrap();
        assert!(String::from_utf8_lossy(&out).contains(":explore"));
        assert_eq!(e.buffer.text(), "");
    }
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
    fn clipped_markdown_emphasis_resets_attributes_before_the_next_row() {
        for (style, enabled, reset) in [
            (
                crate::syntax::Style::Emphasis,
                Attribute::Italic,
                Attribute::NoItalic,
            ),
            (
                crate::syntax::Style::Strong,
                Attribute::Bold,
                Attribute::NormalIntensity,
            ),
        ] {
            let line: Vec<char> = "\t界é".chars().collect();
            let mut out = Vec::new();
            styled_line(
                &mut out,
                &line,
                &vec![style; line.len()],
                5,
                2,
                Theme::Terminal.palette(),
                None,
            )
            .unwrap();
            let rendered = String::from_utf8(out).unwrap();
            assert!(rendered.contains(&format!(
                "{} é{}",
                SetAttribute(enabled),
                SetAttribute(reset)
            )));
            assert!(
                rendered.ends_with(&SetForegroundColor(crossterm::style::Color::Reset).to_string())
            );
        }
    }

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
            Theme::Terminal.palette(),
            None,
        )
        .unwrap();
        let rendered = String::from_utf8(out).unwrap();
        assert!(rendered.contains(" é�"));
        assert!(
            rendered.ends_with(&SetForegroundColor(crossterm::style::Color::Reset).to_string())
        );
    }
}
