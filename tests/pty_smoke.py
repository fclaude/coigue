"""Unix PTY integration test; no third-party Python packages."""
import fcntl
import os
import pathlib
import pty
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time

binary = str(pathlib.Path(sys.argv[1]).resolve())

def session(path, steps, command=None, cwd=None):
    master, slave = pty.openpty()
    original = termios.tcgetattr(slave)
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 24, 80, 0, 0))
    env = dict(os.environ)
    env.pop('NO_COLOR', None)  # Exercise color output even in color-disabled CI hosts.
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
            if keys is None:
                for height, width in [(1, 1), (8, 16)]:
                    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', height, width, 0, 0))
                    proc.send_signal(signal.SIGWINCH)
                    drain()
            else:
                os.write(master, keys)
            drain()
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
        return output
    finally:
        if proc.poll() is None:
            proc.kill()
            proc.wait()
        os.close(master)
        os.close(slave)

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
    root = pathlib.Path(directory) / 'launch space café 界'
    root.mkdir()
    out = session(None, [b'iaccidental', b':w\r', None, b':q\r'], cwd=root)
    for expected in ['Coigüe', 'Welcome', str(root), 'Ctrl-O', 'Ctrl-P', 'No file open']:
        assert expected.encode() in out, (expected, out)
    assert list(root.iterdir()) == [], 'welcome created a file'
    path = root / 'hello café.txt'
    path.write_text('hello')
    out = session(None, [b'-', b'/hello', b'\r', b'i!', b'\x1b', b':wq\r'], cwd=root)
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
        b'\x0f', b'Abox', b'\r', b'a', 'café.txt'.encode(), b'\r',
        b'r', b'\x15', b'box/moved.txt', b'\r',
        b'/box', b'\t', b'd', b'\r', b'n', b'd', b'y', b'\x1b', b':q\r',
    ], cwd=ops_root)
    assert not ops_root.joinpath('box').exists()
    assert list(ops_root.joinpath('.coigue-trash').glob('entry-*/item/moved.txt'))
    # Restart: restoration is discovered from persistent records.
    session(ops_active, [b'\x0f', b'u', b'\x1b', b':q\r'], cwd=ops_root)
    assert ops_root.joinpath('box/moved.txt').read_text() == ''
    assert not ops_root.joinpath('café.txt').exists()
    # Rename the active file while dirty, save to the new path, then undo.
    session(ops_active, [b'A!', b'\x1b', b'\x0f', b'r', b'\x15', b'new.rs', b'\r',
                         b'\x1b', b':w\r', b'u', b':wq\r'], cwd=ops_root)
    assert not ops_active.exists()
    assert ops_root.joinpath('new.rs').read_text() == 'active'
    assert b'\x1b[2J' not in ops_out
    print('Filesystem PTY: shortcut, create, move, delete confirmation, persistent restore, active rename/save/undo verified')
    rust_path = pathlib.Path(directory) / 'fixture.rs'
    rust_path.write_text('fn main() {\n\tlet café = r#"界"#; // comment\n}\n' + '// scrolling\n' * 40)
    rust_out = session(rust_path, [b'j$a!', b'\x1b', b'G', None, b'gg', b':wq\r'])
    assert b'\x1b[38;5;13mfn\x1b[39m main() {' in rust_out, 'incorrect rendered keyword text'
    assert '    \x1b[38;5;13mlet\x1b[39m café = \x1b[38;5;10mr#"界"#'.encode() in rust_out, 'incorrect tab/Unicode/string rendering'
    assert b'\x1b[39m' in rust_out, 'missing foreground reset'
    assert b'\x1b[1;4H' in rust_out, 'cursor did not return to first text cell after gutter'
    assert 'comment!' in rust_path.read_text()
    print('Rust PTY: colored fn/let/raw string, four-cell tab and Unicode text verified')
    plain_out = session(path, [b':q\r'])
    assert b'\x1b[38;5;8m' in plain_out, 'line-number gutter missing'
    assert b'\x1b[38;5;' not in plain_out.replace(b'\x1b[38;5;8m', b''), 'plain text received syntax highlighting'
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
    test_bins = [p for p in pathlib.Path(binary).parent.joinpath('deps').glob('coigue-*')
                 if p.is_file() and os.access(p, os.X_OK) and p.suffix != '.d']
    assert test_bins, 'run cargo test first'
    test_bin = next(p for p in test_bins if b'terminal_guard_restores_after_error_and_unwind'
                    in subprocess.run([str(p), '--list'], capture_output=True).stdout)
    session(path, [], [str(test_bin), '--exact',
                      'tests::terminal_guard_restores_after_error_and_unwind', '--nocapture'])
print('PTY smoke passed: repeated edits, save/reopen, resize, quit protection, cleanup')
