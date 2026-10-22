#!/usr/bin/env python3
"""Regenerates testdata/golden.jsonl from the real Rust lexers.

    python3 internal/syntax/testdata/regenerate.py            # rewrite golden.jsonl
    python3 internal/syntax/testdata/regenerate.py --full DIR # also write DIR/corpus.txt and DIR/rust_out.txt

The Rust side is src/syntax.rs and src/syntax/markdown.rs themselves: a scratch
cargo project in a temporary directory symlinks them next to a 60-line driver
that reads cases, lexes them and prints one letter per character (the letters
are the ones of styleCodes in syntax_test.go).  Nothing in the repository is
written except golden.jsonl.  Needs cargo and `cargo build --offline` must work
(the driver has no dependencies).

The corpus is hand-written snippets for each lexer (run through the other two
lexers as well, "cross"), their CRLF forms, and seeded random documents built
from per-language token alphabets.  golden.jsonl keeps a deterministic sample
of a few hundred cases; --full writes all of them (about 37,000 cases, 110,000
lines), with the hex-encoded texts and the Rust output, one case per line, for
a differential run against the Go lexers.
"""
import argparse
import json
import os
import pathlib
import random
import subprocess
import sys
import tempfile

RUST = [
    # comments
    "// line comment", "let x = 1; // trailing", "/* block */ fn", "/* a /* b */ c */ fn main() {}",
    "/* unterminated", "/* a\n/* b\n*/ c\n*/ fn x() {}", "/*/ still comment */ let", "/**/ let", "/***/ let", "/* */ */ let",
    "//", "///doc comment", "//! inner doc", "/ / not a comment", "a /* b */ c /* d */ e", "/*\n*/\n/*\n*/",
    "/* // not line */ let x", "// /* not block\nlet y", "/* \" quote */ let z = \"s\";", "/* ' */ 'a",
    "/* /* /* deep */ */ */ end", "/* a */ */ b", "*/ stray", "/*/*/ x */ */ y",
    # strings
    '"abc"', '"a\\"b"', '"unterminated', '"line one\nline two"', '"back\\\nslash newline"', '"a\\\\" x', '"\\\\\\"" y',
    'b"bytes"', 'c"cstr"', 'b"x\\n"', 'b"x', 'c"x', 'B"upper"', 'C"upper"', 'bb"x"', 'cc"x"',
    'r"raw"', 'r#"raw"#', 'r##"a"#b"##', 'r#"unterminated', 'r"', 'r#', 'r#"', 'r##', 'r"a\\"', 'r#"a"b"#',
    'br"raw"', 'br#"x"y"#', 'cr"x"', 'cr#"x"#', 'rb"x"', 'br', 'b r"x"', 'r "x"', 'br##"a"#"##',
    'r#"multi\nline\n"# let', 'r##"multi\n"#\n"## let', 'br##"a\n"# /*\n"## fn\n"a\\"b\nend" let',
    'r#"#"#', 'r#""#', 'r""', 'r#"a"# r#"b"#', 'r#"a"#r#"b"#', 'abr"x"', 'foo_r"x"', '_r#"x"#',
    '"#', '"##', '"" ""', '"é界"', '"\\u{1f980}"', 'let s = "// not comment"; // comment', 'let s = "/* not */";',
    '"a" "b" \'c\'', '"tab\there"', '"\\\\n"', '"\\', '"\\\n"', 'x = "a\\\n  b"; let',
    '"a\\\n" let x', '"a\\\n\\" let x', 'b"a\\\n" let x', '"a\\\n\n" let x', '"a\\\\\n" let x', '"\\\n\\\n" fn', '"x\\\n"\nlet y', 'let s = "a\\\n"; let t = 1;',
    "r\"a\\\n\" let x", 'r#"a\\\n"# let x', "'\\\n' x",
    # NUL characters, which slice::get never confuses with the end of a line
    "\x00", "a\x00b", "'\x00'", "'\\\x00'", "'\x00", "'\\\x00", "\x00!", "a\x00!", "'\\x\x00", "'\\u{\x00}'", "1.\x00", "1e\x00", "r#\x00", "r\x00#a", "\"\x00\"", "/* \x00 */ fn", "1\x00",
    # chars and lifetimes
    "'a'", "'\\n'", "'\\''", "'\\\\'", "'\\x41'", "'\\xZZ'", "'\\x4'", "'\\x'", "'\\u{1f980}'", "'\\u{}'", "'\\u{zz}'", "'\\u1234'",
    "'界'", "''", "'ab'", "b'\\n'", "b''", "b'x'", "b'ab'", "'\\", "'a", "'static", "'_", "'_a", "'界", "'1", "'1'", "'\\u{1_F980}'", "'\\u{1f980",
    "'\\u{1f980}x", "'\\u{ff}'", "'\\u'", "'\\a'", "'\\0'", "' '", "'\t'", "'\\t'", "'\"'", "'\\\"'",
    "fn f<'a>(x: &'a str) -> &'a str { x }", "impl<'a, 'b: 'a> T<'a> for U<'b> {}", "where T: 'static + Send", "'a: loop { break 'a; }",
    "let c = 'x'; let l: &'x str;", "'a'b'c'", "'a' 'b", "''a''", "'\\'a'", "&'a", "&'static str", "<'_>", "'é'", "'é", "'éa'", "'ñ", "'Ⅷ", "'ª", "'²", "'a²",
    "'\U0001f980'", "'\U0001f980", "'e\u0301'", "'\u0301'", "b'\u00e9'",
    # numbers
    "0", "00", "0x1F", "0xff_u8", "0b1010_1010", "0o77", "1_000_000", "1.5", "1.", "1..2", "1.e3", "1e10", "1E+10", "1e-10", "1.2e-3f64",
    "0xE+1", "0xe-1", "1.0.0", "1.a", "1.5.method()", "x.0.1", "1 .. 2", "3u8", "1usize", "9é", "1_", "1__", "١٢٣", "²", "1²", "1e", "1e+", "1+2", "1-2",
    "a-1", "x = 1.0e-5;", "0.1+0.2", "1.5E+3", "12e", "1e+e-1", "0b1e+1", "1...2", "1..=2", "5..", "..5", ".5", "1._", "1.e", "1.5e", "0xfeedface", "007", "1u8.pow(2)",
    "123abc456", "1\u00b2", "1\u0663", "1\uff11", "\uff11", "1.\uff11",
    # identifiers and keywords
    "r#type", "r#fn", "r#", "r# x", "r#1", "r#é", "r#r#a", "rr#a", "r#type!", "r#a.b", "r#_x", "r#struct r#enum",
    "fn", "Self", "self", "gen", "try", "yield", "abstract", "let café = 1..2;", "_", "__", "_x", "x_", "ñandú", "界", "Ⅷ", "ª", "e\u0301", "e\u0301a",
    "let \u00e9 = 1", "let x\u0301 = 2", "\u0301", "\u200d", "a\u200db", "\U0001f980", "x\U0001f980", "let \U0001f980 = 1;", "𝒳 = 1", "𝒳𝒴", "ǅ", "ᾼ", "Σ",
    "async move || await", "unsafe impl", "pub(crate) struct", "dyn Trait", "enum E { A, B }", "match x { _ => 1 }", "mut ref", "loop {}", "for i in 0..n {}",
    "keyword_prefix", "fnord", "letter", "iffy", "As", "SELF", "self_", "ifx", "x_if",
    # macros
    "println!(\"x\")", "vec![1, 2]", "a != b", "a!=b", "a ! = b", "x !", "foo ! bar", "r!", "if!x", "matches!(x, A | B)", "macro_rules! m { () => {} }",
    "a!", "a!\n", "a!=", "a! =", "a!!", "!a", "foo!{}", "foo!()", "foo::bar!()", "x!== y", "é!()", "界!x", "r#a!()", "try!(x)", "self!", "r!=1", "b!=c",
    # punctuation
    "+-*/%^&|!<>=", "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", "#[derive(Debug)]", "#![allow(dead_code)]", "a::<B>()", "x => y", "-> T", "..=", "?", "@", "$x", "\\", "`", "~",
    "{}[]()", "<>", ",;:", "a.b.c", "&&||", "<<=>>=",
    # whitespace / unicode
    "\tlet\tx", "let x = 1;\r", "// c\r", "\"s\r\nt\"", "let\u3000x", "let\u00a0x", "let\u0085x", "a\u2028b", " ", "", "\r", "\t", "   ",
    # realistic
    "fn main() {\n    let v = vec![1, 2, 3];\n    for x in &v {\n        println!(\"{x}\");\n    }\n}",
    "#[derive(Debug, Clone)]\npub struct P<'a> {\n    name: &'a str, // name\n    n: u32,\n}",
    "impl<T: Display> fmt::Display for W<T> {\n    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {\n        write!(f, \"{}\", self.0)\n    }\n}",
    "/// Doc\n/// more\npub fn f(x: i32) -> Option<i32> {\n    let r#match = x.checked_add(1)?;\n    Some(r#match)\n}",
    "const A: &str = r#\"raw \"quoted\" string\"#;\nstatic B: [u8; 3] = *b\"abc\";\nlet c = b'\\n' as char;",
    "let x = 0xFF_u8 as f64 * 1.5e-3 + 2.;\nlet y = (1..=10).map(|i| i * 2).sum::<i32>();",
    "match opt {\n    Some(n) if n > 0 => n,\n    Some(_) | None => 0,\n}",
    "async fn g() -> Result<(), Box<dyn Error>> {\n    let s = c\"hi\";\n    Ok(())\n}",
    "/* top\n   /* inner */\n   still */\nfn after() {}",
    "let s = \"multi\n    line // not comment\n    string\";\nlet t = 1;",
    "// unicode: café 界 🦀\nlet 変数 = \"値\";",
    "let a = 'a'; let b = '\\u{41}'; let c = '\\x41'; let d = &'a x;",
    "struct S;\nimpl S { const N: usize = 1_0; }\ntype T = [u8; S::N];",
    "unsafe extern \"C\" { fn abs(x: i32) -> i32; }",
    "mod m { pub use super::*; }",
    "macro_rules! m {\n    ($x:expr) => { $x + 1 };\n}\nm!(2);",
    "x = if a { 'b' } else { 'c' };",
    "let t = (1, 2.0, 'c', \"s\", b's', b\"bs\", r\"raw\", c\"cs\");",
]

