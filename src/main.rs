mod actions;
mod buffer;
mod clipboard;
mod editor;
mod explorer;
mod file;
mod fileops;
mod help;
mod lsp;
mod picker;
mod reflow;
mod render;
mod semantic;
mod syntax;
mod theme;
use crossterm::{
    cursor::Show,
    event::{self, Event, KeyEventKind},
    execute,
    style::ResetColor,
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
            ResetColor,
            LeaveAlternateScreen
        );
        let _ = terminal::disable_raw_mode();
    }
}
fn run() -> io::Result<()> {
    let mut args = std::env::args_os().skip(1);
    let path = args.next();
    if args.next().is_some() {
        return Err(io::Error::other("usage: coigue [file]"));
    }
    if path.as_deref().is_some_and(|p| p == "--help" || p == "-h") {
        println!(
            "Usage: coigue [file]\nWithout a file, welcome opens in the current directory.\nCtrl-O / - / :explore browse; Ctrl-P / :open find files; F1 / :help guide; :q quit."
        );
        return Ok(());
    }
    let mut editor = if let Some(path) = path {
        let (file, text) = file::File::open(PathBuf::from(path))?;
        editor::Editor::new(file, &text)
    } else {
        editor::Editor::welcome(std::env::current_dir()?)
    };
    let _terminal = Terminal::enter()?;
    editor.lsp.enable();
    let mut view = render::View::default();
    loop {
        editor.poll_lsp();
        if let Some(picker) = &mut editor.picker {
            picker.poll();
        }
        if let Some(explorer) = &mut editor.explorer {
            explorer.poll();
            explorer.fit(view.page_rows());
        }
        view.draw(&editor)?;
        if !event::poll(std::time::Duration::from_millis(50))? {
            continue;
        }
        match event::read()? {
            Event::Key(k) if k.kind != KeyEventKind::Release => {
                // Resize notifications can arrive after a queued key. Page by
                // the current terminal height rather than the last drawn frame.
                let page_rows = usize::from(terminal::size()?.1.saturating_sub(2)).max(1);
                if editor.key(k, page_rows) {
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
            ResetColor,
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
