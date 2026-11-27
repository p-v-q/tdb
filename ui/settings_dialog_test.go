package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
)

// settingsClick sends a left-button press at a screen coordinate.
func settingsClick(m Model, x, y int) Model {
	return send(m, tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
}

// openSettingsDialog returns a model with the dialog open on a sized screen.
func openSettingsDialog(t *testing.T) Model {
	t.Helper()
	m := newTestModel("SELECT 1")
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = keyByName(m, "f2")
	if !m.dlg.open {
		t.Fatal("F2 must open the settings dialog")
	}
	return m
}

func TestF2OpensSettingsDialog(t *testing.T) {
	m := openSettingsDialog(t)
	view := m.View()
	if !strings.Contains(view, "Settings") {
		t.Fatalf("view must contain the dialog title:\n%s", view)
	}
	if !strings.Contains(view, "Language") || !strings.Contains(view, "Theme") {
		t.Fatalf("view must contain the dialog rows:\n%s", view)
	}
	// The dialog is a bordered box drawn on top of the panes.
	if !strings.Contains(view, "┌") {
		t.Fatalf("view must contain the dialog border:\n%s", view)
	}
}

func TestSettingsDialogLanguageCycle(t *testing.T) {
	m := openSettingsDialog(t)

	m = keyByName(m, "right")
	if m.settings.Locale != string(i18n.En) {
		t.Fatalf("Locale = %q, want %q", m.settings.Locale, i18n.En)
	}
	if m.translator.Locale() != i18n.En {
		t.Fatalf("translator locale = %q", m.translator.Locale())
	}

	m = keyByName(m, "right")
	if m.settings.Locale != string(i18n.ZhHans) {
		t.Fatalf("Locale = %q, want %q", m.settings.Locale, i18n.ZhHans)
	}
	// The whole interface, dialog included, switches language immediately.
	view := m.View()
	for _, want := range []string{"语言", "设置", "对象", "SQL 编辑器"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the Chinese UI is missing %q:\n%s", want, view)
		}
	}

	// Cycling all the way round returns to "follow the system".
	m = keyByName(m, "right")
	if m.settings.Locale != "" {
		t.Fatalf("Locale = %q, want the empty (follow system) choice", m.settings.Locale)
	}

	// Going backwards wraps the other way.
	m = keyByName(m, "left")
	if m.settings.Locale != string(i18n.ZhHans) {
		t.Fatalf("Locale after cycling left = %q, want %q", m.settings.Locale, i18n.ZhHans)
	}
}

func TestSettingsDialogThemeChange(t *testing.T) {
	m := openSettingsDialog(t)
	if m.theme.palette.name != "dark" {
		t.Fatalf("default palette = %q", m.theme.palette.name)
	}
	m = selectSettingRow(t, m, i18n.KeySettingsTheme)
	// Enter opens the dropdown on the current value; moving down reaches the
	// light theme and a second Enter picks and applies it at once.
	m = keyByName(m, "enter")
	if !m.dlg.choice.open {
		t.Fatal("enter must open the theme dropdown")
	}
	m = keyByName(m, "down")
	m = keyByName(m, "enter")
	if m.settings.Theme != settings.ThemeLight {
		t.Fatalf("Theme = %q, want %q", m.settings.Theme, settings.ThemeLight)
	}
	if m.theme.palette.name != "light" {
		t.Fatalf("palette after the change = %q, want light", m.theme.palette.name)
	}
	// Other keys do not leak into any row while the list is open.
	m = keyByName(m, "enter")
	if !m.dlg.choice.open {
		t.Fatal("enter must open the theme dropdown again")
	}
	m = keyByName(m, "esc")
	if m.dlg.choice.open {
		t.Fatal("esc must close only the list")
	}
	if !m.dlg.open {
		t.Fatal("esc on the list must keep the dialog open")
	} else if m.settings.Theme != settings.ThemeLight {
		t.Fatalf("esc on the list must not change the value, got %q", m.settings.Theme)
	}
}

// selectSettingRow focuses a settings row by its label key, so tests do not
// depend on the order of the rows.
func selectSettingRow(t *testing.T, m Model, key i18n.Key) Model {
	t.Helper()
	for i, row := range settingRows {
		if row.labelKey == key {
			m.dlg.sel = i
			return m
		}
	}
	t.Fatalf("no settings row %v", key)
	return m
}

