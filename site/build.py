#!/usr/bin/env python3
"""Assemble site/content from the repository's own documentation.

docs/*.md and README.md stay the canonical text (GitHub shows them, and the
Go tests load docs/examples), so the site is built from them and nothing under
site/content is checked in. Run it from anywhere, then build with Zola and run
the finishing step:

    python3 site/build.py && (cd site && zola build && python3 finish.py && zola check --skip-external-links)

Two markers in a page are filled in here:

    <!-- readme-table: Explorer keys | Action -->   the README table whose header row is that
    <!-- include: docs/examples/init.scm scheme -->  a file from the repository, as a code block

The script is deterministic: the same markdown gives the same files, and it
stops with a message if a marker names something that isn't there.
"""
import pathlib
import re
import shutil
import sys

SITE = pathlib.Path(__file__).resolve().parent
ROOT = SITE.parent
REPO = "https://github.com/fclaude/coigue/blob/main/"

# source, target in content/, label in the navigation, weight
PAGES = [
    ("docs/home.md", "_index.md", "Home", 1),
    ("docs/setup.md", "setup.md", "Setting it up", 2),
    ("docs/using.md", "using.md", "Using it", 3),
    ("docs/keys.md", "keys.md", "Keys and commands", 4),
    ("docs/writing.md", "writing.md", "Writing an extension", 5),
    ("docs/extending.md", "extending.md", "Scheme and themes", 6),
]
TARGETS = {pathlib.PurePosixPath(src).name: dst for src, dst, _, _ in PAGES}
TARGETS["README.md"] = "_index.md"

# The highlighter on the site knows these names; the repository's markdown uses
# the ones GitHub knows.
FENCE_LANGUAGES = {"scheme": "lisp", "sh": "bash"}

LINK = re.compile(r"(\]\()([^)\s]+)(\))")
FENCE = re.compile(r"^(\s*)(`{3,})([A-Za-z0-9_+-]*)")
TABLE_MARKER = re.compile(r"^<!-- readme-table: (.+?) -->$")
INCLUDE_MARKER = re.compile(r"^<!-- include: (\S+) (\S+) -->$")


class BuildError(Exception):
    pass


def readme_tables():
    """Every table in the README, keyed by its header row without the outer bars."""
    lines = (ROOT / "README.md").read_text(encoding="utf-8").split("\n")
    tables = {}
    i = 0
    while i < len(lines):
        if lines[i].startswith("|"):
            j = i
            while j < len(lines) and lines[j].startswith("|"):
                j += 1
            header = " | ".join(cell.strip() for cell in lines[i].strip().strip("|").split("|"))
            tables[header] = "\n".join(lines[i:j])
            i = j
        else:
            i += 1
    return tables


def rewrite(target, source):
    """A link written for a file in the repository, as a link on the site."""
    if re.match(r"[a-z]+:", target) or target.startswith("#"):
        return target
    path, _, frag = target.partition("#")
    frag = "#" + frag if frag else ""
    name = pathlib.PurePosixPath(path).name
    if name in TARGETS and path.endswith(".md"):
        return "@/" + TARGETS[name] + frag
    resolved = pathlib.PurePosixPath(source).parent / path
    parts = []
    for part in resolved.parts:
        if part == "..":
            parts.pop()
        elif part != ".":
            parts.append(part)
    return REPO + "/".join(parts) + frag


def fill_markers(lines, src, tables):
    out = []
    for line in lines:
        m = TABLE_MARKER.match(line.strip())
        if m:
            key = " | ".join(cell.strip() for cell in m.group(1).split("|"))
            if key not in tables:
                raise BuildError("%s: no README table with the header %r" % (src, key))
            out.append(tables[key])
            continue
        m = INCLUDE_MARKER.match(line.strip())
        if m:
            path = ROOT / m.group(1)
            if not path.is_file():
                raise BuildError("%s: cannot include %s" % (src, m.group(1)))
            lang = FENCE_LANGUAGES.get(m.group(2), m.group(2))
            out += ["```" + lang] + path.read_text(encoding="utf-8").rstrip("\n").split("\n") + ["```"]
            continue
        out.append(line)
    return out


def convert(src, dst, nav, weight, tables):
    text = (ROOT / src).read_text(encoding="utf-8")
    title = None
    body = []
    fence = None  # the backticks that opened the code block we are in
    for line in text.split("\n"):
        m = FENCE.match(line)
        if m and fence is None:
            fence = m.group(2)
            if m.group(3):
                line = m.group(1) + fence + FENCE_LANGUAGES.get(m.group(3), m.group(3))
        elif m and line.strip() == m.group(2) and len(m.group(2)) >= len(fence):
            fence = None
        elif fence is None and title is None and line.startswith("# "):
            title = line[2:].strip()
            continue
        body.append(line)
    if title is None:
        raise BuildError("%s has no # title" % src)
    body = fill_markers(body, src, tables)
    text = "\n".join(body).lstrip("\n")
    text = LINK.sub(lambda m: m.group(1) + rewrite(m.group(2), src) + m.group(3), text)
    front = ["+++", 'title = "%s"' % title.replace('"', '\\"'), "weight = %d" % weight]
    if dst == "_index.md":
        front += ['sort_by = "weight"', 'template = "index.html"']
    front += ["", "[extra]", 'nav = "%s"' % nav, "+++", ""]
    return "\n".join(front) + text


def main(check_only=False):
    tables = readme_tables()
    pages = {}
    for src, dst, nav, weight in PAGES:
        pages[dst] = convert(src, dst, nav, weight, tables)
    for dst, text in pages.items():
        for target in re.findall(r"\]\(@/([^)#\s]+)", text):
            if target not in pages:
                raise BuildError("%s links to @/%s, which is not a page of the site" % (dst, target))
    if check_only:
        print("site/build.py: %d pages assemble, every marker resolves" % len(pages))
        return
    content = SITE / "content"
    if content.exists():
        shutil.rmtree(content)
    content.mkdir()
    for dst, text in pages.items():
        (content / dst).write_text(text, encoding="utf-8")


if __name__ == "__main__":
    try:
        main(check_only="--check" in sys.argv)
    except BuildError as err:
        sys.exit("site/build.py: " + str(err))
