package ui

// settings_dialog.go implements the modal preferences dialog: a small
// bordered box centred between the menu bar and the status bar.
//
//   - ↑/↓ (or Tab) move the selection; a select row opens its dropdown with
//     Enter/Space/↓, the boolean rows change with Enter/Space, Esc closes.
//   - Changes apply immediately (the value, theme or language updates on the
//     spot) and are persisted right away; there is no "OK" to forget.
//   - The dialog is modal: it swallows every other key so nothing leaks into
//     the editor behind it, and the open dropdown swallows everything as long
//     as it is open.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
)

// settingsDialog is the state of the modal dialog.
type settingsDialog struct {
	open bool
	sel  int // selected row
	// choice is the open dropdown of the selected select row (language,
	// theme). While it is open it is modal inside the dialog: every key
	// belongs to it and no click reaches the rows behind it.
	choice selectList
}

// settingRowKind identifies a settings row.
type settingRowKind int

// The dialog rows.
const (
	rowLanguage settingRowKind = iota
	rowTheme
	rowIcons
	rowMouse
	rowGutter
	rowReadOnly
	rowVault
	rowResetLayout
)

// settingRow describes one row of the dialog.
type settingRow struct {
	labelKey i18n.Key
	descKey  i18n.Key
	kind     settingRowKind
}

// settingRows is the dialog content, top to bottom.
var settingRows = []settingRow{
	{i18n.KeySettingsLanguage, i18n.KeySettingsDescLanguage, rowLanguage},
	{i18n.KeySettingsTheme, i18n.KeySettingsDescTheme, rowTheme},
	{i18n.KeySettingsIcons, i18n.KeySettingsDescIcons, rowIcons},
	{i18n.KeySettingsMouse, i18n.KeySettingsDescMouse, rowMouse},
	{i18n.KeySettingsGutter, i18n.KeySettingsDescGutter, rowGutter},
	{i18n.KeySettingsReadOnly, i18n.KeySettingsDescReadOnly, rowReadOnly},
	{i18n.KeyVaultMenu, i18n.KeyVaultPlaintextWarning, rowVault},
	{i18n.KeySettingsResetLayout, i18n.KeySettingsDescLayout, rowResetLayout},
}

// localeChoices are the language values the row cycles through; the empty
// string means "follow the system locale".
var localeChoices = []string{"", string(i18n.En), string(i18n.ZhHans)}

// actionGlyph is shown in the value column of the action row.
const actionGlyph = "↻"

// dialogLayout is the computed geometry of the dialog. Rendering and mouse hit
// testing share it, so the drawn box and the clickable box always agree.
type dialogLayout struct {
	x, y, w, h int // outer box, borders included
	innerW     int // content width between the borders
	labelW     int // label column width
	valueW     int // value column width
	rowY       int // screen row of the first settings row
	valueX     int // screen column where the value column starts
}

// dialogLayout computes the dialog geometry for the current screen size.
// The shared modal rules apply: the box is at least modalMinW wide or the
// screen wide, whichever is smaller, and it is capped the same way; the slack
// of the minimum-width box goes to the value column, so the selects have room
// even on the smallest useful dialog.
func (m Model) dialogLayout() dialogLayout {
	labelW, valueW := 0, 0
	for i, row := range settingRows {
		labelW = max(labelW, lipgloss.Width(m.t(row.labelKey)))
		valueW = max(valueW, lipgloss.Width(m.settingValue(i)))
	}
	innerW := labelW + valueW + 4 // 1 + label + 2 + value + 1
	wantW := innerW + 2           // the borders

	area := m.paneRects().area
	w, h := modalSize(wantW, len(settingRows)+7, area.W, area.H)
	// The box width is what the rules decided, so the columns have to fit it:
	// on a wide screen the slack of the minimum-width box goes to the value
	// column; on a narrow one the value truncates and the label keeps its
	// width. Either way every line is exactly the box wide.
	innerW = w - 2
	valueW = max(1, innerW-labelW-4)
	r := modalRect(area, w, h)
	x, y := r.X, r.Y

	return dialogLayout{
		x: x, y: y, w: w, h: h,
		innerW: innerW, labelW: labelW, valueW: valueW,
		rowY:   y + 3, // border + title + blank line
		valueX: x + labelW + 4,
	}
}

