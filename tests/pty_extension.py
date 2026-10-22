"""Scheme extensions in a real terminal session; no third-party Python packages.

Runs the real binary on a PTY with a temporary HOME and XDG_CONFIG_HOME holding an
init.scm, and checks docs/extending.md end to end: a command on a multibyte selection,
one undo and one redo, failures that change nothing, runaway loops that are interrupted
or stopped, a command that survives switching files, (load ...) refused, a project-local
init.scm that is never run, and a missing or broken init.scm that does not block start-up.
"""
import fcntl
import os
import pathlib
import pty
import select
import struct
import subprocess
import sys
import tempfile
import termios
import time

binary = str(pathlib.Path(sys.argv[1]).resolve())

CTRL_C = b'\x03'
CTRL_R = b'\x12'
CTRL_P = b'\x10'
ESC = b'\x1b'

WRAP = """
(define (wrap open close)
  (replace-selection (string-append open (selection-text) close)))
(define-command "wrap" wrap "Wrap the selection in OPEN and CLOSE")
"""

COMMANDS = WRAP + """
(define (boom) (error "boom" 1))
(define (bad-utf8) (replace-selection "\\xff"))
(define (wrong-type) (replace-selection 42))
(define (edit-then-fail) (replace-selection "changed") (error "late failure"))
(define (spin) (let loop () (loop)))
(define (grow) (let loop ((s "xxxxxxxx")) (loop (string-append s s))))
(define-command "boom" boom)
(define-command "bad-utf8" bad-utf8)
(define-command "wrong-type" wrong-type)
(define-command "edit-then-fail" edit-then-fail)
(define-command "spin" spin)
(define-command "grow" grow)
"""


class Session:
    """The editor on a PTY. Wide enough that no status message is cut off."""

    def __init__(self, args, env, cwd, width=140, height=30):
        self.master, self.slave = pty.openpty()
        self.original = termios.tcgetattr(self.slave)
        fcntl.ioctl(self.slave, termios.TIOCSWINSZ, struct.pack('HHHH', height, width, 0, 0))
        self.proc = subprocess.Popen([binary] + list(args), stdin=self.slave, stdout=self.slave,
                                     stderr=self.slave, env=env, cwd=cwd)
        self.output = bytearray()
        self.drain(0.3)
        assert self.proc.poll() is None, self.output

    def drain(self, duration=0.15):
        deadline = time.monotonic() + duration
        while time.monotonic() < deadline:
            if select.select([self.master], [], [], 0.02)[0]:
                self.output.extend(os.read(self.master, 65536))

    def send(self, keys, expect=None, timeout=5, settle=0.15):
        start = len(self.output)
        os.write(self.master, keys)
        self.drain(settle)
        if expect is not None:
            deadline = time.monotonic() + timeout
            while expect not in self.output[start:] and time.monotonic() < deadline:
                self.drain(0.05)
            assert expect in self.output[start:], (expect, bytes(self.output[start:])[-3000:])
        return bytes(self.output[start:])

    def send_text(self, text, **kwargs):
        return self.send(text.encode(), **kwargs)

    def save(self, path, expected):
        self.send(b':w\r', expect=b'Written')
        actual = pathlib.Path(path).read_text(encoding='utf-8')
        assert actual == expected, (actual, expected)

    def quit(self):
        os.write(self.master, b':q!\r')
        assert self.proc.wait(timeout=5) == 0, bytes(self.output)[-2000:]
        self.drain(0.1)
        restored = termios.tcgetattr(self.slave)
        pending_input = getattr(termios, 'PENDIN', 0)  # a transient kernel flag on macOS
        restored[3] &= ~pending_input
        self.original[3] &= ~pending_input
        assert restored == self.original, 'terminal attributes not restored'
        assert b'\x1b[0m\x1b[?1049l' in self.output, 'alternate screen not restored'
        assert b'\x1b[?25h' in self.output, 'cursor not restored'
        self.close()

    def close(self):
        if self.proc.poll() is None:
            self.proc.kill()
            self.proc.wait()
        os.close(self.master)
        os.close(self.slave)


class Sandbox:
    """A project directory plus a user configuration directory, both temporary."""

    def __init__(self, root, name, init=None, xdg=True, files=None):
        self.root = pathlib.Path(root) / name
        self.home = self.root / 'home'
        self.project = self.root / 'project'
        self.home.mkdir(parents=True)
        self.project.mkdir()
        self.config = self.root / 'xdg'
        self.xdg = xdg
        if init is not None:
            where = (self.config if xdg else self.home / '.config') / 'coigue'
            where.mkdir(parents=True)
            (where / 'init.scm').write_text(init, encoding='utf-8')
        for file, text in (files or {}).items():
            path = self.project / file
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(text, encoding='utf-8')

    def env(self, **extra):
        env = dict(os.environ)
        for name in ('NO_COLOR', 'XDG_CONFIG_HOME'):
            env.pop(name, None)
        env.update(HOME=str(self.home), TERM='xterm-256color')
        if self.xdg:
            env['XDG_CONFIG_HOME'] = str(self.config)
        env.update(extra)
        return env

    def start(self, *args, **kwargs):
        return Session(args, self.env(**kwargs.pop('env', {})), self.project, **kwargs)

    def text(self, name):
        return (self.project / name).read_text(encoding='utf-8')


