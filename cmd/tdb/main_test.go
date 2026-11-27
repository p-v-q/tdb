package main

// main_test.go covers the startup decisions that live outside any package:
// today, when the UI is offered the first-run vault setup.

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsFirstRun pins when the startup counts as a first run: only when a
// settings path exists but the file at it does not. An empty path — a shell
// with neither HOME nor a -settings flag — must never be a first run,
// because persistSettings skips the write there: the setup dialog would let
// the user set a passphrase that never reaches a file, silently.
func TestIsFirstRun(t *testing.T) {
	dir := t.TempDir()

	if isFirstRun("") {
		t.Fatal("an empty settings path must not be a first run")
	}

	missing := filepath.Join(dir, "settings.json")
	if !isFirstRun(missing) {
		t.Fatalf("a missing file at %q must be a first run", missing)
	}

	if err := os.WriteFile(missing, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if isFirstRun(missing) {
		t.Fatal("an existing settings file must not be a first run")
	}

	// A stat error that is not ErrNotExist (here: the parent is a regular
	// file, so the stat fails with ENOTDIR) is not a first run either — the
	// offered setup would be about a file that can never appear.
	if isFirstRun(filepath.Join(missing, "settings.json")) {
		t.Fatal("a stat error other than ErrNotExist must not be a first run")
	}
}

// TestIsFirstRunWithFreshConfigDirectory covers a genuinely fresh install: the
// whole configuration directory is missing, and it can be created when the
// first preference is saved. A path that runs through a regular file can never
// be created, so it is not offered as a first run either.
func TestIsFirstRunWithFreshConfigDirectory(t *testing.T) {
	dir := t.TempDir()
	fresh := filepath.Join(dir, "config", "tdb", "settings.json")
	if !isFirstRun(fresh) {
		t.Fatalf("a missing file under missing directories (%q) must be a first run", fresh)
	}

	regular := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(regular, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if isFirstRun(filepath.Join(regular, "tdb", "settings.json")) {
		t.Fatal("a path that runs through a regular file must not be a first run")
	}
}
