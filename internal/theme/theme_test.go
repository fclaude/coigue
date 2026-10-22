package theme

import (
	"slices"
	"testing"

	"github.com/fclaude/coigue/internal/syntax"
)

// The Rust file has no tests of its own. expected_test.go is derived from
// src/theme.rs by testdata/gen_expected.py, both in the git tag rust-reference (the Rust enum order, the palettes,
// the name/parse tables and the arms of syntax() and diagnostic()), so these
// tests compare the hand-typed Go tables with a second, mechanical reading.

func TestEnumOrderAndDefault(t *testing.T) {
	for i, theme := range expectedThemes {
		if Theme(i) != theme {
			t.Errorf("Theme(%d) = %v, want the Rust variant %v", i, Theme(i), theme)
		}
	}
	var zero Theme
	if zero != CatppuccinMocha {
		t.Errorf("zero Theme = %v, want CatppuccinMocha", zero)
	}
	if zero.Name() != "catppuccin-mocha" {
		t.Errorf("zero Theme name = %q", zero.Name())
	}
}

func TestPalettesMatchRust(t *testing.T) {
	if len(expectedPalettes) != len(expectedThemes) {
		t.Fatalf("generated %d palettes for %d themes", len(expectedPalettes), len(expectedThemes))
	}
	for _, theme := range expectedThemes {
		if got, want := theme.Palette(), expectedPalettes[theme]; got != want {
			t.Errorf("%s palette:\n got %+v\nwant %+v", theme.Name(), got, want)
		}
	}
}

func TestPaletteKinds(t *testing.T) {
	terminal := Terminal.Palette()
	for name, c := range map[string]Color{"background": terminal.Background, "foreground": terminal.Foreground, "surface": terminal.Surface} {
		if c != (Color{}) || c.Kind != ColorReset {
			t.Errorf("terminal %s = %+v, want reset", name, c)
		}
	}
	for _, theme := range []Theme{CatppuccinMocha, GruvboxDark, CatppuccinLatte, Monochrome} {
		p := theme.Palette()
		for _, c := range []Color{p.Background, p.Foreground, p.Muted, p.Surface} {
			if c.Kind != ColorRGB || c.Index != 0 {
				t.Errorf("%s: %+v is not a plain RGB colour", theme.Name(), c)
			}
		}
	}
}

func TestNames(t *testing.T) {
	for _, theme := range expectedThemes {
		if got, want := theme.Name(), expectedNames[theme]; got != want {
			t.Errorf("Theme(%d).Name() = %q, want %q", theme, got, want)
		}
	}
}

func TestParse(t *testing.T) {
	for name, want := range expectedParse {
		got, ok := Parse(name)
		if !ok || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v, true", name, got, ok, want)
		}
	}
	for _, theme := range expectedThemes {
		if got, ok := Parse(theme.Name()); !ok || got != theme {
			t.Errorf("Parse(%q) = %v, %v; want %v, true", theme.Name(), got, ok, theme)
		}
	}
	for _, alias := range []struct {
		name string
		want Theme
	}{{"mocha", CatppuccinMocha}, {"gruvbox", GruvboxDark}, {"latte", CatppuccinLatte}} {
		if got, ok := Parse(alias.name); !ok || got != alias.want {
			t.Errorf("Parse(%q) = %v, %v", alias.name, got, ok)
		}
	}
	// Matching is exact: no case folding, no trimming, no prefixes.
	for _, name := range []string{"", "Mocha", "MOCHA", " mocha", "mocha ", "mocha\n", "catppuccin", "catppuccin-frappe", "gruvbox-light", "gruv", "terminal2", "mono", "default"} {
		if got, ok := Parse(name); ok {
			t.Errorf("Parse(%q) = %v, true; want no theme", name, got)
		}
	}
	if len(expectedParse) != 8 {
		t.Errorf("generated %d parse names, the Rust parse has 8 arms", len(expectedParse))
	}
}

