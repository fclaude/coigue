"""Replay recorded terminal sessions against an editor binary and compare with the recordings of the original Rust editor.

usage: python3 tests/pty_golden.py BINARY            # compare against tests/golden/pty-sessions.json
       python3 tests/pty_golden.py BINARY --record   # re-record the goldens from BINARY (the Go build: it records its own output)

Each scenario starts the editor in a PTY with a fixed working directory and a
PATH that contains no language servers, sends scripted keys and resizes, and
keeps the raw bytes the editor wrote after every step. The goldens in
tests/golden/pty-sessions.json were recorded from the Rust editor (the last Rust-only
commit, ba72614; tag rust-reference holds it with the Go port). The Go editor must write identical bytes; when the byte stream
differs only in how output is batched into frames, the reconstructed screens
(text and cursor) must still be identical. Files left on disk must be equal.

The scenarios cover startup screens for each file type, motions, operators,
Visual mode, search, the command line and completion, themes, help, menus,
scrolling and gutters, tiny windows, the picker, explorer, file operations,
switch prompts, navigation history, project search, undo/redo, reflow and
unknown keys. Linux only: the status line shows the (canonical) working path.
"""
import base64
import fcntl
import json
import os
import pathlib
import pty
import re
import select
import shutil
import signal
import struct
import subprocess
import sys
import termios
import time
import unicodedata

GOLDEN = pathlib.Path(__file__).with_name('golden') / 'pty-sessions.json'
WORK = '/tmp/coigue-golden-work'
EMPTY_PATH = '/tmp/coigue-golden-empty-path'


class Screen:
    def __init__(self, w=80, h=24):
        self.w, self.h = w, h
        self.rows = [[' '] * w for _ in range(h)]
        self.attrs = [[''] * w for _ in range(h)]
        self.x = self.y = 0
        self.sgr = ''
        self.visible = True
    def put(self, ch):
        if self.x < self.w and 0 <= self.y < self.h:
            self.rows[self.y][self.x] = ch
            self.attrs[self.y][self.x] = self.sgr
        self.x += 1
    def feed(self, data):
        text = data.decode('utf-8', 'replace'); i = 0
        while i < len(text):
            c = text[i]
            if c == '\x1b':
                m = re.match(r'\x1b\[([?0-9;]*)([A-Za-z])', text[i:])
                if m:
                    args, final = m.group(1), m.group(2); i += m.end()
                    if final == 'H':
                        p = [int(a) if a else 1 for a in args.split(';')] if args else [1, 1]
                        self.y = p[0] - 1; self.x = (p[1] if len(p) > 1 else 1) - 1
                    elif final == 'K':
                        for xx in range(self.x, self.w):
                            if 0 <= self.y < self.h: self.rows[self.y][xx] = ' '; self.attrs[self.y][xx] = ''
                    elif final == 'm':
                        self.sgr = '' if args in ('', '0') else (self.sgr + ';' + args if args and self.sgr else args)
                    elif final in 'hl' and args == '?25':
                        self.visible = final == 'h'
                    continue
                i += 1; continue
            if c in '\r\n': i += 1; continue
            w = 2 if unicodedata.east_asian_width(c) in 'WF' else (0 if unicodedata.combining(c) else 1)
            if w:
                self.put(c)
                if w == 2: self.put('')
            i += 1
    def snapshot(self):
        return (tuple(''.join(r) for r in self.rows), (self.x, self.y, self.visible))

def screens(result, size):
    sc = Screen(*size)
    out = []
    for step in result['steps']:
        sc.feed(bytes.fromhex(step))
        out.append(sc.snapshot())
    return out


def lines(n, fmt='line {}'):
    return '\n'.join(fmt.format(i) for i in range(1, n + 1))


# Each scenario: name, files {relative path: content}, args, size (w, h), steps.
# A step is bytes to send, ('resize', w, h) or ('wait', seconds).
SCENARIOS = []


def scenario(name, files, args, steps, size=(80, 24), env=None):
    SCENARIOS.append(dict(name=name, files=files, args=args, steps=steps, size=size, env=env or {}))


ESC = b'\x1b'

