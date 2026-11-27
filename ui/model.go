package ui

// model.go defines the top-level Bubble Tea model and the per-tab session.
// It holds state only; input handling lives in editor_keys.go and mouse.go,
// while rendering lives in render.go and the *_render files.

import (
	"context"
	"os"
	"time"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/complete"
	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/history"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
	"github.com/p-v-q/tdb/sql"
	"github.com/p-v-q/tdb/textbuf"
)

// defaultTitle is used when Config.Title is empty.
const defaultTitle = "tdb"

// Config is everything the UI needs to start.
type Config struct {
	// Title is shown in the top-left corner.
	Title string
	// ConnName and FileName are shown in the title bar.
	ConnName string
	FileName string
	// Dialect drives lexing, highlighting and completion.
	Dialect dialect.Dialect
	// Engine produces completion candidates; nil builds a default engine.
	Engine *complete.Engine
	// Provider supplies metadata; when nil, InitialCatalog (possibly nil) is
	// used as-is.
	Provider catalog.Provider
	// Conn runs statements. A driver.Connection is both a provider and a
	// connection, so the binary passes the same object twice; tests may leave
	// this nil, in which case Run reports that there is no connection.
	Conn driver.Connection
	// InitialCatalog is an already-loaded metadata snapshot.
	InitialCatalog *catalog.Catalog
	// Text is the editor's initial content.
	Text string

	// FirstRun reports that the settings file did not exist at startup. The
	// binary passes it when os.Stat finds no file there; the UI then offers
	// to protect the (plain-text) connections right away, which can be
	// skipped.
	FirstRun bool

	// Translator localizes the interface. When nil, a bundle for Settings
	// (or the system locale) is built automatically.
	Translator i18n.Translator
	// Settings holds the user preferences. The zero value means "use the
	// defaults".
	Settings settings.Settings
	// SettingsPath is where preferences are persisted. Empty disables saving,
	// which is what tests usually want.
	SettingsPath string
	// HistoryPath overrides the query-history file. Empty derives it from
	// SettingsPath (next to the settings file); with no settings path history
	// stays in memory.
	HistoryPath string
}

// session is the state of one editor tab: its buffer and everything that moves
// with it (scroll offsets, selection, completion popup). Model embeds a
// *session so every editor helper keeps working on the active tab without
// field-by-field indirection; switching tabs swaps the pointer.
type session struct {
	fileName  string
	createdAt time.Time // names a file-less tab (the time it was opened)
	// name is the automatically derived name of the tab: the object it
	// shows, the kind of statement it was generated from, the first line of
	// a statement opened from the history. It is empty for a tab that has
	// no meaningful name (a plain empty tab).
	name string
	// userName is the name the user gave the tab. It wins over every
	// derived name and survives until the tab is closed; emptying the
	// rename box clears it, which restores the derived name.
	userName string
	buf      *textbuf.Buffer

	// welcome marks this tab as the welcome page (docs/ux-round7.md, §2): a
	// real session whose buffer is empty by construction, but whose editor
	// body is drawn and handled by welcome.go. welcomeSel is the action row
	// Enter activates; welcomeHover is the row under the pointer and, like
	// every hover in round 7 §1, it only draws and never moves welcomeSel.
	welcome      bool
	welcomeSel   int
	welcomeHover int // -1 when the pointer is elsewhere

	firstRow int // first visible line
	scrollX  int // horizontal scroll, in columns

	popup   popupState
	lastCtx sql.Context
	sel     selectionState
	// result is the result set currently shown. resultSets keeps every
	// statement's outcome from the last execution; switching sets swaps the
	// active value in and out so each keeps its own sort, filter and caret.
	result     resultState
	resultSets []resultState
	resultIdx  int

	typeSeq  int64 // undo group id for consecutive typing
	lexCache struct {
		text  string
		kinds [][]sql.TokenKind
	}
}

