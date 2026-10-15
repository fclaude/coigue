//! Lightweight Rust lexer. Character-indexed styles match the editor buffer.
//! Relex from the beginning after edits so multiline state stays correct.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Style {
    Plain,
    Keyword,
    Comment,
    String,
    Number,
    Macro,
    Punctuation,
    Lifetime,
}

pub fn rust(lines: &[Vec<char>]) -> Vec<Vec<Style>> {
    use Style::*;
    let mut depth = 0usize;
    let mut quoted = false;
    let mut escaped = false;
    let mut raw: Option<usize> = None;
    let mut result = Vec::with_capacity(lines.len());
    for line in lines {
        let mut styles = vec![Plain; line.len()];
        let mut i = 0;
        while i < line.len() {
            let start = i;
            let c = line[i];
            let next = line.get(i + 1).copied();
            let style;
            if depth > 0 {
                style = Comment;
                if c == '/' && next == Some('*') {
                    depth += 1;
                    i += 2;
                } else if c == '*' && next == Some('/') {
                    depth -= 1;
                    i += 2;
                } else {
                    i += 1;
                }
            } else if let Some(hashes) = raw {
                style = String;
                if c == '"' && (0..hashes).all(|n| line.get(i + 1 + n) == Some(&'#')) {
                    i += 1 + hashes;
                    raw = None;
                } else {
                    i += 1;
                }
            } else if quoted {
                style = String;
                if escaped {
                    escaped = false;
                } else if c == '\\' {
                    escaped = true;
                } else if c == '"' {
                    quoted = false;
                }
                i += 1;
            } else if c == '/' && next == Some('/') {
                style = Comment;
                i = line.len();
            } else if c == '/' && next == Some('*') {
                style = Comment;
                depth = 1;
                i += 2;
            } else if let Some((end, hashes)) = raw_start(line, i) {
                style = String;
                raw = Some(hashes);
                i = end;
            } else if c == '"' || ((c == 'b' || c == 'c') && next == Some('"')) {
                style = String;
                quoted = true;
                i += if c == '"' { 1 } else { 2 };
            } else if let Some(end) = char_end(line, i) {
                style = String;
                i = end;
            } else if c == '\'' && next.is_some_and(ident_start) {
                style = Lifetime;
                i += 2;
                while i < line.len() && ident(line[i]) {
                    i += 1;
                }
            } else if c.is_ascii_digit() {
                style = Number;
                i += 1;
                while i < line.len() {
                    let n = line[i];
                    if n.is_ascii_alphanumeric()
                        || n == '_'
                        || (n == '.' && line.get(i + 1).is_some_and(char::is_ascii_digit))
                        || ((n == '+' || n == '-') && matches!(line.get(i - 1), Some('e' | 'E')))
                    {
                        i += 1;
                    } else {
                        break;
                    }
                }
            } else if ident_start(c) {
                i += 1;
                while i < line.len() && ident(line[i]) {
                    i += 1;
                }
                let word: std::string::String = line[start..i].iter().collect();
                // Raw identifiers deliberately bypass keyword styling.
                if word == "r"
                    && line.get(i) == Some(&'#')
                    && line.get(i + 1).is_some_and(|c| ident_start(*c))
                {
                    i += 2;
                    while i < line.len() && ident(line[i]) {
                        i += 1;
                    }
                    style = Plain;
                } else if line.get(i) == Some(&'!') && line.get(i + 1) != Some(&'=') {
                    i += 1;
                    style = Macro;
                } else if KEYWORDS.contains(&word.as_str()) {
                    style = Keyword;
                } else {
                    style = Plain;
                }
            } else {
                style = if c.is_ascii_punctuation() {
                    Punctuation
                } else {
                    Plain
                };
                i += 1;
            }
            styles[start..i].fill(style);
        }
        // A backslash at end of line escapes the newline, not the next character.
        escaped = false;
        result.push(styles);
    }
    result
}
fn ident_start(c: char) -> bool {
    c == '_' || c.is_alphabetic()
}
fn ident(c: char) -> bool {
    ident_start(c) || c.is_numeric()
}
fn raw_start(line: &[char], i: usize) -> Option<(usize, usize)> {
    let mut j = i;
    if matches!(line.get(j), Some('b' | 'c')) {
        j += 1;
    }
    if line.get(j) != Some(&'r') {
        return None;
    }
    j += 1;
    let start = j;
    while line.get(j) == Some(&'#') {
        j += 1;
    }
    (line.get(j) == Some(&'"')).then_some((j + 1, j - start))
}
fn char_end(line: &[char], i: usize) -> Option<usize> {
    let mut j = i;
    if line.get(j) == Some(&'b') {
        j += 1;
    }
    if line.get(j) != Some(&'\'') {
        return None;
    }
    j += 1;
    if line.get(j) == Some(&'\\') {
        j += 1;
        match line.get(j)? {
            'u' if line.get(j + 1) == Some(&'{') => {
                j += 2;
                while line
                    .get(j)
                    .is_some_and(|c| c.is_ascii_hexdigit() || *c == '_')
                {
                    j += 1;
                }
                if line.get(j) != Some(&'}') {
                    return None;
                }
                j += 1;
            }
            'x' => {
                j += 1;
                for _ in 0..2 {
                    if !line.get(j)?.is_ascii_hexdigit() {
                        return None;
                    }
                    j += 1;
                }
            }
            _ => j += 1,
        }
    } else {
        if matches!(line.get(j), None | Some('\'')) {
            return None;
        }
        j += 1;
    }
    (line.get(j) == Some(&'\'')).then_some(j + 1)
}
const KEYWORDS: &[&str] = &[
    "as", "async", "await", "break", "const", "continue", "crate", "dyn", "else", "enum", "extern",
    "false", "fn", "for", "if", "impl", "in", "let", "loop", "match", "mod", "move", "mut", "pub",
    "ref", "return", "self", "Self", "static", "struct", "super", "trait", "true", "type",
    "unsafe", "use", "where", "while", "abstract", "become", "box", "do", "final", "macro",
    "override", "priv", "try", "typeof", "unsized", "virtual", "yield", "gen",
];

