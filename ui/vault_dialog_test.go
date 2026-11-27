package ui

// vault_dialog_test.go drives the passphrase dialogs end to end: startup
// unlock, first-run setup, change/remove/lock, the locked tree and the
// interception of connection actions. Every test that touches the disk uses
// t.TempDir() so the settings file is private, and every passphrase in these
// tests doubles as a leak check: the status bar may never quote it back.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/complete"
	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
	"github.com/p-v-q/tdb/vault"
)

// vaultTestMarkers are unique strings a test puts into a saved connection;
// after setting a passphrase, neither may appear in the settings file.
const (
	vaultTestHost     = "better-not-appear.example"
	vaultTestPassword = "better-not-appear-password"
)

// vaultTestConn is the connection the sealed file holds.
func vaultTestConn() settings.Connection {
	return settings.Connection{
		Name: "money", Driver: "demo",
		Host: vaultTestHost, Password: vaultTestPassword,
	}
}

// vaultWriteEncrypted seals conn under pass/hint and writes the file.
func vaultWriteEncrypted(t *testing.T, dir, pass, hint string) string {
	t.Helper()
	s := settings.Default()
	s.Connections = []settings.Connection{vaultTestConn()}
	if err := s.SetPassphrase(pass, hint); err != nil {
		t.Fatalf("SetPassphrase: %v", err)
	}
	path := filepath.Join(dir, "settings.json")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return path
}

// vaultModel builds a model on the given settings and file path.
func vaultModel(s settings.Settings, path string, firstRun bool) Model {
	d := dialect.MustGet("postgres")
	return New(Config{
		Title:          "tdb",
		ConnName:       "demo",
		Dialect:        d,
		Engine:         complete.NewDefaultEngine(d),
		Provider:       catalog.Static{C: catalog.DemoCatalog()},
		InitialCatalog: catalog.DemoCatalog(),
		Text:           "SELECT 1",
		Translator:     i18n.New(i18n.En),
		Settings:       s,
		SettingsPath:   path,
		FirstRun:       firstRun,
	})
}

// vaultFile loads the settings file (a locked file loads sealed: Connections
// empty, vault present).
func vaultFile(t *testing.T, path string) settings.Settings {
	t.Helper()
	s, err := settings.Load(path)
	if err != nil {
		t.Fatalf("Load(%s): %v", path, err)
	}
	return s
}

// vaultReadFile returns the raw file content, for plaintext assertions.
func vaultReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// vaultSave persists the current settings the way the UI's own command does.
func vaultSave(t *testing.T, m Model, path string) {
	t.Helper()
	if cmd := m.persistSettings(); cmd != nil {
		cmd()
	}
}

// vaultStatusNeverQuotes asserts a status message does not leak the secret
// and carries a positive marker, so the check cannot pass vacuously.
func vaultStatusNeverQuotes(t *testing.T, m Model, pass, marker string) {
	t.Helper()
	if strings.Contains(m.status.message, pass) {
		t.Fatalf("the status message must never contain the passphrase: %q", m.status.message)
	}
	if marker != "" && !strings.Contains(m.status.message, marker) {
		t.Fatalf("status = %q, want it to contain %q", m.status.message, marker)
	}
}

// vaultType types text into the focused field and moves on with tab.
func vaultType(m Model, text string) Model {
	m = typeText(m, text)
	return keyByName(m, "tab")
}

// unlockThroughDialog types the right passphrase and submits.
func unlockThroughDialog(t *testing.T, m Model, pass string) Model {
	t.Helper()
	if !m.vaultDlg.open || m.vaultDlg.mode != vaultUnlock {
		t.Fatalf("the unlock dialog must be open; mode = %v", m.vaultDlg.mode)
	}
	m = typeText(m, pass)
	return keyByName(m, "enter")
}

func TestVaultStartupLockedOpensUnlockDialog(t *testing.T) {
	dir := t.TempDir()
	pass := "unlockme123"
	path := vaultWriteEncrypted(t, dir, pass, "the one from the notes")
	m := vaultModel(vaultFile(t, path), path, false)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	if !m.settings.Locked() {
		t.Fatal("a freshly loaded encrypted settings file must be locked")
	}
	if !m.vaultDlg.open || m.vaultDlg.mode != vaultUnlock {
		t.Fatalf("startup must open the unlock dialog, mode = %v", m.vaultDlg.mode)
	}
	// The file's hint is public by design and shown to help the user.
	view := stripANSI(m.View())
	if !strings.Contains(view, "Unlock connections") {
		t.Fatalf("view must carry the unlock title:\n%s", view)
	}
	if !strings.Contains(view, "Hint: the one from the notes") {
		t.Fatalf("view must show the stored hint:\n%s", view)
	}
	// The field masks whatever is typed: nothing appears in the clear.
	m = typeText(m, "masking11")
	if got := strings.Count(stripANSI(m.View()), "•"); got < len([]rune("masking11")) {
		t.Fatalf("the passphrase field must be masked, bullets = %d:\n%s", got, m.View())
	}
}

// TestVaultHintFromTheFileIsSanitized pins the sanitize rule for the one
// text in the vault dialogs that does not come from keystrokes: the stored
// hint is read from the settings file, and a tampered, synced or shared file
// is squarely in the vault's own threat model (docs/secrets-encryption.md).
// A hint carrying ANSI/CSI/OSC sequences must not reach the terminal through
// the unlock form's hint line, nor through the prefilled change-hint field.
// The ASCII hint tests above (TestVaultStartupLockedOpensUnlockDialog,
// TestVaultManageChangeHint) pin that normal hints render and prefill
// unchanged.
func TestVaultHintFromTheFileIsSanitized(t *testing.T) {
	dir := t.TempDir()
	pass := "sanitize12"
	// A CSI sequence (clear screen) and an OSC sequence (set the terminal
	// title), the two shapes a hint would use to hijack the terminal.
	evil := "check\x1b]0;PWNED\x07 the\x1b[2J notes"
	path := vaultWriteEncrypted(t, dir, pass, evil)

	// The hostile bytes really are in the file: the vault stores the hint in
	// the clear by design, control characters included — the sanitizing
	// belongs to the UI, not to the storage.
	if got := vaultFile(t, path).Vault.Hint; !strings.Contains(got, "\x1b]0;") {
		t.Fatalf("the test hint must survive into the file unchanged, got %q", got)
	}

	m := vaultModel(vaultFile(t, path), path, false)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if !m.vaultDlg.open || m.vaultDlg.mode != vaultUnlock {
		t.Fatalf("startup must open the unlock dialog, mode = %v", m.vaultDlg.mode)
	}
	// The raw view (no stripANSI — the styles are stripped would also hide
	// the very bytes under test) must carry no OSC introducer, no clear
	// screen and no bell from the file; the printable words stay.
	view := m.View()
	for _, seq := range []string{"\x1b]", "\x1b[2J", "\x07"} {
		if strings.Contains(view, seq) {
			t.Fatalf("the unlock view must not carry the file's %q bytes:\n%q", seq, view)
		}
	}
	if !strings.Contains(stripANSI(view), "check]0;PWNED the[2J notes") {
		t.Fatalf("the cleaned hint text must still show on the unlock form:\n%s", view)
	}

	// The change-hint form prefills the field with the same stored hint: the
	// prefill must be control-free while keeping the printable text.
	m = unlockThroughDialog(t, m, pass)
	m.openVaultEntry()
	m = keyByName(m, "down") // second entry: change hint
	m = keyByName(m, "enter")
	if m.vaultDlg.mode != vaultHint {
		t.Fatalf("the second entry must be change hint, mode = %v", m.vaultDlg.mode)
	}
	field := m.vaultDlg.fields[0].value
	if !strings.Contains(field, "PWNED") {
		t.Fatalf("the printable part of the hint must survive the cleaning: %q", field)
	}
	for _, r := range field {
		if unicode.IsControl(r) {
			t.Fatalf("the prefilled hint must carry no control characters: %q", field)
		}
	}
	if v := m.View(); strings.Contains(v, "\x1b]") || strings.Contains(v, "\x1b[2J") {
		t.Fatalf("the hint form must not render the file's escape sequences:\n%q", v)
	}
}

