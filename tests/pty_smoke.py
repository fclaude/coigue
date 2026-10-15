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

def session(path, steps, command=None):
    master, slave = pty.openpty()
    original = termios.tcgetattr(slave)
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 24, 80, 0, 0))
    env = dict(os.environ)
    env.pop('NO_COLOR', None)  # Exercise color output even in color-disabled CI hosts.
    proc = subprocess.Popen(command or [binary, str(path)], stdin=slave, stdout=slave, stderr=slave, env=env)
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
    path = pathlib.Path(directory) / 'fixture'
    out = session(path, [b'ihello', b'\x1b', b':q\r', None,
                         b'A', b'0i', 'é'.encode(), b'\x1b', b':w\r',
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
    print('Navigation PTY: Home/End, Page Up/Down, Ctrl-A/E/B/F and resize verified')
    rust_path = pathlib.Path(directory) / 'fixture.rs'
    rust_path.write_text('fn main() {\n\tlet café = r#"界"#; // comment\n}\n' + '// scrolling\n' * 40)
    rust_out = session(rust_path, [b'j$a!', b'\x1b', b'G', None, b'gg', b':wq\r'])
    assert b'\x1b[38;5;13mfn\x1b[39m main() {' in rust_out, 'incorrect rendered keyword text'
    assert '    \x1b[38;5;13mlet\x1b[39m café = \x1b[38;5;10mr#"界"#'.encode() in rust_out, 'incorrect tab/Unicode/string rendering'
    assert b'\x1b[39m' in rust_out, 'missing foreground reset'
    assert b'\x1b[1;1H' in rust_out, 'cursor did not return to first cell'
    assert 'comment!' in rust_path.read_text()
    print('Rust PTY: colored fn/let/raw string, four-cell tab and Unicode text verified')
    plain_out = session(path, [b':q\r'])
    assert b'\x1b[38;5;' not in plain_out, 'plain file was highlighted'
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
