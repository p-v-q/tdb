package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/p-v-q/tdb/vault"
)

// same compares two settings values; Settings holds a slice, so == cannot be
// used on it.
func same(a, b Settings) bool { return reflect.DeepEqual(a, b) }

func TestDefaultIsValid(t *testing.T) {
	s := Default()
	if changed := s.Normalize(); changed {
		t.Fatalf("Normalize must not touch the defaults: %+v", s)
	}
	if s.Theme != ThemeDark || !s.Mouse || !s.Gutter {
		t.Fatalf("unexpected defaults: %+v", s)
	}
}

func TestNormalizeClampsAndFixes(t *testing.T) {
	s := Settings{
		Theme:  "neon", // invalid
		Locale: "  en  ",
		Layout: Layout{ObjectsWidth: 1, ResultsHeight: 9999},
	}
	if !s.Normalize() {
		t.Fatal("invalid fields must be reported as changed")
	}
	if s.Theme != ThemeDark {
		t.Fatalf("Theme = %q, want %q", s.Theme, ThemeDark)
	}
	if s.Locale != "en" {
		t.Fatalf("Locale = %q, want the whitespace trimmed", s.Locale)
	}
	if s.Layout.ObjectsWidth != MinObjectsWidth {
		t.Fatalf("ObjectsWidth = %d, want %d", s.Layout.ObjectsWidth, MinObjectsWidth)
	}
	if s.Layout.ResultsHeight != MaxResultsHeight {
		t.Fatalf("ResultsHeight = %d, want %d", s.Layout.ResultsHeight, MaxResultsHeight)
	}
	if s.Version != Version {
		t.Fatalf("Version = %d", s.Version)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")
	want := Default()
	want.Locale = "en"
	want.Theme = ThemeLight
	want.Mouse = false
	want.Gutter = false
	want.ReadOnly = true
	want.Layout = Layout{ObjectsVisible: false, ObjectsWidth: 40, ResultsVisible: true, ResultsHeight: 12}

	if err := want.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !same(got, want) {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestSaveIsAtomicAndPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := Default().Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// Windows has no POSIX permission bits: a file written with mode 0600
	// reports 0666 there, so the private-file assertion only applies where
	// the bits exist.
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("permissions = %o, want 600", perm)
		}
	}
	// Overwriting must not leave temp files behind in the directory.
	if err := Default().Save(path); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "settings.json" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory must contain only settings.json, found %v", names)
	}
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("a missing file must not be an error: %v", err)
	}
	if !same(got, Default()) {
		t.Fatalf("got %+v, want the defaults", got)
	}
}

func TestLoadPartialFileKeepsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"theme":"light","layout":{"objectsWidth":33}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Theme != ThemeLight {
		t.Fatalf("Theme = %q", got.Theme)
	}
	if got.Layout.ObjectsWidth != 33 {
		t.Fatalf("ObjectsWidth = %d", got.Layout.ObjectsWidth)
	}
	// Fields absent from the file keep their default value.
	if !got.Mouse || !got.Gutter || !got.Layout.ResultsVisible || got.Layout.ResultsHeight != Default().Layout.ResultsHeight {
		t.Fatalf("missing fields must keep their defaults: %+v", got)
	}
}

func TestLoadCorruptFileFallsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err == nil {
		t.Fatal("a corrupt file must return an error")
	}
	if !same(got, Default()) {
		t.Fatalf("a corrupt file must fall back to the defaults, got %+v", got)
	}
}

func TestLoadEmptyFileIsDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || !same(got, Default()) {
		t.Fatalf("an empty file means \"use the defaults\": got %+v err %v", got, err)
	}
}

func TestUnknownFieldsAreIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	body := `{"theme":"light","futureOption":true}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("unknown fields must not be fatal: %v", err)
	}
	if got.Theme != ThemeLight {
		t.Fatalf("Theme = %q", got.Theme)
	}
}

func TestPathRespectsEnvOverride(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "custom.json")
	t.Setenv(EnvConfigPath, custom)
	got, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if got != custom {
		t.Fatalf("Path = %q, want %q", got, custom)
	}
}

func TestPathDefaultLocation(t *testing.T) {
	t.Setenv(EnvConfigPath, "")
	got, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if !strings.HasSuffix(got, filepath.Join("tdb", "settings.json")) {
		t.Fatalf("Path = %q, want it to end with tdb/settings.json", got)
	}
}

func TestJSONRoundTripsThroughStdlib(t *testing.T) {
	s := Default()
	s.Locale = "en"
	text, err := s.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var back Settings
	if err := json.Unmarshal([]byte(text), &back); err != nil {
		t.Fatalf("the output must be valid JSON: %v", err)
	}
	if !same(back, s) {
		t.Fatalf("JSON round trip mismatch: %+v vs %+v", back, s)
	}
}

// TestConnectionSchemaRoundTrips pins that the default schema of a connection
// survives the settings file: it is the schema the connection works in, so
// losing it would silently move the user's unqualified names somewhere else.
func TestConnectionSchemaRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	want := Default()
	want.Connections = []Connection{
		{Name: "shop", Driver: "postgres", Database: "shop", Schema: "sales"},
		{Name: "plain", Driver: "postgres", Database: "shop"},
	}
	if err := want.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !same(got, want) {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
	if got.Connections[0].Schema != "sales" {
		t.Fatalf("schema = %q, want sales", got.Connections[0].Schema)
	}
	// A connection without a schema keeps the field out of the file, so the
	// document stays as small as it was before the field existed.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Count(string(data), `"schema"`) != 1 {
		t.Fatalf("only the connection that has a schema may write the field:\n%s", data)
	}
}

// TestLoadConnectionWithoutSchema pins that a settings file written before
// the schema field existed still loads: the connection then works in the
// server's default schema, exactly as it did before.
func TestLoadConnectionWithoutSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	body := `{"version":1,"connections":[{"name":"old","driver":"postgres","database":"shop"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Connections) != 1 {
		t.Fatalf("connections = %+v, want one", got.Connections)
	}
	if got.Connections[0].Schema != "" {
		t.Fatalf("schema = %q, want an empty schema", got.Connections[0].Schema)
	}
}

// ---- The backup of an unreadable settings file ----

// TestBackupKeepsAFileThatCannotBeLoaded pins that a file whose JSON is
// valid but whose structure does not fit the Settings cannot be loaded — and
// is therefore kept aside before it is overwritten. Judging it by its syntax
// alone would throw the user's configuration away without a trace.
func TestBackupKeepsAFileThatCannotBeLoaded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	body := `{"readOnly":"yes"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// The file really is unreadable.
	if _, err := Load(path); err == nil {
		t.Fatal("the file must not load: its structure does not fit the settings")
	}

	backup, err := Default().SaveWithBackup(path)
	if err != nil {
		t.Fatalf("SaveWithBackup: %v", err)
	}
	if backup != path+".bak" {
		t.Fatalf("backup = %q, want %q", backup, path+".bak")
	}
	got, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != body {
		t.Fatalf("the backup holds %q, want %q", got, body)
	}
	// The settings themselves were written and load again.
	if _, err := Load(path); err != nil {
		t.Fatalf("the saved settings must load: %v", err)
	}
}

// TestBackupKeepsAFileThatIsNotJSON pins the original case: a broken file is
// kept aside too.
func TestBackupKeepsAFileThatIsNotJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	body := "{not json"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	backup, err := Default().SaveWithBackup(path)
	if err != nil {
		t.Fatalf("SaveWithBackup: %v", err)
	}
	if backup != path+".bak" {
		t.Fatalf("backup = %q, want %q", backup, path+".bak")
	}
	got, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != body {
		t.Fatalf("the backup holds %q, want %q", got, body)
	}
}

// TestBackupIsPrivateEvenWhenItAlreadyExists pins the permission of the
// backup: the mode of a write is only applied when the file is created, so an
// existing backup keeps its own mode — and it holds the very same secrets as
// the settings file.
func TestBackupIsPrivateEvenWhenItAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	backup := path + ".bak"
	if err := os.WriteFile(backup, []byte("an old backup"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Default().SaveWithBackup(path)
	if err != nil {
		t.Fatalf("SaveWithBackup: %v", err)
	}
	if got != backup {
		t.Fatalf("backup = %q, want %q", got, backup)
	}
	info, err := os.Stat(backup)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("permissions = %o, want 600", perm)
		}
	}
	// The new content is in there, and no temp file was left behind.
	data, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "{not json" {
		t.Fatalf("the backup holds %q, want the unreadable file", data)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "settings.json" && e.Name() != "settings.json.bak" {
			t.Fatalf("the directory must hold only the settings and its backup, found %q", e.Name())
		}
	}
}

// TestReadableSettingsAreNotBackedUp pins that a file that loads is left
// alone: there is nothing to preserve.
func TestReadableSettingsAreNotBackedUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := Default().Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	backup, err := Default().SaveWithBackup(path)
	if err != nil {
		t.Fatalf("SaveWithBackup: %v", err)
	}
	if backup != "" {
		t.Fatalf("backup = %q, want no backup of a readable file", backup)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("no backup file may be written: %v", err)
	}
}

// TestEmptySettingsAreNotBackedUp pins that an empty file — the "not
// configured" marker — is not treated as unreadable.
func TestEmptySettingsAreNotBackedUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	backup, err := Default().SaveWithBackup(path)
	if err != nil {
		t.Fatalf("SaveWithBackup: %v", err)
	}
	if backup != "" {
		t.Fatalf("backup = %q, want no backup of an empty file", backup)
	}
}

// TestEncryptedSaveDropsAStalePlaintextBackup pins the cleanup of the backup a
// plain-text era left behind: once the settings are sealed, a .bak that is
// not itself a sealed envelope must not survive next to the file — it holds
// every connection password in the clear and defeats the point of the vault.
func TestEncryptedSaveDropsAStalePlaintextBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	plain := Default()
	plain.Connections = []Connection{{Name: "shop", Driver: "demo", Password: "hunter2"}}
	if err := plain.Save(path); err != nil {
		t.Fatalf("Save plaintext: %v", err)
	}
	// The main file becomes unreadable garbage: the encrypted save that
	// follows keeps it as .bak first — plaintext, password included.
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := plain.SetPassphrase(testPassphrase, ""); err != nil {
		t.Fatalf("SetPassphrase: %v", err)
	}
	if err := plain.Save(path); err != nil {
		t.Fatalf("Save encrypted: %v", err)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("the stale plaintext backup must be dropped, stat = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(raw), "hunter2") {
		t.Fatalf("the sealed file must not contain the connection password:\n%s", raw)
	}
	readVault(t, path) // the sealed main file is intact
}

// TestEncryptedSaveKeepsACiphertextBackup pins the other half of the same
// rule: a backup made from an already-encrypted file is sealed itself, so
// further encrypted saves keep it — there is nothing left to leak.
func TestEncryptedSaveKeepsACiphertextBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s := Default()
	s.Connections = []Connection{secretConnection}
	if err := s.SetPassphrase(testPassphrase, ""); err != nil {
		t.Fatalf("SetPassphrase: %v", err)
	}
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// The sealed file turns unreadable in a way that still carries the vault
	// (valid JSON, structure no longer fits the settings): the next save
	// keeps it as .bak, and that backup is ciphertext.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	corrupt := strings.Replace(string(raw), `"version": 1`, `"version": "corrupt"`, 1)
	if corrupt == string(raw) {
		t.Fatal("the corruption must actually change the file")
	}
	if err := os.WriteFile(path, []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}
	backup, err := s.SaveWithBackup(path)
	if err != nil {
		t.Fatalf("SaveWithBackup: %v", err)
	}
	if backup != path+".bak" {
		t.Fatalf("backup = %q, want %q", backup, path+".bak")
	}
	// The backup parses and holds a vault: it is sealed, not stale plaintext.
	assertSealedBackup := func() {
		t.Helper()
		backed, err := os.ReadFile(path + ".bak")
		if err != nil {
			t.Fatalf("ReadFile(.bak): %v", err)
		}
		secretless(t, string(backed))
		var doc struct {
			Vault *Vault `json:"vault"`
		}
		if err := json.Unmarshal(backed, &doc); err != nil {
			t.Fatalf("the backup must parse: %v", err)
		}
		if doc.Vault == nil {
			t.Fatalf("the backup must carry the vault:\n%s", backed)
		}
	}
	assertSealedBackup()

	// Saving again (still encrypted) leaves the ciphertext backup alone.
	backup, err = s.SaveWithBackup(path)
	if err != nil {
		t.Fatalf("second SaveWithBackup: %v", err)
	}
	if backup != "" {
		t.Fatalf("backup = %q, want none: the fresh file is readable", backup)
	}
	assertSealedBackup()
}

// TestEncryptedSaveReportsNoBackupItDeleted pins the reporting half of the
// encrypted-save cleanup: when the settings file is unreadable garbage, the
// save keeps it aside as .bak first and then — because the fresh bytes carry
// the vault — discards that very backup as stale plaintext. The returned
// path must not point at the deleted file (the UI would announce a backup
// that no longer exists).
func TestEncryptedSaveReportsNoBackupItDeleted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	plain := Default()
	plain.Connections = []Connection{{Name: "shop", Driver: "demo", Password: "hunter2"}}
	if err := plain.SetPassphrase(testPassphrase, ""); err != nil {
		t.Fatalf("SetPassphrase: %v", err)
	}
	backup, err := plain.SaveWithBackup(path)
	if err != nil {
		t.Fatalf("SaveWithBackup: %v", err)
	}
	if backup != "" {
		if _, statErr := os.Stat(backup); statErr != nil {
			t.Fatalf("the reported backup %q must exist: %v", backup, statErr)
		}
		t.Fatalf("backup = %q, want none: the kept backup was stale plaintext and was discarded", backup)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("the stale plaintext backup must be dropped, stat = %v", err)
	}
	readVault(t, path) // the sealed main file is intact
}

// TestPlaintextSaveKeepsTheBackupBehaviour pins the untouched plain-text
// path: without a vault, a save never drops a pre-existing .bak — only the
// encrypted path discards stale plaintext backups. (The rest of the .bak
// behaviour — unreadable files are kept aside, readable ones are not — is
// pinned by TestBackupKeepsAFileThat*, TestReadableSettingsAreNotBackedUp and
// friends above.)
func TestPlaintextSaveKeepsTheBackupBehaviour(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	stale := []byte("an old plaintext backup")
	if err := os.WriteFile(path+".bak", stale, 0o600); err != nil {
		t.Fatal(err)
	}
	s := Default()
	s.Connections = []Connection{{Name: "shop", Driver: "demo", Password: "hunter2"}}
	backup, err := s.SaveWithBackup(path) // no file at path yet: nothing to keep aside
	if err != nil {
		t.Fatalf("SaveWithBackup: %v", err)
	}
	if backup != "" {
		t.Fatalf("backup = %q, want none: there was no previous file", backup)
	}
	kept, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("the plaintext path must keep the existing backup: %v", err)
	}
	if string(kept) != string(stale) {
		t.Fatalf("the backup = %q, want it untouched (%q)", kept, stale)
	}
	// The main file went to disk as plain text, as it always did.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(raw), "hunter2") {
		t.Fatalf("a plaintext save must keep writing the connections in the clear:\n%s", raw)
	}
}

// ---- The encrypted saved connections (settings/vault integration) ----

// The distinctive markers a sealed file must not contain: each one is a
// connection value (host, user, …) an attacker is looking for.
var sealedAway = []string{
	"secret-name-42", "secret-dsn-42", "secret-host-42", "secret-user-42",
	"secret-pw-42", "secret-env-42", "secret-db-42", "secret-file-42",
	"secret-schema-42", "secret-ssl-42",
}

var secretConnection = Connection{
	Name:        "secret-name-42",
	Driver:      "postgres",
	DSN:         "secret-dsn-42",
	Host:        "secret-host-42",
	Port:        5432,
	User:        "secret-user-42",
	Password:    "secret-pw-42",
	PasswordEnv: "secret-env-42",
	Database:    "secret-db-42",
	File:        "secret-file-42",
	Schema:      "secret-schema-42",
	SSLMode:     "secret-ssl-42",
}

// testPassphrase is the passphrase every encryption test uses. It never has
// to be strong: the KDF cost is the point, not the entropy.
const testPassphrase = "connecting test phrase"

// readVault extracts the vault envelope a saved file holds.
func readVault(t *testing.T, path string) vault.Envelope {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var doc struct {
		Vault *vault.Envelope `json:"vault"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("the saved file must be valid JSON: %v", err)
	}
	if doc.Vault == nil {
		t.Fatalf("the file must hold a vault:\n%s", data)
	}
	return *doc.Vault
}