func TestSettingsDialogTogglesMouseAndGutter(t *testing.T) {
	m := openSettingsDialog(t)

	m = selectSettingRow(t, m, i18n.KeySettingsMouse)
	m = keyByName(m, "space")
	if m.settings.Mouse {
		t.Fatal("the mouse row must toggle the mouse off")
	}
	// With the mouse off, mouse events are ignored.
	before := m.buf.Cursor()
	m = settingsClick(m, m.paneRects().editor.X+5, m.paneRects().editor.Y+1)
	if m.buf.Cursor() != before {
		t.Fatal("mouse events must be ignored while the mouse setting is off")
	}
	m = keyByName(m, "space")
	if !m.settings.Mouse {
		t.Fatal("the mouse row must toggle the mouse back on")
	}

	m = selectSettingRow(t, m, i18n.KeySettingsGutter)
	m = keyByName(m, "space")
	if m.settings.Gutter {
		t.Fatal("the gutter row must toggle the line numbers off")
	}
	if got := m.gutterWidth(); got != 0 {
		t.Fatalf("gutterWidth = %d, want 0 while the gutter is off", got)
	}
	if m.editorWidth() != m.paneRects().editor.W {
		t.Fatalf("the editor must reclaim the gutter width: %d vs %d",
			m.editorWidth(), m.paneRects().editor.W)
	}
	m = keyByName(m, "space")
	if !m.settings.Gutter || m.gutterWidth() == 0 {
		t.Fatal("the gutter row must toggle the line numbers back on")
	}
}

func TestSettingsDialogResetLayout(t *testing.T) {
	m := openSettingsDialog(t)
	// Change the layout first: hide the objects pane and widen it.
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlB})
	m.layout.objectsWidth = 40
	m = keyByName(m, "esc")

	m = keyByName(m, "f2")
	for i := 0; i < len(settingRows)-1; i++ {
		m = keyByName(m, "tab") // Tab rides over the select rows to the reset row
	}
	if m.dlg.sel != len(settingRows)-1 {
		t.Fatalf("sel = %d, want the reset row", m.dlg.sel)
	}
	m = keyByName(m, "enter")

	want := settings.Default().Layout
	if !m.layout.objectsVisible {
		t.Fatal("reset must restore the objects pane")
	}
	if m.layout.objectsWidth != want.ObjectsWidth {
		t.Fatalf("ObjectsWidth = %d, want %d", m.layout.objectsWidth, want.ObjectsWidth)
	}
	if m.settings.Layout != want {
		t.Fatalf("settings layout = %+v, want %+v", m.settings.Layout, want)
	}
	if !strings.Contains(m.status.message, "restored") {
		t.Fatalf("status = %q", m.status.message)
	}
}

func TestSettingsDialogIsModal(t *testing.T) {
	m := openSettingsDialog(t)
	before := m.buf.Text()
	m = typeText(m, "x")
	if m.buf.Text() != before {
		t.Fatalf("typing while the dialog is open must not reach the editor: %q", m.buf.Text())
	}
	m = keyByName(m, "esc")
	if m.dlg.open {
		t.Fatal("esc must close the dialog")
	}
}

func TestSettingsDialogMouseSelectsAndChanges(t *testing.T) {
	m := openSettingsDialog(t)
	l := m.dialogLayout()

	// Clicking the label of the theme row selects it without changing it.
	m = settingsClick(m, l.x+2, l.rowY+1)
	if m.dlg.sel != 1 {
		t.Fatalf("sel = %d, want 1", m.dlg.sel)
	}
	if m.settings.Theme != settings.ThemeDark {
		t.Fatalf("clicking the label must not change the value, got %q", m.settings.Theme)
	}

	// Clicking the value column opens the dropdown, like Enter does.
	m = settingsClick(m, l.valueX+2, l.rowY+1)
	if !m.dlg.choice.open {
		t.Fatal("clicking the value of a select row must open the dropdown")
	}
	// Clicking an option picks it and applies it on the spot.
	cl, ok := m.settingsChoiceLayout()
	if !ok {
		t.Fatal("the theme dropdown must have a layout")
	}
	// The second option of the theme select is "light".
	lightY := cl.rect.Y + 2
	m = settingsClick(m, cl.rect.X+2, lightY)
	if m.dlg.choice.open {
		t.Fatal("picking an option must close the dropdown")
	}
	if m.settings.Theme != settings.ThemeLight {
		t.Fatalf("Theme = %q, want %q", m.settings.Theme, settings.ThemeLight)
	}
}

