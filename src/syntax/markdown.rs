//! Lightweight Markdown source highlighting, with character offsets like the other lexers.
use super::Style;

pub(super) fn highlight(lines: &[Vec<char>]) -> Vec<Vec<Style>> {
    use Style::*;
    let mut result: Vec<Vec<Style>> = lines.iter().map(|l| vec![Plain; l.len()]).collect();
    let mut row = 0;
    let mut comment = false;
    let mut paragraph = false;
    while row < lines.len() {
        let line = lines[row].strip_suffix(&['\r']).unwrap_or(&lines[row]);
        let indent = line.iter().take_while(|c| **c == ' ').count();
        let start = indent.min(line.len());
        if !comment && indent <= 3 {
            if let Some((marker, count, info)) = fence(&line[start..]) {
                let end = (row + 1..lines.len()).find(|r| {
                    let candidate = &lines[*r];
                    let leading = candidate.iter().take_while(|c| **c == ' ').count();
                    if leading > 3 {
                        return false;
                    }
                    let candidate = &candidate[leading..];
                    let length = candidate.iter().take_while(|c| **c == marker).count();
                    length >= count && candidate[length..].iter().all(|c| c.is_whitespace())
                });
                result[row].fill(Macro);
                let code_end = end.unwrap_or(lines.len());
                let code = &lines[row + 1..code_end];
                let styles = match info.as_str() {
                    "rust" | "rs" => super::rust(code),
                    "go" | "golang" => super::go(code),
                    _ => code.iter().map(|l| vec![String; l.len()]).collect(),
                };
                for (target, styles) in result[row + 1..code_end].iter_mut().zip(styles) {
                    *target = styles;
                }
                if let Some(end) = end {
                    result[end].fill(Macro);
                }
                row = end.map_or(lines.len(), |r| r + 1);
                paragraph = false;
                continue;
            }
            let trimmed = &line[start..];
            if paragraph && setext(trimmed) {
                result[row - 1].fill(Heading);
                result[row].fill(Heading);
                paragraph = false;
                row += 1;
                continue;
            }
            if thematic_break(trimmed) {
                result[row].fill(Macro);
                paragraph = false;
                row += 1;
                continue;
            }
        }
        if !comment && (indent >= 4 || line.first() == Some(&'\t')) {
            result[row].fill(String);
            paragraph = false;
            row += 1;
            continue;
        }
        let styles = &mut result[row];
        let mut content = if comment { 0 } else { start };
        let mut block = false;
        if !comment && indent <= 3 {
            while line.get(content) == Some(&'>') {
                styles[content] = Macro;
                content += 1;
                if line.get(content) == Some(&' ') {
                    content += 1;
                }
                block = true;
            }
            let marker_end = if matches!(line.get(content), Some('-' | '+' | '*')) {
                content + 1
            } else {
                let digits = line[content..]
                    .iter()
                    .take_while(|c| c.is_ascii_digit())
                    .count();
                if (1..=9).contains(&digits)
                    && matches!(line.get(content + digits), Some('.' | ')'))
                {
                    content + digits + 1
                } else {
                    content
                }
            };
            if marker_end > content && line.get(marker_end).is_none_or(|c| c.is_whitespace()) {
                styles[content..marker_end].fill(Macro);
                content = marker_end;
                while line.get(content).is_some_and(|c| c.is_whitespace()) {
                    content += 1;
                }
                if line.get(content) == Some(&'[')
                    && matches!(line.get(content + 1), Some(' ' | 'x' | 'X'))
                    && line.get(content + 2) == Some(&']')
                {
                    styles[content..content + 3].fill(Constant);
                    content += 3;
                }
                block = true;
            }
            let hashes = line[content..].iter().take_while(|c| **c == '#').count();
            if (1..=6).contains(&hashes)
                && line.get(content + hashes).is_none_or(|c| c.is_whitespace())
            {
                styles[content..].fill(Heading);
                block = true;
            }
            if line.get(content) == Some(&'[')
                && let Some(end) = balanced(line, content, '[', ']')
                && line.get(end + 1) == Some(&':')
            {
                styles[content..end + 2].fill(Link);
                styles[end + 2..].fill(String);
                paragraph = false;
                row += 1;
                continue;
            }
        }
        inline(
            &line[content..],
            &mut styles[content..line.len()],
            &mut comment,
        );
        paragraph = !block && !line.iter().all(|c| c.is_whitespace()) && !comment;
        row += 1;
    }
    result
}

fn fence(line: &[char]) -> Option<(char, usize, String)> {
    let marker = *line.first()?;
    if !matches!(marker, '`' | '~') {
        return None;
    }
    let count = line.iter().take_while(|c| **c == marker).count();
    if count < 3 || (marker == '`' && line[count..].contains(&'`')) {
        return None;
    }
    let info: String = line[count..].iter().collect();
    Some((
        marker,
        count,
        info.split_whitespace()
            .next()
            .unwrap_or("")
            .to_ascii_lowercase(),
    ))
}

