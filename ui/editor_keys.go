package ui

// editor_keys.go maps keyboard input to actions. Global shortcuts are
// resolved through menuDefs; modal dialogs and overlays get the first chance
// to consume a key before the editor does.

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/i18n"
)

// shiftF10Key is the name of the shortcut that opens the context menus.
const shiftF10Key = "shift+f10"

// keyName returns the name the application uses for a key press.
//
// Bubble Tea names the space key after its character, so the space is
// normalized here as well.
func keyName(msg tea.KeyMsg) string {
	name := msg.String()
	if msg.Type == tea.KeySpace {
		name = "space"
	}
	return normalizeKeyName(name)
}

// normalizeKeyName maps the key names a real terminal sends onto the names
// the application uses.
//
// A terminal cannot send "shift+f10": the escape sequences that carry the
// shortcut in the wild ("\x1b[25~" and the "\x1b[1;2P" family) are decoded
// by Bubble Tea v1.3.10 as KeyF13, so the application receives the name
// "f13". F13 is the name the terminals give to Shift+F10, so it opens the
// context menus like the documented shortcut does.
//
// F14 ("\x1b[26~", "\x1b[1;2Q") is left alone: the terminals number the
// shifted function keys F13…F24 after F1…F12, so F14 is Shift+F2 and not
// Shift+F11.
func normalizeKeyName(name string) string {
	switch name {
	case "f13":
		return shiftF10Key
	case "ctrl+_":
		// Windows reports Ctrl+/ as a unit separator, which Bubble Tea names
		// "ctrl+_"; the shortcut is documented as Ctrl+/. Treat both names as
		// the same key so the comment toggle works on every platform.
		return "ctrl+/"
	case "\x00":
		// Windows reports Ctrl+Space through a key event whose only rune is
		// NUL (the space carries no character of its own while Ctrl is down),
		// so the key has no name Bubble Tea would recognize. A NUL never
		// arrives as text — paste is sanitized and plain input drops control
		// characters — so it is the completion shortcut here.
		return "ctrl+space"
	}
	return name
}

// menuKeyMsg rewrites the key message for the menus.
//
// The menus compare the name of the message they receive, so the name has to
// be the one the application uses ("shift+f10"). A Bubble Tea message carries
// its name in the runes, and a menu never writes to a buffer, so the name can
// travel that way without anything being typed by accident.
func menuKeyMsg(msg tea.KeyMsg) tea.KeyMsg {
	if msg.Type == tea.KeyF13 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(shiftF10Key)}
	}
	return msg
}

