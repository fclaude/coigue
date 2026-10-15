const HISTORY_LIMIT: usize = 100;

#[derive(Debug)]
struct Snapshot {
    lines: Vec<Vec<char>>,
    row: usize,
    col: usize,
}

#[derive(Debug)]
pub struct Buffer {
    pub lines: Vec<Vec<char>>,
    pub row: usize,
    pub col: usize,
    revision: u64,
    undo: Vec<Snapshot>,
    redo: Vec<Snapshot>,
    edit_origin: Option<(usize, usize)>,
    before_edit: Option<Snapshot>,
}
impl Buffer {
    pub fn new(text: &str) -> Self {
        Self {
            lines: text.split('\n').map(|s| s.chars().collect()).collect(),
            row: 0,
            col: 0,
            revision: 0,
            undo: Vec::new(),
            redo: Vec::new(),
            edit_origin: None,
            before_edit: None,
        }
    }
    pub fn begin_edit(&mut self) {
        self.edit_origin.get_or_insert((self.row, self.col));
    }
    fn snapshot(&self) -> Snapshot {
        Snapshot {
            lines: self.lines.clone(),
            row: self.row,
            col: self.col,
        }
    }
    // Capture lazily: navigation, copying, and search never copy the buffer.
    fn change(&mut self) {
        if self.before_edit.is_none() {
            let mut snapshot = self.snapshot();
            if let Some((row, col)) = self.edit_origin {
                snapshot.row = row;
                snapshot.col = col;
            }
            self.before_edit = Some(snapshot);
        }
        self.revision += 1;
    }
    pub fn finish_edit(&mut self) {
        self.edit_origin = None;
        if let Some(snapshot) = self.before_edit.take()
            && snapshot.lines != self.lines
        {
            self.undo.push(snapshot);
            if self.undo.len() > HISTORY_LIMIT {
                self.undo.remove(0);
            }
            self.redo.clear();
        }
    }
    fn restore(&mut self, snapshot: Snapshot) {
        self.lines = snapshot.lines;
        self.row = snapshot.row;
        self.col = snapshot.col;
        // Revisions must increase even when restoring older contents, so the
        // renderer invalidates highlighting and its cached dirty status.
        self.revision += 1;
        self.clamp(false);
    }
    pub fn undo(&mut self) -> bool {
        self.finish_edit();
        let Some(snapshot) = self.undo.pop() else {
            return false;
        };
        self.redo.push(self.snapshot());
        self.restore(snapshot);
        true
    }
    pub fn redo(&mut self) -> bool {
        self.finish_edit();
        let Some(snapshot) = self.redo.pop() else {
            return false;
        };
        self.undo.push(self.snapshot());
        self.restore(snapshot);
        true
    }
    pub fn revision(&self) -> u64 {
        self.revision
    }
    pub fn open_line(&mut self) {
        self.change();
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
        self.change();
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
            self.change();
            self.col -= 1;
            self.lines[self.row].remove(self.col);
        } else if self.row > 0 {
            self.change();
            let line = self.lines.remove(self.row);
            self.row -= 1;
            self.col = self.lines[self.row].len();
            self.lines[self.row].extend(line);
        }
    }
    pub fn delete(&mut self) {
        if self.col < self.lines[self.row].len() {
            self.change();
            self.lines[self.row].remove(self.col);
        }
        self.clamp(false);
    }
    pub fn replace(&mut self, ch: char) {
        if self.lines[self.row]
            .get(self.col)
            .is_some_and(|&current| current != ch)
        {
            self.change();
            self.lines[self.row][self.col] = ch;
        }
    }
    pub fn paste_line(&mut self, line: &[char], after: bool, terminated: bool) {
        self.change();
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
        let start = self.offset() + usize::from(after && self.col < self.lines[self.row].len());
        self.change();
        let mut text: Vec<_> = self.text().chars().collect();
        text.splice(start..start, chars.iter().copied());
        self.lines = text.split(|&c| c == '\n').map(<[char]>::to_vec).collect();
        self.set_offset(start + chars.len() - 1);
        self.clamp(false);
    }
    fn offset(&self) -> usize {
        self.lines[..self.row]
            .iter()
            .map(|line| line.len() + 1)
            .sum::<usize>()
            + self.col
    }
    fn set_offset(&mut self, mut offset: usize) {
        self.row = 0;
        while self.row + 1 < self.lines.len() && offset > self.lines[self.row].len() {
            offset -= self.lines[self.row].len() + 1;
            self.row += 1;
        }
        self.col = offset.min(self.lines[self.row].len());
    }
    fn word_class(c: char) -> u8 {
        if c.is_whitespace() {
            0
        } else if c.is_alphanumeric() || c == '_' {
            1
        } else {
            2
        }
    }
    fn next_word(chars: &[char], mut at: usize) -> usize {
        if let Some(&c) = chars.get(at) {
            let class = Self::word_class(c);
            while at < chars.len() && Self::word_class(chars[at]) == class {
                at += 1;
            }
            while at < chars.len() && Self::word_class(chars[at]) == 0 {
                at += 1;
            }
        }
        at
    }
    pub fn word_motion(&mut self, motion: char) {
        let chars: Vec<_> = self.text().chars().collect();
        let mut at = self.offset();
        match motion {
            'w' => at = Self::next_word(&chars, at),
            'b' => {
                at = at.saturating_sub(1);
                while at > 0 && Self::word_class(chars[at]) == 0 {
                    at -= 1;
                }
                while at > 0 && Self::word_class(chars[at - 1]) == Self::word_class(chars[at]) {
                    at -= 1;
                }
            }
            'e' => {
                if at < chars.len() {
                    at += 1;
                }
                while at < chars.len() && Self::word_class(chars[at]) == 0 {
                    at += 1;
                }
                while at + 1 < chars.len()
                    && Self::word_class(chars[at + 1]) == Self::word_class(chars[at])
                {
                    at += 1;
                }
            }
            _ => return,
        }
        self.set_offset(at);
        self.clamp(false);
    }
    /// Characterwise range, including literal line separators. Change retains
    /// trailing whitespace when starting on a non-whitespace run.
    pub fn word_range(&mut self, cut: bool, change: bool) -> Vec<char> {
        let mut chars: Vec<_> = self.text().chars().collect();
        let start = self.offset();
        let mut end = Self::next_word(&chars, start);
        if change && chars.get(start).is_some_and(|c| Self::word_class(*c) != 0) {
            end = start + 1;
            while end < chars.len()
                && Self::word_class(chars[end]) == Self::word_class(chars[start])
            {
                end += 1;
            }
        }
        let copied = chars[start..end].to_vec();
        if cut && start < end {
            self.change();
            chars.drain(start..end);
            self.lines = chars.split(|&c| c == '\n').map(<[char]>::to_vec).collect();
            self.set_offset(start);
        }
        copied
    }
    pub fn indent(&mut self, right: bool) {
        if right {
            self.change();
            self.lines[self.row].splice(0..0, [' '; 4]);
            self.col += 4;
        } else {
            let line = &self.lines[self.row];
            let count = if line.first() == Some(&'\t') {
                1
            } else {
                line.iter().take(4).take_while(|&&c| c == ' ').count()
            };
            if count > 0 {
                self.change();
                self.lines[self.row].drain(..count);
                self.col = self.col.saturating_sub(count);
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
        self.change();
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
    fn word_boundaries_and_multiline_paste() {
        let mut b = Buffer::new("é_2!!\u{2003}\n\n界9");
        for (motion, position) in [
            ('e', (0, 2)),
            ('w', (0, 3)),
            ('e', (0, 4)),
            ('w', (2, 0)),
            ('e', (2, 1)),
            ('w', (2, 1)),
            ('b', (2, 0)),
            ('b', (0, 3)),
            ('b', (0, 0)),
        ] {
            b.word_motion(motion);
            assert_eq!((b.row, b.col), position);
        }
        assert_eq!(b.revision(), 0);
        b.col = 3;
        let cut = b.word_range(true, false);
        assert_eq!(cut.iter().collect::<String>(), "!!\u{2003}\n\n");
        b.clamp(false);
        b.col = 2;
        b.paste_chars(&cut, true);
        assert_eq!(b.text(), "é_2!!\u{2003}\n\n界9");
        let mut empty = Buffer::new("");
        assert!(empty.word_range(true, false).is_empty());
        assert_eq!(empty.revision(), 0);
    }
    #[test]
    fn history_limit_and_monotonic_revisions() {
        let mut buffer = Buffer::new("");
        for _ in 0..HISTORY_LIMIT + 5 {
            buffer.begin_edit();
            buffer.insert('界');
            buffer.finish_edit();
        }
        let mut revision = buffer.revision();
        for _ in 0..HISTORY_LIMIT {
            assert!(buffer.undo());
            assert!(buffer.revision() > revision);
            revision = buffer.revision();
        }
        assert!(!buffer.undo());
        assert_eq!(buffer.lines[0].len(), 5);
        for _ in 0..HISTORY_LIMIT {
            assert!(buffer.redo());
        }
        assert!(!buffer.redo());
        assert_eq!(buffer.lines[0].len(), HISTORY_LIMIT + 5);
    }
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
