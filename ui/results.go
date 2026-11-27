package ui

// results.go models one tab's result grid: the snapshot returned by the driver,
// column widths, sorting, filtering, scrolling and the caret. Rendering lives
// in results_render.go, input in results_input.go and clipboard operations in
// results_copy.go.

import (
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/driver"
)

// Column widths of a result grid: wide enough for the data, never so wide that
// a single value pushes the rest out of the pane.
const (
	minColWidth     = 3
	maxColWidth     = 40  // automatic width, so one wide value cannot dominate the grid
	maxUserColWidth = 120 // manual column resizing may go wider
)

// connRef identifies the connection a result came from: the name the user
// knows it by and the connection object itself.
//
// The object is what makes the identity exact. Two connections may carry the
// same name (the settings hold one entry per name, but a connection can be
// reopened), and reconnecting to the same database produces a new object —
// which is a new session, and the rows of the old session must not be
// written through it.
type connRef struct {
	name string
	conn driver.Connection
}

// same reports whether two references point at the same connection.
func (c connRef) same(other connRef) bool {
	return c.name == other.name && c.conn == other.conn
}

// resultOrigin describes the connection a result was produced by: the
// connection itself and the schema its unqualified names resolve in.
type resultOrigin struct {
	// conn identifies the connection.
	conn connRef
	// schema is the schema the connection works in — the first entry of the
	// server's search path, so an unqualified table name resolves there.
	// Empty means the server default.
	schema string
}

// resultState is what the last execution of a tab produced, plus how the grid
// looks at it: a filter and a sort never touch the result itself, so the
// original order can always come back.
type resultState struct {
	res  *driver.Result // nil until something ran
	err  error
	stmt string

	// origin describes the connection the result came from. A pending
	// change belongs to that connection: it must never be applied to
	// another database.
	origin resultOrigin

	colWidths []int // derived from the result, so rendering stays cheap
	// colPrefs remembers manually resized columns by name, so re-running a
	// query (or switching tabs and back) keeps the layout the user chose.
	colPrefs  map[string]int
	scrollY   int // first visible row
	scrollX   int // first visible column
	cursorRow int
	cursorCol int

	filter    string // substring filter, case-insensitive
	filtering bool   // the filter input is being edited
	filterAt  int    // caret inside the filter input
	sortCol   int    // sorted column, -1 for the database order
	sortDesc  bool

	// selAnchor is where the row selection starts, in view positions (the
	// same index space as cursorRow and viewRows); -1 means nothing is
	// selected — and every state starts there (newResultState, setResult),
	// because the zero value would read as "row 0 through the caret".
	// Anchor and cursor form the inclusive selected span, so the
	// highlight always sits on the rows it was made on — sorting or
	// filtering rebuilds the view, and selKey tells when that happened:
	// a selection whose view is gone drops itself instead of naming other
	// rows (round 7 §3).
	selAnchor int
	selKey    viewKey
	// hover is the view position under the pointer, -1 when the pointer is
	// outside the grid. Hover only draws: it never moves the caret and never
	// touches the selection (round 7 §1).
	hover int
	// rowDrag is true while a left-button drag that started on a result row
	// is extending the selection. It ends with the button release; the focus
	// check in the mouse handler keeps a stale flag from stealing a drag that
	// belongs to another pane.
	rowDrag bool

	// src describes the table the result was read from. It is set only for a
	// result that can be edited (see grid_edit.go).
	src *gridSource
	// pending holds the edited cells that have not reached the database yet.
	// They are addressed by the row of the original result, so sorting and
	// filtering cannot move a change onto another row.
	pending []pendingEdit
	// editing is the cell being edited right now, nil when the grid is not
	// in edit mode.
	editing *cellEdit
	// applyNotice is the message the result pane shows after a successful
	// apply, until the refreshed rows replace the result.
	applyNotice string
}

// newResultState returns the result state a brand-new session starts with.
// Two fields are not zero by intent: selAnchor reads as "row 0 through the
// caret" when it is 0, so a fresh session would answer clearRowSelection
// with true and the first Esc in the grid would clear a selection nobody made
// instead of leaving the pane — and hover reads as "row 0 is under the
// pointer" when it is 0, which would draw a highlight no pointer ever asked
// for. -1 is the no-selection/no-hover state every reset path (setResult
// included) already writes.
func newResultState() resultState { return resultState{selAnchor: -1, hover: -1} }

