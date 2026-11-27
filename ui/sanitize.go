package ui

// sanitize.go is the one place where text that comes from outside the
// program is made safe to print.
//
// lipgloss styles a string; it does not escape it. An ANSI sequence inside
// the text therefore reaches the terminal as it is, and the terminal executes
// it: a table name that carries an escape sequence (PostgreSQL allows
// control characters inside a quoted identifier), a database error that
// repeats what the server was sent, or a paste from another program can
// repaint the screen, move the cursor or change the terminal's settings.
//
// The rule is therefore simple: every text that did not come from the user's
// own keystrokes is cleaned before it is rendered. The editor buffer is the
// deliberate exception — it shows what the user typed, verbatim.

import (
	"strings"
	"unicode"
)

// stripControl drops every control character from a text.
//
// What is left is printable: the user reads what the program meant to say,
// without the sequences the terminal would have executed. It is the rule for
// the texts that must stay on one line and keep their shape — a name, a
// pasted fragment.
func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// oneLine folds a text into a single clean line.
//
// A line break or a tab becomes a space, the remaining control characters
// become spaces too, and the runs of whitespace collapse into one. It is
// used for the texts that are shown in one line only — a catalog comment, a
// database error — where a line break would break the layout and an escape
// sequence would break the terminal.
func oneLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// breaksToSpace folds the line breaks and tabs of a text into single spaces
// in one pass, so that a CRLF counts as one break and not two.
var breaksToSpace = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ")

// foldBreaks flattens a text onto one clean line, for the grid.
//
// The grid draws exactly one line per row, so a break inside a value has to
// go — but deleting it would glue the words on its two sides together
// ("a\nb" would read "ab", a tab-separated pair would become one word).
// Each break therefore becomes a single space, and whatever control
// character survives that — the ESC of an escape sequence among them — is
// dropped by stripControl. Like stripControl it leaves plain spaces alone,
// so the shape of a padded value is unchanged.
func foldBreaks(s string) string {
	return stripControl(breaksToSpace.Replace(s))
}

// sanitizeMultiline cleans a message that must keep its line breaks.
//
// The error of a statement is laid out over several lines — wrapText builds
// them from the breaks it finds in the text — so the breaks have to survive,
// while every other control character is a character the terminal could act
// on. Those become spaces instead of disappearing: a tab that separated two
// words must not merge them once it is gone, and wrapText collapses runs of
// spaces anyway, so nothing of the layout depends on them.
func sanitizeMultiline(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || !unicode.IsControl(r) {
			return r
		}
		return ' '
	}, s)
}

// errText renders an error for the status bar.
//
// The message stays readable — the user needs the words of the server — but
// the control characters in it are gone: an error is text from outside the
// program and must not be able to repaint the terminal.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return oneLine(err.Error())
}
