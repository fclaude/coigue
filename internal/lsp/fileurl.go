package lsp

// File URLs, as the Rust `url` crate (2.5.8) builds and reads them:
//
//	Url::from_file_path(path).to_string()      -> fileURI
//	Url::from_directory_path(path).to_string() -> directoryURI
//	Url::parse(uri)?.to_file_path()            -> uriToPath
//
// Building is a direct transcription of path_to_file_url_segments. Reading
// needs the whole WHATWG parser of the crate (schemes, authorities, hosts,
// path normalisation, Windows drive letter quirks) because servers may answer
// with any URL and `to_file_path` accepts more than file: URLs (for example
// https://localhost/x resolves to /x). The parser below follows url/src/parser.rs
// function by function and builds the same serialization string, since several
// of its decisions look at the text produced so far.

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fclaude/coigue/internal/fsx"
)

// fileURI is Url::from_file_path(path).to_string(). It fails for relative paths.
func fileURI(path string) (string, bool) {
	if !fsx.IsAbsolute(path) {
		return "", false
	}
	var out []byte
	out = append(out, "file://"...)
	empty := true
	// skip the root component
	for _, component := range fsx.Components(path)[1:] {
		empty = false
		out = append(out, '/')
		out = percentEncode(out, component, inSpecialPathSegmentSet)
	}
	if empty {
		// An URL's path must not be empty.
		out = append(out, '/')
	}
	return string(out), true
}

// directoryURI is Url::from_directory_path(path).to_string(): fileURI with a
// trailing slash, so the whole path counts when the URL is used as a base.
func directoryURI(path string) (string, bool) {
	uri, ok := fileURI(path)
	if !ok {
		return "", false
	}
	if !strings.HasSuffix(uri, "/") {
		uri += "/"
	}
	return uri, true
}

// uriToPath is Url::parse(uri).ok().and_then(|u| u.to_file_path().ok()).
// Like the crate on Unix it does not look at the scheme: only the host
// (absent or "localhost") and the path decide.
func uriToPath(uri string) (string, bool) {
	parsed, ok := parseURL(uri)
	if !ok {
		return "", false
	}
	// path_segments(): the path must start with '/'.
	if !strings.HasPrefix(parsed.path, "/") {
		return "", false
	}
	switch parsed.host.kind {
	case hostNone:
	case hostDomain:
		if parsed.host.domain != "localhost" {
			return "", false
		}
	default:
		return "", false
	}
	var bytes []byte
	for _, segment := range strings.Split(parsed.path[1:], "/") {
		bytes = append(bytes, '/')
		bytes = append(bytes, percentDecode(segment)...)
	}
	// A windows drive letter must end with a slash.
	if n := len(bytes); n > 2 && isASCIIAlpha(rune(bytes[n-2])) && (bytes[n-1] == ':' || bytes[n-1] == '|') {
		bytes = append(bytes, '/')
	}
	return string(bytes), true
}

// Percent-encode sets (parser.rs). Bytes at or above 0x80 are always encoded.
func isC0Control(b byte) bool { return b < 0x20 || b == 0x7f }

func inFragmentSet(b byte) bool {
	return isC0Control(b) || b == ' ' || b == '"' || b == '<' || b == '>' || b == '`'
}

func inPathSet(b byte) bool {
	return inFragmentSet(b) || b == '#' || b == '?' || b == '{' || b == '}'
}

func inUserinfoSet(b byte) bool {
	switch b {
	case '/', ':', ';', '=', '@', '[', '\\', ']', '^', '|':
		return true
	}
	return inPathSet(b)
}

func inPathSegmentSet(b byte) bool { return inPathSet(b) || b == '/' || b == '%' }

func inSpecialPathSegmentSet(b byte) bool { return inPathSegmentSet(b) || b == '\\' }

func inControlsSet(b byte) bool { return isC0Control(b) }

const upperHex = "0123456789ABCDEF"

func percentEncode(dst []byte, s string, in func(byte) bool) []byte {
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b >= 0x80 || in(b) {
			dst = append(dst, '%', upperHex[b>>4], upperHex[b&0xf])
		} else {
			dst = append(dst, b)
		}
	}
	return dst
}

func percentEncodeRunes(dst []byte, runes []rune, in func(byte) bool) []byte {
	return percentEncode(dst, string(runes), in)
}

func hexValue(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	}
	return -1
}

// percentDecode is percent_encoding::percent_decode: a '%' that is not
// followed by two hex digits stays as it is.
func percentDecode(s string) []byte {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if hi, lo := hexValue(s[i+1]), hexValue(s[i+2]); hi >= 0 && lo >= 0 {
				out = append(out, byte(hi<<4|lo))
				i += 2
				continue
			}
		}
		out = append(out, s[i])
	}
	return out
}

