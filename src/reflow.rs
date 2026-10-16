//! Source-preserving paragraph filling. The parser supplies block and inline boundaries.
use pulldown_cmark::{Event, Options, Parser, Tag, TagEnd};
use unicode_width::UnicodeWidthStr;

pub fn paragraph(text: &str, cursor: usize, width: usize) -> Option<(usize, usize, String)> {
    let events: Vec<_> = Parser::new_ext(text, Options::ENABLE_TABLES | Options::ENABLE_TASKLISTS)
        .into_offset_iter()
        .collect();
    // Tight lists omit Paragraph events; their initial inline run is still a paragraph.
    let mut expanded = Vec::new();
    let mut i = 0;
    while i < events.len() {
        expanded.push(events[i].clone());
        if matches!(events[i].0, Event::Start(Tag::Item)) {
            let begin = i + 1;
            let mut end = begin;
            while end < events.len()
                && matches!(
                    events[end].0,
                    Event::Text(_)
                        | Event::Code(_)
                        | Event::SoftBreak
                        | Event::HardBreak
                        | Event::InlineHtml(_)
                        | Event::FootnoteReference(_)
                        | Event::TaskListMarker(_)
                        | Event::Start(
                            Tag::Emphasis
                                | Tag::Strong
                                | Tag::Strikethrough
                                | Tag::Link { .. }
                                | Tag::Image { .. }
                        )
                        | Event::End(
                            TagEnd::Emphasis
                                | TagEnd::Strong
                                | TagEnd::Strikethrough
                                | TagEnd::Link
                                | TagEnd::Image
                        )
                )
            {
                end += 1;
            }
            if begin < end {
                let range = events[begin].1.start
                    ..events[begin..end].iter().map(|(_, r)| r.end).max().unwrap();
                expanded.push((Event::Start(Tag::Paragraph), range.clone()));
                expanded.extend_from_slice(&events[begin..end]);
                expanded.push((Event::End(TagEnd::Paragraph), range));
                i = end;
                continue;
            }
        }
        i += 1;
    }
    let events = expanded;
    for (index, (event, range)) in events.iter().enumerate() {
        let line_start = text[..range.start].rfind('\n').map_or(0, |i| i + 1);
        if !matches!(event, Event::Start(Tag::Paragraph))
            || !(line_start..range.end).contains(&cursor)
        {
            continue;
        }
        let inline: Vec<_> = events[index + 1..]
            .iter()
            .take_while(|(event, _)| !matches!(event, Event::End(TagEnd::Paragraph)))
            .collect();
        if inline
            .iter()
            .any(|(event, _)| matches!(event, Event::HardBreak | Event::InlineHtml(_)))
        {
            return None;
        }
        let start = text[..range.start].rfind('\n').map_or(0, |i| i + 1);
        let first_prefix = &text[start..range.start];
        // Preserve quote markers and turn list markers into continuation indentation.
        let mut continuation = String::new();
        for c in first_prefix.chars() {
            continuation.push(if c == '>' || c.is_whitespace() {
                c
            } else {
                ' '
            });
        }
        let end = range.end - usize::from(text[..range.end].ends_with('\n'));
        let end = end - usize::from(text[..end].ends_with('\r'));
        let source = &text[range.start..end];
        let mut joined = String::new();
        for (n, line) in source.split('\n').enumerate() {
            let line = line.strip_suffix('\r').unwrap_or(line);
            let line = if n == 0 {
                line
            } else {
                line.strip_prefix(&continuation)?
            };
            if n > 0 {
                joined.push(' ');
            }
            joined.push_str(line);
        }
        // Treat balanced inline constructs as indivisible spans, retaining their whitespace.
        let mut tokens = Vec::new();
        let mut begin = 0;
        let mut brackets = 0usize;
        let mut ticks = 0usize;
        let mut escaped = false;
        let chars: Vec<_> = joined.char_indices().collect();
        let mut i = 0;
        while i < chars.len() {
            let (offset, c) = chars[i];
            if escaped {
                escaped = false;
                i += 1;
                continue;
            }
            if c == '\\' {
                escaped = true;
                i += 1;
                continue;
            }
            if c == '`' {
                let count = chars[i..].iter().take_while(|(_, c)| *c == '`').count();
                if ticks == 0 {
                    ticks = count;
                } else if ticks == count {
                    ticks = 0;
                }
                i += count;
                continue;
            }
            if ticks == 0 {
                if matches!(c, '[' | '(' | '<') {
                    brackets += 1;
                }
                if matches!(c, ']' | ')' | '>') {
                    brackets = brackets.saturating_sub(1);
                }
                if c.is_whitespace() && brackets == 0 {
                    if begin < offset {
                        tokens.push(&joined[begin..offset]);
                    }
                    begin = offset + c.len_utf8();
                }
            }
            i += 1;
        }
        if ticks != 0 || brackets != 0 {
            return None;
        }
        if begin < joined.len() {
            tokens.push(&joined[begin..]);
        }
        let newline = if text[start..range.end].contains("\r\n") {
            "\r\n"
        } else {
            "\n"
        };
        let mut output = first_prefix.to_string();
        let mut used = UnicodeWidthStr::width(first_prefix);
        let mut has_word = false;
        for token in tokens {
            let size = UnicodeWidthStr::width(token);
            // Do not introduce a Markdown block marker at the beginning of a source line.
            let safe_start = !token.starts_with(['#', '>', '-', '+', '*', '`', '~', '['])
                && !token.chars().next().is_some_and(|c| c.is_ascii_digit());
            if has_word && used + 1 + size > width && safe_start {
                output.push_str(newline);
                output.push_str(&continuation);
                used = UnicodeWidthStr::width(continuation.as_str());
                has_word = false;
            }
            if has_word {
                output.push(' ');
                used += 1;
            }
            output.push_str(token);
            used += size;
            has_word = true;
        }
        // Refuse transformations that change Markdown rendering (apart from whitespace).
        // This also catches ambiguous lazy continuations and nested container prefixes.
        let candidate = format!("{}{}{}", &text[..start], output, &text[end..]);
        let render = |source: &str| {
            let mut html = String::new();
            pulldown_cmark::html::push_html(
                &mut html,
                Parser::new_ext(source, Options::ENABLE_TABLES | Options::ENABLE_TASKLISTS),
            );
            html.split_whitespace().collect::<Vec<_>>().join(" ")
        };
        if render(text) != render(&candidate) {
            return None;
        }
        return Some((start, end, output));
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;
    fn fill(s: &str, cursor: usize, width: usize) -> String {
        let Some((start, end, replacement)) = paragraph(s, cursor, width) else {
            return s.into();
        };
        format!("{}{}{}", &s[..start], replacement, &s[end..])
    }
    #[test]
    fn wraps_only_current_paragraph() {
        assert_eq!(
            fill("one two three four\n\nother paragraph", 4, 10),
            "one two\nthree four\n\nother paragraph"
        );
        assert_eq!(fill("one two\nthree four\n", 9, 80), "one two three four\n");
    }
    #[test]
    fn protects_markdown_blocks_and_breaks() {
        for s in [
            "# one two three",
            "title here\n----------",
            "```\none two three\n```",
            "    one two three",
            "| one | two |\n| --- | --- |\n| three | four |",
            "one two  \nthree four",
            "one two\\\nthree four",
            "<div>\none two three\n</div>",
        ] {
            for cursor in 0..s.len() {
                assert_eq!(fill(s, cursor, 5), s, "{s}");
            }
        }
    }
    #[test]
    fn containers_and_inline_syntax() {
        assert_eq!(
            fill("- one two three four\n- next item", 5, 12),
            "- one two\n  three four\n- next item"
        );
        assert_eq!(
            fill("> one two three four", 5, 12),
            "> one two\n> three four"
        );
        assert_eq!(
            fill("  - one two three four", 7, 14),
            "  - one two\n    three four"
        );
        for atom in [
            "`one  two`",
            "[one two](https://example.com/a)",
            "![one two](image.png)",
            "``one ` two``",
        ] {
            let source = format!("before {atom} after words");
            let result = fill(&source, 1, 8);
            assert!(result.contains(atom), "{result}");
        }
    }
    #[test]
    fn nested_lists_quotes_tasks_and_protected_neighbors() {
        for (source, cursor, expected) in [
            (
                "- parent\n  - one two three four\n  - next",
                9,
                "- parent\n  - one two\n    three four\n  - next",
            ),
            (
                "12. one two three four\n13. next",
                0,
                "12. one two\n    three four\n13. next",
            ),
            ("> - one two three four", 0, "> - one two\n>   three four"),
            ("- [x] one two three four", 0, "- [x] one two\n  three four"),
        ] {
            let output = fill(source, cursor, 14);
            assert_eq!(output, expected);
            assert_eq!(fill(&output, cursor, 14), output);
        }
        for source in [
            "- ```\n  one two three four\n  ```",
            "> ```\n> one two three four\n> ```",
            "[link]: https://example.com/somewhere",
            "- | one | two |\n  | --- | --- |\n  | three | four |",
        ] {
            for cursor in 0..source.len() {
                assert_eq!(fill(source, cursor, 5), source);
            }
        }
        assert_eq!(
            fill("one two three four\n\n```\ncode here\n```", 0, 10),
            "one two\nthree four\n\n```\ncode here\n```"
        );
    }
    #[test]
    fn unicode_crlf_and_idempotence() {
        let s = "世界 世界 words more\r\n\r\nother\r\n";
        let result = fill(s, 0, 10);
        assert_eq!(result, "世界 世界\r\nwords more\r\n\r\nother\r\n");
        assert_eq!(fill(&result, 0, 10), result);
    }
}
