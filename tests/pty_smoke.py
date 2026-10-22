"""Unix PTY integration test; no third-party Python packages."""
import fcntl
import os
import pathlib
import pty
import re
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time

binary = str(pathlib.Path(sys.argv[1]).resolve())

def session(path, steps, command=None, cwd=None, width=80, env_overrides=None):
    master, slave = pty.openpty()
    original = termios.tcgetattr(slave)
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 24, width, 0, 0))
    env = dict(os.environ)
    env.pop('NO_COLOR', None)  # Exercise color output even in color-disabled CI hosts.
    env.update(env_overrides or {})
    proc = subprocess.Popen(command or ([binary] if path is None else [binary, str(path)]), stdin=slave, stdout=slave, stderr=slave, env=env, cwd=cwd)
    output = bytearray()
    def drain():
        deadline = time.monotonic() + .15
        while time.monotonic() < deadline:
            if select.select([master], [], [], .02)[0]:
                output.extend(os.read(master, 65536))
    try:
        drain()
        if command is None:
            assert proc.poll() is None
        for keys in steps:
            expected = None
            if isinstance(keys, tuple):
                keys, expected = keys
            beginning = len(output)
            if keys is None:
                for height, width in [(1, 1), (8, 16)]:
                    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', height, width, 0, 0))
                    proc.send_signal(signal.SIGWINCH)
                    drain()
            else:
                os.write(master, keys)
            drain()
            if expected is not None:
                deadline = time.monotonic() + 5
                while expected not in output[beginning:] and time.monotonic() < deadline:
                    drain()
                assert expected in output[beginning:], (expected, output[-6000:])
        assert proc.wait(timeout=3) == 0, output
        drain()
        restored = termios.tcgetattr(slave)
        # macOS can set PENDIN while replaying input after raw mode. It is a
        # transient kernel flag, not a changed terminal setting.
        pending_input = getattr(termios, 'PENDIN', 0)
        restored[3] &= ~pending_input
        original[3] &= ~pending_input
        assert restored == original, 'terminal attributes not restored'
        assert b'\x1b[?1049l' in output, 'alternate screen not restored'
        assert b'\x1b[0m\x1b[?1049l' in output, 'colors not reset on exit'
        return output
    finally:
        if proc.poll() is None:
            proc.kill()
            proc.wait()
        os.close(master)
        os.close(slave)