DOC = 'héllo 界 wörld\nsecond line\n'


def wrap_undo_redo(root):
    box = Sandbox(root, 'wrap', COMMANDS, files={'doc.txt': DOC})
    path = box.project / 'doc.txt'
    s = box.start('doc.txt')
    # "héllo" is five characters (seven bytes): v, then four l.
    s.send(b'v4l')
    s.send_text('!wrap ( )\r', expect=b'Ran :wrap')
    s.save(path, '(héllo) 界 wörld\nsecond line\n')
    # One undo restores the whole command, one redo applies it again.
    s.send(b'u', expect=b'Undone')
    s.save(path, DOC)
    s.send(CTRL_R, expect=b'Redone')
    s.save(path, '(héllo) 界 wörld\nsecond line\n')
    # Multibyte delimiters, typed as UTF-8, around the multibyte tail of the line.
    s.send(b'8lv$')
    s.send_text('!wrap 「 」\r', expect=b'Ran :wrap')
    s.save(path, '(héllo) 「界 wörld」\nsecond line\n')
    s.send(b'u', expect=b'Undone')
    s.save(path, '(héllo) 界 wörld\nsecond line\n')
    s.send(b'u', expect=b'Undone')
    s.save(path, DOC)
    s.send(b'u', expect=b'Nothing to undo')
    # A V selection is whole lines.
    s.send(b'gg')
    s.send(b'Vj')
    s.send_text('!wrap << >>\r', expect=b'Ran :wrap')
    s.save(path, '<<héllo 界 wörld\nsecond line>>\n')
    s.quit()
    print('wrap: multibyte selection, one undo, one redo verified')


def failures_change_nothing(root):
    box = Sandbox(root, 'failures', COMMANDS + """
(define (loader) (load "evil.scm"))
(define-command "loader" loader)
""", files={'doc.txt': 'abc def\nxyz\n', 'evil.scm': '(define-command "pwned" boom)'})
    path = box.project / 'doc.txt'
    s = box.start('doc.txt')
    s.send(b'x')
    s.send(b'u', expect=b'Undone')  # the redo entry has to survive everything below
    cases = [
        (b'', ':wrap ( )\r', b'selection-text: no selection; select text with v or V, then press !'),
        (b'vl', '!boom\r', b':boom failed: boom 1'),
        (b'vl', '!bad-utf8\r', b'replace-selection: text is not valid UTF-8'),
        (b'vl', '!wrong-type\r', b'replace-selection: argument 1: expected string'),
        (b'vl', '!wrap (\r', b'wrap: expected 2 argument(s), got 1'),
        (b'vl', '!edit-then-fail\r', b':edit-then-fail failed: late failure'),
        (b'vl', '!loader\r', b'load: denied: editor extensions cannot use operating-system operations'),
        (b'vl', '!nonexistent ( )\r', b'Unknown command: nonexistent ( )'),
    ]
    for keys, line, message in cases:
        if keys:
            s.send(keys)
        s.send_text(line, expect=message)
        s.save(path, 'abc def\nxyz\n')
    # The editor still works, and neither the failures nor the denied load changed the history.
    s.send(CTRL_R, expect=b'Redone')
    s.save(path, 'bc def\nxyz\n')
    s.send(b'u', expect=b'Undone')
    s.send(b'u', expect=b'Nothing to undo')
    s.send(b'0vl')
    s.send_text('!wrap ( )\r', expect=b'Ran :wrap')
    s.save(path, '(ab)c def\nxyz\n')
    # The file that load was asked to read was not evaluated.
    s.send_text(':pwned\r', expect=b'Unknown command: pwned')
    s.quit()

    big = Sandbox(root, 'oversized', WRAP, files={'big.txt': 'é' * 70000 + '\n'})
    s = big.start('big.txt')
    s.send(b'v$')
    s.send_text('!wrap ( )\r', expect=b'selection is 70000 characters; the limit is 65536')
    s.save(big.project / 'big.txt', 'é' * 70000 + '\n')
    s.send(b'u', expect=b'Nothing to undo')
    s.send(b'v')
    s.send_text('!wrap ( )\r', expect=b'Ran :wrap')
    s.quit()
    print('failures: no selection, errors, rejected edits, oversized selection, load leave text and history alone')


