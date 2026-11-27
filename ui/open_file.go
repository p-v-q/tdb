package ui

// open_file.go implements "open SQL file" (docs/ux-round7.md, §5): the
// entry point that opens the file picker, the reading of the chosen file
// into a new tab, and the routing of a finished pick back to whoever opened
// the picker.
//
// The picker itself only ever reports a path (file_picker.go); this file
// owns what happens to that path — a new tab, except on the welcome page,
// which takes the file itself.

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/textbuf"
)

// maxSQLFileSize is the cap on what "open SQL file" reads: 8 MiB of SQL is
// already far beyond a file anyone edits by hand, and the cap is what keeps
// a mistaken pick (a device, a huge dump) from freezing the interface. The
// stat size is checked first, so an oversized regular file is rejected
// without reading a byte of it.
const maxSQLFileSize = 8 << 20

// The two failures the descriptor re-check reports; openSQLFile maps them back
// onto the same status-bar messages as the checks before the open.
var (
	errSQLFileTooLarge   = errors.New("file exceeds the size cap")
	errSQLFileNotRegular = errors.New("file is not a regular file")
)

// verifyOpenSQLFile re-checks the rules of openSQLFile on the descriptor that
// was actually opened. The path was stat'ed before the open, but the path is
// not what is read: whoever holds the directory may replace it between the two
// calls, and the file behind the descriptor is the one whose bytes end up in
// the buffer. A directory, a device or a FIFO must be refused here as well
// (reading one can block forever), and so must be a file that outgrew the cap
// in the meantime.
func verifyOpenSQLFile(f *os.File) error {
	fi, err := f.Stat()
	if err != nil {
		// A descriptor that cannot even be described is not a file whose
		// contents could be trusted to be readable.
		return errSQLFileNotRegular
	}
	if fi.Size() > maxSQLFileSize {
		return errSQLFileTooLarge
	}
	if !fi.Mode().IsRegular() {
		return errSQLFileNotRegular
	}
	return nil
}

// openSQLFilePicker opens the picker in file mode for actOpenFile (the File
// menu entry and Ctrl+O): the folder of the active file, so a sibling query
// is one Enter away, or the working directory when the tab has no file yet.
// The list starts on the SQL files of that folder (the ".sql" suffix
// restriction, shown on the hint line); the text filter starts empty and
// Backspace lifts the suffix, so the whole folder stays one key away.
func (m *Model) openSQLFilePicker() {
	startDir := ""
	if m.fileName != "" {
		startDir = filepath.Dir(m.fileName)
	} else if wd, err := os.Getwd(); err == nil {
		startDir = wd
	}
	m.openFilePickerWithSuffix(pickerFile, startDir, "", ".sql")
	m.pickerPurpose = pickerForOpenSQL
}

