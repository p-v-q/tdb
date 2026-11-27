package ui

// tabs.go owns the lifecycle of editor tabs and the active *session pointer.

import (
	"path/filepath"
	"time"

	"github.com/p-v-q/tdb/settings"
	"github.com/p-v-q/tdb/textbuf"
)

// ---- Editor tabs ----

// closeTabsExcept closes every tab but the one at keep and activates it. It
// reports false when it asked first instead of closing: pending grid changes
// in one of the tabs would be lost otherwise.
func (m *Model) closeTabsExcept(keep int) bool {
	if keep < 0 || keep >= len(m.tabs) || len(m.tabs) <= 1 {
		return false
	}
	if n := m.pendingGridChangesExcept(keep); n > 0 {
		m.openLossConfirm(confirmLossCloseOthers, n, keep, settings.Connection{})
		return false
	}
	m.forceCloseTabsExcept(keep)
	return true
}

// forceCloseTabsExcept closes every tab but keep, without asking: the caller
// has already confirmed that the pending changes may go.
func (m *Model) forceCloseTabsExcept(keep int) {
	if keep < 0 || keep >= len(m.tabs) || len(m.tabs) <= 1 {
		return
	}
	m.tabs = []*session{m.tabs[keep]}
	m.switchTab(0)
	m.updateContext()
}

// closeTabsToLeft closes the tabs before index: the tab at index becomes the
// first one, the tabs to its right stay. The active tab survives the move when
// it was one of the kept tabs; otherwise the target tab takes over. It reports
// false when it asked first instead of closing.
func (m *Model) closeTabsToLeft(index int) bool {
	if index <= 0 || index >= len(m.tabs) {
		return false
	}
	if n := m.pendingGridChangesIn(m.tabs[:index]); n > 0 {
		m.openLossConfirm(confirmLossCloseLeft, n, index, settings.Connection{})
		return false
	}
	m.forceCloseTabsToLeft(index)
	return true
}

// forceCloseTabsToLeft performs the close a loss confirmation protected.
func (m *Model) forceCloseTabsToLeft(index int) {
	if index <= 0 || index >= len(m.tabs) {
		return
	}
	m.tabs = append([]*session(nil), m.tabs[index:]...)
	wanted := m.active
	if wanted < index {
		wanted = 0
	} else {
		wanted -= index
	}
	m.switchTab(wanted)
	m.updateContext()
}

// closeTabsToRight closes the tabs after index: the tab at index becomes the
// last one. The active tab survives when it is on the kept side; an active tab
// that was closed hands the editor to the target tab. It reports false when it
// asked first instead of closing.
func (m *Model) closeTabsToRight(index int) bool {
	if index < 0 || index >= len(m.tabs)-1 {
		return false
	}
	if n := m.pendingGridChangesIn(m.tabs[index+1:]); n > 0 {
		m.openLossConfirm(confirmLossCloseRight, n, index, settings.Connection{})
		return false
	}
	m.forceCloseTabsToRight(index)
	return true
}

// forceCloseTabsToRight performs the close a loss confirmation protected.
func (m *Model) forceCloseTabsToRight(index int) {
	if index < 0 || index >= len(m.tabs)-1 {
		return
	}
	m.tabs = m.tabs[:index+1]
	wanted := m.active
	if wanted > index {
		wanted = index
	}
	m.switchTab(wanted)
	m.updateContext()
}

// closeAllTabs closes every tab, leaving the fresh welcome tab the × button
// leaves when the last tab goes away. It reports false when it asked first
// instead of closing.
func (m *Model) closeAllTabs() bool {
	if n := m.pendingGridChangesAll(); n > 0 {
		m.openLossConfirm(confirmLossCloseAll, n, -1, settings.Connection{})
		return false
	}
	m.forceCloseAllTabs()
	return true
}

// forceCloseAllTabs performs the close a loss confirmation protected.
func (m *Model) forceCloseAllTabs() {
	m.tabs = []*session{newWelcomeSession(time.Now())}
	m.switchTab(0)
	m.updateContext()
}

// pendingGridChangesIn is how many pending grid changes the given tabs hold.
func (m Model) pendingGridChangesIn(tabs []*session) int {
	n := 0
	for _, s := range tabs {
		n += s.pendingGridChanges()
	}
	return n
}

// pendingGridChangesExcept is how many pending grid changes every tab but keep
// holds.
func (m Model) pendingGridChangesExcept(keep int) int {
	n := 0
	for i, s := range m.tabs {
		if i == keep {
			continue
		}
		n += s.pendingGridChanges()
	}
	return n
}

