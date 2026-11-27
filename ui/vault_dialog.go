package ui

// vault_dialog.go implements the modal forms around the saved-connection
// passphrase: first-time setup, unlocking a locked vault, changing the
// passphrase or its hint, removing the encryption, and the small manager that
// offers those actions once the vault is open.
//
//   - ↑/↓ (or Tab) move between fields, ←→ move the caret; secret fields are
//     masked and a paste is stripped of control characters, like elsewhere.
//   - ⏎ submits, Esc cancels — except in the unlock dialog, where Esc means
//     "skip it and keep working while locked" (the design doc's lifecycle
//     table calls for that: preferences work, connections do not).
//   - Validation errors keep the dialog open with the field values intact;
//     only a successful submit closes it.
//   - The dialog is modal: it swallows every key and click so nothing reaches
//     the editor or the tree behind it. At startup it is the only thing on
//     screen until the vault is open or skipped.
//
// Security rules pinned here (docs/secrets-encryption.md, "What the UI must
// not do"): the passphrase is never echoed, logged or placed into a status
// message; closing the dialog blanks every field; a hint equal to the
// passphrase is refused, because the hint is stored in clear text next to the
// ciphertext.

import (
	"errors"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/vault"
)

// vaultMode selects which form the dialog shows. The fields, the title and
// the submit action all follow from it.
type vaultMode int

// The dialog forms.
const (
	vaultSetup  vaultMode = iota // protect connections that are plain text now
	vaultUnlock                  // open a vault that came with the settings file
	vaultChange                  // swap the passphrase (requires the current one)
	vaultRemove                  // drop the encryption (requires the passphrase)
	vaultHint                    // change the clear-text hint only
	vaultManage                  // the unlocked-vault action menu
)

// Passphrase length policy: fewer than the minimum is refused, fewer than
// the strong length is accepted with a status-bar warning. The numbers match
// the design document, which refuses to pretend a weak passphrase is fine
// while still letting the user choose one.
const (
	vaultMinLen    = 8
	vaultStrongLen = 12
)

// vaultField is one row of the passphrase form. It is the connection form's
// field model reduced to what this dialog needs: text or a masked secret.
type vaultField struct {
	key    i18n.Key
	secret bool
	value  string
	caret  int
}

// vaultDialog is the state of the modal form.
type vaultDialog struct {
	open   bool
	mode   vaultMode
	fields []vaultField
	focus  int
	// err is the validation or failure line shown in the hint row. It is
	// always a localized string or the text of a settings-package error:
	// nothing typed into the fields is ever formatted into it.
	err string
	// manageSel is the highlighted entry of the manager list (vaultManage).
	manageSel int
	// manageHover is the entry under the pointer in the manager list, -1 when
	// the pointer is elsewhere. Hovering only highlights: it never runs an
	// entry and never moves manageSel (round 7 §1).
	manageHover int
}

// The manager entries, in dialog order.
const (
	vaultManageChange = iota
	vaultManageHintEntry
	vaultManageRemove
	vaultManageLock
)

// vaultManageEntries lists what an unlocked vault offers.
func vaultManageEntries() []int {
	return []int{vaultManageChange, vaultManageHintEntry, vaultManageRemove, vaultManageLock}
}

// vaultManageLabelKey maps a manager entry to its label.
func vaultManageLabelKey(entry int) i18n.Key {
	switch entry {
	case vaultManageChange:
		return i18n.KeyVaultManageChange
	case vaultManageHintEntry:
		return i18n.KeyVaultManageChangeHint
	case vaultManageRemove:
		return i18n.KeyVaultManageRemove
	case vaultManageLock:
		return i18n.KeyVaultLockNow
	}
	return i18n.KeyVaultManageChange
}

// vaultModeTitle returns the dialog title of a mode.
func vaultModeTitle(mode vaultMode) i18n.Key {
	switch mode {
	case vaultSetup:
		return i18n.KeyVaultSetupTitle
	case vaultChange:
		return i18n.KeyVaultChangeTitle
	case vaultRemove:
		return i18n.KeyVaultRemoveTitle
	case vaultHint:
		return i18n.KeyVaultHintTitle
	case vaultManage:
		return i18n.KeyVaultManageTitle
	}
	return i18n.KeyVaultTitle // vaultUnlock and the impossible default
}

