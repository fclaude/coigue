//! Explicit desktop clipboard access through local utilities; never OSC 52.
use std::{
    io::{self, Read, Seek, Write},
    process::{Command, Stdio},
    time::{Duration, Instant},
};
const LIMIT: usize = 8 * 1024 * 1024;
const TIMEOUT: Duration = Duration::from_secs(2);

#[derive(Debug, PartialEq)]
struct Backend {
    copy: &'static str,
    copy_args: &'static [&'static str],
    paste: &'static str,
    paste_args: &'static [&'static str],
}
fn backends(macos: bool, wayland: bool, x11: bool) -> Vec<Backend> {
    if macos {
        return vec![Backend {
            copy: "pbcopy",
            copy_args: &[],
            paste: "pbpaste",
            paste_args: &[],
        }];
    }
    let mut choices = Vec::new();
    if wayland {
        choices.push(Backend {
            copy: "wl-copy",
            copy_args: &["--type", "text/plain;charset=utf-8"],
            paste: "wl-paste",
            paste_args: &["--no-newline", "--type", "text"],
        });
    }
    if x11 {
        choices.push(Backend {
            copy: "xclip",
            copy_args: &["-selection", "clipboard", "-in"],
            paste: "xclip",
            paste_args: &["-selection", "clipboard", "-out"],
        });
        choices.push(Backend {
            copy: "xsel",
            copy_args: &["--clipboard", "--input"],
            paste: "xsel",
            paste_args: &["--clipboard", "--output"],
        });
    }
    choices
}

