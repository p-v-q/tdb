package ui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/i18n"
)

func TestExportResultTextCSVAndTSV(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email, name FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}

	csvText, err := m.exportResultText(exportCSV)
	if err != nil {
		t.Fatalf("CSV export: %v", err)
	}
	if !strings.HasPrefix(csvText, "id,email,name\n") {
		t.Fatalf("CSV header = %q", csvText)
	}
	if !strings.Contains(csvText, "1,ada@example.com,Ada") {
		t.Fatalf("CSV rows = %q", csvText)
	}

	tsvText, err := m.exportResultText(exportTSV)
	if err != nil {
		t.Fatalf("TSV export: %v", err)
	}
	if !strings.HasPrefix(tsvText, "id\temail\tname\n") {
		t.Fatalf("TSV header = %q", tsvText)
	}
}

func TestExportQuotesCommasAndKeepsNullEmpty(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT NULL AS n, 'a,b' AS s")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	text, err := m.exportResultText(exportCSV)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	want := "n,s\n,\"a,b\"\n"
	if text != want {
		t.Fatalf("CSV = %q, want %q", text, want)
	}
}

func TestExportDialogWritesTheFile(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})

	if cmd := m.openExportDialog(); cmd != nil {
		t.Fatal("opening the exporter with a result should not show an error")
	}
	if !m.exportDlg.open {
		t.Fatal("the export dialog should be open")
	}
	if _, _, block, ok := m.renderExportDialog(); !ok || len(block) == 0 {
		t.Fatal("the export dialog should render")
	}

	path := filepath.Join(t.TempDir(), "users.csv")
	m.exportDlg.path = path
	next, cmd, handled := m.submitExportDialog()
	if !handled || cmd == nil {
		t.Fatal("submitting a valid export should return a write command")
	}
	if next.exportDlg.open {
		t.Fatal("the dialog should close after submission")
	}
	msg := cmd()
	status, ok := msg.(statusMessageMsg)
	if !ok {
		t.Fatalf("write command returned %T", msg)
	}
	if got := string(status); !strings.HasPrefix(got, "Exported 3 rows to ") {
		t.Fatalf("status = %q", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read exported file: %v", err)
	}
	if !strings.HasPrefix(string(data), "id,email\n") {
		t.Fatalf("exported file = %q", data)
	}
}

func TestExportDialogWithoutResultShowsAMessage(t *testing.T) {
	m := sized("")
	cmd := m.openExportDialog()
	if cmd == nil || m.exportDlg.open {
		t.Fatalf("empty grid: command=%v open=%v", cmd, m.exportDlg.open)
	}
	if !strings.Contains(m.status.message, "no rows") {
		t.Fatalf("status = %q", m.status.message)
	}
}

// TestExportDialogPreviewIsCleanButTheFileKeepsTheBytes: the preview draws
// what the exporter would write, so a value carrying an escape sequence must
// not reach the terminal from inside the dialog — while the export itself is
// data and keeps the bytes exactly as the database returned them, in the
// serializer and on disk.
func TestExportDialogPreviewIsCleanButTheFileKeepsTheBytes(t *testing.T) {
	plain, tainted := "abc", "abc"+escapeCellOSC+escapeCellClear
	m := gridModel(t,
		[]driver.ResultColumn{{Name: "v", Type: "TEXT"}},
		[][]*string{{&plain}, {&tainted}})

	// The export text is data: the escape bytes must survive the serializer.
	text, err := m.exportResultText(exportCSV)
	if err != nil {
		t.Fatalf("CSV export: %v", err)
	}
	if !strings.Contains(text, tainted) {
		t.Fatalf("the exported text must carry the original bytes: %q", text)
	}

	// Opening the dialog (the exporter's entry point in the grid) and
	// drawing the preview of every format stays clean.
	if cmd := m.openExportDialog(); cmd != nil {
		t.Fatal("opening the exporter with a result should not show an error")
	}
	for _, format := range []exportFormat{exportCSV, exportTSV, exportJSON, exportMarkdown} {
		m.exportDlg.format = format
		lines := m.exportPreviewLines(60, exportPreviewRows)
		if len(lines) == 0 {
			t.Fatalf("%v: the preview must show something", format)
		}
		for _, line := range lines {
			if strings.ContainsRune(line, '\x1b') {
				t.Fatalf("%v: the preview carries an escape character: %q", format, line)
			}
		}
		if format == exportCSV || format == exportTSV {
			// The cleaned value is still there: the preview must not pass
			// because the dialog showed nothing.
			if joined := strings.Join(lines, "\n"); !strings.Contains(joined, "abc]52;c;AAAA[2J") {
				t.Fatalf("%v: the cleaned value must be drawn: %q", format, joined)
			}
		}
		_, _, block, ok := m.renderExportDialog()
		if !ok || len(block) == 0 {
			t.Fatalf("%v: the export dialog did not render", format)
		}
		for _, line := range block {
			if strings.ContainsRune(line, '\x1b') {
				t.Fatalf("%v: the dialog carries an escape character: %q", format, line)
			}
		}
	}

	// Writing the file keeps the bytes as well — the regression guard for
	// the "clean the display, not the data" split.
	path := filepath.Join(t.TempDir(), "cells.csv")
	m.exportDlg.format = exportCSV
	m.exportDlg.path = path
	next, cmd, handled := m.submitExportDialog()
	if !handled || cmd == nil {
		t.Fatal("submitting a valid export should return a write command")
	}
	if next.exportDlg.open {
		t.Fatal("the dialog should close after submission")
	}
	if _, ok := cmd().(statusMessageMsg); !ok {
		t.Fatal("the write command must report the result to the status bar")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read exported file: %v", err)
	}
	if !strings.Contains(string(data), tainted) {
		t.Fatalf("the exported file must hold the original bytes: %q", data)
	}
}