with tempfile.TemporaryDirectory() as directory:
    # All clipboard commands resolve to fixtures: never touch the user's clipboard.
    helpers = pathlib.Path(directory) / 'clipboard-helpers'
    helpers.mkdir()
    store = pathlib.Path(directory) / 'fixture-clipboard'
    script = f'#!{sys.executable}\n' + """import os, pathlib, sys
if os.environ.get('COIGUE_CLIPBOARD_FAIL') == '1': sys.exit(7)
name = pathlib.Path(sys.argv[0]).name
copy = name in ('wl-copy', 'pbcopy') or '-in' in sys.argv or '--input' in sys.argv
store = pathlib.Path(os.environ['COIGUE_CLIPBOARD_STORE'])
if copy: store.write_bytes(sys.stdin.buffer.read())
else: sys.stdout.buffer.write(store.read_bytes())
"""
    for name in ['wl-copy', 'wl-paste', 'pbcopy', 'pbpaste', 'xclip', 'xsel']:
        helper = helpers / name
        helper.write_text(script)
        helper.chmod(0o700)
    fixture_env = dict(PATH=str(helpers),
                       WAYLAND_DISPLAY='coigue-fixture', DISPLAY='',
                       COIGUE_CLIPBOARD_STORE=str(store))
    path = pathlib.Path(directory) / 'clipboard.md'
    path.write_text('aé界z\nlast')
    session(path, [b'v2lY', b':clipboard-copy\r', b'$', b':clipboard-paste\r', b'u', b'\x12', b':wq\r'], env_overrides=fixture_env)
    assert path.read_text() == 'aé界zaé界\nlast'
    assert store.read_bytes() == 'aé界'.encode()
    path.write_text('one\ntwo\nlast')
    session(path, [b'VjY', b'G', b':clipboard-paste\r', b':wq\r'], env_overrides=fixture_env)
    assert path.read_text() == 'one\ntwo\nlast\none\ntwo'
    # External Unicode/multiline text is character-wise, and pasting keeps yanks.
    store.write_bytes('界\r\nnext\n'.encode())
    path.write_text('abc')
    x11_env = dict(fixture_env, WAYLAND_DISPLAY='', DISPLAY='coigue-fixture')
    session(path, [b'yy', b':clipboard-paste-before\r', b'u', b'p', b':wq\r'], env_overrides=x11_env)
    assert path.read_text() == 'abc\nabc'
    # With a fixture-only PATH, missing xclip safely falls back to fixture xsel.
    (helpers / 'xclip').unlink()
    store.write_bytes('é界'.encode())
    path.write_text('abc')
    session(path, [b':clipboard-paste-before\r', b':wq\r'], env_overrides=x11_env)
    assert path.read_text() == 'é界abc'
    store.write_bytes(b'')
    path.write_text('unchanged')
    output = session(path, [b':clipboard-paste\r', b':wq\r'], env_overrides=fixture_env)
    assert path.read_text() == 'unchanged' and b'clipboard is empty' in output
    failed_env = dict(fixture_env, COIGUE_CLIPBOARD_FAIL='1')
    path.write_text('abc')
    output = session(path, [b'vY', b':clipboard-paste\r', b'p', b':wq\r'], env_overrides=failed_env)
    assert path.read_text() == 'aabc'
    assert b'copy failed' in output and b'paste failed' in output
    assert b'\x1b]52;' not in output
    print('Clipboard PTY: fixture backends, visual Y, line/character paste, Unicode, undo, empty and errors verified')
    path = pathlib.Path(directory) / 'visual.md'
    path.write_text('aé界z\n\nlast\n')
    output = session(path, [b'v2ly', b'$p', b'u', b'gg0v2ld', b'u', b'\x12', b':wq\r'])
    assert path.read_text() == 'z\n\nlast\n'
    assert b'\x1b[7m' in output and b'Visual' in output
    path.write_text('one\ntwo\n\nlast')
    session(path, [b'Vjy', b'Gp', b'u', b'ggVjd', b'u', b'\x12', b':wq\r'])
    assert path.read_text() == '\nlast'
    print('Visual PTY: highlighting, Unicode yank/paste, line selection, delete and undo/redo verified')
    path = pathlib.Path(directory) / 'reflow.md'
    path.write_text('one two three four\n\nuntouched paragraph\n')
    session(path, [b':textwidth 10\r', b'gq', b'u', b'\x12', b':wq\r'])
    assert path.read_text() == 'one two\nthree four\n\nuntouched paragraph\n'

with tempfile.TemporaryDirectory() as directory:
    first = pathlib.Path(directory) / 'search-first.txt'
    second = pathlib.Path(directory) / 'search-second.txt'
    first.write_text('original')
    second.write_text('😀 prefix needle suffix\nnext')
    out = session(first, [b'i!', b'\x1b', b'\x1b[15~', b'needle', b'\x1bOP', b'\x1b',
                          b'\r', b'c', b'\r', b's', b'i#', b'\x1b', b':w\r',
                          b':back\r', b':wq\r'], cwd=directory, width=160)
    assert first.read_text() == '!original'
    assert second.read_text() == '😀 prefix #needle suffix\nnext'
    assert b'Project search' in out and b'Code navigation' in out
    print('Project search PTY: live search, help, Unicode target, dirty cancel/save and return verified')

with tempfile.TemporaryDirectory() as directory:
    first = pathlib.Path(directory) / 'nav-first'
    second = pathlib.Path(directory) / 'nav-second'
    first.write_text('first\nsecond\nthird')
    second.write_text('alpha\nbeta')
    out = session(first, [b':3\r', b'\x10', b'nav-second', b'\r', b'i!', b'\x1b',
                          b':back\r', b'c', b':back\r', b's', b'i#', b'\x1b',
                          b':w\r', b':forward\r', b':recent\r', b'nav-first', b'\r',
                          None, b':wq\r'], cwd=directory, width=120)
    assert first.read_text() == 'first\nsecond\n#third'
    assert second.read_text() == '!alpha\nbeta'
    assert b'Recent files' in out
    print('History navigation PTY: positions, dirty cancel/save, back/forward, recent files and resize verified')

