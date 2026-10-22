package walk

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// This file is the glob dialect of the Rust `globset` crate (0.4.20) as the
// `ignore` crate configures it for gitignore lines: literal_separator and
// backslash_escape on, allow_unclosed_class on, case-sensitive. A glob is
// parsed into the same token list globset builds, and matched with the
// semantics of the regular expression globset derives from it. Those regular
// expressions run on bytes (`(?-u)`), so `?` and `[...]` consume one byte, not
// one character. Rust oddity preserved: a class containing a non-ASCII
// character is spelled out as `\xNN` escapes, so it is a set of single bytes.

type tokenKind uint8

const (
	tokLiteral tokenKind = iota
	tokAny
	tokZeroOrMore
	tokRecursivePrefix
	tokRecursiveSuffix
	tokRecursiveZeroOrMore
	tokClass
	tokAlternates
)

type token struct {
	kind    tokenKind
	literal rune
	negated bool
	ranges  [][2]rune
	alts    [][]token
}

var (
	errInvalidRange      = errors.New("invalid range")
	errUnopenedAlternate = errors.New("unopened alternate group; missing '{' (maybe escape '}' with '[}]'?)")
	errUnclosedAlternate = errors.New("unclosed alternate group; missing '}' (maybe escape '{' with '[{]'?)")
	errDanglingEscape    = errors.New("dangling '\\'")
)

func isSeparator(r rune) bool { return r == '/' }

type optRune struct {
	r  rune
	ok bool
}

// parser mirrors globset's Parser, including its prev/cur bookkeeping, which
// the `**` rules consult.
type parser struct {
	glob              string
	chars             []rune
	pos               int
	alternatesStack   []int
	branches          [][]token
	prev, cur         optRune
	foundUnclosedLass bool
}

// parseGlob parses a gitignore-flavoured glob into globset tokens.
func parseGlob(glob string) ([]token, error) {
	p := &parser{glob: glob, chars: []rune(glob), branches: [][]token{nil}}
	if err := p.parse(); err != nil {
		return nil, err
	}
	if len(p.branches) > 1 {
		return nil, fmt.Errorf("error parsing glob '%s': %w", glob, errUnclosedAlternate)
	}
	return p.branches[0], nil
}

func (p *parser) errorf(err error) error {
	return fmt.Errorf("error parsing glob '%s': %w", p.glob, err)
}

func (p *parser) bump() optRune {
	p.prev = p.cur
	if p.pos < len(p.chars) {
		p.cur = optRune{p.chars[p.pos], true}
		p.pos++
	} else {
		p.cur = optRune{}
	}
	return p.cur
}

func (p *parser) peek() optRune {
	if p.pos < len(p.chars) {
		return optRune{p.chars[p.pos], true}
	}
	return optRune{}
}

func (p *parser) parse() error {
	for {
		c := p.bump()
		if !c.ok {
			return nil
		}
		var err error
		switch {
		case c.r == '?':
			p.pushToken(token{kind: tokAny})
		case c.r == '*':
			err = p.parseStar()
		case c.r == '[' && !p.foundUnclosedLass:
			err = p.parseClass()
		case c.r == '{':
			p.pushAlternate()
		case c.r == '}':
			err = p.popAlternate()
		case c.r == ',':
			p.parseComma()
		case c.r == '\\':
			err = p.parseBackslash()
		default:
			p.pushToken(token{kind: tokLiteral, literal: c.r})
		}
		if err != nil {
			return err
		}
	}
}

func (p *parser) pushAlternate() {
	p.alternatesStack = append(p.alternatesStack, len(p.branches))
	p.branches = append(p.branches, nil)
}

func (p *parser) popAlternate() error {
	if len(p.alternatesStack) == 0 {
		return p.errorf(errUnopenedAlternate)
	}
	start := p.alternatesStack[len(p.alternatesStack)-1]
	p.alternatesStack = p.alternatesStack[:len(p.alternatesStack)-1]
	alts := append([][]token(nil), p.branches[start:]...)
	p.branches = p.branches[:start]
	p.pushToken(token{kind: tokAlternates, alts: alts})
	return nil
}

func (p *parser) pushToken(t token) {
	last := len(p.branches) - 1
	p.branches[last] = append(p.branches[last], t)
}

func (p *parser) popToken() token {
	last := len(p.branches) - 1
	branch := p.branches[last]
	t := branch[len(branch)-1]
	p.branches[last] = branch[:len(branch)-1]
	return t
}

func (p *parser) haveTokens() bool { return len(p.branches[len(p.branches)-1]) > 0 }