GO = [
    "\x00", "a\x00b", ".\x00", "'\x00'", "'\\\x00'", "\x00if", "if\x00", "\"\x00\"", "`\x00`", "1.\x00", "1e\x00", "0x\x00",
    "// c", "x := 1 // t", "/* b */ func", "/* a\nstill */ func main() {}", "/* unterminated", "/*/ x */ var", "/**/ var", "/* /* */ */ var", "*/ stray",
    "//", "/ /", "/* // */ x", "// /* \nx", "a /* b */ c /* d */ e",
    "`raw`", "`unterminated", "`multi\nline`", "`a` + `b`", "`// not comment`", "`/* not */` x", "`\"`", "` `` `", "``", "`a\nb\nc`", "`\\`", "x`y`z",
    "/* ` */ x", "// ` x\nvar y", "`/* x\n*/` fn", "s := `// raw\nfunc` // comment",
    '"a"', '"a\\"b"', '"unterminated', '"a\\\\"', '"\\"', '"\\', '"a\\\nb"', '"a\\\\\\"b"', '"é界"', '"tab\t"',
    "'a'", "'\\''", "'\\\\'", "'\\n'", "''", "'ab'", "'", "'\\", "'\\x41'", "'\\u00e9'", "'界'", "'\U0001f980'", "r := '\\''; n := 0x1.fp+2; f := .5",
    "0x1.fp+2", "1e+3", "1E-3", ".5", ".", "..", "1..2", "1.2.3", "0b11", "0o7", "1_000", "0xDEAD_BEEF", "0xe+1", "0xp-1", "3i", "1+2", "1e", "x.5", "a.b", ".e5", "5.",
    "0x1p-2", "0X1P+2", "1e+e+1", "1p-1", "0xe-1", "1.5E+10i", "1__0", "09", "0.", "0..", "1.e+2", "..5", "1.5e+", "99bottles", "٣", "1٣", ".٣",
    "break", "case", "chan", "const", "continue", "default", "defer", "else", "fallthrough", "for", "func", "go", "goto", "if", "import", "interface", "map", "package",
    "range", "return", "select", "struct", "switch", "type", "var", "packageName", "funcs", "for_", "界", "var 界 = 1", "go func() {}()", "type T struct{}", "interface{}",
    "chan<- int", "<-ch", "select {}", "fallthrough", "Func", "VAR", "gofmt", "go1", "var_", "_var", "break_", "x.go", "x.func", "if(x)", "else{", "return;",
    "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}",
    "package main\n/* func\nstill */ func main() {\ns := `// raw\nfunc` // comment\nr := '\\''; n := 0x1.fp+2; f := .5\nvar 界 = \"a\\\"b\"\npackageName := 1\n",
    "type T[K comparable, V any] struct {\n\tm map[K]V\n}",
    "for i := range 10 {\n\tif i%2 == 0 { continue }\n}",
    "switch x := v.(type) {\ncase int:\n\tfallthrough\ndefault:\n}",
    "var s = `a\n\"b\"\n`\nvar t = \"c\"",
    "x := []byte(\"é\") // 界\nvar r rune = 'é'",
    "const (\n\tA = iota // 0\n\tB\n)",
    "defer func() { recover() }()",
    "if err != nil { return fmt.Errorf(\"x: %w\", err) }",
    "ch := make(chan struct{}, 1)\nselect {\ncase <-ch:\ndefault:\n}",
    "/* a */ /* b\nc */ d // e\n/* f",
    "// c\r", "x := 1\r", "`raw\r\nstring`", "\"s\"\r", "func\u3000x", "func\u00a0x", "\tfunc\tx", " ", "", "\r", "\t",
    "+-*/%^&|!<>=", "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", "a := b &^ c", "x <<= 1", "a...", "{}[]()", ",;:", "~", "$", "@", "#", "\\",
    "café := 1", "é", "e\u0301", "\u0301", "\U0001f980", "x\U0001f980", "𝒳", "Σ", "ǅ", "Ⅷ", "ª", "²", "a\u200db", "ñandú := 3",
    "'e\u0301'", "\"e\u0301\"", "`e\u0301`",
    "'a' + 'b'", "\"a\" + \"b\"", "'\"'", "\"'\"", "'`'", "\"`\"", "`'`", "`\"`",
    "x := 'a\n'b'",
]

