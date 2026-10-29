package ext

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/fclaude/coigue/internal/buffer"
)

const markProgram = `
(define saved #f)
(define saved2 #f)
(define-command "mark" (lambda (line col) (set! saved (mark-at (string->number line) (string->number col)))))
(define-command "put" (lambda (text) (insert-at-mark saved text)))
(define-command "where" (lambda () (message (string-append (number->string (mark-line saved)) ":" (number->string (mark-column saved))))))
(define-command "disturbed" (lambda () (message (if (mark-disturbed? saved) "yes" "no"))))
(define-command "goto" (lambda () (goto-mark saved)))
(define-command "two" (lambda ()
  (set! saved (mark-at 3 1))
  (set! saved2 (mark-at 5 1))
  (insert-at-mark saved "A\n")
  (insert-at-mark saved2 "B\n")))
(define-command "orphan" (lambda () (set! saved (mark-at 1 1)) (error "no")))
(define-command "many" (lambda ()
  (let loop ((i 0)) (if (< i 300) (begin (mark-at 1 1) (loop (+ i 1)))))))
`

// onText runs a command on a session over text. If the command succeeds its
// marks are adopted and the live text is taken to be what the command left.
func onText(t *testing.T, r *Runtime, text, name string, args ...string) (*Session, error) {
	t.Helper()
	b := buffer.New(text)
	s := NewSession(b, nil)
	err := r.Run(context.Background(), name, args, s)
	if err == nil {
		r.Adopt(s)
	}
	return s, err
}

var rev uint64

// live tells the runtime what the live text is now.
func live(r *Runtime, text string) {
	rev++
	r.SyncMarks(buffer.New(text).Lines(), rev, 1)
}

func TestAMarkFollowsTheTextWhenLinesAreAddedAbove(t *testing.T) {
	r := loaded(t, Options{}, markProgram)
	text := "one\ntwo\nthree"
	live(r, text)
	if _, err := onText(t, r, text, "mark", "3", "2"); err != nil {
		t.Fatal(err)
	}
	live(r, text)
	// The person types a line above.
	moved := "new\none\ntwo\nthree"
	live(r, moved)
	s, err := onText(t, r, moved, "where")
	if err != nil || s.Message() != "4:2" {
		t.Fatalf("where: %q %v", s.Message(), err)
	}
	s, err = onText(t, r, moved, "put", "X")
	if err != nil || s.Buffer.Text() != "new\none\ntwo\ntXhree" {
		t.Fatalf("put: %q %v", s.Buffer.Text(), err)
	}
	s, _ = onText(t, r, moved, "disturbed")
	if s.Message() != "no" {
		t.Fatalf("a mark that only moved with its text is not disturbed: %q", s.Message())
	}
}

func TestAMarkSurvivesTypingOnItsOwnLineAndUndo(t *testing.T) {
	r := loaded(t, Options{}, markProgram)
	text := "hello world"
	live(r, text)
	onText(t, r, text, "mark", "1", "7")
	live(r, text)
	live(r, "hello big world") // typed exactly at the mark: the text goes after it
	if s, _ := onText(t, r, "hello big world", "where"); s.Message() != "1:7" {
		t.Fatalf("after typing at it: %q", s.Message())
	}
	live(r, text) // undo
	if s, _ := onText(t, r, text, "where"); s.Message() != "1:7" {
		t.Fatalf("after undo: %q", s.Message())
	}
	live(r, "oh hello world") // typed before it
	if s, _ := onText(t, r, "oh hello world", "where"); s.Message() != "1:10" {
		t.Fatalf("after typing before it: %q", s.Message())
	}
	live(r, "oh hello world and more") // typed after it
	if s, _ := onText(t, r, "oh hello world and more", "where"); s.Message() != "1:10" {
		t.Fatalf("after typing after it: %q", s.Message())
	}
}

func TestAMarkInReplacedTextIsDisturbedAndFallsToTheStart(t *testing.T) {
	r := loaded(t, Options{}, markProgram)
	text := "keep\nreplace me\nkeep too"
	live(r, text)
	onText(t, r, text, "mark", "2", "4")
	live(r, text)
	changed := "keep\ncompletely different\nkeep too"
	live(r, changed)
	s, _ := onText(t, r, changed, "where")
	if s.Message() != "2:1" {
		t.Fatalf("where: %q", s.Message())
	}
	if s, _ = onText(t, r, changed, "disturbed"); s.Message() != "yes" {
		t.Fatalf("disturbed: %q", s.Message())
	}
}