// vaultModeButton returns the primary button label of a mode; the manager has
// no buttons (entries are activated in place), so it gets no key.
func vaultModeButton(mode vaultMode) i18n.Key {
	switch mode {
	case vaultSetup:
		return i18n.KeyVaultSet
	case vaultChange:
		return i18n.KeyVaultChange
	case vaultRemove:
		return i18n.KeyVaultRemove
	case vaultHint:
		return i18n.KeyVaultSet
	}
	return i18n.KeyVaultUnlock
}

// ---- Opening and closing ----

// openVaultMode opens the dialog in a given shape, replacing any other
// overlay: this dialog blocks the whole interface while it is open.
func (m *Model) openVaultMode(mode vaultMode) {
	m.closeMenu()
	m.closeObjectsMenu()
	m.closeResultMenu()
	m.closeTabMenu()
	m.popup.close()
	dlg := vaultDialog{open: true, mode: mode, manageHover: -1}
	switch mode {
	case vaultSetup: // the entered list stays plain text until this succeeds
		dlg.fields = []vaultField{
			{key: i18n.KeyVaultPassphrase, secret: true},
			{key: i18n.KeyVaultConfirm, secret: true},
			{key: i18n.KeyVaultHint},
		}
	case vaultUnlock:
		dlg.fields = []vaultField{{key: i18n.KeyVaultPassphrase, secret: true}}
	case vaultChange:
		dlg.fields = []vaultField{
			{key: i18n.KeyVaultCurrent, secret: true},
			{key: i18n.KeyVaultPassphrase, secret: true},
			{key: i18n.KeyVaultConfirm, secret: true},
			{key: i18n.KeyVaultHint},
		}
	case vaultRemove:
		dlg.fields = []vaultField{{key: i18n.KeyVaultPassphrase, secret: true}}
	case vaultHint:
		// The current hint starts in the field: it is edited, not retyped.
		// The stored hint is text from the settings file, not from this
		// session's keystrokes, so it is stripped of control characters
		// before it enters the field (the ui/sanitize.go rule): a tampered
		// settings file must not smuggle an escape sequence into the
		// terminal through the prefill. Editing afterwards takes the normal
		// input path, which drops control characters anyway.
		hint := ""
		if m.settings.Vault != nil {
			hint = stripControl(m.settings.Vault.Hint)
		}
		dlg.fields = []vaultField{{key: i18n.KeyVaultNewHint, value: hint, caret: len([]rune(hint))}}
	case vaultManage:
		// No fields; the entries are the content.
	}
	m.vaultDlg = dlg
}

// openVaultUnlock opens the unlock form (the startup path and the tree row).
func (m *Model) openVaultUnlock() {
	if !m.settings.Locked() {
		return // nothing to unlock; the caller wanted the entry point instead
	}
	m.openVaultMode(vaultUnlock)
}

// openVaultEntry is the shared entry of the Settings menu item and the
// settings-dialog row: it routes by vault state, as the design doc's
// lifecycle table prescribes.
func (m *Model) openVaultEntry() {
	switch {
	case !m.settings.Encrypted():
		m.openVaultMode(vaultSetup)
	case m.settings.Locked():
		m.openVaultMode(vaultUnlock)
	default:
		m.openVaultMode(vaultManage)
	}
}

// closeVault closes the dialog and wipes the typed material: the field values
// are blanked so no passphrase lingers in the model, ready to be inherited by
// the next open or fished out of the state.
func (m *Model) closeVault() {
	for i := range m.vaultDlg.fields {
		m.vaultDlg.fields[i].value = ""
		m.vaultDlg.fields[i].caret = 0
	}
	m.vaultDlg = vaultDialog{}
}

// skipVault closes the dialog after an Esc and leaves a status note about the
// state the user just chose: unlocked-but-locked stays locked, a skipped
// first-run setup stays plain text.
func (m *Model) skipVault() tea.Cmd {
	mode := m.vaultDlg.mode
	m.closeVault()
	switch mode {
	case vaultUnlock:
		return m.setMessage(m.t(i18n.KeyVaultLocked))
	case vaultSetup:
		return m.setMessage(m.t(i18n.KeyVaultPlaintextWarning))
	}
	return nil
}

// lockVaultNow seals the vault in memory: the decrypted connections and the
// key are dropped. Nothing is written to disk — the on-disk envelope is
// untouched by locking, which is exactly why the state is offered at all.
func (m *Model) lockVaultNow() tea.Cmd {
	if !m.settings.Encrypted() || m.settings.Locked() {
		return nil // nothing locked in either direction
	}
	m.settings.Lock()
	m.resetObjects()
	if len(m.objectsRows()) > 0 {
		m.objects.sel = 0 // park the caret on the locked row: Enter unlocks
	}
	text := m.t(i18n.KeyVaultLocked)
	if m.connOpen != nil {
		// Locking is disk-side only: an open session keeps running. The user
		// must not read the sealed tree as "I was disconnected".
		text += " · " + m.t(i18n.KeyVaultLockedConnLive)
	}
	return m.setMessage(text)
}

