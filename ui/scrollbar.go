package ui

// scrollbar.go draws and drives the one-column scroll indicator that shows
// where a pane is looking at its content. Terminals have no native scrollbars,
// so this is a track with a thumb: the thumb size is the visible fraction and
// its position is the scroll offset. The thumb can be dragged, and clicking the
// track jumps to that position.

// scrollPane identifies the pane whose scrollbar is being dragged.
type scrollPane int

const (
	scrollPaneNone scrollPane = iota
	scrollPaneObjects
	scrollPaneResults
)

// scrollbarMetrics is the geometry shared by rendering and hit testing.
type scrollbarMetrics struct {
	x, y        int // screen position of the track's top-left cell
	height      int // track height in rows
	total       int // content rows
	visible     int // rows that fit in the pane
	offset      int // first visible content row
	thumbTop    int // thumb position relative to the track
	thumbHeight int
}

// travel is the number of content rows the view can scroll.
func (s scrollbarMetrics) travel() int { return max(0, s.total-s.visible) }

// span is the number of track rows the thumb can move through.
func (s scrollbarMetrics) span() int { return max(0, s.height-s.thumbHeight) }

// offsetAt maps a track row to a content offset with the thumb centred on it.
func (s scrollbarMetrics) offsetAt(y int) int {
	if s.span() == 0 {
		return 0
	}
	top := clamp(y-s.y-s.thumbHeight/2, 0, s.span())
	return clamp(top*s.travel()/s.span(), 0, s.travel())
}

// scrollbarDrag is the drag in progress.
type scrollbarDrag struct {
	active      bool
	pane        scrollPane
	startY      int
	startOffset int
	total       int
	visible     int
	trackHeight int
	thumbHeight int
}

// scrollThumb returns the thumb's top row and height.
func scrollThumb(height, total, visible, offset int) (top, thumb int) {
	if height < 1 || total <= visible || visible < 1 {
		return 0, 0
	}
	thumb = max(1, height*visible/total)
	thumb = min(thumb, height)
	span := max(1, height-thumb)
	travel := max(1, total-visible)
	top = offset * span / travel
	if top+thumb > height {
		top = height - thumb
	}
	return max(0, top), thumb
}

// scrollBar renders one character per row of a pane body.
//
// height is the number of rows to draw, total the number of content rows,
// visible how many of them fit and offset the first visible one.
func (m Model) scrollBar(height, total, visible, offset int) []string {
	out := make([]string, height)
	for i := range out {
		out[i] = m.theme.scrollTrack.Render("░")
	}
	if height < 1 || total <= visible {
		// Everything fits: no thumb, and the track stays as a quiet reminder
		// that the pane can scroll.
		if total <= visible {
			for i := range out {
				out[i] = " "
			}
		}
		return out
	}

	top, thumb := scrollThumb(height, total, visible, offset)
	for i := 0; i < thumb; i++ {
		out[top+i] = m.theme.scrollThumb.Render("█")
	}
	return out
}

// objectsScrollBar draws the indicator for the object browser.
func (m Model) objectsScrollBar(rect Rect) []string {
	body := rect.H - 1
	if body < 1 {
		return nil
	}
	rows := m.objectsRows()
	return m.scrollBar(body, len(rows), body, m.objects.scroll)
}

// resultsScrollBar draws the indicator for the result grid.
func (m Model) resultsScrollBar(rect Rect) []string {
	r := m.session.result
	if r.res == nil {
		return nil
	}
	visible := m.resultsVisibleRows()
	return m.scrollBar(visible, len(r.viewRows()), visible, r.scrollY)
}

// objectsScrollMetrics returns the object scrollbar geometry, or false when
// everything fits and there is nothing to drag.
func (m Model) objectsScrollMetrics() (scrollbarMetrics, bool) {
	if !m.layout.objectsVisible {
		return scrollbarMetrics{}, false
	}
	r := m.paneRects().objects
	height := r.H - 1
	if height < 1 {
		return scrollbarMetrics{}, false
	}
	rows := len(m.objectsRows())
	if rows <= height {
		return scrollbarMetrics{}, false
	}
	top, thumb := scrollThumb(height, rows, height, m.objects.scroll)
	return scrollbarMetrics{
		x:           r.X + r.W - 1,
		y:           r.Y + 1,
		height:      height,
		total:       rows,
		visible:     height,
		offset:      m.objects.scroll,
		thumbTop:    top,
		thumbHeight: thumb,
	}, true
}