// secretless asserts that no connection value appears in the text: by default
// the standard markers, plus any extras a test cares about.
func secretless(t *testing.T, data string, extra ...string) {
	t.Helper()
	for _, marker := range append(sealedAway, extra...) {
		if strings.Contains(data, marker) {
			t.Fatalf("the output must not contain %q:\n%s", marker, data)
		}
	}
}

func TestSealedFileHoldsNoConnectionValues(t *testing.T) {
	s := Default()
	s.Connections = []Connection{secretConnection}
	if err := s.SetPassphrase(testPassphrase, "the hint reminder"); err != nil {
		t.Fatalf("SetPassphrase: %v", err)
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	secretless(t, string(data))
	readVault(t, path)

	// Reloading finds the vault locked: no kiv, no connections.
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !got.Encrypted() || !got.Locked() {
		t.Fatalf("reloaded = encrypted %v locked %v, want both true", got.Encrypted(), got.Locked())
	}
	if len(got.Connections) != 0 {
		t.Fatalf("connections = %+v, want none before unlocking", got.Connections)
	}

	// Unlocking brings the list back, every field intact.
	if err := got.Unlock(testPassphrase); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if !reflect.DeepEqual(got.Connections, s.Connections) {
		t.Fatalf("unlocked connections:\n got %+v\nwant %+v", got.Connections, s.Connections)
	}

	// A sealed file that later fails to parse is kept aside as .bak — and
	// because every form of that file is sealed, the backup leaks no
	// connection values either (docs/secrets-encryption.md).
	sealedBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := os.WriteFile(path, []byte("corrupt;}"+string(sealedBytes)), 0o600); err != nil {
		t.Fatal(err)
	}
	backup, err := Default().SaveWithBackup(path)
	if err != nil {
		t.Fatalf("SaveWithBackup: %v", err)
	}
	if backup != path+".bak" {
		t.Fatalf("backup = %q, want %q", backup, path+".bak")
	}
	backed, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	secretless(t, string(backed))
}

func TestUnlockRejectsWrongPassphrase(t *testing.T) {
	s := Default()
	s.Connections = []Connection{secretConnection}
	if err := s.SetPassphrase(testPassphrase, ""); err != nil {
		t.Fatalf("SetPassphrase: %v", err)
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := got.Unlock("not the phrase"); !errors.Is(err, vault.ErrWrongPassphrase) {
		t.Fatalf("err = %v, want vault.ErrWrongPassphrase", err)
	}
	if !got.Locked() || len(got.Connections) != 0 {
		t.Fatalf("a failed unlock must leave the settings locked, connections = %+v", got.Connections)
	}
	if err := got.Unlock(testPassphrase); err != nil {
		t.Fatalf("Unlock with the right phrase: %v", err)
	}
	if got.Locked() || !reflect.DeepEqual(got.Connections, s.Connections) {
		t.Fatalf("the right phrase must restore the connections, locked = %v", got.Locked())
	}
}

func TestUnlockedSaveRotatesNonceKeepsSalt(t *testing.T) {
	s := Default()
	s.Connections = []Connection{secretConnection, {Name: "plain", Driver: "demo"}}
	if err := s.SetPassphrase(testPassphrase, ""); err != nil {
		t.Fatalf("SetPassphrase: %v", err)
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	first := readVault(t, path)

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := loaded.Unlock(testPassphrase); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if err := loaded.Save(path); err != nil {
		t.Fatalf("Save unlocked: %v", err)
	}
	second := readVault(t, path)
	if !bytesEqual(second.KDF.Salt, first.KDF.Salt) {
		t.Fatal("a save must keep the salt: the key was derived from it and the KDF must not run again")
	}
	if bytesEqual(second.Cipher.Nonce, first.Cipher.Nonce) {
		t.Fatal("a save must draw a fresh nonce; AES-GCM may not reuse one")
	}
	if err := loaded.Save(path); err != nil {
		t.Fatalf("second unlocked Save: %v", err)
	}
	third := readVault(t, path)
	if bytesEqual(third.Cipher.Nonce, second.Cipher.Nonce) {
		t.Fatal("re-saving must rotate the nonce every time")
	}
	if _, _, err := vault.Open(testPassphrase, &third); err != nil {
		t.Fatalf("the resealed envelope must stay openable: %v", err)
	}
}

func TestLockedSaveWritesTheEnvelopeUnchanged(t *testing.T) {
	s := Default()
	s.Connections = []Connection{secretConnection}
	if err := s.SetPassphrase(testPassphrase, ""); err != nil {
		t.Fatalf("SetPassphrase: %v", err)
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	first := readVault(t, path)

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// A locked session still saves preferences (theme change and alike) —
	// and must write back the very envelope it read, nothing resealed.
	loaded.Theme = ThemeLight
	if err := loaded.Save(path); err != nil {
		t.Fatalf("Save locked: %v", err)
	}
	second := readVault(t, path)
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("a locked save must not touch the envelope:\n got %+v\nwant %+v", second, first)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	secretless(t, string(after))
}

func TestLegacyFileWithoutVaultKeepsWorking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	body := `{"version":1,"connections":[{"name":"old","driver":"postgres","host":"legacy-host","password":"legacy-pw"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Encrypted() || len(got.Connections) != 1 || got.Connections[0].Host != "legacy-host" {
		t.Fatalf("an old-style file must load as before, got %+v", got)
	}
	// Round trip through save: still plain text, still no vault key.
	got.Connections[0].Port = 5433
	if err := got.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(after), "vault") {
		t.Fatalf("saving plain settings must not introduce a vault:\n%s", after)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if len(back.Connections) != 1 || back.Connections[0].Port != 5433 {
		t.Fatalf("connections must survive a legacy save, got %+v", back.Connections)
	}
}

func TestVaultWinsOverPlaintextConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	// A hostile/stale file with both forms. The vault is a fake (only its
	// existence matters to Load), but the connections look real.
	body := `{"version":1,` +
		`"connections":[{"name":"shadow","driver":"postgres","host":"downgrade-host","password":"downgrade-pw"}],` +
		`"vault":{"version":1,"kdf":{"algo":"pbkdf2-sha256","iterations":600000,"salt":"c2FsdHNhbHRzYWx0c2FsdA=="},` +
		`"cipher":{"algo":"aes-256-gcm","nonce":"bm9uY2Vub25jZW5v"},"data":"Y2lwaGVydGV4dA=="}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !got.Encrypted() || !got.Locked() {
		t.Fatalf("the vault must win: encrypted = %v, locked = %v", got.Encrypted(), got.Locked())
	}
	if len(got.Connections) != 0 {
		t.Fatalf("plaintext next to a vault must be dropped, got %+v", got.Connections)
	}
	// The plaintext it dropped is not recoverable either way: the fake vault
	// refuses to open, and the connections stay nowhere in memory.
	if err := got.Unlock("whatever"); err == nil {
		t.Fatal("a fake vault must not open")
	}
	if len(got.Connections) != 0 || strings.Contains(fmt.Sprint(got), "downgrade-host") {
		t.Fatal("the dropped plaintext must not reappear anywhere in the settings")
	}
	// Saving the downgraded file writes the vault back, still without any
	// connection values.
	if err := got.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	secretless(t, string(after), "downgrade-host", "downgrade-pw", "shadow")
}

func TestJSONHidesConnectionsWhileSealed(t *testing.T) {
	s := Default()
	s.Connections = []Connection{secretConnection}
	if err := s.SetPassphrase(testPassphrase, ""); err != nil {
		t.Fatalf("SetPassphrase: %v", err)
	}
	// Unlocked: the key is in memory, and this is exactly the state where a
	// naive dump would print everything.
	text, err := s.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	secretless(t, text)
	if !strings.Contains(text, "vault") {
		t.Fatalf("the output must show the sealed envelope:\n%s", text)
	}
	// Locked: same rule.
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	locked, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	text, err = locked.JSON()
	if err != nil {
		t.Fatalf("JSON locked: %v", err)
	}
	secretless(t, text)
}

func TestRemoveEncryptionReturnsToPlaintext(t *testing.T) {
	s := Default()
	s.Connections = []Connection{secretConnection}
	if err := s.SetPassphrase(testPassphrase, ""); err != nil {
		t.Fatalf("SetPassphrase: %v", err)
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// The wrong passphrase changes nothing.
	if err := loaded.RemoveEncryption("not the phrase"); !errors.Is(err, vault.ErrWrongPassphrase) {
		t.Fatalf("err = %v, want vault.ErrWrongPassphrase", err)
	}
	if !loaded.Encrypted() {
		t.Fatal("a failed verification must keep the vault")
	}
	if err := loaded.RemoveEncryption(testPassphrase); err != nil {
		t.Fatalf("RemoveEncryption: %v", err)
	}
	if loaded.Encrypted() || loaded.Locked() {
		t.Fatalf("after removal encrypted = %v locked = %v, want false", loaded.Encrypted(), loaded.Locked())
	}
	if !reflect.DeepEqual(loaded.Connections, s.Connections) {
		t.Fatalf("the connections must return, got %+v", loaded.Connections)
	}
	// The file goes back to the plain format (the user asked for it, and the
	// UI is expected to warn before this call).
	if err := loaded.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "vault") || !strings.Contains(string(after), "secret-host-42") {
		t.Fatalf("the file must be plain text again:\n%s", after)
	}
}

func TestEncryptedLifecycleGuards(t *testing.T) {
	s := Default()
	s.Connections = []Connection{{Name: "shop", Driver: "demo", Database: "shop"}}
	if err := s.SetPassphrase(testPassphrase, ""); err != nil {
		t.Fatalf("SetPassphrase: %v", err)
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	locked, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// A locked settings must not let a passphrase be set over the one the
	// user cannot currently recall.
	if err := locked.SetPassphrase("another", ""); err == nil {
		t.Fatal("SetPassphrase while locked must be refused")
	}
	if err := locked.SetHint("reminder"); err == nil {
		t.Fatal("SetHint while locked must be refused")
	}
	// Changing the passphrase verifies the old one even though no session is
	// unlocked.
	if err := locked.ChangePassphrase("not the phrase", testPassphrase, ""); !errors.Is(err, vault.ErrWrongPassphrase) {
		t.Fatalf("err = %v, want vault.ErrWrongPassphrase", err)
	}
	if err := locked.ChangePassphrase(testPassphrase, "new phrase", "new hint"); err != nil {
		t.Fatalf("ChangePassphrase: %v", err)
	}
	if err := locked.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if env := readVault(t, path); env.Hint != "new hint" {
		t.Fatalf("hint = %q, want %q", env.Hint, "new hint")
	}
	locked, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := locked.Unlock("new phrase"); err != nil {
		t.Fatalf("the new passphrase must open: %v", err)
	}
	if err := locked.Unlock(testPassphrase); !errors.Is(err, vault.ErrWrongPassphrase) {
		t.Fatalf("the old passphrase must be retired, err = %v", err)
	}
	// Lock drops the decrypted material.
	locked.Lock()
	if locked.Locked() != true || len(locked.Connections) != 0 {
		t.Fatal("Lock must forget the connections and the key")
	}
}

// TestSetPassphraseZeroesTheReplacedKey pins the key hygiene of SetPassphrase:
// sealing an already-unlocked session under a new passphrase replaces the
// session key, and the retired one must be wiped like Lock does, not left
// for the garbage collector.
func TestSetPassphraseZeroesTheReplacedKey(t *testing.T) {
	s := Default()
	s.Connections = []Connection{{Name: "shop", Driver: "demo"}}
	if err := s.SetPassphrase(testPassphrase, ""); err != nil {
		t.Fatalf("SetPassphrase: %v", err)
	}
	oldKey := s.key
	if oldKey == nil || oldKey.Iterations() == 0 {
		t.Fatal("SetPassphrase must install a live session key")
	}
	if err := s.SetPassphrase("another phrase 12", ""); err != nil {
		t.Fatalf("second SetPassphrase (unlocked): %v", err)
	}
	if s.key == oldKey {
		t.Fatal("the second seal must install a fresh key")
	}
	if oldKey.Iterations() != 0 {
		t.Fatal("the replaced session key must be zeroed")
	}
	if s.key.Iterations() == 0 {
		t.Fatal("the new session key must be live")
	}
}

// TestChangePassphraseZeroesTheRetiredKeys pins both halves of the key
// hygiene of ChangePassphrase: a refused change (wrong old passphrase) must
// leave the session key intact and the session sealable, and a confirmed one
// must wipe the replaced session key before the new one takes over.
func TestChangePassphraseZeroesTheRetiredKeys(t *testing.T) {
	s := Default()
	s.Connections = []Connection{{Name: "shop", Driver: "demo"}}
	if err := s.SetPassphrase(testPassphrase, ""); err != nil {
		t.Fatalf("SetPassphrase: %v", err)
	}
	sessionKey := s.key

	// The refusal: nothing is wiped and the session stays usable.
	if err := s.ChangePassphrase("not the phrase", "new phrase 456", ""); !errors.Is(err, vault.ErrWrongPassphrase) {
		t.Fatalf("err = %v, want vault.ErrWrongPassphrase", err)
	}
	if s.key != sessionKey || sessionKey.Iterations() == 0 {
		t.Fatal("a refused change must leave the session key intact and live")
	}
	if _, err := s.Seal(); err != nil {
		t.Fatalf("the session must still seal after a refused change: %v", err)
	}

	// The success: the retired session key is wiped, the new one is live.
	if err := s.ChangePassphrase(testPassphrase, "new phrase 456", ""); err != nil {
		t.Fatalf("ChangePassphrase: %v", err)
	}
	if s.key == sessionKey {
		t.Fatal("a confirmed change must install a fresh session key")
	}
	if sessionKey.Iterations() != 0 {
		t.Fatal("the replaced session key must be zeroed")
	}
	if s.key.Iterations() == 0 {
		t.Fatal("the new session key must be live")
	}
	if _, err := s.Seal(); err != nil {
		t.Fatalf("the session must seal under the new key: %v", err)
	}
}

func TestVeryLongHintIsAcceptedAndRoundTrips(t *testing.T) {
	hint := strings.Repeat("х", 200) // multibyte runes, longer than a status line
	s := Default()
	s.Connections = []Connection{{Name: "shop", Driver: "demo"}}
	if err := s.SetPassphrase(testPassphrase, hint); err != nil {
		t.Fatalf("SetPassphrase with a %d-rune hint: %v", len([]rune(hint)), err)
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if gotHint := readVault(t, path).Hint; gotHint != hint {
		t.Fatalf("hint = %q, want it kept in full", gotHint)
	}
}

// TestSealMatchesWhatSaveWithBackupWrites pins that the bytes Seal returns
// are exactly what the write path persists, for the split where the UI seals
// on its own goroutine and hands the bytes to a background write. An unlocked
// save re-seals with a fresh nonce on every call, so Seal and a *separate*
// SaveWithBackup cannot be byte-compared while unlocked (the ciphertext
// legitimately differs); the two stable halves are pinned instead: Seal's
// bytes go to disk verbatim through WriteWithBackup, and a locked save —
// whose envelope is written untouched — is byte-identical to Seal, newline
// included.
func TestSealMatchesWhatSaveWithBackupWrites(t *testing.T) {
	s := Default()
	s.Connections = []Connection{secretConnection}
	if err := s.SetPassphrase(testPassphrase, ""); err != nil {
		t.Fatalf("SetPassphrase: %v", err)
	}
	dir := t.TempDir()

	// Unlocked: the write path persists Seal's bytes verbatim.
	data, err := s.Seal()
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	path := filepath.Join(dir, "settings.json")
	if _, err := WriteWithBackup(path, data); err != nil {
		t.Fatalf("WriteWithBackup: %v", err)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(onDisk, data) {
		t.Fatalf("the write path must persist Seal's bytes verbatim:\n seal %q\ndisk %q", data, onDisk)
	}

	// Locked: SaveWithBackup's bytes equal Seal's, byte for byte.
	locked, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !locked.Encrypted() || !locked.Locked() {
		t.Fatalf("the reloaded settings must be encrypted and locked")
	}
	sealed, err := locked.Seal()
	if err != nil {
		t.Fatalf("Seal locked: %v", err)
	}
	if _, err := locked.SaveWithBackup(path); err != nil {
		t.Fatalf("SaveWithBackup locked: %v", err)
	}
	if onDisk, err = os.ReadFile(path); err != nil {
		t.Fatalf("ReadFile: %v", err)
	} else if !bytes.Equal(onDisk, sealed) {
		t.Fatalf("SaveWithBackup must write exactly Seal's bytes:\n seal %q\ndisk %q", sealed, onDisk)
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNormalizeTrimsSSLMode(t *testing.T) {
	s := Settings{Connections: []Connection{{Name: "shop", Driver: "postgres", SSLMode: "  require "}, {Name: "clean", Driver: "demo"}}}
	if !s.Normalize() {
		t.Fatal("an untrimmed sslMode must be reported as changed")
	}
	if got := s.Connections[0].SSLMode; got != "require" {
		t.Fatalf("sslMode = %q, want the whitespace trimmed", got)
	}
	if s.Connections[1].SSLMode != "" {
		t.Fatalf("an absent sslMode must stay empty, got %q", s.Connections[1].SSLMode)
	}
}
