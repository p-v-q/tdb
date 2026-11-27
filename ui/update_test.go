package ui

// update_test.go covers the message loop: how the outcomes of the background
// work reach the model and what the status bar says about them.

import (
	gosql "database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestIsConnectionLost pins the heuristic that tells a lost connection from
// an ordinary query error. It is a heuristic on purpose — the drivers wrap
// their errors differently — so the cases spell out what counts.
func TestIsConnectionLost(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"no error", nil, false},
		{"end of stream", io.EOF, true},
		{"wrapped end of stream", fmt.Errorf("read row: %w", io.EOF), true},
		{"connection done", gosql.ErrConnDone, true},
		{"network error", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("boom")}, true},
		{"connection refused", errors.New("dial tcp 127.0.0.1:5432: connection refused"), true},
		{"closed connection", errors.New("sql: database is closed (closed connection)"), true},
		{"broken pipe", errors.New("write tcp: broken pipe"), true},
		{"connection reset", errors.New("read tcp: connection reset by peer"), true},
		{"syntax error", errors.New(`syntax error at or near "FROM"`), false},
		{"missing table", errors.New(`no such table: missing_table`), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isConnectionLost(tc.err); got != tc.want {
				t.Fatalf("isConnectionLost(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestQueryFailureReportsALostConnection checks the wording of a query that
// failed because the connection is gone: the user is told to reconnect
// instead of only seeing "Query failed".
func TestQueryFailureReportsALostConnection(t *testing.T) {
	conn := newFakeConn()
	conn.queryErr = io.EOF
	m := sized("SELECT 1")
	m.connOpen = conn
	m.cfg.Provider = conn

	m = runQuery(m)
	if !strings.Contains(m.status.message, "The connection is gone") {
		t.Fatalf("status = %q, want the lost-connection message", m.status.message)
	}
	// The driver's own words are kept: they tell the user what happened.
	if !strings.Contains(m.status.message, "EOF") {
		t.Fatalf("status = %q, want the original error text", m.status.message)
	}
	// The error is still shown in the result grid.
	if m.session.result.err == nil {
		t.Fatal("the failing statement must be reported in the grid")
	}
}

// TestQueryFailureOfAnOrdinaryErrorKeepsTheOldWording makes sure the
// lost-connection wording is only used when it fits.
func TestQueryFailureOfAnOrdinaryErrorKeepsTheOldWording(t *testing.T) {
	conn := newFakeConn()
	conn.queryErr = errors.New("no such table: missing_table")
	m := sized("SELECT * FROM missing_table")
	m.connOpen = conn
	m.cfg.Provider = conn

	m = runQuery(m)
	if strings.Contains(m.status.message, "The connection is gone") {
		t.Fatalf("status = %q, want the ordinary failure message", m.status.message)
	}
	if !strings.Contains(m.status.message, "Query failed") {
		t.Fatalf("status = %q, want the ordinary failure message", m.status.message)
	}
}

// TestQueryFailureOfALostConnectionIsStillRecorded pins that a lost
// connection does not stop the statement from being filed in the history: it
// ran, and the log should say so.
func TestQueryFailureOfALostConnectionIsStillRecorded(t *testing.T) {
	conn := newFakeConn()
	conn.queryErr = io.EOF
	m := sized("SELECT 1")
	m.connOpen = conn
	m.cfg.Provider = conn

	m = runQuery(m)
	if len(m.historyEntries) != 1 {
		t.Fatalf("history entries = %d, want the failed statement", len(m.historyEntries))
	}
	if m.historyEntries[0].Error == "" {
		t.Fatal("the history entry must keep the error")
	}
}

// runQuery runs the statement of the active tab and reports the outcome. The
// commands are not drained completely: the status message expires on a timer,
// and the tests want to read it before it does.
func runQuery(m Model) Model {
	next, cmd := sendCmd(m, tea.KeyMsg{Type: tea.KeyF5})
	for _, msg := range collectCmds(cmd) {
		if qm, ok := msg.(queryMsg); ok {
			next = send(next, qm)
		}
	}
	return next
}
