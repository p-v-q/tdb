// tdb is a terminal database client in the spirit of DBeaver or DataGrip.
//
// Usage:
//
//	tdb [flags] [file.sql]
//
// Examples:
//
//	tdb                       # built-in demo connection (offline sample metadata)
//	tdb query.sql             # open a file; Ctrl+S saves it
//	tdb -lang zh-Hans         # force the Simplified Chinese UI
//	tdb -d postgres -dsn ...  # connect to a PostgreSQL database
//
// Preferences live in a JSON settings file (see the settings package) that is
// read at startup and rewritten whenever the user changes something.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/driver"

	// The binary is the composition root: it links the drivers it wants to
	// offer. Adding one is a blank import, nothing else.
	_ "github.com/p-v-q/tdb/driver/demo"
	_ "github.com/p-v-q/tdb/driver/mysql"
	_ "github.com/p-v-q/tdb/driver/postgres"
	_ "github.com/p-v-q/tdb/driver/sqlite"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
	"github.com/p-v-q/tdb/ui"
)

const version = "0.9.2"

func main() {
	// The language and the settings path are needed before flags are parsed so
	// that even `-h` and early errors come out in the right language.
	langArg, settingsArg := preScanArgs(os.Args[1:])
	settingsPath := settingsArg
	if settingsPath == "" {
		if p, err := settings.Path(); err == nil {
			settingsPath = p
		}
	}
	prefs, loadErr := settings.Load(settingsPath)
	if langArg != "" {
		prefs.Locale = langArg
	}
	tr := i18n.New(resolveLocale(prefs.Locale))

	var (
		driverName    = flag.String("d", "demo", tr.T(i18n.KeyCLIDriver))
		dsn           = flag.String("dsn", "", tr.T(i18n.KeyCLIDSN))
		showVer       = flag.Bool("version", false, tr.T(i18n.KeyCLIVersion))
		listDrv       = flag.Bool("drivers", false, tr.T(i18n.KeyCLIDrivers))
		settingsFlag  = flag.String("settings", settingsPath, tr.T(i18n.KeyCLISettings, settingsPath))
		langFlag      = flag.String("lang", string(prefs.Locale), tr.T(i18n.KeyCLILang))
		printSettings = flag.Bool("print-settings", false, tr.T(i18n.KeyCLIPrintSettings))
	)
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, tr.T(i18n.KeyCLIUsage))
		fmt.Fprintln(os.Stderr, "\nFlags:")
		flag.PrintDefaults()
	}
	flag.Parse()

	// The pre-scan already honoured -lang and -settings; re-resolve in case the
	// canonical flag values differ.
	prefs, _ = settings.Load(*settingsFlag)
	if *langFlag != "" {
		prefs.Locale = *langFlag
	} else if langArg != "" {
		prefs.Locale = langArg
	}
	tr = i18n.New(resolveLocale(prefs.Locale))
	settingsPath = *settingsFlag

	// A settings file that did not exist means this is the first run: the UI
	// offers to protect the connections right away (Esc keeps them plain).
	firstRun := isFirstRun(settingsPath)

	// A corrupt settings file falls back to the defaults, but that must not be
	// silent: the early commands (-print-settings, -drivers) would otherwise
	// show the defaults as if they were the file's values.
	if loadErr != nil {
		fmt.Fprintf(os.Stderr, "tdb: %s\n", tr.T(i18n.KeyCLISettingsFailed, loadErr))
	}

	switch {
	case *showVer:
		fmt.Printf("tdb %s\n", version)
		return
	case *printSettings:
		text, err := prefs.JSON()
		if err != nil {
			fatalf(tr.T(i18n.KeyCLIRunFailed, err))
		}
		fmt.Print(text)
		return
	case *listDrv:
		printDrivers(tr)
		return
	}

	ctx := context.Background()
	conn, err := driver.Open(ctx, *driverName, driver.Config{DSN: *dsn})
	if err != nil {
		if errors.Is(err, driver.ErrNotImplemented) {
			fatalf(tr.T(i18n.KeyCLINotImpl, err))
		}
		fatalf(tr.T(i18n.KeyCLIConnFailed, err))
	}
	defer conn.Close()

	fileName := ""
	text := starterTemplate
	if *driverName != "demo" {
		// The starter template talks about the demo catalog; a real database
		// starts with a statement the user can run as is.
		text = "SELECT 1;\n"
	}
	if flag.NArg() > 0 {
		fileName = flag.Arg(0)
		content, err := os.ReadFile(fileName)
		switch {
		case err == nil:
			text = string(content)
		case errors.Is(err, os.ErrNotExist):
			// A brand new file: start from the template.
		default:
			fatalf(tr.T(i18n.KeyCLIReadFailed, err))
		}
	}

	// Load the metadata synchronously: the demo and local databases are fast,
	// and remote connections can move to background refresh later.
	cat, err := conn.Catalog(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tdb: %s\n", tr.T(i18n.KeyCLIWarningMeta, err))
		cat = nil
	}

	if err := ui.Run(ui.Config{
		Title:          "tdb",
		ConnName:       conn.Name(),
		FileName:       fileName,
		Dialect:        conn.Dialect(),
		Provider:       conn,
		Conn:           conn,
		InitialCatalog: cat,
		Text:           text,
		FirstRun:       firstRun,
		Translator:     tr,
		Settings:       prefs,
		SettingsPath:   settingsPath,
	}); err != nil {
		fatalf(tr.T(i18n.KeyCLIRunFailed, err))
	}
}

