package ui

// render_property_test.go states the property every sanitizing test of this
// package is a special case of: whatever state the model is in, Model.View()
// may contain exactly two things — the '\n' between the frame's rows and the
// style sequences the program's own lipgloss styling emits. Every other
// control byte is text from outside the program (a database identifier, an
// error, a file name, a paste) reaching the terminal, where it would move the
// cursor, clear the screen or, as an OSC 52, write the clipboard.
//
// Why the whitelist is exactly this narrow (the rule the assertions below
// enforce):
//
//   - '\n' — View joins the canvas rows with it; it is the frame's structure.
//   - ESC [ ... m — SGR. lipgloss styles every pane, dialog, highlight and
//     border with it, so the frame is full of these by construction.
//   - Nothing else. In particular:
//     - ESC ] (OSC) is rejected as a whole: that is the shape of the OSC 52
//       clipboard hijack. TDB writes its own OSC 52 through a dedicated
//       writer (ui/clipboard.go), never into the frame, so no OSC — not
//       OSC 52, not the hyperlinks OSC 8, not OSC 7 — is emitted anywhere in
//       ui/. Granting a whitelist entry for a sequence no code path produces
//       would only widen the gap an unsanitized text could walk through, so
//       none is granted. If a renderer ever starts emitting one, it must be
//       added here together with its justification.
//     - Any CSI whose final byte is not 'm' is rejected (ESC[2J clears the
//       screen, ESC[<r>;<c>H parks the cursor). The frame is built from a
//       canvas of blank rows joined by newlines (see render.go), so it needs
//       no cursor addressing at all — a cursor-positioning CSI in the frame
//       would be a bug, not a rendering primitive.
//     - A bare ESC, the other C0 bytes (\r, \t, \b, BEL, …), DEL (0x7f) and
//       the C1 controls U+0080–U+009F are rejected.
//
// The editor buffer is the one deliberate exception of ui/sanitize.go ("it
// shows what the user typed, verbatim"); it is called out where it is
// exercised instead of being silently exempted here.

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/complete"
	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/history"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
	"github.com/p-v-q/tdb/sql"
	"github.com/p-v-q/tdb/vault"
)

// hostileViewText is the payload every case paints its data source with: the
// OSC 52 and the screen clear the repository's own grid regression tests use
// (escapeCellOSC, escapeCellClear), plus an SGR and a stray BEL inside the
// text — the BEL after the SGR is what a naive "drop the escape sequences"
// cleanup leaves behind, so a half-cleaned draw still fails here.
const hostileViewText = escapeCellOSC + escapeCellClear + "\x1b[31m\x07evil"

// hostileViewTextPlain is the printable tail of the payload: it survives every
// cleaner (stripControl, oneLine, foldBreaks, sanitizeMultiline), so a frame
// that still shows it proves the state drew its data instead of passing the
// property by drawing nothing.
const hostileViewTextPlain = "evil"

// ---- The property ----

// viewViolation locates the first byte of a rendered frame that is neither a
// frame newline nor a style sequence.
type viewViolation struct {
	line int    // 1-based line of the frame
	off  int    // 0-based byte offset inside that line
	why  string // what kind of control byte it is
	near string // the bytes around the offender, quoted
}

// sgrEnd returns the index just past the style sequence starting at line[i]
// (ESC [ … m), or -1 when what starts there is not a style sequence.
func sgrEnd(line string, i int) int {
	if i+1 >= len(line) || line[i+1] != '[' {
		return -1
	}
	j := i + 2
	// Parameter bytes (0x30–0x3f) and intermediate bytes (0x20–0x2f) before
	// the final byte of the sequence.
	for j < len(line) && line[j] >= 0x20 && line[j] <= 0x3f {
		j++
	}
	if j >= len(line) || line[j] != 'm' {
		return -1 // a CSI, but not SGR: ESC[2J, ESC[H, …
	}
	return j + 1
}

// escapeReason describes why an ESC at line[i] is not a style sequence, naming
// the sequence kind so a failure points at the product code that drew it.
func escapeReason(line string, i int) string {
	if i+1 >= len(line) {
		return "a lone ESC at the end of a line"
	}
	switch line[i+1] {
	case ']':
		return "an OSC sequence (the OSC-52 clipboard hijack shape; only SGR styling may start with ESC)"
	case '[':
		j := i + 2
		for j < len(line) && line[j] >= 0x20 && line[j] <= 0x3f {
			j++
		}
		if j < len(line) && line[j] >= 0x40 && line[j] <= 0x7e {
			return fmt.Sprintf("a CSI sequence ending in %q: ESC[...%q reaches the terminal as a screen/cursor control, only ESC[...m (SGR) is styling",
				line[j], line[j])
		}
		return "a malformed CSI sequence"
	default:
		return fmt.Sprintf("a lone ESC followed by %q (an escape sequence that is not styling)", line[i+1])
	}
}

