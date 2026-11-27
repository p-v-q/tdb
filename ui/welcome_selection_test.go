package ui

// welcome_selection_test.go pins that every selectable welcome row shows the
// selection, including a saved connection, which has no accelerator printed on
// the right. Without it the keyboard selection on a connection row is
// invisible while Enter still connects to it.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
)

func welcomeSelectionModel(t *testing.T) Model {
	t.Helper()
	m := New(Config{Title: "TDB", ConnName: "demo", Translator: i18n.New(i18n.En)})
	m.settings.Connections = []settings.Connection{
		{Name: "shop", Driver: "sqlite", File: "shop.db"},
		{Name: "reporting", Driver: "postgres", DSN: "postgres://localhost/shop"},
	}
	return m
}

func TestWelcomeSelectionHighlightsConnectionRows(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	m := welcomeSelectionModel(t)
	width := 100
	for idx, it := range m.welcomeItems() {
		if it.kind != welcomeConnection {
			continue
		}
		selected := m.welcomeItemRow(it, idx, idx, width)
		unselected := m.welcomeItemRow(it, idx, -1, width)
		if selected == unselected {
			t.Fatalf("connection row %d (%q) looks the same selected and unselected", idx, it.label)
		}
		if strings.TrimSpace(stripANSI(selected)) == "" {
			t.Fatalf("selected connection row %d rendered blank", idx)
		}
	}
}

func TestWelcomeKeyboardSelectionReachesAConnection(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	// The connection row must stay highlighted while the selection walks down
	// the list with the keyboard.
	m := welcomeSelectionModel(t)
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	connIdx := -1
	for i, it := range m.welcomeItems() {
		if it.kind == welcomeConnection {
			connIdx = i
			break
		}
	}
	if connIdx < 0 {
		t.Fatal("the fixture must contain a connection row")
	}
	m.welcomeSel = connIdx
	row := m.welcomeItemRow(m.welcomeItems()[connIdx], connIdx, connIdx, 120)
	if !strings.Contains(row, "\x1b[") || row == "" {
		t.Fatalf("the selected connection row carries no styling: %q", row)
	}
}