// switchTab makes tab i the active one.
func (m *Model) switchTab(i int) {
	if i < 0 || i >= len(m.tabs) {
		return
	}
	m.active = i
	m.session = m.tabs[i]
	// A pointer gesture never survives a tab switch. rowDrag is the one
	// flag no release can be trusted to end: some terminals do not send a
	// release for a button released outside the window, and a release sent
	// after the switch arrives for the *new* session — so the old session
	// would keep a drag armed from its own anchor, and the next press on
	// empty space would extend a selection from a row that is not on screen
	// anymore. The hover is a pointer view of a grid that is not drawn
	// either, and so is the welcome page's hover. Both are cleared in every
	// tab, not just the one left: the release that may still arrive clears
	// the active session only.
	for _, s := range m.tabs {
		s.result.rowDrag = false
		s.result.hover = -1
		s.welcomeHover = -1
	}
	m.closeFind()
	m.closeResultMenu()
	m.colResize.active = false
	m.colHover = -1
	m.scrollDrag.active = false
	// The result grid's horizontal bar and the pane dividers keep their drag
	// state in the model, not in the tab: the new tab's grid has different
	// columns and widths, so a surviving drag would scroll it with the old
	// tab's geometry. The editor's drag-selection and the multi-click counter
	// belong to the old tab's buffer for the same reason.
	m.hScrollDrag.active = false
	m.layout.dragging = dividerNone
	m.layout.dragMoved = false
	m.dragging = false
	m.clicks = 0
	m.ensureCursorVisible()
	m.ensureTabVisible()
}

// nextTab switches to the tab on the right, wrapping around.
func (m *Model) nextTab() {
	if len(m.tabs) > 1 {
		m.switchTab((m.active + 1) % len(m.tabs))
	}
}

// prevTab switches to the tab on the left, wrapping around.
func (m *Model) prevTab() {
	if len(m.tabs) > 1 {
		m.switchTab((m.active - 1 + len(m.tabs)) % len(m.tabs))
	}
}

// newTab opens an empty tab and switches to it.
func (m *Model) newTab() {
	sess := &session{createdAt: time.Now(), buf: textbuf.New(""), result: newResultState()}
	m.tabs = append(m.tabs, sess)
	m.switchTab(len(m.tabs) - 1)
	m.updateContext()
}

// newTabNamed opens a new tab with an automatically derived name and the given
// text, and switches to it, focused. The name is only a default: the user can
// rename the tab afterwards, and the name then replaces the derived one.
func (m *Model) newTabNamed(name, text string) {
	sess := &session{createdAt: time.Now(), name: name, buf: textbuf.New(text), result: newResultState()}
	m.tabs = append(m.tabs, sess)
	m.switchTab(len(m.tabs) - 1)
	m.clearSelection()
	m.layout.focus = paneEditor
	m.updateContext()
}

// duplicateTabAt copies the tab at index i into a new one right after it.
//
// The copy keeps the name of the original — it shows the same thing, so it
// must look the same — but not its file: a copy is a new buffer, and saving
// it must never overwrite the original file.
func (m *Model) duplicateTabAt(i int) {
	if i < 0 || i >= len(m.tabs) {
		return
	}
	src := m.tabs[i]
	name := src.name
	if name == "" && src.fileName != "" {
		// The original is named after its file; the copy keeps that name as
		// its own default, so both tabs read the same.
		name = filepath.Base(src.fileName)
	}
	sess := &session{
		createdAt: time.Now(),
		name:      name,
		userName:  src.userName,
		buf:       textbuf.New(src.buf.Text()),
		result:    newResultState(),
	}
	m.tabs = append(m.tabs, nil)
	copy(m.tabs[i+2:], m.tabs[i+1:])
	m.tabs[i+1] = sess
	m.switchTab(i + 1)
	m.clearSelection()
	m.layout.focus = paneEditor
	m.updateContext()
}

// closeTab closes the active tab.
func (m *Model) closeTab() { m.closeTabAt(m.active) }

// closeTabAt closes tab i. Closing the last tab opens the welcome page instead
// of exiting: quitting stays Ctrl+Q's job (docs/ux-round7.md, §2).
//
// A tab whose grid still holds pending changes is not closed before the user
// confirms that those changes may be dropped; it reports false in that case so
// the caller does not report the tab as closed.
func (m *Model) closeTabAt(i int) bool {
	if i < 0 || i >= len(m.tabs) {
		return false
	}
	if n := m.tabs[i].pendingGridChanges(); n > 0 {
		m.openLossConfirm(confirmLossCloseTab, n, i, settings.Connection{})
		return false
	}
	m.forceCloseTabAt(i)
	return true
}

// forceCloseTabAt closes tab i without asking: the caller has already
// confirmed that the tab's pending changes may go.
func (m *Model) forceCloseTabAt(i int) {
	if i < 0 || i >= len(m.tabs) {
		return
	}
	m.tabs = append(m.tabs[:i], m.tabs[i+1:]...)
	if len(m.tabs) == 0 {
		m.tabs = []*session{newWelcomeSession(time.Now())}
		i = 0
	}
	next := m.active
	switch {
	case i < m.active:
		next = m.active - 1 // the active tab shifted one to the left
	case i == m.active:
		next = min(i, len(m.tabs)-1)
	}
	m.switchTab(next)
	m.updateContext()
}