// scanFrameLine reports the first violating byte of one frame line.
func scanFrameLine(line string, lineNo int) (viewViolation, bool) {
	fail := func(off int, why string) (viewViolation, bool) {
		lo, hi := max(0, off-14), min(len(line), off+18)
		return viewViolation{line: lineNo, off: off, why: why, near: line[lo:hi]}, true
	}
	for i := 0; i < len(line); {
		b := line[i]
		switch {
		case b == 0x1b:
			if end := sgrEnd(line, i); end > 0 {
				i = end
				continue
			}
			return fail(i, escapeReason(line, i))
		case b < 0x20:
			return fail(i, fmt.Sprintf("the C0 control byte 0x%02x (%q)", b, b))
		case b == 0x7f:
			return fail(i, "the DEL control byte 0x7f")
		case b < 0x80:
			i++
		default:
			r, size := utf8.DecodeRuneInString(line[i:])
			// A C1 control is either a two-byte rune (U+0080–U+009F) or a
			// stray byte of that range that is not valid UTF-8 at all.
			if (r >= 0x80 && r <= 0x9f) || (size == 1 && b >= 0x80 && b <= 0x9f) {
				if size == 1 {
					r = rune(b)
				}
				return fail(i, fmt.Sprintf("the C1 control U+%04X", r))
			}
			i += size // an ordinary multi-byte rune (CJK, emoji, …)
		}
	}
	return viewViolation{}, false
}

// firstViewViolation scans a whole frame and reports the first byte the
// terminal would act on.
func firstViewViolation(view string) (viewViolation, bool) {
	lineNo, lineStart := 1, 0
	for i := 0; i <= len(view); i++ {
		if i < len(view) && view[i] != '\n' {
			continue
		}
		if v, ok := scanFrameLine(view[lineStart:i], lineNo); ok {
			return v, true
		}
		lineNo, lineStart = lineNo+1, i+1
	}
	return viewViolation{}, false
}

// frameLine returns the 1-based line of a frame, for the failure message.
func frameLine(view string, lineNo int) string {
	lineStart, current := 0, 1
	for i := 0; i <= len(view); i++ {
		if i < len(view) && view[i] != '\n' {
			continue
		}
		if current == lineNo {
			return view[lineStart:i]
		}
		current, lineStart = current+1, i+1
	}
	return ""
}

// assertViewClean is the property: the frame of this state may hold frame
// newlines and style sequences, nothing the terminal would act on. A failure
// names the state, the line, the byte and the kind of sequence, so the leak is
// pointed at rather than described.
func assertViewClean(t *testing.T, m Model, state string) {
	t.Helper()
	view := m.View()
	v, ok := firstViewViolation(view)
	if !ok {
		return
	}
	t.Fatalf("%s: Model.View() carries a byte the terminal would act on:\n"+
		"line %d, byte %d: %s\naround: %q\nline: %q",
		state, v.line, v.off, v.why, v.near, frameLine(view, v.line))
}

// viewCase is one UI state rendered under the property.
//
// guard, when set, proves the state really holds the poisoned text (so the
// case cannot pass because the dialog never opened or the value never landed
// where the renderer reads it).
type viewCase struct {
	state string
	build func(t *testing.T) Model
	guard func(t *testing.T, m Model)
}

// viewCases render every case under the property, once per color profile.
//
// The test binary otherwise renders in the Ascii profile — lipgloss emits no
// escape at all — so a single pass would leave the whitelist's SGR branch (the
// sequences the frame is really built from) and the "the payload's own SGR is
// still a hostile byte" half of the payload unexercised. Rendering each state
// with TrueColor as well costs one extra frame and covers both.
func checkViewCases(t *testing.T, cases []viewCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.state, func(t *testing.T) {
			for _, profile := range []struct {
				name string
				p    termenv.Profile
			}{
				{"ascii", termenv.Ascii},
				{"truecolor", termenv.TrueColor},
			} {
				t.Run(profile.name, func(t *testing.T) {
					lipgloss.SetColorProfile(profile.p)
					defer lipgloss.SetColorProfile(termenv.Ascii)
					m := c.build(t)
					if c.guard != nil {
						c.guard(t, m)
					}
					assertViewClean(t, m, c.state)
				})
			}
		})
	}
}

// ---- Shared state builders ----

// editorModel is a sized screen on a normal editor tab (the fixture starts on
// the welcome page, which has a label and a body of its own).
func editorModel(text string) Model {
	m := wideModel(text)
	m.welcomeToEditor()
	return m
}

// hostileGrid is the result the grid cases render: header, type and every cell
// carry the payload, the pane is visible and focused.
func hostileGrid(t *testing.T) Model {
	t.Helper()
	cell := hostileViewText
	m := gridModel(t,
		[]driver.ResultColumn{{Name: "c" + hostileViewText, Type: "TEXT" + hostileViewText}},
		[][]*string{{&cell}, {&cell}})
	if m.session.result.res == nil || !m.layout.resultsVisible {
		t.Fatal("the grid fixture must hold a result with the pane visible")
	}
	return m
}

// hostileObjectTree puts a schema, a table and a column named with the payload
// into the catalog and opens every branch the tree can show, so names at every
// level are drawn.
func hostileObjectTree(t *testing.T) Model {
	t.Helper()
	schemaName := "s" + hostileViewText
	m := wideModel("SELECT 1")
	m.cat = &catalog.Catalog{
		Dialect: "demo",
		// Without capabilities the tree draws no relation group at all
		// (objectGroups consults m.cat.Supports), so the column rows below
		// the table would never be built.
		Capabilities: catalog.Capable(catalog.KindTable, catalog.KindView),
		Schemas: []*catalog.Schema{{
			Name: schemaName,
			Tables: []*catalog.Table{{
				Schema:  schemaName,
				Name:    "u" + hostileViewText,
				Type:    catalog.TypeTable,
				Comment: "rows" + hostileViewText,
				Columns: []*catalog.Column{
					{Name: "i" + hostileViewText, Type: "int" + hostileViewText, Comment: "pk" + hostileViewText},
				},
			}},
		}},
	}
	m.resetObjects()
	// Open every expandable branch, the way newTestModel opens the fixture.
	// Each round only sees the rows the previous round opened (the tree is
	// rebuilt on every read), so the loop runs until the column rows below
	// the table — the deepest names in the tree — come into view.
	for round := 0; round < 8 && !hasObjectRow(m, rowColumn); round++ {
		for _, row := range m.objectsRows() {
			switch row.kind {
			case rowConn, rowSchema, rowGroup, rowTable:
				m.objectsSetOpen(row, true)
			}
		}
	}
	if !hasObjectRow(m, rowColumn) {
		t.Fatal("the fixture must show a column row (the tree is the state under test)")
	}
	return m
}