// vaultBlocked reports that connections are locked and, when they are, sets
// the status message that says so. Every entry point that would open, edit,
// save or test a connection calls it first — connectTo, testConnection,
// deleteConnection and submitConnDialog — so a shortcut or future path cannot
// form a connection while locked even if it bypasses the tree. Two
// presentation guards keep the same contract closer to the screen: the tree
// replaces its rows with the locked notice (objects.go), and the context menu
// intercepts its connection actions (context_menu.go).
func (m *Model) vaultBlocked() (tea.Cmd, bool) {
	if !m.settings.Locked() {
		return nil, false
	}
	return m.setMessage(m.t(i18n.KeyVaultUnlockFirst)), true
}

// ---- Submit ----

// vaultErrText maps an error from opening the vault to the dialog's error
// line. Only an authentication failure earns the "wrong passphrase" line:
// vault.ErrWrongPassphrase covers a wrong passphrase and a damaged file by
// design (GCM cannot tell them apart), and that is the message the package
// itself words that way. Every other error — an unsupported envelope version
// or algorithm, a file this build cannot make sense of — is shown verbatim,
// or the user would keep retyping a passphrase that was never the problem.
// The vault package's parameter errors carry no secret, and errText strips
// control characters like every other error text from outside the program.
func (m Model) vaultErrText(err error) string {
	if errors.Is(err, vault.ErrWrongPassphrase) {
		return m.t(i18n.KeyVaultWrong)
	}
	return errText(err)
}

// hintOpensVault answers whether the hint is the vault's passphrase. The
// session never keeps the passphrase itself, so a trial open against the
// stored envelope is the only comparison possible; one KDF run is the price
// of not publishing the secret through the clear-text hint.
//
// The trial never changes the session: vault.Open hands the key to its
// caller, so a key that proves the hint is the passphrase is zeroed right
// away instead of being installed — a locked vault stays locked, an unlocked
// one keeps its key and connections, exactly as before the trial. An
// authentication failure (vault.ErrWrongPassphrase, which by design also
// covers a damaged file) answers "not the passphrase"; any other error means
// the trial could not run and is returned so the caller shows it instead of
// accepting the hint unverified.
func (m Model) hintOpensVault(hint string) (opens bool, err error) {
	if m.settings.Vault == nil {
		return false, nil // nothing sealed; no hint can be the passphrase
	}
	plaintext, key, err := vault.Open(hint, m.settings.Vault)
	if err != nil {
		if errors.Is(err, vault.ErrWrongPassphrase) {
			return false, nil
		}
		return false, err
	}
	// The hint IS the passphrase: wipe the decrypted payload and drop the
	// key without installing it, leaving the session state untouched.
	for i := range plaintext {
		plaintext[i] = 0
	}
	key.Zero()
	return true, nil
}

