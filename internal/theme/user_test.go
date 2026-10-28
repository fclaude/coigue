package theme

import (
	"slices"
	"strings"
	"testing"

	"github.com/fclaude/coigue/internal/syntax"
)

var sentinel = Color{Kind: ColorRGB, R: 1, G: 2, B: 3}

// seen reads each overridable colour the way the renderer does, so a key that
// is wired to the wrong field shows up here and not only in a screen.
var seen = map[string]func(Palette) Color{
	"background": func(p Palette) Color { return p.Background },
	"foreground": func(p Palette) Color { return p.Foreground },
	"muted":      func(p Palette) Color { return p.Muted },
	"surface":    func(p Palette) Color { return p.Surface },
	"keyword":    func(p Palette) Color { return p.Syntax(syntax.Keyword) },
	"comment":    func(p Palette) Color { return p.Syntax(syntax.Comment) },
	"string":     func(p Palette) Color { return p.Syntax(syntax.String) },
	"number":     func(p Palette) Color { return p.Syntax(syntax.Number) },
	"macro":      func(p Palette) Color { return p.Syntax(syntax.Macro) },
	"lifetime":   func(p Palette) Color { return p.Syntax(syntax.Lifetime) },
	"type":       func(p Palette) Color { return p.Syntax(syntax.Type) },
	"function":   func(p Palette) Color { return p.Syntax(syntax.Function) },
	"property":   func(p Palette) Color { return p.Syntax(syntax.Property) },
	"constant":   func(p Palette) Color { return p.Syntax(syntax.Constant) },
	"namespace":  func(p Palette) Color { return p.Syntax(syntax.Namespace) },
	"error":      func(p Palette) Color { return p.Diagnostic(1) },
	"warning":    func(p Palette) Color { return p.Diagnostic(2) },
	"info":       func(p Palette) Color { return p.Diagnostic(3) },
	"hint":       func(p Palette) Color { return p.Diagnostic(4) },
}

func TestEveryKeyIsCheckedHere(t *testing.T) {
	var want []string
	for _, k := range Keys {
		if k != "bold-keywords" {
			want = append(want, k)
		}
	}
	var got []string
	for k := range seen {
		got = append(got, k)
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		t.Fatalf("Keys %v, checked here %v", want, got)
	}
}

func TestEachKeyChangesItsOwnColourAndNothingElse(t *testing.T) {
	base := CatppuccinMocha.Palette()
	for key := range seen {
		var s Set
		th, err := s.Define("mine", "mocha", []Entry{{Key: key, Value: "#010203"}})
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		p := s.Palette(th)
		for other, read := range seen {
			want := read(base)
			if other == key {
				want = sentinel
			}
			if got := read(p); got != want {
				t.Errorf("setting %s: %s reads %+v, want %+v", key, other, got, want)
			}
		}
		if p.Bold(syntax.Keyword) != base.Bold(syntax.Keyword) {
			t.Errorf("setting %s changed bold", key)
		}
	}
}

func TestAThemeWithOnlyABaseIsThatBase(t *testing.T) {
	for _, base := range []string{"mocha", "gruvbox", "latte", "monochrome", "terminal", "catppuccin-latte", ""} {
		var s Set
		th, err := s.Define("mine", base, nil)
		if err != nil {
			t.Fatalf("%q: %v", base, err)
		}
		want, _ := Parse(base)
		if base == "" {
			want = CatppuccinMocha
		}
		if s.Palette(th) != want.Palette() {
			t.Errorf("base %q: palette differs from the built-in one", base)
		}
	}
}

func TestBoldKeywordsAndDefaultColour(t *testing.T) {
	var s Set
	on, err := s.Define("loud", "mocha", []Entry{{Key: "bold-keywords", Value: true}})
	if err != nil {
		t.Fatal(err)
	}
	off, err := s.Define("quiet", "monochrome", []Entry{{Key: "bold-keywords", Value: false}})
	if err != nil {
		t.Fatal(err)
	}
	if !s.Palette(on).Bold(syntax.Keyword) || s.Palette(off).Bold(syntax.Keyword) {
		t.Fatalf("bold: loud %v, quiet %v", s.Palette(on).Bold(syntax.Keyword), s.Palette(off).Bold(syntax.Keyword))
	}
	d, err := s.Define("plain", "mocha", []Entry{{Key: "background", Value: "default"}, {Key: "keyword", Value: "#ABCDEF"}})
	if err != nil {
		t.Fatal(err)
	}
	p := s.Palette(d)
	if p.Background.Kind != ColorReset || p.Syntax(syntax.Keyword) != (Color{Kind: ColorRGB, R: 0xab, G: 0xcd, B: 0xef}) {
		t.Fatalf("background %+v keyword %+v", p.Background, p.Syntax(syntax.Keyword))
	}
}