type schemeType uint8

const (
	schemeFile schemeType = iota
	schemeSpecialNotFile
	schemeNotSpecial
)

func (s schemeType) isSpecial() bool { return s != schemeNotSpecial }
func (s schemeType) isFile() bool    { return s == schemeFile }

func schemeTypeOf(scheme string) schemeType {
	switch scheme {
	case "http", "https", "ws", "wss", "ftp":
		return schemeSpecialNotFile
	case "file":
		return schemeFile
	}
	return schemeNotSpecial
}

func defaultPort(scheme string) (uint32, bool) {
	switch scheme {
	case "http", "ws":
		return 80, true
	case "https", "wss":
		return 443, true
	case "ftp":
		return 21, true
	}
	return 0, false
}

type hostKind uint8

const (
	hostNone hostKind = iota
	hostDomain
	hostIPv4
	hostIPv6
)

// urlHost is Host<..> together with HostInternal::None for an empty domain.
type urlHost struct {
	kind   hostKind
	domain string
}

// serialization is what `write!(serialization, "{host}")` appends. Only the
// length and the absence of a trailing slash matter to the parser; addresses
// are never compared, so they are written in a fixed shape.
func (h urlHost) serialization() string {
	switch h.kind {
	case hostDomain:
		return h.domain
	case hostIPv4:
		return "0.0.0.0"
	case hostIPv6:
		return "[::]"
	}
	return ""
}

// parsedURL keeps what Url::to_file_path reads: the host and the path (the
// part of the serialization from path_start to the query or fragment).
type parsedURL struct {
	host urlHost
	path string
}

// urlParser mirrors Parser: it owns the serialization being built. The Rust
// Input iterator skips ASCII tab and newline characters everywhere, so they are
// removed once, up front, and the input is a plain slice of characters.
type urlParser struct {
	ser []byte
}

func isASCIIAlpha(c rune) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// parseURL is Url::parse without a base URL.
func parseURL(raw string) (parsedURL, bool) {
	trimmed := strings.TrimFunc(raw, func(c rune) bool { return c <= ' ' })
	input := make([]rune, 0, len(trimmed))
	for _, c := range trimmed {
		if c != '\t' && c != '\n' && c != '\r' {
			input = append(input, c)
		}
	}
	p := &urlParser{}
	remaining, ok := p.parseScheme(input)
	if !ok {
		return parsedURL{}, false // RelativeUrlWithoutBase
	}
	return p.parseWithScheme(remaining)
}

func (p *urlParser) parseScheme(input []rune) ([]rune, bool) {
	if len(input) == 0 || !isASCIIAlpha(input[0]) {
		return nil, false
	}
	for i, c := range input {
		switch {
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.':
			p.ser = append(p.ser, byte(c))
		case c >= 'A' && c <= 'Z':
			p.ser = append(p.ser, byte(c)+('a'-'A'))
		case c == ':':
			return input[i+1:], true
		default:
			p.ser = p.ser[:0]
			return nil, false
		}
	}
	// EOF before ':'
	p.ser = p.ser[:0]
	return nil, false
}

func (p *urlParser) parseWithScheme(input []rune) (parsedURL, bool) {
	schemeEnd := len(p.ser)
	scheme := schemeTypeOf(string(p.ser))
	p.ser = append(p.ser, ':')
	switch scheme {
	case schemeFile:
		p.ser = p.ser[:0]
		return p.parseFile(input)
	case schemeSpecialNotFile:
		// special relative or authority state: any run of slashes is skipped
		rest := input
		for len(rest) > 0 && (rest[0] == '/' || rest[0] == '\\') {
			rest = rest[1:]
		}
		return p.afterDoubleSlash(rest, scheme, schemeEnd)
	}
	return p.parseNonSpecial(input, scheme, schemeEnd)
}

func hasRunePrefix(input []rune, prefix string) ([]rune, bool) {
	rest := input
	for _, c := range prefix {
		if len(rest) == 0 || rest[0] != c {
			return nil, false
		}
		rest = rest[1:]
	}
	return rest, true
}

// parseNonSpecial handles schemes other than file, http, https, ws, wss, ftp.
func (p *urlParser) parseNonSpecial(input []rune, scheme schemeType, schemeEnd int) (parsedURL, bool) {
	// path or authority state
	if rest, ok := hasRunePrefix(input, "//"); ok {
		return p.afterDoubleSlash(rest, scheme, schemeEnd)
	}
	// Anarchist URL (no authority)
	pathStart := len(p.ser)
	if rest, ok := hasRunePrefix(input, "/"); ok {
		p.ser = append(p.ser, '/')
		hasHost := false
		p.parsePath(scheme, &hasHost, pathStart, rest)
	} else {
		p.parseCannotBeABasePath(input)
	}
	return parsedURL{host: urlHost{kind: hostNone}, path: string(p.ser[pathStart:])}, true
}

