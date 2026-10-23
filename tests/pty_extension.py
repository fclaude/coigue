"""Scheme extensions in a real terminal session; no third-party Python packages.

Runs the real binary on a PTY with a temporary HOME and XDG_CONFIG_HOME holding an
init.scm, and checks docs/extending.md end to end: a command on a multibyte selection,
one undo and one redo, failures that change nothing, runaway loops that are interrupted
or stopped, a command that survives switching files, (load ...) refused, a project-local
init.scm that is never run, a missing or broken init.scm that does not block start-up, and
themes defined and chosen from init.scm, keys bound from init.scm (Ctrl, Alt and Visual keys,
the keys that cannot be bound, a bound key that fails), the F1 guide listing them, and the edit
operations as one undo step.
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


def themes_from_init(root):
    pink, mocha_keyword, navy = b'38;2;255;121;198', b'38;2;203;166;247', b'48;2;16;32;48'
    source = 'fn main() {}\n'
    theme = """
(define-theme "dusk"
  '(("keyword" . "#ff79c6") ("background" . "#102030")))
(define-theme "dawn" '(("base" . "latte")))
"""
    # Defined and chosen: the first screen is already dusk.
    box = Sandbox(root, 'theme-chosen', theme + '(set-theme "dusk")', files={'main.rs': source})
    s = box.start('main.rs')
    assert pink in s.output and navy in s.output, bytes(s.output)[-1500:]
    assert mocha_keyword not in s.output
    # :theme still switches at run time, and Tab offers the defined names.
    out = s.send(b':theme mocha\r', expect=b'Theme: catppuccin-mocha')
    assert mocha_keyword in out, out
    s.send(b':theme d')
    s.send(b'\t', expect=b'theme dusk')
    s.send(b'\t', expect=b'theme dawn')
    s.send(b'\t', expect=b'theme dusk')
    out = s.send(b'\r', expect=b'Theme: dusk')
    assert pink in out and navy in out, out
    out = s.send(b':theme\r')
    assert b'|dusk|dawn>' in out, out
    s.quit()
    # Defined but not chosen: nothing changes until :theme says so.
    box = Sandbox(root, 'theme-defined', theme, files={'main.rs': source})
    s = box.start('main.rs')
    assert mocha_keyword in s.output and pink not in s.output, bytes(s.output)[-1500:]
    out = s.send(b':theme dusk\r', expect=b'Theme: dusk')
    assert pink in out, out
    s.quit()
    # A bad spec is reported, the theme before it stays, and the editor starts in mocha.
    box = Sandbox(root, 'theme-bad', theme + '(define-theme "oops" \'(("keyword" . "pink")))', files={'main.rs': source})
    s = box.start('main.rs')
    s.drain(0.3)
    assert b'init.scm: failed: define-theme: keyword: "pink" is not a colour' in s.output, bytes(s.output)[-1500:]
    assert mocha_keyword in s.output and pink not in s.output
    s.send(b':theme dusk\r', expect=b'Theme: dusk')
    s.send(b':theme oops\r', expect=b'Unknown theme: oops')
    s.quit()
    box = Sandbox(root, 'theme-unknown', '(set-theme "nord")', files={'main.rs': source})
    s = box.start('main.rs')
    s.drain(0.3)
    assert b'there is no theme "nord"; use mocha|gruvbox|latte|monochrome|terminal' in s.output, bytes(s.output)[-1500:]
    s.quit()
    # Without an init file the :theme listing is what it was.
    box = Sandbox(root, 'theme-none', None, files={'main.rs': source})
    s = box.start('main.rs')
    out = s.send(b':theme\r', expect=b'Theme: catppuccin-mocha | :theme <mocha|gruvbox|latte|monochrome|terminal>')
    s.quit()
    print('themes: define-theme and set-theme from init.scm, :theme and Tab completion, a bad spec, no init file')


KEYS = """
(define (mark) (insert-text "*") (message "marked"))
(define (shout) (replace-selection (string-upcase (selection-text))))
(define (boom) (insert-text "partial") (error "key failed"))
(define (spin) (let loop () (loop)))
(bind-key "normal" "X" mark "mark the cursor")
(bind-key "normal" "<C-d>" (lambda () (insert-text "ctrl ")) "ctrl d")
(bind-key "normal" "<A-j>" (lambda () (insert-text "alt ")) "alt j")
(bind-key "visual" "U" shout "uppercase the selection")
(bind-key "normal" "B" boom "fails")
(bind-key "normal" "S" spin "runs away")
(bind-key "normal" "x" (lambda () (message "x was bound")) "x" #t)
"""

CTRL_D = b'\x04'
F1 = b'\x1bOP'


def key_bindings(root):
    box = Sandbox(root, 'keys', KEYS, files={'doc.txt': 'héllo wörld\nsecond\n'})
    path = box.project / 'doc.txt'
    s = box.start('doc.txt')
    s.send(b'X', expect=b'marked')
    s.save(path, '*héllo wörld\nsecond\n')
    s.send(CTRL_D)
    s.send(b'\x1bj')  # Esc followed at once by j is Alt-J
    s.save(path, 'alt ctrl *héllo wörld\nsecond\n')
    # Each key press was one undo step. (A second Undone looks like the first on the screen, which
    # only sends what changed, so only the first is waited for.)
    s.send(b'u', expect=b'Undone')
    s.send(b'u')
    s.save(path, '*héllo wörld\nsecond\n')
    s.send(CTRL_R, expect=b'Redone')
    s.save(path, 'ctrl *héllo wörld\nsecond\n')
    s.send(b'u', expect=b'Undone')
    s.send(b'u')
    s.save(path, 'héllo wörld\nsecond\n')
    # A Visual binding gets the selection, and it ends.
    s.send(b'gg')
    s.send(b'vllll')
    s.send(b'U')
    s.save(path, 'HÉLLO wörld\nsecond\n')
    s.send(b'u', expect=b'Undone')
    s.save(path, 'héllo wörld\nsecond\n')
    # A pending operator keeps its meaning: dX cancels the d and runs nothing.
    s.send(b'dX')
    s.save(path, 'héllo wörld\nsecond\n')
    # x was overridden on purpose, and the other keys are untouched.
    s.send(b'x', expect=b'x was bound')
    s.save(path, 'héllo wörld\nsecond\n')
    s.send(b'dd')
    s.save(path, 'second\n')
    s.send(b'u', expect=b'Undone')
    # A binding that fails changes nothing and says so; so does one that runs away.
    s.send(b'B', expect=b'binding B failed: key failed')
    s.save(path, 'héllo wörld\nsecond\n')
    s.send(b'S', expect=b'binding S stopped after 5s; no changes made', timeout=10)
    s.save(path, 'héllo wörld\nsecond\n')
    s.send(b'gg0i')
    s.send_text('X')  # Insert mode types the key
    s.send(ESC)
    s.save(path, 'Xhéllo wörld\nsecond\n')
    # The guide lists the bindings.
    s.send(F1)
    out = s.send(b'\t' * 6, expect=b'Your commands and keys')
    for line in (b'normal X', b'normal <C-d>', b'normal <A-j>', b'visual U', b'uppercase the selection'):
        assert line in out, (line, out[-1500:])
    s.send(ESC)
    s.quit()

    # The keys that cannot be bound, and a built-in key without an override, stop init.scm there.
    for name, program, message in (
        ('colon', '(bind-key "normal" ":" car "" #t)', b'init.scm: failed: bind-key: ":" cannot be bound in normal mode'),
        ('ctrl-c', '(bind-key "normal" "<C-c>" car)', b'Ctrl-C interrupts a running command'),
        ('escape', '(bind-key "normal" "<Esc>" car)', b'Escape cannot be bound'),
        ('quit', '(bind-key "normal" "<C-q>" car)', b'stop and start the terminal'),
        ('builtin', '(bind-key "normal" "w" car)', b'"w" already has a meaning in normal mode (it is a motion); pass #t as the override argument'),
        ('twice', '(bind-key "normal" "Q" car "first") (bind-key "normal" "Q" cdr "second")',
         b'normal Q is already bound by binding 1 ("first"); binding 2 ("second") cannot also use it'),
    ):
        bad = Sandbox(root, 'keys-' + name, program, files={'doc.txt': 'abc\n'})
        s = bad.start('doc.txt')
        s.drain(0.3)
        assert message in s.output, (name, bytes(s.output)[-1500:])
        if name == 'colon':
            s.send(b':')  # the command line still opens: no key can take it away
            s.send_text('q!\r')
            assert s.proc.wait(timeout=5) == 0
            s.close()
            continue
        s.send(b'ix')
        s.send(ESC)
        s.save(bad.project / 'doc.txt', 'xabc\n')
        s.quit()
    print('keys: Ctrl, Alt, Visual and overridden keys, undo, failures, guide, and the keys that refuse to bind')


EDITS = """
(define-command "tidy"
  (lambda ()
    (replace-lines 2 2 "TWO\\nTWO-b")
    (delete-lines 1 1)
    (insert-at (line-count) 1 "> ")
    (set-cursor 1 2)
    (message (format "~a:~a ~a" (cursor-line) (cursor-column) (word-at-cursor)))))
(define-command "find-it"
  (lambda (text) (message (format "~s ~s" (find text) (buffer-path)))))
(define-command "only-move" (lambda () (set-cursor 3 1)))
"""


def edit_operations(root):
    box = Sandbox(root, 'edits', EDITS, files={'doc.txt': 'one\ntwo\nthree\n'})
    path = box.project / 'doc.txt'
    s = box.start('doc.txt')
    s.send_text(':tidy\r', expect=b'1:2 TWO')
    s.save(path, 'TWO\nTWO-b\nthree\n> ')
    s.send(b'u', expect=b'Undone')
    s.save(path, 'one\ntwo\nthree\n')
    s.send(CTRL_R, expect=b'Redone')
    s.save(path, 'TWO\nTWO-b\nthree\n> ')
    s.send(b'u', expect=b'Undone')
    s.send(b'u', expect=b'Nothing to undo')
    s.send_text(':find-it three\r', expect=('(3 1) "%s"' % (box.project / 'doc.txt').resolve()).encode())
    # Moving the cursor alone is not an edit: no undo step, no change on disk.
    s.send_text(':only-move\r', expect=b'Ran :only-move')
    s.send(b'u', expect=b'Nothing to undo')
    s.save(path, 'one\ntwo\nthree\n')
    s.quit()
    print('edits: replace-lines, delete-lines, insert-at and set-cursor are one undo step; set-cursor alone is none')


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
    chosen = sys.argv[2:]  # scenario names, to run only those while developing
    for scenario in (wrap_undo_redo, failures_change_nothing, runaway, file_switching, load_is_refused,
                     only_the_user_configuration_is_loaded, where_the_user_file_lives,
                     missing_or_broken_init, themes_from_init, key_bindings, edit_operations,
                     command_line_contract):
        if not chosen or scenario.__name__ in chosen:
            scenario(directory)
print('PTY extension suite passed')