// viewKey is what the grid's view order is derived from: the filter text and
// the sort. Two states with the same result, filter and sort show the same
// rows in the same order, so comparing the key answers "did the view change?"
// without rebuilding it — cheap enough to run on every grid synchronisation.
type viewKey struct {
	filter   string
	sortCol  int
	sortDesc bool
}

// viewKey returns the input the current view order derives from.
func (r resultState) viewKey() viewKey {
	return viewKey{filter: r.filter, sortCol: r.sortCol, sortDesc: r.sortDesc}
}

// columnResizeState tracks a drag on a result column boundary.
type columnResizeState struct {
	active bool
	col    int
	startX int
	startW int
}

// setResult stores a new outcome and puts the view back at the top left.
func (r *resultState) setResult(res *driver.Result, err error, stmt string) {
	r.res, r.err, r.stmt = res, err, stmt
	r.scrollY, r.scrollX, r.cursorRow, r.cursorCol = 0, 0, 0, 0
	r.filter, r.filtering, r.filterAt = "", false, 0
	r.sortCol, r.sortDesc = -1, false
	// A new result is a new view: the selection, the hover and the drag of
	// the old one addressed rows that are not on screen anymore.
	r.selAnchor, r.hover, r.rowDrag = -1, -1, false
	r.selKey = r.viewKey()
	// A new result replaces the old one, so the edits of the old one are
	// gone with it: they belonged to rows that are not on screen anymore.
	r.src, r.pending, r.editing, r.applyNotice = nil, nil, nil, ""
	r.colWidths = columnWidths(res)
	r.applyColPrefs()
}

// setResults stores every statement outcome from one execution. Each set keeps
// its own view state, so sorting or filtering one result does not change the
// others. The last row-returning set becomes active, preserving the previous
// behaviour for scripts: the rows people usually care about are already on
// screen.
//
// The catalog is needed to find out which results can be edited: it says
// which table the rows came from. The origin says which connection produced
// them, so a pending change cannot end up in another database.
func (s *session) setResults(results []*driver.Result, err error, stmt string, cat *catalog.Catalog, origin resultOrigin) {
	if err != nil || len(results) == 0 {
		s.resultSets = nil
		s.resultIdx = 0
		s.result.setResult(nil, err, stmt)
		s.result.origin = origin
		return
	}

	// Column widths are a per-column preference, not a property of one result
	// set, so all statements share the same map.
	prefs := s.result.colPrefs
	if prefs == nil {
		prefs = map[string]int{}
	}
	sets := make([]resultState, len(results))
	for i, res := range results {
		sets[i].colPrefs = prefs
		sets[i].setResult(res, nil, stmt)
		sets[i].origin = origin
		sets[i].src = resultSource(stmt, cat, res, origin)
	}
	idx := len(sets) - 1
	for i, set := range sets {
		if set.res != nil && len(set.res.Columns) > 0 {
			idx = i
		}
	}
	s.resultSets = sets
	s.resultIdx = idx
	s.result = sets[idx]
	s.result.colPrefs = prefs
}

// resultSetCount is the number of statement outcomes from the last execution.
// It is zero before anything ran.
func (s *session) resultSetCount() int { return len(s.resultSets) }

// selectResultSet switches to another statement outcome, saving the current
// set's sort, filter, scroll and caret first. Indexes outside the list are
// ignored.
func (s *session) selectResultSet(i int) bool {
	if i < 0 || i >= len(s.resultSets) || i == s.resultIdx {
		return false
	}
	s.resultSets[s.resultIdx] = s.result
	s.result = s.resultSets[i]
	s.resultIdx = i
	return true
}

// selectRelativeResultSet moves by delta sets and wraps around, which makes
// both keyboard navigation and repeated context-menu use predictable.
func (s *session) selectRelativeResultSet(delta int) bool {
	n := len(s.resultSets)
	if n < 2 || delta == 0 {
		return false
	}
	idx := (s.resultIdx + delta) % n
	if idx < 0 {
		idx += n
	}
	return s.selectResultSet(idx)
}