#[cfg(test)]
mod tests {
    use super::{Style::*, *};
    fn lex(s: &str) -> Vec<Vec<Style>> {
        rust(
            &s.split('\n')
                .map(|l| l.chars().collect())
                .collect::<Vec<_>>(),
        )
    }
    #[test]
    fn multiline_nested_comments_and_edit() {
        let s = "/* outer\n/* nested */ still\n*/ fn main() {}";
        let styles = lex(s);
        assert!(styles[0].iter().all(|s| *s == Comment));
        assert!(styles[1].iter().all(|s| *s == Comment));
        assert_eq!(styles[2][3], Keyword);
        let edited = lex(&s.replacen("/* outer", "// outer", 1));
        assert_eq!(edited[1][13], Plain);
    }
    #[test]
    fn quotes_raw_strings_and_escapes() {
        let styles = lex("br##\"a\n\"# /*\n\"## fn\n\"a\\\"b\nend\" let");
        assert!(styles[1].iter().all(|s| *s == String));
        assert_eq!(styles[2][4], Keyword);
        assert!(styles[3].iter().all(|s| *s == String));
        assert_eq!(styles[4][5], Keyword);
        assert_eq!(lex("c\"hello\"")[0], vec![String; 8]);
    }
    #[test]
    fn characters_lifetimes_unicode_and_numbers() {
        let s = "'a 'static '界' '\\n' b'X' '\\u{1f980}' 0xff_u8 1.2e-3 println! r#type";
        let styles = lex(s);
        for (needle, expected) in [
            ("'a", Lifetime),
            ("'static", Lifetime),
            ("'界'", String),
            ("'\\n'", String),
            ("b'X'", String),
            ("'\\u{1f980}'", String),
            ("0xff_u8", Number),
            ("1.2e-3", Number),
            ("println!", Macro),
            ("r#type", Plain),
        ] {
            let start = s[..s.find(needle).unwrap()].chars().count();
            assert!(
                styles[0][start..start + needle.chars().count()]
                    .iter()
                    .all(|s| *s == expected),
                "{needle}"
            );
        }
        assert_eq!(lex("let café = 1..2;")[0][0], Keyword);
        assert_eq!(lex("let café = 1..2;")[0][12], Punctuation);
    }
}