// pickerPurpose records why the modal file picker was opened, so its choice
// is handed to the right dialog when it closes (docs/ux-round7.md, §4/§5).
// It is set right after openFilePicker and cleared by dispatchPickerResult
// the moment the picker is finished — a pick and its purpose never outlive
// the dialog that produced them.
type pickerPurpose int

const (
	pickerForNone      pickerPurpose = iota // no picker is outstanding
	pickerForOpenSQL                        // the choice becomes a new editor tab
	pickerForExportDir                      // the choice becomes the export folder
)

// Model is the UI state.
type Model struct {
	cfg Config

	theme      theme
	translator i18n.Translator
	settings   settings.Settings
	// settingsPath is cached so persistence never has to re-resolve it.
	settingsPath string
	// sysLocale is the locale detected from the environment at startup, used
	// when the settings say "follow the system".
	sysLocale i18n.Locale

	// The active tab; tabs holds every open tab in tab-bar order. The embedded
	// *session promotes buf, firstRow, scrollX, sel, popup, lastCtx, typeSeq
	// and lexCache, so editor code reads m.buf as usual.
	*session
	tabs      []*session
	active    int // index of the active tab
	tabScroll int // first tab shown in the tab bar

	// conn describes the connected session and connOpen is its live connection:
	// statements always run through connOpen, so connecting to another database
	// in the object browser really switches the database being worked on.
	conn     settings.Connection
	connOpen driver.Connection

	// connSeq identifies the most recently issued connection request. A newer
	// request, reconnect or disconnect advances it and cancels the previous
	// handshake, so a late outcome can never choose the live session.
	connSeq       int64
	connCancel    context.CancelFunc
	connCloseWarn string // rollback/close warning waiting for the next connection outcome
	// catalogSeq does the same for metadata refreshes; the connection pointer
	// carried by each response additionally binds it to its source.
	catalogSeq int64

	// connFormSeq gives each opening of the connection form a distinct identity,
	// so a server-list response cannot cross from one form instance to another.
	connFormSeq int64
	// settingsWrites serializes sealed settings snapshots in call order. It is a
	// pointer because Bubble Tea copies Model values between updates.
	settingsWrites *settingsSaveQueue

	// queryCancel stops the statement that is running, if any.
	queryCancel  context.CancelFunc
	queryRunning bool
	// querySeq numbers every batch that starts and queryGen is the number of
	// the batch that owns the query state (running flag, cancel function and
	// results). A late outcome of a batch a newer one replaced is stale and
	// must not touch them (see update.go).
	querySeq int64
	queryGen int64

	// txManual switches statement execution from auto-commit (the default)
	// to an explicit transaction the user commits or rolls back; txOpen
	// reports uncommitted work and drives the status-bar indicator. Both are
	// runtime state on purpose: an uncommitted transaction cannot survive a
	// restart, so the mode is deliberately not persisted with the settings.
	txManual bool
	txOpen   bool

	cat *catalog.Catalog

	width  int
	height int

	layout         layoutState  // pane visibility/size/focus/drag
	objects        objectsState // object browser state
	menu           menuState    // menu bar state
	ctx            objectsMenuState
	resultMenu     resultMenuState
	tabMenu        tabMenuState    // the tab bar's context menu
	tabRename      tabRenameDialog // the modal tab rename input
	dlg            settingsDialog
	connDlg        connDialog
	vaultDlg       vaultDialog // passphrase dialog: setup, unlock, change, remove, manage
	exportDlg      exportDialog
	picker         filePicker    // built-in file/directory picker (file_picker.go)
	pickerPurpose  pickerPurpose // what the open picker is choosing for
	historyDlg     historyDialog
	keysDlg        keybindingsDialog // modal F1 help overlay
	confirm        confirmDialog     // execution confirmation for dangerous SQL
	historyEntries []history.Entry
	histStore      history.Store
	find           findState // the status-bar find prompt
	toolHover      int       // toolbar button under the pointer, -1 when none
	colHover       int       // result column separator under the pointer, -1 when none
	tabHover       int       // tab under the pointer, -1 when none (hover never switches tabs)
	colResize      columnResizeState
	scrollDrag     scrollbarDrag
	hScrollDrag    hScrollbarDrag // the result grid's horizontal bar drag

	status      statusState
	dragging    bool      // left button held while dragging
	clicks      int       // consecutive clicks at the current spot
	lastClickAt time.Time // time of the previous click
	lastClickX  int       // screen coordinate of the previous click
	lastClickY  int

	quitting bool
}

