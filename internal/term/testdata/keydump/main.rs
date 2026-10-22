use crossterm::event::{self, Event, KeyCode, KeyEventKind, KeyModifiers};
use crossterm::terminal;
use std::io::Write;
use std::time::Duration;

fn code(c: KeyCode) -> String {
    match c {
        KeyCode::Char(c) => format!("c:{:X}", c as u32),
        KeyCode::F(n) => format!("f:{n}"),
        other => format!("{other:?}"),
    }
}

fn main() {
    terminal::enable_raw_mode().unwrap();
    let mut out = std::io::stdout();
    write!(out, "READY\r\n").unwrap();
    out.flush().unwrap();
    // Quiet period after the last event ends the run.
    while event::poll(Duration::from_millis(400)).unwrap() {
        match event::read().unwrap() {
            Event::Key(k) => {
                let kind = match k.kind {
                    KeyEventKind::Press => "press",
                    KeyEventKind::Repeat => "repeat",
                    KeyEventKind::Release => "release",
                };
                write!(out, "key {} {} {}\r\n", code(k.code), k.modifiers.bits(), kind).unwrap();
            }
            Event::Paste(s) => {
                let hex: String = s.bytes().map(|b| format!("{b:02X}")).collect();
                write!(out, "paste {hex}\r\n").unwrap();
            }
            Event::Resize(..) => write!(out, "resize\r\n").unwrap(),
            _ => write!(out, "other\r\n").unwrap(),
        }
        out.flush().unwrap();
    }
    let _ = KeyModifiers::NONE;
    terminal::disable_raw_mode().unwrap();
    write!(out, "DONE\r\n").unwrap();
}
