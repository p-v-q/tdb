package ui

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/textbuf"
)

// clickMenuTitle clicks the idx-th title on the menu bar.
func clickMenuTitle(m Model, idx int) Model {
	r := m.paneRects()
	titles := m.menuTitles(r.menubar)
	if idx >= len(titles) {
		panic("menu index out of range")
	}
	x, y := titles[idx].x+1, r.menubar.Y
	m = send(m, mousePress(x, y))
	return send(m, mouseRelease(x, y))
}

// clickMenuItem clicks the idx-th item of the currently open dropdown.
func clickMenuItem(m Model, idx int) Model {
	dr, ok := m.menuDropdownScreenRect(m.paneRects().menubar)
	if !ok {
		panic("menu is not open")
	}
	x, y := dr.X+2, dr.Y+1+idx
	m = send(m, mousePress(x, y))
	return send(m, mouseRelease(x, y))
}

func TestMenuBarRenders(t *testing.T) {
	m := sized("")
	view := m.View()
	for _, def := range menuDefs {
		if !strings.Contains(view, m.t(def.titleKey)) {
			t.Fatalf("the menu bar is missing %q:\n%s", m.t(def.titleKey), view)
		}
	}
	// The menu bar is the first row and starts with the application name,
	// macOS style.
	lines := strings.Split(view, "\n")
	if !strings.Contains(lines[0], m.t(i18n.KeyAppName)) {
		t.Fatalf("the menu bar must start with the app name: %q", lines[0])
	}
	if !strings.Contains(lines[0], "File") {
		t.Fatalf("the menu bar must carry the menus: %q", lines[0])
	}
	if m.menu.open != -1 {
		t.Fatal("the menu must start closed")
	}
}

func TestClickMenuTitleOpensDropdown(t *testing.T) {
	m := sized("")
	// The View menu (index 3, after the application menu).
	m = clickMenuTitle(m, 3)
	if m.menu.open != 3 {
		t.Fatalf("menu.open = %d, want 3", m.menu.open)
	}
	view := m.View()
	for _, want := range []string{"Objects Pane", "Results Pane", "Close Focused Pane"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the dropdown is missing %q:\n%s", want, view)
		}
	}
	// Clicking the same title again collapses the menu.
	m = clickMenuTitle(m, 3)
	if m.menu.open != -1 {
		t.Fatalf("a second click must collapse the menu, open = %d", m.menu.open)
	}
}

func TestMenuSwitchByTitleClick(t *testing.T) {
	m := sized("")
	m = clickMenuTitle(m, 1) // File
	m = clickMenuTitle(m, 4) // switch to Query
	if m.menu.open != 4 {
		t.Fatalf("menu.open = %d, want 4", m.menu.open)
	}
	if m.menu.sel != 0 {
		t.Fatalf("the selected item must reset when switching menus, sel = %d", m.menu.sel)
	}
}

func TestMenuItemClickTogglesPane(t *testing.T) {
	m := sized("")
	m = clickMenuTitle(m, 3) // View
	m = clickMenuItem(m, 1)  // Results Pane
	if m.layout.resultsVisible {
		t.Fatal("the menu item must hide the results pane")
	}
	if m.menu.open != -1 {
		t.Fatal("clicking a menu item must close the menu")
	}
	// Running it again must restore the pane.
	m = clickMenuTitle(m, 3)
	m = clickMenuItem(m, 1)
	if !m.layout.resultsVisible {
		t.Fatal("running it again must restore the results pane")
	}
}

func TestMenuItemClosePane(t *testing.T) {
	m := sized("")
	m.layout.focus = paneObjects
	m = clickMenuTitle(m, 3)
	m = clickMenuItem(m, 7) // Close Focused Pane
	if m.layout.objectsVisible {
		t.Fatal("it must close the focused pane (Objects)")
	}
}

func TestMenuCopyWithoutSelectionShowsMessage(t *testing.T) {
	m := sized("SELECT 1")
	m = clickMenuTitle(m, 2) // Edit
	m = clickMenuItem(m, 4)  // Copy
	if !strings.Contains(m.status.message, "Nothing selected") {
		t.Fatalf("status = %q", m.status.message)
	}
}

