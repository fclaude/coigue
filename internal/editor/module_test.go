package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fclaude/coigue/internal/file"
	"github.com/fclaude/coigue/internal/help"
	"github.com/fclaude/coigue/internal/key"
	"github.com/fclaude/coigue/internal/theme"
)

// loadTree writes init.scm and modules under a temporary configuration
// directory and loads them.
func loadTree(t *testing.T, e *Editor, init string, modules map[string]string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "coigue")
	if err := os.MkdirAll(filepath.Join(root, "modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "init.scm")
	write(t, path, init)
	for name, src := range modules {
		write(t, filepath.Join(root, "modules", name), src)
	}
	e.LoadInit(path)
	return path
}

func TestExampleModules(t *testing.T) {
	root := filepath.Join("..", "..", "docs", "examples", "modules")
	initSrc, err := os.ReadFile(filepath.Join(root, "init.scm"))
	if err != nil {
		t.Fatal(err)
	}
	modules := map[string]string{}
	for _, name := range []string{"text-tools.scm", "md-tools.scm"} {
		src, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		modules[name] = string(src)
	}
	e := newEditor(t, &file.File{Path: filepath.Join(t.TempDir(), "doc.txt")}, "abcd")
	loadTree(t, e, string(initSrc), modules)
	if e.Message != "" {
		t.Fatalf("loading the example reported %q", e.Message)
	}
	send(e, "vl")
	command(e, "text-tools/wrap ( )")
	if e.Buffer.Text() != "(ab)cd" || e.Message != "Ran :text-tools/wrap" {
		t.Fatalf("qualified: %q %q", e.Buffer.Text(), e.Message)
	}
	undo(e)
	send(e, "0vl")
	command(e, "md-tools/wrap")
	if e.Buffer.Text() != "**ab**cd" || e.Message != "Ran :md-tools/wrap" {
		t.Fatalf("md: %q %q", e.Buffer.Text(), e.Message)
	}
	undo(e)
	command(e, "wrap")
	if e.Buffer.Text() != "abcd" || !strings.Contains(e.Message, "wrap is ambiguous: md-tools/wrap, text-tools/wrap") {
		t.Fatalf("ambiguous: %q %q", e.Buffer.Text(), e.Message)
	}
	send(e, "0vl")
	alt(e, 'w')
	if e.Buffer.Text() != "**ab**cd" || e.Message != "bolded" {
		t.Fatalf("init's key: %q %q", e.Buffer.Text(), e.Message)
	}
	undo(e)
	send(e, "0vl")
	alt(e, 'b')
	if e.Buffer.Text() != "**ab**cd" || !strings.HasPrefix(e.Message, "Ran binding") {
		t.Fatalf("module key: %q %q", e.Buffer.Text(), e.Message)
	}
	send(e, ":text-tools/wr")
	press(e, key.Tab)
	if e.Command != "text-tools/wrap" {
		t.Fatalf("Tab completed to %q", e.Command)
	}
	press(e, key.Esc)
	press(e, key.F(1))
	for e.Help.Topic != help.Extensions {
		press(e, key.Tab)
	}
	text := strings.Join(e.Help.Lines(), "\n")
	for _, want := range []string{
		"text-tools  Wrap and reshape the selection",
		"exports wrap",
		":text-tools/wrap",
		"visual <A-w> is displaced by init.scm",
		"md-tools  Markdown helpers",
		":md-tools/wrap",
		"visual <A-b>",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the guide does not say %q:\n%s", want, text)
		}
	}
}

func TestModuleReloadReplacesTheModulesOrChangesNothing(t *testing.T) {
	e := newEditor(t, &file.File{Path: filepath.Join(t.TempDir(), "doc.txt")}, "abc")
	path := loadTree(t, e, "(require tools)\n", map[string]string{
		"tools.scm": `(define-module tools)
(define-command "one" (lambda () (message "old")))
(bind-key "normal" "X" (lambda () (message "old X")))
`,
	})
	if e.Message != "" {
		t.Fatal(e.Message)
	}
	command(e, "one")
	if e.Message != "old" {
		t.Fatalf("message %q", e.Message)
	}
	write(t, filepath.Join(filepath.Dir(path), "modules", "tools.scm"), `(define-module tools)
(define-command "one" (lambda () (message "new")))
(define-command "two" (lambda () (message "two")))
(bind-key "normal" "Y" (lambda () (message "new Y")))
`)
	command(e, "reload")
	if e.Message != "Reloaded init.scm: 1 module, 2 commands, 1 key binding" {
		t.Fatalf("message %q", e.Message)
	}
	command(e, "one")
	send(e, "Y")
	if e.Message != "new Y" {
		t.Fatalf("message %q", e.Message)
	}
	send(e, "X")
	if e.Message == "old X" {
		t.Fatal("the old binding survived the reload")
	}
	write(t, filepath.Join(filepath.Dir(path), "modules", "tools.scm"), "(define-module tools)\n(define-command \"three\"")
	command(e, "reload")
	if !strings.HasPrefix(e.Message, "init.scm: ") || !strings.HasSuffix(e.Message, "; nothing changed") {
		t.Fatalf("message %q", e.Message)
	}
	command(e, "two")
	if e.Message != "two" {
		t.Fatalf("a failed reload dropped a command: %q", e.Message)
	}
	if _, ok := e.commands.lookup("three"); ok {
		t.Fatal("a command from the failed module was installed")
	}
}

