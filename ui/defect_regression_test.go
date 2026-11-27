package ui

// defect_regression_test.go pins product defects that real-terminal integration
// scenarios found and that are fixed here, so they cannot come back unnoticed.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/driver"
)

// TestSaveFileWithoutFileReportsStatus: Ctrl+S on a tab that was never given a
// file must say so in the status bar. The message used to be written into a
// value receiver's copy and silently dropped.
func TestSaveFileWithoutFileReportsStatus(t *testing.T) {
	m := newTestModel("SELECT 1")
	m.fileName = ""
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 20})

	m.saveFile()
	if strings.TrimSpace(m.status.message) == "" {
		t.Fatal("saving a tab without a file must report why nothing was saved")
	}
	if !strings.Contains(m.status.message, "file") {
		t.Fatalf("status = %q, want the no-file explanation", m.status.message)
	}
}

// TestPendingEditValueIsDrawn: after a cell edit is committed, the grid shows
// the value the user typed (the database still holds the old one). It used to
// keep drawing the old value while marking the row as pending.
func TestPendingEditValueIsDrawn(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight}) // the email column
	m = editCell(t, m, "new@example.com")
	m.layout.focus = paneEditor // render the row without the caret colour

	row := stripANSI(m.resultRow(0, 0, 120, m.resultsGutterWidth()))
	if !strings.Contains(row, "new@example.com") {
		t.Fatalf("the pending value is not drawn in the grid: %q", row)
	}
	if strings.Contains(row, "ada@example.com") {
		t.Fatalf("the grid still draws the old database value: %q", row)
	}
}

// TestWideGlyphCellsAreNotClipped pins the width rule for cells that occupy two
// terminal columns: the column has to be measured in cells, not runes, or a
// value that fits the screen is drawn with its tail cut away.
func TestWideGlyphCellsAreNotClipped(t *testing.T) {
	value := "unicode 你好 😀"
	m := gridModel(t,
		[]driver.ResultColumn{{Name: "v", Type: "TEXT"}},
		[][]*string{{&value}})
	if got, want := m.session.result.colWidths[0], lipgloss.Width(value); got < want {
		t.Fatalf("column width = %d, want at least %d cells for %q", got, want, value)
	}
	row := stripANSI(m.resultRow(0, 0, 120, m.resultsGutterWidth()))
	if !strings.Contains(row, value) {
		t.Fatalf("the wide value is clipped: %q", row)
	}
	if strings.Contains(row, "…") {
		t.Fatalf("the wide value must not be marked as clipped: %q", row)
	}
}

// TestExportEnterSubmits: Enter in the export path field exports, as the dialog
// hint promises. It used to be a mouse-only action.
func TestExportEnterSubmits(t *testing.T) {
	value := "ada@example.com"
	m := gridModel(t,
		[]driver.ResultColumn{{Name: "email", Type: "TEXT"}},
		[][]*string{{&value}})
	path := filepath.Join(t.TempDir(), "rows.csv")
	m.exportDlg = exportDialog{open: true, focus: exportFocusPath, path: path, caret: len([]rune(path))}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.exportDlg.open {
		t.Fatal("Enter must close the export dialog")
	}
	if cmd == nil {
		t.Fatal("Enter must produce the export command")
	}
	_ = cmd()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the export was not written: %v", err)
	}
	if !strings.Contains(string(data), "ada@example.com") {
		t.Fatalf("exported file = %q", data)
	}
}