func New(cfg Config) Model {
	if cfg.Dialect == nil {
		cfg.Dialect = dialect.MustGet("postgres")
	}
	if cfg.Engine == nil {
		cfg.Engine = complete.NewDefaultEngine(cfg.Dialect)
	}
	if cfg.Title == "" {
		cfg.Title = defaultTitle
	}
	if cfg.Settings.Version == 0 {
		// No settings were supplied: start from the factory preferences.
		cfg.Settings = settings.Default()
	}
	cfg.Settings.Normalize()

	sys := i18n.Detect(os.Getenv)
	if cfg.Translator == nil {
		cfg.Translator = i18n.New(resolveLocale(cfg.Settings.Locale, sys))
	}

	sess := &session{
		fileName:  cfg.FileName,
		createdAt: time.Now(),
		buf:       textbuf.New(cfg.Text),
		result:    newResultState(),
	}
	// No file is open, so the first tab is the welcome page instead of an
	// empty editor (docs/ux-round7.md, §2). The condition is the file and
	// nothing else: the binary starts a file-less session with a starter
	// template in the buffer (cmd/tdb/main.go), and that template is
	// exactly the onboarding the welcome page replaces. The text itself
	// stays in the buffer untouched — the page only stands in front of it,
	// so converting the tab can never lose what was passed in.
	if cfg.FileName == "" {
		sess.welcome = true
		sess.welcomeHover = -1
	}
	m := Model{
		cfg:            cfg,
		theme:          newTheme(paletteFor(cfg.Settings.Theme)),
		translator:     cfg.Translator,
		settings:       cfg.Settings,
		settingsPath:   cfg.SettingsPath,
		settingsWrites: newSettingsSaveQueue(),
		sysLocale:      sys,
		session:        sess,
		tabs:           []*session{sess},
		conn:           settings.Connection{Name: cfg.ConnName, Driver: cfg.Dialect.Name()},
		connOpen:       cfg.Conn,
		cat:            cfg.InitialCatalog,
		layout:         layoutFromSettings(cfg.Settings),
		menu:           newMenuState(),
		toolHover:      -1,
		colHover:       -1,
		tabHover:       -1,
	}
	historyPath := cfg.HistoryPath
	if historyPath == "" {
		historyPath = history.DefaultPath(cfg.SettingsPath)
	}
	m.histStore = history.New(historyPath)
	if entries, err := m.histStore.Load(); err == nil {
		m.historyEntries = entries
	}
	m.resetObjects()
	m.updateContext()
	// A vault that came with the settings file blocks everything until it is
	// opened (or skipped): no connection may be touched first. The two
	// startup dialogs are mutually exclusive — a first run has no file, hence
	// no vault, by construction.
	switch {
	case cfg.Settings.Locked():
		m.openVaultUnlock()
	case cfg.FirstRun && !cfg.Settings.Encrypted():
		m.openVaultMode(vaultSetup)
	}
	return m
}

// resolveLocale picks the active locale: an explicit preference wins, otherwise
// the system locale decides.
func resolveLocale(pref string, sys i18n.Locale) i18n.Locale {
	if l, ok := i18n.Normalize(pref); ok {
		return l
	}
	return sys
}

// t returns a localized message, formatting any arguments.
func (m Model) t(key i18n.Key, args ...any) string { return m.translator.T(key, args...) }
