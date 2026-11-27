package ui

// menu_defs.go is the declarative command table. It is the single source of
// truth for menu labels, accelerators, global shortcuts and actions.

import "github.com/p-v-q/tdb/i18n"

// menuAction is the action a menu item triggers.
type menuAction int

// The available actions.
const (
	actNone menuAction = iota
	actNewTab
	actOpenFile // open the file picker and read the chosen SQL file
	actCloseTab
	actNextTab
	actPrevTab
	actSave
	actQuit
	actUndo
	actRedo
	actSelectAll
	actFind
	actHistory
	actCopy
	actCut
	actToggleComment
	actToggleObjects
	actToggleResults
	actClosePane
	actFocusObjects
	actFocusEditor
	actFocusResults
	actRun
	actExplain
	actClearResults
	actExport
	actFormat
	actPreferences
	actOpenConfig
	actAbout
	actKeymap
	actToggleReadOnly
	actToggleManualCommit
	actCommit
	actRollback
	actApplyChanges
	actDiscardChanges
	actVaultMenu    // open the passphrase dialog for the current vault state
	actVaultLockNow // seal the unlocked vault in memory
)

// menuItem is one dropdown entry.
type menuItem struct {
	labelKey  i18n.Key
	accel     rune   // accelerator: a plain letter while the menu is open
	shortcut  string // primary global key binding ("" when the item has none)
	shortcut2 string // additional global key binding, shown after a slash
	action    menuAction
	// needsTx hides the item when the connected database does not implement
	// driver.TransactionManager: a connection that cannot commit must not
	// show transaction controls that could never work (the object browser
	// discovers DefinitionProvider the same way). Such items must come last
	// in their menu, so the visible list is always a prefix of def.items.
	needsTx bool
	// needsUnlock hides the item while the vault is locked (or missing):
	// locking an already-locked vault is not an action anyone can take, so
	// the menu does not advertise it. Same suffix rule as needsTx.
	needsUnlock bool
}

// menuDef is one top-level menu.
type menuDef struct {
	titleKey i18n.Key
	accel    rune // Alt+accel opens this menu
	items    []menuItem
}