func TestExportDialogLinesStaySquare(t *testing.T) {
	for _, width := range []int{100, 50, 24} {
		for _, focus := range []int{exportFocusPath, exportFocusFormat, exportFocusDirectory, exportFocusBrowse} {
			m := sized("")
			m.width = width
			m.exportDlg = exportDialog{
				open:  true,
				focus: focus,
				path:  "/tmp/a-very-long-export-file-name-that-does-not-fit.csv",
			}
			m.exportDlg.caret = len([]rune(m.exportDlg.path))
			x, y, block, ok := m.renderExportDialog()
			if !ok {
				t.Fatalf("width %d focus %d: dialog did not render", width, focus)
			}
			want := lipgloss.Width(block[0])
			for i, line := range block {
				if got := lipgloss.Width(line); got != want {
					t.Fatalf("width %d focus %d: line %d is %d cells, want %d: %q",
						width, focus, i, got, want, line)
				}
			}
			if x < 0 || y < 0 {
				t.Fatalf("width %d focus %d: origin = %d,%d", width, focus, x, y)
			}
		}
	}
}

func TestExportDialogLongPathKeepsCaretVisible(t *testing.T) {
	m := dialogModel(t, 40, 24)
	path := "/very/long/subdirectory/structure/export-results.csv"
	m.exportDlg = exportDialog{open: true, path: path, caret: len([]rune(path))}
	// The caret sits at the end of a path wider than the value column: the
	// window slides so the tail (and the caret) stays on screen.
	view := stripANSI(m.View())
	if !strings.Contains(view, "export-results.csv") {
		t.Fatalf("the caret window must show the end of a long path:\n%s", view)
	}
}

func TestExportDialogFormatToggleKeepsTheSuffixInSync(t *testing.T) {
	m := sized("")
	m.exportDlg.path = "rows.csv"
	m.setExportFormat(exportTSV)
	if m.exportDlg.path != "rows.tsv" {
		t.Fatalf("path = %q", m.exportDlg.path)
	}
	m.setExportFormat(exportCSV)
	if m.exportDlg.path != "rows.csv" {
		t.Fatalf("path = %q", m.exportDlg.path)
	}
}

func TestExportJSON(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, name, age FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}

	text, err := m.exportResultText(exportJSON)
	if err != nil {
		t.Fatalf("JSON export: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		t.Fatalf("export is not valid JSON (%v): %q", err, text)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3: %q", len(rows), text)
	}
	// NULL is a real JSON null, never the string "NULL" or an empty string.
	if rows[1]["name"] != nil || rows[1]["age"] != nil {
		t.Fatalf("NULL must map to null: %+v", rows[1])
	}
	if got := rows[0]["name"]; got != "Ada" {
		t.Fatalf("name = %v, want Ada", got)
	}
	if strings.Contains(text, `"NULL"`) || strings.Contains(text, `:""`) {
		t.Fatalf("no \"NULL\" or \"\" may stand in for null: %q", text)
	}
}

