package ui

// status.go implements transient status-bar messages and the command that
// expires them. Stale timers are ignored by comparing message ids.

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type statusState struct {
	message string
	id      int // used to ignore stale timeout messages
}

// statusTimeoutMsg means a status bar message should disappear.
type statusTimeoutMsg int

// noopMsg is returned by commands that have nothing to report, so the caller
// gets a concrete message instead of a nil one.
type noopMsg struct{}

// statusMessageMsg is a status bar message produced by a background task.
type statusMessageMsg string

// statusTimeout is how long a status message stays on screen. Tests shorten it
// so that draining a command never waits on the timer.
var statusTimeout = 3 * time.Second

// setMessage shows a status bar message and returns a command that clears it a
// few seconds later.
//
// The message is drawn as plain text, and it may carry text from outside the
// program (the words of a database error, a name from the catalog). It is
// folded into one clean line here, so that no path into the status bar can
// put an escape sequence into the terminal.
func (m *Model) setMessage(text string) tea.Cmd {
	m.status.id++
	m.status.message = oneLine(text)
	id := m.status.id
	return tea.Tick(statusTimeout, func(time.Time) tea.Msg {
		return statusTimeoutMsg(id)
	})
}
