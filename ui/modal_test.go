package ui

// modal_test.go pins the geometry rules of ui/modal.go and the behaviour of
// the shared dropdown (ui/select_list.go): the size clamps, the centring, the
// keyboard contract and the mouse hit test of the open list.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/sql"
)

func TestModalSizeOn40x80(t *testing.T) {
	// On a 40-column screen the screen wins: the box fills the screen minus
	// the two-column margins, and it keeps at least the minimum height.
	w, h := modalSize(60, 12, 40, 80)
	if w != 36 {
		t.Fatalf("w = %d, want 36 (screen-4)", w)
	}
	if h != 12 {
		t.Fatalf("h = %d, want the minimum height", h)
	}
	if w >= 40 {
		t.Fatalf("the dialog must not be as wide as the screen: %d", w)
	}
}

func TestModalSizeOn120x40(t *testing.T) {
	// On a wide screen the wanted size wins up to the caps.
	w, h := modalSize(60, 12, 120, 40)
	if w != 60 {
		t.Fatalf("w = %d, want the wanted width", w)
	}
	if h != 12 {
		t.Fatalf("h = %d, want the minimum height", h)
	}
	w, h = modalSize(150, 60, 120, 40)
	if w != 100 {
		t.Fatalf("w = %d, want the wide cap", w)
	}
	if h != 40 {
		t.Fatalf("h = %d, want the screen height", h)
	}
}

func TestModalSizeRespectsWantedSize(t *testing.T) {
	// A dialog wanting more than the minimum gets it; one wanting less is
	// grown to the minimum.
	w, h := modalSize(72, 16, 200, 60)
	if w != 72 || h != 16 {
		t.Fatalf("w, h = %d, %d, want 72, 16", w, h)
	}
	w, h = modalSize(20, 5, 200, 60)
	if w != 60 || h != 12 {
		t.Fatalf("w, h = %d, %d, want the minimum size", w, h)
	}
}

func TestModalSizeOnTinyScreens(t *testing.T) {
	// The dialog is never larger than the screen, and never nonsense either.
	w, h := modalSize(60, 12, 10, 5)
	if w != 6 {
		t.Fatalf("w = %d, want screen-4", w)
	}
	if h != 5 {
		t.Fatalf("h = %d, want the screen height", h)
	}
	w, h = modalSize(60, 12, 2, 1)
	if w < 1 || h < 1 {
		t.Fatalf("w, h = %d, %d must stay positive", w, h)
	}
}

func TestModalSizeCompactFloorsAtEightRows(t *testing.T) {
	// A dialog wanting less than the compact floor is grown to it, not to the
	// full 12-row minimum: that wall of blank rows is what the compact entry
	// exists for.
	w, h := modalSizeCompact(20, 5, 200, 60)
	if w != 60 || h != 8 {
		t.Fatalf("w, h = %d, %d, want the compact minimum", w, h)
	}
	// A dialog wanting more gets it, like the regular rule.
	w, h = modalSizeCompact(72, 16, 200, 60)
	if w != 72 || h != 16 {
		t.Fatalf("w, h = %d, %d, want 72, 16", w, h)
	}
}

func TestModalSizeCompactKeepsTheWidthRules(t *testing.T) {
	// Compactness is a height concession only: on a narrow screen the screen
	// still wins the width.
	if w, _ := modalSizeCompact(60, 8, 40, 80); w != 36 {
		t.Fatalf("w = %d, want 36 (screen-4)", w)
	}
	// The wide cap and the screen height cap behave as in modalSize.
	w, h := modalSizeCompact(150, 60, 120, 40)
	if w != 100 || h != 40 {
		t.Fatalf("w, h = %d, %d, want the wide cap and the screen height", w, h)
	}
	// Tiny screens stay positive.
	w, h = modalSizeCompact(60, 8, 2, 1)
	if w < 1 || h < 1 {
		t.Fatalf("w, h = %d, %d must stay positive", w, h)
	}
}

func TestModalRectCentresInTheArea(t *testing.T) {
	area := Rect{X: 5, Y: 3, W: 80, H: 40}
	r := modalRect(area, 60, 12)
	if r.X != 5+10 || r.Y != 3+14 {
		t.Fatalf("rect = %+v, want the centred one", r)
	}
	if r.W != 60 || r.H != 12 {
		t.Fatalf("rect = %+v, want the wanted size", r)
	}
	// A box larger than its area stays anchored at the area origin instead of
	// drifting to negative coordinates.
	r = modalRect(area, 90, 50)
	if r.X != 5 || r.Y != 3 {
		t.Fatalf("rect = %+v, want the clamped one", r)
	}
}