MD = [
    "\x00", "a\x00b", "*\x00a*", "*a\x00*", "_a_\x00", "\x00_a_", "\\\x00", "\\\x00*a*", "- \x00", "-\x00", "\x00- a", "[\x00]", "[a](\x00)", "[a]\x00(b)", "<\x00>", "<a\x00@b>", "# \x00", "#\x00", "```\x00", "```\x00rs", "```\n\x00\n```", "`\x00`", "<!-- \x00 -->", "[ ]\x00", "- [ ]\x00", "1.\x00a", "> \x00", ">\x00", "\x00\n---", "a\n\x00\n---",
    # a heading or link reference right after the box of a task item
    "- [ ]# h", "- [x]#h", "- [ ][a]: b", "- [ ]# h\n---", "1. [X]## h ##", "- [ ] [a]: b", "- [x]> q",
    # info-string spelling: ASCII case folding of one letter
    "```golAng\nvar x = 1\n```", "```GOLANG\nvar x = 1\n```", "```gOlang\nvar x = 1\n```", "```RS\nlet x = 1;\n```", "```Rs\nlet x = 1;\n```", "~~~GoLang\nvar x = 1\n~~~", "```rUst\nlet x = 1;\n```", "```rustA\nlet x = 1;\n```", "```Z\nx\n```", "```@\nx\n```", "```[\nx\n```",
    # headings
    "# H", "## H", "### H", "#### H", "##### H", "###### H", "####### x", "#hashtag", "#", "# ", "#\tTab", " # one", "  # two", "   # three", "    # four", "\t# tab",
    "# H #", "## H ##", "# *emph* in heading", "# `code` in heading", "# [l](u) in heading", "#\r", "# H\r", "##", "###### ", "#######", "# # #", "#  spaced",
    "Title\n===", "Title\n---", "Title\n=", "Title\n== =", "Title\n===   ", "Title\n=== x", "a\nb\n---", "a\nb\n===", "- a\n---", "# h\n---", "[ref]: x\n---", "    code\n---", "> q\n---",
    "text\n\n===", "text\n-", "text\n--", "text\n -", "text\n  ===", "text\n    ===", "text\n\t===", "text\n=-=", "text\n-=", "text\n- -", "text\n===\n===", "text\n---\n---", "a\n---\nb\n---",
    "para\n***", "para\n___", "para\n- - -", "para\n* * *", "text\n===\r\nmore", "Title\r\n===\r\n", "Title\r\n---\r\n", "a\n b\n===", "Setext *emph*\n===", "a\n`c`\n---",
    "<!-- c -->\n---", "text <!--\n-->\n---", "text\n<!-- x -->\n---", "- a\n  b\n  ---", "1. a\n---", "# a\n===", "---\n===", "***\n---", "a\n\n---", "a\n\n\n===",
    # thematic breaks
    "---", "***", "___", "- - -", "* * *", "_ _ _", "--", "**", "-*-", "----------", "---x", "-\t-\t-", "  ---  ", "    ---", "***bold***", "---\r", "* * * *", "** *", "- -- -",
    "_ _", "___ x", "   ***", "\t---", "- - - x", "-- -", "- ---", "*-*", "***\n***", "---\n---\n---",
    # fences
    "```\ncode\n```", "~~~\ncode\n~~~", "````\ncode\n````", "```rust\nfn main() {}\n```", "```rs\nlet x = 1;\n```", "```RUST\nfn x() {}\n```", "```go\nfunc x() {}\n```",
    "```golang\nvar x = 1\n```", "```Go\nvar x = 1\n```", "```python\nx = 1\n```", "``` rust extra\nfn x() {}\n```", "```rust{.class}\nfn x() {}\n```", "```{rust}\nfn x() {}\n```",
    "```\trs\nlet x = 1;\n```", "```\u3000rs\nlet x = 1;\n```", "```  rust  \nlet x = 1;\n```", "```rust\n/* c\n*/ fn x() {}\n```", "```go\ns := `a\nb`\n```",
    "```bad`info", "```bad`info\ntext\n```", "~~~bad`info\ntext\n~~~", "``", "``x", "~~", "~~x", "```", "~~~", "````", "```\n", "```\n\n", "```\n```", "```\n\n```", "~~~\n~~~",
    "```\ncode\n``", "```\ncode\n~~~", "~~~\ncode\n```", "```\ncode\n````", "````\ncode\n```", "````\ncode\n```\nmore\n````", "```\ncode\n``` trailing", "```\ncode\n```   ", "```\ncode\n   ```",
    "```\ncode\n    ```", "```\ncode\n\t```", "  ```\ncode\n  ```", "   ```\ncode\n```", "    ```\ncode\n```", "\t```\ncode\n```", "```\n# not heading\n**not bold**\n```",
    "```\ncode\n```\n# Heading", "```\ncode\n```\n```\ncode2\n```", "a\n```\ncode\n```\nb", "- ```\n  code\n  ```", "> ```\n> code\n> ```", "```rust\nfn\n```x", "```rust\nfn\n```\n```go\nvar\n```",
    "~~~~\n```\n~~~", "~~~~\n```\n~~~~", "```\n~~~\n```", "````rust\n/* comment\nstill */ fn main() {}\n```\n```` trailing\n~~~~\n`````\n# Heading\n~~~go\npackage main\ns := `raw\nfunc`\n~~~\nordinary prose",
    "  ~~~python\r\n# code\r\n**code**\r\n  ~~~~\r\n# Heading\r\n```\r\n*code*", "```\r\ncode\r\n```\r\n", "```\r\n", "```rust\r\nfn x() {}\r\n```", "```rust\r\n/* a\r\nb */ fn\r\n```",
    "```\ncode\n```\r", "```\r", "``` \r", "```rust\r", "text\n```\ncode", "# H\n```\nunclosed # still", "```\ncode\n\n\n```", "```rust\n\n```", "```rust\n\n\nfn x() {}\n\n```",
    "``` ```", "```` ``` ````", "```a```", "```a```b", "```\n```a", "```rs\n'a 'b\n```", "```rs\nr#\"x\n\"#\n```", "```go\n/* x\n```\n*/\n```",
    "```\n<!-- comment\n```\n-->", "<!--\n```\ncode\n```\n-->", "<!-- x -->\n```\ncode\n```", "```\n<!--\n```\n*a*",
    # quotes
    "> q", ">q", "> > x", ">> x", ">", "> # h", "> - item", "> ```", ">     code", "> 1. a", "> [x]: y", "> *a*", "> > > deep", ">\t> x", "  > q", "    > q", ">  x", "> \n> x", ">>>", "> - [ ] t", "> ---", "> ===",
    "> a\n> ===", "> a\n===", "> a\n---", ">\r", "> q\r", "> # h\r\n> text\r\n> ===",
    # lists
    "- a", "* a", "+ a", "-a", "1. a", "1) a", "12. a", "123456789. a", "1234567890. a", "1.5 and -not-a-list", "-", "1.", "- [ ] t", "- [x] t", "- [X] t", "- [y] t", "-  [ ] t", "- []", "- [ ]x",
    "* [ ] a", "1. [x] a", "  - a", "   - a", "    - a", "- - a", "- > q", "- # h", "- ---", "- **bold**", "- `code`", "-\ttab", "- ", "-\r", "- a\r", "1.\ta", "1)\ta", "0. zero", "00. zeros",
    "- [ ]", "- [x]", "- [ ] ", "- [x]\tdone", "- [ x] bad", "- [xx] bad", "- [ ](url)", "- [x](url)", "- [ ]: ref", "-  -  - x", "+ + +", "- *a*", "* *a*", "* **a**", "*a*", "** a", "* * a",
    "- a\n- b\n- c", "1. a\n2. b\n3. c", "- a\n  - b\n    - c", "* a\n\n* b", "- [ ] a\n- [x] b", "10. ten\n11. eleven", "- a\n---", "- a\nb\n===",
    "-\ta", "+\ta", "*\ta", "\u2003- a", "-\u3000a", "-\u00a0a", "- \u3000[ ] a", "1.\u3000a", "1.a", "1)a", "-.", "+a", "*a", "*\u00a0a",
    "- 界\n- é", "1. 界 *café*",
    # indented code
    "    code", "    *code*", "\tcode", "  \tcode", "   \tcode", "     five", "    ", "\t", "    # not heading", "    - not list", "    ```", "    > not quote", "    [x]: not ref",
    "para\n    cont", "para\n\n    code\n\npara", "    a\n    b", "\tcode\r", "    code\r", "   \t", "para\n\tcode",
    # link reference definitions
    "[id]: https://example.com", "[id]:", "[id]: <url> \"title\"", "[a [b] c]: x", "[id] : x", "[id]:x", "[escaped\\]]: x", " [id]: x", "- [id]: x", "> [id]: x", "[x]:\n", "[unclosed: x", "[id]: x\n[id2]: y",
    "[id]: x\r", "[id]:\r", "[a](b): c", "[a][b]: c", "[a]: [b]: c", "[a]:[b]", "[]: x", "[ ]: x", "[**a**]: x", "[a]: *b*", "[a]: `b`", "[界]: é", "  [a]: b", "   [a]: b", "    [a]: b",
    "text\n[a]: b", "[a]: b\ntext\n===", "[a]: b\n---", "[a]: b\n\n---", "[^1]: footnote", "[a\\]: b", "[a\\\\]: b", "[a[b]: c", "[a]b]: c",
    # inline code
    "`a`", "``a`b``", "`unclosed", "`a``b`", "```a```b", "`a`b`", "` `", "`**`", "`*a*` *b*", "`[l](x)`", "`<!--` x", "\\`a`", "`a\\`", "``", "```", "` a `", "`a` `b` `c`", "`a\u00e9`", "`界`", "x`a`y",
    "`a` `b", "`a`` `b``", "``a` `b``", "`\\``", "`", "````", "` `` `", "*`a`*", "`*a*`", "_`a`_", "[`a`](b)", "[a](`b`)", "`[a](b)`", "``a``b``", "`a`\r", "`a\r",
    # emphasis
    "*a*", "**a**", "***a***", "****a****", "_a_", "__a__", "___a___", "snake_case", "a_b_", "_a_b", "*a **b** c*", "* a *", "*a *", "* a*", "**a *b* c**", "*a\\*b*", "*a\\", "_a_b_c_",
    "__a__b", "_é_", "é_a_", "_a_é", "界_a_界", "**", "*", "a*b*c", "*a*b*", "**a*", "*a**", "*a* *b*", "**a** and *b* and `c`", "unmatched *markup", "escaped \\*literal*", "text * spaced *",
    "a_b_c", "*a\u00a0*", "*\u00a0a*", "*a\u3000*", "*\u3000a*", "*a\u0085*", "_a_\u00e9", "_a_1", "1_a_", "_a_ b", "a _b_", "(_a_)", "_(a)_", "*a*b", "*a*,", "**a**b", "__a__b", "___a__", "_a__", "__a_",
    "**a***", "***a**", "*a*\\", "\\**a*", "**\\*a**", "**a\\**", "*a**b*", "*a***b*", "***a** b*", "*a **b***", "_a *b* c_", "*a _b_ c*", "*a_b*", "_a*b_", "*", "***", "****", "_", "__", "___", "____",
    "a *b* c _d_ e **f** g __h__", "*界*", "**界**", "_界_", "*é*", "*e\u0301*", "*\U0001f980*", "**\U0001f980 x**", "_x_\U0001f980", "\U0001f980_x_", "*a*\r", "*a\r", "**a**\r\n",
    "* a\n*b*", "*a\nb*", "**a\nb**", "*a*\n*b*",
    # links
    "[a](b)", "[a](b(c))", "[a](b", "[a]()", "[a][b]", "[a][]", "[a] (b)", "![a](b)", "![a][b]", "!a[b](c)", "[a [b] c](d)", "[a\\]b](c)", "[a](b \"t\")", "[a](<b c>)", "[a]", "[[a]]", "[a][b](c)",
    "[![i](a)](b)", "[a](b)(c)", "[a]:b", "[a](b)*c*", "*[a](b)*", "[*a*](b)", "[a](*b*)", "[a](b_c_d)", "![](x)", "![a]", "![a", "![", "!", "!!", "[", "]", "[]", "[]()", "[](x)", "[\\]](x)",
    "[a](b\\)c)", "[a](b\\", "[a\\", "[a](b)\\", "link [a](b) text [c](d) more", "[a]\n(b)", "[a](b\nc)", "[a][b\nc]", "[界](é)", "[a](\U0001f980)", "[a](b)\r", "[a](b\r)",
    "[a]  [b]", "[a](b)[c](d)", "![a](b)![c](d)", "[a[b](c)](d)", "[a](b[c](d))", "[a]((b)", "[a](()", "[a](b))", "[a](()))", "[a]]](b)", "[[[a](b)", "[a](b]",
    # autolinks
    "<https://a.b>", "<http://a>", "<mailto:a@b>", "<a@b.c>", "<https://a b>", "<foo>", "<>", "<", "< https://a>", "<https://a", "<<https://a>>", "<a@b><c@d>", "<ftp://x>", "<HTTPS://x>", "<a@b",
    "a < b > c", "<a>b<c>", "<https://a>x", "x<https://a>", "<mailto:>", "<@>", "<a @b>", "<\u3000a@b>", "<a@b\u3000>", "<https://\u00e9>", "<https://界.com>", "<b>bold</b>", "<br/>", "<https://a>\r", "<https://a\r>",
    "<https://a.b> and <c@d.e>", "<a href=\"x\">", "<a@b>*c*", "*<a@b>*", "<*a@b*>", "<a@b\\>",
    # html comments
    "<!-- c -->", "<!-- unclosed", "<!-- a\nb\nc -->", "<!---->", "<!-->", "<!--->", "-->", "a <!-- b --> c", "<!-- # hidden\n   **hidden**\n--> *visible*\n`<!--` **visible**\ntext <!-- hidden --> text",
    "<!-- a --> b <!-- c --> d", "<!-- a -->\n<!-- b -->", "<!-- a\n-->\n# H", "<!-- a\n# H\n-->", "- <!-- a\n  b -->", "> <!-- a\n> b -->", "<!--\n---\n-->", "<!--\n```\ncode\n```\n-->", "<!--\n    code\n-->",
    "<!-- a --> *b*", "*a* <!-- b", "*a <!-- b --> c*", "`a <!-- b` -->", "<!-- `a` -->", "<!-- a -- > b -->", "<! -- x -->", "<!-x", "<!--x-->y", "<!-- a\r\n-->", "<!-- a\r", "<!--\r\n# x\r\n-->\r\n# y",
    "<!--\n\n\n-->", "<!-- 界 -->", "<!-- é\n-->\u00e9", "x<!--", "<!--<!-- a -->", "<!-- <!-- a -->-->", "--> a <!-- b -->", "<!-- a -->-->", "-->-->",
    "<!--\n- list\n1. num\n> quote\n[a]: b\n-->", "a\n<!--\n===\n-->", "<!--\n-->\n===", "text\n<!--\n-->\n===",
    # escapes
    "\\*not\\*", "\\\\", "\\a", "\\", "\\\u00e9", "\\_", "\\<", "\\[", "\\`", "\\!", "\\!\\[a](b)", "a\\\nb", "\\\\*a*", "\\\\\\*a*", "\\ ", "\\\t", "\\#", "\\# not heading", "\\- not list", "\\> not quote",
    "\\<!-- not comment", "\\*\\*a\\*\\*", "*\\**", "_\\__", "\\\r",
    # mixed / realistic
    "# Title\n\nSubtitle\n===\n\n---\n####### plain\n#hashtag\nplain_text_here\nordinary prose",
    "text\n-\n\n* * *\n\n---",
    "界 *café* **bold** _italic_ __strong__ `**code**` ``a ` b`` [link](path_(one)) ![image](pic.png) [ref][id] <https://example.com>",
    "# Project\n\nSome *emphasis* and **strong** text with `code`.\n\n- item one\n- item two\n  - nested\n\n1. first\n2. second\n\n> quote\n> more\n\n```rust\nfn main() {\n    println!(\"hi\");\n}\n```\n\n[link]: https://example.com\n",
    "Title\n=====\n\nPara one\ncontinues.\n\nSub\n---\n\n* * *\n\n- [x] done\n- [ ] todo\n",
    "| a | b |\n|---|---|\n| 1 | 2 |", "a | b\n--|--\n1 | 2", "Term\n: Definition", "line one  \nline two", "line one\\\nline two", "a  \n---",
    "- a\n\n  b\n\n- c", "1. a\n\n   ```\n   code\n   ```\n\n2. b", "> quote\n>\n> - list\n> - list2\n", "***\n- - -\n___",
    "Hello <!-- hidden --> *world* and `<!--` still **visible**",
    "# H *a* **b** `c` [d](e) <https://f>", "## [Link](url) heading ##", "text *a* text _b_ text __c__ text ***d*** text",
    "```\nfenced\n```\n    indented\n- list\n> quote\n# H", "a\tb\tc", "a\u00a0b", "  leading", "trailing  ", "\u2003em space", "\u3000ideographic",
    "e\u0301 *e\u0301*", "*e\u0301*e\u0301", "_e\u0301_", "a\u0301_b_", "_a_\u0301", "界*a*界", "界**a**界", "界_a_界", "٣_a_٣", "_a_٣", "٣_a", "a_٣_",
    "\r\n\r\n", "\r\r\n", "a\rb", "# a\rb", "```\r\n```\r\n", "- a\r\n- b\r\n", "> a\r\n> b\r\n", "\r", "\n", "\n\n", "\n\n\n",
    "\n```\n\n", "  \n  \n", " \n---", "\n---", "\n===", "a\n \n---", "a\n\t\n---",
]