// dialogRowAt returns the settings row under a screen coordinate.
func (m Model) dialogRowAt(x, y int) (int, bool) {
	l := m.dialogLayout()
	if !(Rect{X: l.x, Y: l.y, W: l.w, H: l.h}).Contains(x, y) {
		return 0, false
	}
	row := y - l.rowY
	if row < 0 || row >= len(settingRows) {
		return 0, false
	}
	return row, true
}

// dialogValueAt reports whether a coordinate lands on the value column of a row
// (clicking there changes the value).
func (m Model) dialogValueAt(x, y int) (int, bool) {
	row, ok := m.dialogRowAt(x, y)
	if !ok {
		return 0, false
	}
	l := m.dialogLayout()
	if x < l.valueX || x >= l.valueX+l.valueW {
		return 0, false
	}
	return row, true
}

// ---- Values ----

// selectGlyph marks a row whose value opens a dropdown, like the conn form's
// type field does.
const selectGlyph = " ▾"

// settingValue returns the text shown in the value column of a row. Select
// rows carry the dropdown marker, boolean rows cycle on/off instead.
func (m Model) settingValue(i int) string {
	switch settingRows[i].kind {
	case rowLanguage:
		text := ""
		if m.settings.Locale == "" {
			text = fmt.Sprintf("%s (%s)",
				m.t(i18n.KeySettingsFollowSystem), i18n.LocaleName(m.sysLocale))
		} else if l, ok := i18n.Normalize(m.settings.Locale); ok {
			text = i18n.LocaleName(l)
		} else {
			text = m.settings.Locale
		}
		return text + selectGlyph
	case rowTheme:
		if m.settings.Theme == settings.ThemeLight {
			return m.t(i18n.KeySettingsThemeLight) + selectGlyph
		}
		return m.t(i18n.KeySettingsThemeDark) + selectGlyph
	case rowIcons:
		return m.t(onOffKey(m.settings.Icons))
	case rowMouse:
		return m.t(onOffKey(m.settings.Mouse))
	case rowGutter:
		return m.t(onOffKey(m.settings.Gutter))
	case rowReadOnly:
		return m.t(onOffKey(m.settings.ReadOnly))
	case rowVault:
		return actionGlyph
	case rowResetLayout:
		return actionGlyph
	}
	return ""
}

// settingHasSelect reports whether the row opens a dropdown (on/off toggles
// have no dropdown: for two values cycling is the better control).
func settingHasSelect(kind settingRowKind) bool {
	return kind == rowLanguage || kind == rowTheme
}

// localeOptions are the language values of the language select; the empty
// value means "follow the system locale".
func (m Model) localeOptions() []selectOption {
	opts := []selectOption{{
		Value: "",
		Label: m.t(i18n.KeySettingsFollowSystem) + " (" + i18n.LocaleName(m.sysLocale) + ")",
	}}
	for _, loc := range i18n.Available() {
		opts = append(opts, selectOption{Value: string(loc), Label: i18n.LocaleName(loc)})
	}
	return opts
}

// themeOptions are the two palettes of the theme select. The values are the
// theme identifiers the settings file stores.
func (m Model) themeOptions() []selectOption {
	return []selectOption{
		{Value: settings.ThemeDark, Label: m.t(i18n.KeySettingsThemeDark)},
		{Value: settings.ThemeLight, Label: m.t(i18n.KeySettingsThemeLight)},
	}
}