// ---- The shared dropdown ----

func TestSelectListOpenSetsTheHighlight(t *testing.T) {
	var s selectList
	s.Open([]selectOption{
		{Value: "dark", Label: "Dark"},
		{Value: "light", Label: "Light"},
	}, "light")
	if !s.open || s.sel != 1 {
		t.Fatalf("open=%v sel=%d, want open on the current value", s.open, s.sel)
	}
	// An unknown current value falls back to the first option.
	s.Open([]selectOption{{Value: "a", Label: "A"}}, "zzz")
	if s.sel != 0 {
		t.Fatalf("sel = %d, want 0 for an unknown value", s.sel)
	}
}

func TestSelectListKeys(t *testing.T) {
	var s selectList
	s.Open([]selectOption{
		{Value: "a", Label: "A"},
		{Value: "b", Label: "B"},
		{Value: "c", Label: "C"},
	}, "a")

	// While the list is open every key is handled, so nothing leaks.
	for _, key := range []string{"x", "left", "right", "pgup"} {
		chosen, done, picked, handled := s.handleKey(key)
		if !handled || done || picked || chosen != "" {
			t.Fatalf("key %q: chosen=%q done=%v picked=%v handled=%v, want swallowed", key, chosen, done, picked, handled)
		}
	}
	// ↑↓ move with wrapping, Home/End jump.
	s.handleKey("down")
	if s.sel != 1 {
		t.Fatalf("sel = %d, want 1 after down", s.sel)
	}
	s.handleKey("up")
	s.handleKey("up")
	if s.sel != 2 {
		t.Fatalf("sel = %d, want the wrapped last entry", s.sel)
	}
	s.handleKey("home")
	if s.sel != 0 {
		t.Fatalf("sel = %d, want 0 after home", s.sel)
	}
	s.handleKey("end")
	if s.sel != 2 {
		t.Fatalf("sel = %d, want 2 after end", s.sel)
	}
	// Enter picks the highlighted option and closes the list.
	chosen, done, picked, handled := s.handleKey("enter")
	if !handled || !done || !picked || chosen != "c" {
		t.Fatalf("enter: chosen=%q done=%v picked=%v handled=%v", chosen, done, picked, handled)
	}
	if s.open {
		t.Fatal("picking must close the list")
	}
	// Esc closes the list without choosing anything — picked, not chosen,
	// tells the dismissal apart from picking an option whose value is "".
	s.Open([]selectOption{{Value: "a", Label: "A"}}, "a")
	chosen, done, picked, _ = s.handleKey("esc")
	if done && picked {
		t.Fatalf("esc: picked=%v, want a dismissal, not a pick", picked)
	}
	if chosen != "" {
		t.Fatalf("esc: chosen=%q, want an empty dismissal", chosen)
	}
	if s.open {
		t.Fatal("esc must close the list")
	}
	// A closed list does not handle keys.
	if _, _, _, handled := s.handleKey("enter"); handled {
		t.Fatal("a closed list must not handle keys")
	}
}

func TestSelectListEmptyOptionsClosesItself(t *testing.T) {
	var s selectList
	s.Open(nil, "x")
	chosen, done, picked, handled := s.handleKey("enter")
	if !handled || !done || picked || chosen != "" {
		t.Fatalf("chosen=%q done=%v picked=%v handled=%v, want a defensive close", chosen, done, picked, handled)
	}
}

func TestLayoutSelectListClipsAndScrolls(t *testing.T) {
	options := []selectOption{
		{Value: "1", Label: "one"},
		{Value: "2", Label: "two"},
		{Value: "3", Label: "three"},
		{Value: "4", Label: "four"},
		{Value: "5", Label: "five"},
	}
	// Three option rows fit between the anchor and the bottom edge; the frame
	// covers exactly that and scrolls to keep the highlight visible.
	l := layoutSelectList(options, 4, 20, 10, 40, 14)
	if l.rect.Y != 11 || l.rect.X != 20 {
		t.Fatalf("rect = %+v, want the frame just below the anchor", l.rect)
	}
	if l.visible != 2 {
		t.Fatalf("visible = %d, want only what fits above the bottom edge", l.visible)
	}
	if l.top != 3 {
		t.Fatalf("top = %d, want the highlight scrolled into view", l.top)
	}
	if l.rect.H != l.visible+2 {
		t.Fatalf("height = %d, want %d with both borders", l.rect.H, l.visible+2)
	}
	// A short list is not stretched.
	l = layoutSelectList(options[:2], 0, 20, 10, 40, 30)
	if l.visible != 2 {
		t.Fatalf("visible = %d, want 2", l.visible)
	}
}