TXT = {'a.txt': 'alpha beta gamma\nsecond line é界 text\n\nlast line here\n'}
RS = {'main.rs': 'fn main() {\n\tlet café = r#"界"#; // comment\n\tprintln!("hi {}", 1.5e3);\n}\n/* block\n   comment */ struct S<\'a>(&\'a str);\n'}
GO = {'main.go': 'package main\n\nimport "fmt"\n\n/* c */ func main() {\n\ts := `raw\nstring`\n\tfmt.Println("hi", 0x1f, \'x\')\n}\n'}
MD = {'doc.md': '# Heading\n\n*italic* and **strong** and `code` [link](http://x.y)\n\n- item one\n- [x] task\n\n> quote\n\n```rust\nfn x() {}\n```\n\nSetext\n======\n'}

scenario('plain-start', TXT, ['a.txt'], [])
scenario('rust-highlight', RS, ['main.rs'], [])
scenario('go-highlight', GO, ['main.go'], [])
scenario('markdown-highlight', MD, ['doc.md'], [])
scenario('welcome', {}, [], [])
scenario('welcome-narrow', {}, [], [('resize', 20, 8)])
scenario('motions', TXT, ['a.txt'], [b'jjl', b'w', b'e', b'b', b'$', b'0', b'G', b'gg', b'3j', b'2l', b'x', b'u'])
scenario('insert-edit', TXT, ['a.txt'], [b'ihello ', ESC, b'A!!', ESC, b'ofresh', ESC, b'u', b'u'])
scenario('visual', TXT, ['a.txt'], [b'vjl', b'o', b'jj', ESC, b'Vj', b'y', b'Gp', b'u'])
scenario('operators', TXT, ['a.txt'], [b'dw', b'2dd', b'p', b'yy', b'P', b'>>', b'<<', b'cwX', ESC, b'u', b'rZ', b'3x'])
scenario('search', TXT, ['a.txt'], [b'/line', b'\r', b'n', b'n', b'N', b'/zzz', b'\r', b'/ab', ESC])
scenario('command-line', TXT, ['a.txt'], [b':', b'wq', ESC, b'ix', ESC, b':q', b'\r', b':3', b'\r', b':99', b'\r', b':bad', b'\r', b':textwidth 10', b'\r', b'gq'])
scenario('completion', TXT, ['a.txt'], [b':th\t', b'\t', b'\x1b[Z', ESC, b':the\t l\t', b'\r', b':w\t\t', ESC])
scenario('themes', RS, ['main.rs'], [b':theme gruvbox\r', b':theme latte\r', b':theme monochrome\r', b':theme terminal\r', b':theme\r', b':theme nope\r', b':theme mocha\r'])
scenario('help-topics', TXT, ['a.txt'], [b'\x1bOP', b'\t', b'\t', b'\t', b'j', b'\x1b[6~', b'\x1b[Z', ESC])
scenario('menus', TXT, ['a.txt'], [b'\x1bOQ', b'fil', b'\t', b'\x1b[B', ESC, b'\x1bOR', b'def', ESC, b'\x1bOS', b'zzz', ESC])
scenario('scroll-long', {'long.txt': lines(80)}, ['long.txt'], [b'\x1b[6~', b'\x1b[6~', b'\x1b[5~', b'G', b'50G', b'gg', b'\x06', b'\x02'], size=(40, 12))
scenario('horizontal-scroll', {'wide.txt': 'x' * 30 + '界' * 20 + '\ttab' + 'y' * 30 + '\n'}, ['wide.txt'], [b'$', b'0', b'20l', b'w'], size=(30, 6))
scenario('resize-small', TXT, ['a.txt'], [('resize', 1, 1), ('resize', 2, 2), ('resize', 10, 3), ('resize', 80, 24), ('resize', 0, 0), ('resize', 60, 10)])
scenario('gutter-width', {'n.txt': lines(120)}, ['n.txt'], [b'99G', b'o', b'x', ESC, b'G', b'dd', b'u'], size=(30, 8))
scenario('unicode-cursor', {'u.txt': 'é界🦀éz\tq\n'}, ['u.txt'], [b'l', b'l', b'l', b'l', b'l', b'$', b'i', ESC])
scenario('crlf-file', {'crlf.txt': 'one\r\ntwo\r\nthree\r\n'}, ['crlf.txt'], [b'$', b'j$', b'x', b'u'])
scenario('empty-file', {'empty.txt': ''}, ['empty.txt'], [b'x', b'dd', b'p', b'ihi', ESC, b':q\r', b':q!\r'])
scenario('new-file', {}, ['new.txt'], [b'inew', ESC, b':w\r'])
scenario('picker', {'a.txt': 'a', 'sub/b.rs': 'fn b() {}', 'sub/c.md': '# c', '.gitignore': 'ignored.txt\n', 'ignored.txt': 'x'}, ['a.txt'], [b'\x10', b'b', b'\x1b[B', b'\x7f', b'sub', ESC, b'\x10', b'zzzz', ESC])
scenario('explorer', {'a.txt': 'a', 'sub/b.rs': 'fn b() {}', 'sub/c.md': '# c', '.hidden': 'h', 'z.txt': 'z'}, ['a.txt'], [b'-', b'j', b'k', b'l', b'h', b'.', b'.', b'/c', ESC, ESC])
scenario('explorer-ops', {'a.txt': 'a'}, ['a.txt'], [b'\x0f', b'a', b'new.txt', b'\r', b'A', b'dir', b'\r', b'r', b'\x15', b'renamed.txt', b'\r', b'd', b'y', b'u', ESC], size=(70, 12))
scenario('welcome-explorer', {'a.txt': 'aa', 'dir/b.txt': 'bb'}, [], [b'-', b'/a', b'\r', b'ix', ESC, b':q!\r'])
scenario('switch-prompt', {'a.txt': 'aa', 'b.txt': 'bb'}, ['a.txt'], [b'ix', ESC, b'\x10', b'b.txt', b'\r', b'c', b'\x10', b'b.txt', b'\r', b'd', b'u'])
scenario('history-nav', {'a.txt': 'a\nb\nc', 'b.txt': 'x\ny'}, ['a.txt'], [b':3\r', b'\x10', b'b.txt', b'\r', b'j', b':back\r', b':forward\r', b':recent\r', ESC, b'\x1b[1;3D', b'\x1b[1;3C'])
scenario('project-search', {'a.txt': 'needle here\nmore needle\n', 'sub/b.txt': 'a needle b\n', 'c.txt': 'nothing'}, ['a.txt'], [b'\x1b[15~', b'needle', ('wait', 0.4), b'\x1b[B', ESC], size=(90, 14))
scenario('recent-empty', TXT, ['a.txt'], [b':recent\r', ESC, b':back\r', b':forward\r'])
scenario('counts-pending', {'c.txt': lines(30, 'row {}')}, ['c.txt'], [b'12', b'd', b'3', ESC, b'5j', b'2d3w', b'u'])
scenario('undo-redo', TXT, ['a.txt'], [b'x', b'x', b'u', b'\x12', b'\x12', b'u', b'u', b'u', b'3', b'\x12'])
scenario('reflow', {'p.md': 'one two three four five six seven eight nine ten\n\nsecond paragraph here\n'}, ['p.md'], [b':textwidth 12\r', b'gq', b'u', b':justify 20\r', b':justify x\r'])
scenario('word-keys-in-prompt', TXT, ['a.txt'], [b'\x0f', b'wbecw', ESC, ESC, b':dwcw', ESC])
scenario('clipboard-missing', TXT, ['a.txt'], [b'vlY', b':clipboard-copy\r', b':clipboard-paste\r'], env={'PATH': '/nonexistent', 'WAYLAND_DISPLAY': '', 'DISPLAY': ''})
scenario('alt-keys', TXT, ['a.txt'], [b'\x1bx', b'i', b'\x1bx', ESC, b'\x1b[200~paste~\x1b[201~', b'\x1b[3~', b'\x1b[2~', b'\x1b[1;5C'])
scenario('unknown-keys', TXT, ['a.txt'], [b'5', b'\x1b[H', b'\x1b[F', b'q', b'Z', b'\t', b'\x7f', b'\r', b'12', b'~', b'\x1b[15;5~'])
scenario('no-color-markdown', MD, ['doc.md'], [b'vjj', b'y', b':theme latte\r', b'\x1bOP', ESC], env={'NO_COLOR': '1'})
scenario('no-color-rust', RS, ['main.rs'], [b'Vj', b'2G', b':theme terminal\r', b'/fn\r'], env={'NO_COLOR': '1'})
scenario('long-prompt', TXT, ['a.txt'], [b':' + b'x' * 100, ESC, b'/' + '界'.encode() * 60, ESC], size=(30, 6))