func TestVaultUnlockWrongThenRightPassphrase(t *testing.T) {
	dir := t.TempDir()
	pass := "unlockme123"
	path := vaultWriteEncrypted(t, dir, pass, "")
	m := vaultModel(vaultFile(t, path), path, false)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	// A wrong passphrase: the error line appears, the dialog stays open and
	// the vault stays locked.
	m = typeText(m, "wrongwrong")
	m = keyByName(m, "enter")
	if !m.vaultDlg.open {
		t.Fatal("a wrong passphrase must keep the dialog open")
	}
	if m.settings.Locked() != true || len(m.settings.Connections) != 0 {
		t.Fatalf("a wrong passphrase must leave the vault locked, locked=%v conns=%d",
			m.settings.Locked(), len(m.settings.Connections))
	}
	if !strings.Contains(stripANSI(m.View()), "Wrong passphrase or damaged file") {
		t.Fatalf("the error line is missing:\n%s", m.View())
	}

	// The right one: the dialog closes, the connections are back and the
	// tree shows them. The status message mentions the unlock, not the
	// passphrase.
	for range len([]rune("wrongwrong")) {
		m = keyByName(m, "backspace")
	}
	m = typeText(m, pass)
	m = keyByName(m, "enter")
	if m.vaultDlg.open {
		t.Fatal("the correct passphrase must close the dialog")
	}
	if m.settings.Locked() || len(m.settings.Connections) != 1 {
		t.Fatalf("unlock must restore the connections, got %d", len(m.settings.Connections))
	}
	rows := m.objectsRows()
	if len(rows) < 2 || rows[0].kind != rowConn || rows[1].kind != rowConn {
		t.Fatalf("the tree must list the connections again: %+v", rows)
	}
	if !strings.Contains(stripANSI(m.View()), "money (demo)") {
		t.Fatalf("the saved connection must appear in the tree:\n%s", m.View())
	}
	vaultStatusNeverQuotes(t, m, pass, "unlocked")
}

func TestVaultUnlockEscSkipsAndLockedTreeRow(t *testing.T) {
	dir := t.TempDir()
	pass := "unlockme123"
	path := vaultWriteEncrypted(t, dir, pass, "")
	m := vaultModel(vaultFile(t, path), path, false)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = keyByName(m, "esc")

	if m.vaultDlg.open {
		t.Fatal("esc must close the unlock dialog")
	}
	if !m.settings.Locked() {
		t.Fatal("skipping the unlock must keep the vault locked")
	}
	// The tree is one notice row, with no connection names anywhere.
	rows := m.objectsRows()
	if len(rows) != 1 || rows[0].kind != rowLocked {
		t.Fatalf("the locked tree must be the single notice row: %+v", rows)
	}
	// The tree pane is narrow, so the notice truncates on screen; the
	// affordable part is checked in the view and the Enter affordance on
	// the message itself.
	view := stripANSI(m.View())
	if strings.Contains(view, "money") {
		t.Fatalf("a locked tree must not show connection names:\n%s", view)
	}
	if !strings.Contains(view, "Connections are locked") {
		t.Fatalf("the locked notice is missing:\n%s", view)
	}
	if tree := m.t(i18n.KeyVaultLockedTree); !strings.Contains(tree, "Enter") {
		t.Fatalf("the locked notice must offer the Enter shortcut: %q", tree)
	}

	// Enter on the notice row opens the unlock dialog again.
	m.layout.focus = paneObjects
	m.objects.sel = 0
	m.objectsAccept()
	if !m.vaultDlg.open || m.vaultDlg.mode != vaultUnlock {
		t.Fatal("enter on the locked row must open the unlock dialog")
	}
	if !strings.Contains(stripANSI(m.View()), "Unlock connections") {
		t.Fatalf("the unlock dialog must be visible:\n%s", m.View())
	}
}

func TestVaultLockedBlocksConnectionActions(t *testing.T) {
	dir := t.TempDir()
	pass := "unlockme123"
	path := vaultWriteEncrypted(t, dir, pass, "")
	m := vaultModel(vaultFile(t, path), path, false)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = keyByName(m, "esc")

	// A context-menu action that would open a connection (or the form for
	// one) is intercepted with a status message instead. The message is the
	// invariant: no connection form, no new in-memory-only connection.
	for _, action := range []objectsMenuAction{objActNewConn, objActConnect, objActEdit, objActDisconnect} {
		got := m
		got.runObjectsMenuAction(action)
		if got.connDlg.open {
			t.Fatalf("action %v opened the connection form while locked", action)
		}
		if !strings.Contains(got.status.message, "Unlock the connections first") {
			t.Fatalf("action %v status = %q, want the unlock-first message", action, got.status.message)
		}
		if got.vaultDlg.open {
			t.Fatalf("action %v must not open the vault dialog", action)
		}
	}
}

func TestVaultFirstRunOpensSetup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json") // the file does not exist
	m := vaultModel(settings.Default(), path, true)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	if !m.vaultDlg.open || m.vaultDlg.mode != vaultSetup {
		t.Fatalf("the first run must open the setup dialog, mode = %v", m.vaultDlg.mode)
	}
	// A first run has no file, hence no vault: the unlock dialog must not
	// have preempted the setup one.
	if m.settings.Encrypted() {
		t.Fatal("first run must not start encrypted")
	}
}

