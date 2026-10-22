package lsp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/fclaude/coigue/internal/ucd"
)

const (
	maxMessage = 16 * 1024 * 1024
	maxHeader  = 8192
)

// readLine is `reader.take(8193).read_line(&mut header)`: bytes up to and
// including a newline, at most limit of them, which must be valid UTF-8.
func readLine(reader *bufio.Reader, limit int) (string, error) {
	line := make([]byte, 0, 64)
	for len(line) < limit {
		b, err := reader.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", err
		}
		line = append(line, b)
		if b == '\n' {
			break
		}
	}
	if !utf8.Valid(line) {
		return "", errors.New("stream did not contain valid UTF-8")
	}
	return string(line), nil
}

// parseUsize is str::parse::<usize>: an optional '+' and decimal digits.
func parseUsize(s string) (uint64, error) {
	if s == "" {
		return 0, errors.New("cannot parse integer from empty string")
	}
	digits := s
	if digits[0] == '+' {
		digits = digits[1:]
		if digits == "" {
			return 0, errors.New("invalid digit found in string")
		}
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, errors.New("invalid digit found in string")
		}
	}
	n, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return 0, errors.New("number too large to fit in target type")
	}
	return n, nil
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// readMessage reads one framed message. Framing is bounded: Content-Length
// counts UTF-8 bytes, never scalar values.
func readMessage(reader *bufio.Reader) (any, error) {
	length := -1
	headerBytes := 0
	for {
		header, err := readLine(reader, maxHeader+1)
		if err != nil {
			return nil, err
		}
		read := len(header)
		headerBytes += read
		if read == 0 {
			return nil, errors.New("server closed stdout")
		}
		if headerBytes > maxHeader {
			return nil, errors.New("LSP header too large")
		}
		if header == "\r\n" || header == "\n" {
			break
		}
		name, value, ok := strings.Cut(header, ":")
		if !ok {
			return nil, errors.New("invalid LSP header")
		}
		if equalFoldASCII(name, "Content-Length") {
			if length >= 0 {
				return nil, errors.New("duplicate Content-Length")
			}
			n, err := parseUsize(strings.TrimFunc(value, ucd.IsWhitespace))
			if err != nil {
				return nil, err
			}
			if n > maxMessage {
				// Rust keeps parsing headers and rejects the length afterwards;
				// every later outcome is an error as well.
				length = maxMessage + 1
			} else {
				length = int(n)
			}
		}
	}
	if length < 0 || length > maxMessage {
		return nil, errors.New("invalid LSP Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, errors.New("failed to fill whole buffer")
	}
	return unmarshal(body)
}

// writeMessage frames and writes one message.
func writeMessage(writer io.Writer, value any) error {
	body := marshal(value)
	frame := append([]byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))), body...)
	_, err := writer.Write(frame)
	return err
}
