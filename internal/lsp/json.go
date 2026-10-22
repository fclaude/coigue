package lsp

// The Rust source passes serde_json::Value around. Here a decoded message is
// an `any` holding nil, bool, string, json.Number, []any or map[string]any;
// messages the client builds use the same shapes plus Go integers and
// []string. These helpers give the Value accessors the code relies on
// (indexing yields Null for anything missing, as_u64 only accepts plain
// non-negative integer literals, and so on) and the serde_json framing of
// numbers, string escapes and key order.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// object is a JSON object under construction or decoded.
type object = map[string]any

// index is value[key]: Null unless value is an object with that key.
func index(v any, key string) any {
	if m, ok := v.(object); ok {
		return m[key]
	}
	return nil
}

// get is value.get(key): present even when the value is null.
func get(v any, key string) (any, bool) {
	if m, ok := v.(object); ok {
		x, present := m[key]
		return x, present
	}
	return nil, false
}

func asString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

func asBool(v any) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}

func asArray(v any) ([]any, bool) {
	a, ok := v.([]any)
	return a, ok
}

func isObject(v any) bool {
	_, ok := v.(object)
	return ok
}

// asU64 is Value::as_u64: only integer literals without sign, fraction or
// exponent that fit in 64 bits (everything else is a float to serde_json).
func asU64(v any) (uint64, bool) {
	switch n := v.(type) {
	case json.Number:
		num, ok := parseNumber(string(n))
		return num.u, ok && num.kind == numberPosInt
	case int:
		return uint64(n), n >= 0
	case int64:
		return uint64(n), n >= 0
	case uint64:
		return n, true
	case uint8:
		return uint64(n), true
	}
	return 0, false
}

// asI64 is Value::as_i64. "-0" is a float to serde_json.
func asI64(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		num, ok := parseNumber(string(n))
		switch {
		case !ok:
		case num.kind == numberNegInt:
			return num.i, true
		case num.kind == numberPosInt && num.u <= math.MaxInt64:
			return int64(num.u), true
		}
		return 0, false
	case int:
		return int64(n), true
	case int64:
		return n, true
	case uint64:
		return int64(n), n <= math.MaxInt64
	}
	return 0, false
}

// equalsInt is `value == 3`: the value is an integer number equal to n.
func equalsInt(v any, n int64) bool {
	i, ok := asI64(v)
	return ok && i == n
}

// equalsString is `value == "text"`.
func equalsString(v any, s string) bool {
	got, ok := v.(string)
	return ok && got == s
}

// Strict decoding, as serde_json::from_slice::<Value> does it. encoding/json
// is more lenient in four places that are checked first: invalid UTF-8,
// unpaired surrogate escapes, nesting of 128 or more containers, and numbers
// that overflow a double.

const recursionLimit = 128

var errRecursionLimit = errors.New("recursion limit exceeded")

func hex4(b []byte) (int, bool) {
	if len(b) < 4 {
		return 0, false
	}
	n := 0
	for _, c := range b[:4] {
		d := hexValue(c)
		if d < 0 {
			return 0, false
		}
		n = n<<4 | d
	}
	return n, true
}

func precheckJSON(data []byte) error {
	if !utf8.Valid(data) {
		return errors.New("invalid unicode code point")
	}
	depth := 0
	inString := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if !inString {
			switch c {
			case '"':
				inString = true
			case '[', '{':
				depth++
				if depth >= recursionLimit {
					return errRecursionLimit
				}
			case ']', '}':
				depth--
			}
			continue
		}
		switch c {
		case '"':
			inString = false
		case '\\':
			i++
			if i >= len(data) {
				return nil // the decoder reports the truncated escape
			}
			if data[i] != 'u' {
				continue
			}
			n, ok := hex4(data[i+1:])
			if !ok {
				return nil // malformed escapes are the decoder's to report
			}
			i += 4
			switch {
			case n >= 0xdc00 && n <= 0xdfff:
				return errors.New("lone leading surrogate in hex escape")
			case n >= 0xd800 && n <= 0xdbff:
				if i+2 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
					return errors.New("lone leading surrogate in hex escape")
				}
				low, ok := hex4(data[i+3:])
				if !ok {
					return nil
				}
				if low < 0xdc00 || low > 0xdfff {
					return errors.New("lone leading surrogate in hex escape")
				}
				i += 6
			}
		}
	}
	return nil
}

// checkNumbers rejects floats that overflow, which serde_json reports as
// "number out of range".
func checkNumbers(v any) error {
	switch x := v.(type) {
	case json.Number:
		if _, ok := parseNumber(string(x)); !ok {
			return errors.New("number out of range")
		}
	case []any:
		for _, e := range x {
			if err := checkNumbers(e); err != nil {
				return err
			}
		}
	case object:
		for _, e := range x {
			if err := checkNumbers(e); err != nil {
				return err
			}
		}
	}
	return nil
}

// unmarshal is serde_json::from_slice::<Value>.
func unmarshal(data []byte) (any, error) {
	if err := precheckJSON(data); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing characters")
	}
	if err := checkNumbers(value); err != nil {
		return nil, err
	}
	return value, nil
}

// marshal is serde_json::to_vec: compact, keys in byte order, only the
// escapes JSON requires.
func marshal(v any) []byte {
	return appendJSON(nil, v)
}