func TestTwoMarksStayRightWhileTheCommandEdits(t *testing.T) {
	r := loaded(t, Options{}, markProgram)
	s, err := onText(t, r, "1\n2\n3\n4\n5\n6", "two")
	if err != nil {
		t.Fatal(err)
	}
	// Both inserts were made on the text as it was when the marks were made.
	if got := s.Buffer.Text(); got != "1\n2\nA\n3\n4\nB\n5\n6" {
		t.Fatalf("text %q", got)
	}
}

func TestAMarkMadeByACommandThatFailedIsNotUsable(t *testing.T) {
	r := loaded(t, Options{}, markProgram)
	live(r, "a\nb")
	if _, err := onText(t, r, "a\nb", "orphan"); err == nil {
		t.Fatal("want an error")
	}
	_, err := onText(t, r, "a\nb", "where")
	if err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Fatalf("got %v", err)
	}
	if r.HasMarks() {
		t.Fatal("a failed command left a mark behind")
	}
}

func TestMarksAreDeadWhenAnotherFileIsOpen(t *testing.T) {
	r := loaded(t, Options{}, markProgram)
	live(r, "a\nb")
	onText(t, r, "a\nb", "mark", "2", "1")
	live(r, "a\nb")
	r.SyncMarks(buffer.New("other file").Lines(), 1, 2) // a new generation
	_, err := onText(t, r, "other file", "where")
	if err == nil || !strings.Contains(err.Error(), "no longer open") {
		t.Fatalf("got %v", err)
	}
}

func TestGotoMarkMovesTheCursor(t *testing.T) {
	r := loaded(t, Options{}, markProgram)
	text := "aa\nbb\ncc"
	live(r, text)
	onText(t, r, text, "mark", "3", "2")
	live(r, text)
	moved := "new\naa\nbb\ncc"
	live(r, moved)
	s, err := onText(t, r, moved, "goto")
	if err != nil || s.Buffer.Row != 3 || s.Buffer.Col != 1 || !s.Edited() {
		t.Fatalf("cursor (%d,%d) %v", s.Buffer.Row, s.Buffer.Col, err)
	}
}

func TestAtMostTwoHundredFiftySixMarksAreAliveButFreedOnesAreReused(t *testing.T) {
	r := loaded(t, Options{}, markProgram)
	live(r, "a")
	// Nothing keeps these marks, so the collector may free them; 300 are made in
	// one command, which has to fail at the cap because they all belong to it.
	err := r.Run(context.Background(), "many", nil, NewSession(buffer.New("a"), nil))
	if err == nil || !strings.Contains(err.Error(), "at most 256 marks") {
		t.Fatalf("got %v", err)
	}
	// Across commands the unreferenced ones are collected.
	for range 5 {
		if _, err := onText(t, r, "a", "mark", "1", "1"); err != nil {
			t.Fatal(err)
		}
		live(r, "a")
	}
	runtime.GC()
	live(r, "a")
	if n := r.markCount(); n > 5 {
		t.Fatalf("%d marks alive", n)
	}
}

