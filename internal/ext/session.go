package ext

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"sync/atomic"
	"unicode/utf8"

	"github.com/fclaude/coigue/internal/buffer"
)

type kind uint8

const (
	kindNone kind = iota
	kindInit
	kindCommand
)

type sessionKey struct{}

// Selection is the Visual selection a command was started from.
type Selection struct {
	// Anchor is the row and column where the selection started; its other
	// end is the cursor of the session's buffer.
	Anchor   [2]int
	Linewise bool
}

// Session is what one command invocation sees and changes: a private copy of
// the buffer, the selection, and the status message it wants. The live editor
// state is never reachable from it. The editor builds it before Run and, if
// Run succeeds, applies Buffer in one edit transaction.
type Session struct {
	// Buffer is the private copy. Operations read and change it; the editor
	// reads it back after a successful run.
	Buffer *buffer.Buffer

	// Path is the file the buffer belongs to, or "" when it has none.
	Path string
	// Mode is the mode the invocation started from: "normal", "visual" or
	// "visual-line".
	Mode string

	selection *Selection
	message   string
	// edited is set by the first change to the text; it ends the selection.
	edited bool
	// moved is set when only the cursor changed, which the editor applies too.
	moved    bool
	inserted int
	kind     kind
	live     atomic.Bool
	// handlers and timers are what the command registered; the editor adopts
	// them only if the command succeeded, as it does the text.
	handlers []*handlerReg
	timers   []*timerReq
	windows  []*WindowReq
	// Marks made in this command and the text they were made on.
	newMarks []*markObj
	snap     *textSnap
	snapRev  uint64
	mappers  map[*textSnap]*cachedMapper
	// label names the command in a handler's failure message.
	label string
	// owner is the goroutine that started the invocation, the only one whose
	// host operations are honoured.
	owner uint64
}

// goroutineID is the number in the first line of this goroutine's stack,
// "goroutine 18 [running]:". Go offers no other way to ask which goroutine is
// running, and the format has been stable since Go 1.
func goroutineID() uint64 {
	var buf [64]byte
	line := bytes.TrimPrefix(buf[:runtime.Stack(buf[:], false)], []byte("goroutine "))
	if end := bytes.IndexByte(line, ' '); end > 0 {
		if id, err := strconv.ParseUint(string(line[:end]), 10, 64); err == nil {
			return id
		}
	}
	return 0
}

// NewSession makes a session over scratch, which the caller gives up, with
// the selection the command was started from, or nil.
func NewSession(scratch *buffer.Buffer, selection *Selection) *Session {
	s := &Session{Buffer: scratch, selection: selection, Mode: "normal"}
	if selection != nil {
		s.Mode = "visual"
		if selection.Linewise {
			s.Mode = "visual-line"
		}
	}
	return s
}

func initSession() *Session { return &Session{kind: kindInit} }

// Message is the status text the command asked for, or "".
func (s *Session) Message() string { return s.message }

// Edited reports whether the command changed the text or the cursor, so the
// editor has something to apply.
func (s *Session) Edited() bool { return s.edited || s.moved }

var (
	errNoSelection   = errors.New("no selection; select text with v or V, then press !")
	errSelectionGone = errors.New("the selection is gone after an earlier edit in this command")
	errNotInCommand  = errors.New("not inside an extension command")
	errInTask        = fmt.Errorf("%w: a task cannot read or edit the buffer, so do that in the command or in an on-channel handler and give the task the data it needs", errNotInCommand)
	errNotInInit     = errors.New("only allowed while init.scm loads")
	errTextNotUTF8   = errors.New("text is not valid UTF-8")
)

// selectionSpan is the character range of the selection as it is now.
func (s *Session) selectionSpan() (start, end int, err error) {
	if s.selection == nil {
		return 0, 0, errNoSelection
	}
	if s.edited {
		return 0, 0, errSelectionGone
	}
	b := s.Buffer
	if s.selection.Linewise {
		start, end = b.LineSpan(s.selection.Anchor[0], b.Row)
	} else {
		start, end = b.SelectionSpan(s.selection.Anchor)
	}
	return start, end, nil
}

// checkText validates text for insertion and counts it against the limits.
func (s *Session) checkText(text string) ([]rune, error) {
	if !utf8.ValidString(text) {
		return nil, errTextNotUTF8
	}
	runes := []rune(text)
	if len(runes) > MaxRegion {
		return nil, fmt.Errorf("text is %d characters; the limit is %d", len(runes), MaxRegion)
	}
	if s.inserted+len(runes) > MaxInserted {
		return nil, fmt.Errorf("this command has inserted more than %d characters", MaxInserted)
	}
	return runes, nil
}