# Multi-line Markdown documents made from fragment lines, to exercise block state.
MD_FRAGMENTS = [
    "# Heading", "## Sub *x*", "text", "more text", "", "", "---", "===", "***", "- item", "* item", "1. one", "2) two", "- [ ] t", "- [x] d",
    "> quote", ">", "> - nested", "```", "```rust", "```go", "~~~", "~~~python", "````", "    code", "\tcode", "[id]: http://x", "<!--", "-->", "<!-- c -->",
    "a *b* c", "**s** and _e_", "`code`", "[l](u)", "![i](u)", "<https://x.y>", "x <!-- y", "z --> w", "\\*esc\\*", "snake_case_name", "界 é *界*", "trailing  ",
    "1. [x] a", "-", "--", "  - indented", "    - code", "###### six", "####### seven", "#nospace", "text\\", "<a@b.c>", "a_b_", "_a_b", "* * *", "- - -",
]


# Cases added because mutating the Go port (changing one condition, constant
# or comparison at a time) survived the sampled cases without them: each one
# distinguishes a different edge, so that golden.jsonl also guards them.
PINNED = [
    ("md", "*\x00a*"),                # emphasis opens before a NUL, which is not whitespace
    ("md", "<!--\r\n\r\n\r\n-->"),    # a CRLF line that holds only the CR
    ("md", "\r"),
    ("md", "```golAng\nvar x = 1\n```"),  # ASCII lower-casing of one capital in the middle
    ("md", "```GOLANG\nvar x = 1\n```"),
    ("md", "```Rs\nlet x = 1;\n```"),
    ("md", "  \t# h"),                    # whitespace after the indent is not a list marker
    ("md", "  \t[ ] x"),
    ("md", "<http://a> <mailto:x> <a@b> <ftp://c>"),
    ("md", "- [X] done"),
    ("md", "snake_case_word a_b_c _a_b \u00e9_a_ x_a_"),
    ("md", "`a` ``b`` `c"),
    ("md", "`a`"),
    ("md", "`*a*` ``**b**``"),            # markup inside a code span that ends the line
    ("md", "<https://x.y>*a* <a@b>`c`"),  # the character right after an autolink
    ("md", "[a](b)*c* [d][e]_f_ ![g](h)`i`"),
    ("go", "1E-3 1E+10 0x1P+2 0xE+1"),
    ("go", "/**/ var"),
    ("go", "/*//"),
    ("go", "/***/ x"),
    ("rust", "/**/ let"),
    ("rust", "/*/*"),
    ("rust", "/*/*/ x */ */ y"),
    ("rust", 'b"x'),
    ("rust", 'c"x'),
    ("rust", "'\\u'"),
    ("rust", "'\\\x00'"),
    ("rust", "'\\"),
    ("rust", "'''"),
    ("rust", "'''x'"),
    ("rust", "b'''"),
    ("rust", "'\\u{}'"),
]


