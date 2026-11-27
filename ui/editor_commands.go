package ui

// editor_commands.go implements editor commands that act on the active tab:
// saving, formatting, comment toggling and copying the selection.

import (
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/sql"
	"github.com/p-v-q/tdb/textbuf"
)

// saveFile writes the active tab's buffer to the file it was opened with. A
// pointer receiver matters here: the "no file" branch reports through the
// status bar, and a value receiver would report into a copy the model never
// keeps — the message would be silently lost.
func (m *Model) saveFile() tea.Cmd {
	name := m.fileName
	if name == "" {
		return m.setMessage(m.t(i18n.KeyMsgNoFileToSave))
	}
	content := m.buf.Text()
	tr := m.translator
	return func() tea.Msg {
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			return statusMessageMsg(tr.T(i18n.KeyMsgSaveFailed, err))
		}
		return statusMessageMsg(tr.T(i18n.KeyMsgSaved, name, strings.Count(content, "\n")+1))
	}
}

// formatSQL pretty-prints the selection, or the whole buffer when nothing is
// selected. Strings, comments and quoted identifiers are preserved verbatim by
// the formatter.
// toggleComment comments or uncomments the selected lines (or the line with
// the caret). The whole block is replaced in one edit, so it undoes as a single
// action just like formatting.
func (m *Model) toggleComment() tea.Cmd {
	start, end := m.buf.Cursor(), m.buf.Cursor()
	if a, b, ok := m.selectionRange(); ok {
		start, end = a, b
		// A selection ending at column zero of the next line does not include
		// that line, matching the way editors treat line selections.
		if end.Col == 0 && end.Row > start.Row {
			end.Row--
			end.Col = len(m.buf.Line(end.Row))
		}
	}
	if start.Row > end.Row {
		start, end = end, start
	}

	lines := make([]string, 0, end.Row-start.Row+1)
	allCommented := true
	hasContent := false
	for row := start.Row; row <= end.Row; row++ {
		line := m.buf.LineString(row)
		lines = append(lines, line)
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" {
			continue
		}
		hasContent = true
		if !strings.HasPrefix(trimmed, "--") {
			allCommented = false
		}
	}
	if !hasContent {
		allCommented = false
	}

	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		indent := len(line) - len(trimmed)
		if allCommented {
			rest := strings.TrimPrefix(trimmed, "--")
			rest = strings.TrimPrefix(rest, " ")
			lines[i] = line[:indent] + rest
		} else {
			lines[i] = line[:indent] + "-- " + trimmed
		}
	}

	m.buf.ReplaceRange(
		textbuf.Pos{Row: start.Row, Col: 0},
		textbuf.Pos{Row: end.Row, Col: len(m.buf.Line(end.Row))},
		strings.Join(lines, "\n"),
	)
	m.clearSelection()
	m.buf.SetCursor(textbuf.Pos{Row: start.Row, Col: 0})
	m.layout.focus = paneEditor
	m.afterEdit(false)
	m.updateContext()
	return nil
}

func (m *Model) formatSQL() tea.Cmd {
	start, end, selected := m.selectionRange()
	text := m.buf.Text()
	if selected {
		text = m.buf.Slice(start, end)
	}
	if strings.TrimSpace(text) == "" {
		return m.setMessage(m.t(i18n.KeyMsgNothingToFormat))
	}
	formatted := sql.Format(text, m.cfg.Dialect)
	if formatted == text {
		return m.setMessage(m.t(i18n.KeyMsgFormatted))
	}

	before := m.buf.Cursor()
	if selected {
		m.buf.ReplaceRange(start, end, formatted)
	} else {
		last := m.buf.Lines() - 1
		m.buf.ReplaceRange(textbuf.Pos{}, textbuf.Pos{Row: last, Col: len(m.buf.Line(last))}, formatted)
	}
	m.clearSelection()
	m.buf.SetCursor(before)
	m.layout.focus = paneEditor
	m.afterEdit(false)
	m.updateContext()
	return m.setMessage(m.t(i18n.KeyMsgFormatted))
}

// copySelectionCmd copies the selection to the system clipboard.
func (m Model) copySelectionCmd() tea.Cmd {
	if text := m.selectedText(); text != "" {
		return m.copyText(text)
	}
	return nil
}
