package ui

// persistence_order_test.go pins call-order persistence: command goroutines may
// finish out of order, but an older settings snapshot must never replace a
// newer one on disk.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSettingsWriteFailureIsReported(t *testing.T) {
	oldWrite := writeSettingsSnapshot
	t.Cleanup(func() { writeSettingsSnapshot = oldWrite })
	writeSettingsSnapshot = func(string, []byte) (string, error) {
		return "", errors.New("disk full")
	}

	m := sized("")
	m.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	msg := m.persistSettings()()
	status, ok := msg.(statusMessageMsg)
	if !ok || !strings.Contains(string(status), "disk full") {
		t.Fatalf("write failure = %#v, want the writer's error in the status", msg)
	}
}

func TestSettingsWritesAreSerializedInCallOrder(t *testing.T) {
	oldWrite := writeSettingsSnapshot
	t.Cleanup(func() { writeSettingsSnapshot = oldWrite })

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondEntered := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	t.Cleanup(release)

	var mu sync.Mutex
	var order []string
	var last []byte
	writeSettingsSnapshot = func(path string, data []byte) (string, error) {
		kind := "new"
		if bytes.Contains(data, []byte(`"readOnly": false`)) {
			kind = "old"
			close(firstStarted)
			<-releaseFirst
		} else {
			close(secondEntered)
		}
		mu.Lock()
		order = append(order, kind)
		last = append([]byte(nil), data...)
		mu.Unlock()
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return "", err
		}
		return "", nil
	}

	path := filepath.Join(t.TempDir(), "settings.json")
	m := sized("")
	m.settingsPath = path
	m.settings.ReadOnly = false
	first := m.persistSettings()
	m.settings.ReadOnly = true
	second := m.persistSettings()
	if first == nil || second == nil {
		t.Fatal("both saves must produce commands")
	}

	firstDone := make(chan tea.Msg, 1)
	go func() { firstDone <- first() }()
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("the first write did not start")
	}

	// Start the newer command first. It must wait for the older snapshot rather
	// than overtaking it and being overwritten when the older write finishes.
	secondDone := make(chan tea.Msg, 1)
	go func() { secondDone <- second() }()
	select {
	case <-secondEntered:
		t.Fatal("the newer write overtook the blocked older write")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the first write did not finish")
	}
	select {
	case <-secondEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("the queued second write did not start after the first")
	}
	select {
	case <-secondDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the second write did not finish")
	}

	mu.Lock()
	gotOrder := append([]string(nil), order...)
	gotLast := append([]byte(nil), last...)
	mu.Unlock()
	if !reflect.DeepEqual(gotOrder, []string{"old", "new"}) {
		t.Fatalf("write order = %v, want [old new]", gotOrder)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(onDisk, gotLast) || !bytes.Contains(onDisk, []byte(`"readOnly": true`)) {
		t.Fatalf("the newest snapshot did not remain on disk:\n%s", onDisk)
	}
}
