package lsp

import (
	"math"
	"strconv"
)

// serde_json's number parser, without its "float_roundtrip" feature (which the
// editor does not enable). Integers that fit stay integers; everything else
// becomes a double through the crate's own scaling arithmetic, which is not
// always the correctly rounded value strconv would give for long literals,
// so the same literals are parsed the same way here. The literal has already
// been checked to be valid JSON by the decoder.

type numberKind uint8

const (
	numberPosInt numberKind = iota
	numberNegInt
	numberFloat
)

// number is a parsed JSON number as serde_json holds it.
type number struct {
	kind numberKind
	u    uint64 // numberPosInt
	i    int64  // numberNegInt
	f    float64
}

// pow10 is serde_json's POW10: the doubles 1e0 to 1e308, each correctly
// rounded. It is filled once and never written again.
var pow10 = func() (table [309]float64) {
	for i := range table {
		table[i], _ = strconv.ParseFloat("1e"+strconv.Itoa(i), 64)
	}
	return table
}()

// numberScanner walks one literal.
type numberScanner struct {
	s   string
	pos int
}

func (n *numberScanner) peek() byte {
	if n.pos < len(n.s) {
		return n.s[n.pos]
	}
	return 0
}

func (n *numberScanner) isDigit() bool {
	c := n.peek()
	return c >= '0' && c <= '9'
}

// parseNumber is serde_json's parse for a valid literal; ok is false for
// "number out of range".
func parseNumber(literal string) (num number, ok bool) {
	n := &numberScanner{s: literal}
	positive := true
	if n.peek() == '-' {
		positive = false
		n.pos++
	}
	if n.peek() == '0' {
		n.pos++
		return n.finishNumber(positive, 0)
	}
	significand := uint64(n.peek() - '0')
	n.pos++
	for n.isDigit() {
		digit := uint64(n.peek() - '0')
		if significand > (math.MaxUint64-digit)/10 {
			f, ok := n.parseLongInteger(positive, significand)
			return number{kind: numberFloat, f: f}, ok
		}
		n.pos++
		significand = significand*10 + digit
	}
	return n.finishNumber(positive, significand)
}

func (n *numberScanner) finishNumber(positive bool, significand uint64) (number, bool) {
	switch n.peek() {
	case '.':
		f, ok := n.parseDecimal(positive, significand, 0)
		return number{kind: numberFloat, f: f}, ok
	case 'e', 'E':
		f, ok := n.parseExponent(positive, significand, 0)
		return number{kind: numberFloat, f: f}, ok
	}
	if positive {
		return number{kind: numberPosInt, u: significand}, true
	}
	neg := -int64(significand) // wrapping, like wrapping_neg
	// Convert into a float if we underflow, or on `-0`.
	if neg >= 0 {
		return number{kind: numberFloat, f: -float64(significand)}, true
	}
	return number{kind: numberNegInt, i: neg}, true
}

func (n *numberScanner) parseDecimal(positive bool, significand uint64, exponentBefore int32) (float64, bool) {
	n.pos++ // the point
	exponentAfter := int32(0)
	for n.isDigit() {
		digit := uint64(n.peek() - '0')
		if significand > (math.MaxUint64-digit)/10 {
			// The next multiply/add would overflow, so ignore all further digits.
			for n.isDigit() {
				n.pos++
			}
			exponent := exponentBefore + exponentAfter
			if c := n.peek(); c == 'e' || c == 'E' {
				return n.parseExponent(positive, significand, exponent)
			}
			return fromParts(positive, significand, exponent)
		}
		n.pos++
		significand = significand*10 + digit
		exponentAfter--
	}
	exponent := exponentBefore + exponentAfter
	if c := n.peek(); c == 'e' || c == 'E' {
		return n.parseExponent(positive, significand, exponent)
	}
	return fromParts(positive, significand, exponent)
}

func (n *numberScanner) parseExponent(positive bool, significand uint64, startingExp int32) (float64, bool) {
	n.pos++ // the 'e'
	positiveExp := true
	switch n.peek() {
	case '+':
		n.pos++
	case '-':
		positiveExp = false
		n.pos++
	}
	exp := int32(n.peek() - '0')
	n.pos++
	for n.isDigit() {
		digit := int32(n.peek() - '0')
		n.pos++
		if exp > (math.MaxInt32-digit)/10 {
			// Error instead of +/- infinity.
			if significand != 0 && positiveExp {
				return 0, false
			}
			for n.isDigit() {
				n.pos++
			}
			if positive {
				return 0, true
			}
			return math.Copysign(0, -1), true
		}
		exp = exp*10 + digit
	}
	var final int32
	if positiveExp {
		final = saturatingAdd32(startingExp, exp)
	} else {
		final = saturatingAdd32(startingExp, -exp)
	}
	return fromParts(positive, significand, final)
}

func saturatingAdd32(a, b int32) int32 {
	sum := int64(a) + int64(b)
	switch {
	case sum > math.MaxInt32:
		return math.MaxInt32
	case sum < math.MinInt32:
		return math.MinInt32
	}
	return int32(sum)
}

func (n *numberScanner) parseLongInteger(positive bool, significand uint64) (float64, bool) {
	exponent := int32(0)
	for {
		switch {
		case n.isDigit():
			n.pos++
			exponent++
		case n.peek() == '.':
			return n.parseDecimal(positive, significand, exponent)
		case n.peek() == 'e' || n.peek() == 'E':
			return n.parseExponent(positive, significand, exponent)
		default:
			return fromParts(positive, significand, exponent)
		}
	}
}

// fromParts is f64_from_parts.
func fromParts(positive bool, significand uint64, exponent int32) (float64, bool) {
	f := float64(significand)
	for {
		abs := int64(exponent)
		if abs < 0 {
			abs = -abs
		}
		if abs < int64(len(pow10)) {
			pow := pow10[abs]
			if exponent >= 0 {
				f *= pow
				if math.IsInf(f, 0) {
					return 0, false
				}
			} else {
				f /= pow
			}
			break
		}
		if f == 0 {
			break
		}
		if exponent >= 0 {
			return 0, false
		}
		f /= 1e308
		exponent += 308
	}
	if positive {
		return f, true
	}
	return -f, true
}
