package history

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSaveAndLoadNewestFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	s := New(path)
	now := time.Now().UTC().Truncate(time.Second)
	entries := []Entry{
		{At: now, Connection: "shop", SQL: "SELECT 2", Rows: 1, DurationMS: 4},
		{At: now.Add(-time.Minute), Connection: "shop", SQL: "SELECT 1", Affected: 3},
	}
	if err := s.Save(entries); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("entries = %d, want 2", len(got))
	}
	if got[0].SQL != "SELECT 2" || got[1].SQL != "SELECT 1" {
		t.Fatalf("order = %+v", got)
	}
	if got[0].Rows != 1 || got[1].Affected != 3 {
		t.Fatalf("metadata lost: %+v", got)
	}
}

func TestLoadSortsNewestFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	now := time.Now().UTC().Truncate(time.Second)
	raw := `[
  {"at":"` + now.Add(-time.Hour).Format(time.RFC3339) + `","sql":"old"},
  {"at":"` + now.Format(time.RFC3339) + `","sql":"new"}
]`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := New(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SQL != "new" || got[1].SQL != "old" {
		t.Fatalf("order = %+v", got)
	}
}

func TestLoadMissingAndCorrupt(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "missing.json"))
	if got, err := s.Load(); err != nil || got != nil {
		t.Fatalf("missing history = %+v, %v", got, err)
	}
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(path).Load(); err == nil {
		t.Fatal("corrupt history must report an error")
	}
}

func TestLimitAndInvalidEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	s := New(path)
	s.Limit = 2
	now := time.Now()
	err := s.Save([]Entry{
		{At: now, SQL: "one"},
		{At: now, SQL: ""},
		{At: now, SQL: "two"},
		{At: now, SQL: "three"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SQL != "one" || got[1].SQL != "two" {
		t.Fatalf("limited history = %+v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no POSIX permission bits: os.Stat reports 0666 for a file
	// that Save wrote with mode 0600, so the private-file check only means
	// something on the platforms that carry the bits.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}
