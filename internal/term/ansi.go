package term

import "strconv"

// Escape sequences exactly as crossterm 0.29 writes them. The renderer
// assembles whole frames from these and writes each frame once, so the byte
// stream (and with it what the PTY tests assert) is fully under our control.
const (
	EnterAlternateScreen    = "\x1b[?1049h"
	LeaveAlternateScreen    = "\x1b[?1049l"
	Show                    = "\x1b[?25h"
	Hide                    = "\x1b[?25l"
	BeginSynchronizedUpdate = "\x1b[?2026h"
	EndSynchronizedUpdate   = "\x1b[?2026l"
	ResetColor              = "\x1b[0m"
	ClearUntilNewLine       = "\x1b[K"
	ClearAll                = "\x1b[2J"

	Reverse         = "\x1b[7m"
	NoReverse       = "\x1b[27m"
	Bold            = "\x1b[1m"
	NormalIntensity = "\x1b[22m"
	Italic          = "\x1b[3m"
	NoItalic        = "\x1b[23m"

	// ResetForeground and ResetBackground select the terminal's default colours.
	ResetForeground = "\x1b[39m"
	ResetBackground = "\x1b[49m"
)

// MoveTo positions the cursor at zero-based column x, row y.
func MoveTo(x, y int) string {
	return "\x1b[" + strconv.Itoa(y+1) + ";" + strconv.Itoa(x+1) + "H"
}

// ForegroundRGB selects a 24-bit foreground colour.
func ForegroundRGB(r, g, b uint8) string {
	return "\x1b[38;2;" + strconv.Itoa(int(r)) + ";" + strconv.Itoa(int(g)) + ";" + strconv.Itoa(int(b)) + "m"
}

// BackgroundRGB selects a 24-bit background colour.
func BackgroundRGB(r, g, b uint8) string {
	return "\x1b[48;2;" + strconv.Itoa(int(r)) + ";" + strconv.Itoa(int(g)) + ";" + strconv.Itoa(int(b)) + "m"
}

// ForegroundAnsi selects a colour from the terminal's 256-colour palette.
func ForegroundAnsi(n uint8) string { return "\x1b[38;5;" + strconv.Itoa(int(n)) + "m" }

// BackgroundAnsi selects a background from the terminal's 256-colour palette.
func BackgroundAnsi(n uint8) string { return "\x1b[48;5;" + strconv.Itoa(int(n)) + "m" }

// restoreSequence is what the terminal receives on every exit path: end any
// synchronized update, show the cursor, reset colours, leave the alternate screen.
const restoreSequence = EndSynchronizedUpdate + Show + ResetColor + LeaveAlternateScreen