func TestDefineErrorsNameTheProblemAndChangeNothing(t *testing.T) {
	long := strings.Repeat("a", 33)
	for _, c := range []struct {
		name, base string
		entries    []Entry
		want       string
	}{
		{"Mine", "", nil, `"Mine" is not a theme name`},
		{"9mine", "", nil, "is not a theme name"},
		{"my theme", "", nil, "is not a theme name"},
		{"", "", nil, "is not a theme name"},
		{long, "", nil, "is not a theme name"},
		{"mocha", "", nil, `the theme "mocha" already exists`},
		{"latte", "", nil, "already exists"},
		{"catppuccin-mocha", "", nil, "already exists"},
		{"terminal", "", nil, "already exists"},
		{"mine", "nord", nil, `base "nord" is not a built-in theme`},
		{"mine", "", []Entry{{"keywrd", "#ffffff"}}, `"keywrd" is not a theme entry`},
		{"mine", "", []Entry{{"keyword", "#fff"}}, "keyword: \"#fff\" is not a colour"},
		{"mine", "", []Entry{{"keyword", "red"}}, "keyword: \"red\" is not a colour"},
		{"mine", "", []Entry{{"string", "#gggggg"}}, "string: "},
		{"mine", "", []Entry{{"string", "ffffff"}}, "string: "},
		{"mine", "", []Entry{{"string", " #ffffff"}}, "string: "},
		{"mine", "", []Entry{{"keyword", true}}, "keyword takes a colour string"},
		{"mine", "", []Entry{{"bold-keywords", "yes"}}, "bold-keywords takes #t or #f"},
		{"mine", "", []Entry{{"keyword", "#ffffff"}, {"keyword", "#000000"}}, "keyword is given twice"},
	} {
		var s Set
		if _, err := s.Define(c.name, c.base, c.entries); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: error %v, want %q", c, err, c.want)
		}
		if len(s.user) != 0 {
			t.Errorf("%+v: a theme was added despite the error", c)
		}
	}
}

func TestAtMostSixteenUserThemes(t *testing.T) {
	var s Set
	for i := 0; i < MaxUser; i++ {
		if _, err := s.Define("t"+strings.Repeat("a", i), "", nil); err != nil {
			t.Fatalf("theme %d: %v", i, err)
		}
	}
	if _, err := s.Define("one-more", "", nil); err == nil || !strings.Contains(err.Error(), "at most 16 themes") {
		t.Fatalf("the seventeenth: %v", err)
	}
	if _, err := s.Define("t", "", nil); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("a name already used: %v", err)
	}
}

func TestASetWithNoUserThemesIsTheBuiltInOnes(t *testing.T) {
	var s Set
	if s.Choices() != Choices || !slices.Equal(s.Completions(), Completions()) {
		t.Fatalf("choices %q completions %v", s.Choices(), s.Completions())
	}
	for _, th := range []Theme{CatppuccinMocha, GruvboxDark, CatppuccinLatte, Monochrome, Terminal} {
		if s.Name(th) != th.Name() || s.Palette(th) != th.Palette() {
			t.Errorf("%v differs through the set", th)
		}
	}
}

func TestAModuleThemeNameHasOneSlash(t *testing.T) {
	var s Set
	th, err := s.Define("text-tools/dusk", "gruvbox", []Entry{{Key: "keyword", Value: "#ff79c6"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := s.Parse("text-tools/dusk"); !ok || got != th || s.Name(th) != "text-tools/dusk" {
		t.Fatalf("parse %v %v name %q", got, ok, s.Name(th))
	}
	if _, ok := s.Parse("dusk"); ok {
		t.Fatal("the short name of a module theme parsed")
	}
	for _, name := range []string{"Text/dusk", "text-tools/Dusk", "a/b/c", "/dusk", "text-tools/", "dusk/"} {
		if _, err := s.Define(name, "", nil); err == nil || !strings.Contains(err.Error(), "is not a theme name") {
			t.Errorf("%q: %v", name, err)
		}
	}
}

func TestUserThemesJoinTheSet(t *testing.T) {
	var s Set
	a, _ := s.Define("dusk", "gruvbox", nil)
	b, _ := s.Define("dawn", "latte", nil)
	if a == b || a <= Terminal || b <= Terminal {
		t.Fatalf("values %v %v", a, b)
	}
	if got, ok := s.Parse("dawn"); !ok || got != b || s.Name(b) != "dawn" {
		t.Fatalf("parse dawn: %v %v, name %q", got, ok, s.Name(b))
	}
	if got, ok := s.Parse("mocha"); !ok || got != CatppuccinMocha {
		t.Fatalf("a built-in name: %v %v", got, ok)
	}
	if _, ok := s.Parse("dusky"); ok {
		t.Fatal("an unknown name parsed")
	}
	if s.Choices() != Choices+"|dusk|dawn" {
		t.Fatalf("choices %q", s.Choices())
	}
	c := s.Completions()
	if len(c) != len(Completions())+2 || c[len(c)-2] != "dusk" || c[len(c)-1] != "dawn" {
		t.Fatalf("completions %v", c)
	}
	// A value this set never made is the default, as for the built-in enum.
	if s.Name(Theme(200)) != CatppuccinMocha.Name() || s.Palette(Theme(200)) != CatppuccinMocha.Palette() {
		t.Fatal("an unknown value is not the default")
	}
}