func splitFirst(input []rune) (rune, []rune) {
	if len(input) == 0 {
		return -1, input
	}
	return input[0], input[1:]
}

func (p *urlParser) parseFile(input []rune) (parsedURL, bool) {
	first, afterFirst := splitFirst(input)
	if first == '/' || first == '\\' {
		// file slash state
		next, afterNext := splitFirst(afterFirst)
		if next == '/' || next == '\\' {
			// file host state
			p.ser = append(p.ser, "file://"...)
			hostStart := len("file://")
			hasHost, host, remaining, ok := p.parseFileHost(afterNext)
			if !ok {
				return parsedURL{}, false
			}
			hostEnd := len(p.ser)
			if hasHost {
				p.parsePathStart(schemeFile, &hasHost, remaining)
			} else {
				pathStart := len(p.ser)
				p.ser = append(p.ser, '/')
				p.parsePath(schemeFile, &hasHost, pathStart, remaining)
			}
			// For file URLs that have a host and whose path starts with the
			// windows drive letter the host is removed.
			if !hasHost {
				p.ser = append(p.ser[:hostStart], p.ser[hostEnd:]...)
				hostEnd = hostStart
				host = urlHost{kind: hostNone}
			}
			return parsedURL{host: host, path: string(p.ser[hostEnd:])}, true
		}
		p.ser = append(p.ser, "file://"...)
		hostEnd := len("file://")
		hasHost := false
		p.parsePath(schemeFile, &hasHost, hostEnd, input)
		return parsedURL{host: urlHost{kind: hostNone}, path: string(p.ser[hostEnd:])}, true
	}
	p.ser = append(p.ser, "file:///"...)
	pathStart := len("file://")
	hasHost := false
	p.parsePath(schemeFile, &hasHost, pathStart, input)
	return parsedURL{host: urlHost{kind: hostNone}, path: string(p.ser[pathStart:])}, true
}

func (p *urlParser) afterDoubleSlash(input []rune, scheme schemeType, schemeEnd int) (parsedURL, bool) {
	p.ser = append(p.ser, '/', '/')
	// authority state
	beforeAuthority := len(p.ser)
	remaining, ok := p.parseUserinfo(input, scheme)
	if !ok {
		return parsedURL{}, false
	}
	hasAuthority := beforeAuthority != len(p.ser)
	// host state
	host, remaining, ok := p.parseHostAndPort(remaining, schemeEnd, scheme)
	if !ok {
		return parsedURL{}, false
	}
	if host.kind == hostNone && hasAuthority {
		return parsedURL{}, false // EmptyHost
	}
	// path state
	pathStart := len(p.ser)
	hasHost := true
	p.parsePathStart(scheme, &hasHost, remaining)
	return parsedURL{host: host, path: string(p.ser[pathStart:])}, true
}

// parseUserinfo returns the input after the userinfo.
func (p *urlParser) parseUserinfo(input []rune, scheme schemeType) ([]rune, bool) {
	lastAt := -1 // number of characters before the last '@'
	var afterLastAt []rune
	count := 0
	for i := 0; i < len(input); i++ {
		c := input[i]
		if c == '@' {
			lastAt = count
			afterLastAt = input[i+1:]
		} else if c == '/' || c == '?' || c == '#' || (c == '\\' && scheme.isSpecial()) {
			break
		}
		count++
	}
	switch lastAt {
	case -1:
		return input, true
	case 0:
		// If @ flag is set and buffer is the empty string, validation error,
		// return failure.
		if c, _ := splitFirst(afterLastAt); c == '/' || c == '?' || c == '#' || (scheme.isSpecial() && c == '\\') {
			return nil, false
		}
		return afterLastAt, true
	}
	usernameSeen := false
	hasPassword := false
	hasUsername := false
	rest := input
	for n := lastAt; n > 0; n-- {
		c := rest[0]
		rest = rest[1:]
		if c == ':' && !usernameSeen {
			// Start parsing password
			usernameSeen = true
			// We don't add a colon if the password is empty
			if n-1 > 0 {
				p.ser = append(p.ser, ':')
				hasPassword = true
			}
		} else {
			if !hasPassword {
				hasUsername = true
			}
			p.ser = percentEncode(p.ser, string(c), inUserinfoSet)
		}
	}
	if hasUsername || hasPassword {
		p.ser = append(p.ser, '@')
	}
	return afterLastAt, true
}