// TestVaultUnlockDialogStaysCompact pins the blank-wall regression: the
// unlock form is one field (plus the stored hint line when there is one), so
// the full 12-row minimum padded it with five empty rows on a real terminal.
// The compact floor of eight (ui/modal.go) keeps the padding at two rows.
func TestVaultUnlockDialogStaysCompact(t *testing.T) {
	dir := t.TempDir()
	pass := "unlockme123"
	// Without a stored hint: only the passphrase field.
	path := vaultWriteEncrypted(t, dir, pass, "")
	m := vaultModel(vaultFile(t, path), path, false)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if !m.vaultDlg.open || m.vaultDlg.mode != vaultUnlock {
		t.Fatalf("startup must open the unlock dialog, mode = %v", m.vaultDlg.mode)
	}
	l := m.vaultLayout()
	if blanks := l.h - (len(l.rows) + 6); blanks > 2 {
		t.Fatalf("unlock box = %d rows for %d content rows, want at most 2 blank", l.h, len(l.rows))
	}
	if l.h < modalMinHCompact {
		t.Fatalf("unlock box = %d rows, want at least the compact floor %d", l.h, modalMinHCompact)
	}
	// The frame must still hold the whole form: every row is on screen.
	if l.drawn < len(l.rows) {
		t.Fatalf("unlock box drew %d of %d rows, want all of them", l.drawn, len(l.rows))
	}

	// With a stored hint the one-line note joins the rows; the box grows to
	// the content instead of the minimum.
	path = vaultWriteEncrypted(t, dir, pass, "the note")
	m = vaultModel(vaultFile(t, path), path, false)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	l = m.vaultLayout()
	if blanks := l.h - (len(l.rows) + 6); blanks > 2 {
		t.Fatalf("unlock box with hint = %d rows for %d content rows, want at most 2 blank", l.h, len(l.rows))
	}
}

// TestVaultOtherShapesKeepTheFullMinimum pins that compactness is for the
// one-field confirmations only: the setup, change, remove, hint and manager
// shapes keep the shared 12-row floor.
func TestVaultOtherShapesKeepTheFullMinimum(t *testing.T) {
	for _, mode := range []vaultMode{vaultSetup, vaultChange, vaultRemove, vaultHint, vaultManage} {
		m := vaultModel(settings.Default(), filepath.Join(t.TempDir(), "settings.json"), false)
		m = send(m, tea.WindowSizeMsg{Width: 100, Height: 40})
		m.openVaultMode(mode)
		if l := m.vaultLayout(); l.h < modalMinH {
			t.Fatalf("mode %v box = %d rows, want at least %d", mode, l.h, modalMinH)
		}
	}
}