// submitVault validates the current form and applies it. Field errors set the
// error line and keep the dialog open; only a successful call to the settings
// package closes it. The typed passphrase never leaves this function: it is
// passed to the settings API and nothing else.
func (m *Model) submitVault() tea.Cmd {
	dlg := m.vaultDlg
	runes := func(i int) []rune { return []rune(dlg.fields[i].value) }
	switch dlg.mode {
	case vaultSetup:
		// Fields: Passphrase(0), Confirm(1), Hint(2).
		if len(runes(0)) < vaultMinLen {
			m.vaultDlg.err = m.t(i18n.KeyVaultTooShort)
			return nil
		}
		if dlg.fields[0].value != dlg.fields[1].value {
			m.vaultDlg.err = m.t(i18n.KeyVaultMismatch)
			return nil
		}
		if h := dlg.fields[2].value; h != "" && h == dlg.fields[0].value {
			// The hint travels in clear text next to the ciphertext; making
			// it the passphrase would publish the secret.
			m.vaultDlg.err = m.t(i18n.KeyVaultHintIsPassphrase)
			return nil
		}
		if err := m.settings.SetPassphrase(dlg.fields[0].value, dlg.fields[2].value); err != nil {
			m.vaultDlg.err = errText(err)
			return nil
		}
		weak := len([]rune(dlg.fields[0].value)) < vaultStrongLen // measured before the fields are wiped
		m.closeVault()
		cmd := m.persistSettings()
		if weak {
			return tea.Batch(cmd, m.setMessage(
				m.t(i18n.KeyVaultSetOK)+" · "+m.t(i18n.KeyVaultWeak)))
		}
		return tea.Batch(cmd, m.setMessage(m.t(i18n.KeyVaultSetOK)))
	case vaultChange:
		// Fields: Current(0), New(1), Confirm(2), Hint(3).
		if len(runes(1)) < vaultMinLen {
			m.vaultDlg.err = m.t(i18n.KeyVaultTooShort)
			return nil
		}
		if dlg.fields[1].value != dlg.fields[2].value {
			m.vaultDlg.err = m.t(i18n.KeyVaultMismatch)
			return nil
		}
		if h := dlg.fields[3].value; h != "" && h == dlg.fields[1].value {
			m.vaultDlg.err = m.t(i18n.KeyVaultHintIsPassphrase)
			return nil
		}
		if err := m.settings.ChangePassphrase(dlg.fields[0].value, dlg.fields[1].value, dlg.fields[3].value); err != nil {
			// A wrong current passphrase: report it and keep the dialog open,
			// the fields intact — retyping four values is the alternative.
			// An envelope this build cannot open says so instead (see
			// vaultErrText).
			m.vaultDlg.err = m.vaultErrText(err)
			return nil
		}
		weak := len([]rune(dlg.fields[1].value)) < vaultStrongLen
		m.closeVault()
		text := m.t(i18n.KeyVaultChanged)
		if weak {
			text += " · " + m.t(i18n.KeyVaultWeak)
		}
		return tea.Batch(m.persistSettings(), m.setMessage(text))
	case vaultUnlock:
		pass := dlg.fields[0].value
		if err := m.settings.Unlock(pass); err != nil {
			// Wrong passphrase and a damaged file look identical to GCM, and
			// the message says so instead of guessing; an unsupported
			// envelope version or algorithm names itself instead (see
			// vaultErrText).
			m.vaultDlg.err = m.vaultErrText(err)
			return nil
		}
		upgraded := m.upgradeVaultParams(pass)
		m.closeVault()
		m.resetObjects() // the saved connections are back: rebuild the tree
		if len(m.objectsRows()) > 0 {
			m.objects.sel = 0
		}
		text := m.t(i18n.KeyVaultUnlocked)
		if upgraded {
			text += " · " + m.t(i18n.KeyVaultUpgraded)
		}
		var save tea.Cmd
		if upgraded {
			// The upgraded envelope (new salt, current parameters) must reach
			// the file, otherwise the next start would upgrade again.
			save = m.persistSettings()
		}
		return tea.Batch(m.setMessage(text), save)
	case vaultRemove:
		if err := m.settings.RemoveEncryption(dlg.fields[0].value); err != nil {
			m.vaultDlg.err = m.vaultErrText(err)
			return nil
		}
		m.closeVault()
		m.resetObjects()
		if len(m.objectsRows()) > 0 {
			m.objects.sel = 0
		}
		return tea.Batch(m.setMessage(m.t(i18n.KeyVaultRemoved)), m.persistSettings())
	case vaultHint:
		hint := strings.TrimSpace(dlg.fields[0].value)
		// The hint is stored in the clear next to the ciphertext, so a
		// hint that IS the passphrase would publish the secret — the rule
		// the setup and change forms enforce by comparing two typed
		// fields. Here there is no passphrase field to compare against,
		// so the typed value is checked by a trial open of the envelope
		// (see hintOpensVault): only an authentication failure proves it
		// is not the passphrase, and a successful trial leaves the session
		// exactly as it was.
		if hint != "" {
			opens, trialErr := m.hintOpensVault(hint)
			switch {
			case trialErr != nil:
				// The trial could not run (an envelope this build cannot
				// open): accepting now would silently accept the
				// passphrase, so the real problem is shown instead.
				m.vaultDlg.err = errText(trialErr)
				return nil
			case opens:
				m.vaultDlg.err = m.t(i18n.KeyVaultHintIsPassphrase)
				return nil
			}
		}
		if err := m.settings.SetHint(hint); err != nil {
			m.vaultDlg.err = errText(err)
			return nil
		}
		m.closeVault()
		return tea.Batch(m.setMessage(m.t(i18n.KeyVaultHintSet)), m.persistSettings())
	case vaultManage:
		switch entry := vaultManageEntries()[clamp(dlg.manageSel, 0, len(vaultManageEntries())-1)]; entry {
		case vaultManageChange:
			m.closeVault()
			m.openVaultMode(vaultChange)
		case vaultManageHintEntry:
			m.closeVault()
			m.openVaultMode(vaultHint)
		case vaultManageRemove:
			m.closeVault()
			m.openVaultMode(vaultRemove)
		case vaultManageLock:
			m.closeVault()
			return m.lockVaultNow()
		}
		return nil
	}
	return nil
}

