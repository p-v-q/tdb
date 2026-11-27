// Package settings manages tdb's user preferences: loading, validation and
// atomic persistence.
//
// Design notes:
//   - The file format is JSON (standard library only, no extra dependency),
//     with readable lowerCamelCase fields a user can hand-edit.
//   - Load starts from Default and then unmarshals, so fields missing from the
//     file keep their default value: a user only writes what they want to
//     override.
//   - Normalize clamps numbers and repairs invalid enums, so a broken file
//     cannot start the program in a nonsensical state.
//   - Save is atomic (temp file + rename) so a crash never leaves a half-written
//     file behind.
//   - Unknown fields are ignored, which keeps files written by a newer version
//     readable by an older one.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/p-v-q/tdb/vault"
)

// Version is the settings schema version, written to disk to ease migrations.
const Version = 1

// Theme values.
const (
	ThemeDark  = "dark"
	ThemeLight = "light"
)

// Valid pane size ranges. The UI and the settings loader share these bounds so
// the limits cannot drift apart.
const (
	MinObjectsWidth  = 12
	MaxObjectsWidth  = 120
	MinResultsHeight = 3
	MaxResultsHeight = 60
)

// EnvConfigPath overrides the settings file path (handy for tests and for
// managing several configurations side by side).
const EnvConfigPath = "TDB_CONFIG"

// appDirName is the configuration directory name under os.UserConfigDir.
const appDirName = "tdb"

// fileName is the settings file name.
const fileName = "settings.json"

// Layout holds the pane layout preferences.
type Layout struct {
	ObjectsVisible bool `json:"objectsVisible"`
	ObjectsWidth   int  `json:"objectsWidth"`
	ResultsVisible bool `json:"resultsVisible"`
	ResultsHeight  int  `json:"resultsHeight"`
}