def runaway(root):
    box = Sandbox(root, 'runaway', COMMANDS, files={'doc.txt': 'abc\n'})
    path = box.project / 'doc.txt'
    s = box.start('doc.txt')
    # Ctrl-C interrupts; keys typed while the loop ran are handled afterwards, in order.
    s.send_text(':spin\r', settle=0.4)
    s.send(b'ixy', settle=0.3)
    s.send(CTRL_C, expect=b':spin interrupted; no changes made', timeout=5)
    s.send(ESC)
    s.save(path, 'xyabc\n')
    s.send_text('v')
    s.send_text('!wrap ( )\r', expect=b'Ran :wrap')
    s.save(path, 'x(y)abc\n')
    # Nobody interrupts: the deadline stops it.
    start = time.monotonic()
    s.send_text(':spin\r', expect=b':spin stopped after 5s; no changes made', timeout=10)
    assert 4 < time.monotonic() - start < 9, time.monotonic() - start
    s.save(path, 'x(y)abc\n')
    s.send(b'u', expect=b'Undone')
    s.save(path, 'xyabc\n')
    # A string that doubles forever is stopped by the memory limit, not by an out-of-memory exit.
    s.send_text(':grow\r', expect=b':grow stopped: used more than 128 MiB of memory; no changes made', timeout=20)
    s.save(path, 'xyabc\n')
    s.quit()
    print('runaway: Ctrl-C, deadline and memory limit stop the command; the terminal is restored')


def file_switching(root):
    box = Sandbox(root, 'switch', WRAP, files={'a.txt': 'first 界 file\n', 'b.txt': 'second é file\n'})
    s = box.start('a.txt')
    s.send(b'v4l')
    s.send_text('!wrap [ ]\r', expect=b'Ran :wrap')
    s.save(box.project / 'a.txt', '[first] 界 file\n')
    s.send(CTRL_P)
    s.send_text('b.txt', settle=0.6)
    s.send(b'\r', settle=0.4)
    s.send(b'wv$')  # on "é file" in b.txt
    s.send_text('!wrap { }\r', expect=b'Ran :wrap')
    s.save(box.project / 'b.txt', 'second {é file}\n')
    assert box.text('a.txt') == '[first] 界 file\n'
    s.send(b'u', expect=b'Undone')
    s.save(box.project / 'b.txt', 'second é file\n')
    s.quit()
    print('file switching: the command is still defined and edits the active buffer')


def load_is_refused(root):
    evil = 'evil.scm'
    box = Sandbox(root, 'load', COMMANDS + """
(define (loader) (load "evil.scm"))
(define-command "loader" loader)
""", files={'doc.txt': 'abc\n', evil: '(define-command "pwned" boom "loaded behind your back")'})
    s = box.start('doc.txt')
    s.send_text(':loader\r', expect=b'load: denied')
    s.send_text(':pwned\r', expect=b'Unknown command: pwned')
    s.quit()
    # A load at the top level of init.scm fails the file at that line and defines nothing more.
    absolute = str(box.project / evil)
    top = Sandbox(root, 'load-top', WRAP + '(load "%s")\n(define-command "after" wrap)\n' % absolute,
                  files={'doc.txt': 'abc\n', evil: '(define-command "pwned" wrap)'})
    s = top.start('doc.txt')
    s.drain(0.3)
    assert b'init.scm: failed: load: denied' in s.output, bytes(s.output)[-2000:]
    s.send_text(':pwned\r', expect=b'Unknown command: pwned')
    s.send_text(':after\r', expect=b'Unknown command: after')
    s.send(b'v')
    s.send_text('!wrap ( )\r', expect=b'Ran :wrap')  # the command defined before the load works
    s.quit()
    print('load: (load ...) is refused in a command and in init.scm')


def only_the_user_configuration_is_loaded(root):
    local = '(define-command "local" (lambda () (message "ran local")))'
    box = Sandbox(root, 'local', WRAP, files={
        'init.scm': local, '.coigue/init.scm': local, 'coigue/init.scm': local,
        'src/init.scm': local, 'src/doc.txt': 'abc\n', 'doc.txt': 'abc\n'})
    for argument in ('doc.txt', 'src/doc.txt'):
        s = box.start(argument)
        s.send_text(':local\r', expect=b'Unknown command: local')
        s.send(b'v')
        s.send_text('!wrap ( )\r', expect=b'Ran :wrap')  # the user's own file still loads
        s.quit()
    # No user file at all: a project init.scm is just a file, nothing is defined.
    bare = Sandbox(root, 'local-only', None, files={'init.scm': local, 'doc.txt': 'abc\n'})
    s = bare.start('doc.txt')
    s.send_text(':local\r', expect=b'Unknown command: local')
    s.quit()
    # The welcome screen in a directory that holds an init.scm: also nothing.
    s = bare.start()
    s.send_text(':local\r', expect=b'Unknown command: local')
    s.quit()
    print('trust: init.scm next to the file, in the project or in the working directory is never run')


