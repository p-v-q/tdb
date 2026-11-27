package ui

// layout.go defines the window layout:
//
//	┌──────────────────────────────────────┐
//	│ tdb File Edit View …   conn info  │  ← menu bar (macOS style)
//	│ toolbar                              │
//	├──────────┬───────────────────────────┤
//	│          │ tabs                      │  ← tabs belong to the editor
//	│ Objects  │ SQL Editor                │
//	│  (left)  │───────────────────────────│
//	│          │  Results (bottom)         │
//	└──────────┴───────────────────────────┘
//	│ status bar                           │
//
// The geometry is computed by a pure function (layoutState.compute) that both
// rendering and hit testing share, so what you see is what you click. Panes can
// be resized by dragging the dividers and closed with the ✕ button; a closed
// pane leaves an expand strip at the screen edge.

import (
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
)

// paneID identifies one of the panes.
type paneID int

// The panes, in focus order.
const (
	paneObjects paneID = iota // left-hand object browser
	paneEditor                // SQL editor (the main workspace, cannot be closed)
	paneResults               // bottom results pane
)

// titleKey returns the i18n key of the pane title.
func (p paneID) titleKey() i18n.Key {
	switch p {
	case paneObjects:
		return i18n.KeyPaneObjects
	case paneEditor:
		return i18n.KeyPaneEditor
	case paneResults:
		return i18n.KeyPaneResults
	}
	return i18n.KeyPaneEditor
}

// Size constraints: a pane must never squeeze its neighbours out of existence.
// The width/height bounds come from the settings package so the UI and the
// persisted preferences cannot drift apart.
const (
	minObjectsWidth  = settings.MinObjectsWidth
	minEditorWidth   = 20
	minEditorHeight  = 3
	minResultsHeight = settings.MinResultsHeight

	dividerSize     = 1 // a divider is one column/row wide
	expandStripSize = 1 // an expand strip is one column/row wide
	menuBarHeight   = 1 // the menu bar occupies one row
	toolbarHeight   = 1 // the toolbar sits right below the menu bar
	tabbarHeight    = 1 // the tab strip sits right below the toolbar
)

// Rect is a rectangle on screen (columns and rows are 0-based).
type Rect struct {
	X, Y, W, H int
}

// Contains reports whether the point lies inside the rectangle.
func (r Rect) Contains(x, y int) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// dividerKind identifies the divider being dragged.
type dividerKind int

// The dividers.
const (
	dividerNone       dividerKind = iota
	dividerVertical               // objects pane <-> editor
	dividerHorizontal             // editor <-> results pane
)

// layoutState is the mutable layout state: pane visibility and size, keyboard
// focus and the divider drag in progress.
type layoutState struct {
	objectsVisible bool
	objectsWidth   int
	resultsVisible bool
	resultsHeight  int
	focus          paneID // pane that owns the keyboard focus
	dragging       dividerKind
	// dragMoved is set once a divider drag has actually resized a pane. While
	// it is set the panes are drawn as empty frames: re-rendering a large
	// result grid or editor on every mouse motion is what made the drag lag.
	dragMoved bool
}

// resizing reports whether a divider drag is moving panes right now. While it
// is, the panes are drawn as empty frames and their contents are rendered again
// on release: re-rendering a large grid or editor on every mouse motion is what
// made resizing feel slow.
func (l layoutState) resizing() bool {
	return l.dragging != dividerNone && l.dragMoved
}

// defaultLayout returns the layout described by the factory settings.
func defaultLayout() layoutState { return layoutFromSettings(settings.Default()) }

// layoutFromSettings builds the initial layout from the stored preferences.
func layoutFromSettings(s settings.Settings) layoutState {
	return layoutState{
		objectsVisible: s.Layout.ObjectsVisible,
		objectsWidth:   s.Layout.ObjectsWidth,
		resultsVisible: s.Layout.ResultsVisible,
		resultsHeight:  s.Layout.ResultsHeight,
		focus:          paneEditor,
	}
}

// paneRects is the result of the layout computation: the screen rectangle of
// every pane plus its dividers and expand strips.
type paneRects struct {
	area          Rect // usable region between the tab bar and the status bar
	menubar       Rect // top menu bar
	toolbar       Rect // button bar below the menu bar
	tabbar        Rect // editor tabs below the toolbar
	objects       Rect
	dividerV      Rect // vertical divider, right of the objects pane
	objectsExpand Rect // left-hand expand strip while the objects pane is closed
	editor        Rect
	results       Rect
	dividerH      Rect // horizontal divider, above the results pane
	resultsExpand Rect // bottom expand strip while the results pane is closed
}