// Connection is a saved database connection.
//
// The fields cover the usual host/port/user/database shape plus a free-form DSN
// that wins when it is set. The password is stored in clear text in the settings
// file (mode 0600); a later version can move it to the system keychain.
//
// PasswordEnv names an environment variable whose value is used as the password
// at connect time. When it is set, the password itself is never written to the
// settings file: the resolved value lives only in the driver configuration and
// Password stays empty.
type Connection struct {
	Name     string `json:"name"`
	Driver   string `json:"driver"` // driver name: demo, postgres, mysql, …
	DSN      string `json:"dsn,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	// PasswordEnv is an environment variable read as the password. Empty means
	// the password is stored in Password directly.
	PasswordEnv string `json:"passwordEnv,omitempty"`
	Database    string `json:"database,omitempty"`
	File        string `json:"file,omitempty"` // file-based databases (SQLite, …)
	// Schema is the schema the connection works in. Once the connection is
	// open, unqualified names resolve there (PostgreSQL applies it as the
	// session's search_path). Empty means the server default is used, which
	// is also what a settings file written before this field existed means.
	Schema string `json:"schema,omitempty"`
	// SSLMode is the transport encryption the driver asks the server for.
	// Empty means the driver's own default; values are driver-specific
	// ("require", "verify-full", "skip-verify", …) and are passed through
	// verbatim.
	SSLMode string `json:"sslMode,omitempty"`
}

// Settings holds every user preference.
type Settings struct {
	Version     int          `json:"version"`
	Locale      string       `json:"locale"`   // empty means "follow the system locale"
	Theme       string       `json:"theme"`    // ThemeDark or ThemeLight
	Mouse       bool         `json:"mouse"`    // whether mouse events are handled
	Gutter      bool         `json:"gutter"`   // whether the editor shows line numbers
	Icons       bool         `json:"icons"`    // Nerd Font icons instead of text labels
	ReadOnly    bool         `json:"readOnly"` // whether write/DDL statements are blocked
	Layout      Layout       `json:"layout"`   // initial pane layout
	Connections []Connection `json:"connections,omitempty"`
	// Vault holds the encrypted saved connections. Nil means plain text
	// (the format of every file written before this field existed); non-nil
	// means the whole connections array is sealed inside Vault and the
	// Connections field of anything written to disk must stay empty.
	Vault *Vault `json:"vault,omitempty"`
	// key is the unlocked vault key, present only while the vault is
	// unlocked in this session. It must never reach the file: connections
	// are re-sealed with it on save and it is dropped by Lock.
	key *vault.Key `json:"-"`
}

// Vault is the sealed-connections envelope some of the settings commands
// hand around as a value.
type Vault = vault.Envelope

// Default returns the factory settings.
func Default() Settings {
	return Settings{
		Version: Version,
		Locale:  "", // follow the system locale
		Theme:   ThemeDark,
		Mouse:   true,
		Gutter:  true,
		Icons:   true,
		// ReadOnly stays false: writes are allowed until the user opts in.
		Layout: Layout{
			ObjectsVisible: true,
			ObjectsWidth:   32,
			ResultsVisible: true,
			ResultsHeight:  8,
		},
	}
}

// Normalize repairs invalid or out-of-range fields and reports whether anything
// changed.
func (s *Settings) Normalize() bool {
	changed := false

	if s.Version == 0 {
		s.Version = Version
		changed = true
	}
	switch s.Theme {
	case ThemeDark, ThemeLight:
	default:
		s.Theme = Default().Theme
		changed = true
	}
	if s.Locale != strings.TrimSpace(s.Locale) {
		s.Locale = strings.TrimSpace(s.Locale)
		changed = true
	}
	if clampInt(&s.Layout.ObjectsWidth, MinObjectsWidth, MaxObjectsWidth) {
		changed = true
	}
	if clampInt(&s.Layout.ResultsHeight, MinResultsHeight, MaxResultsHeight) {
		changed = true
	}
	// Saved connections: keep names usable, drivers filled in and ports sane.
	for i := range s.Connections {
		c := &s.Connections[i]
		if trimmed := strings.TrimSpace(c.Name); trimmed != c.Name {
			c.Name = trimmed
			changed = true
		}
		if c.Name == "" {
			c.Name = fmt.Sprintf("connection-%d", i+1)
			changed = true
		}
		if trimmed := strings.TrimSpace(c.Driver); trimmed != c.Driver {
			c.Driver = trimmed
			changed = true
		}
		if c.Driver == "" {
			c.Driver = "demo"
			changed = true
		}
		if clampInt(&c.Port, 0, 65535) {
			changed = true
		}
		if trimmed := strings.TrimSpace(c.SSLMode); trimmed != c.SSLMode {
			c.SSLMode = trimmed
			changed = true
		}
	}
	return changed
}

// clampInt clamps *v into [lo, hi] and reports whether it changed the value.
func clampInt(v *int, lo, hi int) bool {
	switch {
	case *v < lo:
		*v = lo
		return true
	case *v > hi:
		*v = hi
		return true
	}
	return false
}

// Path returns the settings file path: the TDB_CONFIG environment variable
// when set, otherwise os.UserConfigDir()/tdb/settings.json.
func Path() (string, error) {
	if p := strings.TrimSpace(os.Getenv(EnvConfigPath)); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate the user config directory: %w", err)
	}
	return filepath.Join(dir, appDirName, fileName), nil
}

// Load reads the settings stored at path.
//
// A missing file is not an error: the defaults are returned. A corrupt file
// returns both the defaults and an error so the caller can decide between
// warning the user and silently using the defaults.
func Load(path string) (Settings, error) {
	s := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return s, nil
		}
		return s, fmt.Errorf("reading settings: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return s, nil // an empty file means "not configured"
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return Default(), fmt.Errorf("parsing settings (%s): %w", path, err)
	}
	// A file may not be trusted with both the vault and a clear-connections
	// array: a vault plus readable connections would defeat the whole point
	// of the vault (an attacker edits the old file to whisper the passwords
	// back in, or an old-format backup gets resurrected). The vault wins —
	// it is the sealed, authenticated form — and the plaintext array is
	// dropped, so nothing but an unlock can bring the connections back.
	if s.Vault != nil {
		s.Connections = nil
	}
	s.key = nil // loading never opens the vault; the passphrase comes later
	s.Normalize()
	return s, nil
}

// LoadDefault loads the settings from the default path, falling back to the
// defaults on any error. It returns the settings, the path actually used and
// the error (callers may use the error only for logging or a status message).
func LoadDefault() (Settings, string, error) {
	path, err := Path()
	if err != nil {
		return Default(), "", err
	}
	s, err := Load(path)
	return s, path, err
}

// Save writes the settings atomically: a temp file in the same directory,
// followed by a rename. An existing file that cannot be parsed is first kept as
// path+".bak" (see SaveWithBackup).
func (s Settings) Save(path string) error {
	_, err := s.SaveWithBackup(path)
	return err
}

// SaveWithBackup writes the settings exactly like Save and additionally reports
// the path of the backup it kept. It returns "" when the previous file was
// missing or already readable as settings, so the caller can tell "nothing to
// preserve" from "the unreadable file was saved aside". The UI uses the
// returned path to tell the user where their unreadable file went. It also
// returns "" when an encrypted save had to discard the kept backup itself as
// stale plaintext — reporting that path would point at a deleted file (see
// WriteWithBackup).
//
// It is the composition of Seal and WriteWithBackup — the two halves the UI
// splits across goroutines (marshal on the UI goroutine, file IO in a
// background command). Called as one function, behaviour is unchanged.
func (s Settings) SaveWithBackup(path string) (string, error) {
	data, err := s.Seal()
	if err != nil {
		return "", err
	}
	return WriteWithBackup(path, data)
}

// Seal returns the exact bytes a save would put on disk: the marshalled
// settings (marshalForDisk) plus the trailing newline SaveWithBackup writes.
//
// It must be called on the goroutine that owns the settings value. With an
// unlocked vault the marshal re-seals the envelope, reading the session key —
// the same key Lock zeroes in place — and the marshal also reads the live
// Connections slice, which the UI edits in place (saveConnection,
// replaceSavedConnection). Handing a Settings *copy* to a background command
// that marshals would race both of those; handing it the sealed *bytes*
// cannot.
func (s Settings) Seal() ([]byte, error) {
	data, err := s.marshalForDisk()
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// WriteWithBackup writes the bytes Seal produced to path, atomically and with
// the settings file's private mode, and reports the path of the backup it kept
// (see SaveWithBackup for what counts as unreadable). The data is written
// verbatim: marshalling already happened on the goroutine that owns the
// settings, so this function — safe to run in a background command — touches
// nothing but the file system. It takes no settings state on purpose; whether
// the save is encrypted is judged from the sealed bytes themselves.
func WriteWithBackup(path string, data []byte) (string, error) {
	// Preserve a corrupt file before overwriting it: silently replacing it would
	// destroy the only copy of the user's configuration. Best-effort — a failed
	// copy must not block saving the fresh settings.
	backup, _ := backupUnreadable(path)

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return backup, fmt.Errorf("creating the config directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "."+fileName+".*")
	if err != nil {
		return backup, fmt.Errorf("creating a temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // a no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return backup, fmt.Errorf("writing settings: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return backup, fmt.Errorf("setting file permissions: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return backup, fmt.Errorf("flushing settings to disk: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return backup, fmt.Errorf("closing the temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return backup, fmt.Errorf("replacing the settings file: %w", err)
	}
	// The file on disk is now at least as sealed as the data written to it:
	// with a vault envelope inside, the plaintext connections cannot survive
	// a save anywhere next to it. That makes an older plaintext backup a
	// plain liability (see discardStalePlaintextBackup), so it is cleaned up
	// here and only when this save was encrypted — plain-text saves must keep
	// behaving exactly as they always did. The envelope in the bytes is the
	// same condition s.Encrypted() would report: marshalForDisk emits the
	// vault field if, and only if, the settings carry one.
	var doc struct {
		Vault *vault.Envelope `json:"vault"`
	}
	if err := json.Unmarshal(data, &doc); err == nil && doc.Vault != nil {
		// Two rules collide here when the kept backup is the stale
		// plaintext one this very call just wrote: the security rule says
		// delete it (a plaintext backup next to a sealed file leaks the
		// connection passwords), and the reporting rule says tell the user
		// where their unreadable file went. Deleting wins, but the report
		// must then be cleared too — announcing a path that no longer
		// exists would send the user looking for a file that is gone.
		if discardStalePlaintextBackup(path) {
			backup = ""
		}
	}
	return backup, nil
}

// discardStalePlaintextBackup removes path+".bak" when that backup is not
// itself a sealed envelope, and reports whether it actually removed the file.
// The report lets the caller drop a backup path it would otherwise announce:
// with an encrypted save the removed backup can be the very one the call kept
// seconds earlier, and pointing the user at a deleted file is worse than
// saying nothing (see WriteWithBackup).
//
// Why deleting is right (and not a data loss): a .bak written during the
// plain-text era holds every connection password in the clear, and once the
// settings have moved into the vault the main file no longer leaks — but the
// backup still does. That defeats the whole point of the encryption for the
// threat model the vault exists for: anyone who can read the configuration
// directory (a backup tool, a sync folder, another user on the machine) gets
// the secrets without ever touching the passphrase. The cost of the delete is
// bounded: an encrypted save only ever revises what the vault already holds,
// and the backup predates it — losing that stale copy cannot lose any secret
// the live file does not carry more safely.
//
// The criterion matches the one Load/backupUnreadable use: a backup that
// parses and carries a vault envelope is sealed anyway (it was made from an
// already-encrypted file) and is kept; a backup that does not parse, or that
// parses without a vault, may contain the old plaintext and is removed.
//
// Best-effort, like backupUnreadable: a failed removal must not fail the save
// (the fresh, sealed file is already on disk and intact), and the package has
// no logging of its own — the comment is the record.
func discardStalePlaintextBackup(path string) bool {
	backup := path + ".bak"
	data, err := os.ReadFile(backup)
	if err != nil {
		return false // no backup, or it vanished meanwhile: nothing to clean up
	}
	var doc struct {
		Vault *vault.Envelope `json:"vault"`
	}
	if err := json.Unmarshal(data, &doc); err == nil && doc.Vault != nil {
		return false // the backup is sealed as well; keeping it is safe
	}
	if err := os.Remove(backup); err != nil {
		// Nothing can be done about it: the sealed settings file is already
		// on disk and intact, and the next encrypted save retries the
		// removal. Not returning an error keeps the save honest — it did
		// succeed.
		return false
	}
	return true
}

// marshalForDisk returns the settings bytes as they may appear on disk
// (.Save/SaveWithBackup) or in a user-visible dump (.JSON). Both share this
// copy because both have the same hiding rule: what the user can see in a
// statistic can also land in a backup.
//
// The rules, matching the design in docs/secrets-encryption.md:
//   - No vault: the connections are written as plain text, as they always
//     were. The file is still 0600.
//   - Vault present and unlocked: the current connections are re-sealed into
//     a copy of the envelope with a fresh nonce (the key is already in
//     memory, so no KDF run happens), and the copy's Connections field is
//     emptied. No connection value ever reaches the file.
//   - Vault present and locked: the existing envelope is written back
//     untouched; nothing decrypted can be in memory to lose. The copy's
//     Connections field is emptied even then, so a programming slip cannot
//     resurrect plaintext next to the ciphertext.
//
// The receiver is a value copy, so resealing and clearing here never touches
// the live settings; the JSON tag on the key field keeps the key out of any
// serialization regardless.
func (s Settings) marshalForDisk() ([]byte, error) {
	s.Version = Version
	s.Normalize()
	if s.Vault == nil {
		return json.MarshalIndent(s, "", "  ")
	}

	env := *s.Vault // a shallow copy; Reseal replaces the nonce/ciphertext slices rather than rewriting them in place
	if s.key != nil {
		connections := s.Connections
		if connections == nil {
			connections = []Connection{} // an empty vault better unseal to [], not null
		}
		plaintext, err := json.Marshal(connections)
		if err != nil {
			return nil, fmt.Errorf("encoding the encrypted connections: %w", err)
		}
		if err := env.Reseal(s.key, plaintext); err != nil {
			return nil, fmt.Errorf("sealing the connections: %w", err)
		}
	}
	s.Connections = nil
	s.key = nil
	s.Vault = &env
	return json.MarshalIndent(s, "", "  ")
}

// backupUnreadable copies an existing settings file that cannot be read as
// settings to path+".bak". A missing file, an empty file (the "not configured"
// marker) and a file that loads as settings are left alone; a copy failure is
// reported as "no backup" so the caller can still save. It returns the backup
// path and whether a backup was written.
//
// The criterion is the one Load uses: a file that is valid JSON but does not
// fit the Settings structure ("readOnly": "yes") is just as unreadable as a
// broken one, and it would be overwritten without a trace otherwise.
func backupUnreadable(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return "", false // an empty file means "not configured"
	}
	if settingsLoadable(data) {
		return "", false
	}
	backup := path + ".bak"
	if err := writePrivateFile(backup, data); err != nil {
		return "", false
	}
	return backup, true
}

// settingsLoadable reports whether the data can be read as settings.
//
// It parses the data the way Load does, so the two cannot disagree about
// what a readable settings file is: a file whose JSON is valid but whose
// structure does not fit Settings cannot be loaded, and is therefore kept
// aside before it is overwritten.
func settingsLoadable(data []byte) bool {
	var s Settings
	return json.Unmarshal(data, &s) == nil
}

// writePrivateFile writes data to path with the permissions of a settings
// file (0600), atomically: a temp file in the same directory followed by a
// rename.
//
// The mode of a plain write is only applied when the file is created, so an
// existing file keeps its own permissions: a backup that was written once
// with a wider mode would keep it, and it holds the very same secrets as the
// settings file itself. Writing through a temp file gives the copy the
// private mode every time, and the rename keeps the old backup readable
// until the new one is complete.
func writePrivateFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // a no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	// Flushed to disk before the rename, like the settings save: the backup
	// holds the same secrets and a crash must not leave half of it behind.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("flushing the backup to disk: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// JSON returns the pretty-printed settings (used by -print-settings).
//
// Output goes through the same marshalling as a save (marshalForDisk): an
// unlocked vault must not turn a diagnostic into a password dump, so the
// output shows the sealed envelope while vault is present and the plain
// connections only when they were never encrypted.
func (s Settings) JSON() (string, error) {
	data, err := s.marshalForDisk()
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

// Encrypted reports whether the settings carry a vault, i.e. whether the
// connection list is stored sealed.
func (s *Settings) Encrypted() bool {
	return s.Vault != nil
}

// Locked reports whether no connection can be used right now: the vault is
// present and this session has not (or no longer) proved the passphrase.
// Settings without a vault are never locked — their connections are simply
// there, as they always were.
func (s *Settings) Locked() bool {
	return s.Vault != nil && s.key == nil
}

// Unlock opens the vault with the passphrase and fills Connections with the
// decrypted list. It fails with vault.ErrWrongPassphrase when the passphrase
// is wrong or the envelope is damaged, and leaves Connections untouched then.
// The key is the only thing remembered; the passphrase itself is never stored.
func (s *Settings) Unlock(passphrase string) error {
	if s.Vault == nil {
		return nil // nothing is sealed, so nothing needs unlocking
	}
	plaintext, key, err := vault.Open(passphrase, s.Vault)
	if err != nil {
		return err
	}
	var connections []Connection
	if err := json.Unmarshal(plaintext, &connections); err != nil {
		key.Zero()
		return fmt.Errorf("decoding the unlocked connections: %w", err)
	}
	s.Connections = connections
	s.key = key
	return nil
}

// Lock forgets the passphrase's effects: the decrypted connections drop out of
// memory and the key is wiped. The vault itself stays, so the next session
// opens with the dialog rather than the connections; a locked save writes the
// existing envelope untouched.
func (s *Settings) Lock() {
	s.Connections = nil
	if s.key != nil {
		s.key.Zero()
	}
	s.key = nil
}

// SetPassphrase seals the current connection list under a passphrase: the
// vault appears in the file and the plaintext list disappears from it. An
// already-encrypted settings must be unlocked first (the check below refuses
// otherwise), because sealing would silently replace a passphrase the user
// cannot recall. Nothing is changed on failure, and the session stays
// unlocked with the returned key.
func (s *Settings) SetPassphrase(passphrase, hint string) error {
	if s.Vault != nil && s.key == nil {
		return errors.New("cannot set a passphrase while locked; unlock first or change the passphrase")
	}
	connections := s.Connections
	if connections == nil {
		connections = []Connection{}
	}
	plaintext, err := json.Marshal(connections)
	if err != nil {
		return fmt.Errorf("encoding the connections to seal: %w", err)
	}
	env, key, err := vault.Create(passphrase, hint, plaintext)
	if err != nil {
		return err
	}
	// Re-sealing an already-unlocked session replaces its key: wipe the
	// retired one before the reference is dropped, like Lock and
	// RemoveEncryption do. The failure paths above leave the session key
	// untouched on purpose — only a confirmed change may retire it.
	if s.key != nil {
		s.key.Zero()
	}
	s.Vault = env
	s.key = key
	return nil
}

// ChangePassphrase replaces the vault's passphrase. The old passphrase is
// verified even if the session is already unlocked — knowing the key by
// having unlocked five minutes ago is not knowing the password — and then the
// payload is re-sealed under a new passphrase with a new salt, so the old
// passphrase answers only the envelope it was derived for.
func (s *Settings) ChangePassphrase(oldPassphrase, newPassphrase, hint string) error {
	if s.Vault == nil {
		return errors.New("no passphrase is set")
	}
	plaintext, oldKey, err := vault.Open(oldPassphrase, s.Vault)
	if err != nil {
		// A wrong old passphrase must leave the session exactly as it was:
		// nothing is wiped before the change is confirmed.
		return err
	}
	// The decrypted payload lives only until the reseal below consumes it.
	defer func() {
		for i := range plaintext {
			plaintext[i] = 0
		}
	}()
	key, err := s.Vault.ChangePassphrase(newPassphrase, hint, plaintext)
	if err != nil {
		oldKey.Zero()
		return err
	}
	// The change is confirmed: wipe the keys it retires — the one derived
	// here to verify the old passphrase, and the session's previous key —
	// right before the new one takes over, mirroring Lock.
	oldKey.Zero()
	if s.key != nil {
		s.key.Zero()
	}
	s.key = key
	return nil
}

// RemoveEncryption verifies the passphrase and then leaves the connections in
// the file as plain text again. That is only ever done because the user asked
// for it, deliberately: from that point on the file holds every password in
// the clear again (mode 0600 is the only protection). The connections are
// recovered from the vault (not from memory) so the visible list is exactly
// what was sealed, and both the envelope and the key are dropped.
func (s *Settings) RemoveEncryption(passphrase string) error {
	if s.Vault == nil {
		return nil // already plain text; the caller's request is a no-op
	}
	plaintext, key, err := vault.Open(passphrase, s.Vault)
	if err != nil {
		return err
	}
	var connections []Connection
	if err := json.Unmarshal(plaintext, &connections); err != nil {
		key.Zero()
		return fmt.Errorf("decoding the unlocked connections: %w", err)
	}
	key.Zero()
	s.Vault = nil
	s.key = nil
	s.Connections = connections
	return nil
}

// SetHint updates the clear-text hint stored next to the ciphertext. The hint
// helps whoever does not know the passphrase, so it needs no preprocessing to
// change — the file must be unlocked (settings without a vault have nothing
// to attach a hint to).
func (s *Settings) SetHint(hint string) error {
	if s.Vault == nil {
		return nil // plain-text settings have no envelope to hold a hint
	}
	if s.key == nil {
		return errors.New("cannot set the hint while locked; unlock first")
	}
	s.Vault.SetHint(hint)
	return nil
}
