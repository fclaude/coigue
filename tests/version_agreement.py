"""The Go and Cargo source versions agree, and are one three-part number.

usage: python3 tests/version_agreement.py

While the Rust reference sources exist, Cargo.toml and internal/version/version.go
must name the same version, so a drift cannot leave the two editors with different numbers. Once Cargo.toml is deleted
(docs/rust-removal.md) only the format of the Go version is checked.
"""
import pathlib
import re
import sys
import tomllib

ROOT = pathlib.Path(__file__).resolve().parent.parent
THREE_PART = re.compile(r'[0-9]+\.[0-9]+\.[0-9]+')


def go_version():
    text = (ROOT / 'internal/version/version.go').read_text()
    match = re.search(r'^\s*Version\s*=\s*"([^"]*)"', text, re.MULTILINE)
    if not match:
        sys.exit('internal/version/version.go: no `Version = "x.y.z"` variable found')
    return match[1]


def cargo_version():
    cargo = ROOT / 'Cargo.toml'
    if not cargo.exists():
        return None
    return tomllib.loads(cargo.read_text())['package']['version']


go, cargo = go_version(), cargo_version()
if not THREE_PART.fullmatch(go):
    sys.exit(f'Go source version {go!r} is not a numeric three-part version')
if cargo is not None and cargo != go:
    sys.exit(f'Cargo.toml says {cargo} but internal/version/version.go says {go}: bump both together')
print(f'source version {go}' + (' (Cargo.toml agrees)' if cargo else ' (no Cargo.toml)'))
