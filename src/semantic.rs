//! Decode LSP relative UTF-16 tokens into theme-independent, character-indexed spans.
use crate::syntax::Style;
use serde::Deserialize;

pub const TYPES: &[&str] = &[
    "namespace",
    "type",
    "class",
    "enum",
    "interface",
    "struct",
    "typeParameter",
    "parameter",
    "variable",
    "property",
    "enumMember",
    "event",
    "function",
    "method",
    "macro",
    "keyword",
    "modifier",
    "comment",
    "string",
    "number",
    "regexp",
    "operator",
    "decorator",
    "lifetime",
    "attribute",
    "boolean",
    "builtinType",
    "typeAlias",
    "derive",
    "deriveHelper",
    "constParameter",
    "selfKeyword",
    "selfTypeKeyword",
    "escapeSequence",
    "formatSpecifier",
];
pub const MODIFIERS: &[&str] = &[
    "declaration",
    "definition",
    "readonly",
    "static",
    "deprecated",
    "abstract",
    "async",
    "modification",
    "documentation",
    "defaultLibrary",
];
#[derive(Clone, Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Legend {
    pub token_types: Vec<String>,
    pub token_modifiers: Vec<String>,
}
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Token {
    pub row: usize,
    pub start: usize,
    pub end: usize,
    pub style: Style,
}
fn style(kind: &str, readonly: bool) -> Option<Style> {
    use Style::*;
    Some(match kind {
        "namespace" => Namespace,
        "type" | "class" | "enum" | "interface" | "struct" | "typeParameter" | "builtinType"
        | "typeAlias" | "selfTypeKeyword" => Type,
        "function" | "method" => Function,
        "variable" | "parameter" | "property" if readonly => Constant,
        "variable" | "parameter" => Variable,
        "property" | "event" => Property,
        "enumMember" | "constParameter" | "boolean" => Constant,
        "macro" | "decorator" | "attribute" | "derive" | "deriveHelper" => Macro,
        "keyword" | "modifier" | "selfKeyword" => Keyword,
        "comment" => Comment,
        "string" | "regexp" | "escapeSequence" | "formatSpecifier" => String,
        "number" => Number,
        "operator" => Punctuation,
        "lifetime" => Lifetime,
        _ => return None,
    })
}

pub fn decode(data: &[u32], legend: &Legend, lines: &[Vec<char>]) -> Option<Vec<Token>> {
    if !data.len().is_multiple_of(5) {
        return None;
    }
    let mut tokens = Vec::with_capacity(data.len() / 5);
    let mut row = 0usize;
    let mut start = 0usize;
    let mut previous_end = 0;
    let mut offsets = Vec::new();
    let mut cached_row = None;
    let readonly = legend
        .token_modifiers
        .iter()
        .position(|s| s == "readonly")
        .filter(|i| *i < 32);
    for chunk in data.chunks_exact(5) {
        row = row.checked_add(chunk[0] as usize)?;
        start = if chunk[0] == 0 {
            start.checked_add(chunk[1] as usize)?
        } else {
            chunk[1] as usize
        };
        let line = lines.get(row)?;
        if cached_row != Some(row) {
            offsets.clear();
            offsets.push(0usize);
            let length =
                line.len() - usize::from(row + 1 < lines.len() && line.last() == Some(&'\r'));
            for c in line.iter().take(length) {
                offsets.push(offsets.last()?.checked_add(c.len_utf16())?);
            }
            cached_row = Some(row);
            previous_end = 0;
        }
        if chunk[2] == 0 || start < previous_end || chunk[3] >= 65536 {
            return None;
        }
        let end = start.checked_add(chunk[2] as usize)?.min(*offsets.last()?);
        let scalar_start = offsets.binary_search(&start).ok()?;
        let scalar_end = offsets.binary_search(&end).ok()?;
        if scalar_end <= scalar_start {
            return None;
        }
        let kind = legend.token_types.get(chunk[3] as usize)?;
        let is_readonly = readonly.is_some_and(|i| chunk[4] & (1 << i) != 0);
        if let Some(style) = style(kind, is_readonly) {
            tokens.push(Token {
                row,
                start: scalar_start,
                end: scalar_end,
                style,
            });
        }
        previous_end = end;
    }
    Some(tokens)
}

#[cfg(test)]
mod tests {
    use super::*;
    fn legend() -> Legend {
        Legend {
            token_types: ["function", "variable", "type", "future-token", "comment"]
                .map(str::to_owned)
                .into(),
            token_modifiers: vec!["readonly".into()],
        }
    }
    #[test]
    fn decodes_relative_utf16_spans_and_modifiers_using_the_server_legend() {
        let lines = crate::buffer::Buffer::new("😀name value\r\nType unknown\n// 😀").lines;
        let tokens = decode(
            &[
                0, 2, 4, 0, 0, 0, 5, 5, 1, 1, 1, 0, 4, 2, 0, 0, 5, 7, 3, 0, 1, 0, 999, 4, 0,
            ],
            &legend(),
            &lines,
        )
        .unwrap();
        assert_eq!(
            tokens,
            vec![
                Token {
                    row: 0,
                    start: 1,
                    end: 5,
                    style: Style::Function
                },
                Token {
                    row: 0,
                    start: 6,
                    end: 11,
                    style: Style::Constant
                },
                Token {
                    row: 1,
                    start: 0,
                    end: 4,
                    style: Style::Type
                },
                Token {
                    row: 2,
                    start: 0,
                    end: 4,
                    style: Style::Comment
                },
            ]
        );
        assert_eq!(decode(&[], &legend(), &lines), Some(vec![]));
    }
    #[test]
    fn rejects_malformed_overlapping_and_split_surrogate_tokens() {
        let lines = crate::buffer::Buffer::new("a😀b\nsecond").lines;
        for data in [
            vec![0],
            vec![0, 0, 0, 0, 0],
            vec![0, 2, 1, 0, 0],
            vec![0, 1, 1, 0, 0],
            vec![2, 0, 1, 0, 0],
            vec![0, 9, 1, 0, 0],
            vec![0, 0, 1, 99, 0],
            vec![0, 0, 1, 65536, 0],
            vec![0, 0, 3, 0, 0, 0, 1, 1, 0, 0],
            vec![0, 0, 1, 0, 0, u32::MAX, u32::MAX, u32::MAX, 0, 0],
        ] {
            assert!(decode(&data, &legend(), &lines).is_none(), "{data:?}");
        }
    }
    #[test]
    fn token_classes_preserve_unknown_fallback_and_map_supported_categories() {
        use Style::*;
        for (kind, expected) in [
            ("struct", Type),
            ("method", Function),
            ("parameter", Variable),
            ("property", Property),
            ("enumMember", Constant),
            ("namespace", Namespace),
            ("macro", Macro),
            ("keyword", Keyword),
            ("string", String),
            ("number", Number),
            ("lifetime", Lifetime),
            ("operator", Punctuation),
        ] {
            assert_eq!(style(kind, false), Some(expected));
        }
        assert_eq!(style("variable", true), Some(Constant));
        assert_eq!(style("unknown", false), None);
    }
}
