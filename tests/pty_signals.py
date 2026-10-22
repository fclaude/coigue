"""Unix PTY checks for the Go editor's exit paths that tests/pty_smoke.py does not cover.

usage: python3 tests/pty_signals.py BINARY

The Rust editor leaves the terminal in raw mode on the alternate screen when it
is killed by SIGTERM or SIGHUP (oddity ODD-03 in docs/port-parity.md). The Go
editor restores the terminal first and then dies of the same signal. Ctrl-C is
an ordinary key in both (raw mode clears ISIG)."""
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


def start(path):
    master, slave = pty.openpty()
    original = termios.tcgetattr(slave)
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 24, 80, 0, 0))
    env = dict(os.environ, TERM='xterm-256color')
    proc = subprocess.Popen([binary, str(path)], stdin=slave, stdout=slave, stderr=slave, env=env)
    output = bytearray()

    def drain(duration=.3):
        end = time.monotonic() + duration
        while time.monotonic() < end:
            if select.select([master], [], [], .02)[0]:
                try:
                    output.extend(os.read(master, 65536))
                except OSError:
                    return

    drain(.5)
    return proc, master, slave, original, output, drain


def finish(master, slave):
    os.close(master)
    os.close(slave)


with tempfile.TemporaryDirectory() as directory:
    path = pathlib.Path(directory) / 'file.txt'
    path.write_text('hello\n')

    for sig in (signal.SIGTERM, signal.SIGHUP, signal.SIGINT):
        proc, master, slave, original, output, drain = start(path)
        proc.send_signal(sig)
        status = proc.wait(timeout=3)
        drain(.2)
        restored = termios.tcgetattr(slave)
        pending_input = getattr(termios, 'PENDIN', 0)
        restored[3] &= ~pending_input
        original[3] &= ~pending_input
        assert status == -sig, (sig, status)
        assert restored == original, f'terminal attributes not restored after {sig.name}'
        assert b'\x1b[0m\x1b[?1049l' in output, f'screen not restored after {sig.name}'
        finish(master, slave)

    # Ctrl-C is a key, not a signal: the editor keeps running and the file is untouched.
    proc, master, slave, original, output, drain = start(path)
    os.write(master, b'\x03')
    drain(.3)
    assert proc.poll() is None, 'Ctrl-C ended the editor'
    os.write(master, b':q\r')
    assert proc.wait(timeout=3) == 0
    assert path.read_text() == 'hello\n'
    finish(master, slave)

    # An Esc that arrives in the same read as the next key is Alt-key, and the editor ignores it.
    proc, master, slave, original, output, drain = start(path)
    os.write(master, b'i')
    drain(.2)
    os.write(master, b'\x1bx')
    drain(.3)
    os.write(master, b'\x1b')
    drain(.3)
    os.write(master, b':wq\r')
    assert proc.wait(timeout=3) == 0
    assert path.read_text() == 'hello\n', 'ESC x was inserted or treated as Esc + x'
    finish(master, slave)
print('PTY signals: SIGTERM/SIGHUP/SIGINT restore the terminal, Ctrl-C is a key, ESC x is Alt-x')