func TestMenuCopyWithSelection(t *testing.T) {
	buf := captureClipboard(t)
	m := sized("SELECT * FROM users")
	m.sel.anchor = &textbuf.Pos{Row: 0, Col: 7}
	m.buf.SetCursor(textbuf.Pos{Row: 0, Col: 13})
	// Open the Edit menu and click Copy.
	titles := m.menuTitles(m.paneRects().menubar)
	m, _ = sendCmd(m, mousePress(titles[2].x+1, m.paneRects().menubar.Y)) // Edit
	if m.menu.open != 2 {
		t.Fatalf("menu.open = %d", m.menu.open)
	}
	dr, _ := m.menuDropdownScreenRect(m.paneRects().menubar)
	m, cmd := sendCmd(m, mousePress(dr.X+2, dr.Y+1+4)) // Copy
	if cmd != nil {
		msg := cmd()
		_ = msg
	}
	if !strings.Contains(buf.String(), "\x1b]52;c;") {
		t.Fatalf("the OSC52 clipboard must be written: %q", buf.String())
	}
}

func TestClickOutsideClosesMenuAndSwallows(t *testing.T) {
	m := sized("SELECT 1")
	m = clickMenuTitle(m, 0)
	// Click the editor area: the menu closes but the caret must not move.
	x, y := editorX(m, 3), editorRowY(m, 0)
	m = send(m, mousePress(x, y))
	if m.menu.open != -1 {
		t.Fatal("clicking outside the menu must close it")
	}
	if c := m.buf.Cursor(); c.Col != 0 {
		t.Fatalf("the click must be swallowed by the menu, cursor = %+v", c)
	}
	// A second click goes through.
	m = send(m, mousePress(x, y))
	m = send(m, mouseRelease(x, y))
	if c := m.buf.Cursor(); c.Col != 3 {
		t.Fatalf("cursor = %+v, want col 3", c)
	}
}

// ---- Keyboard ----