func TestChoicesAndCompletions(t *testing.T) {
	if Choices != expectedChoices {
		t.Errorf("Choices = %q, want %q", Choices, expectedChoices)
	}
	got := Completions()
	if !slices.Equal(got, expectedCompletions) {
		t.Errorf("Completions() = %q, want %q", got, expectedCompletions)
	}
	for _, name := range got {
		if _, ok := Parse(name); !ok {
			t.Errorf("completion %q does not parse", name)
		}
	}
	// Every canonical name is offered.
	for _, theme := range expectedThemes {
		if !slices.Contains(got, theme.Name()) {
			t.Errorf("completions lack %q", theme.Name())
		}
	}
	// The result is a copy: changing it does not change what the next call returns.
	got[0] = "changed"
	if again := Completions(); !slices.Equal(again, expectedCompletions) {
		t.Errorf("Completions() after caller mutation = %q", again)
	}
	for _, choice := range []string{"mocha", "gruvbox", "latte", "monochrome", "terminal"} {
		if _, ok := Parse(choice); !ok {
			t.Errorf("choice %q does not parse", choice)
		}
	}
}

func TestSyntaxColors(t *testing.T) {
	covered := map[syntax.Style]int{}
	for _, row := range expectedSyntax {
		covered[row.style]++
	}
	for style := syntax.Plain; style <= syntax.Link; style++ {
		if covered[style] != 1 {
			t.Errorf("style %d appears %d times in the generated syntax() arms", style, covered[style])
		}
	}
	for _, theme := range expectedThemes {
		p := theme.Palette()
		for _, row := range expectedSyntax {
			if got, want := p.Syntax(row.style), row.color(p); got != want {
				t.Errorf("%s: Syntax(%d) = %+v, want %+v", theme.Name(), row.style, got, want)
			}
		}
	}
	// Spot checks that read the Rust by hand rather than through the generator.
	mocha := CatppuccinMocha.Palette()
	if got := mocha.Syntax(syntax.Heading); got != (Color{Kind: ColorRGB, R: 0xcb, G: 0xa6, B: 0xf7}) {
		t.Errorf("mocha heading = %+v", got)
	}
	if got := Terminal.Palette().Syntax(syntax.Link); got != (Color{Kind: ColorAnsi, Index: 12}) {
		t.Errorf("terminal link = %+v, want ANSI 12 (crossterm Blue)", got)
	}
	if got := Terminal.Palette().Syntax(syntax.Plain); got.Kind != ColorReset {
		t.Errorf("terminal plain = %+v, want reset", got)
	}
}

func TestBold(t *testing.T) {
	for _, theme := range expectedThemes {
		p := theme.Palette()
		for style := syntax.Plain; style <= syntax.Link; style++ {
			want := style == syntax.Heading || style == syntax.Strong ||
				(theme == Monochrome && (style == syntax.Keyword || style == syntax.Macro))
			if got := p.Bold(style); got != want {
				t.Errorf("%s: Bold(%d) = %v, want %v", theme.Name(), style, got, want)
			}
		}
	}
}

func TestDiagnosticColors(t *testing.T) {
	for _, theme := range expectedThemes {
		p := theme.Palette()
		for _, row := range expectedDiagnostic {
			if got, want := p.Diagnostic(row.severity), row.color(p); got != want {
				t.Errorf("%s: Diagnostic(%d) = %+v, want %+v", theme.Name(), row.severity, got, want)
			}
		}
	}
	p := CatppuccinMocha.Palette()
	if p.Diagnostic(1) != p.error || p.Diagnostic(2) != p.warning || p.Diagnostic(3) != p.info || p.Diagnostic(0) != p.hint || p.Diagnostic(4) != p.hint {
		t.Error("severity mapping is not error/warning/info/hint")
	}
}

func TestValuesAreComparable(t *testing.T) {
	if CatppuccinMocha.Palette() != Theme(0).Palette() {
		t.Error("equal palettes compare unequal")
	}
	if CatppuccinMocha.Palette() == GruvboxDark.Palette() {
		t.Error("different palettes compare equal")
	}
	if rgb(0x010203) != (Color{Kind: ColorRGB, R: 1, G: 2, B: 3}) {
		t.Errorf("rgb(0x010203) = %+v", rgb(0x010203))
	}
	if ansi(8) == rgb(0x080808) || ansi(8) == (Color{}) {
		t.Error("colour kinds compare equal")
	}
}

func TestUndefinedThemeFallsBackToDefault(t *testing.T) {
	// Rust's enum cannot hold such a value; the port treats it as the default theme.
	bad := Terminal + 1
	if bad.Name() != CatppuccinMocha.Name() || bad.Palette() != CatppuccinMocha.Palette() {
		t.Errorf("Theme(%d) is not treated as the default theme", bad)
	}
}