def hexs(s):
    return s.encode("utf-8").hex()


def emit(out, lang, kind, text):
    out.write(f"{lang} {kind} {hexs(text)}\n")


# ---- random token alphabets -------------------------------------------------
UNI_WORDS = ["\x00", "café", "界", "ñandú", "Ⅷ", "ª", "e\u0301", "é", "\U0001f980", "x\u0301", "²", "٣", "ǅ", "Σ", "𝒳", "a\u200db", "\u3000", "\u00a0", "界é", "z\u0345", "ᾼ"]
ASCII_WORDS = ["x", "foo", "a_b", "_", "_x", "T", "Self", "self", "ref_", "let_", "r", "b", "c", "br", "cr", "rb"]
PUNCT = list("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~")
WS = [" ", " ", " ", "  ", "\t", "\u3000", "\u00a0", "\u0085", "\x00"]

RUST_KW = ["fn", "let", "mut", "pub", "struct", "enum", "impl", "match", "if", "else", "for", "in", "while", "loop", "return", "use", "mod", "self", "Self", "true", "false", "gen", "try", "yield", "type", "where", "dyn", "ref", "r#type", "r#fn", "r#"]
RUST_TOK = [
    "\"", "b\"", "c\"", "r\"", "r#\"", "r##\"", "br\"", "br#\"", "cr\"", "cr#\"", "\"#", "\"##", "\"###", "'", "b'", "'\\n'", "'\\''", "'\\\\'", "'\\u{1F980}'", "'\\u{}'", "'\\x41'", "'\\x4'", "'a'", "'é'", "'界'", "'a", "'static", "'_", "'\U0001f980'",
    "/*", "*/", "//", "///", "\\", "\\\"", "!", "!=", "!(", "![", "println!", "vec!", "r#a!", "a!=b",
    "0", "1", "12", "1_000", "0x1F", "0xE+1", "1.5", "1.", "1..2", ".5", "1e5", "1e+5", "1E-5", "1.2e-3", "3u8", "0b1", "1.0.0", "1.a", "9é", "1²",
    "{", "}", "(", ")", "[", "]", "<", ">", "::", "->", "=>", "=", "+", "-", "*", "/", "%", "&", "|", "^", "?", ";", ",", ".", ":", "#", "#[", "$", "@", "~", "`",
]
GO_KW = ["break", "case", "chan", "const", "continue", "default", "defer", "else", "fallthrough", "for", "func", "go", "goto", "if", "import", "interface", "map", "package", "range", "return", "select", "struct", "switch", "type", "var", "packageName", "funcs", "Func"]
GO_TOK = [
    "`", "``", "\"", "'", "\\", "\\\"", "\\'", "'\\''", "'a'", "'é'", "'界'", "'\\n'", "\"a\"", "\"\\\"\"", "//", "/*", "*/", "/", "*",
    "0", "1", "12", "1_000", "0x1.fp+2", "0xe+1", "0xp-1", "1e+3", "1E-3", ".5", ".", "..", "...", "1.2.3", "1+2", "3i", "5.", ".e5", "9é", "1٣",
    "{", "}", "(", ")", "[", "]", "<-", ":=", "==", "!=", "&^", "&&", "||", "+", "-", "%", "<", ">", "=", "!", ";", ",", ":", "?", "#", "$", "@", "~",
]
MD_TOK = [
    "#", "##", "######", "#######", "> ", ">", "- ", "* ", "+ ", "1. ", "12) ", "123456789. ", "1234567890. ", "[ ] ", "[x] ", "[X] ", "[y] ", "[]", "```", "~~~", "````", "```rust", "```go", "~~~golang", "```python", "``` rs",
    "---", "***", "___", "===", "- - -", "*", "**", "***", "****", "_", "__", "___", "`", "``", "[", "]", "(", ")", "![", "](", "][", "]:", "<", ">", "<!--", "-->", "<!---->", "https://x.y", "http://z", "mailto:a@b", "a@b", "<https://x.y>", "<a@b>",
    "\\*", "\\_", "\\", "\\`", "\\[", "\t", "    ", "  ", " ", "word", "snake_case", "界", "é", "|", ":", "!", "\U0001f980", "e\u0301", "\u3000", "\u00a0", "\r",
]
DENSE = {
    "rust": list("'\"\\rbc#/*  a\u00e9\u754c1.e+-u{}x!=_`\r\x00") + ["\\u", "r#", "b'", "'a", "/*", "*/", "//"],
    "go": list("'\"\\`/* a\u00e9\u754c1.e+-p_x0\r!\x00") + ["//", "/*", "*/", "0x"],
    "md": list("#>-*+_`~[]()!<>\\ \t\r:=1.a\u754c|x\x00") + ["```", "~~~", "<!--", "-->", "](", "[x]", "  ", "    ", "https://", "@"],
}


