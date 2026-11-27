package ui

// mouse.go dispatches pointer events to overlays, dialogs, panes and the
// editor. Hit testing helpers live in mouse_geometry.go and click helpers in
// mouse_click.go.

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const multiClickWindow = 450 * time.Millisecond

// ---- Mouse events ----

// modalOpen reports whether one of the modal dialogs owns the screen. The
// key-bindings overlay, the vault dialog and the file picker take part in
// the same rule even though they dispatch ahead of the other dialogs in
// handleMouse, so "a modal owns the pointer" is one rule with one list, not
// two.
func (m Model) modalOpen() bool {
	return m.keysDlg.open || m.vaultDlg.open || m.dlg.open || m.confirm.open ||
		m.connDlg.open || m.exportDlg.open || m.historyDlg.open || m.tabRename.open ||
		m.picker.open
}

// handleMouse dispatches mouse events: modal dialogs claim the pointer first
// (clicks and motion alike), then the hover highlights, the menu bar, the
// context menus, the divider drags, the panes and the editor.
func (m Model) handleMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	if m.width == 0 || m.height == 0 {
		return m, nil
	}
	// Mouse support can be turned off in the settings dialog.
	if !m.settings.Mouse {
		return m, nil
	}

	// A pick whose dialog already closed is dispatched before this event is
	// interpreted, mirroring handleKey: normal use never lands here (the
	// picker branch below consumes a choice in the event that produced it),
	// but a directly-driven picker handler can leave a result behind, and a
	// stale result must not outlive its dialog.
	if !m.picker.open && (m.pickerPurpose != pickerForNone || m.picker.result != "") {
		return m, m.dispatchPickerResult()
	}

	// Every press starts a new gesture, so a row drag armed earlier is over
	// the moment the button goes down again — wherever it goes down. The
	// release that should have ended it cannot be trusted: a button released
	// outside the window produces no event at all, and a press on chrome the
	// grid never reaches (the toolbar, the menu bar, a pane divider) does not
	// visit the branch that clears it. The flag is dropped here, for every
	// button, and armed again only by the press that lands on a row — the
	// same start-over rule the editor's drag selection follows: a flag that
	// belongs to one gesture never survives into the next press.
	if msg.Action == tea.MouseActionPress {
		m.session.result.rowDrag = false
	}

	// A modal dialog owns the pointer (round 7 §1): motion is routed to the
	// open dialog, so the chrome behind it must not keep a highlight the
	// pointer has left — the toolbar, the menu titles, the column edges, the
	// tree and the tab strip are all out of reach while a modal is up, and no
	// row drag of the result grid survives a modal either.
	if m.modalOpen() {
		m.toolHover, m.menu.hover, m.colHover = -1, -1, -1
		m.objects.hover, m.tabHover = -1, -1
		m.session.result.hover, m.session.result.rowDrag = -1, false
		m.welcomeHover = -1
	}

	// The key-bindings overlay is the topmost modal: the wheel scrolls it and
	// every click is either its dismissal or swallowed, never a pane click.
	if m.keysDlg.open {
		return m.handleKeybindingsMouse(msg)
	}

	// The vault dialog is the topmost modal (it may hold the only way into
	// the saved connections): every click and hover belongs to it, the same
	// way the key-bindings overlay claims the pointer.
	if m.vaultDlg.open {
		return m.handleVaultMouse(msg)
	}

	// The file/directory picker claims the pointer before every other dialog
	// and every pane: the exporter stays open beneath it, so a click (or a
	// motion, or the wheel) must reach the picker — never the export button
	// or the grid behind the box. Like every modal it swallows what it does
	// not use, so nothing leaks downwards (round 7 §4). The key-bindings
	// overlay and the vault dialog dispatch earlier — they are drawn above
	// the picker and cannot be open at the same time.
	if m.picker.open {
		var cmd tea.Cmd
		m, cmd = m.handleFilePickerMouse(msg)
		return m, tea.Batch(cmd, m.dispatchPickerResult())
	}

	if m.find.open && msg.Action == tea.MouseActionPress {
		// The find prompt is keyboard-only; a click hands control back to the
		// pane under the pointer.
		m.closeFind()
	}
	r := m.paneRects()

	// A column-width drag owns the mouse until release, even when the pointer
	// leaves the results pane.
	if m.colResize.active {
		switch msg.Action {
		case tea.MouseActionMotion:
			m.resizeColumn(msg.X)
		case tea.MouseActionRelease:
			m.finishColumnResize()
		}
		return m, nil
	}

	// A scrollbar drag also owns the mouse until release.
	if m.scrollDrag.active {
		switch msg.Action {
		case tea.MouseActionMotion:
			m.dragScroll(msg.Y)
		case tea.MouseActionRelease:
			m.finishScrollDrag()
		}
		return m, nil
	}

	// The horizontal bar of the result grid owns the mouse the same way.
	if m.hScrollDrag.active {
		switch msg.Action {
		case tea.MouseActionMotion:
			m.dragHScroll(msg.X)
		case tea.MouseActionRelease:
			m.finishHScrollDrag()
		}
		return m, nil
	}

	// ---- Modal dialogs, topmost first ----
	// The open dialog claims motion exactly like a click, and it claims them
	// before anything else on screen: a hover inside it highlights a row, a
	// hover outside it is ignored (the dialog is modal), and neither may
	// reach the menu bar, the context menus or the panes below. The order is
	// the keyboard path's (editor_keys.go): settings, confirmation,
	// connection, export, history, rename.
	if m.dlg.open {
		return m.handleSettingsMouse(msg)
	}
	if m.confirm.open {
		return m.handleConfirmMouse(msg)
	}
	if m.connDlg.open {
		return m.handleConnMouse(msg)
	}
	if m.exportDlg.open {
		return m.handleExportMouse(msg)
	}
	if m.historyDlg.open {
		return m.handleHistoryMouse(msg)
	}
	if m.tabRename.open {
		return m.handleTabRenameMouse(msg)
	}

	// The toolbar highlights the button under the pointer, the result header
	// a draggable column boundary, and the tree and the tab strip highlight
	// the row under the pointer. Hover only draws: it never moves a selection
	// and never switches anything (round 7 §1).
	if msg.Action == tea.MouseActionMotion {
		if idx, ok := m.toolItemAt(msg.X, msg.Y); ok {
			m.toolHover = idx
		} else {
			m.toolHover = -1
		}
		if idx, ok := m.menuTitleAt(msg.X, msg.Y); ok {
			m.menu.hover = idx
		} else {
			m.menu.hover = -1
		}
		m.colHover = -1
		if col, ok := m.resultColumnEdgeAt(msg.X, msg.Y); ok {
			m.colHover = col
		}
		m.objectsHoverAt(r.objects, msg.X, msg.Y)
		// The result grid highlights the row under the pointer. Hover draws
		// only: it never moves the caret and never touches the selection
		// (round 7 §1/§3), and it is computed here — after the modals and the
		// column/scrollbar drags above have taken what they own, but before
		// the panes act on the same event.
		if display, _, ok := m.resultPointAt(msg.X, msg.Y); ok {
			m.session.result.hover = display
		} else {
			m.session.result.hover = -1
		}
		m.tabHover = -1
		if slot, _, ok := m.tabItemAt(msg.X, msg.Y); ok && slot.kind == slotTab {
			m.tabHover = slot.index
		}
		// The welcome page highlights the action row under the pointer like
		// every other list of round 7 §1: it only draws, the row Enter acts
		// on (welcomeSel) never moves.
		m.welcomeHover = -1
		if m.welcome {
			if idx, ok := m.welcomeItemAt(r.editor, msg.X, msg.Y); ok {
				m.welcomeHover = idx
			}
		}
	}

	// ---- Menu bar first: it overlays everything ----
	if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
		if idx, ok := m.menuTitleAt(msg.X, msg.Y); ok {
			if m.menu.open == idx {
				m.closeMenu() // clicking the open title again closes it
			} else {
				m.openMenu(idx) // otherwise open or switch to it
			}
			return m, nil
		}
	}
	if m.menu.open >= 0 {
		switch msg.Action {
		case tea.MouseActionPress:
			if it, ok := m.menuItemAt(msg.X, msg.Y); ok {
				m.menu.sel = it
				action := menuDefs[m.menu.open].items[it].action
				m.closeMenu()
				return m, m.runMenuAction(action)
			}
			// A click outside the dropdown closes the menu and is swallowed so
			// it cannot also hit a pane.
			m.closeMenu()
			return m, nil
		case tea.MouseActionMotion:
			if it, ok := m.menuItemAt(msg.X, msg.Y); ok {
				m.menu.sel = it // moving over the dropdown highlights items
			} else if idx, ok := m.menuTitleAt(msg.X, msg.Y); ok && idx != m.menu.open {
				// Sliding across the menu bar switches menus, as on the
				// desktop: no second click needed.
				m.openMenu(idx)
			}
			return m, nil
		case tea.MouseActionRelease:
			return m, nil
		}
	}

	// ---- Object browser context menu (overlay) ----
	if m.ctx.open {
		switch msg.Action {
		case tea.MouseActionPress:
			switch msg.Button {
			case tea.MouseButtonLeft:
				if idx, ok := m.objectsMenuItemAt(msg.X, msg.Y); ok {
					m.ctx.sel = idx
					action := m.ctx.actions[idx]
					return m, m.runObjectsMenuAction(action)
				}
				// A click outside dismisses the menu without reaching the pane.
				m.closeObjectsMenu()
				return m, nil
			case tea.MouseButtonRight:
				// A right click elsewhere closes this menu; the handler below
				// may then open the menu of another row.
				if r, ok := m.objectsMenuRect(); ok && r.Contains(msg.X, msg.Y) {
					m.closeObjectsMenu()
					return m, nil
				}
				m.closeObjectsMenu()
			default:
				return m, nil
			}
		case tea.MouseActionMotion:
			if idx, ok := m.objectsMenuItemAt(msg.X, msg.Y); ok {
				m.ctx.sel = idx
			}
			return m, nil
		case tea.MouseActionRelease:
			return m, nil
		}
	}

	// ---- Result grid context menu (overlay) ----
	if m.resultMenu.open {
		switch msg.Action {
		case tea.MouseActionPress:
			switch msg.Button {
			case tea.MouseButtonLeft:
				if idx, ok := m.resultMenuItemAt(msg.X, msg.Y); ok {
					m.resultMenu.sel = idx
					action := m.resultMenu.actions[idx]
					return m, m.runResultMenuAction(action)
				}
				// A click outside dismisses the menu without reaching the pane.
				m.closeResultMenu()
				return m, nil
			case tea.MouseButtonRight:
				if r, ok := m.resultMenuRect(); ok && r.Contains(msg.X, msg.Y) {
					m.closeResultMenu()
					return m, nil
				}
				m.closeResultMenu()
			default:
				return m, nil
			}
		case tea.MouseActionMotion:
			if idx, ok := m.resultMenuItemAt(msg.X, msg.Y); ok {
				m.resultMenu.sel = idx
			}
			return m, nil
		case tea.MouseActionRelease:
			return m, nil
		}
	}

	// ---- Tab bar context menu (overlay) ----
	if m.tabMenu.open {
		switch msg.Action {
		case tea.MouseActionPress:
			switch msg.Button {
			case tea.MouseButtonLeft:
				if idx, ok := m.tabMenuItemAt(msg.X, msg.Y); ok {
					m.tabMenu.sel = idx
					action := m.tabMenu.actions[idx]
					return m, m.runTabMenuAction(action)
				}
				// A click outside dismisses the menu without reaching the pane.
				m.closeTabMenu()
				return m, nil
			case tea.MouseButtonRight:
				// A right click elsewhere closes this menu; the handler below
				// may then open the menu of another tab.
				m.closeTabMenu()
			default:
				return m, nil
			}
		case tea.MouseActionMotion:
			if idx, ok := m.tabMenuItemAt(msg.X, msg.Y); ok {
				m.tabMenu.sel = idx
			}
			return m, nil
		case tea.MouseActionRelease:
			return m, nil
		}
	}

	// ---- Divider drag in progress: only motion and release matter ----
	if m.layout.dragging != dividerNone {
		switch msg.Action {
		case tea.MouseActionMotion:
			w, h := m.layout.objectsWidth, m.layout.resultsHeight
			m.resizeDivider(r, msg.X, msg.Y)
			if m.layout.objectsWidth != w || m.layout.resultsHeight != h {
				// The panes really moved: from now on the drag draws the
				// empty frames and the full contents come back on release.
				m.layout.dragMoved = true
			}
		case tea.MouseActionRelease:
			m.resizeDivider(r, msg.X, msg.Y)
			m.layout.dragging = dividerNone
			m.layout.dragMoved = false
			m.ensureCursorVisible()
			return m, m.persistLayout()
		}
		return m, nil
	}

	// Wheel: routed by the pane under the pointer.
	if tea.MouseEvent(msg).IsWheel() {
		switch {
		case r.objects.Contains(msg.X, msg.Y):
			delta := wheelDelta(msg.Button)
			m.objects.scroll = clamp(m.objects.scroll+delta, 0,
				max(0, len(m.objectsRows())-m.objectsBodyHeight()))
			// The rows moved under a still pointer: the highlight follows the
			// row the pointer is on now, not the one it was on (hover is
			// derived from the screen, so it is re-derived after the scroll).
			m.objectsHoverAt(r.objects, msg.X, msg.Y)
		case r.results.Contains(msg.X, msg.Y):
			m.moveCursor(0, wheelDelta(msg.Button)) // the grid scrolls with it
		case r.editor.Contains(msg.X, msg.Y):
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				m.scrollBy(-3)
			case tea.MouseButtonWheelDown:
				m.scrollBy(3)
			case tea.MouseButtonWheelLeft:
				m.scrollX = max(m.scrollX-8, 0)
			case tea.MouseButtonWheelRight:
				m.scrollX += 8
			}
		}
		return m, nil
	}

	// ---- Editor tab bar: switch, close or open tabs ----
	if slot, off, ok := m.tabItemAt(msg.X, msg.Y); ok && msg.Action == tea.MouseActionPress {
		switch msg.Button {
		case tea.MouseButtonLeft:
			m.handleTabClick(slot, off)
			return m, nil
		case tea.MouseButtonMiddle:
			m.closeTabAt(slot.index) // middle click closes, as in a browser
			return m, nil
		}
	}

	// ---- Toolbar buttons ----
	// The buttons dispatch through the same table the menu bar uses, so an
	// action that reads or rewrites the buffer (Run, Save, Format) reveals the
	// welcome page in runMenuAction exactly as its menu entry and F5 do.
	if r.toolbar.Contains(msg.X, msg.Y) &&
		msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
		if idx, ok := m.toolItemAt(msg.X, msg.Y); ok {
			return m, m.runMenuAction(toolButtons[idx].action)
		}
	}

	switch msg.Button {
	case tea.MouseButtonLeft:
		switch msg.Action {
		case tea.MouseActionPress:
			// 1) Expand strip: reopen a closed pane.
			if r.objectsExpand.Contains(msg.X, msg.Y) {
				m.layout.objectsVisible = true
				return m, m.persistLayout()
			}
			if r.resultsExpand.Contains(msg.X, msg.Y) {
				m.layout.resultsVisible = true
				return m, m.persistLayout()
			}
			// 2) Divider: start dragging.
			if r.dividerV.Contains(msg.X, msg.Y) {
				m.layout.dragging = dividerVertical
				m.layout.dragMoved = false
				return m, nil
			}
			if r.dividerH.Contains(msg.X, msg.Y) {
				m.layout.dragging = dividerHorizontal
				m.layout.dragMoved = false
				return m, nil
			}
			// 3) Scrollbar: clicking the track jumps to that position; the
			//    press also starts a drag, so one motion reaches any row.
			if pane, sb, ok := m.scrollbarAt(msg.X, msg.Y); ok {
				m.endObjectsFilterInput()
				if pane == scrollPaneObjects {
					m.layout.focus = paneObjects
				} else {
					m.layout.focus = paneResults
				}
				m.beginScrollDrag(pane, sb, msg.Y)
				return m, nil
			}
			// 3b) The result grid's horizontal bar: clicking the track jumps
			//     to that column; the press also starts a drag, so one motion
			//     reaches any column.
			if hs, ok := m.resultsHScrollMetrics(); ok &&
				msg.Y == hs.y && msg.X >= hs.x && msg.X < hs.x+hs.width {
				m.endObjectsFilterInput()
				m.layout.focus = paneResults
				m.beginHScrollDrag(hs, msg.X)
				return m, nil
			}
			// 4) A result column boundary resizes that column. A double click
			//    restores its automatic width; a click on the header text below
			//    still sorts.
			if col, ok := m.resultColumnEdgeAt(msg.X, msg.Y); ok {
				clicks := m.registerClick(msg.X, msg.Y)
				m.endObjectsFilterInput()
				m.layout.focus = paneResults
				if clicks >= 2 {
					m.clicks = 0
					m.session.result.resetColumnWidth(col)
					m.ensureResultVisible()
					return m, nil
				}
				m.beginColumnResize(col, msg.X)
				return m, nil
			}
			// 4) A column header sorts the result by that column.
			if col, ok := m.resultHeaderAt(msg.X, msg.Y); ok {
				m.session.result.filtering = false
				m.toggleSort(col)
				m.endObjectsFilterInput()
				m.layout.focus = paneResults
				return m, nil
			}
			// 4) Pane close buttons.
			if m.layout.objectsVisible && closeBtnRect(r.objects).Contains(msg.X, msg.Y) {
				m.toggleObjects()
				return m, m.persistLayout()
			}
			if m.layout.resultsVisible && closeBtnRect(r.results).Contains(msg.X, msg.Y) {
				m.toggleResults()
				return m, m.persistLayout()
			}
			// 4) Completion popup item: accept it.
			if idx, ok := m.popupItemAt(msg.X, msg.Y); ok {
				m.popup.selected = idx
				return m.acceptCompletion()
			}
			// 5) Objects pane: focus and select the row. The twisty (caret and
			//    icon) is a button: it folds the node without triggering the
			//    double-click insert. A double click on the label inserts the
			//    table name.
			if r.objects.Contains(msg.X, msg.Y) {
				// Picking a row ends the filter input but keeps the query: the
				// typing is done, the filter stays in effect. End it before the
				// twisty runs: an input that was just opened (empty query) must
				// not make the click a no-op. With text in the filter the twisty
				// still only selects, as the filtered tree never writes the
				// stored expansion state.
				m.endObjectsFilterInput()
				m.layout.focus = paneObjects
				if idx, ok := m.objectsItemAt(r.objects, msg.X, msg.Y); ok {
					m.objects.sel = idx
					if caretIdx, ok := m.objectsCaretAt(r.objects, msg.X, msg.Y); ok {
						m.objectsPointerToggle(caretIdx)
						m.ensureObjectsVisible(m.objectsBodyHeight())
						m.clicks = 0
						return m, nil
					}
					m.ensureObjectsVisible(m.objectsBodyHeight())
				}
				m.registerClick(msg.X, msg.Y)
				return m, nil
			}
			// 6) Results pane: select the row under the pointer, and the cell
			//    inside it. This is also how the grid gets keyboard focus. A
			//    plain click starts a fresh selection (there is none until a
			//    drag grows one), a shift+click anchors at the caret this
			//    click takes over from and extends to the clicked row, and
			//    either arms the row drag that motion then extends (round 7 §3).
			if r.results.Contains(msg.X, msg.Y) {
				if m.session.result.editing != nil {
					// The cell editor owns the grid while it is open, the
					// same way it owns the keyboard: a press must not move
					// the caret underneath it (the editor would stay bound
					// to the cell it was opened on while the highlight
					// jumps away), and it must not commit the value into a
					// cell the pointer happens to sit on. Enter and Esc
					// are how the edit ends. The press itself still brings
					// the keyboard back to the grid, so the edit is where
					// the user is looking.
					m.endObjectsFilterInput()
					m.layout.focus = paneResults
					return m, nil
				}
				m.endObjectsFilterInput()
				m.layout.focus = paneResults
				m.session.result.filtering = false
				if display, col, ok := m.resultPointAt(msg.X, msg.Y); ok {
					res := &m.session.result
					if msg.Shift {
						res.anchorResultSelection()
					} else {
						res.clearRowSelection()
					}
					res.cursorRow, res.cursorCol = display, col
					res.rowDrag = true
					m.ensureResultVisible()
				} else {
					// A press on no row at all (empty space below the rows,
					// the summary line) starts no drag: the flag has to go
					// here, because the release that ended an earlier drag
					// may never have arrived (a button released outside the
					// window), and a motion after this press would then
					// extend the selection from that stale anchor.
					m.session.result.rowDrag = false
					if !msg.Shift {
						// A plain click on the empty space below the rows
						// leaves nothing selected.
						m.session.result.clearRowSelection()
					}
				}
				return m, nil
			}
			// 7) Editor: focus, place the caret; double click selects a word,
			//    triple click selects a line.
			if !r.editor.Contains(msg.X, msg.Y) {
				m.popup.close()
				return m, nil
			}
			m.endObjectsFilterInput()
			m.layout.focus = paneEditor
			if m.welcome {
				// The welcome page owns the pane while it is up: a click on
				// an action row picks and runs it (like every list of round
				// 7 §1, the click — not the pointer — moves welcomeSel), and
				// a click in the body turns the page into an editor tab so
				// the caret can be placed in it.
				if idx, ok := m.welcomeItemAt(r.editor, msg.X, msg.Y); ok {
					m.welcomeSel = idx
					return m, m.welcomeActivate(idx)
				}
				if msg.Y <= r.editor.Y {
					return m, nil // the pane title row is chrome, not text
				}
				m.welcomeToEditor()
			}
			clicks := m.registerClick(msg.X, msg.Y)
			pos := m.screenToBuffer(msg.X, msg.Y)
			m.popup.close()
			m.dragging = true
			switch clicks {
			case 2: // double click selects a word (dots included, so u.email works)
				m.dragging = false
				m.selectWordAt(pos)
			case 3: // triple click selects the whole line
				m.dragging = false
				m.selectLineAt(pos.Row)
			default:
				m.buf.SetCursor(pos)
				m.beginSelection()
			}
			m.ensureCursorVisible()
			return m, nil

		case tea.MouseActionMotion:
			// A drag that began on a result row extends the selection to the
			// row under the pointer. The column and the scrollbar drags own
			// the mouse until release (above), so they cannot be stolen here,
			// and the focus check keeps a stale flag from claiming a drag
			// that started in another pane (round 7 §3).
			//
			// Only a motion that still carries the left button extends: a
			// sweep with no button is a pointer move, even with the flag still
			// armed — a button released outside the window never sends its
			// release event, so the terminal simply reports plain motions
			// afterwards (a motion with any other button is no drag either).
			if m.session.result.rowDrag && m.layout.focus == paneResults &&
				msg.Button == tea.MouseButtonLeft {
				if display, _, ok := m.resultPointAt(msg.X, msg.Y); ok {
					m.extendResultSelection(display)
				}
				return m, nil
			}
			if !m.dragging {
				return m, nil
			}
			// Auto-scroll when dragging past the edge of the editor pane.
			er := r.editor
			if msg.Y <= er.Y+1 && m.firstRow > 0 {
				m.firstRow--
			} else if msg.Y >= er.Y+er.H-1 && m.firstRow < m.buf.Lines()-1 {
				m.firstRow++
			}
			m.buf.SetCursor(m.screenToBuffer(msg.X, msg.Y))
			return m, nil

		case tea.MouseActionRelease:
			// The button is up wherever the pointer is: the row drag is over.
			// The selection it built stays — releasing only ends the gesture.
			m.session.result.rowDrag = false
			// Double click in the objects pane: the second press already
			// selected the same row, so act on it (insert a table name or open
			// a saved connection). insertObjectName focuses the editor itself;
			// toggling/connecting keeps the object browser focused.
			if r.objects.Contains(msg.X, msg.Y) {
				if idx, ok := m.objectsItemAt(r.objects, msg.X, msg.Y); ok {
					// The press that started this click already left the
					// filter input; say so explicitly for clicks whose press
					// landed elsewhere. The query itself stays.
					m.objects.filtering = false
					if m.objects.sel == idx && m.clicks >= 2 {
						m.clicks = 0
						return m, m.objectsDoubleClick()
					}
				}
				return m, nil
			}
			m.dragging = false
			// A plain click produces an empty selection; drop the anchor.
			if !m.hasSelection() {
				m.clearSelection()
			}
			return m, nil
		}

	case tea.MouseButtonRight:
		// The system clipboard is not readable from a terminal, so the right
		// button opens the context menu of the pane under the pointer.
		m.closeObjectsMenu()
		m.closeResultMenu()
		m.closeTabMenu()
		m.popup.close()
		if m.dragging {
			m.dragging = false
			m.clearSelection()
		}
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		if slot, _, ok := m.tabItemAt(msg.X, msg.Y); ok && slot.kind == slotTab {
			// A tab: its own menu (rename, duplicate, close).
			m.openTabMenu(msg.X, msg.Y, slot.index)
			return m, nil
		}
		if r.objects.Contains(msg.X, msg.Y) {
			m.endObjectsFilterInput()
			m.layout.focus = paneObjects
			if idx, ok := m.objectsItemAt(r.objects, msg.X, msg.Y); ok {
				rows := m.objectsRows()
				m.objects.sel = idx
				// A row was picked: leave the filter input, keep the query.
				m.ensureObjectsVisible(m.objectsBodyHeight())
				m.openObjectsMenu(msg.X, msg.Y, rows[idx])
			} else if msg.Y >= r.objects.Y+1 {
				// Empty space below the tree: offer the connection form.
				m.objects.sel = -1
				m.openObjectsNewConnMenu(msg.X, msg.Y)
			}
			return m, nil
		}
		if r.results.Contains(msg.X, msg.Y) {
			m.endObjectsFilterInput()
			m.layout.focus = paneResults
			m.session.result.filtering = false
			// A right click is a fresh caret placement for the menu: it clears
			// an older selection the way a plain click does, so the menu acts
			// on the row it was opened for.
			m.session.result.clearRowSelection()
			if col, ok := m.resultHeaderAt(msg.X, msg.Y); ok {
				m.session.result.cursorCol = col
				m.openResultMenu(msg.X, msg.Y, resultTargetHeader, -1, col)
				return m, nil
			}
			if display, col, ok := m.resultPointAt(msg.X, msg.Y); ok {
				m.session.result.cursorRow, m.session.result.cursorCol = display, col
				m.ensureResultVisible()
				kind := resultTargetCell
				if msg.X < r.results.X+m.resultsGutterWidth() {
					kind = resultTargetGutter
				}
				m.openResultMenu(msg.X, msg.Y, kind, display, col)
				return m, nil
			}
			m.openResultMenu(msg.X, msg.Y, resultTargetEmpty, -1, -1)
		}
		return m, nil
	}
	return m, nil
}

// wheelDelta maps a wheel button to a scroll step (positive scrolls down).
func wheelDelta(btn tea.MouseButton) int {
	switch btn {
	case tea.MouseButtonWheelDown:
		return 3
	case tea.MouseButtonWheelUp:
		return -3
	}
	return 0
}