func TestExportJSONEscapesSpecialCharacters(t *testing.T) {
	conn := e2eConn(t)
	// Quotes, a backslash, an embedded newline and non-ASCII text in one row.
	m := e2eModel(t, conn, `SELECT 'he said "hi"' AS q, 'a\b' AS b, 'line1'||char(10)||'line2' AS s, '中文' AS c`)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}

	text, err := m.exportResultText(exportJSON)
	if err != nil {
		t.Fatalf("JSON export: %v", err)
	}
	for _, want := range []string{`\"hi\"`, `a\\b`, `line1\nline2`, "中文"} {
		if !strings.Contains(text, want) {
			t.Fatalf("escaped export is missing %q: %q", want, text)
		}
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		t.Fatalf("export is not valid JSON (%v): %q", err, text)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if got := rows[0]["q"]; got != `he said "hi"` {
		t.Fatalf("q = %v", got)
	}
	if got := rows[0]["b"]; got != `a\b` {
		t.Fatalf("b = %v", got)
	}
	if got := rows[0]["s"]; got != "line1\nline2" {
		t.Fatalf("s = %v", got)
	}
	if got := rows[0]["c"]; got != "中文" {
		t.Fatalf("c = %v", got)
	}
}

func TestExportJSONColumnsOnly(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email FROM users WHERE 0")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}

	text, err := m.exportResultText(exportJSON)
	if err != nil {
		t.Fatalf("JSON export: %v", err)
	}
	if strings.TrimSpace(text) != "[]" {
		t.Fatalf("columns without rows must export as [], got %q", text)
	}
}

func TestExportJSONWithoutResultFails(t *testing.T) {
	m := sized("")
	if _, err := m.exportResultText(exportJSON); !errors.Is(err, errNoResult) {
		t.Fatalf("err = %v, want errNoResult", err)
	}
}

func TestExportMarkdown(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT 1 AS id, 'a|b' AS pipe, 'x'||char(10)||'y' AS s, NULL AS n")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}

	text, err := m.exportResultText(exportMarkdown)
	if err != nil {
		t.Fatalf("Markdown export: %v", err)
	}
	want := "| id | pipe | s | n |\n" +
		"| --- | --- | --- | --- |\n" +
		"| 1 | a\\|b | x<br>y | NULL |\n"
	if text != want {
		t.Fatalf("Markdown = %q, want %q", text, want)
	}
}

func TestExportMarkdownColumnsOnly(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email FROM users WHERE 0")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}

	text, err := m.exportResultText(exportMarkdown)
	if err != nil {
		t.Fatalf("Markdown export: %v", err)
	}
	want := "| id | email |\n| --- | --- |\n"
	if text != want {
		t.Fatalf("Markdown = %q, want %q", text, want)
	}
}

func TestExportMarkdownEscapesBackslashBeforePipe(t *testing.T) {
	conn := e2eConn(t)
	// A literal \| must stay literal instead of closing the cell early.
	m := e2eModel(t, conn, `SELECT 'a\|b' AS s`)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	text, err := m.exportResultText(exportMarkdown)
	if err != nil {
		t.Fatalf("Markdown export: %v", err)
	}
	if !strings.Contains(text, `| a\\\|b |`) {
		t.Fatalf("backslash before pipe must be escaped twice: %q", text)
	}
}

func TestExportDialogCyclesThroughAllFormats(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id FROM users")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if cmd := m.openExportDialog(); cmd != nil {
		t.Fatal("opening the exporter with a result should not show an error")
	}
	if m.exportDlg.format != exportCSV || !strings.HasSuffix(m.exportDlg.path, ".csv") {
		t.Fatalf("start: format=%v path=%q", m.exportDlg.format, m.exportDlg.path)
	}

	// The format row keeps ←→ cycling as a fallback, keeping the suffix in
	// sync each step.
	m.exportDlg.focus = 1
	steps := []struct {
		format exportFormat
		suffix string
		label  string
	}{
		{exportTSV, ".tsv", "TSV"},
		{exportJSON, ".json", "JSON"},
		{exportMarkdown, ".md", "Markdown"},
		{exportCSV, ".csv", "CSV"},
	}
	for _, step := range steps {
		var handled bool
		m, _, handled = m.handleExportKey("right", nil)
		if !handled {
			t.Fatal("right on the format row must be handled")
		}
		if m.exportDlg.format != step.format {
			t.Fatalf("format = %v, want %v", m.exportDlg.format, step.format)
		}
		if !strings.HasSuffix(m.exportDlg.path, step.suffix) {
			t.Fatalf("path = %q, want suffix %q", m.exportDlg.path, step.suffix)
		}
		if got := m.t(m.exportDlg.format.labelKey()); got != step.label {
			t.Fatalf("label = %q, want %q", got, step.label)
		}
	}
}