// settingsChoiceLayout computes the geometry of the open dropdown: anchored
// under the value column of the selected row and kept inside the dialog's
// last settings row, so the description and hint rows at the bottom of the
// box stay visible behind it.
func (m Model) settingsChoiceLayout() (selectLayout, bool) {
	if !m.dlg.choice.open {
		return selectLayout{}, false
	}
	l := m.dialogLayout()
	anchorX := l.valueX
	anchorY := l.rowY + clamp(m.dlg.sel, 0, len(settingRows)-1)
	return layoutSelectList(m.dlg.choice.options, m.dlg.choice.sel,
		anchorX, anchorY, l.x+l.w-2, l.rowY+len(settingRows)-1), true
}

// onOffKey maps a boolean to its "on"/"off" message key.
func onOffKey(v bool) i18n.Key {
	if v {
		return i18n.KeyOn
	}
	return i18n.KeyOff
}

// ---- Interaction ----

// openSettings opens the dialog.
func (m *Model) openSettings() {
	m.closeMenu()
	m.closeObjectsMenu()
	m.closeResultMenu()
	m.dlg.open = true
	m.dlg.sel = clamp(m.dlg.sel, 0, len(settingRows)-1)
}

// closeSettings closes the dialog.
func (m *Model) closeSettings() {
	m.dlg.open = false
	m.dlg.choice.Close()
}

// moveSettingsSelection moves the row selection by delta, wrapping around.
func (m *Model) moveSettingsSelection(delta int) {
	n := len(settingRows)
	m.dlg.sel = ((m.dlg.sel+delta)%n + n) % n
}

// openSettingChoice opens the dropdown of the selected select row, starting
// on its current value.
func (m *Model) openSettingChoice() {
	if m.dlg.sel < 0 || m.dlg.sel >= len(settingRows) {
		return
	}
	switch settingRows[m.dlg.sel].kind {
	case rowLanguage:
		m.dlg.choice.Open(m.localeOptions(), m.settings.Locale)
	case rowTheme:
		m.dlg.choice.Open(m.themeOptions(), m.settings.Theme)
	}
}

// handleSettingsKey handles a key press while the dialog is open and reports
// whether it consumed the key. The dialog is modal, so it consumes everything.
func (m Model) handleSettingsKey(key string) (Model, tea.Cmd, bool) {
	// The open dropdown is modal inside the dialog: while it is open, no key
	// can reach the rows behind it. Esc (and Tab) close the list only; Enter
	// picks and applies the value on the spot. picked (not chosen) decides
	// between the two: the "follow the system" locale carries "" as its
	// value, so an empty chosen with picked=true is a real choice, not a
	// dismissal.
	if chosen, done, picked, handled := m.dlg.choice.handleKey(key); handled {
		if !done {
			return m, nil, true
		}
		if picked {
			return m, m.applySettingChoice(chosen), true
		}
		return m, nil, true
	}
	// A select row opens its dropdown on Enter/Space/↓; ←/→ keep cycling as a
	// fallback, like the conn form's type field does.
	if m.dlg.sel >= 0 && m.dlg.sel < len(settingRows) &&
		settingHasSelect(settingRows[m.dlg.sel].kind) {
		switch key {
		case "enter", "space", "down":
			m.openSettingChoice()
			return m, nil, true
		}
	}
	switch key {
	case "esc", "f2":
		m.closeSettings()
		return m, m.setMessage(m.t(i18n.KeyMsgSettingsSaved)), true
	case "up", "shift+tab":
		m.moveSettingsSelection(-1)
	case "down", "tab":
		m.moveSettingsSelection(1)
	case "home":
		m.dlg.sel = 0
	case "end":
		m.dlg.sel = len(settingRows) - 1
	case "left":
		return m, m.changeSetting(-1), true
	case "right":
		return m, m.changeSetting(1), true
	case "enter", "space":
		return m, m.changeSetting(1), true
	}
	return m, nil, true
}