with tempfile.TemporaryDirectory() as directory:
    path = pathlib.Path(directory) / 'actions'
    path.write_text('first\nsecond\nthird')
    out = session(path, [b'iONE', b'\x1bOQ', b'no such action', b'\r', b'\x1b', b'TWO',
                         b'\x1b', b'u', b'\x1bOR', b'last line', b'\r',
                         b'i!', b'\x1b', b'\x1bOS', b'getting started', b'\r',
                         b'\x1b', b':wq\r'], width=120)
    assert path.read_text() == 'first\nsecond\n!third'
    for text in [b'F2 Files', b'F3 Code', b'F1 Help', b'No matching actions', b'Help | Getting started']:
        assert text in out, (text, out)
    print('Actions PTY: visible menus, filtering, help, navigation and canceled Insert group verified')

with tempfile.TemporaryDirectory() as directory:
    path = pathlib.Path(directory) / 'help'
    path.write_text('original')
    out = session(path, [b'iONE', b'\x1bOP', b'\t', b'\x1b', b'TWO',
                         b'\x1b', b'u', b':help\r', None, b'\x1b', b':wq\r'])
    assert path.read_text() == 'original', 'help split the insert undo group'
    assert b'Help |' in out and b'Editing and movement' in out
    print('Help PTY: F1, topics, command, resize, return and grouped undo verified')

with tempfile.TemporaryDirectory() as directory:
    path = pathlib.Path(directory) / 'go-to-line'
    path.write_text('\n'.join(f'line{n}' for n in range(1, 61)))
    out = session(path, [b':42\r', b'i@', b'\x1b', b':1', b'\x1b',
                         b':999999\r', b'i#', b'\x1b', b':0\r',
                         b':1\r', b'i^', b'\x1b', None, b':wq\r'])
    expected = [f'line{n}' for n in range(1, 61)]
    expected[0] = '^line1'
    expected[41] = '@line42'
    expected[59] = '#line60'
    assert path.read_text() == '\n'.join(expected)
    assert b'Invalid line number' in out
    print('Go-to-line PTY: numeric commands, EOF clamp, cancellation and scrolling verified')

with tempfile.TemporaryDirectory() as directory:
    path = pathlib.Path(directory) / 'counts'
    path.write_text('one two three four five six seven\nb\nc\nd\ne')
    out = session(path, [b'2d3w', b'u', b'3j', b'2dd', b'u', b'gg',
                         b'2>>', b'u', b'2yy', b'G2p', None, b':wq\r'])
    assert path.read_text() == 'one two three four five six seven\nb\nc\nd\ne\none two three four five six seven\nb\none two three four five six seven\nb'
    assert b'1 ' in out and b'5 ' in out, 'line numbers missing'
    print('Counts and line numbers PTY: operators, undo, block paste and resize verified')

with tempfile.TemporaryDirectory() as directory:
    root = (pathlib.Path(directory) / 'launch space café 界').resolve()
    root.mkdir()
    out = session(None, [b'iaccidental', b':w\r', None, b':q\r'], cwd=root, width=200)
    for expected in ['Coigüe', 'Welcome', str(root), 'Ctrl-O', 'Ctrl-P', 'No file open']:
        assert expected.encode() in out, (expected, out)
    assert list(root.iterdir()) == [], 'welcome created a file'
    path = root / 'hello café.txt'
    path.write_text('hello')
    out = session(None, [b'-', b'/hello', b'\r', b'i!', b'\x1b', b':wq\r'], cwd=root, width=200)
    assert path.read_text() == '!hello'
    assert str(root).encode() in out
    out = session(None, [b'\x10', b'hello', b'\x1b', b'\x0f', b'\x1b',
                         b':explore\r', b'a', 'new 界.txt'.encode(), b'\r',
                         b'\r', b'iNEW', b'\x1b', b':wq\r'], cwd=root)
    assert (root / 'new 界.txt').read_text() == 'NEW'
    assert b'Welcome' in out
    assert sorted(p.name for p in root.iterdir()) == ['hello café.txt', 'new 界.txt']
    print('Welcome PTY: launch cwd, Unicode/spaces, no writes, browsers, creation and save verified')