func TestMarkOperationsCheckTheirArguments(t *testing.T) {
	r := loaded(t, Options{}, markProgram+`
(define-command "not-a-mark" (lambda () (mark-line 5)))
(define-command "bad-line" (lambda () (mark-at 99 1)))
(define-command "bad-col" (lambda () (mark-at 1 99)))
`)
	for name, want := range map[string]string{
		"not-a-mark": "expected a mark",
		"bad-line":   "outside 1..",
		"bad-col":    "outside 1..",
	} {
		_, err := onText(t, r, "abc", name)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestMarksCostNothingUntilOneExists(t *testing.T) {
	r := loaded(t, Options{}, markProgram)
	if r.HasMarks() {
		t.Fatal("marks before any was made")
	}
	r.SyncMarks(buffer.New("x").Lines(), 7, 1)
	if r.tracker.snap != nil {
		t.Fatal("a snapshot of the text is kept with no marks to serve")
	}
}

const wordProgram = `
(define saved #f)
(define word "")
(define-command "range"
  (lambda ()
    (let ((r (word-range)))
      (message (if r
                   (string-append (number->string (car r)) ":" (number->string (cadr r)) "-" (number->string (caddr r)))
                   "none")))))
(define-command "mark-word"
  (lambda ()
    (let ((r (word-range)))
      (set! word (word-at-cursor))
      (set! saved (mark-at (car r) (cadr r))))))
(define-command "swap" (lambda (text) (replace-at-mark saved word text)))
(define-command "replace-text" (lambda (expected text) (replace-at-mark saved expected text)))
`

func TestWordRangeIsTheWordUnderTheCursor(t *testing.T) {
	r := loaded(t, Options{}, wordProgram)
	cases := []struct {
		text     string
		row, col int
		want     string
	}{
		{"one two three", 0, 4, "1:5-7"},
		{"one two three", 0, 5, "1:5-7"},
		{"one two three", 0, 6, "1:5-7"},
		{"one two three", 0, 0, "1:1-3"},
		{"one two three", 0, 12, "1:9-13"},
		{"one two three", 0, 3, "none"}, // on the space
		{"say héllo_1, ok", 0, 6, "1:5-11"},
		{"a\nb c", 1, 2, "2:3-3"},
		{"(x)", 0, 0, "none"},
		{"", 0, 0, "none"},
	}
	for _, c := range cases {
		s := session(c.text, c.row, c.col, nil, false)
		if err := r.Run(context.Background(), "range", nil, s); err != nil || s.Message() != c.want {
			t.Errorf("word-range in %q at %d,%d = %q (%v); want %q", c.text, c.row, c.col, s.Message(), err, c.want)
		}
	}
}

func markWord(t *testing.T, r *Runtime, text string, row, col int) {
	t.Helper()
	s := session(text, row, col, nil, false)
	if err := r.Run(context.Background(), "mark-word", nil, s); err != nil {
		t.Fatal(err)
	}
	r.Adopt(s)
	live(r, text)
}

func replaceOn(t *testing.T, r *Runtime, text, name string, args ...string) (*Session, error) {
	t.Helper()
	s := session(text, 0, 0, nil, false)
	err := r.Run(context.Background(), name, args, s)
	return s, err
}

func TestReplaceAtMarkReplacesTheWordWhereverItHasMoved(t *testing.T) {
	r := loaded(t, Options{}, wordProgram)
	text := "one two three"
	live(r, text)
	markWord(t, r, text, 0, 5)
	typed := "new line\none two three" // the person typed a line above
	live(r, typed)
	s, err := replaceOn(t, r, typed, "swap", "2")
	if err != nil || s.Buffer.Text() != "new line\none 2 three" {
		t.Fatalf("got %q, %v", s.Buffer.Text(), err)
	}
	if !s.Edited() {
		t.Fatal("the edit was not recorded")
	}
}

func TestReplaceAtMarkRefusesWhenTheWordWasEditedAway(t *testing.T) {
	r := loaded(t, Options{}, wordProgram)
	text := "one two three"
	live(r, text)
	markWord(t, r, text, 0, 5)
	edited := "one tw three"
	live(r, edited)
	s, err := replaceOn(t, r, edited, "swap", "2")
	if err == nil || !strings.Contains(err.Error(), `the text at the mark is "tw "`) || !strings.Contains(err.Error(), `not "two"`) {
		t.Fatalf("got %v", err)
	}
	if s.Buffer.Text() != edited {
		t.Fatalf("the text changed: %q", s.Buffer.Text())
	}
}

func TestReplaceAtMarkCanDeleteAndCanSpanLines(t *testing.T) {
	r := loaded(t, Options{}, wordProgram)
	text := "keep this\nand that"
	live(r, text)
	markWord(t, r, text, 0, 5)
	s, err := replaceOn(t, r, text, "swap", "")
	if err != nil || s.Buffer.Text() != "keep \nand that" {
		t.Fatalf("delete: %q %v", s.Buffer.Text(), err)
	}
	s, err = replaceOn(t, r, text, "replace-text", "this\nand", "X")
	if err != nil || s.Buffer.Text() != "keep X that" {
		t.Fatalf("across lines: %q %v", s.Buffer.Text(), err)
	}
}

func TestReplaceAtMarkNeedsSomethingToReplace(t *testing.T) {
	r := loaded(t, Options{}, wordProgram)
	text := "ab"
	live(r, text)
	markWord(t, r, text, 0, 0)
	for name, tc := range map[string]struct {
		expected, want string
	}{
		"empty":        {"", "cannot be empty"},
		"past the end": {"abcd", `is "ab", not "abcd"`},
	} {
		_, err := replaceOn(t, r, text, "replace-text", tc.expected, "x")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v; want %q", name, err, tc.want)
		}
	}
}

func TestReplaceAtMarkWorksOnTheTextOfAMarkMadeInTheSameCommand(t *testing.T) {
	r := loaded(t, Options{}, wordProgram+`
(define-command "same" (lambda ()
  (let ((m (mark-at 1 5)))
    (insert-at 1 1 ">> ")
    (replace-at-mark m "two" "2"))))`)
	s := session("one two", 0, 0, nil, false)
	if err := r.Run(context.Background(), "same", nil, s); err != nil || s.Buffer.Text() != ">> one 2" {
		t.Fatalf("got %q, %v", s.Buffer.Text(), err)
	}
}