// upgradeVaultParams reseals a vault whose envelope was written with fewer
// KDF rounds than this build uses. It runs only after a successful Unlock, so
// the passphrase is known-good and a wrong-password path can never trigger a
// rewrite. Failure keeps the old envelope: an upgrade must never lock the
// user out.
func (m *Model) upgradeVaultParams(pass string) bool {
	env := m.settings.Vault
	if env == nil || env.KDF.Iterations >= vault.DefaultIterations {
		return false
	}
	hint := env.Hint
	if err := m.settings.ChangePassphrase(pass, pass, hint); err != nil {
		return false
	}
	return true
}

// ---- Field editing ----

// handleVaultKey handles a key press while the dialog is open and reports
// whether it consumed the key. The dialog is modal, so it consumes everything.
func (m Model) handleVaultKey(key string, runes []rune, paste bool) (Model, tea.Cmd, bool) {
	if m.vaultDlg.mode == vaultManage {
		return m.handleVaultManageKey(key)
	}
	if len(m.vaultDlg.fields) == 0 {
		return m, nil, true // defensive: nothing to edit
	}
	f := &m.vaultDlg.fields[m.vaultDlg.focus]
	switch key {
	case "esc":
		return m, m.skipVault(), true
	case "enter":
		return m, m.submitVault(), true
	case "up", "shift+tab":
		m.moveVaultFocus(-1)
		return m, nil, true
	case "down", "tab":
		m.moveVaultFocus(1)
		return m, nil, true
	}
	switch key {
	case "left":
		f.caret = max(f.caret-1, 0)
	case "right":
		f.caret = min(f.caret+1, len([]rune(f.value)))
	case "home":
		f.caret = 0
	case "end":
		f.caret = len([]rune(f.value))
	case "backspace":
		if f.caret > 0 {
			r := []rune(f.value)
			f.value = string(r[:f.caret-1]) + string(r[f.caret:])
			f.caret--
		}
	case "delete":
		if r := []rune(f.value); f.caret < len(r) {
			f.value = string(r[:f.caret]) + string(r[f.caret+1:])
		}
	default:
		if len(runes) == 0 {
			return m, nil, true
		}
		// Modified keys (Alt+2) carry runes as well, and those belong to the
		// shortcut layer this dialog shields; only keys whose name IS the
		// text they carry are text. A paste is text in bulk, control
		// characters and all: they are dropped, a newline inside a passphrase
		// must not survive anyway.
		if !paste && key != string(runes) && key != "space" {
			return m, nil, true
		}
		insert := strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, string(runes))
		r := []rune(f.value)
		f.value = string(r[:f.caret]) + insert + string(r[f.caret:])
		f.caret += len([]rune(insert))
	}
	return m, nil, true
}

// moveVaultFocus moves to the next field in the given direction, wrapping.
func (m *Model) moveVaultFocus(delta int) {
	n := len(m.vaultDlg.fields)
	m.vaultDlg.focus = ((m.vaultDlg.focus+delta)%n + n) % n
}

// handleVaultManageKey handles keys while the manager list is open: the
// entries are a small menu.
func (m Model) handleVaultManageKey(key string) (Model, tea.Cmd, bool) {
	n := len(vaultManageEntries())
	switch key {
	case "esc":
		m.closeVault()
		return m, nil, true
	case "up", "shift+tab":
		m.vaultDlg.manageSel = ((m.vaultDlg.manageSel-1)%n + n) % n
	case "down", "tab":
		m.vaultDlg.manageSel = (m.vaultDlg.manageSel + 1) % n
	case "enter", "space":
		return m, m.submitVault(), true
	}
	return m, nil, true
}

// ---- Mouse ----

