package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fclaude/coigue/internal/file"
	"github.com/fclaude/coigue/internal/key"
	"github.com/fclaude/coigue/internal/syntax"
	"github.com/fclaude/coigue/internal/theme"
)

func plainEditor(t *testing.T) *Editor {
	t.Helper()
	return newEditor(t, &file.File{Path: filepath.Join(t.TempDir(), "doc.txt")}, "text")
}

var pink = theme.Color{Kind: theme.ColorRGB, R: 0xff, G: 0x79, B: 0xc6}

func TestExampleThemeInit(t *testing.T) {
	program, err := os.ReadFile(filepath.Join("..", "..", "docs", "examples", "theme.scm"))
	if err != nil {
		t.Fatal(err)
	}
	e := extensionEditor(t, "text", string(program))
	if e.Themes.Name(e.Theme) != "dusk" {
		t.Fatalf("started with %q", e.Themes.Name(e.Theme))
	}
	p := e.Palette()
	gruvbox := theme.GruvboxDark.Palette()
	if p.Syntax(syntax.Keyword) != pink || !p.Bold(syntax.Keyword) ||
		p.Syntax(syntax.String) == gruvbox.Syntax(syntax.String) || p.Syntax(syntax.Comment) != gruvbox.Syntax(syntax.Comment) {
		t.Fatalf("palette: keyword %+v string %+v comment %+v", p.Syntax(syntax.Keyword), p.Syntax(syntax.String), p.Syntax(syntax.Comment))
	}
}

func TestADefinedThemeIsNotUsedUntilChosen(t *testing.T) {
	e := extensionEditor(t, "text", `(define-theme "dusk" '(("keyword" . "#ff79c6")))`)
	if e.Theme != theme.CatppuccinMocha {
		t.Fatalf("started with %v", e.Theme)
	}
	send(e, ":theme")
	press(e, key.Enter)
	if e.Message != "Theme: catppuccin-mocha | :theme <mocha|gruvbox|latte|monochrome|terminal|dusk>" {
		t.Fatalf("message %q", e.Message)
	}
	send(e, ":theme dusk")
	press(e, key.Enter)
	if e.Message != "Theme: dusk" || e.Palette().Syntax(syntax.Keyword) != pink {
		t.Fatalf("message %q keyword %+v", e.Message, e.Palette().Syntax(syntax.Keyword))
	}
	send(e, ":theme latte")
	press(e, key.Enter)
	if e.Message != "Theme: catppuccin-latte" || e.Palette() != theme.CatppuccinLatte.Palette() {
		t.Fatalf("message %q", e.Message)
	}
	send(e, ":theme nord")
	press(e, key.Enter)
	if !strings.HasPrefix(e.Message, "Unknown theme: nord") || e.Theme != theme.CatppuccinLatte {
		t.Fatalf("message %q", e.Message)
	}
}

func TestThemeCompletionOffersDefinedThemes(t *testing.T) {
	e := extensionEditor(t, "text", `(define-theme "dusk" '()) (define-theme "dawn" '())`)
	send(e, ":theme d")
	var seen []string
	for i := 0; i < 3; i++ {
		press(e, key.Tab)
		seen = append(seen, e.Command)
	}
	if strings.Join(seen, "|") != "theme dusk|theme dawn|theme dusk" {
		t.Fatalf("completions %v", seen)
	}
}

func TestWithoutAnInitFileThemesAreAsTheyWere(t *testing.T) {
	e := plainEditor(t)
	send(e, ":theme")
	press(e, key.Enter)
	if e.Message != "Theme: catppuccin-mocha | :theme <"+theme.Choices+">" || e.Palette() != theme.CatppuccinMocha.Palette() {
		t.Fatalf("message %q", e.Message)
	}
}

func TestABadThemeSpecIsReportedAndDoesNotStopStartUp(t *testing.T) {
	e := plainEditor(t)
	loadInit(t, e, `
(define-theme "good" '(("keyword" . "#ff79c6")))
(define-theme "bad" '(("keyword" . "pink")))
(define-theme "never" '())`)
	if !strings.HasPrefix(e.Message, `init.scm: failed: define-theme: keyword: "pink" is not a colour`) {
		t.Fatalf("message %q", e.Message)
	}
	if _, ok := e.Themes.Parse("good"); !ok {
		t.Fatal("the theme before the error was dropped")
	}
	if _, ok := e.Themes.Parse("never"); ok {
		t.Fatal("the program ran past its error")
	}
	if e.Theme != theme.CatppuccinMocha {
		t.Fatalf("theme %v", e.Theme)
	}
	e = plainEditor(t)
	loadInit(t, e, `(set-theme "nord")`)
	if !strings.Contains(e.Message, `there is no theme "nord"`) || e.Theme != theme.CatppuccinMocha {
		t.Fatalf("message %q theme %v", e.Message, e.Theme)
	}
}