func TestSettingsDialogClickOutsideCloses(t *testing.T) {
	m := openSettingsDialog(t)
	l := m.dialogLayout()
	m = settingsClick(m, l.x+l.w+3, l.y+l.h+3)
	if m.dlg.open {
		t.Fatal("clicking outside the dialog must close it")
	}
}

func TestSettingsDialogClickInsideDoesNotClose(t *testing.T) {
	m := openSettingsDialog(t)
	l := m.dialogLayout()
	// The title row, the description row and the hint row are inside the box
	// but are not settings rows: clicking them must be swallowed, not close.
	for _, y := range []int{l.y + 1, l.y + l.h - 3, l.y + l.h - 2} {
		m = settingsClick(m, l.x+3, y)
		if !m.dlg.open {
			t.Fatalf("clicking inside the dialog at y=%d closed it", y)
		}
	}
}

func TestSettingsDialogPersistsChanges(t *testing.T) {
	path := t.TempDir() + "/settings.json"
	m := newTestModel("SELECT 1")
	m.settingsPath = path
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	// press sends a key and returns the command the UI produced for it, so the
	// test can check that every change is wired to persistence. The command is
	// not run here: it is a batch that also holds the status message timer.
	press := func(key tea.KeyMsg) tea.Cmd {
		next, cmd := m.Update(key)
		m = next.(Model)
		return cmd
	}

	if cmd := press(tea.KeyMsg{Type: tea.KeyF2}); cmd != nil {
		t.Fatal("opening the dialog must not change anything")
	}
	if cmd := press(tea.KeyMsg{Type: tea.KeyRight}); cmd == nil { // language: follow system -> en (cycle fallback)
		t.Fatal("changing the language must persist it")
	}
	// Tab moves off the language row without opening its list.
	press(tea.KeyMsg{Type: tea.KeyTab})
	if m.dlg.sel != 1 {
		t.Fatalf("sel = %d, want the theme row", m.dlg.sel)
	}
	// The theme select: Enter opens the list on the current value, ↓ reaches
	// the light theme and a second Enter applies and persists it.
	if cmd := press(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Fatal("opening the list must not change anything")
	}
	if !m.dlg.choice.open {
		t.Fatal("enter must open the theme dropdown")
	}
	press(tea.KeyMsg{Type: tea.KeyDown})
	if m.dlg.choice.sel != 1 {
		t.Fatalf("choice.sel = %d, want the light entry", m.dlg.choice.sel)
	}
	if cmd := press(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
		t.Fatal("picking a theme must persist it")
	}
	if m.dlg.choice.open {
		t.Fatal("picking a theme must close the list")
	}
	if m.settings.Theme != settings.ThemeLight {
		t.Fatalf("Theme = %q, want %q", m.settings.Theme, settings.ThemeLight)
	}

	// Write the current settings (the same command the UI batches) and check
	// what lands on disk.
	if cmd := m.persistSettings(); cmd != nil {
		cmd()
	}

	got, err := settings.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Locale != string(i18n.En) {
		t.Fatalf("persisted Locale = %q, want %q", got.Locale, i18n.En)
	}
	if got.Theme != settings.ThemeLight {
		t.Fatalf("persisted Theme = %q, want %q", got.Theme, settings.ThemeLight)
	}
}

func TestSettingsDialogArrowKeysAndEnter(t *testing.T) {
	m := openSettingsDialog(t)
	// Enter on a select row opens the list instead of changing the value.
	m = keyByName(m, "enter")
	if !m.dlg.choice.open {
		t.Fatal("enter on the language row must open its dropdown")
	}
	// ↑↓ move the highlight, and picking applies immediately: two downs
	// from the current value ("follow the system") reach the last entry,
	// Chinese, and Enter applies it. Picking the empty "follow the system"
	// entry itself is pinned by TestSettingsDialogKeyboardPicksTheEmptyLocale,
	// which arrives at the list with a non-empty locale.
	m = keyByName(m, "down")
	m = keyByName(m, "down")
	m = keyByName(m, "enter")
	if m.dlg.choice.open {
		t.Fatal("picking must close the list")
	}
	if m.settings.Locale != string(i18n.ZhHans) {
		t.Fatalf("Locale = %q, want %q", m.settings.Locale, i18n.ZhHans)
	}
	// Esc closed the dialog-level list only; the dialog is still open and
	// shows the freshly applied value.
	if !m.dlg.open {
		t.Fatal("the dialog must stay open")
	}
	// ↑↓ still wrap around the row list while no list is open.
	m = keyByName(m, "up")
	if m.dlg.sel != len(settingRows)-1 {
		t.Fatalf("sel = %d, want the last row after wrapping up", m.dlg.sel)
	}
	m = keyByName(m, "down")
	if m.dlg.sel != 0 {
		t.Fatalf("sel = %d, want the first row after wrapping down", m.dlg.sel)
	}
}