// handleVaultMouse handles a mouse event while the dialog is open. A click
// outside the box is swallowed rather than dismissed: the dialog may be
// holding the only way into the user's connections, so wandering clicks must
// not close it by accident.
func (m Model) handleVaultMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	// Motion comes first: in the manager it highlights the entry under the
	// pointer (it never runs one), and anywhere else — inside the box or
	// outside it — it does nothing at all. The dialog is modal, so a motion
	// may never press a button, focus a field or reach the panes behind it.
	if msg.Action == tea.MouseActionMotion {
		m.vaultDlg.manageHover = -1
		if m.vaultDlg.mode == vaultManage {
			l := m.vaultLayout()
			if row := m.vaultRowAt(msg.X, msg.Y); row >= 0 && row < len(l.rows) && l.rows[row].entry >= 0 {
				m.vaultDlg.manageHover = l.rows[row].entry
			}
		}
		return m, nil
	}
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}
	l := m.vaultLayout()
	if m.vaultDlg.mode == vaultManage {
		if row := m.vaultRowAt(msg.X, msg.Y); row >= 0 {
			if row < len(l.rows) && l.rows[row].entry >= 0 {
				m.vaultDlg.manageSel = l.rows[row].entry
				return m, m.submitVault()
			}
		}
		return m, nil
	}
	switch {
	case l.primary.Contains(msg.X, msg.Y):
		return m, m.submitVault()
	case l.cancel.Contains(msg.X, msg.Y):
		return m, m.skipVault()
	}
	if row, ok := m.vaultFieldAt(msg.X, msg.Y); ok {
		m.vaultDlg.focus = row
	}
	return m, nil
}

// ---- Geometry ----

// vaultRow is one body row of the dialog: a form field (field >= 0), a
// manager entry (entry >= 0) or a static notice line (both < 0, text set).
type vaultRow struct {
	field int
	entry int
	text  string // a static line's pre-localized text
	// hot marks the static line that must never be missed (the setup form's
	// no-recovery warning): it is drawn in the palette's warning colour, one
	// step quieter than the bold danger style of an error line.
	hot bool
}

type vaultLayout struct {
	x, y, w, h int // outer box, borders included
	innerW     int
	labelW     int
	valueW     int
	rows       []vaultRow // in draw order
	drawn      int        // how many rows fit (a short screen clips the tail)
	rowY       int        // screen row of the first body row
	valueX     int        // screen column of the value column
	primary    Rect       // field forms only; empty for the manager list
	cancel     Rect
}

// vaultLayout computes the dialog geometry for the current screen. It follows
// the connection form's scheme, which the shared modal rules (ui/modal.go)
// were written around: fields as two-column rows, notices as full-width
// lines, buttons on the last row. Rendering and the mouse hit test both read
// it, so the drawn box and the clickable box agree.
func (m Model) vaultLayout() vaultLayout {
	d := m.vaultDlg

	labelW := 0
	for _, f := range d.fields {
		labelW = max(labelW, lipgloss.Width(m.t(f.key)))
	}
	valueW := connValueMaxW // a passphrase is normal-sized; keep it simple
	rows := make([]vaultRow, 0, len(d.fields)+4)
	for i := range d.fields {
		rows = append(rows, vaultRow{field: i, entry: -1})
	}
	// The notices each form owes its user, below the fields.
	switch d.mode {
	case vaultSetup:
		rows = append(rows,
			vaultRow{field: -1, entry: -1, text: m.t(i18n.KeyVaultMinNote)},
			vaultRow{field: -1, entry: -1, text: m.t(i18n.KeyVaultHintWarning)},
			// The no-recovery line is not an error and cannot be dismissed:
			// it names the one consequence of forgetting the passphrase that
			// no confirmation dialog can undo, so it is always on the form,
			// at its very end, in the warning colour.
			vaultRow{field: -1, entry: -1, text: m.t(i18n.KeyVaultNoRecovery), hot: true},
		)
	case vaultChange, vaultHint:
		// The forms that type a hint owe the same warning: the value ends
		// up in the file in the clear, next to the ciphertext.
		rows = append(rows, vaultRow{field: -1, entry: -1, text: m.t(i18n.KeyVaultHintWarning)})
	case vaultRemove:
		rows = append(rows, vaultRow{field: -1, entry: -1, text: m.t(i18n.KeyVaultRemoveWarning)})
	case vaultUnlock:
		// The stored hint is public by design: showing it here is the point.
		// It is text from the settings file, not from this session's
		// keystrokes, so it goes through stripControl before rendering, like
		// every other file-sourced text (ui/sanitize.go): a tampered or
		// shared settings file must not reach the terminal as an ANSI/OSC
		// escape sequence.
		if m.settings.Vault != nil {
			if hint := strings.TrimSpace(stripControl(m.settings.Vault.Hint)); hint != "" {
				rows = append(rows, vaultRow{field: -1, entry: -1, text: m.t(i18n.KeyVaultHintLine, hint)})
			}
		}
	}
	if d.mode == vaultManage {
		for i, entry := range vaultManageEntries() {
			name := m.t(vaultManageLabelKey(entry))
			rows = append(rows, vaultRow{field: -1, entry: i, text: name})
			labelW = max(labelW, lipgloss.Width(name))
		}
	}
	for _, row := range rows {
		if row.field < 0 && row.entry < 0 {
			labelW = max(labelW, lipgloss.Width(row.text))
		}
	}

	area := m.paneRects().area
	wantW, wantH := labelW+valueW+6, len(rows)+6
	var w, h int
	if d.mode == vaultUnlock {
		// The unlock form is a single field and at most one hint line: the
		// full 12-row minimum would pad it with a wall of blank rows (it
		// measured five on a real terminal), so the compact floor of eight
		// applies (ui/modal.go). Every other shape keeps the full minimum.
		w, h = modalSizeCompact(wantW, wantH, area.W, area.H)
	} else {
		w, h = modalSize(wantW, wantH, area.W, area.H)
	}
	rect := modalRect(area, w, h)
	x, y := rect.X, rect.Y
	innerW := w - 2
	valueW = clamp(innerW-labelW-4, 1, connValueMaxW)

	// The manager keeps the entries but loses the button row: its rows ARE
	// the buttons, and the hint line sits directly above the bottom border.
	extra := 4 // title row, hint row, top and bottom border
	if d.mode != vaultManage {
		extra = 5 // ... plus the button row
	}
	drawn := min(len(rows), h-extra)

	l := vaultLayout{
		x: x, y: y, w: w, h: h,
		innerW: innerW,
		labelW: labelW,
		valueW: valueW,
		rows:   rows,
		drawn:  drawn,
		rowY:   y + 2,
		valueX: x + labelW + 4,
	}
	if d.mode != vaultManage {
		btnY := y + h - 2
		l.primary = Rect{X: x + 2, Y: btnY, W: lipgloss.Width(m.t(vaultModeButton(d.mode))) + 2, H: 1}
		l.cancel = Rect{X: l.primary.X + l.primary.W + 2, Y: btnY, W: lipgloss.Width(m.t(i18n.KeyConnCancel)) + 2, H: 1}
	}
	return l
}