def rand_word(r):
    return r.choice(UNI_WORDS) if r.random() < 0.25 else r.choice(ASCII_WORDS)


def rand_line(r, lang):
    n = r.randint(0, 12)
    if lang == "rust":
        pool = RUST_TOK
        kws = RUST_KW
    elif lang == "go":
        pool = GO_TOK
        kws = GO_KW
    else:
        pool = MD_TOK
        kws = []
    parts = []
    for _ in range(n):
        x = r.random()
        if x < 0.45:
            parts.append(r.choice(pool))
        elif x < 0.6 and kws:
            parts.append(r.choice(kws))
        elif x < 0.75:
            parts.append(rand_word(r))
        elif x < 0.8:
            parts.append(r.choice(PUNCT))
        else:
            parts.append(r.choice(WS))
        if r.random() < 0.55:
            parts.append(" ")
    line = "".join(parts)
    if r.random() < 0.05:
        line += "\r"
    return line


def rand_md_line(r):
    x = r.random()
    if x < 0.45:
        return r.choice(MD_FRAGMENTS)
    if x < 0.55:
        return " " * r.randint(0, 5) + r.choice(MD_FRAGMENTS)
    if x < 0.62:
        return r.choice(MD_FRAGMENTS) + "\r"
    return rand_line(r, "md")


