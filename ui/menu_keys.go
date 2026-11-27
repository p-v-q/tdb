package ui

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// menu_keys.go turns keys into menu and toolbar actions. menuDefs supplies
// every global binding; keyAliases covers terminal encodings of the same key.

// ---- Keyboard ----

// handleMenuKey handles a key press while a menu is open and reports whether it
// consumed the key.
func (m Model) handleMenuKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	key := msg.String()
	switch key {
	case "esc":
		m.closeMenu()
		return m, nil, true
	case "left":
		m.openMenu((m.menu.open - 1 + len(menuDefs)) % len(menuDefs))
		return m, nil, true
	case "right":
		m.openMenu((m.menu.open + 1) % len(menuDefs))
		return m, nil, true
	case "up":
		if n := len(m.menuItems(menuDefs[m.menu.open])); n > 0 {
			m.menu.sel = (m.menu.sel - 1 + n) % n
		}
		return m, nil, true
	case "down":
		if n := len(m.menuItems(menuDefs[m.menu.open])); n > 0 {
			m.menu.sel = (m.menu.sel + 1) % n
		}
		return m, nil, true
	case "enter":
		items := m.menuItems(menuDefs[m.menu.open])
		if m.menu.sel < 0 || m.menu.sel >= len(items) {
			m.closeMenu()
			return m, nil, true
		}
		action := items[m.menu.sel].action
		m.closeMenu()
		return m, m.runMenuAction(action), true
	}
	// Alt+letter switches to another menu, just like clicking its title.
	if idx, ok := menuAccelIndex(key); ok {
		if idx == m.menu.open {
			m.closeMenu()
		} else {
			m.openMenu(idx)
		}
		return m, nil, true
	}
	// A plain letter activates the item carrying it as accelerator.
	if r, ok := singleLetter(key); ok {
		for _, it := range m.menuItems(menuDefs[m.menu.open]) {
			if it.accel == r {
				m.closeMenu()
				return m, m.runMenuAction(it.action), true
			}
		}
	}
	// A global shortcut (Alt+1…3, Ctrl+S, F5, …) keeps working while the menu
	// is open: it closes the menu and runs, instead of only dismissing it.
	// It must come after the Alt+letter menu switch and the plain-letter
	// mnemonics, so those keep the keys they own.
	if a, ok := keymapAction(key); ok {
		m.closeMenu()
		return m, m.runMenuAction(a), true
	}
	// Anything else closes the menu. Printable input is consumed so the letter
	// that dismissed the menu is not typed into the document; key combinations
	// (Ctrl+S and friends) fall through so global shortcuts keep working.
	m.closeMenu()
	if len(msg.Runes) > 0 {
		return m, nil, true
	}
	return m, nil, false
}

// menuAccelIndex returns the menu index opened by an "alt+<letter>" key.
func menuAccelIndex(key string) (int, bool) {
	if !strings.HasPrefix(key, "alt+") || len(key) != len("alt+x") {
		return 0, false
	}
	r := rune(key[len("alt+")])
	for i, def := range menuDefs {
		if def.accel == r {
			return i, true
		}
	}
	return 0, false
}

// shortcutOf returns the global shortcut bound to an action ("" when it has
// none), which is what the status bar shows next to a toolbar button.
func shortcutOf(a menuAction) string {
	for _, def := range menuDefs {
		for _, it := range def.items {
			if it.action == a {
				return it.shortcut
			}
		}
	}
	return ""
}

// keyAliases maps keys that some terminals send instead of the advertised
// binding. Ctrl+Enter often arrives as Ctrl+J, and Ctrl+/ as Ctrl+_.
var keyAliases = map[string]string{
	"ctrl+j": "ctrl+enter",
	"ctrl+_": "ctrl+/",
}

// keymapAction returns the action bound to a global key binding, using the menu
// table as the key map.
func keymapAction(key string) (menuAction, bool) {
	if canonical, ok := keyAliases[key]; ok {
		key = canonical
	}
	for _, def := range menuDefs {
		for _, it := range def.items {
			if it.shortcut == key || (it.shortcut2 != "" && it.shortcut2 == key) {
				return it.action, true
			}
		}
	}
	return actNone, false
}

// displayItemShortcuts renders the primary and alternate binding of a menu
// item, e.g. "F5 / ^Enter" for Run.
func displayItemShortcuts(it menuItem) string {
	if it.shortcut2 == "" {
		return displayShortcut(it.shortcut)
	}
	if it.shortcut == "" {
		return displayShortcut(it.shortcut2)
	}
	return displayShortcut(it.shortcut) + " / " + displayShortcut(it.shortcut2)
}

// singleLetter returns the rune of a one-letter key press.
func singleLetter(key string) (rune, bool) {
	if len(key) != 1 {
		return 0, false
	}
	r := rune(key[0])
	if r >= 'a' && r <= 'z' {
		return r, true
	}
	return 0, false
}

// keyDisplay is how named keys appear in the shortcut column of a menu.
var keyDisplay = map[string]string{
	"pgup":   "PgUp",
	"pgdown": "PgDn",
	"home":   "Home",
	"end":    "End",
	"enter":  "Enter",
	"space":  "Space",
	"tab":    "Tab",
	"esc":    "Esc",
}

// displayShortcut renders a key binding the way menu items show it: ctrl+s
// becomes ^S, ctrl+pgdown becomes ^PgDn and f5 becomes F5.
func displayShortcut(s string) string {
	if s == "" {
		return ""
	}
	name := func(k string) string {
		if v, ok := keyDisplay[k]; ok {
			return v
		}
		return strings.ToUpper(k)
	}
	switch {
	case strings.HasPrefix(s, "ctrl+"):
		return "^" + name(strings.TrimPrefix(s, "ctrl+"))
	case strings.HasPrefix(s, "alt+"):
		return "Alt+" + name(strings.TrimPrefix(s, "alt+"))
	}
	return name(s)
}

// labelWithMnemonic renders a menu label with the first letter of its
// accelerator underlined, the way desktop menus mark their mnemonics. Localized
// labels may not contain the accelerator letter at all (Chinese labels do not),
// in which case they are left alone.
func labelWithMnemonic(label string, accel rune, style lipgloss.Style) string {
	runes := []rune(label)
	for i, r := range runes {
		if unicode.ToLower(r) == accel {
			return style.Render(string(runes[:i])) +
				style.Underline(true).Render(string(runes[i])) +
				style.Render(string(runes[i+1:]))
		}
	}
	return style.Render(label)
}