// applySettingChoice applies and persists the value picked in the dropdown of
// the selected row. The dropdown is closed by the time this runs.
func (m *Model) applySettingChoice(value string) tea.Cmd {
	if m.dlg.sel < 0 || m.dlg.sel >= len(settingRows) {
		return nil
	}
	switch settingRows[m.dlg.sel].kind {
	case rowLanguage:
		if _, ok := i18n.Normalize(value); !ok && value != "" {
			return nil // not a locale the UI knows; keep the current one
		}
		m.settings.Locale = value
		m.applyLocale()
		// Report the switch in the newly selected language.
		return tea.Batch(m.persistSettings(),
			m.setMessage(m.t(i18n.KeyMsgLanguageChanged, m.t(i18n.KeyLocaleName))))
	case rowTheme:
		if value == settings.ThemeLight {
			m.settings.Theme = settings.ThemeLight
		} else {
			m.settings.Theme = settings.ThemeDark
		}
		m.theme = m.theme.withPalette(m.settings.Theme)
	}
	return m.persistSettings()
}

// handleSettingsMouse handles a mouse event while the dialog is open.
func (m Model) handleSettingsMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	// Motion comes first: it belongs to the open dropdown (a hovered option
	// is highlighted and nothing is picked), and anywhere else — inside the
	// dialog or outside it — it does nothing at all. The dialog is modal, so
	// a motion may never dismiss it or reach the panes behind it.
	if msg.Action == tea.MouseActionMotion {
		m.dlg.choice.hover = -1
		if m.dlg.choice.open {
			if l, ok := m.settingsChoiceLayout(); ok {
				if idx, ok := selectRowAt(l, m.dlg.choice.options, msg.X, msg.Y); ok {
					m.dlg.choice.hover = idx
				}
			}
		}
		return m, nil
	}
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}
	// The open dropdown is a layer of the dialog: a click on an option picks
	// it, a click anywhere else only closes the list — the same click must
	// never also toggle a value or dismiss the dialog behind the list.
	if m.dlg.choice.open {
		if l, ok := m.settingsChoiceLayout(); ok {
			if idx, ok := selectRowAt(l, m.dlg.choice.options, msg.X, msg.Y); ok {
				m.dlg.choice.sel = idx
				chosen := m.dlg.choice.Chosen()
				m.dlg.choice.Close()
				return m, m.applySettingChoice(chosen)
			}
		}
		m.dlg.choice.Close()
		return m, nil
	}
	l := m.dialogLayout()
	if !(Rect{X: l.x, Y: l.y, W: l.w, H: l.h}).Contains(msg.X, msg.Y) {
		// A click outside the box dismisses it (changes are already applied).
		m.closeSettings()
		return m, m.setMessage(m.t(i18n.KeyMsgSettingsSaved))
	}
	row, ok := m.dialogRowAt(msg.X, msg.Y)
	if !ok {
		return m, nil // the title, description or hint: swallowed
	}
	m.dlg.sel = row
	if _, ok := m.dialogValueAt(msg.X, msg.Y); ok {
		// The value column toggles a boolean and opens the dropdown of a
		// select row, exactly like the keyboard does.
		if settingHasSelect(settingRows[row].kind) {
			m.openSettingChoice()
			return m, nil
		}
		return m, m.changeSetting(1)
	}
	if settingRows[row].kind == rowResetLayout || settingRows[row].kind == rowVault {
		return m, m.changeSetting(1)
	}
	return m, nil
}