def rand_doc(r, lang):
    n = r.choice([1, 1, 2, 3, 3, 4, 5, 6, 8])
    if lang == "md":
        return "\n".join(rand_md_line(r) for _ in range(n))
    return "\n".join(rand_line(r, lang) for _ in range(n))


def dense_doc(r, lang):
    alpha = DENSE[lang]
    n = r.choice([1, 1, 2, 3])
    lines = []
    for _ in range(n):
        lines.append("".join(r.choice(alpha) for _ in range(r.randint(0, 24))))
    return "\n".join(lines)



HARNESS = r"""
// Driver for the syntax differential run: cases in on stdin, rows out on stdout.
#![allow(dead_code)]
mod syntax;
use std::io::{BufRead, Write};
use syntax::Style;

fn code(s: Style) -> char {
    match s {
        Style::Plain => '.',
        Style::Keyword => 'k',
        Style::Comment => 'c',
        Style::String => 's',
        Style::Number => 'n',
        Style::Macro => 'm',
        Style::Punctuation => 'p',
        Style::Lifetime => 'l',
        Style::Type => 't',
        Style::Function => 'f',
        Style::Variable => 'v',
        Style::Property => 'r',
        Style::Constant => 'o',
        Style::Namespace => 'a',
        Style::Heading => 'h',
        Style::Emphasis => 'e',
        Style::Strong => 'b',
        Style::Link => 'i',
    }
}

fn unhex(h: &str) -> String {
    let b: Vec<u8> = (0..h.len() / 2)
        .map(|i| u8::from_str_radix(&h[2 * i..2 * i + 2], 16).unwrap())
        .collect();
    String::from_utf8(b).unwrap()
}

fn main() {
    let stdin = std::io::stdin();
    let stdout = std::io::stdout();
    let mut out = std::io::BufWriter::new(stdout.lock());
    for line in stdin.lock().lines() {
        let line = line.unwrap();
        let mut fields = line.splitn(3, ' ');
        let lang = fields.next().unwrap();
        let _kind = fields.next().unwrap();
        let text = unhex(fields.next().unwrap_or(""));
        // What Buffer::new does: split on '\n' only, characters as chars.
        let lines: Vec<Vec<char>> = text.split('\n').map(|l| l.chars().collect()).collect();
        let styles = match lang {
            "rust" => syntax::rust(&lines),
            "go" => syntax::go(&lines),
            "md" => syntax::for_path(std::path::Path::new("x.md"), &lines).unwrap(),
            _ => panic!("unknown lexer {lang}"),
        };
        assert_eq!(styles.len(), lines.len());
        let rows: Vec<String> = styles
            .iter()
            .zip(&lines)
            .map(|(s, l)| {
                assert_eq!(s.len(), l.len());
                s.iter().map(|x| code(*x)).collect()
            })
            .collect();
        writeln!(out, "{}", rows.join("|")).unwrap();
    }
}
"""

