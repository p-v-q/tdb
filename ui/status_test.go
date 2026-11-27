package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/i18n"
)

// objectSelection focuses the object browser on a named table, so the status
// bar has a real row and position to show.
func objectSelection(t *testing.T, m Model, table string) (Model, int) {
	t.Helper()
	idx := objectRowIndex(m, table)
	if idx < 0 {
		t.Fatalf("no %q row in the object tree", table)
	}
	m.layout.focus = paneObjects
	m.objects.sel = idx
	return m, idx
}

func TestStatusBarObjectSelection(t *testing.T) {
	m, idx := objectSelection(t, sized(""), "users")
	rows := m.objectsRows()
	status := stripANSI(m.renderStatus())

	if want := fmt.Sprintf("Object %d/%d", idx+1, len(rows)); !strings.Contains(status, want) {
		t.Fatalf("status = %q, want the object position %q", status, want)
	}
	if !strings.Contains(status, "users") {
		t.Fatalf("status = %q, want the selected object name", status)
	}
	if !strings.Contains(status, m.t(i18n.KeyStatusObjectsHints)) {
		t.Fatalf("status = %q, want the object-browser hints", status)
	}
}

func TestStatusBarObjectBrowserWithoutSelection(t *testing.T) {
	m := sized("")
	m.layout.focus = paneObjects
	m.objects.sel = -1
	status := stripANSI(m.renderStatus())
	if !strings.HasPrefix(status, " "+m.t(i18n.KeyPaneObjects)) {
		t.Fatalf("status = %q, want the pane name on the left", status)
	}
	if !strings.Contains(status, m.t(i18n.KeyStatusObjectsHints)) {
		t.Fatalf("status = %q, want the object-browser hints", status)
	}
}

func TestStatusBarObjectFilterInput(t *testing.T) {
	m := sized("")
	m.layout.focus = paneObjects
	m.objects.filtering = true
	m.objects.filter = "us"

	status := stripANSI(m.renderStatus())
	if !strings.Contains(status, "Filter /us") {
		t.Fatalf("status = %q, want the filter query", status)
	}
	if !strings.Contains(status, m.t(i18n.KeyStatusObjectsFilterHints)) {
		t.Fatalf("status = %q, want the filter hints", status)
	}

	// An empty query still shows the prompt, not a bare "Filter".
	m.objects.filter = ""
	status = stripANSI(m.renderStatus())
	if !strings.HasPrefix(status, " "+m.t(i18n.KeyStatusFiltering, "")) {
		t.Fatalf("status = %q, want the empty filter prompt", status)
	}
}

// wideModel is sized so the status bar has room for its left-hand text and the
// right-hand hints at once; on a narrow terminal the hints are dropped rather
// than squeezing the left side out.
func wideModel(text string) Model {
	m := newTestModel(text)
	return send(m, tea.WindowSizeMsg{Width: 160, Height: 30})
}

func TestStatusBarResultsFocus(t *testing.T) {
	m := wideModel("")
	m.layout.focus = paneResults
	status := stripANSI(m.renderStatus())
	if !strings.Contains(status, m.t(i18n.KeyResultsCaretHint)) {
		t.Fatalf("status = %q, want the result-grid caret hint", status)
	}
	if !strings.Contains(status, m.t(i18n.KeyStatusResultsHints)) {
		t.Fatalf("status = %q, want the result-grid key hints", status)
	}
}

func TestStatusBarEditorKeepsGeneralHints(t *testing.T) {
	m := wideModel("")
	m.layout.focus = paneEditor
	status := stripANSI(m.renderStatus())
	if !strings.Contains(status, m.t(i18n.KeyStatusHints)) {
		t.Fatalf("status = %q, want the general hints", status)
	}
	if strings.Contains(status, m.t(i18n.KeyStatusObjectsHints)) ||
		strings.Contains(status, m.t(i18n.KeyStatusResultsHints)) {
		t.Fatalf("status = %q, editor focus must not show pane-specific hints", status)
	}
}

func TestStatusBarTransactionIndicatorStaysBeforeHints(t *testing.T) {
	m, _ := objectSelection(t, sized(""), "users")
	m.txOpen = true
	status := stripANSI(m.renderStatus())

	tx := strings.Index(status, m.t(i18n.KeyStatusUncommitted))
	hints := strings.Index(status, m.t(i18n.KeyStatusObjectsHints))
	if tx < 0 || hints < 0 {
		t.Fatalf("status = %q, want both the transaction indicator and the hints", status)
	}
	if tx > hints {
		t.Fatalf("status = %q, the transaction indicator must precede the hints", status)
	}

	m.txOpen = false
	m.txManual = true
	status = stripANSI(m.renderStatus())
	manual := strings.Index(status, m.t(i18n.KeyStatusManualCommit))
	hints = strings.Index(status, m.t(i18n.KeyStatusObjectsHints))
	if manual < 0 || hints < 0 || manual > hints {
		t.Fatalf("status = %q, the manual-commit indicator must precede the hints", status)
	}
}