fn setext(line: &[char]) -> bool {
    let Some(marker @ ('=' | '-')) = line.first() else {
        return false;
    };
    let count = line.iter().take_while(|c| *c == marker).count();
    line[count..].iter().all(|c| c.is_whitespace())
}

fn thematic_break(line: &[char]) -> bool {
    let Some(marker @ ('-' | '*' | '_')) = line.first() else {
        return false;
    };
    line.iter().filter(|c| *c == marker).count() >= 3
        && line.iter().all(|c| c == marker || matches!(c, ' ' | '\t'))
}

fn balanced(line: &[char], start: usize, open: char, close: char) -> Option<usize> {
    let mut depth = 0;
    let mut i = start;
    while i < line.len() {
        if line[i] == '\\' {
            i += 2;
            continue;
        }
        if line[i] == open {
            depth += 1;
        } else if line[i] == close {
            depth -= 1;
            if depth == 0 {
                return Some(i);
            }
        }
        i += 1;
    }
    None
}

fn inline(line: &[char], styles: &mut [Style], comment: &mut bool) {
    use Style::*;
    let mut i = 0;
    while i < line.len() {
        if *comment || line[i..].starts_with(&['<', '!', '-', '-']) {
            let start = i;
            if !*comment {
                i += 4;
                *comment = true;
            }
            while i < line.len() {
                if line[i..].starts_with(&['-', '-', '>']) {
                    i += 3;
                    *comment = false;
                    break;
                }
                i += 1;
            }
            styles[start..i].fill(Comment);
            continue;
        }
        if line[i] == '\\' && line.get(i + 1).is_some_and(char::is_ascii_punctuation) {
            i += 2;
            continue;
        }
        if line[i] == '`' {
            let length = line[i..].iter().take_while(|c| **c == '`').count();
            let mut end = i + length;
            while end < line.len() {
                if line[end] == '`' {
                    let closing = line[end..].iter().take_while(|c| **c == '`').count();
                    if closing == length {
                        end += closing;
                        styles[i..end].fill(String);
                        break;
                    }
                    end += closing;
                } else {
                    end += 1;
                }
            }
            i = if end < line.len() || styles[i] == String {
                end
            } else {
                i + length
            };
            continue;
        }
        let bracket = i + usize::from(line[i] == '!' && line.get(i + 1) == Some(&'['));
        if line.get(bracket) == Some(&'[')
            && let Some(label_end) = balanced(line, bracket, '[', ']')
        {
            let destination = label_end + 1;
            let end = match line.get(destination) {
                Some('(') => balanced(line, destination, '(', ')'),
                Some('[') => balanced(line, destination, '[', ']'),
                _ => None,
            };
            if let Some(end) = end {
                styles[i..destination].fill(Link);
                styles[destination..=end].fill(String);
                i = end + 1;
                continue;
            }
        }
        if line[i] == '<'
            && let Some(length) = line[i + 1..].iter().position(|c| *c == '>')
        {
            let end = i + 1 + length;
            let target: std::string::String = line[i + 1..end].iter().collect();
            if !target.chars().any(char::is_whitespace)
                && (target.starts_with("https://")
                    || target.starts_with("http://")
                    || target.starts_with("mailto:")
                    || target.contains('@'))
            {
                styles[i..=end].fill(Link);
                i = end + 1;
                continue;
            }
        }
        if matches!(line[i], '*' | '_') {
            let marker = line[i];
            let length = line[i..].iter().take_while(|c| **c == marker).count();
            let start = i + length;
            if length <= 3
                && line.get(start).is_some_and(|c| !c.is_whitespace())
                && (marker != '_' || i == 0 || !line[i - 1].is_alphanumeric())
            {
                let mut end = start;
                while end < line.len() {
                    if line[end] == '\\' {
                        end += 2;
                        continue;
                    }
                    if line[end] == marker {
                        let closing = line[end..].iter().take_while(|c| **c == marker).count();
                        if closing == length
                            && end > start
                            && !line[end - 1].is_whitespace()
                            && (marker != '_'
                                || line.get(end + length).is_none_or(|c| !c.is_alphanumeric()))
                        {
                            styles[i..end + length].fill(if length == 1 {
                                Emphasis
                            } else {
                                Strong
                            });
                            i = end + length;
                            break;
                        }
                        end += closing;
                    } else {
                        end += 1;
                    }
                }
                if end < line.len() {
                    continue;
                }
            }
            i = start;
            continue;
        }
        i += 1;
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use Style::*;

    fn lex(text: &str) -> Vec<Vec<Style>> {
        highlight(&crate::buffer::Buffer::new(text).lines)
    }

    fn span(text: &str, needle: &str, style: Style) {
        let start = text[..text.find(needle).unwrap()].chars().count();
        assert!(
            lex(text)[0][start..start + needle.chars().count()]
                .iter()
                .all(|s| *s == style),
            "{needle}"
        );
    }

    #[test]
    fn extensions_headings_and_plain_text() {
        for extension in ["md", "MD", "markdown", "MarkDown"] {
            let lines = crate::buffer::Buffer::new("# Heading").lines;
            assert_eq!(
                super::super::for_path(std::path::Path::new(&format!("file.{extension}")), &lines),
                Some(vec![vec![Heading; 9]])
            );
        }
        let styles = lex(
            "# Title\n\nSubtitle\n===\n\n---\n####### plain\n#hashtag\nplain_text_here\nordinary prose",
        );
        for row in [0, 2, 3] {
            assert!(styles[row].iter().all(|s| *s == Heading));
        }
        assert!(styles[5].iter().all(|s| *s == Macro));
        for row in [6, 7, 8, 9] {
            assert!(styles[row].iter().all(|s| *s == Plain));
        }
        let styles = lex("text\n-\n\n* * *\n\n---");
        assert_eq!(styles[0], vec![Heading; 4]);
        assert_eq!(styles[1], vec![Heading]);
        assert!(styles[3].iter().all(|s| *s == Macro));
        assert!(styles[5].iter().all(|s| *s == Macro));
    }

    #[test]
    fn inline_markup_links_and_unicode_offsets() {
        let text = "界 *café* **bold** _italic_ __strong__ `**code**` ``a ` b`` [link](path_(one)) ![image](pic.png) [ref][id] <https://example.com>";
        for (needle, style) in [
            ("界", Plain),
            ("*café*", Emphasis),
            ("**bold**", Strong),
            ("_italic_", Emphasis),
            ("__strong__", Strong),
            ("`**code**`", String),
            ("``a ` b``", String),
            ("[link]", Link),
            ("(path_(one))", String),
            ("![image]", Link),
            ("[ref]", Link),
            ("[id]", String),
            ("<https://example.com>", Link),
        ] {
            span(text, needle, style);
        }
        for text in [
            "unmatched *markup",
            "escaped \\*literal*",
            "snake_case_word",
            "a_b_c",
            "[unclosed](link",
            "`unclosed",
            "text * spaced *",
        ] {
            assert!(lex(text)[0].iter().all(|s| *s == Plain), "{text}");
        }
        span("[id]: https://example.com", "[id]:", Link);
        span("[id]: https://example.com", "https://example.com", String);
    }

    #[test]
    fn lists_quotes_tasks_and_indented_code() {
        for (text, needle, style) in [
            ("- [x] done", "-", Macro),
            ("- [x] done", "[x]", Constant),
            ("12. item", "12.", Macro),
            ("> > # Title", ">", Macro),
            ("> > # Title", "# Title", Heading),
            ("    *code*", "*code*", String),
        ] {
            span(text, needle, style);
        }
        assert!(lex("1.5 and -not-a-list")[0].iter().all(|s| *s == Plain));
    }

    #[test]
    fn fenced_code_reuses_rust_and_go_multiline_lexers() {
        let styles = lex(
            "````rust\n/* comment\nstill */ fn main() {}\n```\n```` trailing\n~~~~\n`````\n# Heading\n~~~go\npackage main\ns := `raw\nfunc`\n~~~\nordinary prose",
        );
        assert!(styles[0].iter().all(|s| *s == Macro));
        assert!(styles[1].iter().all(|s| *s == Comment));
        assert_eq!(styles[2][9], Keyword);
        assert!(styles[6].iter().all(|s| *s == Macro));
        assert!(styles[7].iter().all(|s| *s == Heading));
        assert_eq!(styles[9][0], Keyword);
        assert!(styles[11].iter().all(|s| *s == String));
        assert!(styles[13].iter().all(|s| *s == Plain));
    }

    #[test]
    fn unknown_and_unclosed_fences_hide_markdown_markup() {
        let text = "  ~~~python\r\n# code\r\n**code**\r\n  ~~~~\r\n# Heading\r\n```\r\n*code*";
        let styles = lex(text);
        for row in [1, 2, 6] {
            assert!(styles[row].iter().all(|s| *s == String));
        }
        for row in [0, 3, 5] {
            assert!(styles[row].iter().all(|s| *s == Macro));
        }
        assert_eq!(styles[4][0], Heading);
        assert!(lex("```bad`info")[0].iter().all(|s| *s == Plain));
    }

    #[test]
    fn html_comments_continue_across_lines_and_resume_inline_markup() {
        let styles = lex(
            "<!-- # hidden\n   **hidden**\n--> *visible*\n`<!--` **visible**\ntext <!-- hidden --> text",
        );
        for row in [0, 1] {
            assert!(styles[row].iter().all(|s| *s == Comment));
        }
        assert!(styles[2][..3].iter().all(|s| *s == Comment));
        assert!(styles[2][4..].iter().all(|s| *s == Emphasis));
        assert!(styles[3][..6].iter().all(|s| *s == String));
        assert!(styles[3][7..].iter().all(|s| *s == Strong));
        assert!(styles[4][5..20].iter().all(|s| *s == Comment));
        assert_eq!(styles[4][21], Plain);
    }
}