// hasObjectRow reports whether the tree currently shows a row of that kind.
func hasObjectRow(m Model, kind rowKind) bool {
	for _, row := range m.objectsRows() {
		if row.kind == kind {
			return true
		}
	}
	return false
}

// ---- 1. The editor pane's names: header, tab bar, status ----

func TestViewStaysCleanWithHostileEditorNames(t *testing.T) {
	const state = "editor names (file name, tab names, connection, schema)"
	cases := []viewCase{
		{
			state: "header file name",
			build: func(t *testing.T) Model {
				m := editorModel("SELECT 1")
				m.fileName = "report" + hostileViewText + ".sql"
				return m
			},
			guard: func(t *testing.T, m Model) {
				if !strings.Contains(m.fileName, hostileViewText) {
					t.Fatalf("the file name must hold the payload, got %q", m.fileName)
				}
			},
		},
		{
			state: "tab bar user name",
			build: func(t *testing.T) Model {
				m := editorModel("SELECT 1")
				m.tabs[0].userName = "mine " + hostileViewText
				return m
			},
			guard: func(t *testing.T, m Model) {
				if got := stripANSI(m.View()); !strings.Contains(got, hostileViewTextPlain) {
					t.Fatalf("the tab bar must draw the (cleaned) name:\n%s", got)
				}
			},
		},
		{
			state: "tab bar derived name",
			build: func(t *testing.T) Model {
				m := editorModel("SELECT 1")
				m.tabs[0].userName = ""
				m.tabs[0].name = "public.users " + hostileViewText
				return m
			},
		},
		{
			state: "tab bar file name",
			build: func(t *testing.T) Model {
				m := editorModel("SELECT 1")
				m.tabs[0].userName = ""
				m.tabs[0].name = ""
				m.tabs[0].fileName = "q" + hostileViewText + ".sql"
				return m
			},
		},
		{
			state: "menu bar connection name",
			build: func(t *testing.T) Model {
				m := editorModel("SELECT 1")
				m.cfg.ConnName = "demo " + hostileViewText
				return m
			},
			guard: func(t *testing.T, m Model) {
				if got := stripANSI(m.View()); !strings.Contains(got, hostileViewTextPlain) {
					t.Fatalf("the menu bar must draw the (cleaned) connection name:\n%s", got)
				}
			},
		},
		{
			state: "menu bar default schema",
			build: func(t *testing.T) Model {
				m := editorModel("SELECT 1")
				m.conn.Schema = "pub " + hostileViewText
				return m
			},
		},
		{
			state: "status bar message",
			build: func(t *testing.T) Model {
				m := editorModel("SELECT 1")
				m.setMessage("boom " + hostileViewText)
				return m
			},
			guard: func(t *testing.T, m Model) {
				if m.status.message == "" {
					t.Fatal("the status message must be set (it is the text under test)")
				}
			},
		},
		{
			state: "narrow window (the bar falls back to the short summaries)",
			build: func(t *testing.T) Model {
				m := newTestModel("SELECT 1")
				m = send(m, tea.WindowSizeMsg{Width: 44, Height: 12})
				m.welcomeToEditor()
				m.cfg.ConnName = "demo " + hostileViewText
				m.conn.Schema = "pub " + hostileViewText
				m.fileName = "q" + hostileViewText + ".sql"
				return m
			},
		},
	}
	_ = state
	checkViewCases(t, cases)
}

// TestViewStaysCleanWithHostilePastedEditorText pins the paste path of the
// editor buffer. ui/sanitize.go calls the buffer a deliberate exception (the
// user's own keystrokes are drawn verbatim), but a paste is text from
// outside the program — every other one-line input of the program cleans it
// (cell editor, object filter, find prompt, export path, tab rename, vault
// fields). The editor's paste branch therefore cleans on the way in
// (sanitizePastedText), and since renderLine draws the buffer verbatim, that
// cleaning is what keeps the frame below free of the payload.
func TestViewStaysCleanWithHostilePastedEditorText(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	m := editorModel("")
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("SELECT '" + hostileViewText + "'"), Paste: true})
	if got := m.buf.Text(); !strings.Contains(got, hostileViewTextPlain) {
		t.Fatalf("the paste must reach the buffer, got %q", got)
	}
	// The printable tail of the payload is still there, so the frame below is
	// a frame that draws a buffer really holding the pasted text: what the
	// assertion proves is the cleaning of the paste (ui/editor_keys.go, the
	// InsertStringGrouped branch), not a renderer that draws nothing.
	assertViewClean(t, m, "editor buffer paste")
}

// ---- 2. The result grid ----