// vaultRowAt maps a screen coordinate onto a body row of the dialog (-1 when
// the point is on no drawn row).
func (m Model) vaultRowAt(x, y int) int {
	l := m.vaultLayout()
	if !(Rect{X: l.x, Y: l.y, W: l.w, H: l.h}).Contains(x, y) {
		return -1
	}
	row := y - l.rowY
	if row < 0 || row >= l.drawn {
		return -1
	}
	return row
}

// vaultFieldAt maps a coordinate onto a form field; only field rows answer.
func (m Model) vaultFieldAt(x, y int) (int, bool) {
	row := m.vaultRowAt(x, y)
	if row < 0 {
		return 0, false
	}
	f := m.vaultLayout().rows[row]
	if f.field < 0 {
		return 0, false
	}
	return f.field, true
}

// ---- Rendering ----

// renderVaultDialog renders the dialog and returns its screen origin plus the
// block of lines to blit.
func (m Model) renderVaultDialog() (x, y int, block []string, ok bool) {
	if !m.vaultDlg.open {
		return 0, 0, nil, false
	}
	l := m.vaultLayout()
	if l.w < 4 || l.h < 3 {
		return 0, 0, nil, false
	}
	border, box := m.theme.dlgBorder, m.theme.dlgBox
	rule := strings.Repeat("─", l.w-2)

	out := make([]string, 0, l.h)
	out = append(out, border.Render("┌"+rule+"┐"))
	title := padANSI(truncateANSI(" "+m.t(vaultModeTitle(m.vaultDlg.mode)), l.innerW), l.innerW)
	out = append(out, border.Render("│")+m.theme.dlgTitle.Render(title)+border.Render("│"))

	for r := 0; r < l.drawn; r++ {
		row := l.rows[r]
		var line string
		switch {
		case row.field >= 0:
			line = m.renderVaultField(row.field, l)
		case row.entry >= 0:
			line = m.renderVaultEntry(row.entry, row.text, l)
		default:
			// A static notice line. The hot ones (the setup form's
			// no-recovery warning) take the palette's danger colour without
			// the bold that marks an error: seen at once, not shouted.
			lineStyle := m.theme.dlgDesc
			if row.hot {
				lineStyle = lipgloss.NewStyle().Foreground(m.theme.palette.danger)
			}
			line = m.theme.dlgBox.Render(" ") +
				lineStyle.Render(padANSI(truncateANSI(row.text, l.innerW-1), l.innerW-1))
		}
		out = append(out, border.Render("│")+padANSI(truncateANSI(line, l.innerW), l.innerW)+border.Render("│"))
	}
	blank := border.Render("│") + box.Render(strings.Repeat(" ", l.innerW)) + border.Render("│")
	// The hint row belongs on the row above the bottom border. The button row
	// occupies the one above it in the field forms; the manager has no button
	// row (its entries are the controls), so its blanks run one row deeper.
	fill := l.h - 3
	if m.vaultDlg.mode == vaultManage {
		fill = l.h - 2
	}
	for len(out) < fill {
		out = append(out, blank)
	}

	// The row above the bottom border is the hint line — or the error line
	// when validation failed: the two share one row so the frame never grows
	// over a small screen, and an error always stands where the eye goes.
	hintKey := i18n.KeyVaultFormHint
	if m.vaultDlg.mode == vaultManage {
		hintKey = i18n.KeyVaultManageHint
	}
	hintText := m.t(hintKey)
	if m.vaultDlg.err != "" {
		hintText = m.vaultDlg.err
	}
	hint := padANSI(truncateANSI(hintText, l.innerW), l.innerW)
	out = append(out, border.Render("│")+m.theme.dlgDesc.Render(hint)+border.Render("│"))

	if m.vaultDlg.mode == vaultManage {
		out = append(out, border.Render("└"+rule+"┘"))
		return l.x, l.y, out, true
	}

	primary := m.theme.toolBtnHover.Render(" " + m.t(vaultModeButton(m.vaultDlg.mode)) + " ")
	cancel := m.theme.toolBtn.Render(" " + m.t(i18n.KeyConnCancel) + " ")
	row := strings.Repeat(" ", l.primary.X-l.x-1) + primary + "  " + cancel
	out = append(out, border.Render("│")+padANSI(truncateANSI(row, l.innerW), l.innerW)+border.Render("│"))
	out = append(out, border.Render("└"+rule+"┘"))
	return l.x, l.y, out, true
}