func (p *urlParser) parseHostAndPort(input []rune, schemeEnd int, scheme schemeType) (urlHost, []rune, bool) {
	host, remaining, ok := parseHost(input, scheme)
	if !ok {
		return urlHost{}, nil, false
	}
	p.ser = append(p.ser, host.serialization()...)
	if host.kind == hostDomain && host.domain == "" {
		// Port with an empty host
		if len(remaining) > 0 && remaining[0] == ':' {
			return urlHost{}, nil, false
		}
		if scheme.isSpecial() {
			return urlHost{}, nil, false
		}
	}
	if rest, ok := hasRunePrefix(remaining, ":"); ok {
		port, rest, ok := parsePort(rest)
		if !ok {
			return urlHost{}, nil, false
		}
		if def, has := defaultPort(string(p.ser[:schemeEnd])); has && port == int64(def) {
			port = -1
		}
		if port >= 0 {
			p.ser = append(p.ser, ':')
			p.ser = strconv.AppendInt(p.ser, port, 10)
		}
		remaining = rest
	}
	if host.kind == hostDomain && host.domain == "" {
		host = urlHost{kind: hostNone} // HostInternal::None
	}
	return host, remaining, true
}

// parsePort returns the port (-1 when absent) for the UrlParser context.
func parsePort(input []rune) (int64, []rune, bool) {
	var port int64
	hasAnyDigit := false
	for len(input) > 0 {
		c := input[0]
		if c >= '0' && c <= '9' {
			port = port*10 + int64(c-'0')
			if port > 0xffff {
				return 0, nil, false
			}
			hasAnyDigit = true
		} else if c != '/' && c != '\\' && c != '?' && c != '#' {
			return 0, nil, false
		} else {
			break
		}
		input = input[1:]
	}
	if !hasAnyDigit {
		return -1, input, true
	}
	return port, input, true
}

// parseHost is Parser::parse_host for schemes other than file.
func parseHost(input []rune, scheme schemeType) (urlHost, []rune, bool) {
	insideBrackets := false
	n := 0
scan:
	for _, c := range input {
		switch {
		case c == ':' && !insideBrackets:
			break scan
		case c == '\\' && scheme.isSpecial():
			break scan
		case c == '/' || c == '?' || c == '#':
			break scan
		case c == '[':
			insideBrackets = true
		case c == ']':
			insideBrackets = false
		}
		n++
	}
	hostStr, remaining := string(input[:n]), input[n:]
	if scheme == schemeSpecialNotFile && hostStr == "" {
		return urlHost{}, nil, false // EmptyHost
	}
	if !scheme.isSpecial() {
		host, ok := parseOpaqueHost(hostStr)
		return host, remaining, ok
	}
	host, ok := parseSpecialHost(hostStr)
	return host, remaining, ok
}

func isDriveLetterString(s string) bool {
	return len(s) == 2 && isASCIIAlpha(rune(s[0])) && (s[1] == ':' || s[1] == '|')
}

// fileHost is Parser::file_host: the host text of a file URL (a Windows drive
// letter is not a host and is left in the input).
func fileHost(input []rune) (string, []rune) {
	n := 0
	for n < len(input) && input[n] != '/' && input[n] != '\\' && input[n] != '?' && input[n] != '#' {
		n++
	}
	hostStr := string(input[:n])
	if isDriveLetterString(hostStr) {
		return "", input
	}
	return hostStr, input[n:]
}

func (p *urlParser) parseFileHost(input []rune) (hasHost bool, host urlHost, remaining []rune, ok bool) {
	hostStr, remaining := fileHost(input)
	if hostStr == "" {
		return false, urlHost{kind: hostNone}, remaining, true
	}
	h, ok := parseSpecialHost(hostStr)
	if !ok {
		return false, urlHost{}, nil, false
	}
	if h.kind == hostDomain && h.domain == "localhost" {
		return false, urlHost{kind: hostNone}, remaining, true
	}
	p.ser = append(p.ser, h.serialization()...)
	return true, h, remaining, true
}

