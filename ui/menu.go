package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
	"github.com/p-v-q/tdb/textbuf"
)

// menu.go implements menu state, layout, hit testing and action dispatch. The
// command table lives in menu_defs.go, key handling in menu_keys.go and drawing
// in menu_render.go; all three share the same geometry.

type menuState struct {
	open  int // index of the open menu, -1 when closed
	sel   int // selected item inside the open dropdown
	hover int // menu title under the pointer, -1 when none
}

// newMenuState returns a closed menu bar.
func newMenuState() menuState { return menuState{open: -1, hover: -1} }

// menuTitleLayout is the position (screen columns) of one menu title.
type menuTitleLayout struct {
	index int
	x, w  int
}

// menuTitles computes the position of every menu title. Rendering and hit
// testing share this.
func (m Model) menuTitles(rect Rect) []menuTitleLayout {
	out := make([]menuTitleLayout, 0, len(menuDefs))
	x := rect.X
	for i, def := range menuDefs {
		w := lipgloss.Width(m.t(def.titleKey)) + 2 // one space on each side
		if x+w > rect.X+rect.W {
			break
		}
		out = append(out, menuTitleLayout{index: i, x: x, w: w})
		x += w
	}
	return out
}

// menuTitleAt returns the index of the menu title under a screen column.
func (m Model) menuTitleAt(x, y int) (int, bool) {
	r := m.paneRects()
	if !r.menubar.Contains(x, y) {
		return 0, false
	}
	for _, t := range m.menuTitles(r.menubar) {
		if x >= t.x && x < t.x+t.w {
			return t.index, true
		}
	}
	return 0, false
}

// menuDropdownScreenRect returns the on-screen rectangle of the open dropdown.
func (m Model) menuDropdownScreenRect(rect Rect) (Rect, bool) {
	if m.menu.open < 0 || m.menu.open >= len(menuDefs) {
		return Rect{}, false
	}
	titles := m.menuTitles(rect)
	if m.menu.open >= len(titles) {
		return Rect{}, false
	}
	def := menuDefs[m.menu.open]
	w, h := m.menuDropdownSize(def)
	x := clamp(titles[m.menu.open].x, rect.X, max(rect.X, rect.X+rect.W-w))
	y := rect.Y + rect.H
	if y+h > m.height-1 {
		h = max(1, m.height-1-y)
	}
	return Rect{X: x, Y: y, W: w, H: h}, true
}

// menuItems returns the dropdown items of def that apply to the current
// connection. Transaction items need driver.TransactionManager; a connection
// that cannot commit (the demo) never shows them, so rendering, hit testing
// and keyboard navigation always work from the same list. Because needsTx
// items form a suffix of their menu (pinned by a test), every visible index
// equals its index in def.items, which is what keeps mouse.go's dispatch
// through the definition table correct.
func (m Model) menuItems(def menuDef) []menuItem {
	// needsUnlock items hide while the vault is sealed (or missing): the
	// menu must not offer an action that cannot run.
	vaultOpen := m.settings.Encrypted() && !m.settings.Locked()
	if m.supportsTransactions() && vaultOpen {
		return def.items
	}
	out := make([]menuItem, 0, len(def.items))
	for _, it := range def.items {
		if it.needsTx && !m.supportsTransactions() {
			continue
		}
		if it.needsUnlock && !vaultOpen {
			continue
		}
		out = append(out, it)
	}
	return out
}

// menuDropdownInnerWidth is the content width of a dropdown: a leading space,
// the label, the gap and the shortcut.
func (m Model) menuDropdownInnerWidth(def menuDef) int {
	w := 0
	for _, it := range m.menuItems(def) {
		line := 1 + lipgloss.Width(m.t(it.labelKey)) + 2 + lipgloss.Width(displayItemShortcuts(it))
		w = max(w, line)
	}
	return max(w, 12)
}

// menuDropdownSize returns the size of a dropdown, borders included.
func (m Model) menuDropdownSize(def menuDef) (w, h int) {
	return m.menuDropdownInnerWidth(def) + 2, len(m.menuItems(def)) + 2
}

// menuDropdownOverlayRect returns the rectangle the dropdown covers when drawn
// on top of the panes (empty while no menu is open).
func (m Model) menuDropdownOverlayRect() Rect {
	if m.menu.open < 0 {
		return Rect{}
	}
	dr, ok := m.menuDropdownScreenRect(m.paneRects().menubar)
	if !ok {
		return Rect{}
	}
	return dr
}

// openMenu opens (or switches to) a menu.
func (m *Model) openMenu(idx int) {
	if idx < 0 || idx >= len(menuDefs) {
		return
	}
	m.closeObjectsMenu()
	m.closeResultMenu()
	m.menu.open = idx
	m.menu.sel = 0
}

// closeMenu closes the menu bar.
func (m *Model) closeMenu() { m.menu.open = -1 }

// menuItemAt reports whether a coordinate hits an item of the open dropdown.
func (m Model) menuItemAt(x, y int) (int, bool) {
	r := m.paneRects()
	dr, ok := m.menuDropdownScreenRect(r.menubar)
	if !ok || !dr.Contains(x, y) {
		return 0, false
	}
	idx := y - dr.Y - 1 // skip the top padding row
	if idx < 0 || idx >= len(m.menuItems(menuDefs[m.menu.open])) {
		return 0, false
	}
	return idx, true
}

// ---- Action dispatch ----

