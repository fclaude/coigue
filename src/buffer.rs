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