// Private temporary files avoid pipe deadlocks with large text or clipboard owners
// that fork. They are unlinked on every exit path, including helper errors/timeouts.
fn run(command: &mut Command, input: Option<&str>, timeout: Duration) -> io::Result<String> {
    if input.is_some_and(|s| s.len() > LIMIT) {
        return Err(io::Error::other("clipboard text exceeds 8 MiB"));
    }
    let mut source = tempfile::tempfile()?;
    if let Some(text) = input {
        source.write_all(text.as_bytes())?;
        source.rewind()?;
    }
    let mut output = tempfile::tempfile()?;
    command.stdin(Stdio::from(source)).stderr(Stdio::null());
    if input.is_some() {
        command.stdout(Stdio::null());
    } else {
        command.stdout(Stdio::from(output.try_clone()?));
    }
    let mut child = command.spawn()?;
    let deadline = Instant::now() + timeout;
    let status = loop {
        if input.is_none() && output.metadata().is_ok_and(|m| m.len() > LIMIT as u64) {
            let _ = child.kill();
            let _ = child.wait();
            return Err(io::Error::other("clipboard text exceeds 8 MiB"));
        }
        match child.try_wait() {
            Ok(Some(status)) => break status,
            Ok(None) if Instant::now() < deadline => std::thread::sleep(Duration::from_millis(10)),
            result => {
                let _ = child.kill();
                let _ = child.wait();
                return Err(match result {
                    Err(e) => e,
                    _ => io::Error::new(io::ErrorKind::TimedOut, "clipboard helper timed out"),
                });
            }
        }
    };
    if !status.success() {
        return Err(io::Error::other(format!(
            "clipboard helper failed ({status}); check desktop access/clipboard contents"
        )));
    }
    output.rewind()?;
    let mut bytes = Vec::new();
    output.take(LIMIT as u64 + 1).read_to_end(&mut bytes)?;
    if bytes.len() > LIMIT {
        return Err(io::Error::other("clipboard text exceeds 8 MiB"));
    }
    String::from_utf8(bytes).map_err(|_| io::Error::other("clipboard does not contain UTF-8 text"))
}
fn transfer(input: Option<&str>) -> Result<String, String> {
    let present = |name| std::env::var_os(name).is_some_and(|s| !s.is_empty());
    let choices = backends(
        cfg!(target_os = "macos"),
        present("WAYLAND_DISPLAY"),
        present("DISPLAY"),
    );
    if choices.is_empty() {
        return Err(
            "No desktop clipboard available (SSH/headless); internal y/p still work".into(),
        );
    }
    for backend in choices {
        let (program, args) = if input.is_some() {
            (backend.copy, backend.copy_args)
        } else {
            (backend.paste, backend.paste_args)
        };
        let mut command = Command::new(program);
        command.args(args);
        if cfg!(target_os = "macos") {
            command.env("LC_CTYPE", "en_US.UTF-8");
        }
        match run(&mut command, input, TIMEOUT) {
            Err(e) if e.kind() == io::ErrorKind::NotFound => continue,
            result => return result.map_err(|e| format!("{program}: {e}")),
        }
    }
    Err("Clipboard utility missing: use wl-clipboard (Wayland), xclip/xsel (X11), or pbcopy/pbpaste (macOS); internal y/p still work".into())
}
pub fn copy(text: &str) -> Result<(), String> {
    transfer(Some(text)).map(|_| ())
}
pub fn paste() -> Result<String, String> {
    transfer(None)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn desktop_choice_is_explicit_and_never_uses_terminal_export() {
        assert!(backends(false, false, false).is_empty());
        assert_eq!(backends(true, false, false)[0].copy, "pbcopy");
        let both = backends(false, true, true);
        assert_eq!(
            both.iter().map(|b| b.copy).collect::<Vec<_>>(),
            ["wl-copy", "xclip", "xsel"]
        );
        assert!(both[0].paste_args.contains(&"--no-newline"));
    }
    #[cfg(unix)]
    #[test]
    fn fixtures_roundtrip_utf8_empty_failures_timeout_and_limits() {
        use std::os::unix::fs::PermissionsExt;
        let dir = tempfile::tempdir().unwrap();
        let helper = dir.path().join("helper");
        std::fs::write(&helper, "#!/bin/sh\ncase \"$1\" in\ncopy) cat > \"$2\";;\npaste) cat \"$2\";;\nfail) exit 7;;\nhang) exec sleep 10;;\ninvalid) printf '\\377';;\nesac\n").unwrap();
        std::fs::set_permissions(&helper, std::fs::Permissions::from_mode(0o700)).unwrap();
        let store = dir.path().join("clipboard");
        for text in ["", "é界\\literal\nsecond\r\n", "one\ntwo\n"] {
            run(
                Command::new(&helper).arg("copy").arg(&store),
                Some(text),
                TIMEOUT,
            )
            .unwrap();
            assert_eq!(
                run(
                    Command::new(&helper).arg("paste").arg(&store),
                    None,
                    TIMEOUT
                )
                .unwrap(),
                text
            );
        }
        assert!(run(Command::new(&helper).arg("fail"), None, TIMEOUT).is_err());
        assert!(run(Command::new(&helper).arg("invalid"), None, TIMEOUT).is_err());
        assert_eq!(
            run(
                Command::new(&helper).arg("hang"),
                None,
                Duration::from_millis(30)
            )
            .unwrap_err()
            .kind(),
            io::ErrorKind::TimedOut
        );
        assert_eq!(
            run(&mut Command::new(dir.path().join("missing")), None, TIMEOUT)
                .unwrap_err()
                .kind(),
            io::ErrorKind::NotFound
        );
        let denied = dir.path().join("not-executable");
        std::fs::write(&denied, "#!/bin/sh\nexit 0\n").unwrap();
        std::fs::set_permissions(&denied, std::fs::Permissions::from_mode(0o600)).unwrap();
        assert_eq!(
            run(&mut Command::new(denied), None, TIMEOUT)
                .unwrap_err()
                .kind(),
            io::ErrorKind::PermissionDenied
        );
        assert!(
            run(
                Command::new(&helper).arg("copy"),
                Some(&"x".repeat(LIMIT + 1)),
                TIMEOUT
            )
            .is_err()
        );
        std::fs::write(&store, vec![b'x'; LIMIT + 1]).unwrap();
        assert!(
            run(
                Command::new(&helper).arg("paste").arg(&store),
                None,
                TIMEOUT
            )
            .is_err()
        );
    }
}
