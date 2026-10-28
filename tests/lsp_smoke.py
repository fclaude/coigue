"""Deterministic Unix PTY LSP integration checks; no installed servers required."""
import fcntl
import json
import os
import pathlib
import pty
import re
import select
import struct
import subprocess
import sys
import tempfile
import termios
import time

EDITORS = []

def fake_server():
    log = pathlib.Path(os.environ['COIGUE_LSP_LOG']) / (pathlib.Path(sys.argv[0]).name + '.jsonl')
    mode = os.environ.get('COIGUE_LSP_MODE', 'normal')
    documents = {}
    def record(value):
        with log.open('a') as f:
            f.write(json.dumps(value) + '\n')
    record({'pid': os.getpid(), 'args': sys.argv[1:], 'cwd': os.getcwd()})
    def send(value):
        data = json.dumps(value, ensure_ascii=False).encode()
        sys.stdout.buffer.write(('Content-Length: %d\r\n\r\n' % len(data)).encode() + data)
        sys.stdout.buffer.flush()
    while True:
        headers = {}
        while True:
            line = sys.stdin.buffer.readline()
            if not line:
                return
            if line == b'\r\n':
                break
            key, value = line.decode().split(':', 1)
            headers[key.lower()] = value.strip()
        msg = json.loads(sys.stdin.buffer.read(int(headers['content-length'])))
        record(msg)
        method = msg.get('method')
        if method == 'initialize':
            if mode == 'hang':
                time.sleep(30)
            capabilities = {
                'textDocumentSync': {'openClose': True, 'change': 2, 'save': {'includeText': True}}
            }
            if mode.startswith('navigation'):
                capabilities.update(definitionProvider=True, referencesProvider=True,
                                    documentSymbolProvider=True, workspaceSymbolProvider=True)
            if mode.startswith('semantic') and pathlib.Path(sys.argv[0]).name != 'marksman':
                assert msg['params']['capabilities']['textDocument']['semanticTokens']['requests']['full'] is True
                if pathlib.Path(sys.argv[0]).name == 'gopls':
                    assert msg['params']['initializationOptions']['semanticTokens'] is True
                capabilities['semanticTokensProvider'] = {'full': {'delta': True}, 'legend': {
                    'tokenTypes': ['variable', 'function'], 'tokenModifiers': ['readonly']}}
            send({'jsonrpc': '2.0', 'id': msg['id'], 'result': {'capabilities': capabilities}})
        elif method in ('textDocument/didOpen', 'textDocument/didChange'):
            doc = msg['params']['textDocument']
            if mode == 'crash':
                os._exit(7)
            text = doc.get('text') if method.endswith('didOpen') else msg['params']['contentChanges'][0]['text']
            documents[doc['uri']] = text
            # Publish after input handling has gone idle.
            time.sleep(.08)
            diagnostics = [] if 'FIXED' in text else [{
                'range': {'start': {'line': 0, 'character': 1}, 'end': {'line': 0, 'character': 3}},
                'severity': 2, 'message': 'LSP warning from ' + pathlib.Path(sys.argv[0]).name,
            }]
            send({'jsonrpc': '2.0', 'method': 'textDocument/publishDiagnostics', 'params': {
                'uri': doc['uri'], 'version': doc['version'], 'diagnostics': diagnostics,
            }})
        elif method in ('textDocument/definition', 'textDocument/references',
                         'textDocument/documentSymbol', 'workspace/symbol'):
            if mode == 'navigation-hang':
                continue
            if mode == 'navigation-error':
                send({'jsonrpc': '2.0', 'id': msg['id'], 'error': {'code': -32603, 'message': 'fixture navigation failure'}})
                continue
            def span(line, column):
                return {'start': {'line': line, 'character': column}, 'end': {'line': line, 'character': column + 1}}
            source = (pathlib.Path.cwd() / 'navigation-source.rs').as_uri()
            target = (pathlib.Path.cwd() / 'navigation-target.rs').as_uri()
            if method == 'textDocument/definition':
                result = [{'targetUri': target, 'targetRange': span(0, 0), 'targetSelectionRange': span(0, 3)}]
            elif method == 'textDocument/documentSymbol':
                result = [{'name': 'Outer', 'kind': 23, 'range': span(0, 0), 'selectionRange': span(0, 3),
                           'children': [{'name': 'Local', 'kind': 12, 'range': span(1, 0), 'selectionRange': span(1, 3)}]}]
            elif method == 'textDocument/references':
                result = [{'uri': source, 'range': span(0, 3)}, {'uri': target, 'range': span(0, 3)}]
            else:
                result = [{'name': 'WorkspaceTarget', 'kind': 12, 'location': {'uri': target, 'range': span(1, 3)}}]
            if mode == 'navigation-null':
                result = None
            elif mode == 'navigation-malformed':
                result = [{'uri': 'https://invalid.example/', 'range': span(0, 0)}]
            send({'jsonrpc': '2.0', 'id': msg['id'], 'result': result})
        elif method == 'textDocument/semanticTokens/full':
            if mode == 'semantic-error':
                send({'jsonrpc': '2.0', 'id': msg['id'], 'error': {'code': -32601, 'message': 'unsupported'}})
            elif mode != 'semantic-hang':
                text = documents[msg['params']['textDocument']['uri']]
                before = text.split('main', 1)[0]
                line = before.count('\n')
                start = len(before.rsplit('\n', 1)[-1].encode('utf-16-le')) // 2
                send({'jsonrpc': '2.0', 'id': msg['id'], 'result': {'data': [line, start, 4, 1, 0]}})
        elif method == 'shutdown':
            send({'jsonrpc': '2.0', 'id': msg['id'], 'result': None})
        elif method == 'exit':
            return