func TestExportDialogFormatSelectPicksAndApplies(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id FROM users")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if cmd := m.openExportDialog(); cmd != nil {
		t.Fatal("opening the exporter with a result should not show an error")
	}
	m.exportDlg.path = "rows.csv"
	m.exportDlg.caret = len([]rune(m.exportDlg.path))
	m.exportDlg.focus = 1

	// Enter opens the list; Enter again picks the current format (CSV) and
	// closes the list without changing anything.
	var handled bool
	m, _, handled = m.handleExportKey("enter", nil)
	if !handled || !m.exportDlg.choice.open {
		t.Fatal("enter on the format row must open the dropdown")
	}
	m, _, handled = m.handleExportKey("enter", nil)
	if !handled || m.exportDlg.choice.open {
		t.Fatal("enter on the list must pick and close it")
	}
	if m.exportDlg.format != exportCSV {
		t.Fatalf("format = %v, want CSV", m.exportDlg.format)
	}

	// ↓ reaches TSV and Enter applies it on the spot: the suffix follows.
	m, _, handled = m.handleExportKey("enter", nil)
	m, _, handled = m.handleExportKey("down", nil)
	if m.exportDlg.choice.sel != 1 {
		t.Fatalf("choice.sel = %d, want the TSV entry", m.exportDlg.choice.sel)
	}
	m, _, handled = m.handleExportKey("enter", nil)
	if !handled {
		t.Fatal("enter must pick the highlighted format")
	}
	if m.exportDlg.format != exportTSV {
		t.Fatalf("format = %v, want TSV", m.exportDlg.format)
	}
	if !strings.HasSuffix(m.exportDlg.path, "rows.tsv") {
		t.Fatalf("path = %q, want the suffix in sync", m.exportDlg.path)
	}
	if text, err := m.exportResultText(m.exportDlg.format); err != nil ||
		!strings.HasPrefix(text, "id\n") {
		t.Fatalf("the picked format must write TSV: %q err=%v", text, err)
	}

	// While the list is open, no key may leak to the path behind it.
	m, _, handled = m.handleExportKey("enter", nil)
	if !m.exportDlg.choice.open {
		t.Fatal("the list must be open")
	}
	before := m.exportDlg.path
	m, _, _ = m.handleExportKey("a", []rune{'a'})
	if m.exportDlg.path != before {
		t.Fatalf("path = %q, typing must not leak to the path while the list is open", m.exportDlg.path)
	}
	// Esc closes the list only.
	m, _, _ = m.handleExportKey("esc", nil)
	if m.exportDlg.choice.open || !m.exportDlg.open {
		t.Fatal("esc must close the list but keep the dialog open")
	}
	if m.exportDlg.path != before {
		t.Fatalf("path = %q, closing the list must not change the path", m.exportDlg.path)
	}
}

func TestExportDialogWritesJSONAndMarkdown(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, name FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}

	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "rows.json")
	m.exportDlg = exportDialog{open: true, path: jsonPath, format: exportJSON}
	next, cmd, handled := m.submitExportDialog()
	if !handled || cmd == nil || next.exportDlg.open {
		t.Fatal("submitting a JSON export should write a file and close the dialog")
	}
	cmd() // the command performs the actual write
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read JSON file: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatalf("exported file is not valid JSON (%v): %q", err, data)
	}
	if len(rows) != 3 || rows[1]["name"] != nil {
		t.Fatalf("rows = %+v, want three rows with NULL as null", rows)
	}

	mdPath := filepath.Join(dir, "rows.md")
	m.exportDlg = exportDialog{open: true, path: mdPath, format: exportMarkdown}
	_, cmd, handled = m.submitExportDialog()
	if !handled || cmd == nil {
		t.Fatal("submitting a Markdown export should write a file")
	}
	cmd() // the command performs the actual write
	data, err = os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read Markdown file: %v", err)
	}
	if !strings.HasPrefix(string(data), "| id | name |\n| --- | --- |\n") {
		t.Fatalf("Markdown file = %q", data)
	}
	if !strings.Contains(string(data), "| 2 | NULL |") {
		t.Fatalf("NULL must be spelled out: %q", data)
	}
}