// changeSetting applies the row's change in the given direction and persists
// the result.
func (m *Model) changeSetting(delta int) tea.Cmd {
	if m.dlg.sel < 0 || m.dlg.sel >= len(settingRows) {
		return nil
	}
	switch settingRows[m.dlg.sel].kind {
	case rowLanguage:
		m.cycleLocale(delta)
		// Report the switch in the newly selected language.
		return tea.Batch(m.persistSettings(),
			m.setMessage(m.t(i18n.KeyMsgLanguageChanged, m.t(i18n.KeyLocaleName))))
	case rowTheme:
		if m.settings.Theme == settings.ThemeLight {
			m.settings.Theme = settings.ThemeDark
		} else {
			m.settings.Theme = settings.ThemeLight
		}
		m.theme = m.theme.withPalette(m.settings.Theme)
	case rowIcons:
		// Switching the icon set is a redraw: every glyph is looked up when it
		// is drawn, so nothing else has to be rebuilt.
		m.settings.Icons = !m.settings.Icons
	case rowMouse:
		m.settings.Mouse = !m.settings.Mouse
		return tea.Batch(m.persistSettings(), m.setMessage(m.t(mouseStateKey(m.settings.Mouse))))
	case rowGutter:
		m.settings.Gutter = !m.settings.Gutter
		m.ensureCursorVisible()
		return tea.Batch(m.persistSettings(), m.setMessage(m.t(gutterStateKey(m.settings.Gutter))))
	case rowReadOnly:
		m.settings.ReadOnly = !m.settings.ReadOnly
		return tea.Batch(m.persistSettings(), m.setMessage(m.t(readOnlyStateKey(m.settings.ReadOnly))))
	case rowVault:
		if delta <= 0 {
			return nil // an action, not a value: only forward/Enter runs it
		}
		// One dialog at a time: the passphrase form replaces the settings
		// dialog, which closed on the way out.
		m.closeSettings()
		m.openVaultEntry()
		return nil
	case rowResetLayout:
		if delta <= 0 {
			return nil // an action, not a value: only forward/Enter runs it
		}
		m.settings.Layout = settings.Default().Layout
		m.layout = layoutFromSettings(m.settings)
		m.ensureCursorVisible()
		return tea.Batch(m.persistSettings(), m.setMessage(m.t(i18n.KeyMsgLayoutReset)))
	}
	return m.persistSettings()
}

// mouseStateKey returns the message key describing the mouse state.
func mouseStateKey(on bool) i18n.Key {
	if on {
		return i18n.KeyMsgMouseEnabled
	}
	return i18n.KeyMsgMouseDisabled
}

// gutterStateKey returns the message key describing the line-number state.
func gutterStateKey(on bool) i18n.Key {
	if on {
		return i18n.KeyMsgGutterEnabled
	}
	return i18n.KeyMsgGutterDisabled
}

// cycleLocale steps through localeChoices and rebuilds the translator.
func (m *Model) cycleLocale(delta int) {
	cur := 0
	for i, c := range localeChoices {
		if c == m.settings.Locale {
			cur = i
			break
		}
	}
	n := len(localeChoices)
	m.settings.Locale = localeChoices[((cur+delta)%n+n)%n]
	m.applyLocale()
}

// applyLocale rebuilds the translator from the current settings.
func (m *Model) applyLocale() {
	loc := m.sysLocale
	if l, ok := i18n.Normalize(m.settings.Locale); ok {
		loc = l
	}
	m.translator = i18n.New(loc)
}

// ---- Rendering ----

