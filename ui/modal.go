package ui

// modal.go holds the geometry rules every modal dialog shares.
//
// A dialog used to size itself to its content, so each dialog invented its own
// margins and boxes drifted apart. The rules here give a new dialog no room to
// invent anything: it asks for the size it needs and modalSize decides what it
// may use.
//
// The shared frame convention (every modal obeys it):
//
//   - one blank row of padding inside the borders, right under the title;
//   - the title sits on the first content row;
//   - a hint line above the button row, and buttons on the last content row;
//   - rendering and mouse hit testing share the same computed rectangle, so
//     the drawn box and the clickable box always agree.

// The height floors: the useful minimum of any modal dialog is 12 rows
// (a box shorter than that cannot show the frame — title, body, hint,
// buttons — plus a row or two of content); the compact floor of 8 is the
// entry for dialogs that are a single field or a short confirmation (the
// unlock form, the demo connection form) — there the full minimum would
// pad a row or two of content with a wall of blank rows and make a
// finished feature look half-built. The width floor is shared: a box
// narrower than modalMinW cannot show a two-column form and a hint line.
const (
	modalMinW        = 60
	modalMaxW        = 100
	modalMinH        = 12
	modalMinHCompact = 8
)

// modalSize clamps the wanted size to what a dialog should use on a screen of
// the given size:
//
//   - width at least modalMinW, at most min(screenW-4, modalMaxW);
//   - height at least modalMinH, at most screenH.
//
// On a narrow screen the screen wins: the dialog is never drawn wider or
// taller than the area it lives in, and its content is expected to truncate
// or scroll. screenW/screenH is the size of the region between the menu bar
// and the status bar (two columns are left as side margins).
func modalSize(wantW, wantH, screenW, screenH int) (w, h int) {
	return modalSizeFloor(wantW, wantH, screenW, screenH, modalMinH)
}

// modalSizeCompact is the compact entry of the sizing rule: the same clamps
// as modalSize with the height floor of modalMinHCompact, for the dialogs
// whose content is one short form (a field or three, a confirmation) — see
// the floor comment above for why they may go below the full minimum. The
// width rules are deliberately identical.
func modalSizeCompact(wantW, wantH, screenW, screenH int) (w, h int) {
	return modalSizeFloor(wantW, wantH, screenW, screenH, modalMinHCompact)
}

// modalSizeFloor is modalSize with the minimum height spelled out.
func modalSizeFloor(wantW, wantH, screenW, screenH, minH int) (w, h int) {
	w = min(max(wantW, modalMinW), modalMaxW)
	if capW := screenW - 4; w > capW {
		// A screen narrower than the minimum sets the size itself.
		w = max(capW, 1)
	}
	h = max(wantH, minH)
	if screenH < h {
		h = max(screenH, 1)
	}
	return w, h
}

// modalRect centres a w×h box inside an area, so the status bar (and whatever
// sits behind the dialog) stays visible around it.
func modalRect(area Rect, w, h int) Rect {
	return Rect{
		X: area.X + max(0, (area.W-w)/2),
		Y: area.Y + max(0, (area.H-h)/2),
		W: w,
		H: h,
	}
}