// resultsScrollMetrics returns the result-grid scrollbar geometry. The track
// covers only data rows, below the summary, header and type rows.
func (m Model) resultsScrollMetrics() (scrollbarMetrics, bool) {
	if !m.layout.resultsVisible {
		return scrollbarMetrics{}, false
	}
	r := m.session.result
	if r.res == nil {
		return scrollbarMetrics{}, false
	}
	height := m.resultsVisibleRows()
	total := len(r.viewRows())
	if height < 1 || total <= height {
		return scrollbarMetrics{}, false
	}
	top, thumb := scrollThumb(height, total, height, r.scrollY)
	return scrollbarMetrics{
		x:           m.paneRects().results.X + m.paneRects().results.W - 1,
		y:           m.resultDataTopY(),
		height:      height,
		total:       total,
		visible:     height,
		offset:      r.scrollY,
		thumbTop:    top,
		thumbHeight: thumb,
	}, true
}

// hScrollbarMetrics is the geometry of the result grid's horizontal scrollbar,
// shared by rendering and hit testing. The track maps cells, so the thumb's
// width says how much of the grid fits and its position says how far right the
// view is.
type hScrollbarMetrics struct {
	x, y       int // screen position of the track's first cell
	width      int // track width in cells
	total      int // content width in cells
	visible    int // cells that fit in the pane
	offset     int // content cells hidden to the left
	thumbLeft  int // thumb position relative to the track
	thumbWidth int
}

// offsetAt maps a track cell to a content offset with the thumb centred on it.
func (h hScrollbarMetrics) offsetAt(x int) int {
	span := max(0, h.width-h.thumbWidth)
	if span == 0 {
		return 0
	}
	left := clamp(x-h.x-h.thumbWidth/2, 0, span)
	travel := max(0, h.total-h.visible)
	return clamp(left*travel/span, 0, travel)
}

// hScrollbarDrag is the horizontal drag in progress.
type hScrollbarDrag struct {
	active      bool
	startX      int
	startOffset int
	total       int
	visible     int
	trackWidth  int
	thumbWidth  int
}

// resultsHScrollMetrics returns the horizontal scrollbar geometry, or false
// when the grid fits or there is no room for the bar.
func (m Model) resultsHScrollMetrics() (hScrollbarMetrics, bool) {
	if !m.resultsHScrollBarShown() {
		return hScrollbarMetrics{}, false
	}
	rect := m.paneRects().results
	gutter := m.resultsGutterWidth()
	track := rect.W - gutter - 1 // the rightmost column belongs to the vertical bar
	if track < 3 {
		return hScrollbarMetrics{}, false
	}
	content := m.resultContentWidth()
	visible := m.resultsGridWidth()
	offset := m.resultColumnOffset(m.session.result.scrollX)
	thumbLeft, thumbWidth := scrollThumb(track, content, visible, offset)
	return hScrollbarMetrics{
		x:          rect.X + gutter,
		y:          m.resultsHScrollBarY(),
		width:      track,
		total:      content,
		visible:    visible,
		offset:     offset,
		thumbLeft:  thumbLeft,
		thumbWidth: thumbWidth,
	}, true
}

// beginHScrollDrag starts a horizontal drag. Clicking the track jumps the thumb
// to the pointer first, so a single click and a drag share one interaction.
func (m *Model) beginHScrollDrag(hs hScrollbarMetrics, x int) {
	offset := hs.offset
	if x < hs.x+hs.thumbLeft || x >= hs.x+hs.thumbLeft+hs.thumbWidth {
		offset = hs.offsetAt(x)
		m.setResultScrollOffset(offset)
	}
	m.hScrollDrag = hScrollbarDrag{
		active:      true,
		startX:      x,
		startOffset: offset,
		total:       hs.total,
		visible:     hs.visible,
		trackWidth:  hs.width,
		thumbWidth:  hs.thumbWidth,
	}
}

// dragHScroll maps the pointer movement onto a content offset.
func (m *Model) dragHScroll(x int) {
	d := m.hScrollDrag
	if !d.active {
		return
	}
	span := max(1, d.trackWidth-d.thumbWidth)
	travel := max(0, d.total-d.visible)
	offset := d.startOffset + (x-d.startX)*travel/span
	m.setResultScrollOffset(clamp(offset, 0, travel))
}