func TestMenuKeyboardNavigation(t *testing.T) {
	m := sized("")
	m = send(m, tea.KeyMsg{Type: tea.KeyF10}) // F10 opens the first menu
	if m.menu.open != 0 {
		t.Fatalf("F10 must open the File menu, open = %d", m.menu.open)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.menu.sel != 1 {
		t.Fatalf("sel = %d, want 1", m.menu.sel)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.menu.sel != 0 {
		t.Fatalf("sel = %d, want 0", m.menu.sel)
	}
	// Left/right cycle through the menus.
	m = send(m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.menu.open != len(menuDefs)-1 {
		t.Fatalf("moving left must wrap to the last menu, open = %d", m.menu.open)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.menu.open != 0 {
		t.Fatalf("moving right must wrap back to the first menu, open = %d", m.menu.open)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.menu.open != -1 {
		t.Fatal("Esc must close the menu")
	}
}

func TestMenuKeyboardActivatesAction(t *testing.T) {
	m := sized("")
	m = send(m, tea.KeyMsg{Type: tea.KeyF10})
	// The application menu → move down to "Quit tdb", Enter.
	for i := 0; i < 2; i++ {
		m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	m, cmd := sendCmd(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.quitting {
		t.Fatal("running Quit must set the quitting state")
	}
	if cmd == nil {
		t.Fatal("Quit must return a tea.Quit command")
	}
}

func TestMenuKeyboardViewToggle(t *testing.T) {
	m := sized("")
	// Alt+V opens the View menu → the first item "Objects Pane" → Enter.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}, Alt: true})
	if m.menu.open != 3 {
		t.Fatalf("Alt+V must open the View menu, open = %d", m.menu.open)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.layout.objectsVisible {
		t.Fatal("Enter must run the Objects Pane toggle")
	}
}

func TestMenuTypingClosesMenuAndSwallows(t *testing.T) {
	m := sized("")
	m = clickMenuTitle(m, 0)
	m = typeText(m, "S")
	if m.menu.open != -1 {
		t.Fatal("typing a plain character must close the menu")
	}
	// The printed letter that dismissed the menu is consumed, so it must not
	// leak into the document.
	if got := m.buf.Text(); got != "" {
		t.Fatalf("the dismissing character must be swallowed, got %q", got)
	}
}

func TestMenuUndoRedo(t *testing.T) {
	m := sized("")
	m = typeText(m, "abc")
	m = clickMenuTitle(m, 2) // Edit
	m = clickMenuItem(m, 0)  // Undo
	if got := m.buf.Text(); got != "" {
		t.Fatalf("after undo = %q", got)
	}
	m = clickMenuTitle(m, 2)
	m = clickMenuItem(m, 1) // Redo
	if got := m.buf.Text(); got != "abc" {
		t.Fatalf("after redo = %q", got)
	}
}

func TestMenuHelpOpensKeyBindings(t *testing.T) {
	m := sized("")
	m = clickMenuTitle(m, 6) // Help
	m = clickMenuItem(m, 0)
	if !m.keysDlg.open {
		t.Fatal("the Help menu must open the Key Bindings overlay")
	}
	if !strings.Contains(stripANSI(m.View()), m.t(i18n.KeyKeysTitle)) {
		t.Fatalf("the overlay is missing its title:\n%s", stripANSI(m.View()))
	}
}

// ---- Focus shortcuts ----

// viewMenuDef returns the View menu definition.
func viewMenuDef(t *testing.T) menuDef {
	t.Helper()
	for _, def := range menuDefs {
		if def.titleKey == i18n.KeyMenuView {
			return def
		}
	}
	t.Fatal("the View menu is missing")
	return menuDef{}
}

func TestMenuFocusShortcuts(t *testing.T) {
	cases := []struct {
		a        menuAction
		shortcut string
	}{
		{actFocusObjects, "alt+1"},
		{actFocusEditor, "alt+2"},
		{actFocusResults, "alt+3"},
	}
	for _, c := range cases {
		if got := shortcutOf(c.a); got != c.shortcut {
			t.Errorf("shortcutOf(%v) = %q, want %q", c.a, got, c.shortcut)
		}
		if got, ok := keymapAction(c.shortcut); !ok || got != c.a {
			t.Errorf("keymapAction(%q) = (%v, %v), want %v", c.shortcut, got, ok, c.a)
		}
		if got := displayShortcut(c.shortcut); got != "Alt+"+c.shortcut[len("alt+"):] {
			t.Errorf("displayShortcut(%q) = %q", c.shortcut, got)
		}
	}
}

func TestMenuFocusItemsSitNextToPaneToggles(t *testing.T) {
	def := viewMenuDef(t)
	// The focus items sit right behind the pane toggles they complement.
	last := -1
	for i, it := range def.items {
		if it.action == actToggleObjects || it.action == actToggleResults {
			last = i
		}
	}
	if last < 0 {
		t.Fatal("the View menu must carry the pane toggles")
	}
	want := []menuAction{actFocusObjects, actFocusEditor, actFocusResults}
	for k, a := range want {
		idx := last + 1 + k
		if idx >= len(def.items) || def.items[idx].action != a {
			got := actNone
			if idx < len(def.items) {
				got = def.items[idx].action
			}
			t.Fatalf("View item %d = %v, want %v", idx, got, a)
		}
	}
	// Mnemonics must stay unique inside the dropdown.
	seen := map[rune]bool{}
	for _, it := range def.items {
		if seen[it.accel] {
			t.Fatalf("accelerator %q is bound twice in the View menu", it.accel)
		}
		seen[it.accel] = true
	}
}

func TestMenuFocusItemsRenderWithShortcuts(t *testing.T) {
	m := sized("")
	m = clickMenuTitle(m, 3) // View
	view := m.View()
	for _, want := range []string{
		m.t(i18n.KeyMenuFocusObjects), m.t(i18n.KeyMenuFocusEditor), m.t(i18n.KeyMenuFocusResults),
		"Alt+1", "Alt+2", "Alt+3",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("the View dropdown is missing %q:\n%s", want, view)
		}
	}
}

func TestAltFocusSwitchesPane(t *testing.T) {
	m := sized("")
	if m.layout.focus != paneEditor {
		t.Fatalf("focus = %v, want the editor", m.layout.focus)
	}
	altDigit := func(digit rune) Model {
		return send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{digit}, Alt: true})
	}

	// Alt+1 → objects, Alt+3 → results, Alt+2 → back to the editor.
	m = altDigit('1')
	if m.layout.focus != paneObjects {
		t.Fatalf("Alt+1 focus = %v, want the objects pane", m.layout.focus)
	}
	m = altDigit('3')
	if m.layout.focus != paneResults {
		t.Fatalf("Alt+3 focus = %v, want the results pane", m.layout.focus)
	}
	// Alt+2 hands the focus from the object tree back to the editor.
	m = altDigit('1')
	m = altDigit('2')
	if m.layout.focus != paneEditor {
		t.Fatalf("Alt+2 focus = %v, want the editor", m.layout.focus)
	}
	// A repeated press on an already visible pane only moves the focus.
	m = altDigit('1')
	m = altDigit('1')
	if m.layout.focus != paneObjects || !m.layout.objectsVisible {
		t.Fatalf("focus = %v, objectsVisible = %v", m.layout.focus, m.layout.objectsVisible)
	}
}

func TestAltFocusRevealsHiddenPane(t *testing.T) {
	// A hidden objects pane must be shown first, then focused.
	m := sized("")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlB}) // hide the objects pane
	if m.layout.objectsVisible {
		t.Fatal("Ctrl+B must hide the objects pane")
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}, Alt: true})
	if !m.layout.objectsVisible {
		t.Fatal("Alt+1 must reveal the objects pane")
	}
	if m.layout.focus != paneObjects {
		t.Fatalf("focus = %v, want the objects pane", m.layout.focus)
	}

	// Same for a hidden results pane.
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlE}) // hide the results pane
	if m.layout.resultsVisible {
		t.Fatal("Ctrl+E must hide the results pane")
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}, Alt: true})
	if !m.layout.resultsVisible {
		t.Fatal("Alt+3 must reveal the results pane")
	}
	if m.layout.focus != paneResults {
		t.Fatalf("focus = %v, want the results pane", m.layout.focus)
	}
}

