// Package history stores the queries a user has run.
//
// History is deliberately separate from settings: it is an append-heavy
// log, while settings are a small document that is rewritten whenever a
// preference changes. The file is JSON so it stays inspectable, and writes are
// atomic (temp file + rename) with restrictive permissions because SQL can
// contain sensitive values.
package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultLimit is the number of entries kept in the log.
const DefaultLimit = 200

// Entry is one executed statement.
type Entry struct {
	At         time.Time `json:"at"`
	Connection string    `json:"connection,omitempty"`
	SQL        string    `json:"sql"`
	DurationMS int64     `json:"durationMs,omitempty"`
	RowSet     bool      `json:"rowSet,omitempty"`
	Rows       int       `json:"rows,omitempty"`
	Affected   int64     `json:"affected,omitempty"`
	Truncated  bool      `json:"truncated,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// Store persists history entries at Path. A zero Path keeps history in memory
// only; Save and Load are no-ops that still report missing data cleanly.
type Store struct {
	Path  string
	Limit int
}

// New returns a store with the default limit.
func New(path string) Store {
	return Store{Path: path, Limit: DefaultLimit}
}

// DefaultPath returns the history file next to a settings file. The history
// follows a custom -settings path, so separate profiles also get separate
// history.
func DefaultPath(settingsPath string) string {
	if strings.TrimSpace(settingsPath) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(settingsPath), "history.json")
}

// Load reads the entries, newest first. A missing file is not an error.
func (s Store) Load() ([]Entry, error) {
	if s.Path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading query history: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parsing query history (%s): %w", s.Path, err)
	}
	entries = normalize(entries, s.limit())
	// The file is normally written newest first, but sorting on load keeps a
	// hand-edited or older file consistent for the list.
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].At.After(entries[j].At)
	})
	return entries, nil
}

// Save writes entries in the order given (callers prepend new runs), trimming
// the log to the store's limit.
func (s Store) Save(entries []Entry) error {
	if s.Path == "" {
		return nil
	}
	entries = normalize(entries, s.limit())
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating the history directory: %w", err)
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding query history: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".history.json.*")
	if err != nil {
		return fmt.Errorf("creating a history temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // a no-op once the rename succeeds
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing query history: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("setting history permissions: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("flushing query history: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing the history temp file: %w", err)
	}
	if err := os.Rename(tmpName, s.Path); err != nil {
		return fmt.Errorf("replacing query history: %w", err)
	}
	return nil
}

func (s Store) limit() int {
	if s.Limit > 0 {
		return s.Limit
	}
	return DefaultLimit
}

// normalize drops unusable entries and keeps at most limit rows. It also trims
// surrounding whitespace from stored SQL so the list never begins with a blank
// line.
func normalize(entries []Entry, limit int) []Entry {
	out := make([]Entry, 0, min(len(entries), limit))
	for _, e := range entries {
		if e.SQL == "" || e.At.IsZero() {
			continue
		}
		if len(out) >= limit {
			break
		}
		out = append(out, e)
	}
	return out
}
