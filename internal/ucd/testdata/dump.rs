// Dumps the Unicode property data that Rust's standard library uses for char::is_* and
// str::to_lowercase, as range lists. Regenerate the .txt files next to this file with:
//   rustc -O dump.rs -o dump && ./dump testdata    (run from internal/ucd)
// then `go generate ./internal/ucd`. The checked-in data comes from rustc 1.96.1 (Unicode 17.0.0).
use std::fmt::Write as _;
use std::fs;

fn ranges(name: &str, pred: impl Fn(char) -> bool) -> String {
    let mut out = String::new();
    let mut start: Option<u32> = None;
    for cp in 0..=0x110000u32 {
        let on = char::from_u32(cp).is_some_and(&pred);
        match (on, start) {
            (true, None) => start = Some(cp),
            (false, Some(s)) => {
                writeln!(out, "{s:X} {:X}", cp - 1).unwrap();
                start = None;
            }
            _ => {}
        }
    }
    let _ = name;
    out
}

fn main() {
    let dir = std::env::args().nth(1).expect("output directory");
    fs::create_dir_all(&dir).unwrap();
    let w = |file: &str, data: String| fs::write(format!("{dir}/{file}"), data).unwrap();
    w("rust-alphabetic.txt", ranges("alphabetic", char::is_alphabetic));
    w("rust-numeric.txt", ranges("numeric", char::is_numeric));
    w("rust-whitespace.txt", ranges("whitespace", char::is_whitespace));
    w("rust-control.txt", ranges("control", char::is_control));
    w("rust-uppercase.txt", ranges("uppercase", char::is_uppercase));
    w("rust-lowercase.txt", ranges("lowercase", char::is_lowercase));
    // Simple lower-casing: "cp lower" pairs for every scalar whose lower-case is a single
    // different scalar. U+0130 is the only multi-scalar result (checked below).
    let mut pairs = String::new();
    for cp in 0..0x110000u32 {
        let Some(c) = char::from_u32(cp) else { continue };
        let lower: Vec<char> = c.to_lowercase().collect();
        match lower.as_slice() {
            [l] if *l == c => {}
            [l] => writeln!(pairs, "{cp:X} {:X}", *l as u32).unwrap(),
            _ => assert_eq!(cp, 0x130, "unexpected multi-scalar lower-case of U+{cp:X}"),
        }
    }
    w("rust-lower-map.txt", pairs);
}