// TestExportDropdownHoverHighlightsWithoutPicking pins the pointer semantics
// of the export format dropdown (round 7 §1): motion highlights the option
// under the pointer without moving sel and without changing the format, a
// motion outside the dialog is swallowed, and only a press picks.
func TestExportDropdownHoverHighlightsWithoutPicking(t *testing.T) {
	// Lip Gloss emits no ANSI without a TTY: force colors so the highlight
	// style is really in the rendered row, then restore the plain profile.
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if cmd := m.openExportDialog(); cmd != nil {
		t.Fatal("opening the exporter with a result should not show an error")
	}
	m.exportDlg.focus = 1
	m.openExportChoice()
	if !m.exportDlg.choice.open {
		t.Fatal("the format dropdown must open")
	}
	cl, ok := m.exportChoiceLayout()
	if !ok {
		t.Fatal("the format dropdown must have a layout")
	}
	options := m.exportDlg.choice.options
	if cl.top != 0 || cl.visible < 3 || len(options) < 3 {
		t.Fatalf("precondition: top = %d, visible = %d, options = %d", cl.top, cl.visible, len(options))
	}
	format := m.exportDlg.format

	// Motion onto the third option highlights it and changes nothing.
	m = send(m, mouseMotion(cl.rect.X+2, cl.rect.Y+1+2))
	if m.exportDlg.choice.hover != 2 {
		t.Fatalf("choice.hover = %d, want 2 (the third option)", m.exportDlg.choice.hover)
	}
	if m.exportDlg.choice.sel != 0 {
		t.Fatalf("choice.sel = %d, hovering must not move the keyboard highlight", m.exportDlg.choice.sel)
	}
	if m.exportDlg.format != format {
		t.Fatalf("format = %v, hovering must not change the value", m.exportDlg.format)
	}
	if !m.exportDlg.choice.open || !m.exportDlg.open {
		t.Fatal("hovering must leave the dropdown and the dialog open")
	}

	// The hovered row is drawn exactly like a selected one, and differently
	// from a row that is neither selected nor hovered.
	lines := m.renderSelectList(cl, &m.exportDlg.choice)
	rowIdx := 1 + (m.exportDlg.choice.hover - cl.top) // out[0] is the top border
	savedSel, savedHover := m.exportDlg.choice.sel, m.exportDlg.choice.hover
	m.exportDlg.choice.sel, m.exportDlg.choice.hover = savedHover, -1
	selected := m.renderSelectList(cl, &m.exportDlg.choice)
	m.exportDlg.choice.sel, m.exportDlg.choice.hover = savedSel, -1
	plain := m.renderSelectList(cl, &m.exportDlg.choice)
	m.exportDlg.choice.sel, m.exportDlg.choice.hover = savedSel, savedHover
	if lines[rowIdx] != selected[rowIdx] {
		t.Fatalf("the hovered row must be drawn like the selected one:\nhover:   %q\nselect: %q",
			lines[rowIdx], selected[rowIdx])
	}
	if lines[rowIdx] == plain[rowIdx] {
		t.Fatalf("the hovered row must be drawn differently from a plain one: %q", lines[rowIdx])
	}

	// A motion outside the dialog clears the highlight and is swallowed.
	l := m.exportLayout()
	m = send(m, mouseMotion(l.x-3, l.y))
	if m.exportDlg.choice.hover != -1 {
		t.Fatalf("choice.hover = %d, want -1 outside the dialog", m.exportDlg.choice.hover)
	}
	if !m.exportDlg.open || !m.exportDlg.choice.open {
		t.Fatal("a motion outside the dialog must leave it open")
	}
	if m.exportDlg.format != format {
		t.Fatalf("format = %v, want it unchanged (%v)", m.exportDlg.format, format)
	}

	// The press contract is unchanged: picking the third option applies it.
	m = send(m, mousePress(cl.rect.X+2, cl.rect.Y+1+2))
	if m.exportDlg.choice.open {
		t.Fatal("pressing an option must close the dropdown")
	}
	if got, want := m.exportDlg.format, options[2].Value; got.id() != want {
		t.Fatalf("format = %q, want %q (the pressed option)", got.id(), want)
	}
}

// ---- The directory picker behind Browse… (round 7 §4/§5) ----

// exportModelAt builds a sized model whose exporter is open on path.
func exportModelAt(t *testing.T, width, height int, path string) Model {
	t.Helper()
	m := dialogModel(t, width, height)
	m.exportDlg = exportDialog{open: true, path: path}
	m.exportDlg.caret = len([]rune(path))
	return m
}