def run(binary, sc):
    root = os.path.join(WORK, sc['name'])
    shutil.rmtree(root, ignore_errors=True)
    os.makedirs(root)
    for rel, content in sc['files'].items():
        path = os.path.join(root, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, 'w', encoding='utf-8', newline='') as f:
            f.write(content)
    width, height = sc['size']
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', height, width, 0, 0))
    os.makedirs(EMPTY_PATH, exist_ok=True)
    env = dict(os.environ, TERM='xterm-256color', PATH=EMPTY_PATH)
    env.pop('NO_COLOR', None)
    env.update(sc['env'])
    proc = subprocess.Popen([binary] + sc['args'], stdin=slave, stdout=slave, stderr=slave, cwd=root, env=env, close_fds=True)
    os.close(slave)

    def drain(quiet=0.12, limit=1.5):
        data = b''
        end = time.monotonic() + limit
        last = time.monotonic()
        while time.monotonic() < end:
            if select.select([master], [], [], 0.02)[0]:
                try:
                    data += os.read(master, 65536)
                except OSError:
                    break
                last = time.monotonic()
            elif time.monotonic() - last > quiet and (data or time.monotonic() - last > 0.3):
                break
        return data

    out = [drain(quiet=0.3, limit=2.0)]
    for step in sc['steps']:
        if isinstance(step, tuple) and step[0] == 'resize':
            fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack('HHHH', step[2], step[1], 0, 0))
            proc.send_signal(signal.SIGWINCH)
        elif isinstance(step, tuple) and step[0] == 'wait':
            time.sleep(step[1])
        else:
            os.write(master, step)
        out.append(drain())
    if proc.poll() is None:
        proc.kill()
    proc.wait()
    os.close(master)
    files = {}
    for directory, _, names in os.walk(root):
        for name in names:
            full = os.path.join(directory, name)
            with open(full, 'rb') as f:
                files[os.path.relpath(full, root)] = f.read()
    return out, files