func TestMenuAltDigitRunsFocusShortcut(t *testing.T) {
	// A menu open must not swallow Alt+1/2/3: the key closes the menu and
	// runs the focus action in one press.
	m := sized("")
	m = clickMenuTitle(m, 3) // View
	if m.menu.open != 3 {
		t.Fatalf("menu.open = %d, want 3", m.menu.open)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}, Alt: true})
	if m.menu.open != -1 {
		t.Fatalf("Alt+1 must close the open menu, open = %d", m.menu.open)
	}
	if m.layout.focus != paneObjects {
		t.Fatalf("focus = %v, want the objects pane", m.layout.focus)
	}
	if m.objects.sel != 0 {
		t.Fatalf("sel = %d, want 0 — the tree must be ready for the arrow keys", m.objects.sel)
	}

	// Same for the other digits: menu open, one press closes and focuses.
	m = clickMenuTitle(m, 3)
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}, Alt: true})
	if m.menu.open != -1 {
		t.Fatalf("Alt+3 must close the open menu, open = %d", m.menu.open)
	}
	if m.layout.focus != paneResults {
		t.Fatalf("focus = %v, want the results pane", m.layout.focus)
	}

	m = clickMenuTitle(m, 3)
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}, Alt: true})
	if m.menu.open != -1 {
		t.Fatalf("Alt+2 must close the open menu, open = %d", m.menu.open)
	}
	if m.layout.focus != paneEditor {
		t.Fatalf("focus = %v, want the editor", m.layout.focus)
	}
}

func TestMenuAltLetterStillSwitchesMenu(t *testing.T) {
	// The global-shortcut lookup must not steal the Alt+letter menu keys.
	m := sized("")
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}, Alt: true})
	if m.menu.open != 3 {
		t.Fatalf("Alt+V must open the View menu, open = %d", m.menu.open)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}, Alt: true})
	if m.menu.open != 1 {
		t.Fatalf("Alt+F must switch to the File menu, open = %d", m.menu.open)
	}
	// Alt+<menu letter> on the menu that is already open closes it.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}, Alt: true})
	if m.menu.open != -1 {
		t.Fatalf("a repeated Alt+F must close the File menu, open = %d", m.menu.open)
	}
}

// ---- Mnemonics and hover ----

// ansiRE matches the SGR sequences lipgloss emits.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// stripANSI removes SGR sequences so a test can compare the plain text.
func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