class Editor:
    def __init__(self, binary, path, root, env, width=None):
        self.master, self.slave = pty.openpty()
        self.original = termios.tcgetattr(self.slave)
        if width is None:
            width = max(140, len(os.fsencode(path)) + 80)
        fcntl.ioctl(self.slave, termios.TIOCSWINSZ, struct.pack('HHHH', 16, width, 0, 0))
        self.proc = subprocess.Popen([binary, str(path)], stdin=self.slave, stdout=self.slave,
                                     stderr=self.slave, cwd=root, env=env)
        self.output = bytearray()
        self.closed = False
        EDITORS.append(self)
    def drain(self, duration=.2):
        end = time.monotonic() + duration
        while time.monotonic() < end:
            if select.select([self.master], [], [], .02)[0]:
                self.output.extend(os.read(self.master, 65536))
    def wait_for(self, text, timeout=4):
        end = time.monotonic() + timeout
        while text not in self.output and time.monotonic() < end:
            self.drain(.05)
        assert text in self.output, (text, self.output[-6000:])
    def keys(self, keys):
        os.write(self.master, keys)
        self.drain()
    def close(self, keys=b':q!\r'):
        if self.closed:
            return
        try:
            self.keys(keys)
            assert self.proc.wait(timeout=3) == 0, self.output
            self.drain()
            restored = termios.tcgetattr(self.slave)
            restored[3] &= ~getattr(termios, 'PENDIN', 0)
            self.original[3] &= ~getattr(termios, 'PENDIN', 0)
            assert restored == self.original, 'terminal attributes not restored'
        finally:
            if self.proc.poll() is None:
                self.proc.kill()
                self.proc.wait()
            os.close(self.master)
            os.close(self.slave)
            self.closed = True


def records(log, name):
    path = log / (name + '.jsonl')
    return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []


def assert_reaped(log):
    for path in log.glob('*.jsonl'):
        for record in [json.loads(line) for line in path.read_text().splitlines()]:
            if 'pid' in record:
                try:
                    os.kill(record['pid'], 0)
                except ProcessLookupError:
                    continue
                raise AssertionError('language server not reaped: %s' % record['pid'])


