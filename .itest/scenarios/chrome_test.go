package scenarios

// chrome_test.go exercises the application chrome a user lives in: the menu
// bar, the toolbar, the preferences/keymap/about dialogs, the language switch,
// read-only mode, and the welcome page. Everything is driven through the real
// binary in a real terminal.

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"tdb-itest/harness"
	"tdb-itest/scenario"
)

// startWithSettings starts TDB with a settings file the test can inspect
// afterwards.
func startWithSettings(t *testing.T, name string, args ...string) (*scenario.Ctx, string) {
	t.Helper()
	c := scenario.New(t)
	settings := c.Env.WriteSettings(t, name, c.Env.StandardConnections()...)
	c.StartWithSettings(settings, args...)
	return c, settings
}

// readSettings returns the settings file as a generic map.
func readSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse settings: %v\n%s", err, data)
	}
	return doc
}

// TestChromeMenuBar walks the menu bar with the keyboard: F10 opens it, the
// arrows move between menus, and Esc closes it again.
func TestChromeMenuBar(t *testing.T) {
	c, _ := startWithSettings(t, "main", "-d", "demo")
	c.WaitFor("Welcome to TDB", 20*time.Second)

	c.Keys("open-menu", harness.KeyF10)
	c.RequireContains("About TDB", "Preferences", "Quit")
	c.Shot("app-menu")

	c.Keys("next-menu-file", harness.KeyRight)
	c.RequireContains("New Tab", "Open SQL File", "Close Tab", "Save")
	c.Shot("file-menu")

	c.Keys("next-menu-edit", harness.KeyRight)
	c.RequireContains("Undo", "Redo", "Select All", "Find", "Copy", "Cut", "Format")
	c.Shot("edit-menu")

	c.Keys("next-menu-view", harness.KeyRight)
	c.RequireContains("Objects", "Results", "Focus Editor", "Next Tab")
	c.Shot("view-menu")

	c.Keys("close-menu", harness.KeyEsc)
	c.RequireNotContains("Focus Editor")
	c.Shot("closed")

	// The Help menu through its Alt accelerator, then the key-bindings page.
	c.Menu('h', 'k')
	c.WaitFor("Key Bindings", 10*time.Second)
	c.RequireContains("Object browser", "Editor", "Esc")
	c.Shot("keybindings")
	c.Keys("close-keybindings", harness.KeyEsc)
	c.WaitGone("Key Bindings", 10*time.Second)
}

// TestChromeToolbar drives the everyday toolbar buttons with the mouse: Run
// executes the editor content, Settings opens the dialog, Help opens the
// keymap, and every click is a real pointer event on the drawn button.
func TestChromeToolbar(t *testing.T) {
	c := scenario.New(t)
	dbPath := scenario.ShopSQLite(t, c)
	settings := c.Env.WriteSettings(t, "main", c.Env.StandardConnections()...)
	c.StartWithSettings(settings, "-d", "sqlite", "-dsn", dbPath)
	c.WaitFor("conn shop.db", 25*time.Second)

	c.Keys("select-all", harness.KeyCtrlA)
	c.Type("typed-sql", "SELECT 42 AS answer;")
	// The toolbar is the second screen row: the menu bar labels repeat there.
	c.ClickTextOnRow("Run", 1)
	c.WaitFor("answer", 20*time.Second)
	c.RequireContains("42", "1 row in")
	c.Shot("toolbar-run")

	c.ClickTextOnRow("Settings", 1)
	c.WaitFor("Theme", 10*time.Second)
	c.RequireContains("Language", "Mouse", "Line numbers", "Read-only mode")
	c.Shot("toolbar-settings")
	c.Keys("close", harness.KeyEsc)

	c.ClickTextOnRow("Help", 1)
	c.WaitFor("Key Bindings", 10*time.Second)
	c.Shot("toolbar-help")
	c.Keys("close", harness.KeyEsc)
}

// TestChromePreferencesTheme flips the theme in the Preferences dialog and
// verifies the change reached the settings file.
func TestChromePreferencesTheme(t *testing.T) {
	c, settings := startWithSettings(t, "main", "-d", "demo")
	c.WaitFor("Welcome to TDB", 20*time.Second)

	c.Keys("preferences", harness.KeyF2)
	c.WaitFor("Theme", 10*time.Second)
	c.Shot("settings-dialog")

	// Down on a select row opens its list, so Tab moves the selection instead.
	c.Keys("to-theme", harness.KeyTab)
	c.Keys("open-theme", harness.KeyEnter)
	c.WaitFor("Light", 10*time.Second)
	c.Shot("theme-dropdown")
	c.Keys("pick-light", harness.KeyDown)
	c.Keys("apply", harness.KeyEnter)
	c.Keys("close", harness.KeyEsc)

	doc := readSettings(t, settings)
	if got, _ := doc["theme"].(string); got != "light" {
		t.Fatalf("settings theme = %q, want light\n%v", got, doc)
	}
	c.Shot("light-theme")
}