// applyColPrefs overlays user-resized widths on a freshly measured result.
func (r *resultState) applyColPrefs() {
	if r.res == nil || len(r.colPrefs) == 0 {
		return
	}
	for i, c := range r.res.Columns {
		if w, ok := r.colPrefs[c.Name]; ok {
			r.colWidths[i] = clamp(w, minColWidth, maxUserColWidth)
		}
	}
}

// setColumnWidth stores a manual width for one column.
func (r *resultState) setColumnWidth(col, width int) {
	if r.res == nil || col < 0 || col >= len(r.colWidths) {
		return
	}
	width = clamp(width, minColWidth, maxUserColWidth)
	r.colWidths[col] = width
	if r.colPrefs == nil {
		r.colPrefs = map[string]int{}
	}
	r.colPrefs[r.res.Columns[col].Name] = width
}

// resetColumnWidth goes back to the width measured from the data.
func (r *resultState) resetColumnWidth(col int) {
	if r.res == nil || col < 0 || col >= len(r.colWidths) {
		return
	}
	if auto := columnWidths(r.res); col < len(auto) {
		r.colWidths[col] = auto[col]
	}
	if r.colPrefs != nil {
		delete(r.colPrefs, r.res.Columns[col].Name)
	}
}

// viewRows returns the row indices the grid shows: the rows that pass the
// filter, in sort order (or in the order the database returned them).
func (r resultState) viewRows() []int {
	if r.res == nil {
		return nil
	}
	out := make([]int, 0, len(r.res.Rows))
	for i := range r.res.Rows {
		if r.filter != "" && !rowMatches(r.res.Rows[i], r.filter) {
			continue
		}
		out = append(out, i)
	}
	if r.sortCol >= 0 {
		col, desc := r.sortCol, r.sortDesc
		sort.SliceStable(out, func(a, b int) bool {
			c := compareCells(cellAt(r.res.Rows[a], col), cellAt(r.res.Rows[b], col))
			if desc {
				return c > 0
			}
			return c < 0
		})
	}
	return out
}

// rowMatches reports whether any cell of a row contains the filter text.
func rowMatches(row []*string, filter string) bool {
	f := strings.ToLower(filter)
	for _, cell := range row {
		if cell != nil && strings.Contains(strings.ToLower(*cell), f) {
			return true
		}
	}
	return false
}

// cellAt returns a cell, or nil when the row is shorter than the column.
func cellAt(row []*string, col int) *string {
	if col < 0 || col >= len(row) {
		return nil
	}
	return row[col]
}

// compareCells orders two cells: numbers numerically, NULLs last and everything
// else as text.
func compareCells(a, b *string) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1 // NULLs go last, whatever the direction
	case b == nil:
		return -1
	}
	if x, err := strconv.ParseFloat(*a, 64); err == nil {
		if y, err := strconv.ParseFloat(*b, 64); err == nil {
			switch {
			case x < y:
				return -1
			case x > y:
				return 1
			default:
				return 0
			}
		}
	}
	return strings.Compare(strings.ToLower(*a), strings.ToLower(*b))
}

// ---- Row selection (round 7 §3) ----
//
// The selection is an inclusive span of *view* positions between selAnchor and
// cursorRow. Both ends live in view order, so a sort or a filter can never
// slide the highlight onto other data: instead, a view the selection was not
// made on drops it (see ensureResultVisible and selectionSpan).

// anchorResultSelection starts a selection at the caret, whatever was selected
// before: shift+click re-anchors on every press, the shift keys and the drag
// only anchor when there is none yet.
func (r *resultState) anchorResultSelection() {
	r.selAnchor = r.cursorRow
	r.selKey = r.viewKey()
}

// startResultSelection anchors the selection at the caret the first time a
// shift gesture or a drag reaches for another row. The caret is clamped to
// the view first: an anchor outside the rows (a caret left over from a
// result that shrank to nothing) would name a selection of rows that are
// not there, so an empty view starts no selection at all.
func (r *resultState) startResultSelection() {
	if r.selAnchor >= 0 {
		return
	}
	rows := len(r.viewRows())
	if rows == 0 {
		return
	}
	r.selAnchor = clamp(r.cursorRow, 0, rows-1)
	r.selKey = r.viewKey()
}