func TestViewStaysCleanWithHostileResultCells(t *testing.T) {
	cases := []viewCase{
		{
			state: "cells column name and column type",
			build: hostileGrid,
			guard: func(t *testing.T, m Model) {
				if got := stripANSI(m.View()); !strings.Contains(got, hostileViewTextPlain) {
					t.Fatalf("the grid must draw the (cleaned) cell text:\n%s", got)
				}
			},
		},
		{
			state: "row hover",
			build: func(t *testing.T) Model {
				m := hostileGrid(t)
				m.session.result.hover = 1
				return m
			},
		},
		{
			state: "row selection",
			build: func(t *testing.T) Model {
				m := hostileGrid(t)
				m.session.result.selAnchor = 0
				m.session.result.cursorRow = 1
				m.session.result.selKey = m.session.result.viewKey()
				return m
			},
			guard: func(t *testing.T, m Model) {
				if n := m.session.result.selectedRowCount(); n < 2 {
					t.Fatalf("selected rows = %d, the selection must be in effect", n)
				}
			},
		},
		{
			state: "statement error text",
			build: func(t *testing.T) Model {
				m := wideModel("SELECT 1")
				m.session.result.setResult(nil, errors.New(
					"pq: division by zero\nDETAIL: column \""+hostileViewText+"\"\nHINT: "+hostileViewText),
					"SELECT 1")
				m.layout.resultsVisible = true
				m.layout.focus = paneResults
				return m
			},
			guard: func(t *testing.T, m Model) {
				if m.session.result.err == nil {
					t.Fatal("the result must hold the error (it is the text under test)")
				}
			},
		},
		{
			state: "hidden pane (the expand strip still draws)",
			build: func(t *testing.T) Model {
				m := hostileGrid(t)
				m.layout.resultsVisible = false
				return m
			},
		},
	}
	checkViewCases(t, cases)
}

// ---- 3. The cell editor and the results filter ----

func TestViewStaysCleanWithHostileCellEditor(t *testing.T) {
	cases := []viewCase{
		{
			state: "cell editor holding the pasted value",
			build: func(t *testing.T) Model {
				m := hostileGrid(t)
				m.session.result.editing = &cellEdit{
					row: 0, col: 0,
					text: "new " + hostileViewText,
					at:   len([]rune("new " + hostileViewText)),
					orig: "old",
				}
				return m
			},
			guard: func(t *testing.T, m Model) {
				if m.session.result.editing == nil {
					t.Fatal("the cell editor must be open")
				}
			},
		},
		{
			state: "cell editor after a paste through the keys",
			build: func(t *testing.T) Model {
				m := hostileGrid(t)
				m.session.result.editing = &cellEdit{row: 0, col: 0, text: "old", at: 3, orig: "old"}
				m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(hostileViewText + "typed"), Paste: true})
				return m
			},
			guard: func(t *testing.T, m Model) {
				if m.session.result.editing == nil {
					t.Fatal("the paste must not close the cell editor")
				}
			},
		},
	}
	checkViewCases(t, cases)
}

func TestViewStaysCleanWithHostileResultsFilter(t *testing.T) {
	cases := []viewCase{
		{
			state: "filter input holding a pasted query",
			build: func(t *testing.T) Model {
				m := hostileGrid(t)
				m.session.result.filtering = true
				m.session.result.filter = hostileViewText
				m.session.result.filterAt = len([]rune(m.session.result.filter))
				return m
			},
			guard: func(t *testing.T, m Model) {
				if !m.session.result.filtering {
					t.Fatal("the filter input must be open")
				}
			},
		},
		{
			state: "filter seeded from a hostile cell (right-click, filter by cell)",
			build: func(t *testing.T) Model {
				m := hostileGrid(t)
				m.filterResultCell(0, 0)
				return m
			},
			guard: func(t *testing.T, m Model) {
				if got := m.session.result.filter; !strings.Contains(got, hostileViewText) {
					t.Fatalf("the filter must be seeded from the cell, got %q", got)
				}
			},
		},
		{
			state: "seeded filter input open (the text drawn while refining it)",
			build: func(t *testing.T) Model {
				m := hostileGrid(t)
				m.filterResultCell(0, 0)
				m.session.result.filtering = true
				return m
			},
		},
	}
	checkViewCases(t, cases)
}

// ---- 4. The export dialog ----

func TestViewStaysCleanWithHostileExportDialog(t *testing.T) {
	// The exporter needs a result to preview.
	exportModel := func(t *testing.T) Model {
		t.Helper()
		m := dialogModel(t, 120, 40)
		cell := hostileViewText
		m.session.result.setResult(&driver.Result{
			Columns: []driver.ResultColumn{{Name: "c" + hostileViewText, Type: "TEXT" + hostileViewText}},
			Rows:    [][]*string{{&cell}},
		}, nil, "SELECT 1")
		return m
	}
	openExport := func(t *testing.T, path string) Model {
		m := exportModel(t)
		m.exportDlg = exportDialog{open: true, path: path, format: exportCSV}
		m.exportDlg.caret = len([]rune(m.exportDlg.path))
		if !m.exportDlg.open {
			t.Fatal("the export dialog must be open")
		}
		return m
	}
	hostilePath := "/tmp/out" + hostileViewText + "/rows.csv"

	cases := []viewCase{
		{
			state: "path row focused",
			build: func(t *testing.T) Model {
				m := openExport(t, hostilePath)
				m.exportDlg.focus = exportFocusPath
				return m
			},
		},
		{
			state: "directory row focused",
			build: func(t *testing.T) Model {
				m := openExport(t, hostilePath)
				m.exportDlg.focus = exportFocusDirectory
				return m
			},
		},
		{
			state: "format dropdown open",
			build: func(t *testing.T) Model {
				m := openExport(t, hostilePath)
				m.exportDlg.focus = exportFocusFormat
				m.openExportChoice()
				if !m.exportDlg.choice.open {
					t.Fatal("the format dropdown must be open")
				}
				return m
			},
		},
		{
			state: "CSV preview",
			build: func(t *testing.T) Model {
				m := openExport(t, hostilePath)
				m.exportDlg.format = exportCSV
				return m
			},
		},
		{
			state: "TSV preview",
			build: func(t *testing.T) Model {
				m := openExport(t, hostilePath)
				m.exportDlg.format = exportTSV
				return m
			},
		},
		{
			state: "JSON preview",
			build: func(t *testing.T) Model {
				m := openExport(t, hostilePath)
				m.exportDlg.format = exportJSON
				return m
			},
		},
		{
			state: "Markdown preview",
			build: func(t *testing.T) Model {
				m := openExport(t, hostilePath)
				m.exportDlg.format = exportMarkdown
				return m
			},
		},
	}
	checkViewCases(t, cases)
}