// handleKey handles a key press.
func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	key := keyName(msg)

	// A pick whose dialog already closed is dispatched before this key is
	// interpreted. Normal use never lands here — the picker branch below
	// consumes a choice in the very event that produced it — but a caller
	// driving handleFilePickerKey directly can leave a result behind, and
	// a stale result must not outlive its dialog. The key that hit this
	// guard is dropped: the choice it belongs to matters more.
	if !m.picker.open && (m.pickerPurpose != pickerForNone || m.picker.result != "") {
		return m, m.dispatchPickerResult()
	}

	// Consecutive plain characters (spaces included) form a single undo group;
	// any other key starts a new one.
	if len(msg.Runes) == 0 {
		m.typeSeq++
	}

	// ---- The key-bindings overlay takes priority over every other modal ----
	// It is drawn on top of the dialogs and must also be the first thing the
	// keyboard sees, so its Esc only closes the overlay.
	if m.keysDlg.open {
		if m.handleKeybindingsKey(key) {
			return m, nil
		}
	}

	// ---- The vault dialog is the topmost modal ----
	// It may hold the only way into the saved connections (the startup
	// unlock), so it comes before the menu bar and every other modal and
	// swallows all keys until it is closed or skipped.
	if m.vaultDlg.open {
		var cmd tea.Cmd
		var handled bool
		m, cmd, handled = m.handleVaultKey(key, msg.Runes, msg.Paste)
		if handled {
			return m, cmd
		}
	}

	// ---- The file/directory picker is modal and above every dialog ----
	// It sits on a layer of its own: the exporter stays open beneath it, so
	// its keys must be claimed before the export branch (and every other
	// dialog and pane below) can see them. Only the key-bindings overlay and
	// the vault dialog dispatch earlier, and neither can be open while a
	// picker is (they are drawn above it for the same reason).
	//
	// Ctrl+O itself needs no branch here: menuDefs is the key map, so the
	// shortcut travels with the menu entry it belongs to (keymapAction).
	if m.picker.open {
		var cmd tea.Cmd
		var handled bool
		m, cmd, handled = m.handleFilePickerKey(key, msg.Runes)
		if handled {
			// The press may have closed the picker: the purpose decides who
			// receives the choice (open a file, set the export folder).
			return m, tea.Batch(cmd, m.dispatchPickerResult())
		}
	}

	// ---- An open menu takes priority ----
	if m.menu.open >= 0 {
		var cmd tea.Cmd
		var handled bool
		m, cmd, handled = m.handleMenuKey(menuKeyMsg(msg))
		if handled {
			return m, cmd
		}
	}

	// ---- The settings dialog is modal ----
	if m.dlg.open {
		var cmd tea.Cmd
		var handled bool
		m, cmd, handled = m.handleSettingsKey(key)
		if handled {
			return m, cmd
		}
	}

	// ---- The execution confirmation is modal ----
	if m.confirm.open {
		var cmd tea.Cmd
		var handled bool
		m, cmd, handled = m.handleConfirmKey(key)
		if handled {
			return m, cmd
		}
	}

	// ---- The connection form is modal too ----
	if m.connDlg.open {
		var cmd tea.Cmd
		var handled bool
		m, cmd, handled = m.handleConnKey(key, msg.Runes)
		if handled {
			return m, cmd
		}
	}

	// ---- The export dialog is modal ----
	if m.exportDlg.open {
		var cmd tea.Cmd
		var handled bool
		m, cmd, handled = m.handleExportKey(key, msg.Runes)
		if handled {
			return m, cmd
		}
	}

	// ---- The query-history list is modal ----
	if m.historyDlg.open {
		var cmd tea.Cmd
		var handled bool
		// The runes are handed over as well: the history filter is a
		// one-line input, and a space or a paste must reach it.
		m, cmd, handled = m.handleHistoryKey(key, msg.Runes)
		if handled {
			return m, cmd
		}
	}

	// ---- The find prompt owns the keyboard while it is open ----
	if m.find.open {
		var cmd tea.Cmd
		var handled bool
		m, cmd, handled = m.handleFindKey(key, msg.Runes)
		if handled {
			return m, cmd
		}
	}

	// ---- The object browser context menu is an overlay ----
	if m.ctx.open {
		var cmd tea.Cmd
		var handled bool
		m, cmd, handled = m.handleObjectsMenuKey(menuKeyMsg(msg))
		if handled {
			return m, cmd
		}
	}

	// ---- The result grid context menu is an overlay too ----
	if m.resultMenu.open {
		var cmd tea.Cmd
		var handled bool
		m, cmd, handled = m.handleResultMenuKey(menuKeyMsg(msg))
		if handled {
			return m, cmd
		}
	}

	// ---- The tab bar's context menu is an overlay as well ----
	if m.tabMenu.open {
		var cmd tea.Cmd
		var handled bool
		m, cmd, handled = m.handleTabMenuKey(menuKeyMsg(msg))
		if handled {
			return m, cmd
		}
	}

	// ---- The tab rename input is modal ----
	if m.tabRename.open {
		var cmd tea.Cmd
		var handled bool
		m, cmd, handled = m.handleTabRenameKey(msg)
		if handled {
			return m, cmd
		}
	}

	// ---- A running query is cancelled with Esc ----
	if key == "esc" && m.queryRunning {
		m.cancelQuery()
		return m, m.setMessage(m.t(i18n.KeyMsgQueryCancelled))
	}

	// ---- The object filter input owns the keyboard while it is open ----
	if m.layout.focus == paneObjects && m.objects.filtering {
		// A one-line input: every key is filter text, filter editing or tree
		// navigation, and nothing else. The branch sits before the menu
		// accelerators and the global shortcuts on purpose — Ctrl+A would
		// select the whole editor buffer behind the input and the next typed
		// character would replace the SQL, and Alt+1/2/3 must not steal the
		// focus mid-word (Esc leaves the input first). A running query is
		// still cancelled with Esc above; on its own Esc clears the filter.
		m.objectsFilterKey(msg)
		return m, nil
	}

	// ---- The results grid owns its keys while it has the focus ----
	if m.layout.focus == paneResults {
		if m.session.result.editing != nil {
			// The cell editor is a one-line input: nothing else may see
			// these keys while it is open.
			m.cellEditKey(key, msg.Runes)
			return m, nil
		}
		if m.session.result.filtering {
			// The filter input is a one-line editor: nothing else may see these
			// keys while it is open.
			return m.handleResultKey(key, msg.Runes)
		}
		// The welcome page's accelerators are plain letters the grid claims
		// as keys of its own too ("s" sorts the column): while the page is up
		// it owns this tab, so n/o/s are resolved before the grid sees them.
		// F1 needs no branch of its own — the grid does not claim it, and
		// keymapAction resolves it before the page would. The two one-line
		// inputs above keep every key, accelerators included.
		if m.welcome {
			switch key {
			case "n", "o", "s":
				if cmd, handled := m.welcomeKey(msg, key); handled {
					return m, cmd
				}
			}
		}
		switch key {
		case "shift+up", "shift+down", "shift+home", "shift+end":
			// Shift navigation extends the row selection; with no selection
			// yet the first press anchors it at the caret (round 7 §3).
			return m.handleResultSelectionKey(key)
		case "/":
			// Opening the filter starts another view session: Enter jumps the
			// caret to the top row, and that must not drag a selection there.
			m.session.result.clearRowSelection()
			return m.handleResultKey(key, msg.Runes)
		case "ctrl+c", "y":
			// Both keys copy the selected rows when there are any; without a
			// selection they keep their old meaning (the whole row under the
			// caret, the cell under the caret). Ctrl+C outside the grid keeps
			// its global meaning as before — except that it must not quit
			// while this grid still shows rows selected (see below).
			if m.session.result.hasRowSelection() {
				return m, m.copyResultSelection()
			}
			return m.handleResultKey(key, msg.Runes)
		case "up", "down", "left", "right", "pgup", "pgdown", "home", "end", "s", "e", "[", "]", "alt+left", "alt+right", shiftF10Key:
			return m.handleResultKey(key, msg.Runes)
		case "ctrl+s", "ctrl+enter", "ctrl+j":
			// Ctrl+S applies the pending changes of the grid. Ctrl+Enter is
			// the fallback for the terminals whose tty swallows 0x13: with
			// the software flow control of IXON enabled the byte never
			// reaches the application. The terminals send Ctrl+Enter as
			// Ctrl+J, so both names are accepted. Without any pending
			// change the key is not intercepted and keeps its global meaning
			// (save file, run the statement).
			if m.session.result.pendingCount() > 0 {
				cmd := m.applyPendingChanges()
				return m, cmd
			}
		case "esc":
			// Esc drops the selection first; only a grid without one is left
			// with the second press (round 7 §3). Nothing else changes.
			if m.session.result.clearRowSelection() {
				return m, nil
			}
			m.layout.focus = paneEditor
			return m, nil
		case "tab":
			m.layout.focus = paneEditor
			return m, nil
		}
	}

	// ---- Menu accelerators ----
	if idx, ok := menuAccelIndex(key); ok { // Alt+letter opens a menu
		m.openMenu(idx)
		return m, nil
	}
	if key == "f10" { // the conventional terminal menu key
		m.openMenu(0)
		return m, nil
	}

	// Ctrl+C means "copy" whenever there is something selected: in the
	// editor, or in the result grid even though the focus has moved away
	// from it (Tab, a click in another pane). The summary line still says
	// "N rows selected" in that state, so quitting here would throw the
	// user's work away behind their back — Ctrl+C copies the rows instead,
	// exactly as it does with the grid focused.
	if key == "ctrl+c" && m.session.result.hasRowSelection() {
		return m, m.copyResultSelection()
	}

	// Ctrl+C quits when there is nothing to copy (terminal convention).
	if key == "ctrl+c" && !m.hasSelection() {
		// The exit is the same as the one from the File menu, ask included:
		// pending grid changes would be lost with the process.
		return m, m.requestQuit()
	}

	// Global shortcuts live in the menu table, so what the menus advertise is
	// exactly what works.
	if a, ok := keymapAction(key); ok {
		return m, m.runMenuAction(a)
	}

	// ---- Global keys that are not menu items ----
	switch key {
	case "ctrl+shift+z":
		// Redo alias. Bubble Tea v1 has no key name for it and the
		// terminals report Ctrl+Shift+Z as Ctrl+Z, so this branch is
		// hardly ever reached: the advertised shortcut is Ctrl+Y.
		if m.buf.Redo() {
			m.clearSelection()
			m.afterEdit(false)
		}
		return m, nil
	case "ctrl+space", "ctrl+@": // most terminals send NUL, which Bubble Tea parses as ctrl+@
		// Completion reads the buffer and its popup is drawn over the editor
		// text area (and skipped entirely while the welcome page is up), so
		// the page has to step aside first: the candidates would otherwise be
		// computed for — and later accepted into — SQL the user cannot see.
		m.revealEditor()
		m.refreshCompletion(true)
		return m, nil
	}

	// ---- Objects pane navigation (while it has the focus) ----
	if m.layout.focus == paneObjects {
		if key == "/" {
			// Start filtering from scratch; the key itself switches the mode
			// and is never part of the query.
			m.objects.filtering = true
			m.objects.filter = ""
			m.objectsFilterSync()
			return m, nil
		}
		switch key {
		case "up":
			m.objectsMove(-1)
			return m, nil
		case "down":
			m.objectsMove(1)
			return m, nil
		case "pgup":
			m.objectsMove(-m.objectsBodyHeight())
			return m, nil
		case "pgdown":
			m.objectsMove(m.objectsBodyHeight())
			return m, nil
		case "home":
			if len(m.objectsRows()) > 0 {
				m.objects.sel = 0
				m.ensureObjectsVisible(m.objectsBodyHeight())
			}
			return m, nil
		case "end":
			if rows := m.objectsRows(); len(rows) > 0 {
				m.objects.sel = len(rows) - 1
				m.ensureObjectsVisible(m.objectsBodyHeight())
			}
			return m, nil
		case "left": // fold the selected node
			return m, m.objectsToggle(false)
		case "right": // unfold it
			return m, m.objectsToggle(true)
		case "enter", "space": // insert the name and jump back to the editor
			return m, m.objectsAccept()
		case shiftF10Key: // open the row's context menu
			m.openObjectsMenuForSelection()
			return m, nil
		case "esc", "tab": // hand the focus back to the editor
			m.layout.focus = paneEditor
			return m, nil
		}
		// Every other key (plain typing included) goes to the editor.
	}

	// ---- The tab bar's context menu ----
	// The tab bar is not a focus target of its own, so its shortcut lives
	// here: with the editor focused, Shift+F10 opens the menu of the active
	// tab. The object browser and the result grid keep their own Shift+F10,
	// which is why this comes after their branches.
	if key == shiftF10Key {
		m.openTabMenuForActiveTab()
		return m, nil
	}

	// ---- The welcome page owns its keys while it is the active tab ----
	// It sits here, after the global shortcuts (Ctrl+O, Ctrl+T, Ctrl+Q, F1,
	// the Alt+letter menu accelerators) and after the panes that own the
	// keyboard while focused (the grid resolves the page's n/o/s accelerators
	// first — see its branch above), so none of them lose a key to it. What is
	// left is either a key of the page or input that turns it into a normal
	// editor tab (docs/ux-round7.md, §2).
	if m.welcome {
		// The page is not an editor: nothing may complete behind it while it
		// is up. Ctrl+Space reveals the page before it arms the popup (its
		// own case above), so this is the guard for the keys that reach this
		// branch with a popup still open.
		m.popup.close()
		if cmd, handled := m.welcomeKey(msg, key); handled {
			return m, cmd
		}
		// The press carries text and the user is starting to write: this tab
		// becomes an ordinary editor tab and the very same key is applied
		// below, exactly as it would be in any other tab.
		m.welcomeToEditor()
	}

	// Bracketed paste (Cmd+V, right-click paste): insert it all as one undo
	// group. A paste is text from outside the program, unlike the keystrokes
	// below it, so it is cleaned on the way in — escape sequences and other
	// control characters must not reach the terminal through the buffer,
	// which is drawn verbatim (sanitizePastedText).
	if msg.Paste && len(msg.Runes) > 0 {
		m.typeSeq++
		m.deleteSelection()
		m.buf.InsertStringGrouped(sanitizePastedText(string(msg.Runes)), m.typeSeq)
		m.popup.close()
		m.ensureCursorVisible()
		return m, nil
	}

	// ---- Keys handled by the completion popup ----
	if m.popup.open {
		switch key {
		case "esc":
			m.popup.close()
			return m, nil
		case "up", "ctrl+p":
			m.popup.move(-1)
			return m, nil
		case "down", "ctrl+n":
			m.popup.move(1)
			return m, nil
		case "pgup":
			m.popup.page(-1)
			return m, nil
		case "pgdown":
			m.popup.page(1)
			return m, nil
		case "enter", "tab":
			return m.acceptCompletion()
		case "left", "right", "home", "end", "ctrl+left", "ctrl+right":
			// Moving the caret dismisses the popup.
			m.popup.close()
		}
	}

	// ---- Editing and movement ----
	switch key {
	case "up", "shift+up":
		m.moveVertically(-1, key == "shift+up")
	case "down", "shift+down":
		m.moveVertically(1, key == "shift+down")
	case "left", "shift+left":
		if key == "shift+left" {
			m.startSelectionIfNeeded()
			m.buf.MoveLeft(false)
		} else {
			m.moveCaret(func() { m.buf.MoveLeft(false) })
		}
	case "right", "shift+right":
		if key == "shift+right" {
			m.startSelectionIfNeeded()
			m.buf.MoveRight(false)
		} else {
			m.moveCaret(func() { m.buf.MoveRight(false) })
		}
	case "ctrl+left":
		m.moveCaret(func() { m.buf.MoveLeft(true) })
	case "ctrl+right":
		m.moveCaret(func() { m.buf.MoveRight(true) })
	case "home", "shift+home":
		if key == "shift+home" {
			m.startSelectionIfNeeded()
			m.buf.MoveLineHome()
		} else {
			m.moveCaret(m.buf.MoveLineHome)
		}
	case "end", "shift+end":
		if key == "shift+end" {
			m.startSelectionIfNeeded()
			m.buf.MoveLineEnd()
		} else {
			m.moveCaret(m.buf.MoveLineEnd)
		}
	case "ctrl+home":
		m.moveCaret(m.buf.MoveBufferHome)
	case "ctrl+end":
		m.moveCaret(m.buf.MoveBufferEnd)
	case "pgup", "shift+pgup":
		if key == "shift+pgup" {
			m.startSelectionIfNeeded()
		} else {
			m.clearSelection()
		}
		m.moveVertical(-m.editorHeight())
	case "pgdown", "shift+pgdown":
		if key == "shift+pgdown" {
			m.startSelectionIfNeeded()
		} else {
			m.clearSelection()
		}
		m.moveVertical(m.editorHeight())
	case "backspace":
		if m.deleteSelection() {
			m.afterEdit(false)
			return m, nil
		}
		if m.buf.DeleteBackward() {
			m.afterEdit(false)
			return m, nil
		}
		m.popup.close()
		return m, nil
	case "delete":
		if m.deleteSelection() {
			m.afterEdit(false)
			return m, nil
		}
		if m.buf.DeleteForward() {
			m.afterEdit(false)
			return m, nil
		}
		m.popup.close()
		return m, nil
	case "enter":
		m.deleteSelection()
		m.insertNewline()
		m.afterEdit(false)
		return m, nil
	case "tab":
		if !m.popup.open {
			m.deleteSelection()
			m.buf.InsertString("  ")
			m.afterEdit(false)
		}
		return m, nil
	case "esc":
		m.popup.close()
		m.clearSelection()
		return m, nil
	}

	// Plain character input (note: the space key has Type KeySpace, so Runes is
	// the reliable signal).
	if len(msg.Runes) > 0 {
		// A terminal can deliver control characters inside a key event's runes:
		// Windows reports Alt+NUL for a lone escape byte, and a terminal that
		// sends Esc with a modified key can hand over Escape itself. They are
		// never text — keeping them would put invisible bytes into a statement
		// (the paste path strips them for the same reason).
		text := stripControl(string(msg.Runes))
		if text == "" {
			return m, nil
		}
		if m.typeSeq == 0 {
			m.typeSeq = 1
		}
		m.deleteSelection() // typing replaces the selection
		m.buf.InsertStringGrouped(text, m.typeSeq)
		// Typing an identifier character refreshes completion; a dot forces
		// qualified completion; anything else (space, operator, …) dismisses
		// the popup.
		last := []rune(text)[len([]rune(text))-1]
		switch {
		case isIdentRune(last):
			m.afterEdit(true)
		case last == '.':
			m.refreshCompletion(true)
			m.ensureCursorVisible()
		default:
			m.popup.close()
			m.ensureCursorVisible()
		}
		return m, nil
	}
	return m, nil
}
