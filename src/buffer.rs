#[derive(Debug)]
pub struct Buffer {
    pub lines: Vec<Vec<char>>,
    pub row: usize,
    pub col: usize,
    revision: u64,
}
impl Buffer {
    pub fn new(text: &str) -> Self {
        Self {
            lines: text.split('\n').map(|s| s.chars().collect()).collect(),
            row: 0,
            col: 0,
            revision: 0,
        }
    }
    pub fn revision(&self) -> u64 {
        self.revision
    }
    pub fn open_line(&mut self) {
        self.revision += 1;
        self.row += 1;
        self.lines.insert(self.row, vec![]);
        self.col = 0;
    }
    pub fn text(&self) -> String {
        self.lines
            .iter()
            .map(|s| s.iter().collect::<String>())
            .collect::<Vec<_>>()
            .join("\n")
    }
    pub fn clamp(&mut self, insert: bool) {
        self.row = self.row.min(self.lines.len() - 1);
        self.col = self.col.min(
            self.lines[self.row]
                .len()
                .saturating_sub(usize::from(!insert)),
        );
    }
    pub fn insert(&mut self, ch: char) {
        self.revision += 1;
        if ch == '\n' {
            let tail = self.lines[self.row].split_off(self.col);
            self.row += 1;
            self.lines.insert(self.row, tail);
            self.col = 0;
        } else {
            self.lines[self.row].insert(self.col, ch);
            self.col += 1;
        }
    }
    pub fn backspace(&mut self) {
        if self.col > 0 {
            self.revision += 1;
            self.col -= 1;
            self.lines[self.row].remove(self.col);
        } else if self.row > 0 {
            self.revision += 1;
            let line = self.lines.remove(self.row);
            self.row -= 1;
            self.col = self.lines[self.row].len();
            self.lines[self.row].extend(line);
        }
    }
    pub fn delete(&mut self) {
        if self.col < self.lines[self.row].len() {
            self.revision += 1;
            self.lines[self.row].remove(self.col);
        }
        self.clamp(false);
    }
    pub fn replace(&mut self, ch: char) {
        if let Some(current) = self.lines[self.row].get_mut(self.col)
            && *current != ch
        {
            *current = ch;
            self.revision += 1;
        }
    }
    pub fn paste_line(&mut self, line: &[char], after: bool, terminated: bool) {
        self.revision += 1;
        if self.lines.len() == 1 && self.lines[0].is_empty() {
            self.lines[0] = line.to_vec();
            // An empty buffer is represented by one placeholder line. Restore
            // the copied terminator as well when replacing that placeholder.
            if terminated {
                self.lines.push(Vec::new());
            }
        } else {
            self.row += usize::from(after);
            self.lines.insert(self.row, line.to_vec());
        }
        self.col = 0;
    }
    pub fn paste_chars(&mut self, chars: &[char], after: bool) {
        if chars.is_empty() {
            return;
        }
        self.revision += 1;
        let start = (self.col + usize::from(after)).min(self.lines[self.row].len());
        self.lines[self.row].splice(start..start, chars.iter().copied());
        self.col = start + chars.len() - 1;
    }
    pub fn indent(&mut self, right: bool) {
        let line = &mut self.lines[self.row];
        if right {
            line.splice(0..0, [' '; 4]);
            self.col += 4;
            self.revision += 1;
        } else {
            let count = if line.first() == Some(&'\t') {
                1
            } else {
                line.iter().take(4).take_while(|&&c| c == ' ').count()
            };
            if count > 0 {
                line.drain(..count);
                self.col = self.col.saturating_sub(count);
                self.revision += 1;
            }
        }
        self.clamp(false);
    }
    /// Literal, case-sensitive search in character coordinates; wraps once.
    pub fn find(&self, query: &[char], forward: bool) -> Option<(usize, usize)> {
        if query.is_empty() {
            return None;
        }
        let cursor = (self.row, self.col);
        let mut before = None;
        let mut after = None;
        for (row, line) in self.lines.iter().enumerate() {
            for (col, window) in line.windows(query.len()).enumerate() {
                if window != query {
                    continue;
                }
                let position = (row, col);
                if forward {
                    if position > cursor {
                        return Some(position);
                    }
                    if before.is_none() {
                        before = Some(position);
                    }
                } else {
                    if position < cursor {
                        before = Some(position);
                    }
                    after = Some(position);
                }
            }
        }
        if forward { before } else { before.or(after) }
    }
    pub fn delete_line(&mut self) {
        self.revision += 1;
        self.lines.remove(self.row);
        if self.lines.is_empty() {
            self.lines.push(vec![]);
        }
        self.clamp(false);
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn unicode_split_join() {
        let mut b = Buffer::new("é🦀\n");
        b.col = 1;
        b.insert('\n');
        assert_eq!(b.text(), "é\n🦀\n");
        b.backspace();
        assert_eq!(b.text(), "é🦀\n");
        b.delete();
        assert_eq!(b.text(), "é\n");
    }
    #[test]
    fn empty_and_final_newlines() {
        for s in ["", "\n", "a\n\n", "a"] {
            assert_eq!(Buffer::new(s).text(), s);
        }
        let mut b = Buffer::new("");
        b.delete_line();
        b.backspace();
        b.delete();
        assert_eq!(b.text(), "");
    }
}