// TestExportFocusWalksDirectoryAndBrowseAndEnterTriggers pins the exporter's
// focus order — path → format → directory → browse, with ↑/↓ and Tab wrapping
// over the same stops — and that Enter on either of the two new stops opens
// the directory picker while the exporter stays open and untouched behind it.
func TestExportFocusWalksDirectoryAndBrowseAndEnterTriggers(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "rows.csv")
	m := exportModelAt(t, 100, 30, path)

	if m.exportDlg.focus != exportFocusPath {
		t.Fatalf("focus = %d, the path field is the first stop", m.exportDlg.focus)
	}
	// ↑ wraps from the path field to the Browse row; ↓ wraps back.
	m = keyByName(m, "up")
	if m.exportDlg.focus != exportFocusBrowse {
		t.Fatalf("focus = %d, want the browse stop after ↑ from the path", m.exportDlg.focus)
	}
	m = keyByName(m, "down")
	if m.exportDlg.focus != exportFocusPath {
		t.Fatalf("focus = %d, want to wrap back to the path field", m.exportDlg.focus)
	}
	m = keyByName(m, "tab")
	if m.exportDlg.focus != exportFocusFormat {
		t.Fatalf("focus = %d, want the format stop", m.exportDlg.focus)
	}
	m = keyByName(m, "tab")
	if m.exportDlg.focus != exportFocusDirectory {
		t.Fatalf("focus = %d, want the directory stop", m.exportDlg.focus)
	}

	// Enter on the directory row opens the picker on the path's folder.
	m = keyByName(m, "enter")
	if !m.picker.open || m.picker.mode != pickerDir {
		t.Fatalf("open=%v mode=%v, enter on the directory row must open the directory picker",
			m.picker.open, m.picker.mode)
	}
	if m.picker.dir != root {
		t.Fatalf("picker dir = %q, want the folder of %q", m.picker.dir, path)
	}
	if !m.exportDlg.open {
		t.Fatal("the exporter must stay open beneath the picker")
	}
	if m.pickerPurpose != pickerForExportDir {
		t.Fatalf("purpose = %v, want pickerForExportDir", m.pickerPurpose)
	}

	// The picker is modal: keys typed while it is open must not edit the
	// export path behind it.
	before := m.exportDlg.path
	m = typeText(m, "q")
	if m.exportDlg.path != before {
		t.Fatalf("path = %q, typing must not reach the exporter", m.exportDlg.path)
	}

	// Esc cancels the pick only: nothing changes and the exporter stays.
	m = keyByName(m, "esc")
	if m.picker.open {
		t.Fatal("esc must close the picker")
	}
	if !m.exportDlg.open {
		t.Fatal("the exporter must survive the cancelled pick")
	}
	if m.exportDlg.path != before {
		t.Fatalf("path = %q, a cancelled pick must change nothing", m.exportDlg.path)
	}
	if m.pickerPurpose != pickerForNone {
		t.Fatalf("purpose = %v, cancelling must clear it", m.pickerPurpose)
	}

	// Tab from the directory row reaches the Browse row, Enter triggers the
	// same picker, and the walk wraps back to the path field.
	m = keyByName(m, "tab")
	if m.exportDlg.focus != exportFocusBrowse {
		t.Fatalf("focus = %d, want the browse stop", m.exportDlg.focus)
	}
	m = keyByName(m, "enter")
	if !m.picker.open || m.picker.mode != pickerDir {
		t.Fatal("enter on the browse row must open the directory picker")
	}
	m = keyByName(m, "esc")
	m = keyByName(m, "tab")
	if m.exportDlg.focus != exportFocusPath {
		t.Fatalf("focus = %d, want the walk to wrap back to the path field", m.exportDlg.focus)
	}
}

// TestExportPickerChoiceKeepsTheFileName pins the hand-back: the chosen
// folder replaces only the directory part of the path, the file name stays,
// the caret moves to the end, and only the picker closes.
func TestExportPickerChoiceKeepsTheFileName(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(root, "rows.csv")
	m := exportModelAt(t, 100, 30, path)
	m.exportDlg.focus = exportFocusDirectory

	m = keyByName(m, "enter")
	if !m.picker.open || m.picker.dir != root {
		t.Fatalf("open=%v dir=%q, the picker must start in %q", m.picker.open, m.picker.dir, root)
	}

	// Enter descends into "out" (a directory never picks itself), and the
	// picker's own button then takes the folder — the pointer's way to the
	// same choice.
	m.picker.sel = pickerIndexOf(t, m, "out")
	m = keyByName(m, "enter")
	if !m.picker.open || m.picker.dir != out {
		t.Fatalf("open=%v dir=%q, enter must descend into %q", m.picker.open, m.picker.dir, out)
	}
	l := m.pickerLayout()
	m = send(m, mousePress(l.choose.X, l.choose.Y))

	if m.picker.open {
		t.Fatal("taking the folder must close the picker")
	}
	if !m.exportDlg.open {
		t.Fatal("the exporter must stay open")
	}
	want := filepath.Join(out, "rows.csv")
	if m.exportDlg.path != want {
		t.Fatalf("path = %q, want %q (folder replaced, file name kept)", m.exportDlg.path, want)
	}
	if m.exportDlg.caret != len([]rune(want)) {
		t.Fatalf("caret = %d, want at the end of the new path (%d)", m.exportDlg.caret, len([]rune(want)))
	}
	if m.pickerPurpose != pickerForNone || m.picker.result != "" {
		t.Fatalf("purpose=%v result=%q, the dispatch must clear both",
			m.pickerPurpose, m.picker.result)
	}
}

