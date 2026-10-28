"""Real Scheme colouring and symbol navigation through the editor and Brote."""
import os
import pathlib
import sys
import tempfile

sys.dont_write_bytecode = True
from lsp_smoke import Editor


def check(binary, server, path, config_home):
    original = path.read_bytes()
    env = dict(os.environ, TERM='xterm-256color', XDG_CONFIG_HOME=str(config_home),
               PATH=str(pathlib.Path(server).parent) + os.pathsep + os.environ.get('PATH', ''))
    env.pop('NO_COLOR', None)
    editor = Editor(binary, path, path.parent, env)
    try:
        editor.wait_for(b'brote-lsp E:0 W:0', timeout=8)
        # The starter's dusk theme gives keywords pink and strings cyan. These
        # escape sequences must come from actual Scheme source rendering.
        editor.wait_for(b'\x1b[38;2;255;121;198m')
        editor.wait_for(b'\x1b[38;2;139;233;253m')
        editor.output.clear()
        editor.keys(b':symbols\r')
        editor.wait_for(b'results; type to filter', timeout=8)
        editor.wait_for(b'wrap')
        assert b'0 results;' not in editor.output, editor.output[-6000:]
        editor.keys(b'wrap')
        editor.keys(b'\r')
        editor.wait_for(b'brote-lsp E:0 W:0')
        editor.close(b':q\r')
        assert path.read_bytes() == original, 'symbol navigation changed init.scm'
    finally:
        editor.close()
    print('Scheme PTY: lexical colours, real Brote symbols, selection and clean exit passed')


if __name__ == '__main__':
    binary, server = map(os.path.abspath, sys.argv[1:3])
    if len(sys.argv) == 4:
        path = pathlib.Path(sys.argv[3]).resolve()
        check(binary, server, path, path.parent.parent)
    else:
        with tempfile.TemporaryDirectory(prefix='coigue-scheme-') as directory:
            config_home = pathlib.Path(directory)
            path = config_home / 'coigue' / 'init.scm'
            path.parent.mkdir()
            source = pathlib.Path(__file__).resolve().parents[1] / 'docs/examples/sample.scm'
            path.write_bytes(source.read_bytes())
            check(binary, server, path, config_home)