// actsOnBuffer reports whether an action reads or rewrites the buffer of the
// active tab (its text, its selection, or the query it holds). Every one of
// them runs through revealEditor first, so the user watches the action work on
// SQL that is on screen instead of on text the welcome page hides. Actions
// that never touch the buffer — panes, focus, history, transactions, the
// dialogs — leave the page up.
func actsOnBuffer(a menuAction) bool {
	switch a {
	case actSave, // writes the buffer to the tab's file
		actUndo, actRedo, // rewrite the buffer
		actSelectAll, actFind, actCopy, actCut, // read (or move) the selection
		actToggleComment, actFormat, // rewrite the text
		actRun, actExplain: // read the text to execute it
		return true
	}
	return false
}

// runMenuAction performs an action. Commands that need to run asynchronously are
// returned as a tea.Cmd.
//
// The menu bar, the toolbar, the global shortcuts and the open-menu mnemonics
// all dispatch through here, so this is also where the welcome page steps
// aside for the actions listed in actsOnBuffer — one gate instead of one per
// entry point.
func (m *Model) runMenuAction(a menuAction) tea.Cmd {
	if actsOnBuffer(a) {
		m.revealEditor()
	}
	switch a {
	case actNewTab:
		m.newTab()
	case actOpenFile:
		// The picker is opened with its purpose recorded, so the choice
		// that comes back through the input handlers knows it belongs to
		// "open SQL file" (open_file.go).
		m.openSQLFilePicker()
	case actCloseTab:
		m.closeTab()
	case actNextTab:
		m.nextTab()
	case actPrevTab:
		m.prevTab()
	case actSave:
		return m.saveFile()
	case actQuit:
		return m.requestQuit()
	case actUndo:
		if m.buf.Undo() {
			m.clearSelection()
			m.afterEdit(false)
		}
	case actRedo:
		if m.buf.Redo() {
			m.clearSelection()
			m.afterEdit(false)
		}
	case actFind:
		m.openFind()
		return nil
	case actHistory:
		return m.openHistoryDialog()
	case actSelectAll:
		m.sel.anchor = &textbuf.Pos{Row: 0, Col: 0}
		m.buf.MoveBufferEnd()
		m.layout.focus = paneEditor
	case actCopy:
		if m.hasSelection() {
			return m.copySelectionCmd()
		}
		return m.setMessage(m.t(i18n.KeyMsgNoSelectionCopy))
	case actToggleComment:
		return m.toggleComment()
	case actCut:
		if m.hasSelection() {
			cmd := m.copySelectionCmd()
			m.deleteSelection()
			m.afterEdit(false)
			return cmd
		}
		return m.setMessage(m.t(i18n.KeyMsgNoSelectionCut))
	case actToggleObjects:
		m.toggleObjects()
		return m.persistLayout()
	case actToggleResults:
		m.toggleResults()
		return m.persistLayout()
	case actClosePane:
		m.closeFocusedPane()
		return m.persistLayout()
	case actFocusObjects:
		// Focusing may reveal a hidden pane; only that reveal is persisted,
		// a repeat press that only moves the focus leaves the layout alone.
		if m.focusPane(paneObjects) {
			return m.persistLayout()
		}
	case actFocusEditor:
		if m.focusPane(paneEditor) {
			return m.persistLayout()
		}
	case actFocusResults:
		if m.focusPane(paneResults) {
			return m.persistLayout()
		}
	case actRun:
		return m.runQuery()
	case actExplain:
		return m.explainQuery()
	case actClearResults:
		m.clearResults()
	case actExport:
		return m.openExportDialog()
	case actFormat:
		return m.formatSQL()
	case actPreferences:
		m.openSettings()
	case actOpenConfig:
		return m.setMessage(m.settingsPathMessage())
	case actToggleReadOnly:
		m.settings.ReadOnly = !m.settings.ReadOnly
		return tea.Batch(m.persistSettings(),
			m.setMessage(m.t(readOnlyStateKey(m.settings.ReadOnly))))
	case actToggleManualCommit:
		return m.toggleManualCommit()
	case actCommit:
		return m.commitTx()
	case actRollback:
		return m.rollbackTx()
	case actApplyChanges:
		return m.applyPendingChanges()
	case actDiscardChanges:
		return m.discardPendingChanges()
	case actAbout:
		return m.setMessage(m.t(i18n.KeyMsgAbout))
	case actKeymap:
		m.openKeybindings()
	case actVaultMenu:
		m.openVaultEntry()
	case actVaultLockNow:
		return m.lockVaultNow()
	}
	return nil
}

// requestQuit ends the session. Pending grid changes would disappear with it
// (the process is going away), so the quit asks first when there is any
// pending work; cancel leaves the workspace exactly as it was.
func (m *Model) requestQuit() tea.Cmd {
	if n := m.pendingGridChangesAll(); n > 0 {
		m.openLossConfirm(confirmLossQuit, n, -1, settings.Connection{})
		return nil
	}
	return m.quitNow()
}

// quitNow closes the connection and tells Bubble Tea to exit.
func (m *Model) quitNow() tea.Cmd {
	// Cancellation and detachment are immediate; rollback and Close run in a
	// background command so a query holding a pooled connection cannot freeze
	// the TUI while the user quits.
	closeCmd := m.closeLiveConnection()
	m.quitting = true
	if closeCmd == nil {
		return tea.Quit
	}
	return func() tea.Msg {
		msg, ok := closeCmd().(connCloseMsg)
		if !ok {
			return tea.QuitMsg{}
		}
		msg.quit = true
		return msg
	}
}

// settingsPathMessage reports where the settings file lives.
func (m Model) settingsPathMessage() string {
	path := m.settingsPath
	if path == "" {
		p, err := settings.Path()
		if err != nil {
			return m.t(i18n.KeyMsgOpenConfigFailed, err)
		}
		path = p
	}
	return m.t(i18n.KeyMsgSettingsPath, path)
}