with tempfile.TemporaryDirectory() as directory:
    path = pathlib.Path(directory) / 'fixture'
    out = session(path, [b'ihello', b'\x1b', b':q\r', None,
                         b'Z', b'0i', 'é'.encode(), b'\x1b', b':w\r',
                         b'oagain', b'\x1b', b':wq\r'])
    assert b'Unsaved' in out
    assert path.read_text() == 'éhello\nagain', path.read_bytes()
    session(path, [b'G0x', b':wq\r'])
    assert path.read_text() == 'éhello\ngain'
    session(path, [b'iDISCARD', b'\x1b', b':q!\r'])
    assert path.read_text() == 'éhello\ngain'
    path.write_text(''.join(f'line {n} ' + '界' * 30 + '\n' for n in range(40)))
    before = path.read_text()
    session(path, [b'Gk$a!', b'\x1b', None, b'gg', b':wq\r'])
    lines = before.split('\n')
    lines[-2] += '!'
    assert path.read_text() == '\n'.join(lines)
    nav_path = pathlib.Path(directory) / 'navigation'
    nav_path.write_text('\n'.join(f'row{n:02d}' for n in range(60)))
    nav_out = session(nav_path, [
        b'\x1b[6~',          # Page Down: row 22 in the 22-row viewport.
        b'\x1b[F', b'a!', b'\x1b',
        b'\x1b[5~',          # Page Up: back to row 0.
        b'\x1b[H', b'i^', b'\x1b',
        b'\x06',             # Ctrl-F: row 22.
        b'\x01', b'i@', b'\x05', b'?', b'\x1b',
        None,                # Resize: six file rows in the final viewport.
        b'\x1b[6~', b'\x1b[H', b'i#', b'\x1b',
        b'\x02', b'\x1b[F', b'a+', b'\x1b', b':wq\r',
    ])
    expected = [f'row{n:02d}' for n in range(60)]
    expected[0] = '^row00'
    expected[22] = '@row22!?+'
    expected[28] = '#row28'
    assert nav_path.read_text() == '\n'.join(expected), nav_path.read_text()
    assert b'\x1b[2J' not in nav_out, 'navigation cleared the entire screen'
    assert b'\x1b[?2026h' in nav_out and b'\x1b[?2026l' in nav_out
    editing_path = pathlib.Path(directory) / 'editing.rs'
    editing_path.write_text('alpha\né界\nomega')
    edit_out = session(editing_path, [
        b'yy', b'p', b'dd', b'P', b'>>', b'<<', b'A!', b'\x1b',
        b'/omega\r', b'rO', b'n', b'/alpha\r', b'n', b'N',
        b'/cancel', b'\x1b', b'j', b'x', b'P', b':wq\r',
    ])
    assert editing_path.read_text() == 'alpha\nalpha!\né界\nOmega', editing_path.read_text()
    assert b'/omega' in edit_out and b'Not found: omega' in edit_out
    assert b'\x1b[2J' not in edit_out
    print('Editing PTY: copy/cut/paste, replacement, literal search, indentation and append verified')
    history_path = pathlib.Path(directory) / 'history.rs'
    history_path.write_text('é界\n')
    history_out = session(history_path, [b'A!', b'\x1b', b':w\r', b'u', b':q\r', b'\x12', b':q\r'])
    assert b'Unsaved changes' in history_out
    assert history_path.read_text() == 'é界!\n'
    session(history_path, [b'ohello\rworld', b'\x7f', b'\x1b', b'u', b'\x12', b'u', b':wq\r'])
    assert history_path.read_text() == 'é界!\n'
    branch_out = session(history_path, [b'x', b'u', b'ru', b'\x12', b':wq\r'])
    assert b'Nothing to redo' in branch_out
    assert history_path.read_text() == 'u界!\n'
    print('History PTY: insert grouping, undo/redo, save/quit safeguards and branching verified')
    picker_root = pathlib.Path(directory) / 'picker-root'
    picker_root.joinpath('nested').mkdir(parents=True)
    picked = picker_root / 'nested' / 'café.rs'
    picked.write_text('fn picked() {}')
    picker_root.joinpath('.gitignore').write_text('ignored-only.rs\n')
    ignored = picker_root / 'ignored-only.rs'
    ignored.write_text('excluded')
    ignored_out = session(path, [b'\x10', b'ignored-only', b'\r', b'\x1b', b':q\r'], cwd=picker_root)
    assert b'No matches' in ignored_out
    assert ignored.read_text() == 'excluded'
    # The initial file is outside cwd: discovery must use cwd, not its parent.
    original = path.read_text()
    picker_out = session(path, [b'\x10', b'n/cf', b'\r', b'$a!', b'\x1b', b':wq\r'], cwd=picker_root)
    assert picked.read_text() == 'fn picked() {}!'
    assert path.read_text() == original
    assert 'nested/café.rs'.encode() in picker_out
    assert b'\x1b[2J' not in picker_out
    session(picked, [b'\x10', b'no-match', b'\r', b'\x7f', b'\x1b', b':open\r', None,
                     b'\x1b', b'0iX', b'\x1b', b'\x10', b'\x1b', b':wq\r'], cwd=picker_root)
    assert picked.read_text() == 'Xfn picked() {}!'
    print('Picker PTY: cwd recursion, fuzzy query, open/save, cancel, resize and unsaved protection verified')
    print('Navigation PTY: Home/End, Page Up/Down, Ctrl-A/E/B/F and resize verified')
    explorer_root = pathlib.Path(directory) / 'explorer-root'
    explorer_root.joinpath('nested').mkdir(parents=True)
    explorer_start = explorer_root / 'start.txt'
    explorer_target = explorer_root / 'nested' / 'café.rs'
    explorer_start.write_text('start')
    explorer_target.write_text('fn target() {}')
    explorer_root.joinpath('.gitignore').write_text('ignored.txt\n')
    explorer_root.joinpath('ignored.txt').write_text('hidden')
    explorer_out = session(explorer_start, [
        b'A!', b'\x1b', b'-', b'/nested', b'\r', b'h', b'l',
        b'/cf', b'\r', b'c', b'\r', b's', b'A!', b'\x1b', b':wq\r',
    ], cwd=explorer_root)
    assert explorer_start.read_text() == 'start!'
    assert explorer_target.read_text() == 'fn target() {}!'
    assert b'Unsaved |' in explorer_out
    assert b'\x1b[2J' not in explorer_out
    discard_out = session(explorer_start, [
        b'A-discard', b'\x1b', b':explore\r', b'.', b'.', b'/nested', b'\r',
        b'/cf', b'\r', b'd', b':q\r',
    ], cwd=explorer_root)
    assert explorer_start.read_text() == 'start!'
    assert b'ignored.txt' in discard_out
    cancel_out = session(explorer_start, [b'-', b'/absent', None, b'\x1b', b'\x1b', b':q\r'], cwd=explorer_root)
    assert b'No matches' in cancel_out
    print('Explorer PTY: directory navigation, filter, resize, visibility, save/discard/cancel verified')
    ops_root = pathlib.Path(directory) / 'ops-root'
    ops_root.mkdir()
    ops_active = ops_root / 'active.txt'
    ops_active.write_text('active')
    ops_out = session(ops_active, [
        (b'\x0f', b'> active.txt'), b'Abox', (b'\r', b'> box/'),
        b'a', 'café.txt'.encode(), (b'\r', '> café.txt'.encode()),
        b'r', b'\x15', b'box/moved.txt', b'\r',
        (b'/box', b'> box/'), b'\t', b'd', b'\r', b'n', b'd', b'y', b'\x1b', b':q\r',
    ], cwd=ops_root)
    assert not ops_root.joinpath('box').exists(), ops_out[-6000:]
    assert list(ops_root.joinpath('.coigue-trash').glob('entry-*/item/moved.txt'))
    # Restart: restoration is discovered from persistent records.
    session(ops_active, [b'\x0f', b'u', b'\x1b', b':q\r'], cwd=ops_root)
    assert ops_root.joinpath('box/moved.txt').read_text() == ''
    assert not ops_root.joinpath('café.txt').exists()
    # Rename the active file while dirty, save to the new path, then undo.
    session(ops_active, [b'A!', b'\x1b', (b'\x0f', b'> active.txt'), b'r', b'\x15', b'new.rs', b'\r',
                         b'\x1b', b':w\r', b'u', b':wq\r'], cwd=ops_root)
    assert not ops_active.exists()
    assert ops_root.joinpath('new.rs').read_text() == 'active'
    assert b'\x1b[2J' not in ops_out
    print('Filesystem PTY: shortcut, create, move, delete confirmation, persistent restore, active rename/save/undo verified')
    rust_path = pathlib.Path(directory) / 'fixture.rs'
    rust_path.write_text('fn main() {\n\tlet café = r#"界"#; // comment\n}\n' + '// scrolling\n' * 40)
    rust_out = session(rust_path, [b'j$a!', b'\x1b', b'G', None, b'gg', b':wq\r'])
    assert b'\x1b[38;2;203;166;247mfn\x1b[38;2;205;214;244m main() {' in rust_out, 'incorrect rendered keyword text'
    assert '    \x1b[38;2;203;166;247mlet\x1b[38;2;205;214;244m café = \x1b[38;2;166;227;161mr#"界"#'.encode() in rust_out, 'incorrect tab/Unicode/string rendering'
    assert b'\x1b[48;2;30;30;46m' in rust_out, 'missing Mocha background'
    assert b'\x1b[1;4H' in rust_out, 'cursor did not return to first text cell after gutter'
    assert 'comment!' in rust_path.read_text()
    print('Rust PTY: colored fn/let/raw string, four-cell tab and Unicode text verified')
    markdown_path = pathlib.Path(directory) / 'fixture.MARKDOWN'
    markdown_text = '# Heading\n*café* plain **bold**\n```rust\nfn main() {}\n```\n'
    markdown_path.write_text(markdown_text)
    markdown_out = session(markdown_path, [b':3\r', b'dd', b'u', b'\x12', b'u',
                                           b':theme terminal\r', None, b':q\r'])
    assert b'\x1b[38;2;203;166;247m\x1b[1m# Heading\x1b[22m' in markdown_out, 'Markdown heading not highlighted'
    assert '\x1b[3m*café*\x1b[38;2;205;214;244m\x1b[23m plain '.encode() in markdown_out, 'Markdown italic text or attribute reset missing'
    assert b'\x1b[1m**bold**\x1b[22m' in markdown_out, 'Markdown strong emphasis not bold'
    assert b'\x1b[38;2;203;166;247mfn' in markdown_out, 'Rust fence not highlighted'
    assert b'\x1b[38;5;13m\x1b[1m# Heading' in markdown_out, 'Markdown theme switch not rendered'
    assert markdown_path.read_text() == markdown_text, 'Markdown highlighting/history changed file contents'
    print('Markdown PTY: headings, emphasis, fenced Rust, undo/redo, theme and resize verified')
    plain_out = session(path, [b':q\r'])
    assert b'\x1b[38;2;127;132;156m' in plain_out, 'line-number gutter missing'
    assert b'\x1b[38;2;203;166;247m' not in plain_out, 'plain text received syntax highlighting'
    theme_path = pathlib.Path(directory) / 'themes.rs'
    theme_text = 'fn main() {}\n'
    theme_path.write_text(theme_text)
    completion_out = session(theme_path, [b':the\t m\t', b'\t', b'\x1b[Z\r',
                                         b':theme catppuccin-l\t\r', b':w\t\t',
                                         b'\x1b', b':q\t\t\x1b[Z\r'])
    for expected in [b':theme mocha', b':theme monochrome', b'Theme: catppuccin-mocha',
                     b'Theme: catppuccin-latte', b':wq', b':q!']:
        assert expected in completion_out, (expected, completion_out)
    assert theme_path.read_text() == theme_text, 'completion changed the file'
    completion_root = pathlib.Path(directory) / 'completion-welcome'
    completion_root.mkdir()
    welcome_completion_out = session(None, [b':the\t term\t\r', b':q\r'], cwd=completion_root)
    assert b'Theme: terminal' in welcome_completion_out
    assert list(completion_root.iterdir()) == []
    print('Command completion PTY: Tab, Shift-Tab, theme aliases, cancel and welcome verified')
    theme_out = session(theme_path, [b':theme\r', b':theme gruvbox-dark\r',
                                   b':theme catppuccin-latte\r', b':theme nope\r',
                                   b':theme mocha extra\r',
                                   b':theme terminal\r', None, b':theme mocha', b'\x1b', b':q\r'])
    for expected in [b'Theme: catppuccin-mocha', b'Theme: gruvbox-dark',
                     b'Theme: catppuccin-latte', b'Theme: terminal',
                     b'Unknown theme: nope', b'Usage: :theme']:
        assert expected in theme_out, (expected, theme_out)
    assert b'\x1b[48;2;40;40;40m' in theme_out, 'missing Gruvbox background'
    assert b'\x1b[48;2;239;241;245m' in theme_out, 'missing Latte background'
    terminal_out = theme_out.split(b'Theme: terminal', 1)[1]
    assert b'\x1b[49m' in terminal_out, 'terminal background not restored'
    # Function names may also receive semantic colors from an installed server.
    assert b'\x1b[38;5;13mfn' in terminal_out, 'terminal ANSI syntax not restored'
    assert b'\x1b[48;2;' not in terminal_out, 'canceling a theme command changed the palette'
    assert theme_path.read_text() == theme_text, 'theme commands changed the file'
    monochrome_out = session(theme_path, [b':theme monochrome\r', None, b':q\r'])
    monochrome_out = monochrome_out.split(b'Theme: monochrome', 1)[1]
    colors = re.findall(rb'\x1b\[(?:38|48);2;(\d+);(\d+);(\d+)m', monochrome_out)
    assert colors and all(r == g == b for r, g, b in colors), 'monochrome emitted a colored hue'
    assert b'\x1b[48;2;8;8;8m' in monochrome_out, 'missing monochrome background'
    assert b'\x1b[1m' in monochrome_out, 'monochrome keywords lack bold emphasis'
    assert b'\x1b[22m' in monochrome_out, 'bold emphasis leaked beyond syntax'
    restarted = session(theme_path, [b':q\r'])
    assert b'\x1b[48;2;30;30;46m' in restarted, 'theme persisted after restart'
    theme_welcome = pathlib.Path(directory) / 'theme-welcome'
    theme_welcome.mkdir()
    session(None, [b':theme latte\r', b'\x0f', b'\x1b', b':q\r'], cwd=theme_welcome)
    assert list(theme_welcome.iterdir()) == [], 'theme selection created a config file'
    print('Themes PTY: palettes, command errors/cancel, resize, ANSI fallback, no persistence and color cleanup verified')
    path.write_bytes('café_2!! \t\r\n\n界 end'.encode())
    session(path, [b'wyw', b'u', b'dwP', b'ggcwNEW', b'\x1b', b'u', b'\x12', b':wq\r'])
    assert path.read_bytes() == 'NEW!! \t\r\n\n界 end'.encode()
    session(path, [b'G$dw', b'u', b'ggwwbew', b':wq\r'])
    assert path.read_bytes() == 'NEW!! \t\r\n\n界 end'.encode()
    session(path, [b'\x0f', b'wbecw', b'/dwcwbywe', b'\x1b',
                   b'acwbywe', b'\x1b', b'\x1b', b':wq\r'], cwd=directory)
    assert path.read_bytes() == 'NEW!! \t\r\n\n界 end'.encode()
    print('Word PTY: Unicode, punctuation, cross-line register, EOF and grouped undo/redo verified')
    if pathlib.Path("/dev/full").exists():
        # A write-only failure during alternate-screen entry must restore raw mode.
        master, slave = pty.openpty()
        original = termios.tcgetattr(slave)
        with open('/dev/full', 'wb') as full:
            proc = subprocess.Popen([binary, str(path)], stdin=slave, stdout=full, stderr=slave)
            assert proc.wait(timeout=3) == 1
            assert termios.tcgetattr(slave) == original
        os.close(master)
        os.close(slave)
    else:
        print("SKIP: /dev/full output-failure test (device unavailable)")
    # Exercise guard unwinding for returned errors and panics in a real terminal.
    guard = pathlib.Path(binary).parent / 'deps' / 'coigue-terminal-guard'
    assert guard.is_file(), 'run make build first'
    session(path, [], [str(guard)])
print('PTY smoke passed: repeated edits, save/reopen, resize, quit protection, cleanup')