// compute maps the layout state onto concrete rectangles. Pure function, no
// side effects.
func (l layoutState) compute(width, height int) paneRects {
	// Top to bottom: menu bar (row 0) / toolbar (row 1) / panes / status bar
	// (last row). The menu bar starts with the application name, macOS style,
	// and carries the session info on its right-hand side. The tab strip is
	// part of the editor column, not a full-width application chrome row.
	chrome := 1 + menuBarHeight + toolbarHeight
	area := Rect{
		X: 0,
		Y: menuBarHeight + toolbarHeight,
		W: max(1, width),
		H: max(1, height-chrome),
	}
	r := paneRects{
		area:    area,
		menubar: Rect{X: 0, Y: 0, W: max(1, width), H: menuBarHeight},
		toolbar: Rect{X: 0, Y: menuBarHeight, W: max(1, width), H: toolbarHeight},
	}

	x := area.X
	if l.objectsVisible {
		ow := clamp(l.objectsWidth, minObjectsWidth,
			max(minObjectsWidth, area.W-minEditorWidth-dividerSize))
		r.objects = Rect{X: x, Y: area.Y, W: ow, H: area.H}
		r.dividerV = Rect{X: x + ow, Y: area.Y, W: dividerSize, H: area.H}
		x += ow + dividerSize
	} else {
		r.objectsExpand = Rect{X: x, Y: area.Y, W: expandStripSize, H: area.H}
		x += expandStripSize
	}

	rightW := max(1, area.X+area.W-x)
	y, h := area.Y, area.H
	if l.resultsVisible {
		// The right column reserves one row for tabs and one for the editor
		// title, so the results pane cannot squeeze either away.
		maxResults := max(minResultsHeight, h-tabbarHeight-minEditorHeight-dividerSize)
		rh := clamp(l.resultsHeight, minResultsHeight, maxResults)
		r.results = Rect{X: x, Y: y + h - rh, W: rightW, H: rh}
		r.dividerH = Rect{X: x, Y: y + h - rh - dividerSize, W: rightW, H: dividerSize}
		h -= rh + dividerSize
	} else {
		r.resultsExpand = Rect{X: x, Y: y + h - expandStripSize, W: rightW, H: expandStripSize}
		h -= expandStripSize
	}
	r.tabbar = Rect{X: x, Y: y, W: rightW, H: tabbarHeight}
	r.editor = Rect{X: x, Y: y + tabbarHeight, W: rightW, H: max(1, h-tabbarHeight)}
	return r
}

// paneRects computes the current layout rectangles.
func (m Model) paneRects() paneRects { return m.layout.compute(m.width, m.height) }

// closeBtnRect returns the rectangle of a pane header's close button.
func closeBtnRect(r Rect) Rect {
	return Rect{X: r.X + r.W - 1, Y: r.Y, W: 1, H: 1}
}

// resizeDivider updates the pane sizes while dragging, honouring the minimum
// sizes.
func (m *Model) resizeDivider(r paneRects, x, y int) {
	switch m.layout.dragging {
	case dividerVertical:
		m.layout.objectsWidth = clamp(x-r.objects.X, minObjectsWidth,
			max(minObjectsWidth, r.area.W-minEditorWidth-dividerSize))
	case dividerHorizontal:
		bottom := r.area.Y + r.area.H
		m.layout.resultsHeight = clamp(bottom-y-dividerSize, minResultsHeight,
			max(minResultsHeight, r.area.H-minEditorHeight-dividerSize))
	}
}

// ---- Pane visibility and focus ----

// toggleObjects shows or hides the objects pane.
func (m *Model) toggleObjects() {
	m.layout.objectsVisible = !m.layout.objectsVisible
	if !m.layout.objectsVisible && m.layout.focus == paneObjects {
		m.endObjectsFilterInput()
		m.layout.focus = paneEditor
	}
}

// toggleResults shows or hides the results pane.
func (m *Model) toggleResults() {
	m.layout.resultsVisible = !m.layout.resultsVisible
	if !m.layout.resultsVisible && m.layout.focus == paneResults {
		m.layout.focus = paneEditor
	}
}

// closeFocusedPane closes the focused pane; the editor cannot be closed.
func (m *Model) closeFocusedPane() {
	switch m.layout.focus {
	case paneObjects:
		m.toggleObjects()
	case paneResults:
		m.toggleResults()
	}
}

// focusPane hands the keyboard focus to p, revealing p's pane first when it is
// hidden. Showing is one-way: unlike the toggles it never hides a pane, so
// repeating the command on a visible pane only moves the focus. It reports
// whether a pane was revealed, so the caller can persist the new layout.
func (m *Model) focusPane(p paneID) bool {
	revealed := false
	// Reveal before focusing: toggleObjects/toggleResults push the focus back
	// to the editor while hiding, so the focus must be set afterwards.
	switch p {
	case paneObjects:
		if !m.layout.objectsVisible {
			m.toggleObjects()
			revealed = true
		}
	case paneResults:
		if !m.layout.resultsVisible {
			m.toggleResults()
			revealed = true
		}
	}
	// Moving the focus away must also leave the object filter input, or it
	// would take the keyboard back on the next visit to the tree.
	m.endObjectsFilterInput()
	m.layout.focus = p
	switch p {
	case paneObjects:
		// Arriving with no usable selection would leave Right/Left (and Enter)
		// dead until the user presses Down first, so select the first row and
		// scroll it into view.
		if rows := m.objectsRows(); len(rows) > 0 && (m.objects.sel < 0 || m.objects.sel >= len(rows)) {
			m.objects.sel = 0
			m.ensureObjectsVisible(m.objectsBodyHeight())
		}
		// The keys now belong to the tree, so a completion popup left over
		// from the editor must not stay behind, still covering the editor.
		m.popup.close()
	case paneResults:
		// Same for the results pane; the editor keeps its popup (paneEditor
		// is deliberately not handled here).
		m.popup.close()
	}
	return revealed
}