def where_the_user_file_lives(root):
    # Without XDG_CONFIG_HOME: ~/.config/coigue/init.scm.
    home = Sandbox(root, 'home-config', WRAP, xdg=False, files={'doc.txt': 'abc\n'})
    s = home.start('doc.txt')
    s.send(b'v')
    s.send_text('!wrap ( )\r', expect=b'Ran :wrap')
    s.quit()
    # A relative XDG_CONFIG_HOME is ignored, as the specification says.
    relative = Sandbox(root, 'relative-xdg', WRAP, xdg=False, files={'doc.txt': 'abc\n'})
    s = relative.start('doc.txt', env={'XDG_CONFIG_HOME': 'relative/dir'})
    s.send(b'v')
    s.send_text('!wrap ( )\r', expect=b'Ran :wrap')
    s.quit()
    print('paths: XDG_CONFIG_HOME, the ~/.config fallback and a relative XDG_CONFIG_HOME verified')


def missing_or_broken_init(root):
    missing = Sandbox(root, 'missing', None, files={'doc.txt': 'abc\n'})
    s = missing.start('doc.txt')
    assert b'init.scm' not in s.output, 'a missing init.scm should say nothing'
    s.send(b'ix')
    s.send(ESC)
    s.save(missing.project / 'doc.txt', 'xabc\n')
    s.send_text(':wrap ( )\r', expect=b'Unknown command: wrap ( )')
    s.quit()
    for name, init, message in (
        ('syntax', '(define-command "x" car', b'init.scm: '),
        ('runtime', WRAP + '(no-such-procedure)', b'init.scm: failed: eval: unbound variable: no-such-procedure'),
        ('builtin', '(define-command "w" car)', b'init.scm: failed: define-command: "w" is a built-in command'),
    ):
        box = Sandbox(root, 'broken-' + name, init, files={'doc.txt': 'abc\n'})
        s = box.start('doc.txt')
        s.drain(0.3)
        assert message in s.output, (name, bytes(s.output)[-1500:])
        s.send(b'ix')
        s.send(ESC)
        s.save(box.project / 'doc.txt', 'xabc\n')
        if name == 'runtime':  # the command defined before the error is available
            s.send(b'v')
            s.send_text('!wrap ( )\r', expect=b'Ran :wrap')
        s.quit()
    # The welcome screen reports the error too and still works.
    box = Sandbox(root, 'broken-welcome', '(error "nope")')
    s = box.start()
    s.drain(0.3)
    assert b'init.scm: failed: nope' in s.output, bytes(s.output)[-1500:]
    s.quit()
    # A runaway init.scm delays start-up by at most the deadline, and Ctrl-C ends it at once.
    box = Sandbox(root, 'runaway-init', WRAP + '(let loop () (loop))', files={'doc.txt': 'abc\n'})
    start = time.monotonic()
    s = box.start('doc.txt')
    s.send(CTRL_C, expect=b'init.scm: interrupted', timeout=5)
    assert time.monotonic() - start < 4
    s.send(b'v')
    s.send_text('!wrap ( )\r', expect=b'Ran :wrap')
    s.quit()
    print('start-up: a missing, broken or runaway init.scm is reported and never blocks the editor')


def command_line_contract(root):
    # --version and --help are answered before any configuration is read.
    box = Sandbox(root, 'contract', '(let loop () (loop))')
    expected = subprocess.run([binary, '--version'], capture_output=True, text=True, timeout=5,
                              env=Sandbox(root, 'contract-clean', None).env())
    start = time.monotonic()
    actual = subprocess.run([binary, '--version'], capture_output=True, text=True, timeout=5, env=box.env())
    assert actual.stdout == expected.stdout and actual.returncode == 0 == expected.returncode, actual
    assert actual.stdout.startswith('coigue '), actual.stdout
    assert time.monotonic() - start < 2
    usage = subprocess.run([binary, '--help'], capture_output=True, text=True, timeout=5, env=box.env())
    assert usage.stdout.startswith('Usage: coigue [FILE]\n') and usage.returncode == 0
    two = subprocess.run([binary, 'a', 'b'], capture_output=True, text=True, timeout=5, env=box.env())
    assert two.returncode == 1 and 'usage: coigue [file]' in two.stderr, two
    print('contract: --version, --help and the argument check are unchanged and read no configuration')


with tempfile.TemporaryDirectory() as directory:
    for scenario in (wrap_undo_redo, failures_change_nothing, runaway, file_switching, load_is_refused,
                     only_the_user_configuration_is_loaded, where_the_user_file_lives,
                     missing_or_broken_init, command_line_contract):
        scenario(directory)
print('PTY extension suite passed')