func (p *urlParser) parsePathStart(scheme schemeType, hasHost *bool, input []rune) []rune {
	pathStart := len(p.ser)
	maybe, remaining := splitFirst(input)
	// If url is special, then:
	if scheme.isSpecial() {
		// A special URL always has a non-empty path.
		if len(p.ser) == 0 || p.ser[len(p.ser)-1] != '/' {
			p.ser = append(p.ser, '/')
			// We have already made sure the forward slash is present.
			if maybe == '/' || maybe == '\\' {
				return p.parsePath(scheme, hasHost, pathStart, remaining)
			}
		}
		return p.parsePath(scheme, hasHost, pathStart, input)
	} else if maybe == '?' || maybe == '#' {
		// The query and path states are handled by the caller.
		return input
	}
	if maybe != -1 && maybe != '/' {
		p.ser = append(p.ser, '/')
	}
	return p.parsePath(scheme, hasHost, pathStart, input)
}

func isNormalizedWindowsDriveLetter(segment string) bool {
	return isDriveLetterString(segment) && segment[1] == ':'
}

func startsWithWindowsDriveLetter(s string) bool {
	return len(s) >= 2 && isASCIIAlpha(rune(s[0])) && (s[1] == ':' || s[1] == '|') &&
		(len(s) == 2 || s[2] == '/' || s[2] == '\\' || s[2] == '?' || s[2] == '#')
}

// pathStartsWithWindowsDriveLetter: whether s starts with a root slash and a
// windows drive letter, e.g. "/c:" or "/a:/".
func pathStartsWithWindowsDriveLetter(s string) bool {
	if len(s) == 0 {
		return false
	}
	return (s[0] == '/' || s[0] == '\\' || s[0] == '?' || s[0] == '#') && startsWithWindowsDriveLetter(s[1:])
}

func (p *urlParser) endsWithSlash() bool { return len(p.ser) > 0 && p.ser[len(p.ser)-1] == '/' }

func (p *urlParser) parsePath(scheme schemeType, hasHost *bool, pathStart int, input []rune) []rune {
	for {
		segmentStart := len(p.ser)
		endsWithSlash := false
		var pending []rune
		flush := func() {
			p.ser = percentEncodeRunes(p.ser, pending, inPathSet)
			pending = pending[:0]
		}
	segment:
		for {
			if len(input) == 0 {
				flush()
				break
			}
			c := input[0]
			switch {
			case c == '/':
				flush()
				input = input[1:]
				p.ser = append(p.ser, '/')
				endsWithSlash = true
				break segment
			case c == '\\' && scheme.isSpecial():
				flush()
				input = input[1:]
				p.ser = append(p.ser, '/')
				endsWithSlash = true
				break segment
			case c == '?' || c == '#':
				flush()
				break segment
			default:
				input = input[1:]
				if scheme.isFile() && len(p.ser) > pathStart &&
					isNormalizedWindowsDriveLetter(string(p.ser[pathStart+1:])) {
					flush()
					p.ser = append(p.ser, '/')
					segmentStart++
				}
				pending = append(pending, c)
			}
		}
		end := len(p.ser)
		if endsWithSlash {
			end--
		}
		switch segment := string(p.ser[segmentStart:end]); segment {
		// If buffer is a double-dot path segment, shorten url's path,
		case "..", "%2e%2e", "%2e%2E", "%2E%2e", "%2E%2E", "%2e.", "%2E.", ".%2e", ".%2E":
			p.ser = p.ser[:segmentStart]
			if p.endsWithSlash() && lastSlashCanBeRemoved(p.ser, pathStart) {
				p.ser = p.ser[:len(p.ser)-1]
			}
			p.shortenPath(scheme, pathStart)
			// and then if neither c is U+002F (/), nor url is special and c is
			// U+005C (\), append the empty string to url's path.
			if endsWithSlash && !p.endsWithSlash() {
				p.ser = append(p.ser, '/')
			}
		// Otherwise, if buffer is a single-dot path segment and if neither c is
		// U+002F (/), nor url is special and c is U+005C (\), append the empty
		// string to url's path.
		case ".", "%2e", "%2E":
			p.ser = p.ser[:segmentStart]
			if !p.endsWithSlash() {
				p.ser = append(p.ser, '/')
			}
		default:
			// If url's scheme is "file", url's path is empty, and buffer is a
			// Windows drive letter, then
			if scheme.isFile() && segmentStart == pathStart+1 && isDriveLetterString(segment) {
				// Replace the second code point in buffer with U+003A (:).
				letter := segment[0]
				p.ser = append(p.ser[:segmentStart], letter, ':')
				if endsWithSlash {
					p.ser = append(p.ser, '/')
				}
				// If url's host is neither the empty string nor null, set it
				// to the empty string.
				*hasHost = false
			}
		}
		if !endsWithSlash {
			break
		}
	}
	if scheme.isFile() {
		// while url's path's size is greater than 1 and url's path[0] is the
		// empty string, remove the first item from url's path.
		path := string(p.ser[pathStart:])
		p.ser = append(p.ser[:pathStart], '/')
		p.ser = append(p.ser, strings.TrimLeft(path, "/")...)
	}
	return input
}

