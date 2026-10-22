# The Coigüe site

The site is a small [Zola](https://www.getzola.org) project that GitHub Pages publishes at <https://fclaude.github.io/coigue/>. Its pages are the repository's own Markdown: `docs/home.md`, `docs/setup.md`, `docs/using.md`, `docs/keys.md` and `docs/extending.md`. Nothing under `site/content` is checked in, and `site/build.py` writes it every time. The Keys page takes its tables from the README, and the Using page includes the example files from `docs/examples`, so those can't drift from the editor.

## Building it

You need Zola 0.23.6 and Python 3:

```sh
python3 site/build.py
cd site
zola build
python3 finish.py
zola check --skip-external-links
```

`finish.py` runs on the built pages. It lets you Tab to every code block and table that scrolls sideways, names each of them for a screen reader, gives tables a caption and `scope`, and numbers the code blocks of a kind when a page has more than one. `.github/workflows/pages.yml` does the same steps, with a pinned Zola, and publishes the result. For the first publish, GitHub's repository settings need Pages set to use "GitHub Actions" as its source.

## Checking accessibility

`bash site/audit/run.sh` builds the site for a local address, serves it and runs two checks in Chromium (`npx playwright install chromium` the first time). The first runs axe-core on every page in light and dark, at 1280 and 320 pixels wide, with the WCAG 2.0, 2.1 and 2.2 A and AA rules plus axe's best-practice rules. It currently reports no violations. The second presses Tab like a keyboard user would: the skip link comes first and moves focus to the content, every scrollable region can be reached, each page has one `h1` and no skipped heading levels, and the page doesn't scroll sideways at 320 pixels or with the text at 200%.

## Contrast

These are the measured ratios (WCAG asks for 4.5 for text and 3 for the focus ring and borders):

| Pair | Light | Dark |
| --- | --- | --- |
| Body text on the page | 15.33 | 14.34 |
| Muted text on the page | 6.77 | 7.71 |
| Links on the page | 6.15 | 9.94 |
| Inline code on its background | 13.65 | 12.13 |
| Muted text on the code background | 6.02 | 6.53 |
| Links on the code background | 5.48 | 8.41 |
| Skip link text on its button | 6.53 | 10.42 |
| Focus ring on the page | 6.15 | 9.94 |
| Borders on the page | 3.55 | 3.40 |
| Weakest token in a code block | 5.04 | 9.23 |

With the browser's "more contrast" setting on, text is black on white (or white on black) at 21:1 and links are 10.4:1 (15.8:1 in the dark theme).

The code colours come from Zola's `github-light-high-contrast` and `github-dark-high-contrast` themes. We measured ten pairs of themes on a sample of Go, Scheme and shell code. Only two pairs reached 4.5:1 for every token in both modes, and this one has the strongest weakest token (5.04, where the other pair's is 4.57).