// menuDefs is the menu bar definition; the order is the render order. It is the
// single source of truth for both the displayed shortcuts and the key map:
// accel is the letter pressed while the menu is open (underlined in the
// dropdown), shortcut is the global binding shown on the right.
var menuDefs = []menuDef{
	{
		// The application menu comes first, as on macOS: the app name itself is
		// a menu (About, Preferences, Quit). It has no Alt+letter because the
		// name is a brand, not a label to translate.
		titleKey: i18n.KeyAppName,
		items: []menuItem{
			{labelKey: i18n.KeyAppAbout, accel: 'a', action: actAbout},
			{labelKey: i18n.KeyMenuPreferences, accel: 'p', action: actPreferences},
			{labelKey: i18n.KeyAppQuit, accel: 'q', action: actQuit},
		},
	},
	{
		titleKey: i18n.KeyMenuFile,
		accel:    'f',
		items: []menuItem{
			{labelKey: i18n.KeyMenuNewTab, accel: 'n', shortcut: "ctrl+t", action: actNewTab},
			{labelKey: i18n.KeyMenuOpenFile, accel: 'o', shortcut: "ctrl+o", action: actOpenFile},
			{labelKey: i18n.KeyMenuCloseTab, accel: 'c', shortcut: "ctrl+w", action: actCloseTab},
			{labelKey: i18n.KeyMenuSave, accel: 's', shortcut: "ctrl+s", action: actSave},
			{labelKey: i18n.KeyMenuQuit, accel: 'q', shortcut: "ctrl+q", action: actQuit},
		},
	},
	{
		titleKey: i18n.KeyMenuEdit,
		accel:    'e',
		items: []menuItem{
			{labelKey: i18n.KeyMenuUndo, accel: 'u', shortcut: "ctrl+z", action: actUndo},
			{labelKey: i18n.KeyMenuRedo, accel: 'r', shortcut: "ctrl+y", action: actRedo},
			{labelKey: i18n.KeyMenuSelectAll, accel: 'a', shortcut: "ctrl+a", action: actSelectAll},
			{labelKey: i18n.KeyMenuFind, accel: 'n', shortcut: "ctrl+f", action: actFind},
			{labelKey: i18n.KeyMenuCopy, accel: 'c', shortcut: "ctrl+c", action: actCopy},
			{labelKey: i18n.KeyMenuCut, accel: 'x', shortcut: "ctrl+x", action: actCut},
			{labelKey: i18n.KeyMenuToggleComment, accel: 't', shortcut: "ctrl+/", action: actToggleComment},
			{labelKey: i18n.KeyToolFormat, accel: 'f', action: actFormat},
		},
	},
	{
		titleKey: i18n.KeyMenuView,
		accel:    'v',
		items: []menuItem{
			{labelKey: i18n.KeyMenuPaneObjects, accel: 'o', shortcut: "ctrl+b", action: actToggleObjects},
			{labelKey: i18n.KeyMenuPaneResults, accel: 'r', shortcut: "ctrl+e", action: actToggleResults},
			// Focus items sit next to the pane toggles they complement: they
			// reveal a hidden pane and then hand it the keyboard focus.
			{labelKey: i18n.KeyMenuFocusObjects, accel: 'f', shortcut: "alt+1", action: actFocusObjects},
			{labelKey: i18n.KeyMenuFocusEditor, accel: 'e', shortcut: "alt+2", action: actFocusEditor},
			{labelKey: i18n.KeyMenuFocusResults, accel: 't', shortcut: "alt+3", action: actFocusResults},
			{labelKey: i18n.KeyMenuNextTab, accel: 'n', shortcut: "ctrl+pgdown", action: actNextTab},
			{labelKey: i18n.KeyMenuPrevTab, accel: 'p', shortcut: "ctrl+pgup", action: actPrevTab},
			{labelKey: i18n.KeyMenuClosePane, accel: 'c', action: actClosePane},
		},
	},
	{
		titleKey: i18n.KeyMenuQuery,
		accel:    'q',
		items: []menuItem{
			{labelKey: i18n.KeyMenuRun, accel: 'r', shortcut: "f5", shortcut2: "ctrl+enter", action: actRun},
			{labelKey: i18n.KeyMenuExplain, accel: 'e', shortcut: "f6", action: actExplain},
			{labelKey: i18n.KeyMenuHistory, accel: 'h', shortcut: "ctrl+h", action: actHistory},
			{labelKey: i18n.KeyMenuClearResults, accel: 'c', action: actClearResults},
			{labelKey: i18n.KeyMenuExport, accel: 'x', shortcut: "f7", action: actExport},
			// The grid edit has no global shortcut: Ctrl+S is the save of
			// the file, and it applies the pending changes only while the
			// result grid has the focus.
			{labelKey: i18n.KeyResultApplyChanges, accel: 'a', action: actApplyChanges},
			{labelKey: i18n.KeyResultDiscardChanges, accel: 'd', action: actDiscardChanges},
			{labelKey: i18n.KeyMenuReadOnly, accel: 'o', action: actToggleReadOnly},
			// The transaction block ends the menu (needsTx items must be a
			// suffix), so hiding it keeps every other item's index stable.
			{labelKey: i18n.KeyMenuManualCommit, accel: 'm', action: actToggleManualCommit, needsTx: true},
			{labelKey: i18n.KeyMenuCommit, accel: 't', shortcut: "f9", action: actCommit, needsTx: true},
			{labelKey: i18n.KeyMenuRollback, accel: 'b', shortcut: "f11", action: actRollback, needsTx: true},
		},
	},
	{
		titleKey: i18n.KeyMenuSettings,
		accel:    's',
		items: []menuItem{
			{labelKey: i18n.KeyMenuPreferences, accel: 'p', shortcut: "f2", action: actPreferences},
			{labelKey: i18n.KeyMenuOpenConfigFile, accel: 's', action: actOpenConfig},
			// The passphrase entry routes by vault state: setup on a plain
			// file, unlock when locked, the manager once open.
			{labelKey: i18n.KeyVaultMenu, accel: 'c', action: actVaultMenu},
			// Locking is only meaningful while the vault is unlocked, so the
			// item hides with the vault (needsUnlock items stay last).
			{labelKey: i18n.KeyVaultLockNow, accel: 'l', action: actVaultLockNow, needsUnlock: true},
		},
	},
	{
		titleKey: i18n.KeyMenuHelp,
		accel:    'h',
		items: []menuItem{
			{labelKey: i18n.KeyMenuKeymap, accel: 'k', shortcut: "f1", action: actKeymap},
		},
	},
}

// appMenuIndex is the position of the application menu: on macOS the menu bar
// starts with the app name, and so does tdb's.
const appMenuIndex = 0