// ---- 5. The file picker ----

func TestViewStaysCleanWithHostileFilePicker(t *testing.T) {
	poisonEntries := func(m Model) {
		for i := range m.picker.entries {
			m.picker.entries[i].name += hostileViewText
		}
	}
	cases := []viewCase{
		{
			state: "file mode entry names",
			build: func(t *testing.T) Model {
				m := openPicker(t, pickerFile, t.TempDir(), "")
				poisonEntries(m)
				return m
			},
			guard: func(t *testing.T, m Model) {
				if len(m.picker.entries) == 0 {
					t.Fatal("the picker must list entries (they are the text under test)")
				}
				if !m.picker.open {
					t.Fatal("the picker must be open")
				}
			},
		},
		{
			state: "directory mode entry names",
			build: func(t *testing.T) Model {
				m := openPicker(t, pickerDir, t.TempDir(), "")
				poisonEntries(m)
				return m
			},
		},
		{
			state: "path line",
			build: func(t *testing.T) Model {
				m := openPicker(t, pickerFile, t.TempDir(), "")
				m.picker.path = "/tmp" + hostileViewText + "/rows.sql"
				m.picker.caret = len([]rune(m.picker.path))
				return m
			},
		},
		{
			state: "typed filter",
			build: func(t *testing.T) Model {
				m := openPicker(t, pickerFile, t.TempDir(), hostileViewText)
				return m
			},
			guard: func(t *testing.T, m Model) {
				if m.picker.filter == "" {
					t.Fatal("the picker filter must hold the payload")
				}
			},
		},
		{
			state: "suffix hint",
			build: func(t *testing.T) Model {
				m := openPicker(t, pickerFile, t.TempDir(), "")
				m.picker.suffix = hostileViewText
				return m
			},
			guard: func(t *testing.T, m Model) {
				if m.picker.suffix == "" {
					t.Fatal("the picker suffix must hold the payload")
				}
			},
		},
		{
			state: "error line in file mode",
			build: func(t *testing.T) Model {
				m := openPicker(t, pickerFile, t.TempDir(), "")
				m.picker.err = errors.New("stat: " + hostileViewText)
				return m
			},
		},
		{
			state: "error line in directory mode",
			build: func(t *testing.T) Model {
				m := openPicker(t, pickerDir, t.TempDir(), "")
				m.picker.err = errors.New("readdir: " + hostileViewText)
				return m
			},
		},
	}
	checkViewCases(t, cases)
}

// ---- 6. The welcome page ----

func TestViewStaysCleanWithHostileWelcomePage(t *testing.T) {
	cases := []viewCase{
		{
			state: "saved connection name and driver",
			build: func(t *testing.T) Model {
				m := wideModel("")
				if !m.welcome {
					t.Fatal("the fixture must start on the welcome page")
				}
				m.settings.Connections = []settings.Connection{{
					Name: "shop " + hostileViewText, Driver: "postgres" + hostileViewText,
				}}
				return m
			},
			guard: func(t *testing.T, m Model) {
				if got := stripANSI(m.View()); !strings.Contains(got, hostileViewTextPlain) {
					t.Fatalf("the welcome page must draw the connection row:\n%s", got)
				}
			},
		},
		{
			state: "connection name of the header",
			build: func(t *testing.T) Model {
				m := wideModel("")
				m.cfg.ConnName = "demo " + hostileViewText
				return m
			},
		},
		{
			state: "locked vault (the notice replaces the connection list)",
			build: func(t *testing.T) Model {
				m := wideModel("")
				m.settings.Vault = &vault.Envelope{Hint: "hint " + hostileViewText}
				return m
			},
		},
	}
	checkViewCases(t, cases)
}

// ---- 7. The object tree ----