func (p *parser) parseComma() {
	// Outside an alternate group a comma is an ordinary character.
	if len(p.alternatesStack) == 0 {
		p.pushToken(token{kind: tokLiteral, literal: ','})
	} else {
		p.branches = append(p.branches, nil)
	}
}

func (p *parser) parseBackslash() error {
	c := p.bump()
	if !c.ok {
		return p.errorf(errDanglingEscape)
	}
	p.pushToken(token{kind: tokLiteral, literal: c.r})
	return nil
}

func (p *parser) parseStar() error {
	prev := p.prev
	if next := p.peek(); !next.ok || next.r != '*' {
		p.pushToken(token{kind: tokZeroOrMore})
		return nil
	}
	p.bump()
	if !p.haveTokens() {
		if next := p.peek(); next.ok && !isSeparator(next.r) {
			p.pushToken(token{kind: tokZeroOrMore})
			p.pushToken(token{kind: tokZeroOrMore})
		} else {
			p.pushToken(token{kind: tokRecursivePrefix})
			p.bump()
		}
		return nil
	}
	if !(prev.ok && isSeparator(prev.r)) {
		if len(p.branches) <= 1 || !(prev.ok && (prev.r == ',' || prev.r == '{')) {
			p.pushToken(token{kind: tokZeroOrMore})
			p.pushToken(token{kind: tokZeroOrMore})
			return nil
		}
	}
	var isSuffix bool
	switch next := p.peek(); {
	case !next.ok:
		p.bump()
		isSuffix = true
	case (next.r == ',' || next.r == '}') && len(p.branches) >= 2:
		isSuffix = true
	case isSeparator(next.r):
		p.bump()
		isSuffix = false
	default:
		p.pushToken(token{kind: tokZeroOrMore})
		p.pushToken(token{kind: tokZeroOrMore})
		return nil
	}
	switch last := p.popToken(); last.kind {
	case tokRecursivePrefix:
		p.pushToken(token{kind: tokRecursivePrefix})
	case tokRecursiveSuffix:
		p.pushToken(token{kind: tokRecursiveSuffix})
	default:
		if isSuffix {
			p.pushToken(token{kind: tokRecursiveSuffix})
		} else {
			p.pushToken(token{kind: tokRecursiveZeroOrMore})
		}
	}
	return nil
}

func (p *parser) parseClass() error {
	savedPos, savedPrev, savedCur := p.pos, p.prev, p.cur
	var ranges [][2]rune
	negated := false
	if next := p.peek(); next.ok && (next.r == '!' || next.r == '^') {
		p.bump()
		negated = true
	}
	first := true
	inRange := false
	addToLastRange := func(add rune) error {
		r := &ranges[len(ranges)-1]
		r[1] = add
		if r[1] < r[0] {
			return p.errorf(fmt.Errorf("%w; '%c' > '%c'", errInvalidRange, r[0], r[1]))
		}
		return nil
	}
	for {
		c := p.bump()
		if !c.ok {
			// allow_unclosed_class: the '[' is literal and no later '[' starts
			// a class (this keeps `[[[[...` linear).
			p.pos, p.cur, p.prev = savedPos, savedCur, savedPrev
			p.foundUnclosedLass = true
			p.pushToken(token{kind: tokLiteral, literal: '['})
			return nil
		}
		switch c.r {
		case ']':
			if first {
				ranges = append(ranges, [2]rune{']', ']'})
			} else {
				goto done
			}
		case '-':
			switch {
			case first:
				ranges = append(ranges, [2]rune{'-', '-'})
			case inRange:
				if err := addToLastRange('-'); err != nil {
					return err
				}
				inRange = false
			default:
				inRange = true
			}
		default:
			if inRange {
				if err := addToLastRange(c.r); err != nil {
					return err
				}
			} else {
				ranges = append(ranges, [2]rune{c.r, c.r})
			}
			inRange = false
		}
		first = false
	}
done:
	if inRange {
		// The class ended in '-': it is a literal member.
		ranges = append(ranges, [2]rune{'-', '-'})
	}
	p.pushToken(token{kind: tokClass, negated: negated, ranges: ranges})
	return nil
}

// ---- matching ---------------------------------------------------------

type opcode uint8

const (
	opByte opcode = iota
	opAny
	opNotSlash
	opClass
	opSplit
	opJmp
	opMatch
)

type inst struct {
	op   opcode
	b    byte
	set  *[256]bool
	x, y int
}

type fastKind uint8

const (
	fastNone fastKind = iota
	fastLiteral
	fastBasename
	fastExtension
	fastAll
)

// matcher is one compiled glob.
type matcher struct {
	fast  fastKind
	lit   string
	insts []inst
}