// TestChromeSettingsLanguageSwitch switches the UI to Simplified Chinese from
// the Preferences dialog and checks both the rendered strings and the saved
// preference.
func TestChromeSettingsLanguageSwitch(t *testing.T) {
	c, settings := startWithSettings(t, "main", "-d", "demo")
	c.WaitFor("Welcome to TDB", 20*time.Second)

	c.Keys("preferences", harness.KeyF2)
	c.WaitFor("Theme", 10*time.Second)
	c.Keys("home", harness.KeyHome)
	// Down on the language row opens its list; Down again picks Chinese.
	c.Keys("open-language", harness.KeyDown)
	c.WaitFor("English", 10*time.Second)
	c.Shot("language-dropdown")
	c.Keys("pick-chinese", harness.KeyDown)
	c.Keys("apply", harness.KeyEnter)
	c.WaitFor("语言", 10*time.Second) // the dialog is already localized
	c.Shot("chinese-dialog")
	c.Keys("close", harness.KeyEsc)

	c.RequireContains("文件", "查询", "设置")
	c.Shot("chinese-ui")

	doc := readSettings(t, settings)
	if got, _ := doc["locale"].(string); got != "zh-Hans" {
		t.Fatalf("settings locale = %q, want zh-Hans\n%v", got, doc)
	}
}

// TestChromeReadOnlyToggle turns read-only mode on from the Preferences dialog
// and proves a write statement never reaches the database.
func TestChromeReadOnlyToggle(t *testing.T) {
	c := scenario.New(t)
	dbPath := scenario.ShopSQLite(t, c)
	settings := c.Env.WriteSettings(t, "main", c.Env.StandardConnections()...)
	c.StartWithSettings(settings, "-d", "sqlite", "-dsn", dbPath)
	c.WaitFor("conn shop.db", 25*time.Second)

	toggleReadOnly := func() {
		c.Keys("preferences", harness.KeyF2)
		c.WaitFor("Theme", 10*time.Second)
		c.Keys("home", harness.KeyHome)
		for i := 0; i < 5; i++ {
			c.Keys("tab", harness.KeyTab)
		}
		c.Keys("toggle", harness.KeyEnter)
		c.Keys("close", harness.KeyEsc)
	}

	toggleReadOnly()
	c.Shot("readonly-on")
	doc := readSettings(t, settings)
	if got, _ := doc["readOnly"].(bool); !got {
		t.Fatalf("settings readOnly = %v, want true\n%v", doc["readOnly"], doc)
	}

	// A write statement must be blocked before it reaches the database.
	runSQL(c, "DELETE FROM users;")
	c.WaitFor("Read-only", 20*time.Second)
	c.Shot("blocked")
	if got := scenario.SQLiteCount(t, dbPath, "SELECT COUNT(*) FROM users"); got != 5 {
		t.Fatalf("users count = %d after a blocked DELETE, want 5", got)
	}

	// Turning it off again lets writes through.
	toggleReadOnly()
	runSQL(c, "DELETE FROM orders WHERE status = 'refunded';")
	c.WaitFor("row affected", 20*time.Second)
	if got := scenario.SQLiteCount(t, dbPath, "SELECT COUNT(*) FROM orders WHERE status='refunded'"); got != 0 {
		t.Fatalf("refunded orders = %d after the DELETE, want 0", got)
	}
	c.Shot("write-through")
}

// TestChromeAboutAndQuit opens the About dialog through the application menu
// and quits through the File menu, like a keyboard user would.
func TestChromeAboutAndQuit(t *testing.T) {
	c, _ := startWithSettings(t, "main", "-d", "demo")
	c.WaitFor("Welcome to TDB", 20*time.Second)

	c.ClickText("TDB")
	c.RequireContains("About TDB", "Preferences", "Quit")
	c.Shot("app-menu-mouse")
	c.Keys("about", "a")
	c.WaitFor("terminal database client", 10*time.Second)
	c.Shot("about-dialog")
	c.Keys("close-about", harness.KeyEsc)

	c.Menu('f', 'q')
	if err := c.S.WaitExit(10 * time.Second); err != nil {
		c.Fail("the app did not quit from File -> Quit: %v", err)
	}
}

// TestChromeWelcomeTyping starts on the welcome page and simply types: the
// page turns into an editor tab and the typed statement runs.
func TestChromeWelcomeTyping(t *testing.T) {
	c := scenario.New(t)
	dbPath := scenario.ShopSQLite(t, c)
	settings := c.Env.WriteSettings(t, "main", c.Env.StandardConnections()...)
	c.StartWithSettings(settings, "-d", "sqlite", "-dsn", dbPath)
	c.WaitFor("conn shop.db", 25*time.Second)
	c.WaitFor("Welcome to TDB", 15*time.Second)

	c.Type("typing", "SELECT 42 AS answer;")
	c.RequireContains("SELECT 42 AS answer;")
	c.Keys("run", harness.KeyF5)
	c.WaitFor("answer", 20*time.Second)
	c.RequireContains("42", "1 row in")
	c.Shot("welcome-ran")
}

// TestChromeMenuShortcutsRun checks the accelerators that live on the menu
// bar: Alt+Q opens Query and R runs the statement.
func TestChromeMenuShortcutsRun(t *testing.T) {
	c, _ := startWithSettings(t, "main", "-d", "demo")
	c.WaitFor("Welcome to TDB", 20*time.Second)

	c.Keys("new-query", "n") // the welcome page's own accelerator
	c.Keys("select-all", harness.KeyCtrlA)
	c.Type("typed-sql", "SELECT 'run via menu' AS who;")
	c.Menu('q', 'r')
	c.WaitFor("run via menu", 20*time.Second)
	c.Shot("ran-via-menu")

	// The Edit menu's Format item uppercases the keywords.
	c.Keys("select-all", harness.KeyCtrlA)
	c.Type("messy", "select 1+1 as two;")
	c.Keys("select-all", harness.KeyCtrlA)
	c.Menu('e', 'f')
	c.Shot("formatted")
	if screen := c.Screen(); !strings.Contains(screen, "SELECT") {
		c.Fail("format did not uppercase keywords:\n%s", screen)
	}
}