// clearRowSelection drops the row selection and reports whether there was
// one. Esc leaves the selection first and the pane only afterwards, and a
// fresh caret placement (a plain click, a right click) clears it the way a
// plain keystroke does.
func (r *resultState) clearRowSelection() bool {
	if r.selAnchor < 0 {
		return false
	}
	r.selAnchor = -1
	return true
}

// hasRowSelection reports whether the grid has rows selected — rows that
// really exist in the view, so an anchor that outlived its result (or a
// span whose rows are all filtered away) is not a selection a copy could
// act on.
func (r resultState) hasRowSelection() bool {
	if _, _, ok := r.selectionSpan(); !ok {
		return false
	}
	return r.selectedRowCount() > 0
}

// selectionSpan returns the inclusive span of view positions the selection
// covers and whether there is one at all. Without a result there is no view
// to select in (the zero value of a session that has not run anything would
// otherwise read as "anchor 0"), and a selection whose filter or sort no
// longer matches the grid is not a selection either: it was made on a view
// that is gone, and its anchor would name different rows now.
func (r resultState) selectionSpan() (int, int, bool) {
	if r.res == nil || r.selAnchor < 0 || r.selKey != r.viewKey() {
		return 0, 0, false
	}
	return min(r.selAnchor, r.cursorRow), max(r.selAnchor, r.cursorRow), true
}

// selectionContains reports whether a view position is inside the selection.
func (r resultState) selectionContains(display int) bool {
	lo, hi, ok := r.selectionSpan()
	return ok && display >= lo && display <= hi
}

// selectedRowCount is how many rows of the current view are selected.
func (r resultState) selectedRowCount() int {
	lo, hi, ok := r.selectionSpan()
	if !ok {
		return 0
	}
	n := len(r.viewRows())
	if n == 0 {
		return 0
	}
	lo, hi = max(lo, 0), min(hi, n-1)
	if lo > hi {
		return 0
	}
	return hi - lo + 1
}

// handleResultSelectionKey extends the row selection with the shift navigation
// keys. It is called from the results branch of editor_keys.go, which is why
// it lives next to the rest of the grid's selection state. While the filter or
// the cell editor owns the keyboard these keys never arrive.
func (m Model) handleResultSelectionKey(key string) (Model, tea.Cmd) {
	r := &m.session.result
	if r.res == nil {
		return m, nil
	}
	rows := len(r.viewRows())
	if rows == 0 {
		return m, nil // nothing to anchor a selection on
	}
	target := r.cursorRow
	switch key {
	case "shift+up":
		target--
	case "shift+down":
		target++
	case "shift+home":
		target = 0
	case "shift+end":
		target = rows - 1
	default:
		return m, nil
	}
	// The anchor is the caret the gesture started from; only the first press
	// sets it, so extending and shrinking walk around the same anchor.
	r.startResultSelection()
	r.cursorRow = clamp(target, 0, max(0, rows-1))
	m.ensureResultVisible()
	return m, nil
}

// extendResultSelection moves the caret to a row during a pointer drag,
// anchoring the selection at the row the drag started from when there is none.
// It deliberately does not go through moveCursor: a plain move drops the
// selection, and this gesture *is* the selection.
func (m *Model) extendResultSelection(display int) {
	r := &m.session.result
	if r.res == nil {
		return
	}
	rows := len(r.viewRows())
	if rows == 0 {
		return
	}
	r.startResultSelection()
	r.cursorRow = clamp(display, 0, rows-1)
	m.ensureResultVisible()
}

// toggleSort cycles a column through the three grid states: ascending,
// descending, then back to the database order. The third state is important:
// once a result has been sorted there must be a way to see the original rows
// again without re-running the query.
func (m *Model) toggleSort(col int) {
	r := &m.session.result
	if r.res == nil || col < 0 || col >= len(r.res.Columns) {
		return
	}
	switch {
	case r.sortCol != col:
		r.sortCol, r.sortDesc = col, false
	case !r.sortDesc:
		r.sortDesc = true
	default:
		r.sortCol, r.sortDesc = -1, false
	}
	m.ensureResultVisible()
}

