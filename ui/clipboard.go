package ui

// clipboard.go is the single clipboard boundary. It writes OSC 52 escape
// sequences and exposes copyText for both editor and result commands.

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/i18n"
)

const maxClipboard = 64 * 1024

var clipboardWriter io.Writer = os.Stdout

// copyText issues one OSC 52 copy and a status message.
func (m Model) copyText(text string) tea.Cmd {
	tr := m.translator
	count := len([]rune(text))
	return func() tea.Msg {
		if err := copyToClipboard(text); err != nil {
			return statusMessageMsg(tr.T(i18n.KeyMsgCopyFailed, err))
		}
		return statusMessageMsg(tr.T(i18n.KeyMsgCopied, count))
	}
}

// copyToClipboard writes text to the system clipboard over OSC 52, which
// virtually every terminal supports. The writer is swappable for tests.

func copyToClipboard(text string) error {
	if text == "" {
		return nil
	}
	if len(text) > maxClipboard {
		text = text[:maxClipboard]
	}
	_, err := fmt.Fprintf(clipboardWriter, "\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(text)))
	return err
}