func TestSelectRowAtMapsToVisibleOptions(t *testing.T) {
	options := []selectOption{
		{Value: "1", Label: "one"},
		{Value: "2", Label: "two"},
		{Value: "3", Label: "three"},
	}
	l := layoutSelectList(options, 0, 20, 10, 40, 30)
	// outside the frame / on the border → no option
	if _, ok := selectRowAt(l, options, 10, 12); ok {
		t.Fatal("a click left of the frame must not pick an option")
	}
	if _, ok := selectRowAt(l, options, 20, l.rect.Y); ok {
		t.Fatal("a click on the top border must not pick an option")
	}
	// the first visible option
	idx, ok := selectRowAt(l, options, 23, l.rect.Y+1)
	if !ok || idx != 0 {
		t.Fatalf("idx=%d ok=%v, want the first option", idx, ok)
	}
	// the third option, past the visible count? there are three rows and
	// three options, so the last frame row of options is the third one.
	idx, ok = selectRowAt(l, options, 23, l.rect.Y+3)
	if !ok || idx != 2 {
		t.Fatalf("idx=%d ok=%v, want the third option", idx, ok)
	}
}

// ---- The three dialogs on the shared geometry ----

// dialogModel returns a model of the given screen size (the terminal is
// described as width×height, as in the examples above).
func dialogModel(t *testing.T, width, height int) Model {
	t.Helper()
	m := newTestModel("SELECT 1")
	m = send(m, tea.WindowSizeMsg{Width: width, Height: height})
	return m
}

func blockInsideRect(t *testing.T, name string, x, y int, block []string, area Rect) {
	t.Helper()
	w := 0
	for _, line := range block {
		w = max(w, lipgloss.Width(line))
	}
	for i, line := range block {
		if got := lipgloss.Width(line); got != w {
			t.Fatalf("%s: line %d is %d cells, want %d", name, i, got, w)
		}
	}
	if w > area.W || len(block) > area.H {
		t.Fatalf("%s: box %dx%d does not fit the area %dx%d", name, w, len(block), area.W, area.H)
	}
	if x < area.X || y < area.Y || x+w > area.X+area.W || y+len(block) > area.Y+area.H {
		t.Fatalf("%s: origin %d,%d escapes the area %+v", name, x, y, area)
	}
}

func TestDialogsStayInsideTheScreenOn40x80(t *testing.T) {
	m := dialogModel(t, 40, 80)
	area := m.paneRects().area

	m.dlg.open = true
	if x, y, block, ok := m.renderSettingsDialog(); !ok {
		t.Fatal("the settings dialog must render on a 40-column screen")
	} else {
		blockInsideRect(t, "settings", x, y, block, area)
	}
	if view := stripANSI(m.View()); !strings.Contains(view, "Esc close") {
		t.Fatalf("the settings hint must be visible on a 40-column screen:\n%s", view)
	}

	m.dlg.open = false
	m.exportDlg = exportDialog{open: true, path: "/tmp/very-long-export-file-name-that-does-not-fit.csv"}
	if x, y, block, ok := m.renderExportDialog(); !ok {
		t.Fatal("the export dialog must render on a 40-column screen")
	} else {
		blockInsideRect(t, "export", x, y, block, area)
	}
	if view := stripANSI(m.View()); !strings.Contains(view, "Esc cancel") {
		t.Fatalf("the export hint must be visible on a 40-column screen:\n%s", view)
	}

	m.exportDlg.open = false
	m.openConfirmAll("the batch", []execStmt{{
		Text: `UPDATE "main"."users" SET "email" = $1 WHERE "id" = $2`,
		Args: []any{"new@example.com", "1"},
	}}, []sql.RiskLevel{sql.RiskWrite})
	if l := m.confirmLayout(); l.w > area.W || l.h > area.H {
		t.Fatalf("confirm box %dx%d does not fit the area %dx%d", l.w, l.h, area.W, area.H)
	}
	if x, y, block, ok := m.renderConfirmDialog(); !ok {
		t.Fatal("the confirm dialog must render on a 40-column screen")
	} else {
		blockInsideRect(t, "confirm", x, y, block, area)
	}
	if view := stripANSI(m.View()); !strings.Contains(view, "Esc cancel") {
		t.Fatalf("the confirm hint must be visible on a 40-column screen:\n%s", view)
	}
}