func TestAClashingKeyIsUnboundAtStartupAndBlocksReload(t *testing.T) {
	modules := map[string]string{
		"a.scm": `(define-module a)
(define-command "one" (lambda () (message "one")))
(bind-key "normal" "X" (lambda () (insert-text "A")) "from a")
`,
		"b.scm": `(define-module b)
(bind-key "normal" "X" (lambda () (insert-text "B")) "from b")
`,
	}
	e := newEditor(t, &file.File{Path: filepath.Join(t.TempDir(), "doc.txt")}, "abc")
	loadTree(t, e, "(require a) (require b)\n", modules)
	if !strings.Contains(e.Message, "left unbound") || !strings.Contains(e.Message, "a ") || !strings.Contains(e.Message, "b ") {
		t.Fatalf("message %q", e.Message)
	}
	command(e, "one")
	send(e, "X")
	if e.Message != "one" || e.Buffer.Text() != "abc" {
		t.Fatalf("the clash bound the key or dropped the command: %q %q", e.Message, e.Buffer.Text())
	}
	// A file that loads cleanly, then a reload that clashes, changes nothing.
	e = newEditor(t, &file.File{Path: filepath.Join(t.TempDir(), "doc.txt")}, "abc")
	path := loadTree(t, e, "(require a)\n", modules)
	if e.Message != "" {
		t.Fatal(e.Message)
	}
	write(t, path, "(require a) (require b)\n")
	command(e, "reload")
	if !strings.Contains(e.Message, "left unbound") || !strings.HasSuffix(e.Message, "; nothing changed") {
		t.Fatalf("message %q", e.Message)
	}
	send(e, "X")
	if e.Buffer.Text() != "Aabc" {
		t.Fatalf("reload dropped the old binding: %q", e.Buffer.Text())
	}
}

func TestStartProceduresFromModulesRunSeparately(t *testing.T) {
	e := newEditor(t, &file.File{Path: filepath.Join(t.TempDir(), "doc.txt")}, "abc")
	loadTree(t, e, `(require early) (require late)
(on-start (lambda () (insert-at 1 (+ (string-length (buffer-text 1)) 1) "i")))
`, map[string]string{
		"early.scm": `(define-module early)
(on-start (lambda () (insert-at 1 (+ (string-length (buffer-text 1)) 1) "e")))
`,
		"late.scm": `(define-module late)
(on-start (lambda () (insert-at 1 (+ (string-length (buffer-text 1)) 1) "l")))
`,
	})
	if e.Message != "" {
		t.Fatal(e.Message)
	}
	e.RunStart()
	if e.Buffer.Text() != "abceli" {
		t.Fatalf("text %q", e.Buffer.Text())
	}
	undo(e)
	if e.Buffer.Text() != "abcel" {
		t.Fatalf("the procedures were not separate undo steps: %q", e.Buffer.Text())
	}
	undo(e)
	undo(e)
	if e.Buffer.Text() != "abc" {
		t.Fatalf("text %q", e.Buffer.Text())
	}
	e.RunStart()
	if e.Buffer.Text() != "abc" {
		t.Fatalf("ran again: %q", e.Buffer.Text())
	}

	e = newEditor(t, &file.File{Path: filepath.Join(t.TempDir(), "doc.txt")}, "abc")
	loadTree(t, e, `(require bad)
(on-start (lambda () (insert-at 1 (+ (string-length (buffer-text 1)) 1) "i")))
`, map[string]string{
		"bad.scm": `(define-module bad)
(on-start (lambda () (error "nope")))
`,
	})
	e.RunStart()
	if e.Buffer.Text() != "abci" || e.Message != "on-start bad failed: nope" {
		t.Fatalf("text %q message %q", e.Buffer.Text(), e.Message)
	}
}

func TestAModuleThemeIsQualifiedAndChosenFromInit(t *testing.T) {
	e := newEditor(t, &file.File{Path: filepath.Join(t.TempDir(), "doc.txt")}, "abc")
	loadTree(t, e, `(require tools)
(set-theme "tools/dusk")
`, map[string]string{
		"tools.scm": `(define-module tools)
(define-theme "dusk" '(("keyword" . "#ff79c6")))
`,
	})
	if e.Message != "" {
		t.Fatal(e.Message)
	}
	if e.Themes.Name(e.Theme) != "tools/dusk" || e.Palette() == theme.CatppuccinMocha.Palette() {
		t.Fatalf("theme %q", e.Themes.Name(e.Theme))
	}
	command(e, "theme tools/dusk")
	if e.Message != "Theme: tools/dusk" {
		t.Fatalf("message %q", e.Message)
	}
}