// TestExportBrowseButtonOpensThePickerByPointer pins the pointer side: the
// drawn Browse button sits exactly where its hit rectangle says, clicking it
// opens the directory picker, and while the picker is up no event reaches
// the chrome (and hence the exporter) behind it.
func TestExportBrowseButtonOpensThePickerByPointer(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "rows.csv")
	m := exportModelAt(t, 100, 30, path)

	// Rendering and hit testing share one rectangle: the button is drawn at
	// the column its rect names.
	l := m.exportLayout()
	_, _, block, ok := m.renderExportDialog()
	if !ok {
		t.Fatal("the exporter must render")
	}
	line := stripANSI(block[l.browse.Y-l.y])
	idx := strings.Index(line, m.t(i18n.KeyExportBrowse))
	if idx < 0 {
		t.Fatalf("the browse row must draw its button:\n%s", line)
	}
	// Display columns, not bytes: the border is a multi-byte rune.
	if col := lipgloss.Width(line[:idx]); col != l.browse.X-l.x+1 {
		t.Fatalf("the drawn Browse button starts at column %d, its rectangle says %d:\n%s",
			col, l.browse.X-l.x+1, line)
	}

	m = send(m, mousePress(l.browse.X, l.browse.Y))
	if !m.picker.open || m.picker.mode != pickerDir {
		t.Fatalf("open=%v mode=%v, the button must open the directory picker",
			m.picker.open, m.picker.mode)
	}
	if m.picker.dir != root {
		t.Fatalf("picker dir = %q, want %q", m.picker.dir, root)
	}
	if !m.exportDlg.open {
		t.Fatal("the exporter must stay open beneath the picker")
	}

	// A press on the menu bar behind the picker is swallowed: no menu opens
	// while the picker owns the pointer.
	r := m.paneRects()
	m = send(m, mousePress(r.menubar.X+1, r.menubar.Y))
	if m.menu.open != -1 {
		t.Fatalf("menu.open = %d, the press belongs to the picker", m.menu.open)
	}
	// Motion is routed into the picker as well, so no hover lights up on
	// the toolbar behind it.
	m = send(m, mouseMotion(r.toolbar.X+1, r.toolbar.Y))
	if m.toolHover != -1 {
		t.Fatalf("toolHover = %d, hover must not reach the chrome", m.toolHover)
	}

	// The picker's own Cancel (a click, this time) closes only the picker.
	pl := m.pickerLayout()
	m = send(m, mousePress(pl.cancel.X, pl.cancel.Y))
	if m.picker.open {
		t.Fatal("cancel must close the picker")
	}
	if !m.exportDlg.open || m.exportDlg.path != path {
		t.Fatalf("export open=%v path=%q, cancelling must leave the exporter alone",
			m.exportDlg.open, m.exportDlg.path)
	}
	if m.pickerPurpose != pickerForNone {
		t.Fatalf("purpose = %v, cancelling must clear it", m.pickerPurpose)
	}
}

// ---- Escape sequences in the path ----

// TestExportDialogRowsDropTheEscapeSequencesOfThePath pins that the exporter
// cannot put an escape sequence into the frame: the path can be seeded from a
// directory listing, and the path row is drawn in two ways (focused and not),
// so both as well as the directory row must draw the cleaned text.
func TestExportDialogRowsDropTheEscapeSequencesOfThePath(t *testing.T) {
	// filepath.FromSlash keeps the test honest on every platform: the dialog
	// draws the path it is given, so the expected text has to use the same
	// separators.
	path := filepath.FromSlash("/tmp/" + seqOSC52 + "/rows.csv")
	cleanPath := filepath.FromSlash("/tmp/]52;c;aaaa/rows.csv")
	cleanDir := filepath.FromSlash("/tmp/]52;c;aaaa")

	for _, focus := range []int{exportFocusPath, exportFocusFormat, exportFocusDirectory, exportFocusBrowse} {
		m := sized("")
		m.exportDlg = exportDialog{open: true, focus: focus, path: path}
		// The caret sits where typing the whole path would leave it.
		m.exportDlg.caret = len([]rune(m.exportDlg.path))

		x, y, block, ok := m.renderExportDialog()
		if !ok {
			t.Fatalf("focus %d: the dialog did not render", focus)
		}
		if x < 0 || y < 0 {
			t.Fatalf("focus %d: origin = %d,%d", focus, x, y)
		}
		want := lipgloss.Width(block[0])
		for i, line := range block {
			if strings.Contains(line, "\x1b]52") {
				t.Fatalf("focus %d: line %d carries the sequence of the path:\n%q", focus, i, line)
			}
			if got := lipgloss.Width(line); got != want {
				t.Fatalf("focus %d: line %d is %d cells, want %d: %q", focus, i, got, want, line)
			}
		}
		// Both rows draw the cleaned text — the printable rest of the
		// sequence, not the sequence itself.
		if got := stripANSI(block[2]); !strings.Contains(got, cleanPath) {
			t.Fatalf("focus %d: the path row = %q, want the cleaned path %q", focus, got, cleanPath)
		}
		if got := stripANSI(block[4]); !strings.Contains(got, cleanDir) {
			t.Fatalf("focus %d: the directory row = %q, want the cleaned folder %q", focus, got, cleanDir)
		}
		assertNoPayload(t, m.View(), "the export path")
	}
}