func compileGlob(tokens []token) *matcher {
	m := &matcher{}
	// Special case in globset: a glob that is only `**` matches everything.
	if len(tokens) == 1 && tokens[0].kind == tokRecursivePrefix {
		m.fast = fastAll
		return m
	}
	if lit, ok := basenameLiteral(tokens); ok {
		m.fast, m.lit = fastBasename, lit
		return m
	}
	if lit, ok := literalOf(tokens); ok {
		m.fast, m.lit = fastLiteral, lit
		return m
	}
	if ext, ok := extensionOf(tokens); ok {
		m.fast, m.lit = fastExtension, ext
		return m
	}
	c := &compiler{}
	c.tokens(tokens)
	c.emit(inst{op: opMatch})
	m.insts = c.insts
	return m
}

// literalOf, extensionOf and basenameLiteral are globset's match strategies of
// the same names; they only avoid running the generic matcher.
func literalOf(tokens []token) (string, bool) {
	var b strings.Builder
	for _, t := range tokens {
		if t.kind != tokLiteral {
			return "", false
		}
		b.WriteRune(t.literal)
	}
	if b.Len() == 0 {
		return "", false
	}
	return b.String(), true
}

func extensionOf(tokens []token) (string, bool) {
	if len(tokens) == 0 {
		return "", false
	}
	start := 0
	if tokens[0].kind == tokRecursivePrefix {
		start = 1
	}
	if start >= len(tokens) || tokens[start].kind != tokZeroOrMore {
		return "", false
	}
	// literal_separator is on, so `*` alone cannot cross a `/`: only the
	// `**/*.ext` shape qualifies.
	if start == 0 {
		return "", false
	}
	if start+1 >= len(tokens) || tokens[start+1].kind != tokLiteral || tokens[start+1].literal != '.' {
		return "", false
	}
	var b strings.Builder
	b.WriteByte('.')
	for _, t := range tokens[start+2:] {
		if t.kind != tokLiteral || t.literal == '.' || t.literal == '/' {
			return "", false
		}
		b.WriteRune(t.literal)
	}
	return b.String(), true
}

func basenameLiteral(tokens []token) (string, bool) {
	if len(tokens) < 2 || tokens[0].kind != tokRecursivePrefix {
		return "", false
	}
	var b strings.Builder
	for _, t := range tokens[1:] {
		if t.kind != tokLiteral || t.literal == '/' {
			return "", false
		}
		b.WriteRune(t.literal)
	}
	return b.String(), true
}

type compiler struct{ insts []inst }

func (c *compiler) emit(i inst) int {
	c.insts = append(c.insts, i)
	return len(c.insts) - 1
}

func (c *compiler) bytes(s string) {
	for i := 0; i < len(s); i++ {
		c.emit(inst{op: opByte, b: s[i]})
	}
}

// star emits `x*` for a single-byte instruction.
func (c *compiler) star(i inst) {
	split := c.emit(inst{op: opSplit})
	c.emit(i)
	c.emit(inst{op: opJmp, x: split})
	c.insts[split].x = split + 1
	c.insts[split].y = len(c.insts)
}

func (c *compiler) tokens(tokens []token) {
	for _, t := range tokens {
		switch t.kind {
		case tokLiteral:
			c.bytes(string(t.literal))
		case tokAny:
			c.emit(inst{op: opNotSlash})
		case tokZeroOrMore:
			c.star(inst{op: opNotSlash})
		case tokRecursivePrefix:
			// (?:/?|.*/)
			split := c.emit(inst{op: opSplit})
			optional := c.emit(inst{op: opSplit})
			c.emit(inst{op: opByte, b: '/'})
			jump := c.emit(inst{op: opJmp})
			second := len(c.insts)
			c.star(inst{op: opAny})
			c.emit(inst{op: opByte, b: '/'})
			end := len(c.insts)
			c.insts[split].x, c.insts[split].y = optional, second
			c.insts[optional].x, c.insts[optional].y = optional+1, jump+0
			c.insts[jump].x = end
		case tokRecursiveSuffix:
			// /.*
			c.emit(inst{op: opByte, b: '/'})
			c.star(inst{op: opAny})
		case tokRecursiveZeroOrMore:
			// (?:/|/.*/)
			split := c.emit(inst{op: opSplit})
			c.emit(inst{op: opByte, b: '/'})
			jump := c.emit(inst{op: opJmp})
			second := len(c.insts)
			c.emit(inst{op: opByte, b: '/'})
			c.star(inst{op: opAny})
			c.emit(inst{op: opByte, b: '/'})
			c.insts[split].x, c.insts[split].y = split+1, second
			c.insts[jump].x = len(c.insts)
		case tokClass:
			c.emit(inst{op: opClass, set: classSet(t)})
		case tokAlternates:
			c.alternates(t.alts)
		}
	}
}