// TestSettingsDialogKeyboardPicksTheEmptyLocale pins the empty-value pick:
// the "follow the system" language entry carries "" as its value, and once a
// concrete locale was set, the keyboard must be able to pick it back. Picking
// it is distinguished from dismissing the list by the dropdown's picked flag,
// not by the chosen value — before that flag an empty chosen meant "Esc", so
// the keyboard could never return to "follow the system" while the mouse
// could.
func TestSettingsDialogKeyboardPicksTheEmptyLocale(t *testing.T) {
	path := t.TempDir() + "/settings.json"
	m := newTestModel("SELECT 1")
	m.settingsPath = path
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = keyByName(m, "f2")
	if !m.dlg.open {
		t.Fatal("F2 must open the settings dialog")
	}
	// The cycle fallback moves the language from "follow the system" to
	// English, so the list opens with a non-empty current value.
	m = keyByName(m, "right")
	if m.settings.Locale != string(i18n.En) {
		t.Fatalf("Locale = %q, want %q", m.settings.Locale, i18n.En)
	}
	// Enter opens the language list on the current value (English, index 1);
	// Home moves to the "follow the system" entry and Enter picks it.
	m = keyByName(m, "enter")
	if !m.dlg.choice.open {
		t.Fatal("enter on the language row must open its dropdown")
	}
	if m.dlg.choice.sel != 1 {
		t.Fatalf("the list must open on the current value, sel = %d", m.dlg.choice.sel)
	}
	m = keyByName(m, "home")
	m = keyByName(m, "enter")
	if m.dlg.choice.open {
		t.Fatal("picking must close the list")
	}
	if m.settings.Locale != "" {
		t.Fatalf("Locale = %q, want %q (an empty value is a real pick, not a dismissal)",
			m.settings.Locale, "")
	}
	// The pick is persisted like any other value.
	if cmd := m.persistSettings(); cmd != nil {
		cmd()
	}
	got, err := settings.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Locale != "" {
		t.Fatalf("persisted Locale = %q, want the empty (follow system) value", got.Locale)
	}
}

// TestSettingsDropdownSwallowsKeys pins that the open dropdown is modal
// inside the dialog: no key reaches the rows behind it.
func TestSettingsDropdownSwallowsKeys(t *testing.T) {
	m := openSettingsDialog(t)
	m.dlg.choice.Open(m.localeOptions(), "")

	before := m.settings.Icons
	beforeSel := m.dlg.sel
	m = typeText(m, "x")
	if !m.dlg.choice.open {
		t.Fatal("typing must not close the dropdown")
	}
	if m.settings.Icons != before {
		t.Fatal("typing must not toggle a row behind the dropdown")
	}
	if m.dlg.sel != beforeSel {
		t.Fatal("typing must not move the row selection behind the dropdown")
	}
}

func TestMenuPreferencesOpensSettings(t *testing.T) {
	m := newTestModel("SELECT 1")
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	// Alt+S opens the Settings menu, "p" activates the Preferences item.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s"), Alt: true})
	if m.menu.open != 5 {
		t.Fatalf("menu.open = %d, want the Settings menu (5)", m.menu.open)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	if !m.dlg.open {
		t.Fatal("the Preferences item must open the settings dialog")
	}
	if m.menu.open != -1 {
		t.Fatal("opening the dialog must close the menu")
	}
}

func TestMenuAltAcceleratorsOpenMenus(t *testing.T) {
	cases := map[string]int{"f": 1, "e": 2, "v": 3, "q": 4, "s": 5, "h": 6}
	for letter, want := range cases {
		m := newTestModel("SELECT 1")
		m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(letter), Alt: true})
		if m.menu.open != want {
			t.Errorf("alt+%s opened menu %d, want %d", letter, m.menu.open, want)
		}
	}
}