// finishHScrollDrag ends a horizontal scrollbar drag.
func (m *Model) finishHScrollDrag() { m.hScrollDrag.active = false }

// setResultScrollOffset moves the grid to the column that holds the given
// content offset. At the very end of the track the view shows as many trailing
// columns as fit — column widths are not uniform, so the column under the
// thumb's end can otherwise leave the last column out of reach.
func (m *Model) setResultScrollOffset(offset int) {
	r := &m.session.result
	if r.res == nil || len(r.colWidths) == 0 {
		return
	}
	scrollX := clamp(m.resultScrollXAtOffset(offset), 0, len(r.colWidths)-1)
	if travel := max(0, m.resultContentWidth()-m.resultsGridWidth()); offset >= travel {
		scrollX = m.resultLastPageScrollX()
	}
	r.scrollX = scrollX
}

// resultLastPageScrollX is the first column of the last page: the smallest
// index whose columns still fit into the pane, so the final scroll position
// shows the grid's right edge instead of a single leftover column.
func (m Model) resultLastPageScrollX() int {
	r := m.session.result
	if r.res == nil || len(r.colWidths) == 0 {
		return 0
	}
	width := m.resultsGridWidth()
	used := 0
	i := len(r.colWidths) - 1
	for i >= 0 {
		w := r.colWidths[i] + 1
		if used+w > width && used > 0 {
			break
		}
		used += w
		i--
	}
	return clamp(i+1, 0, len(r.colWidths)-1)
}

// resultsScrollEdge is the screen rectangle of the results scrollbar track.
func (m Model) resultsScrollEdge() Rect {
	if s, ok := m.resultsScrollMetrics(); ok {
		return Rect{X: s.x, Y: s.y, W: 1, H: s.height}
	}
	return Rect{}
}

// scrollbarAt returns the scrollbar under a screen point.
func (m Model) scrollbarAt(x, y int) (scrollPane, scrollbarMetrics, bool) {
	if s, ok := m.objectsScrollMetrics(); ok &&
		x == s.x && y >= s.y && y < s.y+s.height {
		return scrollPaneObjects, s, true
	}
	if s, ok := m.resultsScrollMetrics(); ok &&
		x == s.x && y >= s.y && y < s.y+s.height {
		return scrollPaneResults, s, true
	}
	return scrollPaneNone, scrollbarMetrics{}, false
}

// beginScrollDrag starts a drag. Clicking the track jumps the thumb to the
// pointer first, so a single click and a drag share one interaction.
func (m *Model) beginScrollDrag(pane scrollPane, s scrollbarMetrics, y int) {
	offset := s.offset
	if y < s.y+s.thumbTop || y >= s.y+s.thumbTop+s.thumbHeight {
		offset = s.offsetAt(y)
		m.setScrollOffset(pane, offset)
	}
	m.scrollDrag = scrollbarDrag{
		active:      true,
		pane:        pane,
		startY:      y,
		startOffset: offset,
		total:       s.total,
		visible:     s.visible,
		trackHeight: s.height,
		thumbHeight: s.thumbHeight,
	}
}

// dragScroll maps the pointer movement onto a content offset.
func (m *Model) dragScroll(y int) {
	d := m.scrollDrag
	if !d.active {
		return
	}
	span := max(1, d.trackHeight-d.thumbHeight)
	travel := max(0, d.total-d.visible)
	offset := d.startOffset + (y-d.startY)*travel/span
	m.setScrollOffset(d.pane, clamp(offset, 0, travel))
}

// finishScrollDrag ends a scrollbar drag.
func (m *Model) finishScrollDrag() { m.scrollDrag.active = false }

// setScrollOffset applies an offset to the pane being dragged.
func (m *Model) setScrollOffset(pane scrollPane, offset int) {
	switch pane {
	case scrollPaneObjects:
		rows := len(m.objectsRows())
		visible := m.objectsBodyHeight()
		m.objects.scroll = clamp(offset, 0, max(0, rows-visible))
	case scrollPaneResults:
		r := &m.session.result
		rows := len(r.viewRows())
		visible := m.resultsVisibleRows()
		r.scrollY = clamp(offset, 0, max(0, rows-visible))
	}
}