func lastSlashCanBeRemoved(ser []byte, pathStart int) bool {
	before := ser[:len(ser)-1]
	start := strings.LastIndexByte(string(before), '/')
	if start < 0 {
		return false
	}
	// Do not remove the root slash, or a windows drive letter slash.
	return start >= pathStart && !pathStartsWithWindowsDriveLetter(string(ser[start:]))
}

// shortenPath is https://url.spec.whatwg.org/#shorten-a-urls-path
func (p *urlParser) shortenPath(scheme schemeType, pathStart int) {
	if len(p.ser) == pathStart {
		return
	}
	if scheme.isFile() && isNormalizedWindowsDriveLetter(string(p.ser[pathStart:])) {
		return
	}
	p.popPath(scheme, pathStart)
}

func (p *urlParser) popPath(scheme schemeType, pathStart int) {
	if len(p.ser) > pathStart {
		slash := strings.LastIndexByte(string(p.ser[pathStart:]), '/')
		segmentStart := pathStart + slash + 1
		// Don't pop a Windows drive letter
		if !(scheme.isFile() && isNormalizedWindowsDriveLetter(string(p.ser[segmentStart:]))) {
			p.ser = p.ser[:segmentStart]
		}
	}
}

func (p *urlParser) parseCannotBeABasePath(input []rune) {
	for _, c := range input {
		if c == '?' || c == '#' {
			return
		}
		p.ser = percentEncode(p.ser, string(c), inControlsSet)
	}
}

// Hosts (host.rs).

// parseOpaqueHost is Host::parse_opaque for non-special schemes.
func parseOpaqueHost(input string) (urlHost, bool) {
	if strings.HasPrefix(input, "[") {
		if !strings.HasSuffix(input, "]") {
			return urlHost{}, false
		}
		if !validIPv6(input[1 : len(input)-1]) {
			return urlHost{}, false
		}
		return urlHost{kind: hostIPv6}, true
	}
	if strings.ContainsAny(input, "\x00\t\n\r #/:<>?@[\\]^|") {
		return urlHost{}, false
	}
	return urlHost{kind: hostDomain, domain: string(percentEncode(nil, input, inControlsSet))}, true
}

// parseSpecialHost is Host::parse for special schemes.
func parseSpecialHost(input string) (urlHost, bool) {
	if strings.HasPrefix(input, "[") {
		if !strings.HasSuffix(input, "]") {
			return urlHost{}, false
		}
		if !validIPv6(input[1 : len(input)-1]) {
			return urlHost{}, false
		}
		return urlHost{kind: hostIPv6}, true
	}
	domain, ok := domainToASCII(percentDecode(input))
	if !ok || domain == "" {
		return urlHost{}, false
	}
	if endsInANumber(domain) {
		if !validIPv4(domain) {
			return urlHost{}, false
		}
		return urlHost{kind: hostIPv4}, true
	}
	return urlHost{kind: hostDomain, domain: domain}, true
}

func allASCIIDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// endsInANumber is https://url.spec.whatwg.org/#ends-in-a-number-checker
func endsInANumber(input string) bool {
	parts := strings.Split(input, ".")
	last := parts[len(parts)-1]
	if last == "" {
		if len(parts) < 2 {
			return false
		}
		last = parts[len(parts)-2]
	}
	if last != "" && allASCIIDigits(last) {
		return true
	}
	_, _, ok := parseIPv4Number(last)
	return ok
}

// parseIPv4Number is https://url.spec.whatwg.org/#ipv4-number-parser. The
// overflow result is Rust's Ok(None).
func parseIPv4Number(input string) (n uint32, overflow bool, ok bool) {
	if input == "" {
		return 0, false, false
	}
	radix := 10
	if strings.HasPrefix(input, "0x") || strings.HasPrefix(input, "0X") {
		input = input[2:]
		radix = 16
	} else if len(input) >= 2 && input[0] == '0' {
		input = input[1:]
		radix = 8
	}
	if input == "" {
		return 0, false, true
	}
	for i := 0; i < len(input); i++ {
		d := hexValue(input[i])
		if d < 0 || d >= radix {
			return 0, false, false
		}
	}
	v, err := strconv.ParseUint(input, radix, 32)
	if err != nil {
		return 0, true, true
	}
	return uint32(v), false, true
}

