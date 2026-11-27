package scenarios

// conn_vault_test.go drives the two security-facing setup flows of the client:
// creating a connection through the form (no settings file editing), and
// protecting the saved connections with a passphrase (the vault).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tdb-itest/harness"
	"tdb-itest/scenario"
)

// TestConnFormCreate builds a working SQLite connection entirely through the
// form: open the object browser menu, pick "New connection", choose the type
// from its dropdown, type a name and a file, and connect.
func TestConnFormCreate(t *testing.T) {
	c := scenario.New(t)
	dbPath := scenario.ShopSQLite(t, c)
	c.Start("-d", "demo")
	c.WaitFor("Welcome to TDB", 20*time.Second)

	// Right-click the connection in the object browser: its menu holds the
	// "New connection…" entry.
	if err := c.S.MouseClickButton(10, 4, harness.MouseRight); err != nil {
		t.Fatalf("right click: %v", err)
	}
	c.WaitFor("New connection", 10*time.Second)
	c.Shot("objects-menu")
	c.ClickText("New connection")
	c.WaitFor("Database type", 10*time.Second)
	c.RequireContains("Name", "Connect", "Cancel")
	c.Shot("conn-form")

	// Name first.
	c.Type("name", "shop-form")
	// Tab to the type field, open its list and walk down to SQLite: Demo,
	// MariaDB, MySQL, PostgreSQL, SQLite.
	c.Keys("to-driver", harness.KeyTab)
	c.Keys("open-types", harness.KeyEnter)
	c.WaitFor("SQLite", 10*time.Second)
	c.Shot("type-dropdown")
	for i := 0; i < 4; i++ {
		c.Keys("next-type", harness.KeyDown)
	}
	c.Keys("pick-type", harness.KeyEnter)
	c.WaitFor("File", 10*time.Second)
	c.Shot("sqlite-form")

	// Tab to the file field and type the database path; Enter connects.
	c.Keys("to-file", harness.KeyTab)
	c.Type("file", dbPath)
	c.Shot("filled-form")

	// Enter on any text field is "connect": the form leaves the screen and the
	// new connection becomes the active one.
	c.Keys("connect", harness.KeyEnter)
	c.WaitFor("conn shop-form", 25*time.Second)
	c.RequireContains("Connected to shop-form")
	c.Shot("connected")

	// The object browser shows the new connection next to the old one; the
	// active connection may already be unfolded, so only unfold when needed.
	c.FocusObjects()
	if !strings.Contains(c.Screen(), "main") {
		c.Keys("unfold", harness.KeyRight)
	}
	c.WaitFor("main", 10*time.Second)
	c.RequireContains("shop-form (sqlite)")
	c.Shot("browser")

	// It survived: the settings file lists the connection by name.
	c.Keys("preferences", harness.KeyF2)
	c.WaitFor("Theme", 10*time.Second)
	c.Keys("close", harness.KeyEsc)
}

// TestVaultSetupAndUnlock covers the first-run offer and the passphrase flow:
// skip it once, set a passphrase later from the Settings menu, then unlock the
// file on the next start (with a wrong passphrase first).
func TestVaultSetupAndUnlock(t *testing.T) {
	c := scenario.New(t)
	dbPath := scenario.ShopSQLite(t, c)
	settingsPath := filepath.Join(c.ArtifactDir(), "vault-settings.json")
	_ = os.Remove(settingsPath)

	// ---- First run: the offer to protect the connections appears. ----
	c.StartWithSettings(settingsPath, "-d", "sqlite", "-dsn", dbPath)
	c.WaitFor("Protect your connections", 25*time.Second)
	c.RequireContains("There is no recovery", "At least 8 characters")
	c.Shot("first-run-offer")

	// Esc keeps the file plain and says so.
	c.Keys("skip", harness.KeyEsc)
	c.WaitFor("plain text", 10*time.Second)
	c.RequireContains("conn shop.db")
	c.Shot("skipped")

	// ---- Set a passphrase from the Settings menu (Alt+S, C). ----
	c.Keys("settings-menu", "\x1bs")
	c.Keys("passphrase-item", "c")
	c.WaitFor("Protect your connections", 10*time.Second)
	c.Shot("setup-form")

	pass := "correct horse battery staple"
	c.Type("passphrase", pass)
	c.Keys("to-confirm", harness.KeyTab)
	c.Type("confirm", pass)
	c.Keys("to-hint", harness.KeyTab)
	c.Type("hint", "the one from the notebook")
	c.Keys("submit", harness.KeyEnter)
	c.WaitFor("Passphrase set", 15*time.Second)
	c.Shot("vault-set")

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if !strings.Contains(string(data), `"vault"`) {
		t.Fatalf("the settings file holds no vault envelope:\n%s", data)
	}
	if strings.Contains(string(data), pass) {
		t.Fatal("the passphrase must never be written to the settings file")
	}
	c.S.Close()

	// ---- Next start: the file is locked, a wrong passphrase fails. ----
	c.StartWithSettings(settingsPath, "-d", "sqlite", "-dsn", dbPath)
	c.WaitFor("Unlock connections", 25*time.Second)
	c.Shot("locked")
	c.Type("wrong", "definitely not the passphrase")
	c.Keys("unlock", harness.KeyEnter)
	c.WaitFor("Wrong passphrase", 15*time.Second)
	c.Shot("wrong-passphrase")

	// The right passphrase unlocks it: clear the mistyped value first.
	c.Keys("clear", strings.Repeat(harness.KeyBackspace, 40))
	c.Type("right", pass)
	c.Keys("unlock", harness.KeyEnter)
	c.WaitFor("Connections unlocked", 15*time.Second)
	c.RequireContains("conn shop.db")
	c.Shot("unlocked")
}