def main():
    binary = str(pathlib.Path(sys.argv[1]).resolve())
    with tempfile.TemporaryDirectory() as temp:
        root = pathlib.Path(temp).resolve()
        bin_dir, log = root / 'bin', root / 'log'
        bin_dir.mkdir()
        log.mkdir()
        source = pathlib.Path(__file__).read_text()
        for name in ['rust-analyzer', 'gopls', 'marksman']:
            script = bin_dir / name
            script.write_text('#!' + sys.executable + '\n' + source)
            script.chmod(0o755)
        env = dict(os.environ, PATH=str(bin_dir), COIGUE_LSP_LOG=str(log))
        env.pop('NO_COLOR', None)
        for name in ['Cargo.toml', 'go.mod', '.marksman.toml']:
            (root / name).write_text('')
        source_path = root / 'navigation-source.rs'
        target_path = root / 'navigation-target.rs'
        source_path.write_text('a😀target\nfn local() {}')
        target_path.write_text('😀 target\nfn target() {}')
        nav_env = dict(env, COIGUE_LSP_MODE='navigation')
        editor = Editor(binary, source_path, root, nav_env)
        editor.wait_for(b'LSP warning')
        editor.keys(b'2l')
        editor.output.clear()
        editor.keys(b':definition\r')
        editor.wait_for(b'navigation-target.rs')
        editor.keys(b'i!\x1b')
        editor.keys(b':w\r')
        editor.keys(b':back\r')
        editor.keys(b':symbols\r')
        editor.wait_for(b'Outer / Local')
        editor.keys(b'Local\r')
        editor.keys(b'i#\x1b')
        editor.keys(b':w\r')
        editor.output.clear()
        editor.keys(b':references\r')
        editor.wait_for(b'navigation-target.rs')
        editor.keys(b'navigation-target\r')
        editor.output.clear()
        editor.keys(b':workspace-symbols\r')
        editor.keys(b'Target')
        editor.wait_for(b'WorkspaceTarget')
        editor.keys(b'\r')
        editor.close(b':q\r')
        assert target_path.read_text() == '😀 !target\nfn target() {}'
        assert source_path.read_text() == 'a😀target\nfn #local() {}'
        nav_records = records(log, 'rust-analyzer')
        definition = next(e for e in nav_records if e.get('method') == 'textDocument/definition')
        assert definition['params']['position'] == {'line': 0, 'character': 3}
        assert any(e.get('method') == 'workspace/symbol' and e['params']['query'] == 'Target' for e in nav_records)
        assert any(e.get('method') == 'textDocument/references' and e['params']['context']['includeDeclaration'] for e in nav_records)
        for mode, expected in [('normal', b'does not support'), ('navigation-error', b'fixture navigation failure'),
                               ('navigation-null', b'0 results'), ('navigation-malformed', b'No usable local-file'),
                               ('navigation-hang', b'timed out')]:
            editor = Editor(binary, source_path, root, dict(env, COIGUE_LSP_MODE=mode))
            editor.wait_for(b'LSP warning')
            editor.keys(b':symbols\r')
            editor.wait_for(expected, timeout=8)
            editor.keys(b'\x1b')
            editor.close(b':q\r')
        print('Navigation LSP PTY: symbols, definitions, references, workspace, Unicode, history and failure isolation verified')
        assert_reaped(log)
        for record_path in log.glob('*.jsonl'):
            record_path.unlink()

        for suffix, name, language in [('rs', 'rust-analyzer', 'rust'), ('go', 'gopls', 'go'), ('md', 'marksman', 'markdown')]:
            path = root / ('file café #界.' + suffix)
            path.write_bytes('a😀b\r\nnext'.encode())
            editor = Editor(binary, path, root, env)
            editor.wait_for(('LSP warning from ' + name).encode())
            assert b'E:0 W:1' in editor.output
            editor.keys(b'iFIXED\x1b')
            editor.wait_for(b'E:0 W:0')
            editor.keys(b'u')
            editor.keys(b'\x12')  # redo
            editor.keys(b':w\r')
            editor.close(b':wq\r')
            entries = records(log, name)
            methods = [e.get('method') for e in entries]
            opened = next(e for e in entries if e.get('method') == 'textDocument/didOpen')
            assert opened['params']['textDocument']['languageId'] == language
            assert '%23' in opened['params']['textDocument']['uri']
            assert opened['params']['textDocument']['text'] == 'a😀b\r\nnext'
            changes = [e for e in entries if e.get('method') == 'textDocument/didChange']
            assert changes and changes[-1]['params']['contentChanges'][0]['text'].startswith('FIXED')
            assert all('range' in e['params']['contentChanges'][0] for e in changes)
            versions = [e['params']['textDocument']['version'] for e in changes]
            assert versions == sorted(set(versions))
            assert 'textDocument/didSave' in methods
            assert methods[-3:] == ['textDocument/didClose', 'shutdown', 'exit'], methods
            assert entries[0]['args'] == (['server'] if suffix == 'md' else [])
            assert entries[0]['cwd'] == str(root)
            assert_reaped(log)
        print('LSP PTY: Rust/Go/Markdown launch, idle diagnostics, Unicode/CRLF, edit/undo/redo/save, shutdown verified')

        # Save-and-switch must notify the old document before closing it.
        path, next_path = root / 'switch.rs', root / 'next.rs'
        path.write_text('old')
        next_path.write_text('next')
        previous = len(records(log, 'rust-analyzer'))
        editor = Editor(binary, path, root, env)
        editor.wait_for(b'LSP warning')
        editor.keys(b'i!\x1b')
        editor.keys(b'\x10next.rs')
        editor.wait_for(b'> next.rs')
        editor.keys(b'\r')
        editor.wait_for(b'[s] save/open')
        editor.keys(b's')
        editor.close()
        entries = records(log, 'rust-analyzer')[previous:]
        methods = [e.get('method') for e in entries]
        saved_at = methods.index('textDocument/didSave')
        assert methods[saved_at + 1:saved_at + 3] == ['textDocument/didClose', 'textDocument/didOpen'], methods
        assert entries[saved_at]['params']['text'] == '!old'
        assert path.read_text() == '!old'
        assert_reaped(log)
        print('LSP PTY: save-and-switch ordering verified')

        # Fast paste must coalesce without disabling the server or losing saves.
        path = root / 'paste.go'
        path.write_text('text')
        previous = len(records(log, 'gopls'))
        editor = Editor(binary, path, root, env)
        editor.wait_for(b'LSP warning')
        paste = b'x' * 512
        editor.keys(b'i' + paste)
        editor.wait_for(('Insert ' + str(path) + ' [+]  1:513').encode())
        editor.keys(b'\x1b')
        editor.keys(b':w\r')
        editor.keys(b'iUNSAVED\x1b')
        editor.close()
        entries = records(log, 'gopls')[previous:]
        saved = next(e for e in entries if e.get('method') == 'textDocument/didSave')
        assert saved['params']['text'] == paste.decode() + 'text'
        assert path.read_text() == paste.decode() + 'text'
        assert b':lsp-restart to retry' not in editor.output
        assert_reaped(log)

        # A refused write never emits didSave.
        path = root / 'conflict.rs'
        path.write_text('original')
        previous = len(records(log, 'rust-analyzer'))
        editor = Editor(binary, path, root, env)
        editor.wait_for(b'LSP warning')
        editor.keys(b'i!\x1b')
        path.write_text('external')
        editor.keys(b':w\r')
        editor.wait_for(b'file changed externally')
        editor.close()
        assert not any(e.get('method') == 'textDocument/didSave' for e in records(log, 'rust-analyzer')[previous:])
        assert path.read_text() == 'external'
        assert_reaped(log)
        print('LSP PTY: fast paste, save snapshots, and failed-save notifications verified')

        # Renaming the active file changes servers and closes the previous URI.
        path = root / 'rename.rs'
        path.write_text('text')
        editor = Editor(binary, path, root, env)
        editor.wait_for(b'LSP warning from rust-analyzer')
        for suffix, server in [('go', 'gopls'), ('md', 'marksman'), ('txt', None)]:
            editor.keys(b':explore\r')
            editor.keys(('r\x15renamed.' + suffix + '\r').encode())
            editor.keys(b'\x1b')
            if server:
                editor.wait_for(('LSP warning from ' + server).encode())
            assert (root / ('renamed.' + suffix)).exists()
        editor.close()
        assert_reaped(log)
        print('LSP PTY: active-file rename, language changes and unsupported extensions verified')

        for mode in ['crash', 'hang', 'missing']:
            path = root / ('failure-' + mode + '.rs')
            path.write_text('text')
            mode_env = dict(env, COIGUE_LSP_MODE=mode)
            if mode == 'missing':
                mode_env['PATH'] = str(root / 'empty-path')
            editor = Editor(binary, path, root, mode_env)
            if mode != 'hang':
                editor.wait_for(b':lsp-restart to retry')
            else:
                editor.drain(.2)
            editor.keys(b'iSAVED\x1b')
            editor.close(b':wq\r')
            assert path.read_text() == 'SAVEDtext'
            assert_reaped(log)
        print('LSP PTY: missing/crashed/hung servers preserve editing, saving and terminal restoration')

        # Installing a missing server and restarting recovers in the same session.
        path = root / 'retry.go'
        path.write_text('text')
        server = bin_dir / 'gopls'
        hidden = bin_dir / 'gopls-hidden'
        server.rename(hidden)
        editor = Editor(binary, path, root, env)
        editor.wait_for(b':lsp-restart to retry')
        hidden.rename(server)
        editor.keys(b':lsp-restart\r')
        editor.wait_for(b'LSP warning from gopls')
        editor.close()
        assert_reaped(log)
        print('LSP PTY: retry after installing a missing server verified')

        for suffix, server, text in [('rs', 'rust-analyzer', 'fn main() {}'), ('go', 'gopls', 'func main() {}')]:
            path = root / ('semantic.' + suffix)
            path.write_text(text)
            previous = len(records(log, server))
            semantic_env = dict(env, COIGUE_LSP_MODE='semantic')
            editor = Editor(binary, path, root, semantic_env)
            editor.wait_for(b'\x1b[38;2;137;180;250mmain')
            for theme, color in [('gruvbox', b'38;2;184;187;38'), ('latte', b'38;2;30;102;245'), ('monochrome', b'38;2;255;255;255'), ('terminal', b'38;5;12')]:
                offset = len(editor.output)
                editor.keys((':theme ' + theme + '\r').encode())
                assert b'\x1b[' + color + b'mmain' in editor.output[offset:], editor.output[offset:]
                if theme == 'terminal':
                    terminal_frame = editor.output[offset:].split(b'Theme: terminal', 1)[1]
                    assert b'38;2;' not in terminal_frame
                    assert b'48;2;' not in terminal_frame
                if theme == 'monochrome':
                    mono_frame = editor.output[offset:].split(b'Theme: monochrome', 1)[1]
                    colors = re.findall(rb'\x1b\[(?:38|48);2;(\d+);(\d+);(\d+)m', mono_frame)
                    assert all(r == g == b for r, g, b in colors)
                    assert b'\x1b[1m' in editor.output[offset:]
                    assert b'\x1b[22m' in editor.output[offset:]
            editor.close()
            requests = [e for e in records(log, server)[previous:] if e.get('method') == 'textDocument/semanticTokens/full']
            assert len(requests) == 1, 'theme changes should reuse semantic tokens'
            assert_reaped(log)
            for mode in ['semantic-error', 'semantic-hang', 'missing']:
                fallback_env = dict(env, COIGUE_LSP_MODE=mode)
                if mode == 'missing':
                    fallback_env['PATH'] = str(root / 'empty-path')
                editor = Editor(binary, path, root, fallback_env)
                keyword = b'fn' if suffix == 'rs' else b'func'
                editor.wait_for(b'\x1b[38;2;203;166;247m' + keyword)
                editor.drain(.2)
                assert b'\x1b[38;2;137;180;250mmain' not in editor.output
                editor.close()
                assert_reaped(log)
        print('LSP PTY: Rust/Go semantic colors, live theme changes, and lexical fallback verified')


if __name__ == '__main__':
    if pathlib.Path(sys.argv[0]).name in ('rust-analyzer', 'gopls', 'marksman'):
        fake_server()
    else:
        try:
            main()
        finally:
            for editor in EDITORS:
                if not editor.closed:
                    editor.keys(b'\x1b')
                    editor.close()