// renderSettingsDialog renders the dialog and returns its screen origin plus the
// block of lines to blit.
func (m Model) renderSettingsDialog() (x, y int, block []string, ok bool) {
	if !m.dlg.open {
		return 0, 0, nil, false
	}
	l := m.dialogLayout()
	if l.w < 4 || l.h < 3 {
		return 0, 0, nil, false
	}
	border, box := m.theme.dlgBorder, m.theme.dlgBox
	rule := strings.Repeat("─", l.w-2)

	out := make([]string, 0, l.h)
	out = append(out, border.Render("┌"+rule+"┐"))
	title := padANSI(truncateANSI(" "+m.t(i18n.KeySettingsTitle), l.innerW), l.innerW)
	out = append(out, border.Render("│")+m.theme.dlgTitle.Render(title)+border.Render("│"))
	out = append(out, border.Render("│")+box.Render(strings.Repeat(" ", l.innerW))+border.Render("│"))

	for i := range settingRows {
		// renderSettingRow pads every column, so the row is already exactly
		// innerW columns wide and carries the right background itself.
		line := truncateANSI(m.renderSettingRow(i, l), l.innerW)
		out = append(out, border.Render("│")+line+border.Render("│"))
	}

	blank := border.Render("│") + box.Render(strings.Repeat(" ", l.innerW)) + border.Render("│")
	out = append(out, blank)

	// The description of the selected row sits on the bottom of the box: it
	// is one line instead of a second line per row, so the dialog keeps the
	// modal minimum height.
	descText := m.t(settingRows[clamp(m.dlg.sel, 0, len(settingRows)-1)].descKey)
	// The vault row describes the vault's *current* state, not a fixed value:
	// it is the one row whose meaning changes outside the dialog.
	if settingRows[clamp(m.dlg.sel, 0, len(settingRows)-1)].kind == rowVault {
		descText = m.vaultRowDesc()
	}
	desc := padANSI(truncateANSI(" "+descText, l.innerW), l.innerW)
	out = append(out, border.Render("│")+m.theme.dlgDesc.Render(desc)+border.Render("│"))

	// On a narrow screen the long hint would lose its Esc part to the
	// truncation, so a short hint takes over.
	hintKey := i18n.KeySettingsHint
	if lipgloss.Width(m.t(hintKey)) > l.innerW {
		hintKey = i18n.KeySettingsHintShort
	}
	hint := padANSI(truncateANSI(m.t(hintKey), l.innerW), l.innerW)
	out = append(out, border.Render("│")+m.theme.dlgBox.Render(hint)+border.Render("│"))

	out = append(out, border.Render("└"+rule+"┘"))

	// The open dropdown is drawn over the rows it covers, inside the frame
	// (overlayPopup does the same in-block blitting for the completion
	// popup). Its layout is shared with the mouse hit test, so a click always
	// lands on the option that is drawn there.
	if sl, dok := m.settingsChoiceLayout(); dok {
		for i, pl := range m.renderSelectList(sl, &m.dlg.choice) {
			row := sl.rect.Y - l.y + i // out[0] is the top border row
			if row < 0 || row >= len(out) {
				continue
			}
			line := out[row]
			lx := sl.rect.X - l.x
			prefix := truncateANSI(line, lx)
			suffix := cutLeftANSI(line, lx+lipgloss.Width(pl))
			out[row] = prefix + pl + suffix
		}
	}
	return l.x, l.y, out, true
}

// vaultRowDesc describes the saved-connection passphrase row by the vault's
// state: nothing to open yet, open, or locked. The three wordings are the
// status-bar keys, so the same fact always reads the same way.
func (m Model) vaultRowDesc() string {
	switch {
	case !m.settings.Encrypted():
		return m.t(i18n.KeyVaultPlaintextWarning)
	case m.settings.Locked():
		return m.t(i18n.KeyVaultLocked)
	}
	return m.t(i18n.KeyVaultUnlocked)
}

// renderSettingRow renders one row without its borders: a caret, the padded
// label, the padded value and a trailing space.
func (m Model) renderSettingRow(i int, l dialogLayout) string {
	selected := i == m.dlg.sel
	labelStyle, valueStyle, padStyle := m.theme.dlgLabel, m.theme.dlgValue, m.theme.dlgBox
	caret := " "
	if selected {
		labelStyle, valueStyle, padStyle = m.theme.dlgRowSel, m.theme.dlgValueSel, m.theme.dlgRowSel
		caret = "▸"
	}
	return padStyle.Render(caret) +
		labelStyle.Render(padANSI(m.t(settingRows[i].labelKey), l.labelW)) +
		padStyle.Render("  ") +
		valueStyle.Render(padANSI(m.settingValue(i), l.valueW)) +
		padStyle.Render(" ")
}