// empty reports whether nothing has been executed yet.
func (r resultState) empty() bool { return r.res == nil && r.err == nil }

// columnWidths measures the columns of a result. It measures terminal cells,
// not runes: a wide glyph (CJK, emoji) occupies two columns, and counting it as
// one made the column too narrow, so the renderer clipped values that would
// have fit. The measured text is the cleaned text the grid draws (foldBreaks),
// so an escape sequence removed before drawing does not widen the column.
func columnWidths(res *driver.Result) []int {
	if res == nil {
		return nil
	}
	widths := make([]int, len(res.Columns))
	for i, c := range res.Columns {
		// The header carries the name and, underneath it, the type: both must
		// fit, or the type gets cut into something it is not.
		w := max(cellWidth(foldBreaks(c.Name)), cellWidth(foldBreaks(c.Type)))
		widths[i] = clamp(w, minColWidth, maxColWidth)
	}
	for _, row := range res.Rows {
		for i, cell := range row {
			if i >= len(widths) {
				break
			}
			n := 0
			if cell != nil {
				n = cellWidth(foldBreaks(*cell))
			}
			if n > widths[i] {
				widths[i] = n
			}
		}
	}
	for i := range widths {
		widths[i] = clamp(widths[i], minColWidth, maxColWidth)
	}
	return widths
}

// cellWidth is the number of terminal cells a cleaned cell text occupies.
func cellWidth(cleaned string) int {
	return lipgloss.Width(cleaned)
}

// moveCursor moves the grid caret and keeps it in view. A plain move ends the
// row selection the way a plain move ends the editor's selection: without a
// shift the caret is not reaching for anything (round 7 §3). The shift
// gestures and the pointer drag set cursorRow themselves and keep the anchor.
func (m *Model) moveCursor(dx, dy int) {
	r := &m.session.result
	if r.res == nil {
		return
	}
	r.selAnchor = -1
	r.cursorCol = clamp(r.cursorCol+dx, 0, len(r.res.Columns)-1)
	r.cursorRow = clamp(r.cursorRow+dy, 0, max(0, len(r.viewRows())-1))
	m.ensureResultVisible()
}

// ensureResultVisible scrolls the grid so that the caret cell is visible.
// It is also where every filter and sort change is observed: those rebuild
// the view (from the filter input, the context menu, the header click or the
// `s` key alike) and an anchor kept across that would name different rows, so
// the selection drops itself here — the one place all of them pass through.
func (m *Model) ensureResultVisible() {
	r := &m.session.result
	if r.selAnchor >= 0 && r.selKey != r.viewKey() {
		// The view was rebuilt: a selection in view positions would now name
		// different rows, so it is dropped (round 7 §3).
		r.selAnchor = -1
	}
	scrollBefore := r.scrollY
	cols, rows := len(r.colWidths), len(r.viewRows())
	visibleRows := m.resultsVisibleRows()
	if r.cursorRow < r.scrollY {
		r.scrollY = r.cursorRow
	}
	if r.cursorRow >= r.scrollY+visibleRows {
		r.scrollY = r.cursorRow - visibleRows + 1
	}
	r.scrollY = clamp(r.scrollY, 0, max(0, rows-visibleRows))
	if r.cursorCol >= cols {
		r.cursorCol = max(0, cols-1)
	}

	visibleCols := len(m.visibleResultCols())
	if visibleCols < 1 {
		visibleCols = 1
	}
	if r.cursorCol < r.scrollX {
		r.scrollX = r.cursorCol
	}
	if r.cursorCol >= r.scrollX+visibleCols {
		r.scrollX = r.cursorCol - visibleCols + 1
	}
	r.scrollX = clamp(r.scrollX, 0, max(0, cols-visibleCols))

	if r.scrollY != scrollBefore && r.hover >= 0 {
		// The rows moved under a still pointer (a wheel tick, a caret move,
		// a scrollbar drag): hover is a *view* position derived from the
		// screen, so the old index now names another row. Dropping it keeps
		// a stale highlight off a row the pointer never entered; the next
		// motion event re-derives it (round 7 §1).
		r.hover = -1
	}
}

