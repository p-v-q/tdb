package ui

// persistence.go writes settings and layout changes atomically through the
// settings package. With no configured path it is a no-op, which keeps tests
// and in-memory sessions side-effect free.

import (
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
)

// writeSettingsSnapshot is the file-writing half of a save. It is a variable so
// the ordering guarantee can be tested with a controllably blocked first write.
var writeSettingsSnapshot = settings.WriteWithBackup

// settingsSaveQueue is a single background writer. persistSettings appends a
// sealed snapshot on the UI goroutine, in call order; the actor performs every
// file write before taking the next snapshot. Returned commands only wait for
// their own result, so command scheduling cannot reorder writes.
type settingsSaveQueue struct {
	mu      sync.Mutex
	pending []settingsSaveRequest
	running bool
	write   func(string, []byte) (string, error)
}

type settingsSaveRequest struct {
	path   string
	data   []byte
	result chan settingsSaveResult
}

type settingsSaveResult struct {
	backup string
	err    error
}

func newSettingsSaveQueue() *settingsSaveQueue {
	return &settingsSaveQueue{write: writeSettingsSnapshot}
}

func (q *settingsSaveQueue) enqueue(path string, data []byte) <-chan settingsSaveResult {
	req := settingsSaveRequest{
		path: path, data: data, result: make(chan settingsSaveResult, 1),
	}
	q.mu.Lock()
	q.pending = append(q.pending, req)
	q.mu.Unlock()
	return req.result
}

// start is called by the returned command, not by enqueue: merely requesting a
// save still has no file side effect. If an earlier command was discarded, a
// later executed command drains that older snapshot first, so ordering cannot
// deadlock and the newest save still waits for every predecessor.
func (q *settingsSaveQueue) start() {
	q.mu.Lock()
	if !q.running {
		q.running = true
		go q.run()
	}
	q.mu.Unlock()
}

func (q *settingsSaveQueue) run() {
	for {
		q.mu.Lock()
		if len(q.pending) == 0 {
			q.running = false
			q.mu.Unlock()
			return
		}
		req := q.pending[0]
		q.pending[0] = settingsSaveRequest{}
		q.pending = q.pending[1:]
		q.mu.Unlock()

		backup, err := q.write(req.path, req.data)
		req.result <- settingsSaveResult{backup: backup, err: err}
	}
}

// persistSettings writes the preferences to disk. It is a no-op (returning a
// command that does nothing) when no path is configured.
//
// Marshalling happens here, on the UI goroutine that owns m.settings: with an
// unlocked vault it re-seals the envelope (reading the session key that Lock
// zeroes in place) and reads the Connections slice that connection edits
// mutate in place. The returned background command receives the sealed bytes
// and only performs file IO, so it can never race the live settings.
func (m *Model) persistSettings() tea.Cmd {
	if m.settingsPath == "" {
		return nil
	}
	tr := m.translator
	data, err := m.settings.Seal()
	if err != nil {
		return func() tea.Msg {
			return statusMessageMsg(tr.T(i18n.KeyMsgSettingsFailed, err))
		}
	}
	path := m.settingsPath
	q := m.settingsWrites
	if q == nil {
		q = newSettingsSaveQueue()
		m.settingsWrites = q
	}
	result := q.enqueue(path, data)
	return func() tea.Msg {
		q.start()
		saved := <-result
		if saved.err != nil {
			return statusMessageMsg(tr.T(i18n.KeyMsgSettingsFailed, saved.err))
		}
		// A corrupt settings file was kept aside rather than overwritten: say
		// where it went instead of leaving the user to wonder what happened.
		if saved.backup != "" {
			return statusMessageMsg(tr.T(i18n.KeySettingsBackupKept, saved.backup))
		}
		return noopMsg{}
	}
}

// persistLayout copies the current pane layout into the settings and saves it,
// so resizing or closing a pane survives a restart.
func (m *Model) persistLayout() tea.Cmd {
	m.settings.Layout = settings.Layout{
		ObjectsVisible: m.layout.objectsVisible,
		ObjectsWidth:   m.layout.objectsWidth,
		ResultsVisible: m.layout.resultsVisible,
		ResultsHeight:  m.layout.resultsHeight,
	}
	return m.persistSettings()
}