// renderVaultEntry draws one manager entry with the selection highlight, like
// a context-menu item of its own dialog. The entry under the pointer wears
// the same look as manageSel; hovering only draws, it never moves the entry
// Enter runs.
func (m Model) renderVaultEntry(entry int, text string, l vaultLayout) string {
	selected := entry == m.vaultDlg.manageSel || entry == m.vaultDlg.manageHover
	labelStyle, padStyle := m.theme.dlgLabel, m.theme.dlgBox
	if selected {
		labelStyle, padStyle = m.theme.dlgValueSel, m.theme.dlgRowSel
	}
	return padStyle.Render(" ") +
		labelStyle.Render(padANSI(truncateANSI(text, l.innerW-2), l.innerW-2)) +
		padStyle.Render(" ")
}

// renderVaultField renders one field row: the label, the value (masked when
// the field is a secret) and a caret in the focused field. Long values scroll
// horizontally inside the value column instead of widening the box, the same
// sliding window the connection form uses.
func (m Model) renderVaultField(i int, l vaultLayout) string {
	f := m.vaultDlg.fields[i]
	focused := i == m.vaultDlg.focus
	labelStyle, valueStyle, padStyle := m.theme.dlgLabel, m.theme.dlgValue, m.theme.dlgBox
	caret := " "
	if focused {
		labelStyle, valueStyle, padStyle = m.theme.dlgRowSel, m.theme.dlgValueSel, m.theme.dlgRowSel
		caret = "▸"
	}

	shown := f.value
	if f.secret {
		shown = strings.Repeat("•", len([]rune(f.value)))
	}
	text := padStyle.Render(caret) +
		labelStyle.Render(padANSI(m.t(f.key), l.labelW)) +
		padStyle.Render("  ")
	if focused {
		runes := []rune(shown)
		at := clamp(f.caret, 0, len(runes))
		maxChars := max(1, l.valueW-1)
		start := 0
		if at > maxChars {
			start = at - maxChars
		}
		end := min(len(runes), start+maxChars)
		visible := runes[start:end]
		at -= start
		text += valueStyle.Render(string(visible[:at])) +
			m.theme.cursor.Render(" ") +
			valueStyle.Render(padANSI(string(visible[at:]), maxChars-at))
	} else {
		text += valueStyle.Render(padANSI(shown, l.valueW))
	}
	return text + padStyle.Render(" ")
}