CARGO_TOML = """[package]
name = "dump"
version = "0.1.0"
edition = "2024"

[workspace]
"""


def hexs(s):
    return s.encode("utf-8").hex()


def build_corpus(seed=20260722):
    """Returns [(lang, kind, text)] in a fixed order."""
    r = random.Random(seed)
    cases = []
    for lang, snippets in (("rust", RUST), ("go", GO), ("md", MD)):
        for s in snippets:
            cases.append((lang, "snippet", s))
            for other in ("rust", "go", "md"):
                if other != lang:
                    cases.append((other, "cross", s))
            if "\n" in s and "\r" not in s:
                cases.append((lang, "snippet-crlf", s.replace("\n", "\r\n")))
    for lang, n in (("rust", 9000), ("go", 7000), ("md", 9000)):
        for _ in range(n):
            cases.append((lang, "random", rand_doc(r, lang)))
        for _ in range(n // 3):
            cases.append((lang, "dense", dense_doc(r, lang)))
    for lang, text in PINNED:
        cases.append((lang, "pinned", text))
    return cases


def run_rust(root, cases):
    with tempfile.TemporaryDirectory(prefix="coigue-syntax-golden-") as scratch:
        work = pathlib.Path(scratch)
        (work / "src" / "syntax").mkdir(parents=True)
        (work / "Cargo.toml").write_text(CARGO_TOML)
        (work / "src" / "main.rs").write_text(HARNESS)
        (work / "src" / "syntax.rs").symlink_to(root / "src" / "syntax.rs")
        (work / "src" / "syntax" / "markdown.rs").symlink_to(root / "src" / "syntax" / "markdown.rs")
        env = dict(os.environ, CARGO_TARGET_DIR=str(work / "target"))
        subprocess.run(["cargo", "build", "--offline", "--quiet"], cwd=work, env=env, check=True)
        stdin = "".join(f"{lang} {kind} {hexs(text)}\n" for lang, kind, text in cases)
        out = subprocess.run([str(work / "target" / "debug" / "dump")], input=stdin, env=env,
                             capture_output=True, text=True, check=True).stdout
    rows = out.split("\n")
    assert rows[-1] == "" and len(rows) == len(cases) + 1
    return rows[:-1]


def sample(cases):
    """The cases that go into golden.jsonl: every k-th hand-written snippet and
    a few random and dense ones of each lexer."""
    picked = []
    stride = {"rust": 3, "go": 3, "md": 4}
    seen = {}
    for index, (lang, kind, text) in enumerate(cases):
        if kind == "snippet":
            seen[lang] = seen.get(lang, 0) + 1
            if (seen[lang] - 1) % stride[lang] == 0:
                picked.append(index)
        elif kind in ("random", "dense"):
            seen[(lang, kind)] = seen.get((lang, kind), 0) + 1
            if seen[(lang, kind)] <= (24 if kind == "random" else 12):
                picked.append(index)
        elif kind == "pinned":
            picked.append(index)
        elif kind == "snippet-crlf":
            seen["crlf"] = seen.get("crlf", 0) + 1
            if seen["crlf"] % 8 == 0:
                picked.append(index)
    return picked


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--full", metavar="DIR", help="also write the whole corpus and the Rust output here")
    args = parser.parse_args()
    here = pathlib.Path(__file__).resolve().parent
    root = here.parents[2]
    assert (root / "src" / "syntax.rs").exists(), root
    cases = build_corpus()
    rows = run_rust(root, cases)
    if args.full:
        full = pathlib.Path(args.full)
        full.mkdir(parents=True, exist_ok=True)
        with open(full / "corpus.txt", "w") as corpus, open(full / "rust_out.txt", "w") as expected:
            for (lang, kind, text), row in zip(cases, rows):
                corpus.write(f"{lang} {kind} {hexs(text)}\n")
                expected.write(row + "\n")
    with open(here / "golden.jsonl", "w") as golden:
        for index in sample(cases):
            lang, kind, text = cases[index]
            golden.write(json.dumps({"lang": lang, "text": text, "styles": rows[index].split("|")}) + "\n")
    print(f"{len(cases)} cases lexed, {len(sample(cases))} written to golden.jsonl", file=sys.stderr)


main()