func TestMenuLetterAcceleratorRunsItem(t *testing.T) {
	m := newTestModel("SELECT 1")
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v"), Alt: true}) // View menu
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})            // Objects Pane
	if m.layout.objectsVisible {
		t.Fatal("the leading letter of a menu item must activate it")
	}
	if m.menu.open != -1 {
		t.Fatal("activating an item must close the menu")
	}
}

// TestSettingsDropdownHoverHighlightsWithoutPicking pins the pointer
// semantics of the settings dropdowns (round 7 §1): motion highlights the
// option under the pointer without moving sel and without applying anything,
// a motion outside the dialog is swallowed by the modal, and only a press
// picks the option.
func TestSettingsDropdownHoverHighlightsWithoutPicking(t *testing.T) {
	// Lip Gloss emits no ANSI without a TTY: force colors so the highlight
	// style is really in the rendered row, then restore the plain profile.
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	m := openSettingsDialog(t)
	m = keyByName(m, "enter") // the language row is selected first
	if !m.dlg.choice.open {
		t.Fatal("enter must open the language dropdown")
	}
	cl, ok := m.settingsChoiceLayout()
	if !ok {
		t.Fatal("the language dropdown must have a layout")
	}
	options := m.dlg.choice.options
	if cl.top != 0 || cl.visible < 3 || len(options) < 3 {
		t.Fatalf("precondition: top = %d, visible = %d, options = %d", cl.top, cl.visible, len(options))
	}
	locale := m.settings.Locale

	// Motion onto the third option highlights it and changes nothing.
	m = send(m, mouseMotion(cl.rect.X+2, cl.rect.Y+1+2))
	if m.dlg.choice.hover != 2 {
		t.Fatalf("choice.hover = %d, want 2 (the third option)", m.dlg.choice.hover)
	}
	if m.dlg.choice.sel != 0 {
		t.Fatalf("choice.sel = %d, hovering must not move the keyboard highlight", m.dlg.choice.sel)
	}
	if m.settings.Locale != locale {
		t.Fatalf("Locale = %q, hovering must not change the value", m.settings.Locale)
	}
	if !m.dlg.choice.open || !m.dlg.open {
		t.Fatal("hovering must leave the dropdown and the dialog open")
	}

	// The hovered row is drawn exactly like a selected one, and differently
	// from a row that is neither selected nor hovered.
	lines := m.renderSelectList(cl, &m.dlg.choice)
	rowIdx := 1 + (m.dlg.choice.hover - cl.top) // out[0] is the top border
	savedSel, savedHover := m.dlg.choice.sel, m.dlg.choice.hover
	m.dlg.choice.sel, m.dlg.choice.hover = savedHover, -1
	selected := m.renderSelectList(cl, &m.dlg.choice)
	m.dlg.choice.sel, m.dlg.choice.hover = savedSel, -1
	plain := m.renderSelectList(cl, &m.dlg.choice)
	m.dlg.choice.sel, m.dlg.choice.hover = savedSel, savedHover
	if lines[rowIdx] != selected[rowIdx] {
		t.Fatalf("the hovered row must be drawn like the selected one:\nhover:   %q\nselect: %q",
			lines[rowIdx], selected[rowIdx])
	}
	if lines[rowIdx] == plain[rowIdx] {
		t.Fatalf("the hovered row must be drawn differently from a plain one: %q", lines[rowIdx])
	}

	// A motion outside the dialog clears the highlight and is swallowed: the
	// modal must not close (only a press outside does that) and the value
	// must not move.
	l := m.dialogLayout()
	m = send(m, mouseMotion(l.x-3, l.y))
	if m.dlg.choice.hover != -1 {
		t.Fatalf("choice.hover = %d, want -1 outside the dialog", m.dlg.choice.hover)
	}
	if !m.dlg.open || !m.dlg.choice.open {
		t.Fatal("a motion outside the dialog must leave it open")
	}
	if m.settings.Locale != locale {
		t.Fatalf("Locale = %q, want it unchanged (%q)", m.settings.Locale, locale)
	}

	// The press contract is unchanged: picking applies the value on the spot.
	m = send(m, mousePress(cl.rect.X+2, cl.rect.Y+1+2))
	if m.dlg.choice.open {
		t.Fatal("pressing an option must close the dropdown")
	}
	if m.settings.Locale == locale {
		t.Fatalf("Locale = %q, pressing the third option must apply it", m.settings.Locale)
	}
}