// validIPv4 is whether parse_ipv4addr succeeds.
func validIPv4(input string) bool {
	parts := strings.Split(input, ".")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 4 {
		return false
	}
	numbers := make([]uint32, 0, 4)
	for _, part := range parts {
		n, overflow, ok := parseIPv4Number(part)
		if !ok || overflow {
			return false
		}
		numbers = append(numbers, n)
	}
	ipv4 := numbers[len(numbers)-1]
	numbers = numbers[:len(numbers)-1]
	// Equivalent to: ipv4 >= 256 ** (4 - numbers.len())
	if uint64(ipv4) > uint64(0xffffffff)>>(8*uint(len(numbers))) {
		return false
	}
	for _, n := range numbers {
		if n > 255 {
			return false
		}
	}
	return true
}

// validIPv6 is whether parse_ipv6addr succeeds (https://url.spec.whatwg.org/#concept-ipv6-parser).
func validIPv6(s string) bool {
	input := []byte(s)
	n := len(input)
	isIPv4 := false
	piecePointer := 0
	compress := -1
	i := 0
	if n < 2 {
		return false
	}
	if input[0] == ':' {
		if input[1] != ':' {
			return false
		}
		i = 2
		piecePointer = 1
		compress = 1
	}
	for i < n {
		if piecePointer == 8 {
			return false
		}
		if input[i] == ':' {
			if compress >= 0 {
				return false
			}
			i++
			piecePointer++
			compress = piecePointer
			continue
		}
		start := i
		end := min(n, start+4)
		for i < end && hexValue(input[i]) >= 0 {
			i++
		}
		if i < n {
			switch input[i] {
			case '.':
				if i == start {
					return false
				}
				i = start
				if piecePointer > 6 {
					return false
				}
				isIPv4 = true
			case ':':
				i++
				if i == n {
					return false
				}
			default:
				return false
			}
		}
		if isIPv4 {
			break
		}
		piecePointer++
	}
	if isIPv4 {
		if piecePointer > 6 {
			return false
		}
		numbersSeen := 0
		for i < n {
			if numbersSeen > 0 {
				if numbersSeen < 4 && i < n && input[i] == '.' {
					i++
				} else {
					return false
				}
			}
			piece := -1
			for i < n {
				if input[i] < '0' || input[i] > '9' {
					break
				}
				digit := int(input[i] - '0')
				switch {
				case piece < 0:
					piece = digit
				case piece == 0:
					return false // No leading zero
				default:
					piece = piece*10 + digit
					if piece > 255 {
						return false
					}
				}
				i++
			}
			if piece < 0 {
				return false
			}
			numbersSeen++
			if numbersSeen == 2 || numbersSeen == 4 {
				piecePointer++
			}
		}
		if numbersSeen != 4 {
			return false
		}
	}
	if i < n {
		return false
	}
	if compress < 0 && piecePointer != 8 {
		return false
	}
	return true
}

// Domains. The Rust crate runs UTS #46 (idna 1.1.0, non-transitional, no
// hyphen or length checks, ASCII deny list for URLs) with the full Unicode
// mapping, normalization, bidi and joiner tables, which Go does not have.
// Plain ASCII domains are handled exactly. For characters above U+007F two
// tables measured from the crate itself stand in (idna_data.go): which
// characters are valid in a label, and which map to part of "localhost", a
// digit, a hyphen or a dot. What is not modelled: normalization, the bidi
// rules for labels that mix directions, the joiner rules for U+200C and
// U+200D beyond rejecting them, and the exact punycode form (see
// the tests for the measured agreement).

// asciiDenied is AsciiDenyList::URL: glyphless ASCII and the forbidden domain
// code points.
func asciiDenied(c rune) bool {
	return c <= ' ' || c == 0x7f || strings.ContainsRune("%#/:<>?@[\\]^|", c)
}

// domainToASCII is idna::domain_to_ascii_from_cow(domain, AsciiDenyList::URL).
func domainToASCII(domain []byte) (string, bool) {
	if !utf8.Valid(domain) {
		return "", false
	}
	var mapped []rune
	for _, c := range string(domain) {
		replacement, ok := mapIDNA(c)
		if !ok {
			return "", false
		}
		for _, r := range replacement {
			if r < 0x80 && asciiDenied(r) {
				return "", false
			}
			mapped = append(mapped, r)
		}
	}
	labels := strings.Split(string(mapped), ".")
	for i, label := range labels {
		out, ok := checkLabel(label)
		if !ok {
			return "", false
		}
		labels[i] = out
	}
	return strings.Join(labels, "."), true
}