func TestViewStaysCleanWithHostileObjectTree(t *testing.T) {
	cases := []viewCase{
		{
			state: "schema table and column names",
			build: hostileObjectTree,
			guard: func(t *testing.T, m Model) {
				if got := stripANSI(m.View()); !strings.Contains(got, hostileViewTextPlain) {
					t.Fatalf("the tree must draw a (cleaned) hostile name:\n%s", got)
				}
			},
		},
		{
			state: "selected row in the status bar",
			build: func(t *testing.T) Model {
				m := hostileObjectTree(t)
				m.layout.focus = paneObjects
				for i, row := range m.objectsRows() {
					if row.kind == rowColumn {
						m.objects.sel = i
						break
					}
				}
				return m
			},
			guard: func(t *testing.T, m Model) {
				if m.objects.sel < 0 {
					t.Fatal("a row must be selected (it is the text under test)")
				}
			},
		},
		{
			state: "filter text in the pane title and status bar",
			build: func(t *testing.T) Model {
				m := hostileObjectTree(t)
				m.layout.focus = paneObjects
				m.objects.filtering = true
				m.objects.filter = hostileViewText
				return m
			},
		},
		{
			state: "saved connection row in the tree",
			build: func(t *testing.T) Model {
				m := hostileObjectTree(t)
				m.settings.Connections = append(m.settings.Connections,
					settings.Connection{Name: "shop " + hostileViewText, Driver: "demo"})
				return m
			},
		},
	}
	checkViewCases(t, cases)
}

// ---- 8. The completion popup ----

func TestViewStaysCleanWithHostileCompletionPopup(t *testing.T) {
	cases := []viewCase{
		{
			state: "candidate label and detail",
			build: func(t *testing.T) Model {
				m := editorModel("SELECT u")
				m.popup.set([]complete.Candidate{
					{Kind: complete.KindTable, Label: "u" + hostileViewText, Detail: "table " + hostileViewText},
					{Kind: complete.KindColumn, Label: "id", Detail: "int " + hostileViewText},
				}, sql.Pos{Row: 0, Col: 8})
				return m
			},
			guard: func(t *testing.T, m Model) {
				if !m.popup.open || len(m.popup.items) == 0 {
					t.Fatal("the completion popup must be open with candidates")
				}
			},
		},
		{
			state: "candidate inserted into the buffer",
			build: func(t *testing.T) Model {
				m := editorModel("SELECT u")
				m.popup.set([]complete.Candidate{
					{Kind: complete.KindTable, Label: "u" + hostileViewText},
				}, sql.Pos{Row: 0, Col: 7})
				next, _ := m.acceptCompletion()
				return next
			},
			guard: func(t *testing.T, m Model) {
				// The insert is cleaned on the way in (stripControlKeepLines):
				// the buffer may hold the printable text, never the sequence.
				if got := m.buf.Text(); strings.ContainsRune(got, '\x1b') {
					t.Fatalf("the buffer must not hold an escape character after accepting, got %q", got)
				}
			},
		},
	}
	checkViewCases(t, cases)
}

// ---- 9. The find prompt ----

func TestViewStaysCleanWithHostileFindQuery(t *testing.T) {
	cases := []viewCase{
		{
			state: "query text in the status bar",
			build: func(t *testing.T) Model {
				m := editorModel("SELECT users FROM users")
				m.find.open = true
				m.find.query = hostileViewText + "sel"
				m.find.caret = len([]rune(m.find.query))
				m.find.matches = 1
				return m
			},
			guard: func(t *testing.T, m Model) {
				if !m.find.open {
					t.Fatal("the find prompt must be open")
				}
			},
		},
		{
			state: "query seeded from a hostile selection",
			build: func(t *testing.T) Model {
				// The selection the prompt seeds from is file bytes; the
				// buffer itself stays out of the property (the deliberate
				// exception), so the seeded result is what is under test.
				m := editorModel("SELECT users FROM users")
				m.find.open = true
				m.find.query = hostileViewText
				m.find.caret = len([]rune(m.find.query))
				m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(hostileViewText + "sel"), Paste: true})
				return m
			},
		},
	}
	checkViewCases(t, cases)
}

// ---- 10. The connection form ----

func TestViewStaysCleanWithHostileConnForm(t *testing.T) {
	// poison fills every field the form draws as plain text: name, host, DSN
	// and default schema are all values a settings file or a CLI can carry.
	poison := func(m Model) int {
		focus := -1
		for i := range m.connDlg.fields {
			f := &m.connDlg.fields[i]
			switch f.key {
			case i18n.KeyConnName, i18n.KeyConnHost, i18n.KeyConnDSN, i18n.KeyConnSchema:
				f.value = "v " + hostileViewText
				f.caret = len([]rune(f.value))
				if focus < 0 {
					focus = i
				}
			}
		}
		return focus
	}
	form := func(t *testing.T) Model {
		t.Helper()
		m := dialogModel(t, 120, 40)
		m.openConnDialog()
		m.connDlg.fields = buildConnFields("postgres", nil)
		if !m.connDlg.open {
			t.Fatal("the connection form must be open")
		}
		return m
	}
	cases := []viewCase{
		{
			state: "name host DSN and schema fields",
			build: func(t *testing.T) Model {
				m := form(t)
				if focus := poison(m); focus >= 0 {
					m.connDlg.focus = focus
				}
				return m
			},
			guard: func(t *testing.T, m Model) {
				if got := stripANSI(m.View()); !strings.Contains(got, hostileViewTextPlain) {
					t.Fatalf("the form must draw a (cleaned) field value:\n%s", got)
				}
			},
		},
		{
			state: "open dropdown of a choice field",
			build: func(t *testing.T) Model {
				m := form(t)
				poison(m)
				for i := range m.connDlg.fields {
					if m.connDlg.fields[i].kind == fieldChoice && len(m.connDlg.fields[i].options) > 1 {
						m.connDlg.focus = i
						m.connDlg.dropdown = true
						m.connDlg.dropSel = 1
						m.connDlg.dropHover = 2
						break
					}
				}
				if !m.connDlg.dropdown {
					t.Fatal("a choice dropdown must be open (it is the state under test)")
				}
				return m
			},
		},
		{
			state: "secret field masked",
			build: func(t *testing.T) Model {
				m := form(t)
				for i := range m.connDlg.fields {
					f := &m.connDlg.fields[i]
					if f.kind == fieldSecret {
						f.value = "secret " + hostileViewText
						f.caret = len([]rune(f.value))
						m.connDlg.focus = i
						break
					}
				}
				return m
			},
		},
	}
	checkViewCases(t, cases)
}