// visibleResultCols returns the columns that fit into the pane, starting at the
// horizontal scroll position.
func (m Model) visibleResultCols() []int {
	r := m.session.result
	width := m.paneRects().results.W - m.resultsGutterWidth() - 1
	var out []int
	used := 0
	for i := r.scrollX; i < len(r.colWidths); i++ {
		w := r.colWidths[i] + 1 // the separator column
		if used+w > width {
			break
		}
		used += w
		out = append(out, i)
	}
	return out
}

// resultsVisibleRows is how many data rows fit under the summary and header.
// When the grid is wider than the pane, the last row is the horizontal
// scrollbar, so one data row goes to it.
func (m Model) resultsVisibleRows() int {
	used := 2 // summary and header
	if m.resultsBodyHeight() >= 5 {
		used++ // the type row
	}
	rows := m.resultsBodyHeight() - used
	if m.resultsHScrollBarShown() {
		rows--
	}
	return max(1, rows)
}

// resultContentWidth is the width of every column plus its separator: the
// full width the grid would need without scrolling.
func (m Model) resultContentWidth() int {
	r := m.session.result
	if r.res == nil {
		return 0
	}
	w := 0
	for _, cw := range r.colWidths {
		w += cw + 1 // the separator column
	}
	return w
}

// resultsGridWidth is the width the columns may occupy: the pane minus the row
// gutter and the vertical scrollbar column.
func (m Model) resultsGridWidth() int {
	return max(1, m.paneRects().results.W-m.resultsGutterWidth()-1)
}

// resultsHScrollBarShown reports whether the grid is wider than its pane and
// there is room for a scrollbar row under the data. Without room the data rows
// keep the space: a bar no data row can make room for would hide cells anyway.
func (m Model) resultsHScrollBarShown() bool {
	if !m.layout.resultsVisible {
		return false
	}
	r := m.session.result
	if r.res == nil || r.err != nil || len(r.res.Columns) == 0 {
		return false
	}
	if m.resultContentWidth() <= m.resultsGridWidth() {
		return false
	}
	used := 2 // summary and header
	if m.resultsBodyHeight() >= 5 {
		used++ // the type row
	}
	return m.resultsBodyHeight()-used >= 2
}

// resultsHScrollBarY is the screen row of the horizontal scrollbar: the last
// body row of the results pane.
func (m Model) resultsHScrollBarY() int {
	return m.paneRects().results.Y + m.resultsBodyHeight()
}

// resultColumnOffset is the content width hidden to the left of scrollX, in
// cells. The horizontal scrollbar maps cells, not column indices, so a wide
// column takes the share of the track its width deserves.
func (m Model) resultColumnOffset(scrollX int) int {
	r := m.session.result
	if r.res == nil {
		return 0
	}
	offset := 0
	for i := 0; i < scrollX && i < len(r.colWidths); i++ {
		offset += r.colWidths[i] + 1
	}
	return offset
}

// resultScrollXAtOffset maps a content width (in cells, hidden to the left) to
// the first column that should be visible.
func (m Model) resultScrollXAtOffset(offset int) int {
	r := m.session.result
	if r.res == nil || len(r.colWidths) == 0 {
		return 0
	}
	offset = max(offset, 0)
	acc := 0
	for i, cw := range r.colWidths {
		if acc+cw+1 > offset {
			return i
		}
		acc += cw + 1
	}
	return len(r.colWidths) - 1
}

// resultsGutterWidth is the width of the row-number column.
func (m Model) resultsGutterWidth() int {
	n := 1
	if m.session.result.res != nil {
		n = len(strconv.Itoa(len(m.session.result.res.Rows)))
	}
	return clamp(n+1, 2, 6)
}

// resultsBodyHeight is the number of rows available under the pane title.
func (m Model) resultsBodyHeight() int {
	h := m.paneRects().results.H - 1
	if h < 1 {
		return 1
	}
	return h
}
