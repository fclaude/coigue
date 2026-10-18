use std::process::Command;

#[test]
fn version_exits_without_a_terminal() {
    let output = Command::new(env!("CARGO_BIN_EXE_coigue"))
        .arg("--version")
        .output()
        .unwrap();
    assert!(output.status.success());
    assert!(output.stderr.is_empty());
    let text = String::from_utf8(output.stdout).unwrap();
    assert!(text.starts_with(concat!("coigue ", env!("CARGO_PKG_VERSION"))));
    assert!(text.contains("main build"));
    assert!(text.contains("commit"));
}

#[test]
fn help_exits_without_a_terminal() {
    let output = Command::new(env!("CARGO_BIN_EXE_coigue"))
        .arg("--help")
        .output()
        .unwrap();
    assert!(output.status.success());
    assert!(output.stderr.is_empty());
    let text = String::from_utf8(output.stdout).unwrap();
    assert!(text.contains("Usage: coigue [FILE]"));
    assert!(text.contains("--version"));
    assert!(!text.contains('\u{1b}'));
}