func TestVaultSetupAlwaysShowsTheNoRecoveryNotice(t *testing.T) {
	// English: the line is on the fresh form (no typing, no error) at 80x24,
	// the small screen the warning has to survive.
	path := filepath.Join(t.TempDir(), "settings.json")
	m := vaultModel(settings.Default(), path, true)
	m = send(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if !m.vaultDlg.open || m.vaultDlg.mode != vaultSetup {
		t.Fatalf("a first run must open the setup dialog, mode = %v", m.vaultDlg.mode)
	}
	if view := stripANSI(m.View()); !strings.Contains(view, "no recovery") {
		t.Fatalf("the setup form must name the no-recovery consequence:\n%s", view)
	}

	// Chinese: the same line, localized, still from the first paint.
	m2 := vaultModel(settings.Default(), filepath.Join(t.TempDir(), "settings.json"), true)
	m2 = send(m2, tea.WindowSizeMsg{Width: 80, Height: 24})
	m2.translator = i18n.New(i18n.ZhHans)
	m2.openVaultMode(vaultSetup)
	if view := stripANSI(m2.View()); !strings.Contains(view, "无法解密") {
		t.Fatalf("the zh setup form must name the no-recovery consequence:\n%s", view)
	}
}

func TestVaultSetupValidationAndSave(t *testing.T) {
	pass := "abcdefgh12" // 10 runes: accepted, but reported as weak
	path := filepath.Join(t.TempDir(), "settings.json")
	s := settings.Default()
	s.Connections = []settings.Connection{vaultTestConn()}
	m := vaultModel(s, path, true)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	// Too short: refused, the dialog stays open.
	m = typeText(m, "SHORT")
	m = keyByName(m, "enter")
	if !m.vaultDlg.open || m.vaultDlg.err != "The passphrase needs at least 8 characters" {
		t.Fatalf("an 8-char-minimum violation must be refused, err = %q", m.vaultDlg.err)
	}

	// Reopen for each of the other refusals, so the field state is fresh.
	m = keyByName(m, "esc")
	m.openVaultEntry() // still unencrypted: this is the setup dialog again
	m = vaultType(m, "abcdefgh")
	m = vaultType(m, "different") // confirm does not match
	m = keyByName(m, "enter")
	if !m.vaultDlg.open || m.vaultDlg.err != "The passphrases do not match" {
		t.Fatalf("a mismatched confirm must be refused, err = %q", m.vaultDlg.err)
	}

	m = keyByName(m, "esc")
	m.openVaultEntry()
	m = vaultType(m, pass) // passphrase
	m = vaultType(m, pass) // confirm
	m = vaultType(m, pass) // hint equal to the passphrase: refused
	m = keyByName(m, "enter")
	if !m.vaultDlg.open || m.vaultDlg.err != "The hint must not be the passphrase" {
		t.Fatalf("a hint equal to the passphrase must be refused, err = %q", m.vaultDlg.err)
	}

	// The good run: the hint is different, the vault appears in the file and
	// the plaintext connections disappear from it.
	m = keyByName(m, "esc")
	m.openVaultEntry()
	m = vaultType(m, pass)
	m = vaultType(m, pass)
	m = vaultType(m, "the one from the office notes")
	m = keyByName(m, "enter")
	if m.vaultDlg.open {
		t.Fatal("a valid setup must close the dialog")
	}
	if !m.settings.Encrypted() {
		t.Fatal("the settings must be encrypted now")
	}
	// A passphrase shorter than 12 is accepted but warned about.
	vaultStatusNeverQuotes(t, m, pass, "Passphrase set")
	if !strings.Contains(m.status.message, "short") {
		t.Fatalf("status = %q, want the weak-passphrase warning", m.status.message)
	}

	vaultSave(t, m, path)
	if got := vaultFile(t, path); !got.Encrypted() {
		t.Fatal("the saved file must carry the vault")
	}
	raw := vaultReadFile(t, path)
	if strings.Contains(raw, vaultTestHost) || strings.Contains(raw, vaultTestPassword) {
		t.Fatalf("the file must not contain the connection secrets:\n%s", raw)
	}
	if !strings.Contains(raw, "the one from the office notes") {
		t.Fatalf("the hint is stored in the clear by design:\n%s", raw)
	}
}

func TestVaultChangePassphrase(t *testing.T) {
	dir := t.TempDir()
	oldPass := "oldpass12345"
	newPass := "newpass67890"
	path := vaultWriteEncrypted(t, dir, oldPass, "")
	m := vaultModel(vaultFile(t, path), path, false)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = unlockThroughDialog(t, m, oldPass)

	// The Settings menu entry routes an unlocked vault to the manager; its
	// first entry is "Change passphrase".
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s"), Alt: true})
	if m.menu.open != 5 {
		t.Fatalf("menu.open = %d, want the Settings menu", m.menu.open)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if !m.vaultDlg.open || m.vaultDlg.mode != vaultManage {
		t.Fatalf("a passphrase but no lock must open the manager, mode = %v", m.vaultDlg.mode)
	}
	m = keyByName(m, "enter")
	if m.vaultDlg.mode != vaultChange {
		t.Fatalf("the manager's first entry must be change, mode = %v", m.vaultDlg.mode)
	}

	// The wrong current passphrase: the error appears and the dialog stays
	// open; the file is untouched.
	m = vaultType(m, "not-the-passphrase")
	m = vaultType(m, newPass)
	m = vaultType(m, newPass)
	m = vaultType(m, "hint")
	m = keyByName(m, "enter")
	if !m.vaultDlg.open || m.vaultDlg.err != "Wrong passphrase or damaged file" {
		t.Fatalf("a wrong current passphrase must be refused, err = %q", m.vaultDlg.err)
	}
	if file := vaultFile(t, path); file.Vault == nil || file.Vault.KDF.Iterations == 0 {
		t.Fatal("the envelope must still be there")
	}
	if file := vaultFile(t, path); file.Unlock(oldPass) != nil {
		t.Fatalf("the old passphrase must still open the file")
	}

	// Esc closes the manage-derived change dialog; reopen through the
	// manager with the current passphrase filled correctly this time.
	m = keyByName(m, "esc")
	if m.vaultDlg.open {
		t.Fatal("esc must close the change dialog")
	}
	m.openVaultEntry()
	m = keyByName(m, "enter") // the manager opens, first entry = change
	m = vaultType(m, oldPass)
	m = vaultType(m, newPass)
	m = vaultType(m, newPass)
	m = vaultType(m, "")
	m = keyByName(m, "enter")
	if m.vaultDlg.open {
		t.Fatalf("a correct change must close the dialog, err = %q", m.vaultDlg.err)
	}
	vaultStatusNeverQuotes(t, m, oldPass, "changed")
	vaultStatusNeverQuotes(t, m, newPass, "changed")

	vaultSave(t, m, path)
	got := vaultFile(t, path)
	if err := got.Unlock(newPass); err != nil {
		t.Fatalf("the new passphrase must open the file: %v", err)
	}
	if err := (&got).Unlock(oldPass); err == nil {
		t.Fatal("the old passphrase must stop opening the file")
	}
	if len(got.Connections) != 1 || got.Connections[0].Name != "money" {
		t.Fatalf("the connections survive the change: %+v", got.Connections)
	}
}

func TestVaultRemoveEncryption(t *testing.T) {
	dir := t.TempDir()
	pass := "remove-me-1"
	path := vaultWriteEncrypted(t, dir, pass, "")
	m := vaultModel(vaultFile(t, path), path, false)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = unlockThroughDialog(t, m, pass)
	m.openVaultEntry() // the manager
	m = keyByName(m, "down")
	m = keyByName(m, "down") // third entry: remove encryption
	m = keyByName(m, "enter")
	if m.vaultDlg.mode != vaultRemove {
		t.Fatalf("the third entry must be remove, mode = %v", m.vaultDlg.mode)
	}

	// A wrong passphrase is refused and the vault stays.
	m = typeText(m, "not-the-passphrase")
	m = keyByName(m, "enter")
	if !m.vaultDlg.open || m.vaultDlg.err != "Wrong passphrase or damaged file" {
		t.Fatalf("a wrong passphrase must be refused, err = %q", m.vaultDlg.err)
	}
	if !m.settings.Encrypted() {
		t.Fatal("the removal must not have happened")
	}

	m = keyByName(m, "esc")
	m.openVaultEntry()
	m = keyByName(m, "down")
	m = keyByName(m, "down")
	m = keyByName(m, "enter")
	m = typeText(m, pass)
	m = keyByName(m, "enter")
	if m.vaultDlg.open {
		t.Fatalf("a correct removal must close the dialog, err = %q", m.vaultDlg.err)
	}
	if m.settings.Encrypted() {
		t.Fatal("the encryption must be gone")
	}
	vaultStatusNeverQuotes(t, m, pass, "Encryption removed")

	vaultSave(t, m, path)
	got := vaultFile(t, path)
	if got.Encrypted() {
		t.Fatal("the saved file must be plain again")
	}
	if len(got.Connections) != 1 || got.Connections[0].Password != vaultTestPassword {
		t.Fatalf("the connections must be restored in the clear: %+v", got.Connections)
	}
	if raw := vaultReadFile(t, path); !strings.Contains(raw, vaultTestHost) {
		t.Fatalf("the file must carry the connections in plain text:\n%s", raw)
	}
}

func TestVaultLockNow(t *testing.T) {
	dir := t.TempDir()
	pass := "lock-me-12"
	path := vaultWriteEncrypted(t, dir, pass, "")
	m := vaultModel(vaultFile(t, path), path, false)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = unlockThroughDialog(t, m, pass)
	if m.settings.Locked() {
		t.Fatal("the vault must be unlocked before locking it again")
	}

	// "Lock connections now" arrives through the Settings menu, which is the
	// only place that advertises it.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s"), Alt: true})
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if !m.settings.Locked() {
		t.Fatal("lock now must lock the vault")
	}
	if len(m.settings.Connections) != 0 {
		t.Fatalf("locking must drop the decrypted connections, got %d", len(m.settings.Connections))
	}
	rows := m.objectsRows()
	if len(rows) != 1 || rows[0].kind != rowLocked {
		t.Fatalf("the locked tree must show the notice row: %+v", rows)
	}
	if !strings.Contains(m.status.message, "Connections are locked") {
		t.Fatalf("status = %q", m.status.message)
	}

	// Locking is memory-only: a fresh load of the file still unlocks and
	// brings the connections back.
	file := vaultFile(t, path)
	if err := (&file).Unlock(pass); err != nil {
		t.Fatalf("the file must still unlock: %v", err)
	}
	if len(file.Connections) != 1 || file.Connections[0].Name != "money" {
		t.Fatalf("the connections must come back: %+v", file.Connections)
	}
}

func TestVaultMenuRoutesByState(t *testing.T) {
	// Locked: the entry opens the unlock dialog.
	dir := t.TempDir()
	pass := "route123456"
	path := vaultWriteEncrypted(t, dir, pass, "")
	m := vaultModel(vaultFile(t, path), path, false)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = keyByName(m, "esc")
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s"), Alt: true})
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if !m.vaultDlg.open || m.vaultDlg.mode != vaultUnlock {
		t.Fatalf("locked state must route to unlock, mode = %v", m.vaultDlg.mode)
	}

	// Plain file: the entry opens setup.
	plain := filepath.Join(dir, "plain.json")
	m2 := vaultModel(settings.Default(), plain, false)
	m2 = send(m2, tea.WindowSizeMsg{Width: 100, Height: 30})
	m2.openVaultEntry()
	if !m2.vaultDlg.open || m2.vaultDlg.mode != vaultSetup {
		t.Fatalf("plain state must route to setup, mode = %v", m2.vaultDlg.mode)
	}

	// Unlocked: the entry opens the manager.
	m3 := unlockThroughDialog(t, m, pass)
	m3.openVaultEntry()
	if !m3.vaultDlg.open || m3.vaultDlg.mode != vaultManage {
		t.Fatalf("unlocked state must route to the manager, mode = %v", m3.vaultDlg.mode)
	}
}

// TestVaultLockNowMenuVisibility pins the needsUnlock suffix rule: hidden
// items are always the tail of their menu, so the visible indices keep
// matching def.items (what the mouse dispatch reads).
func TestVaultLockNowMenuVisibility(t *testing.T) {
	def := menuDefs[5]
	if last := def.items[len(def.items)-1]; last.action != actVaultLockNow {
		t.Fatalf("the lock item must stay last in its menu, found %v", last.action)
	}

	lockedDef := menuDefs[5]
	m := vaultModel(settings.Default(), "", false) // no vault: nothing to lock
	m.settings.Vault = nil
	items := m.menuItems(lockedDef)
	for _, it := range items {
		if it.action == actVaultLockNow {
			t.Fatal("the lock item must hide while the vault is not unlocked")
		}
	}

	// Unlocked: the item appears after the others.
	dir := t.TempDir()
	path := vaultWriteEncrypted(t, dir, "visible123", "")
	m2 := vaultModel(vaultFile(t, path), path, false)
	m2 = send(m2, tea.WindowSizeMsg{Width: 100, Height: 30})
	m2 = unlockThroughDialog(t, m2, "visible123")
	items = m2.menuItems(lockedDef)
	var found bool
	for i, it := range items {
		if it.action == actVaultLockNow {
			if i == 0 {
				t.Fatal("the lock item must not be the first entry")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("the lock item must show while the vault is unlocked")
	}
}

func TestVaultSettingsDialogRow(t *testing.T) {
	// Unencrypted: the row opens setup and the description says so.
	plain := filepath.Join(t.TempDir(), "plain.json")
	m := vaultModel(settings.Default(), plain, false)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = keyByName(m, "f2")
	m = selectSettingRow(t, m, i18n.KeyVaultMenu)
	if got := m.settingValue(m.dlg.sel); got != actionGlyph {
		t.Fatalf("the vault row value = %q, want the action glyph", got)
	}
	if !strings.Contains(m.vaultRowDesc(), "plain text") {
		t.Fatalf("unencrypted description = %q", m.vaultRowDesc())
	}
	m = keyByName(m, "enter")
	if m.dlg.open {
		t.Fatal("activating the vault row must close the settings dialog")
	}
	if !m.vaultDlg.open || m.vaultDlg.mode != vaultSetup {
		t.Fatalf("the vault row must open setup, mode = %v", m.vaultDlg.mode)
	}

	// Locked: the description follows the state and the row opens unlock.
	dir := t.TempDir()
	path := vaultWriteEncrypted(t, dir, "rowtest123", "")
	m2 := vaultModel(vaultFile(t, path), path, false)
	m2 = send(m2, tea.WindowSizeMsg{Width: 100, Height: 30})
	m2 = keyByName(m2, "esc") // skip the startup unlock
	m2 = keyByName(m2, "f2")
	m2 = selectSettingRow(t, m2, i18n.KeyVaultMenu)
	if !strings.Contains(m2.vaultRowDesc(), "locked") {
		t.Fatalf("locked description = %q", m2.vaultRowDesc())
	}
	m2 = keyByName(m2, "enter")
	if !m2.vaultDlg.open || m2.vaultDlg.mode != vaultUnlock {
		t.Fatalf("the locked state must route to unlock, mode = %v", m2.vaultDlg.mode)
	}

	// Unlocked: the description says unlocked, the row opens the manager.
	m3 := unlockThroughDialog(t, m2, "rowtest123")
	m3 = keyByName(m3, "f2")
	m3 = selectSettingRow(t, m3, i18n.KeyVaultMenu)
	if !strings.Contains(m3.vaultRowDesc(), "unlocked") {
		t.Fatalf("unlocked description = %q", m3.vaultRowDesc())
	}
	m3 = keyByName(m3, "enter")
	if !m3.vaultDlg.open || m3.vaultDlg.mode != vaultManage {
		t.Fatalf("the unlocked state must route to the manager, mode = %v", m3.vaultDlg.mode)
	}
}

func TestVaultManageChangeHint(t *testing.T) {
	dir := t.TempDir()
	pass := "hinttest123"
	path := vaultWriteEncrypted(t, dir, pass, "the office note")
	m := vaultModel(vaultFile(t, path), path, false)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = unlockThroughDialog(t, m, pass)
	m.openVaultEntry()

	m = keyByName(m, "down") // second entry: change hint
	m = keyByName(m, "enter")
	if m.vaultDlg.mode != vaultHint {
		t.Fatalf("the second entry must be change hint, mode = %v", m.vaultDlg.mode)
	}
	// The current hint starts in the field, so it is edited, not retyped.
	if m.vaultDlg.fields[0].value != "the office note" {
		t.Fatalf("hint field = %q, want the current hint", m.vaultDlg.fields[0].value)
	}
	m = keyByName(m, "enter")
	if m.vaultDlg.open {
		t.Fatalf("a hint update must close the dialog, err = %q", m.vaultDlg.err)
	}
	vaultSave(t, m, path)
	if got := vaultFile(t, path); got.Vault.Hint != "the office note" {
		t.Fatalf("hint = %q, want the unchanged hint (plain Enter keeps it)", got.Vault.Hint)
	}

	// Now actually change it.
	m.openVaultEntry()
	m = keyByName(m, "down")
	m = keyByName(m, "enter")
	m = typeText(m, "X")
	m = keyByName(m, "enter")
	if m.vaultDlg.open {
		t.Fatalf("the hint update must succeed, err = %q", m.vaultDlg.err)
	}
	vaultSave(t, m, path)
	if got := vaultFile(t, path); got.Vault.Hint != "the office noteX" {
		t.Fatalf("hint = %q, want the edited one", got.Vault.Hint)
	}
}

func TestVaultDialogFieldsClearOnClose(t *testing.T) {
	pass := "clearme123"
	path := filepath.Join(t.TempDir(), "plain.json")
	m := vaultModel(settings.Default(), path, false)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.openVaultEntry() // plain file → the setup dialog
	m = typeText(m, pass)
	m = keyByName(m, "esc")
	if m.vaultDlg.open {
		t.Fatal("esc must close the setup dialog")
	}
	// Reopening produces an empty, masked form again: whatever was typed
	// before the close must not leak into it.
	m.openVaultEntry()
	for i, f := range m.vaultDlg.fields {
		if f.value != "" || f.caret != 0 {
			t.Fatalf("field %d must start empty, got %q (caret %d)", i, f.value, f.caret)
		}
	}
	view := stripANSI(m.View())
	if strings.Contains(view, pass) {
		t.Fatal("the dialog must never show a typed passphrase")
	}
}

// vaultLegacyEnvelope seals plaintext the way an older build with a lower
// default round count would have: PBKDF2-HMAC-SHA256 at vault.MinIterations
// and AES-256-GCM, the format documented in docs/secrets-encryption.md. The
// construction is written out here instead of delegating to the vault
// package on purpose: the package lowers its round count only inside its own
// tests, and a legacy file must come from an older build, not the current
// one. The algorithm and AAD strings are the vault format's own; if the
// format changes, this test breaks with it, which is the point.
func vaultLegacyEnvelope(t *testing.T, pass string, plaintext []byte) *vault.Envelope {
	t.Helper()
	salt := make([]byte, 16)
	key, err := pbkdf2.Key(sha256.New, pass, salt, vault.MinIterations, vault.KeySize)
	if err != nil {
		t.Fatalf("derive the legacy key: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	nonce := make([]byte, 12)
	return &vault.Envelope{
		Version:    vault.Version,
		KDF:        vault.KDF{Algo: "pbkdf2-sha256", Iterations: vault.MinIterations, Salt: salt},
		Cipher:     vault.Cipher{Algo: "aes-256-gcm", Nonce: nonce},
		Ciphertext: gcm.Seal(nil, nonce, plaintext, []byte("tdb-vault-v1")),
	}
}

func TestVaultUnlockUpgradesWeakParameters(t *testing.T) {
	dir := t.TempDir()
	pass := "upgrade-123"
	path := filepath.Join(dir, "settings.json")

	// The connections are sealed inside a legacy envelope: an older build's
	// envelope with an iteration count below the current default.
	s := settings.Default()
	s.Connections = []settings.Connection{vaultTestConn()}
	plaintext, err := json.Marshal(s.Connections)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s.Connections = nil // locked from here on: the list lives in the envelope
	s.Vault = vaultLegacyEnvelope(t, pass, plaintext)
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	m := vaultModel(vaultFile(t, path), path, false)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = unlockThroughDialog(t, m, pass)
	if !strings.Contains(m.status.message, "Encryption parameters upgraded") {
		t.Fatalf("status = %q, want the upgrade note", m.status.message)
	}
	vaultSave(t, m, path)
	got := vaultFile(t, path)
	if got.Vault.KDF.Iterations != vault.DefaultIterations {
		t.Fatalf("iterations = %d, want the current default on disk", got.Vault.KDF.Iterations)
	}
	if err := (&got).Unlock(pass); err != nil {
		t.Fatalf("the upgraded file must still open: %v", err)
	}
	if len(got.Connections) != 1 {
		t.Fatalf("the connections survive the upgrade: %+v", got.Connections)
	}
}

func TestVaultDialogStaysInsideSmallScreens(t *testing.T) {
	// Every dialog shape, each on a fresh model, must stay inside the area at
	// 80x24: the hint/error line (the bottom border's neighbour) stays
	// visible and the canvas keeps its full height.
	for _, mode := range []vaultMode{vaultSetup, vaultUnlock, vaultChange, vaultRemove, vaultHint, vaultManage} {
		path := filepath.Join(t.TempDir(), "settings.json")
		m := vaultModel(settings.Default(), path, false)
		m = send(m, tea.WindowSizeMsg{Width: 80, Height: 24})
		m.openVaultMode(mode) // the dialog replaces every overlay
		block := m.View()
		if !strings.Contains(stripANSI(block), "Esc") {
			t.Fatalf("mode %v: the hint line must stay visible on a small screen:\n%s", mode, block)
		}
		lines := strings.Split(stripANSI(block), "\n")
		if len(lines) != 24 {
			t.Fatalf("mode %v: the view must stay 24 rows, got %d", mode, len(lines))
		}
	}
}

// TestPersistSettingsMarshalsBeforeTheBackgroundWrite pins the race-free
// split of persistSettings: the marshal (which re-seals the vault, reading
// the session key) runs on the calling — UI — goroutine, and the background
// command only writes the finished bytes. Before the split the command
// marshalled in the background, so a Lock — which zeroes the key in place —
// between creating the command and running it made the reseal fail and the
// save error out.
func TestPersistSettingsMarshalsBeforeTheBackgroundWrite(t *testing.T) {
	dir := t.TempDir()
	pass := "persist-race-12"
	path := filepath.Join(dir, "settings.json")
	s := settings.Default()
	s.Connections = []settings.Connection{vaultTestConn()}
	if err := s.SetPassphrase(pass, ""); err != nil {
		t.Fatalf("SetPassphrase: %v", err)
	}
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	m := vaultModel(s, path, false)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.saveConnection(settings.Connection{Name: "second", Driver: "demo"})

	cmd := m.persistSettings()
	if cmd == nil {
		t.Fatal("persistSettings must return the background write")
	}
	// The key leaves memory before the background command runs: with the
	// marshal in the background, the reseal would hit the zeroed key here.
	m.settings.Lock()
	msg := cmd()
	if sm, ok := msg.(statusMessageMsg); ok {
		t.Fatalf("the background write must not depend on the live settings: %v", sm)
	}
	got := vaultFile(t, path)
	if !got.Encrypted() {
		t.Fatal("the file on disk must still carry the vault")
	}
	if err := got.Unlock(pass); err != nil {
		t.Fatalf("the written file must unlock: %v", err)
	}
	if len(got.Connections) != 2 {
		t.Fatalf("connections = %+v, want both saved ones", got.Connections)
	}
}

// TestVaultUnlockReportsUnsupportedEnvelope pins the error triage in the
// dialog: only vault.ErrWrongPassphrase earns the "wrong passphrase" line.
// An envelope this build cannot open (here: version 99) must name its own
// problem, or the user keeps retyping a passphrase that was never wrong.
func TestVaultUnlockReportsUnsupportedEnvelope(t *testing.T) {
	dir := t.TempDir()
	pass := "versioned-123"
	path := vaultWriteEncrypted(t, dir, pass, "")

	// Rewrite the stored envelope to a version no build here can read.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(doc["vault"], &env); err != nil {
		t.Fatalf("parse the vault: %v", err)
	}
	env["version"] = json.RawMessage("99")
	sealed, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal the vault: %v", err)
	}
	doc["vault"] = sealed
	rewritten, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal the settings: %v", err)
	}
	if err := os.WriteFile(path, rewritten, 0o600); err != nil {
		t.Fatal(err)
	}

	m := vaultModel(vaultFile(t, path), path, false)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if !m.vaultDlg.open || m.vaultDlg.mode != vaultUnlock {
		t.Fatalf("startup must open the unlock dialog, mode = %v", m.vaultDlg.mode)
	}
	m = typeText(m, pass)
	m = keyByName(m, "enter")
	if !m.vaultDlg.open {
		t.Fatal("an unsupported envelope must keep the dialog open")
	}
	if m.vaultDlg.err == m.t(i18n.KeyVaultWrong) {
		t.Fatalf("version 99 must not be reported as a wrong passphrase, err = %q", m.vaultDlg.err)
	}
	if !strings.Contains(m.vaultDlg.err, "unsupported envelope version") {
		t.Fatalf("the error must name the real problem, err = %q", m.vaultDlg.err)
	}
	if strings.Contains(m.vaultDlg.err, pass) {
		t.Fatalf("the error must never contain the passphrase: %q", m.vaultDlg.err)
	}
}

// TestVaultHintRefusesThePassphrase pins the audit fix: the change-hint form
// refuses a hint that opens the vault. There is no passphrase field to
// compare against, so the check is a trial open of the envelope — and a
// successful trial must leave the session exactly as it was: unlocked stays
// unlocked, and nothing reaches the file.
func TestVaultHintRefusesThePassphrase(t *testing.T) {
	dir := t.TempDir()
	pass := "hintguard12"
	path := vaultWriteEncrypted(t, dir, pass, "the office note")
	m := vaultModel(vaultFile(t, path), path, false)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = unlockThroughDialog(t, m, pass)

	m.openVaultEntry() // the manager (unlocked)
	m = keyByName(m, "down")
	m = keyByName(m, "enter")
	if m.vaultDlg.mode != vaultHint {
		t.Fatalf("the second entry must be change hint, mode = %v", m.vaultDlg.mode)
	}

	// Replace the pre-filled hint with the passphrase itself: refused.
	for range len([]rune("the office note")) {
		m = keyByName(m, "backspace")
	}
	m = typeText(m, pass)
	m = keyByName(m, "enter")
	if !m.vaultDlg.open || m.vaultDlg.err != "The hint must not be the passphrase" {
		t.Fatalf("a hint equal to the passphrase must be refused, err = %q", m.vaultDlg.err)
	}
	// The trial opened the envelope but must not have changed the session:
	// still unlocked, the connections still in memory, no stray status text.
	if m.settings.Locked() {
		t.Fatal("a successful trial must not lock an unlocked vault")
	}
	if len(m.settings.Connections) != 1 {
		t.Fatalf("the connections must stay in memory, got %d", len(m.settings.Connections))
	}
	vaultStatusNeverQuotes(t, m, pass, "")

	// And a refused submit changes nothing on disk.
	vaultSave(t, m, path)
	if got := vaultFile(t, path); got.Vault.Hint != "the office note" {
		t.Fatalf("hint = %q, want the refused submit to change nothing", got.Vault.Hint)
	}
}

// TestVaultHintTrialLeavesALockedVaultLocked is the other direction: even
// with the vault locked, a hint equal to the passphrase is caught by the
// trial and refused with the same line — the trial must not unlock the
// session on the way, and SetHint never runs.
func TestVaultHintTrialLeavesALockedVaultLocked(t *testing.T) {
	dir := t.TempDir()
	pass := "hintguard12"
	path := vaultWriteEncrypted(t, dir, pass, "the office note")
	m := vaultModel(vaultFile(t, path), path, false)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = keyByName(m, "esc") // skip the unlock: stay locked

	// The hint form while locked is only reachable through stale state in
	// production; the guard must hold there too.
	m.openVaultMode(vaultHint)
	for range len([]rune("the office note")) {
		m = keyByName(m, "backspace")
	}
	m = typeText(m, pass)
	m = keyByName(m, "enter")
	if !m.vaultDlg.open || m.vaultDlg.err != "The hint must not be the passphrase" {
		t.Fatalf("a hint equal to the passphrase must be refused, err = %q", m.vaultDlg.err)
	}
	if !m.settings.Locked() {
		t.Fatal("a successful trial must not unlock the session")
	}
	if len(m.settings.Connections) != 0 {
		t.Fatalf("a locked vault must keep no connections in memory, got %d", len(m.settings.Connections))
	}

	// A hint that is not the passphrase passes the trial but SetHint still
	// refuses while locked — with the session untouched either way.
	for range len([]rune(pass)) {
		m = keyByName(m, "backspace")
	}
	m = typeText(m, "not-the-passphrase")
	m = keyByName(m, "enter")
	if !m.vaultDlg.open || !strings.Contains(m.vaultDlg.err, "cannot set the hint while locked") {
		t.Fatalf("SetHint must refuse while locked, err = %q", m.vaultDlg.err)
	}
	if !m.settings.Locked() {
		t.Fatal("the hint form must leave a locked vault locked")
	}
	vaultStatusNeverQuotes(t, m, pass, "")
}

// TestVaultHintShowsTheWarningNotice pins the static line: the change-hint
// form carries the same plain-text warning as the setup and change forms —
// a hint equal to the passphrase can now be caught, but the rule still has
// to be stated where the value is typed.
func TestVaultHintShowsTheWarningNotice(t *testing.T) {
	dir := t.TempDir()
	pass := "hintguard12"
	path := vaultWriteEncrypted(t, dir, pass, "")
	m := vaultModel(vaultFile(t, path), path, false)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = unlockThroughDialog(t, m, pass)
	m.openVaultEntry()
	m = keyByName(m, "down")
	m = keyByName(m, "enter")
	if m.vaultDlg.mode != vaultHint {
		t.Fatalf("the second entry must be change hint, mode = %v", m.vaultDlg.mode)
	}
	if view := stripANSI(m.View()); !strings.Contains(view, "never write the passphrase itself") {
		t.Fatalf("the hint form must carry the plain-text warning:\n%s", view)
	}
}

// TestVaultLockedGuardsTheConnectionEntryPoints pins the wiring of
// vaultBlocked: every function that forms a connection refuses it while the
// vault is locked, whatever reached it, and each call goes through again
// once the vault is unlocked.
func TestVaultLockedGuardsTheConnectionEntryPoints(t *testing.T) {
	dir := t.TempDir()
	pass := "guardme1234"
	path := vaultWriteEncrypted(t, dir, pass, "")
	m := vaultModel(vaultFile(t, path), path, false)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = keyByName(m, "esc") // skip the unlock: stay locked

	const want = "Unlock the connections first"
	conn := settings.Connection{Name: "money", Driver: "demo", Host: vaultTestHost}

	// connectTo: refused before anything is saved or opened.
	collectCmds(m.connectTo(conn))
	if !strings.Contains(m.status.message, want) {
		t.Fatalf("connectTo status = %q, want the unlock-first message", m.status.message)
	}
	if len(m.settings.Connections) != 0 {
		t.Fatalf("a locked connectTo must save nothing, got %+v", m.settings.Connections)
	}

	// testConnection: refused before any driver work.
	collectCmds(m.testConnection(conn, false))
	if !strings.Contains(m.status.message, want) {
		t.Fatalf("testConnection status = %q, want the unlock-first message", m.status.message)
	}

	// deleteConnection: refused without touching the list.
	collectCmds(m.deleteConnection("money"))
	if !strings.Contains(m.status.message, want) {
		t.Fatalf("deleteConnection status = %q, want the unlock-first message", m.status.message)
	}

	// submitConnDialog: refused; no connection is formed and the message
	// says why.
	m.openConnDialog()
	m = typeText(m, "locked-form")
	next, cmd, ok := m.submitConnDialog()
	if !ok {
		t.Fatal("submitConnDialog always consumes the key")
	}
	collectCmds(cmd)
	if !strings.Contains(next.status.message, want) {
		t.Fatalf("submitConnDialog status = %q, want the unlock-first message", next.status.message)
	}
	if len(next.settings.Connections) != 0 {
		t.Fatalf("a locked submit must save nothing, got %+v", next.settings.Connections)
	}
	m = next

	// Unlocked: the same four calls go through (regression). The commands
	// are left unexecuted where they would dial out; the synchronous half
	// — the guard's absence — is what is under test here.
	m.openVaultUnlock()
	m = unlockThroughDialog(t, m, pass)
	if m.settings.Locked() {
		t.Fatal("the vault must be unlocked for the regression half")
	}

	m.connectTo(conn)
	if strings.Contains(m.status.message, want) {
		t.Fatalf("an unlocked connectTo must go through, status = %q", m.status.message)
	}
	if len(m.settings.Connections) != 1 || m.settings.Connections[0].Name != "money" {
		t.Fatalf("the connection must be saved, got %+v", m.settings.Connections)
	}

	m.testConnection(conn, false)
	if strings.Contains(m.status.message, want) {
		t.Fatalf("an unlocked testConnection must go through, status = %q", m.status.message)
	}

	collectCmds(m.deleteConnection("money"))
	if strings.Contains(m.status.message, want) || len(m.settings.Connections) != 0 {
		t.Fatalf("an unlocked deleteConnection must go through, status = %q conns = %+v",
			m.status.message, m.settings.Connections)
	}

	m.openConnDialog()
	m = typeText(m, "unlocked-form")
	next, cmd, ok = m.submitConnDialog()
	if !ok {
		t.Fatal("submitConnDialog always consumes the key")
	}
	collectCmds(cmd)
	if strings.Contains(next.status.message, want) {
		t.Fatalf("an unlocked submit must go through, status = %q", next.status.message)
	}
	if next.connDlg.open {
		t.Fatal("a successful submit must close the connection form")
	}
	if len(next.settings.Connections) != 1 || next.settings.Connections[0].Name != "unlocked-form" {
		t.Fatalf("the submitted connection must be saved, got %+v", next.settings.Connections)
	}
}

// TestVaultManageHoverHighlightsWithoutRunning pins the pointer semantics of
// the manager list (round 7 §1): motion highlights the entry under the
// pointer without moving manageSel and without running anything, a motion
// outside the box is swallowed by the modal, and only a press activates the
// entry.
func TestVaultManageHoverHighlightsWithoutRunning(t *testing.T) {
	// Lip Gloss emits no ANSI without a TTY: force colors so the highlight
	// style is really in the rendered row, then restore the plain profile.
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	m := sized("")
	m.openVaultMode(vaultManage)
	if !m.vaultDlg.open || m.vaultDlg.mode != vaultManage {
		t.Fatalf("open = %v mode = %v, want the manager list", m.vaultDlg.open, m.vaultDlg.mode)
	}
	l := m.vaultLayout()
	entries := vaultManageEntries()
	if len(l.rows) != len(entries) || l.drawn < len(entries) {
		t.Fatalf("precondition: rows = %d, drawn = %d, entries = %d", len(l.rows), l.drawn, len(entries))
	}
	for i, row := range l.rows {
		if row.entry != i {
			t.Fatalf("precondition: row %d holds entry %d, want it in dialog order", i, row.entry)
		}
	}

	// Motion onto the second entry highlights it and runs nothing.
	y := l.rowY + 1
	m = send(m, mouseMotion(l.x+2, y))
	if m.vaultDlg.manageHover != 1 {
		t.Fatalf("manageHover = %d, want 1 (the second entry)", m.vaultDlg.manageHover)
	}
	if m.vaultDlg.manageSel != 0 {
		t.Fatalf("manageSel = %d, hovering must not move the keyboard highlight", m.vaultDlg.manageSel)
	}
	if m.vaultDlg.mode != vaultManage {
		t.Fatalf("mode = %v, hovering must not run an entry", m.vaultDlg.mode)
	}

	// The hovered entry is drawn exactly like the selected one, and
	// differently from an entry that is neither selected nor hovered.
	got := m.renderVaultEntry(1, l.rows[1].text, l)
	savedSel, savedHover := m.vaultDlg.manageSel, m.vaultDlg.manageHover
	m.vaultDlg.manageSel, m.vaultDlg.manageHover = 1, -1
	selected := m.renderVaultEntry(1, l.rows[1].text, l)
	m.vaultDlg.manageSel, m.vaultDlg.manageHover = savedSel, -1
	plain := m.renderVaultEntry(1, l.rows[1].text, l)
	m.vaultDlg.manageSel, m.vaultDlg.manageHover = savedSel, savedHover
	if got != selected {
		t.Fatalf("the hovered entry must be drawn like the selected one:\nhover:   %q\nselect: %q", got, selected)
	}
	if got == plain {
		t.Fatalf("the hovered entry must be drawn differently from a plain one: %q", got)
	}

	// A motion outside the box clears the highlight and is swallowed: the
	// dialog must stay open (a stray motion cannot dismiss it).
	m = send(m, mouseMotion(l.x-2, l.y))
	if m.vaultDlg.manageHover != -1 {
		t.Fatalf("manageHover = %d, want -1 outside the dialog", m.vaultDlg.manageHover)
	}
	if !m.vaultDlg.open || m.vaultDlg.mode != vaultManage {
		t.Fatalf("open = %v mode = %v, a motion outside must change nothing", m.vaultDlg.open, m.vaultDlg.mode)
	}

	// The press contract is unchanged: pressing the entry runs it (the
	// second manager entry opens the hint form).
	m = send(m, mousePress(l.x+2, y))
	if m.vaultDlg.mode != vaultHint {
		t.Fatalf("mode = %v, want the hint form after pressing the second entry", m.vaultDlg.mode)
	}
}