// ---- 11. The context menus and the menu dropdown ----

func TestViewStaysCleanWithHostileContextMenus(t *testing.T) {
	cases := []viewCase{
		{
			state: "object browser context menu over a hostile row",
			build: func(t *testing.T) Model {
				m := hostileObjectTree(t)
				m.layout.focus = paneObjects
				for i, row := range m.objectsRows() {
					if row.kind == rowTable {
						m.objects.sel = i
						break
					}
				}
				m.openObjectsMenuForSelection()
				if !m.ctx.open {
					t.Fatal("the object context menu must be open")
				}
				return m
			},
		},
		{
			state: "result grid context menu over a hostile cell",
			build: func(t *testing.T) Model {
				m := hostileGrid(t)
				x, y := resultCellPoint(m, 0, 0)
				m = send(m, resultRightClick(x, y))
				if !m.resultMenu.open {
					t.Fatal("the result context menu must be open")
				}
				return m
			},
		},
		{
			state: "tab bar context menu",
			build: func(t *testing.T) Model {
				m := editorModel("SELECT 1")
				m.tabs[0].userName = "mine " + hostileViewText
				m = send(m, tea.KeyMsg{Type: tea.KeyCtrlT}) // a second tab
				m = rightClickTab(m, 0)
				if !m.tabMenu.open {
					t.Fatal("the tab context menu must be open")
				}
				return m
			},
		},
		{
			state: "menu bar dropdown",
			build: func(t *testing.T) Model {
				m := editorModel("SELECT 1")
				m.cfg.ConnName = "demo " + hostileViewText
				m.menu.open = 0
				return m
			},
			guard: func(t *testing.T, m Model) {
				if m.menu.open < 0 {
					t.Fatal("a menu must be open")
				}
			},
		},
	}
	checkViewCases(t, cases)
}

// ---- 12. The F1 key-bindings overlay ----

func TestViewStaysCleanWithHostileKeybindingsOverlay(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	m := hostileObjectTree(t)
	m.cfg.ConnName = "demo " + hostileViewText
	m = send(m, tea.KeyMsg{Type: tea.KeyF1})
	if !m.keysDlg.open {
		t.Fatal("F1 must open the key-bindings overlay")
	}
	assertViewClean(t, m, "F1 key-bindings overlay over a poisoned model")
}

// ---- 13. The query history ----

func TestViewStaysCleanWithHostileHistoryDialog(t *testing.T) {
	// Each field of an entry is drawn by a different line of the dialog, so
	// every case poisons exactly one of them: a failure then names the field
	// that leaks instead of only the first one the scanner walked over.
	entry := func(over func(*history.Entry)) history.Entry {
		e := history.Entry{
			At:         time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC),
			Connection: "conn",
			SQL:        "SELECT 1",
			RowSet:     true,
			Rows:       3,
			DurationMS: 12,
		}
		if over != nil {
			over(&e)
		}
		return e
	}
	open := func(t *testing.T, e history.Entry) Model {
		t.Helper()
		m := dialogModel(t, 120, 40)
		m.historyEntries = []history.Entry{e}
		m.openHistoryDialog()
		if !m.historyDlg.open {
			t.Fatal("the history dialog must be open")
		}
		return m
	}
	cases := []viewCase{
		{
			state: "entry connection name",
			build: func(t *testing.T) Model {
				return open(t, entry(func(e *history.Entry) { e.Connection = "conn " + hostileViewText }))
			},
			// Both draw points of the stored name clean it first:
			// renderHistoryRow's connection column and the preview's meta
			// line (historyMetaLine) go through stripControl, so the frame
			// gets the printable text while the log keeps the name.
		},
		{
			state: "entry SQL preview",
			build: func(t *testing.T) Model {
				return open(t, entry(func(e *history.Entry) { e.SQL = "SELECT '" + hostileViewText + "'" }))
			},
			// historyPreviewLines folds the statement's breaks and control
			// bytes away before either preview row measures it; the list row
			// already oneLine()s the statement, and opening the entry still
			// hands the stored bytes to the editor untouched.
		},
		{
			state: "entry error text",
			build: func(t *testing.T) Model {
				return open(t, entry(func(e *history.Entry) { e.Error = "pq: " + hostileViewText }))
			},
		},
		{
			state: "filter input fed a hostile paste",
			build: func(t *testing.T) Model {
				m := open(t, entry(nil))
				// Driven through the keys, not assigned: "/" opens the input
				// and the bracketed paste is what a hostile clipboard sends.
				m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
				m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(hostileViewText), Paste: true})
				return m
			},
			guard: func(t *testing.T, m Model) {
				// historyFilterKey only accepts runes whose key name IS the
				// text, so a bracketed paste (key name "[…]") is dropped
				// whole: the filter cannot receive the payload at all.
				if m.historyDlg.filter != "" {
					t.Fatalf("the history filter must not accept a paste, got %q", m.historyDlg.filter)
				}
			},
		},
		{
			// The field itself is still poisoned after the guard, and it
			// stays byte-for-byte (historyVisible matches against it raw):
			// only the drawing cleans, so historyHint strips the control
			// characters when it shows the filter. Latent input path noted:
			// historyFilterKey drops pastes (the case above), so today no
			// keystroke can put a control character here — the cleaning
			// belongs at the render, where the field would leak the moment
			// the input ever accepts bulk text.
			state: "filter field itself (drawn through the hint line)",
			build: func(t *testing.T) Model {
				m := open(t, entry(nil))
				m.historyDlg.filter = hostileViewText
				m.historyDlg.filtering = true
				return m
			},
			guard: func(t *testing.T, m Model) {
				if !strings.Contains(m.historyDlg.filter, hostileViewText) {
					t.Fatalf("the filter must hold the payload, got %q", m.historyDlg.filter)
				}
			},
		},
	}
	checkViewCases(t, cases)
}