func TestDialogsReachTheModalMinimumOnWideScreens(t *testing.T) {
	m := dialogModel(t, 120, 40)

	m.dlg.open = true
	if l := m.dialogLayout(); l.w != 60 || l.h < 12 {
		t.Fatalf("settings box %dx%d, want at least 60x12", l.w, l.h)
	}
	m.dlg.open = false

	m.exportDlg = exportDialog{open: true, path: "rows.csv"}
	if l := m.exportLayout(); l.w != 60 || l.h != exportDialogRows {
		t.Fatalf("export box %dx%d, want %dx%d", l.w, l.h, 60, exportDialogRows)
	}
	m.exportDlg.open = false

	m.openConfirmAll("the batch", []execStmt{{Text: "DROP TABLE users"}},
		[]sql.RiskLevel{sql.RiskDangerousWrite})
	if l := m.confirmLayout(); l.w != 60 || l.h < 12 {
		t.Fatalf("confirm box %dx%d, want at least 60x12", l.w, l.h)
	}
}

func TestSettingsDropdownRendersInsideTheBox(t *testing.T) {
	m := openSettingsDialog(t)
	m.dlg.choice.Open(m.localeOptions(), "")

	x, y, block, ok := m.renderSettingsDialog()
	if !ok {
		t.Fatal("the dialog must render with its dropdown open")
	}
	view := stripANSI(strings.Join(block, "\n"))
	for _, want := range []string{"English", "中文", "Esc close"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the open dropdown must show the options, missing %q:\n%s", want, view)
		}
	}
	// Every line keeps the box width: the dropdown lives inside the frame,
	// so it cannot cross the dialog's border.
	blockInsideRect(t, "settings+dropdown", x, y, block, m.paneRects().area)
}

func TestExportDropdownRendersInsideTheBox(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, name FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if cmd := m.openExportDialog(); cmd != nil {
		t.Fatal("opening the exporter with a result should not show an error")
	}
	m.exportDlg.focus = 1
	m.openExportChoice()

	x, y, block, ok := m.renderExportDialog()
	if !ok {
		t.Fatal("the exporter must render with its dropdown open")
	}
	view := stripANSI(strings.Join(block, "\n"))
	if !strings.Contains(view, "TSV") {
		t.Fatalf("the open format dropdown must show the options:\n%s", view)
	}
	blockInsideRect(t, "export+dropdown", x, y, block, m.paneRects().area)

	// Clicking an option applies the format on the spot.
	cl, ok := m.exportChoiceLayout()
	if !ok {
		t.Fatal("the format dropdown must have a layout")
	}
	m = send(m, mousePress(cl.rect.X+2, cl.rect.Y+2)) // the second option: TSV
	if m.exportDlg.format != exportTSV {
		t.Fatalf("format = %v, want TSV after the click", m.exportDlg.format)
	}
	if !strings.HasSuffix(m.exportDlg.path, ".tsv") {
		t.Fatalf("path = %q, the suffix must follow the pick", m.exportDlg.path)
	}
}

func TestConfirmDialogButtonsIn80Columns(t *testing.T) {
	m := dialogModel(t, 80, 24)
	m.openConfirmAll("the batch", []execStmt{{
		Text: `UPDATE "main"."users" SET "email" = $1 WHERE "id" = $2`,
		Args: []any{"new@example.com", "1"},
	}}, []sql.RiskLevel{sql.RiskWrite})

	l := m.confirmLayout()
	if l.run.X < l.x+1 || l.run.X+l.run.W > l.x+l.w-1 {
		t.Fatalf("run button %+v escapes the box", l.run)
	}
	if l.cancel.X+l.cancel.W > l.x+l.w-1 {
		t.Fatalf("cancel button %+v escapes the box", l.cancel)
	}
	// The body shows the statement, the bound values and the target table at
	// 80 columns without scrolling: the whole dialog fits its area height.
	if l.h > m.paneRects().area.H {
		t.Fatalf("confirm height %d overflows the area", l.h)
	}
	view := confirmView(m)
	for _, want := range []string{"new@example.com", "$1 = 'new@example.com'", "main.users", "Run anyway"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the 80-column confirm view is missing %q:\n%s", want, view)
		}
	}
}
