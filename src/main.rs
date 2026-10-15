mod buffer;
mod editor;
mod file;
mod render;
mod syntax;
use crossterm::{
    cursor::Show,
    event::{self, Event, KeyEventKind},
    execute,
    terminal::{self, EndSynchronizedUpdate, EnterAlternateScreen, LeaveAlternateScreen},
};
use std::{io, path::PathBuf};
struct Terminal;
impl Terminal {
    fn enter() -> io::Result<Self> {
        terminal::enable_raw_mode()?;
        let guard = Self;
        execute!(io::stdout(), EnterAlternateScreen)?;
        Ok(guard)
    }
}
impl Drop for Terminal {
    fn drop(&mut self) {
        let _ = execute!(
            io::stdout(),
            EndSynchronizedUpdate,
            Show,
            LeaveAlternateScreen
        );
        let _ = terminal::disable_raw_mode();
    }
}
fn run() -> io::Result<()> {
    let mut args = std::env::args_os().skip(1);
    let path = args
        .next()
        .ok_or_else(|| io::Error::other("usage: coigue <file>"))?;
    if args.next().is_some() {
        return Err(io::Error::other("exactly one file per session"));
    }
    let (file, text) = file::File::open(PathBuf::from(path))?;
    let mut editor = editor::Editor::new(file, &text);
    let _terminal = Terminal::enter()?;
    let mut view = render::View::default();
    loop {
        view.draw(&editor)?;
        match event::read()? {
            Event::Key(k) if k.kind != KeyEventKind::Release => {
                if editor.key(k, view.page_rows()) {
                    break;
                }
            }
            Event::Resize(_, _) => {}
            _ => {}
        }
    }
    Ok(())
}
fn main() {
    let previous = std::panic::take_hook();
    std::panic::set_hook(Box::new(move |info| {
        let _ = execute!(
            io::stdout(),
            EndSynchronizedUpdate,
            Show,
            LeaveAlternateScreen
        );
        let _ = terminal::disable_raw_mode();
        previous(info);
    }));
    if let Err(e) = run() {
        eprintln!("coigue: {e}");
        std::process::exit(1);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn terminal_guard_restores_after_error_and_unwind() {
        use std::io::IsTerminal;
        if !io::stdin().is_terminal() || !io::stdout().is_terminal() {
            return; // Exercised with a real PTY by pty_smoke.py.
        }
        let result = (|| -> io::Result<()> {
            let _terminal = Terminal::enter()?;
            assert!(terminal::is_raw_mode_enabled()?);
            Err(io::Error::other("injected event/render error"))
        })();
        assert!(result.is_err());
        assert!(!terminal::is_raw_mode_enabled().unwrap());
        let result = std::panic::catch_unwind(|| {
            let _terminal = Terminal::enter().unwrap();
            panic!("injected panic");
        });
        assert!(result.is_err());
        assert!(!terminal::is_raw_mode_enabled().unwrap());
    }
}
