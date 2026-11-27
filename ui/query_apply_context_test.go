package ui

// query_apply_context_test.go pins the context carried by failed grid applies.
// A failure before the first UPDATE is still an apply failure: it must reach
// finishApply, keep the user's pending edits, and retain the session,
// connection, origin and transaction state of the batch.

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

type beginFailingConn struct {
	*recConn
	beginErr error
}

func (c *beginFailingConn) Begin(context.Context) error { return c.beginErr }

type commitFailingConn struct {
	*recConn
	commitErr error
}

func (c *commitFailingConn) Commit(context.Context) error { return c.commitErr }

// TestGridApplyBeginFailureKeepsPendingChangesAndContext reproduces the high
// finding: the Begin-error queryMsg had no apply marker, so update.go treated it
// as an ordinary query error and setResults cleared the confirmed edits.
func TestGridApplyBeginFailureKeepsPendingChangesAndContext(t *testing.T) {
	beginErr := errors.New("transaction could not be opened")
	rec := &recConn{Connection: e2eConn(t)}
	conn := &beginFailingConn{recConn: rec, beginErr: beginErr}
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})
	m = editCell(t, m, "kept@example.com")
	m = openApplyDialog(t, m)

	apply, sess := m.confirm.apply, m.session
	origin, originalRes := sess.result.origin, sess.result.res
	next, cmd, ok := m.acceptConfirm()
	if !ok || cmd == nil {
		t.Fatal("confirming must dispatch the grid apply")
	}
	msg := cmd()
	q, ok := msg.(queryMsg)
	if !ok {
		t.Fatalf("the apply returned %T, want queryMsg", msg)
	}
	if !errors.Is(q.err, beginErr) {
		t.Fatalf("error = %v, want the Begin failure", q.err)
	}

	m = send(next, q)
	if got := m.session.result.pendingCount(); got != 1 {
		t.Fatalf("pending changes = %d, want 1 after Begin failed", got)
	}
	if m.session.result.res != originalRes {
		t.Fatal("a Begin failure must preserve the grid the pending edits belong to")
	}
	p, ok := m.session.result.pendingAt(0, 1)
	if !ok || p.value == nil || *p.value != "kept@example.com" {
		t.Fatalf("pending edit = %+v, want the user's value kept unchanged", p)
	}
	if !strings.Contains(m.status.message, "could not be applied") ||
		!strings.Contains(m.status.message, beginErr.Error()) {
		t.Fatalf("status = %q, want the apply failure and its cause", m.status.message)
	}
	if q.apply != apply || q.sess != sess {
		t.Fatal("the Begin failure lost the grid-apply or session context")
	}
	if q.open != conn || q.conn != origin.conn.name ||
		q.origin.conn.conn != origin.conn.conn || q.origin.schema != origin.schema {
		t.Fatal("the Begin failure lost the connection or result origin")
	}
	if q.txOpen || q.rolledBack {
		t.Fatalf("txOpen/rolledBack = %v/%v, want false/false", q.txOpen, q.rolledBack)
	}
	if got := rec.updateCount(); got != 0 {
		t.Fatalf("UPDATEs = %d, want 0 when Begin fails", got)
	}
}

// TestGridApplyCommitFailureKeepsPendingChangesAndContext covers the other
// early-return path: Commit errors must carry the same apply/origin context,
// and the still-open transaction must remain visible to the user.
func TestGridApplyCommitFailureKeepsPendingChangesAndContext(t *testing.T) {
	commitErr := errors.New("transaction could not be committed")
	rec := &recConn{Connection: e2eConn(t)}
	conn := &commitFailingConn{recConn: rec, commitErr: commitErr}
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})
	m = editCell(t, m, "kept@example.com")
	m = openApplyDialog(t, m)

	apply, sess := m.confirm.apply, m.session
	origin := sess.result.origin
	next, cmd, ok := m.acceptConfirm()
	if !ok || cmd == nil {
		t.Fatal("confirming must dispatch the grid apply")
	}
	msg := cmd()
	q, ok := msg.(queryMsg)
	if !ok {
		t.Fatalf("the apply returned %T, want queryMsg", msg)
	}
	if !errors.Is(q.err, commitErr) {
		t.Fatalf("error = %v, want the Commit failure", q.err)
	}
	if q.apply != apply || q.sess != sess {
		t.Fatal("the Commit failure lost the grid-apply or session context")
	}
	if q.open != conn || q.origin.conn.conn != origin.conn.conn || q.origin.schema != origin.schema {
		t.Fatal("the Commit failure lost the connection or result origin")
	}
	if !q.txOpen {
		t.Fatal("a failed Commit must report that the transaction is still open")
	}

	m = send(next, q)
	if got := m.session.result.pendingCount(); got != 1 {
		t.Fatalf("pending changes = %d, want 1 after Commit failed", got)
	}
	if !m.txOpen || !strings.Contains(m.status.message, "transaction") ||
		!strings.Contains(m.status.message, commitErr.Error()) {
		t.Fatalf("txOpen/status = %v/%q, want the open transaction and Commit cause", m.txOpen, m.status.message)
	}
}