// alternates emits `(?:p1|p2|...)` over the branches that produce any
// pattern text; with none it emits nothing (globset drops empty branches, and
// an alternation with no branch left, because `()` would be a regex error).
func (c *compiler) alternates(alts [][]token) {
	var parts [][]token
	for _, alt := range alts {
		if !emitsNothing(alt) {
			parts = append(parts, alt)
		}
	}
	if len(parts) == 0 {
		return
	}
	var jumps []int
	for i, part := range parts {
		if i < len(parts)-1 {
			split := c.emit(inst{op: opSplit})
			c.insts[split].x = split + 1
			c.tokens(part)
			jumps = append(jumps, c.emit(inst{op: opJmp}))
			c.insts[split].y = len(c.insts)
		} else {
			c.tokens(part)
		}
	}
	for _, j := range jumps {
		c.insts[j].x = len(c.insts)
	}
}

// emitsNothing reports whether the token list yields an empty regex text.
func emitsNothing(tokens []token) bool {
	for _, t := range tokens {
		if t.kind != tokAlternates {
			return false
		}
		allEmpty := true
		for _, alt := range t.alts {
			if !emitsNothing(alt) {
				allEmpty = false
				break
			}
		}
		if !allEmpty {
			return false
		}
	}
	return true
}

// classSet turns a class into the byte set globset's regex denotes: every
// character is written as its (escaped) UTF-8 bytes, and a range `a-b` as
// `<bytes of a>-<bytes of b>`, so only the last byte of `a` and the first byte
// of `b` form the range.
func classSet(t token) *[256]bool {
	var set [256]bool
	for _, r := range t.ranges {
		lo := utf8.AppendRune(nil, r[0])
		if r[0] == r[1] {
			for _, b := range lo {
				set[b] = true
			}
			continue
		}
		hi := utf8.AppendRune(nil, r[1])
		for _, b := range lo[:len(lo)-1] {
			set[b] = true
		}
		for b := int(lo[len(lo)-1]); b <= int(hi[0]); b++ {
			set[b] = true
		}
		for _, b := range hi[1:] {
			set[b] = true
		}
	}
	if t.negated {
		for i := range set {
			set[i] = !set[i]
		}
	}
	return &set
}

// match reports whether the whole path matches the glob.
func (m *matcher) match(path string) bool {
	switch m.fast {
	case fastAll:
		return true
	case fastLiteral:
		return path == m.lit
	case fastBasename:
		name := basename(path)
		return name != "" && name == m.lit
	case fastExtension:
		name := basename(path)
		dot := strings.LastIndexByte(name, '.')
		return dot >= 0 && name[dot:] == m.lit
	}
	return m.run(path)
}

// basename is globset's Candidate basename: the text after the last '/', or
// nothing for an empty path or "..".
func basename(path string) string {
	if path == "" {
		return ""
	}
	name := path[strings.LastIndexByte(path, '/')+1:]
	if name == ".." {
		return ""
	}
	return name
}

// run simulates the compiled program (a Thompson NFA) over the path's bytes.
func (m *matcher) run(path string) bool {
	n := len(m.insts)
	var stackBuf [64]int
	var curBuf, nextBuf, seenBuf [64]int
	cur, next := curBuf[:0], nextBuf[:0]
	var mark []int
	if n <= len(seenBuf) {
		mark = seenBuf[:n]
	} else {
		mark = make([]int, n)
	}
	generation := 0
	stack := stackBuf[:0]
	add := func(list []int, pc int) []int {
		stack = append(stack[:0], pc)
		for len(stack) > 0 {
			pc := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if mark[pc] == generation {
				continue
			}
			mark[pc] = generation
			in := &m.insts[pc]
			switch in.op {
			case opJmp:
				stack = append(stack, in.x)
			case opSplit:
				stack = append(stack, in.y, in.x)
			default:
				list = append(list, pc)
			}
		}
		return list
	}
	generation = 1
	cur = add(cur, 0)
	for i := 0; i <= len(path); i++ {
		for _, pc := range cur {
			if m.insts[pc].op == opMatch && i == len(path) {
				return true
			}
		}
		if i == len(path) || len(cur) == 0 {
			break
		}
		b := path[i]
		generation++
		next = next[:0]
		for _, pc := range cur {
			in := &m.insts[pc]
			ok := false
			switch in.op {
			case opByte:
				ok = in.b == b
			case opAny:
				ok = true
			case opNotSlash:
				ok = b != '/'
			case opClass:
				ok = in.set[b]
			}
			if ok {
				next = add(next, pc+1)
			}
		}
		cur, next = next, cur
	}
	return false
}