func TestMenuMnemonicUnderlinesAccelerator(t *testing.T) {
	// Lip Gloss emits no ANSI without a TTY, so force colors to see underlines
	// and restore the plain profile afterwards.
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	got := labelWithMnemonic("Select All", 's', lipgloss.NewStyle())
	if !strings.Contains(got, "\x1b[4") {
		t.Fatalf("the accelerator letter must be underlined, got %q", got)
	}
	if plain := stripANSI(got); plain != "Select All" {
		t.Fatalf("the label must be unchanged, got %q", plain)
	}
	if lipgloss.Width(got) != lipgloss.Width("Select All") {
		t.Fatalf("underlining must not change the label width")
	}

	// A translation may not contain the accelerator letter at all (Chinese
	// labels do not); those are left alone instead of underlined at random.
	plain := labelWithMnemonic("显示配置文件位置", 's', lipgloss.NewStyle())
	if strings.Contains(plain, "\x1b[4") {
		t.Fatalf("labels without the accelerator must not be underlined, got %q", plain)
	}
}

func TestMenuDropdownUnderlinesMnemonics(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	m := sized("")
	m = send(m, tea.KeyMsg{Type: tea.KeyF10}) // File
	if m.menu.open != 0 {
		t.Fatalf("F10 must open the first menu, open = %d", m.menu.open)
	}
	block := strings.Join(m.renderMenuDropdown(m.paneRects().menubar), "\n")
	for i, it := range menuDefs[m.menu.open].items {
		// The selected row is drawn in the highlight style.
		style := m.theme.menuBox
		if i == m.menu.sel {
			style = m.theme.menuItemSel
		}
		want := labelWithMnemonic(m.t(it.labelKey), it.accel, style)
		if !strings.Contains(block, want) {
			t.Fatalf("the dropdown must render %q with its mnemonic underlined:\n%s",
				m.t(it.labelKey), block)
		}
	}
}

func TestMenuHoverSwitchesMenus(t *testing.T) {
	m := sized("")
	m = send(m, tea.KeyMsg{Type: tea.KeyF10}) // File
	if m.menu.open != 0 {
		t.Fatalf("F10 must open the first menu, open = %d", m.menu.open)
	}
	r := m.paneRects()
	titles := m.menuTitles(r.menubar)
	if len(titles) < 3 {
		t.Fatalf("menu titles = %+v", titles)
	}
	// Sliding onto another title switches to its menu, without a second click.
	target := titles[2] // View
	m = send(m, mouseMotion(target.x+1, r.menubar.Y))
	if m.menu.open != 2 {
		t.Fatalf("hovering a menu title must switch to it, open = %d", m.menu.open)
	}
	// Sliding over the dropdown highlights its items.
	dr, ok := m.menuDropdownScreenRect(m.paneRects().menubar)
	if !ok {
		t.Fatal("the dropdown must be open")
	}
	m = send(m, mouseMotion(dr.X+2, dr.Y+2)) // the second item
	if m.menu.sel != 1 {
		t.Fatalf("hovering a dropdown item must select it, sel = %d", m.menu.sel)
	}
}

func TestMenuTitleHoverWhenClosed(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	m := sized("")
	r := m.paneRects()
	titles := m.menuTitles(r.menubar)
	if len(titles) < 4 {
		t.Fatalf("menu titles = %+v", titles)
	}
	target := titles[3] // View
	m = send(m, mouseMotion(target.x+1, r.menubar.Y))
	if m.menu.hover != target.index {
		t.Fatalf("hover = %d, want %d", m.menu.hover, target.index)
	}
	bar := strings.Join(m.renderMenuBar(r.menubar), "\n")
	want := m.theme.menuHover.Render(" " + m.t(menuDefs[target.index].titleKey) + " ")
	if !strings.Contains(bar, want) {
		t.Fatalf("the hovered title must use the hover style:\n%s", bar)
	}

	// Moving off the bar clears the highlight.
	m = send(m, mouseMotion(target.x+1, r.menubar.Y+menuBarHeight+toolbarHeight))
	if m.menu.hover != -1 {
		t.Fatalf("hover off the bar = %d, want -1", m.menu.hover)
	}
}