// openSQLFile reads path and opens it in a new tab (or in the welcome tab,
// which is converted instead — see openSQLFileIntoWelcomeTab): the tab is
// named after the file and records it as fileName, so the existing Save
// (Ctrl+S) writes back to it. Every failure reports in the status bar and
// leaves no tab behind — a half-open file would look like an editor that
// silently lost the content it was asked to show.
//
// The read is synchronous on purpose: it is bounded by maxSQLFileSize, the
// same class of blocking work saveFile already does with os.WriteFile, and
// a tea.Cmd would only add a round trip in which the user can switch tabs
// and then see the file open somewhere they are no longer looking.
func (m *Model) openSQLFile(path string) tea.Cmd {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return m.setMessage(m.t(i18n.KeyMsgFileOpenFailed, path))
	}
	if fi.Size() > maxSQLFileSize {
		return m.setMessage(m.t(i18n.KeyMsgFileTooLarge, path, maxSQLFileSize>>20))
	}
	if !fi.Mode().IsRegular() {
		// A device or a FIFO reports no useful size and reading it could
		// block forever — the fate the size cap exists to prevent.
		return m.setMessage(m.t(i18n.KeyMsgFileOpenFailed, path))
	}
	f, err := os.Open(path)
	if err != nil {
		return m.setMessage(m.t(i18n.KeyMsgFileOpenFailed, path))
	}
	defer f.Close()
	// The rules above were checked on the path; this one is checked on the
	// descriptor, because between the stat and the open the path may have
	// been replaced by another file — a rename, or a hostile process that
	// watched the picker. The file that is read is the one behind f.
	if err := verifyOpenSQLFile(f); err != nil {
		if errors.Is(err, errSQLFileTooLarge) {
			return m.setMessage(m.t(i18n.KeyMsgFileTooLarge, path, maxSQLFileSize>>20))
		}
		return m.setMessage(m.t(i18n.KeyMsgFileOpenFailed, path))
	}
	// One byte past the cap: a file whose stat size lied (procfs and friends
	// report 0) is caught by the read instead of by the stat.
	data, err := io.ReadAll(io.LimitReader(f, maxSQLFileSize+1))
	if err != nil {
		return m.setMessage(m.t(i18n.KeyMsgFileOpenFailed, path))
	}
	if len(data) > maxSQLFileSize {
		return m.setMessage(m.t(i18n.KeyMsgFileTooLarge, path, maxSQLFileSize>>20))
	}
	if !utf8.Valid(data) {
		// Refusing beats decoding lossily: replacing the bytes of SQL with
		// U+FFFD would silently change what the file says.
		return m.setMessage(m.t(i18n.KeyMsgFileNotUTF8, path))
	}
	// A welcome page that asks for a file gets it in its own tab: opening
	// beside it would leave the page sitting next to the file for the user
	// to close by hand — the same reason "New query" converts the page
	// instead of opening another tab (docs/ux-round7.md, §2).
	if m.session != nil && m.session.welcome {
		m.openSQLFileIntoWelcomeTab(path, string(data))
		return nil
	}
	m.newTabNamed(filepath.Base(path), string(data))
	m.fileName = path // newTabNamed switched to the new tab, so this names it
	return nil
}

// openSQLFileIntoWelcomeTab converts the welcome tab into the editor tab that
// shows the file. The fields are the ones a tab built by newTabNamed starts
// with (it is new, so they are all zero), and the tail is its own: no
// selection, the editor focused and the completion context rebuilt from the
// file rather than from the text the page stood in front of. welcomeToEditor
// drops the page marker, the page-only hover/selection and the popup.
func (m *Model) openSQLFileIntoWelcomeTab(path, text string) {
	m.welcomeToEditor()
	m.buf = textbuf.New(text)
	m.fileName = path
	m.name = filepath.Base(path)
	m.firstRow, m.scrollX = 0, 0
	m.clearSelection()
	m.closeFind() // the prompt was started on the text behind the page
	m.layout.focus = paneEditor
	m.updateContext()
}

// dispatchPickerResult hands a finished pick to the purpose the picker was
// opened for and clears the picker's result, then reports the command of the
// dispatch (a failure message, or nil). It is safe to call at any time: a
// picker that is still open, a cancellation (result "") and an unknown
// purpose all leave the state cleared without acting.
//
// The input handlers call it right after their picker branch, so a pick is
// consumed by the same key press or click that produced it. They also call
// it up front (guarded by "the picker is closed") for a pick left behind by
// a caller that drove the picker handler directly: a stale result must not
// outlive the dialog that produced it.
func (m *Model) dispatchPickerResult() tea.Cmd {
	if m.picker.open {
		return nil // still choosing: the purpose stays with the dialog
	}
	purpose, result := m.pickerPurpose, m.picker.result
	m.pickerPurpose = pickerForNone
	m.picker.result = ""
	if result == "" {
		return nil // cancelled: the purpose was the only thing to clear
	}
	switch purpose {
	case pickerForOpenSQL:
		return m.openSQLFile(result)
	case pickerForExportDir:
		m.applyExportDirectory(result)
	}
	return nil
}