// isFirstRun reports whether the startup should offer the first-run vault
// setup: a settings path exists, but the file at it does not. An empty path
// — a shell with neither HOME nor a settings flag — is never a first run:
// persistSettings skips the write when there is no path, so the offer would
// let the user set a passphrase that never reaches any file, silently.
//
// A missing settings file is a first run when it *could* be created. On a
// genuinely fresh install the whole configuration directory is missing too
// (Windows: %AppData%\tdb, Unix: ~/.config/tdb), so the parent must not
// be required to exist: settings.Save creates it (0700) when the first
// preference is written. The nearest ancestor that exists has to be a
// directory — Windows reports a path that runs through a regular file as
// "not found" (POSIX says ENOTDIR), and such a file can never appear.
func isFirstRun(settingsPath string) bool {
	if settingsPath == "" {
		return false
	}
	if _, err := os.Stat(settingsPath); !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	dir := filepath.Dir(settingsPath)
	for {
		info, err := os.Stat(dir)
		switch {
		case err == nil:
			return info.IsDir()
		case !errors.Is(err, fs.ErrNotExist):
			// A permission error and the like: the file cannot be judged, and
			// offering to set a passphrase for it would be misleading.
			return false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Nothing above this point exists (a relative path): not creatable.
			return false
		}
		dir = parent
	}
}

// preScanArgs reads -lang and -settings directly from the command line, before
// flag.Parse runs. That way early output (usage, errors) is already localized
// and the settings file is known before the flags are registered.
func preScanArgs(args []string) (lang, settingsPath string) {
	get := func(name string) (string, bool) {
		for i, a := range args {
			switch {
			case a == "-"+name || a == "--"+name:
				if i+1 < len(args) {
					return args[i+1], true
				}
			case strings.HasPrefix(a, "-"+name+"="):
				return strings.TrimPrefix(a, "-"+name+"="), true
			case strings.HasPrefix(a, "--"+name+"="):
				return strings.TrimPrefix(a, "--"+name+"="), true
			}
		}
		return "", false
	}
	if v, ok := get("lang"); ok {
		lang = v
	}
	if v, ok := get("settings"); ok {
		settingsPath = v
	}
	return lang, settingsPath
}

// resolveLocale turns a locale preference into a supported locale, falling back
// to the system locale and then to the default.
func resolveLocale(pref string) i18n.Locale {
	if l, ok := i18n.Normalize(pref); ok {
		return l
	}
	return i18n.Detect(os.Getenv)
}

// printDrivers lists the registered drivers and dialects.
func printDrivers(tr i18n.Translator) {
	fmt.Println(tr.T(i18n.KeyCLIDriversHead))
	for _, name := range driver.Names() {
		mark := tr.T(i18n.KeyCLIDriverPending)
		if driver.Implemented(name) {
			mark = tr.T(i18n.KeyCLIDriverOK)
		}
		fmt.Printf("  %-12s%s\n", name, mark)
	}
	fmt.Println()
	fmt.Println(tr.T(i18n.KeyCLIDialectsHead))
	for _, name := range dialect.Names() {
		d := dialect.MustGet(name)
		fmt.Printf(tr.T(i18n.KeyCLIDialectLine), name, len(d.Keywords()), len(d.Functions()))
	}
}

// starterTemplate is the SQL a new session starts with (it demonstrates
// qualified completion).
const starterTemplate = `-- tdb ` + version + ` · Ctrl+Space completes, Tab/Enter accepts, Esc dismisses
-- The demo connection ships sample metadata: public.users / orders / products /
-- order_items … plus the reporting views.
-- Try: type "SELECT u." and watch the popup, then add "FROM users u".

SELECT u.
FROM users u
`

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "tdb: "+format+"\n", args...)
	os.Exit(1)
}
