//go:build unix

package ui

// open_file_fifo_test.go pins the refusal of a FIFO by "open SQL file". The
// path is stat'ed before it is opened, and a FIFO fails the regular-file rule
// there: opening one without a writer blocks the caller until a writer shows
// up, which would freeze the whole interface — the fate the checks exist to
// prevent.
//
// The file is built for unix only because mkfifo has no portable spelling
// (Windows has no FIFOs); the code under test is the same on every platform,
// and the descriptor re-check that guards the same race after the open is
// covered portably by TestVerifyOpenSQLFile* in open_file_test.go.

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/p-v-q/tdb/i18n"
)

// TestOpenSQLFileRefusesAFIFOWithoutBlocking runs the open on its own copy of
// the model in a goroutine and watches it with a timeout: a refusal is
// immediate, and anything that waits for a writer is a frozen interface.
func TestOpenSQLFileRefusesAFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sneaky.sql")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("cannot create a FIFO: %v", err)
	}

	m := dialogModel(t, 100, 30)
	done := make(chan string, 1)
	go func() {
		// A copy of the model: the goroutine writes the status of its own
		// state, so the message the test reads later is never written
		// concurrently with anything the test does.
		local := m
		_ = local.openSQLFile(path)
		done <- local.status.message
	}()

	select {
	case msg := <-done:
		if want := m.t(i18n.KeyMsgFileOpenFailed, path); msg != want {
			t.Fatalf("status = %q, want %q", msg, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("openSQLFile is still waiting on the FIFO — the interface would be frozen")
	}
}