def encode(out, files):
    return {'steps': [base64.b64encode(s).decode() for s in out],
            'files': {k: base64.b64encode(v).decode() for k, v in sorted(files.items())}}


def screens(steps, size):
    screen = Screen(*size)
    result = []
    for data in steps:
        screen.feed(data)
        result.append(screen.snapshot())
    return result


def main():
    binary = os.path.abspath(sys.argv[1])
    if sys.platform != 'linux':
        print('SKIP: pty_golden compares absolute Linux paths shown in the status line')
        return
    if '--record' in sys.argv:
        golden = {sc['name']: encode(*run(binary, sc)) for sc in SCENARIOS}
        GOLDEN.parent.mkdir(exist_ok=True)
        GOLDEN.write_text(json.dumps(golden, indent=0, sort_keys=True))
        print('recorded', len(golden), 'scenarios to', GOLDEN)
        return
    golden = json.loads(GOLDEN.read_text())
    identical = batched = 0
    failures = []
    for sc in SCENARIOS:
        want = golden[sc['name']]
        out, files = run(binary, sc)
        got = encode(out, files)
        if got == want:
            identical += 1
            continue
        if got['files'] != want['files']:
            failures.append(f"{sc['name']}: files differ: {sorted(got['files'])} vs {sorted(want['files'])}")
            continue
        a = screens([base64.b64decode(s) for s in want['steps']], sc['size'])
        b = screens(out, sc['size'])
        step = next((i for i, (x, y) in enumerate(zip(a, b)) if x != y), None)
        if step is None and len(a) == len(b):
            batched += 1
            continue
        detail = ''
        if step is not None:
            detail = '; '.join(f'row {k}: {p.rstrip()!r} != {q.rstrip()!r}' for k, (p, q) in enumerate(zip(a[step][0], b[step][0])) if p != q)
            detail += f' cursor {a[step][1]} != {b[step][1]}'
        failures.append(f"{sc['name']}: screen differs at step {step}: {detail}")
    for failure in failures:
        print('FAIL', failure)
    assert not failures, f'{len(failures)} scenarios differ from the Rust recordings'
    print(f'PTY golden: {len(SCENARIOS)} scenarios match saved Rust-reference recordings ({identical} byte-identical, {batched} screen-equivalent with different output batching)')


main()