// ---- 14. The vault dialog ----

func TestViewStaysCleanWithHostileVaultDialog(t *testing.T) {
	cases := []viewCase{
		{
			state: "unlock form with the stored hint",
			build: func(t *testing.T) Model {
				m := dialogModel(t, 120, 40)
				m.settings.Vault = &vault.Envelope{Hint: "hint " + hostileViewText}
				m.openVaultMode(vaultUnlock)
				if !m.vaultDlg.open {
					t.Fatal("the vault dialog must be open")
				}
				return m
			},
		},
		{
			state: "setup form after a pasted passphrase",
			build: func(t *testing.T) Model {
				m := dialogModel(t, 120, 40)
				m.openVaultMode(vaultSetup)
				if !m.vaultDlg.open {
					t.Fatal("the vault dialog must be open")
				}
				m = send(m, tea.KeyMsg{Type: tea.KeyRunes,
					Runes: []rune(hostileViewText + "passphrase"), Paste: true})
				return m
			},
			guard: func(t *testing.T, m Model) {
				for _, f := range m.vaultDlg.fields {
					if strings.ContainsRune(f.value, '\x1b') {
						t.Fatalf("the field %v must not hold an escape character, got %q", f.key, f.value)
					}
				}
			},
		},
		{
			state: "validation error line",
			build: func(t *testing.T) Model {
				m := dialogModel(t, 120, 40)
				m.openVaultMode(vaultSetup)
				// The dialog only ever builds this line with errText (see the
				// vaultDlg.err assignments in ui/vault_dialog.go), so the case
				// models that call: the raw assignment would be a state the
				// product cannot reach, and would test nothing about it.
				m.vaultDlg.err = errText(errors.New("kdf failed: " + hostileViewText))
				return m
			},
			guard: func(t *testing.T, m Model) {
				if m.vaultDlg.err == "" {
					t.Fatal("the error line must be set")
				}
			},
		},
	}
	checkViewCases(t, cases)
}

// ---- 15. Normal content must survive the whitelist ----

// TestViewKeepsNormalTextAndStyles is the regression guard for the assertion
// itself: Chinese, emoji, long text and the program's own styling all have to
// come through untouched, so a whitelist that "passes" by rejecting the frame
// wholesale would fail here instead of hiding every other case.
func TestViewKeepsNormalTextAndStyles(t *testing.T) {
	// The styles only exist with a color profile: the test binary otherwise
	// renders in Ascii (lipgloss emits nothing at all), and the assertion
	// below would then pass vacuously instead of proving the whitelist
	// accepts the sequences the frame really is built from.
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	const (
		cn    = "中文表名"
		emoji = "🙂🚀"
		conn  = "生产库"
	)
	// The editor draws the buffer line by line, the header draws the
	// connection name and the grid draws the cell: three different renderers,
	// all of them must survive the whitelist untouched.
	m := editorModel("SELECT '" + cn + emoji + "'")
	m.cfg.ConnName = conn
	cell := cn
	m.session.result.setResult(&driver.Result{
		Columns: []driver.ResultColumn{{Name: "列名", Type: "文本"}},
		Rows:    [][]*string{{&cell}},
	}, nil, "SELECT 1")
	m.layout.resultsVisible = true

	view := m.View()
	assertViewClean(t, m, "normal Chinese and emoji content")

	plain := stripANSI(view)
	for _, want := range []string{cn, emoji, "列名", "conn " + conn} {
		if !strings.Contains(plain, want) {
			t.Fatalf("the frame must still show %q (the whitelist must not eat normal text):\n%s", want, plain)
		}
	}
	// The styles the frame is built from must still be there: an assertion
	// that passes only because styling disappeared would be vacuous.
	if n := strings.Count(view, "\x1b["); n == 0 {
		t.Fatalf("the frame must still carry SGR styling:\n%q", view)
	}
	// A long value is truncated to the column, never dropped: the head of it
	// is still on screen, on one row.
	long := strings.Repeat("很长的值", 60)
	m.session.result.setResult(&driver.Result{
		Columns: []driver.ResultColumn{{Name: "v", Type: "TEXT"}},
		Rows:    [][]*string{{&long}},
	}, nil, "SELECT 1")
	m.layout.resultsVisible = true
	if got := stripANSI(m.View()); !strings.Contains(got, "很长的值") {
		t.Fatalf("a long value must still show its head:\n%s", got)
	}
	assertViewClean(t, m, "long truncated value")
}