// TestExportDialogCaretMapsOntoTheCleanedPath pins that the caret keeps
// pointing at the character it was moved to once the drawing cleaned the
// path: the window is rebuilt from the cleaned text, and the caret becomes the
// number of printable runes in front of it — a position inside the cleaned
// text, never one past it.
func TestExportDialogCaretMapsOntoTheCleanedPath(t *testing.T) {
	raw := "rows" + seqOSC52 + ".csv" // the caret counts runes of this text

	// Every caret position must render a square dialog without the sequence:
	// the mapping may never send the caret outside the cleaned text.
	for caret := 0; caret <= len([]rune(raw)); caret++ {
		m := sized("")
		m.exportDlg = exportDialog{open: true, focus: exportFocusPath, path: raw, caret: caret}
		_, _, block, ok := m.renderExportDialog()
		if !ok {
			t.Fatalf("caret %d: the dialog did not render", caret)
		}
		want := lipgloss.Width(block[0])
		for i, line := range block {
			if strings.Contains(line, "\x1b]52") {
				t.Fatalf("caret %d: line %d carries the sequence:\n%q", caret, i, line)
			}
			if got := lipgloss.Width(line); got != want {
				t.Fatalf("caret %d: line %d is %d cells, want %d: %q", caret, i, got, want, line)
			}
		}
	}

	// The caret right after the escape character still sits behind "rows":
	// the cleaned prefix of the path names its position.
	m := sized("")
	m.exportDlg = exportDialog{open: true, focus: exportFocusPath, path: raw, caret: 5}
	_, _, block, ok := m.renderExportDialog()
	if !ok {
		t.Fatal("the dialog did not render")
	}
	if got := stripANSI(block[2]); !strings.Contains(got, "rows ]52;c;aaaa.csv") {
		t.Fatalf("the path row = %q, want the cursor right behind \"rows\"", got)
	}

	// At the end of the raw path the whole cleaned path fits the window.
	m.exportDlg.caret = len([]rune(raw))
	_, _, block, ok = m.renderExportDialog()
	if !ok {
		t.Fatal("the dialog did not render")
	}
	if got := stripANSI(block[2]); !strings.Contains(got, "rows]52;c;aaaa.csv") {
		t.Fatalf("the path row = %q, want the whole cleaned path", got)
	}
}

// TestExportDialogPasteDropsTheEscapeSequences pins the way the sequences
// reach the path in the first place: a paste into the path field is cleaned
// before it is inserted, like every other one-line input of the program, so
// the field never holds what it would then draw.
func TestExportDialogPasteDropsTheEscapeSequences(t *testing.T) {
	m := sized("")
	m.exportDlg = exportDialog{open: true, focus: exportFocusPath, path: "rows.csv"}
	m.exportDlg.caret = len([]rune(m.exportDlg.path))

	paste := seqOSC52 + seqClearScreen + "tail.csv"
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(paste), Paste: true})

	if got := m.exportDlg.path; strings.ContainsRune(got, '\x1b') {
		t.Fatalf("the path = %q, want no escape character", got)
	}
	if want := "rows.csv]52;c;aaaa[2Jtail.csv"; m.exportDlg.path != want {
		t.Fatalf("the path = %q, want %q", m.exportDlg.path, want)
	}
	if want := len([]rune(m.exportDlg.path)); m.exportDlg.caret != want {
		t.Fatalf("caret = %d, want %d (behind the inserted text)", m.exportDlg.caret, want)
	}
	assertNoPayload(t, m.View(), "the pasted path")
}