// mapIDNA is the UTS #46 mapping step for one character: exact for ASCII,
// table driven above it (see above). Valid characters the tables do not map
// are kept, lower-cased.
func mapIDNA(c rune) ([]rune, bool) {
	if c < 0x80 {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		return []rune{c}, true
	}
	if i := sort.Search(len(idnaMapped), func(i int) bool { return idnaMapped[i].r >= c }); i < len(idnaMapped) && idnaMapped[i].r == c {
		return []rune(idnaMapped[i].s), true
	}
	if !inRanges(idnaValid, c) {
		return nil, false
	}
	return []rune{unicode.ToLower(c)}, true
}

func inRanges(table [][2]rune, c rune) bool {
	i := sort.Search(len(table), func(i int) bool { return table[i][1] >= c })
	return i < len(table) && table[i][0] <= c
}

// checkLabel validates one mapped label. Labels that start with "xn--" must
// be valid punycode whose decoding is itself in mapped form.
func checkLabel(label string) (string, bool) {
	if strings.HasPrefix(label, "xn--") {
		rest := label[4:]
		if rest == "" || strings.HasSuffix(rest, "-") {
			return "", false
		}
		for i := 0; i < len(rest); i++ {
			if rest[i] >= 0x80 {
				return "", false
			}
		}
		decoded, ok := punycodeDecode(rest)
		if !ok {
			return "", false
		}
		for i, c := range decoded {
			mapped, ok := mapIDNA(c)
			if !ok || len(mapped) != 1 || mapped[0] != c || c == 0x200c || c == 0x200d {
				return "", false
			}
			if i == 0 && unicode.Is(unicode.M, c) {
				return "", false
			}
		}
		return label, true
	}
	ascii := true
	for i := 0; i < len(label); i++ {
		if label[i] >= 0x80 {
			ascii = false
		}
	}
	if ascii {
		return label, true
	}
	rtl, latin := false, false
	for i, c := range label {
		if i == 0 && unicode.Is(unicode.M, c) {
			return "", false
		}
		if c == 0x200c || c == 0x200d {
			return "", false // ContextJ needs the joining tables
		}
		if c >= 'a' && c <= 'z' {
			latin = true
		} else if inRanges(idnaRTL, c) {
			rtl = true
		}
	}
	if rtl && latin {
		return "", false // the bidi rule: a label is left-to-right or right-to-left
	}
	// Stand-in for the punycode form: it is never compared to anything but
	// "localhost" and tested for ending in a number.
	return "xn--" + label, true
}

// punycodeDecode is RFC 3492 decoding of the part after "xn--".
func punycodeDecode(input string) ([]rune, bool) {
	const (
		base        = 36
		tMin        = 1
		tMax        = 26
		skew        = 38
		damp        = 700
		initialBias = 72
		initialN    = 128
	)
	var output []rune
	rest := input
	if i := strings.LastIndexByte(input, '-'); i >= 0 {
		for _, c := range input[:i] {
			output = append(output, c)
		}
		rest = input[i+1:]
	}
	n := rune(initialN)
	bias := initialBias
	idx := 0
	pos := 0
	for pos < len(rest) {
		oldIdx := idx
		w := 1
		for k := base; ; k += base {
			if pos >= len(rest) {
				return nil, false
			}
			c := rest[pos]
			pos++
			var digit int
			switch {
			case c >= 'a' && c <= 'z':
				digit = int(c - 'a')
			case c >= 'A' && c <= 'Z':
				digit = int(c - 'A')
			case c >= '0' && c <= '9':
				digit = int(c-'0') + 26
			default:
				return nil, false
			}
			if digit > (1<<31-1-idx)/w {
				return nil, false
			}
			idx += digit * w
			t := k - bias
			if t < tMin {
				t = tMin
			} else if t > tMax {
				t = tMax
			}
			if digit < t {
				break
			}
			if w > (1<<31-1)/(base-t) {
				return nil, false
			}
			w *= base - t
		}
		count := len(output) + 1
		bias = adaptBias(idx-oldIdx, count, oldIdx == 0, base, tMin, tMax, skew, damp)
		if idx/count > int(unicode.MaxRune)-int(n) {
			return nil, false
		}
		n += rune(idx / count)
		idx %= count
		if n < 0x80 || n > unicode.MaxRune || (n >= 0xd800 && n <= 0xdfff) {
			return nil, false
		}
		output = append(output, 0)
		copy(output[idx+1:], output[idx:])
		output[idx] = n
		idx++
	}
	return output, true
}

func adaptBias(delta, count int, first bool, base, tMin, tMax, skew, damp int) int {
	if first {
		delta /= damp
	} else {
		delta /= 2
	}
	delta += delta / count
	k := 0
	for delta > ((base-tMin)*tMax)/2 {
		delta /= base - tMin
		k += base
	}
	return k + (base-tMin+1)*delta/(delta+skew)
}