func appendJSON(dst []byte, v any) []byte {
	switch x := v.(type) {
	case nil:
		return append(dst, "null"...)
	case bool:
		return strconv.AppendBool(dst, x)
	case string:
		return appendString(dst, x)
	case json.Number:
		return appendNumber(dst, x)
	case int:
		return strconv.AppendInt(dst, int64(x), 10)
	case int32:
		return strconv.AppendInt(dst, int64(x), 10)
	case int64:
		return strconv.AppendInt(dst, x, 10)
	case uint8:
		return strconv.AppendUint(dst, uint64(x), 10)
	case uint32:
		return strconv.AppendUint(dst, uint64(x), 10)
	case uint64:
		return strconv.AppendUint(dst, x, 10)
	case []string:
		dst = append(dst, '[')
		for i, s := range x {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = appendString(dst, s)
		}
		return append(dst, ']')
	case []any:
		dst = append(dst, '[')
		for i, e := range x {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = appendJSON(dst, e)
		}
		return append(dst, ']')
	case object:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		dst = append(dst, '{')
		for i, k := range keys {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = appendString(dst, k)
			dst = append(dst, ':')
			dst = appendJSON(dst, x[k])
		}
		return append(dst, '}')
	}
	panic("lsp: unsupported JSON value")
}

// appendNumber writes a decoded number the way serde_json writes it back:
// integers as they are, everything else as the double it was parsed to
// (so "1.50" becomes 1.5 and "1e2" becomes 100.0).
func appendNumber(dst []byte, n json.Number) []byte {
	num, _ := parseNumber(string(n))
	switch num.kind {
	case numberPosInt:
		return strconv.AppendUint(dst, num.u, 10)
	case numberNegInt:
		return strconv.AppendInt(dst, num.i, 10)
	}
	return appendFloat(dst, num.f)
}

// appendFloat is how serde_json writes a double (the zmij crate): shortest
// round-trip digits, plain notation for decimal exponents from -5 to 15
// (always with a fraction digit) and scientific notation with a signed
// exponent outside.
func appendFloat(dst []byte, f float64) []byte {
	switch {
	case math.IsNaN(f) || math.IsInf(f, 0):
		return append(dst, "null"...)
	case f == 0:
		if math.Signbit(f) {
			return append(dst, "-0.0"...)
		}
		return append(dst, "0.0"...)
	case f < 0:
		dst = append(dst, '-')
		f = -f
	}
	mantissa, exponent, _ := strings.Cut(strconv.FormatFloat(f, 'e', -1, 64), "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	e10, _ := strconv.Atoi(exponent)
	length := len(digits)
	k := e10 - (length - 1)
	kk := length + k // 10^(kk-1) <= f < 10^kk
	switch {
	case k >= 0 && kk <= 16:
		dst = append(dst, digits...)
		dst = append(dst, strings.Repeat("0", kk-length)...)
		return append(dst, '.', '0')
	case 0 < kk && kk <= 16:
		dst = append(dst, digits[:kk]...)
		dst = append(dst, '.')
		return append(dst, digits[kk:]...)
	case -5 < kk && kk <= 0:
		dst = append(dst, '0', '.')
		dst = append(dst, strings.Repeat("0", -kk)...)
		return append(dst, digits...)
	case length == 1:
		dst = append(dst, digits...)
	default:
		dst = append(dst, digits[0], '.')
		dst = append(dst, digits[1:]...)
	}
	dst = append(dst, 'e')
	if kk-1 >= 0 {
		dst = append(dst, '+')
	}
	return strconv.AppendInt(dst, int64(kk-1), 10)
}

func appendString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	for _, r := range s {
		switch {
		case r == '"':
			dst = append(dst, '\\', '"')
		case r == '\\':
			dst = append(dst, '\\', '\\')
		case r == '\b':
			dst = append(dst, '\\', 'b')
		case r == '\f':
			dst = append(dst, '\\', 'f')
		case r == '\n':
			dst = append(dst, '\\', 'n')
		case r == '\r':
			dst = append(dst, '\\', 'r')
		case r == '\t':
			dst = append(dst, '\\', 't')
		case r < 0x20:
			dst = append(dst, '\\', 'u', '0', '0', "0123456789abcdef"[r>>4], "0123456789abcdef"[r&0xf])
		default:
			dst = utf8.AppendRune(dst, r)
		}
	}
	return append(dst, '"')
}

// equalJSON compares two values the way serde_json::Value equality does for
// the shapes used here, through their serialization.
func equalJSON(a, b any) bool {
	return bytes.Equal(marshal(a), marshal(b))
}

// Typed reads, as serde's derived Deserialize does them from a Value.

// structFields reads the fields of a derived struct: from a JSON object
// (unknown keys ignored; a missing key is reported through present) or from a
// JSON array holding exactly one element per field.
func structFields(v any, names ...string) (values []any, present []bool, ok bool) {
	values = make([]any, len(names))
	present = make([]bool, len(names))
	switch x := v.(type) {
	case object:
		for i, name := range names {
			values[i], present[i] = x[name]
		}
		return values, present, true
	case []any:
		if len(x) != len(names) {
			return nil, nil, false
		}
		for i := range names {
			values[i], present[i] = x[i], true
		}
		return values, present, true
	}
	return nil, nil, false
}

// toUint decodes an unsigned integer field of at most the given bit size.
func toUint(v any, bits uint) (uint64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	num, ok := parseNumber(string(n))
	if !ok || num.kind != numberPosInt || (bits < 64 && num.u >= 1<<bits) {
		return 0, false
	}
	return num.u, true
}

// toInt32 decodes an i32 field.
func toInt32(v any) (int32, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	num, ok := parseNumber(string(n))
	switch {
	case !ok:
	case num.kind == numberPosInt && num.u <= math.MaxInt32:
		return int32(num.u), true
	case num.kind == numberNegInt && num.i >= math.MinInt32:
		return int32(num.i), true
	}
	return 0, false
}

func toStrings(v any) ([]string, bool) {
	items, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}
