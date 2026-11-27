package ui

// conn_dialog.go implements the modal "new connection" form of the object
// browser: pick a database type, fill in its parameters and connect.
//
//   - ↑/↓ (or Tab) move between fields, ←→ move the caret, ⏎ connects, Esc
//     cancels.
//   - The database type is a dropdown: ⏎/Space/↓ open the option list, ↑/↓
//     move the highlight, ⏎/Space pick the driver (Esc closes the list only).
//     ←→ still cycle through the types as a fallback while the list is closed.
//     The SSL mode is a dropdown of the same kind, per-driver vocabulary.
//   - The field list depends on the driver type: a file database asks for a
//     file, the demo connection asks for nothing, the rest ask for
//     host/port/SSL mode, the credentials, the database (plus a default
//     schema where the driver has one) and an optional DSN.
//   - The fields fall under quiet section headers (Server, Credentials,
//     Database, Advanced). A header other than the first one is preceded by
//     a blank row, so the groups read as blocks; the headers are drawn rows
//     only: they are never focused, never typed into and never picked by the
//     mouse (see connField.section on why they live outside the fields
//     slice).
//   - The value column is capped at 56 columns; a longer value scrolls
//     horizontally so the caret stays visible instead of widening the box.
//   - The password is masked while typing and stored like the other fields.

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
)

// connFieldKind decides how a field is edited.
type connFieldKind int

// The field kinds.
const (
	fieldText   connFieldKind = iota // free text
	fieldSecret                      // free text, shown masked
	fieldChoice                      // a dropdown (←→ cycle through as a fallback)
)

// connField is one row of the form. Values are kept as text so the form can be
// rebuilt when the database type changes without losing what was typed.
type connField struct {
	key     i18n.Key
	kind    connFieldKind
	value   string
	caret   int
	options []string // fieldChoice, and the server lists of listable fields
	// listable marks a text field whose values can be read from the server
	// (the database and the default schema): ↓ opens the list it holds, and
	// typing a value by hand keeps working for names the server did not
	// offer.
	listable bool
	// section is the title of the group the field belongs to
	// (i18n.KeyConnSectionXxx); it is drawn as a header row above the first
	// field of a group and left out for the leading Name/Type pair (see
	// secConnection). Headers are NOT entries of the fields slice: every
	// element stays an editable field, so the focus index, toConnection,
	// fillConnFields and the choice callbacks never have to skip anything.
	// A title row exists only on the way out — connLayout builds the row list
	// once, and rendering and mouse hit testing both read it (they must
	// agree, the rows on screen are the clickable ones).
	section i18n.Key
}

// connDialog is the state of the modal form.
type connDialog struct {
	open   bool
	fields []connField
	focus  int
	// dropdown is the option list of the focused choice field. While it is
	// open it is modal inside the form: every key belongs to it.
	dropdown bool
	dropSel  int // highlighted option in the open list (keyboard; Enter picks it)
	// dropHover is the option under the pointer while the list is open, -1
	// when the pointer is elsewhere. It only drives the highlight: hovering
	// never writes a value and never moves dropSel (round 7 §1).
	dropHover int
	// editing turns the form into "edit connection": it saves instead of
	// connecting (unless the edited connection is the active one, in which case
	// it reconnects so the changes take effect).
	editing      bool
	originalName string
	// listKey is the field whose server-provided list is being loaded right
	// now ("" when nothing is in flight). It keeps ↓ from starting the same
	// round trip twice.
	listKey i18n.Key
	// instance identifies this opening of the form; listSeq is advanced whenever
	// a request starts or a connection parameter is edited. listParams is the
	// exact snapshot the current request asked the server about.
	instance   int64
	listSeq    int64
	listParams settings.Connection
}

// defaultPort returns the conventional port of a driver, as text.
func defaultPort(driverName string) string {
	switch driverName {
	case "postgres":
		return "5432"
	case "mysql", "mariadb":
		return "3306"
	case "sqlserver":
		return "1433"
	}
	return ""
}

// Section titles of the form. The leading Name/Type pair gets none
// (secConnection): the border of the dialog already carries the form's title,
// so a header repeating "this is the connection part" would only add a row.
const (
	secConnection  = i18n.Key("")
	secServer      = i18n.KeyConnSectionServer
	secCredentials = i18n.KeyConnSectionCredentials
	secDatabase    = i18n.KeyConnSectionDatabase
	secAdvanced    = i18n.KeyConnSectionAdvanced
)

// connSupportsSchema reports whether a driver has a default schema of its
// own. PostgreSQL resolves unqualified names in a search_path, so the
// connection can name the schema it works in. MySQL and MariaDB use the
// database, SQLite has only main: there the field would say nothing the
// database does not already say, so it is not offered.
func connSupportsSchema(driverName string) bool {
	return driverName == "postgres"
}

// connSSLModes lists the SSL modes a driver understands, in the order the
// dropdown shows them; the empty first entry means "the driver's own default"
// and is shown as "default" (KeyConnSSLDefault). The other entries are
// protocol keywords of the driver, so they are not translated. A driver with
// no client-side idea of SSL gets nil: SQLite's connection is a file and the
// demo never leaves the machine. A driver TDB has not implemented yet gets
// nil too (sqlserver is a reserved stub): inventing a vocabulary the driver
// cannot confirm would dead-end every typed value there.
func connSSLModes(driverName string) []string {
	switch driverName {
	case "postgres":
		return []string{"", "disable", "allow", "prefer", "require", "verify-ca", "verify-full"}
	case "mysql", "mariadb":
		// go-sql-driver's tls parameter has no "require": its vocabulary is
		// only true, false, skip-verify, preferred (plus custom names
		// registered by the application), and an unknown value fails the
		// connection open with "unknown config name". skip-verify encrypts
		// without verifying the server certificate — that is what MySQL's
		// ssl-mode=REQUIRED means — so skip-verify stands in for "require"
		// here; do not add it back.
		return []string{"", "false", "preferred", "skip-verify", "true"}
	}
	return nil
}

// buildConnFields returns the form for a driver type. Values already typed are
// carried over, so switching type never loses input.
func buildConnFields(driverName string, prev []connField) []connField {
	keep := make(map[i18n.Key]string, len(prev))
	for _, f := range prev {
		keep[f.key] = f.value
	}
	text := func(key i18n.Key, kind connFieldKind, section i18n.Key, value string) connField {
		if v, ok := keep[key]; ok {
			value = v
		}
		return connField{key: key, kind: kind, value: value, caret: len([]rune(value)), section: section}
	}
	// The port belongs to the database type, so it always restarts from the
	// default of the type being selected.
	fresh := func(key i18n.Key, kind connFieldKind, section i18n.Key, value string) connField {
		return connField{key: key, kind: kind, value: value, caret: len([]rune(value)), section: section}
	}
	// choice is an option-list field. A value kept from the previous type
	// survives only when the new type's vocabulary contains it — the vocabularies
	// do not overlap (postgres sslmode vs go-sql-driver tls), so switching type
	// resets the field to its default, like the port above.
	choice := func(key i18n.Key, section i18n.Key, value string, options []string) connField {
		if v, ok := keep[key]; ok {
			for _, opt := range options {
				if opt == v {
					value = v
					break
				}
			}
		}
		return connField{key: key, kind: fieldChoice, value: value, caret: len([]rune(value)), options: options, section: section}
	}
	// pick is a text field whose values the server can list: ↓ opens the list,
	// typing a name the list does not hold still works.
	pick := func(key i18n.Key, section i18n.Key) connField {
		f := text(key, fieldText, section, "")
		f.listable = true
		return f
	}
	// listableDriver reports whether the database and schema of this type can
	// be read from the server: PostgreSQL has both, MySQL/MariaDB name a
	// database and treat it as the schema. The stub drivers have no server to
	// ask.
	listableDriver := driverName == "postgres" || driverName == "mysql" || driverName == "mariadb"

	fields := []connField{
		text(i18n.KeyConnName, fieldText, secConnection, ""),
		choice(i18n.KeyConnDriver, secConnection, driverName, driver.Names()),
	}

	switch driverName {
	case "demo":
		// The offline demo connection needs nothing else.
	case "sqlite":
		// The file is the database; it goes under the Database header.
		fields = append(fields, text(i18n.KeyConnFile, fieldText, secDatabase, ""))
	default:
		fields = append(fields,
			text(i18n.KeyConnHost, fieldText, secServer, "localhost"),
			fresh(i18n.KeyConnPort, fieldText, secServer, defaultPort(driverName)),
		)
		if modes := connSSLModes(driverName); modes != nil {
			// The SSL mode is transport security between client and server,
			// not a name of anything in the database — so it stays in the
			// Server group next to host and port. It is a dropdown; the SSL
			// modes list says its vocabulary.
			fields = append(fields, choice(i18n.KeyConnSSLMode, secServer, "", modes))
		}
		fields = append(fields,
			text(i18n.KeyConnUser, fieldText, secCredentials, ""),
			text(i18n.KeyConnPassword, fieldSecret, secCredentials, ""),
			// The environment-variable field sits right after the password it
			// overrides, so the two ways of providing a secret stay together.
			text(i18n.KeyConnPasswordEnv, fieldText, secCredentials, ""),
		)
		if listableDriver {
			fields = append(fields, pick(i18n.KeyConnDatabase, secDatabase))
		} else {
			fields = append(fields, text(i18n.KeyConnDatabase, fieldText, secDatabase, ""))
		}
		if connSupportsSchema(driverName) {
			// The default schema sits next to the database: both name the
			// place the statements work in, and both are read from the server.
			if listableDriver {
				fields = append(fields, pick(i18n.KeyConnSchema, secDatabase))
			} else {
				fields = append(fields, text(i18n.KeyConnSchema, fieldText, secDatabase, ""))
			}
		}
		fields = append(fields, text(i18n.KeyConnDSN, fieldText, secAdvanced, ""))
	}
	return fields
}

// openConnDialog opens the form with the usual default (the demo type).
func (m *Model) openConnDialog() {
	m.closeMenu()
	m.closeObjectsMenu()
	m.closeResultMenu()
	m.invalidateConnFieldList()
	m.connFormSeq++
	m.connDlg = connDialog{
		open:     true,
		fields:   buildConnFields("demo", nil),
		focus:    0,
		instance: m.connFormSeq,
	}
}

// openConnDialogFor opens the form prefilled with an existing connection.
func (m *Model) openConnDialogFor(c settings.Connection) {
	m.closeMenu()
	m.closeObjectsMenu()
	m.closeResultMenu()
	name := c.Driver
	if name == "" {
		name = "demo"
	}
	fields := buildConnFields(name, nil)
	fillConnFields(fields, c)
	m.invalidateConnFieldList()
	m.connFormSeq++
	m.connDlg = connDialog{
		open:         true,
		fields:       fields,
		focus:        0,
		editing:      true,
		originalName: c.Name,
		instance:     m.connFormSeq,
	}
}

// fillConnFields copies a saved connection into the form fields.
func fillConnFields(fields []connField, c settings.Connection) {
	for i := range fields {
		f := &fields[i]
		switch f.key {
		case i18n.KeyConnName:
			f.value = c.Name
		case i18n.KeyConnDriver:
			if c.Driver != "" {
				f.value = c.Driver
			}
		case i18n.KeyConnHost:
			f.value = c.Host
		case i18n.KeyConnPort:
			if c.Port > 0 {
				f.value = strconv.Itoa(c.Port)
			}
		case i18n.KeyConnUser:
			f.value = c.User
		case i18n.KeyConnPassword:
			f.value = c.Password
		case i18n.KeyConnPasswordEnv:
			f.value = c.PasswordEnv
		case i18n.KeyConnDatabase:
			f.value = c.Database
		case i18n.KeyConnSchema:
			f.value = c.Schema
		case i18n.KeyConnFile:
			f.value = c.File
		case i18n.KeyConnDSN:
			f.value = c.DSN
		case i18n.KeyConnSSLMode:
			f.value = c.SSLMode
		}
		f.caret = len([]rune(f.value))
	}
}

// invalidateConnFieldList makes an in-flight server-list response stale and
// drops cached server lists. The next request will read the edited parameters.
func (m *Model) invalidateConnFieldList() {
	m.connDlg.listSeq++
	m.connDlg.listKey = ""
	m.connDlg.listParams = settings.Connection{}
	m.connDlg.closeConnDropdown()
	for i := range m.connDlg.fields {
		if m.connDlg.fields[i].listable {
			m.connDlg.fields[i].options = nil
		}
	}
}

// closeConnDialog closes the form.
func (m *Model) closeConnDialog() {
	m.invalidateConnFieldList()
	m.connDlg.open = false
	m.connDlg.dropdown = false
}

// toConnection builds the connection the form describes.
func (d connDialog) toConnection() settings.Connection {
	c := settings.Connection{}
	for _, f := range d.fields {
		value := strings.TrimSpace(f.value)
		switch f.key {
		case i18n.KeyConnName:
			c.Name = strings.TrimSpace(f.value)
		case i18n.KeyConnDriver:
			c.Driver = value
		case i18n.KeyConnHost:
			c.Host = value
		case i18n.KeyConnPort:
			c.Port, _ = strconv.Atoi(value)
		case i18n.KeyConnUser:
			c.User = value
		case i18n.KeyConnPassword:
			c.Password = f.value
		case i18n.KeyConnPasswordEnv:
			c.PasswordEnv = value
		case i18n.KeyConnDatabase:
			c.Database = value
		case i18n.KeyConnSchema:
			c.Schema = value
		case i18n.KeyConnFile:
			c.File = value
		case i18n.KeyConnDSN:
			c.DSN = value
		case i18n.KeyConnSSLMode:
			c.SSLMode = value
		}
	}
	// A password read from the environment is resolved at connect time and
	// must never reach the settings document, whatever the password field holds
	// (it is ignored while PasswordEnv is set).
	if c.PasswordEnv != "" {
		c.Password = ""
	}
	return c
}

// driverConfig turns a connection into a driver configuration: a full DSN wins,
// otherwise the fields are passed as options and a conventional URL DSN is
// composed for drivers that want one. password is the already-resolved secret
// (see resolveConnectionPassword); c is only read, never written back, so a
// password read from the environment cannot leak into the saved connection.
func driverConfig(c settings.Connection, password string) driver.Config {
	opts := map[string]string{
		"host":     c.Host,
		"user":     c.User,
		"password": password,
		"database": c.Database,
		"file":     c.File,
	}
	if connSupportsSchema(c.Driver) {
		// The default schema is not part of the connection string: it is a
		// session setting the driver applies, so it travels as an option even
		// when the DSN carries everything else.
		opts["schema"] = strings.TrimSpace(c.Schema)
	}
	if c.DSN != "" {
		return driver.Config{Name: c.Driver, DSN: c.DSN, Options: opts}
	}
	if c.Port > 0 {
		opts["port"] = strconv.Itoa(c.Port)
	}
	return driver.Config{Name: c.Driver, DSN: composeDSN(c, password), Options: opts}
}

// composeDSN builds a URL-style DSN from the fields (user:pass@host:port/db).
// The components are URL-escaped: a password containing @, / or : would
// otherwise silently change the shape of the connection string. password is the
// resolved secret, not c.Password, so an environment-provided password is used
// without being stored on the connection.
//
// The SSL mode rides along as a query parameter the driver's URL parser
// understands (sslmode for PostgreSQL, tls for MySQL/MariaDB). It is only
// composed when the fields compose the DSN at all: driverConfig keeps a
// hand-written DSN untouched, so a DSN still wins wholesale.
func composeDSN(c settings.Connection, password string) string {
	switch c.Driver {
	case "demo":
		return ""
	case "sqlite":
		return c.File
	}
	host := strings.TrimSpace(c.Host)
	if host == "" {
		host = "localhost"
	}
	if c.Port > 0 {
		if _, _, err := net.SplitHostPort(host); err != nil {
			host = net.JoinHostPort(host, strconv.Itoa(c.Port))
		}
	}
	u := url.URL{Scheme: c.Driver, Host: host}
	if c.Database != "" {
		u.Path = "/" + c.Database
	}
	if c.User != "" || password != "" {
		if password != "" {
			u.User = url.UserPassword(c.User, password)
		} else {
			u.User = url.User(c.User)
		}
	}
	// Query parameters go through url.Values, not hand-built text, so the
	// encoding of odd modes stays correct. A mode the UI never offered (an
	// edited settings file, an unknown driver) may name a query parameter no
	// parser knows about — that failure mode belongs to the driver's own
	// parser, which reports it.
	if mode := strings.TrimSpace(c.SSLMode); mode != "" {
		q := u.Query()
		switch c.Driver {
		case "postgres":
			q.Set("sslmode", mode)
		case "mysql", "mariadb":
			q.Set("tls", mode)
		}
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// ---- Geometry ----

// connValueMaxW caps the value column. The form used to stretch to its widest
// value, so a long DSN blew the box up to the screen width; now a longer value
// scrolls horizontally (see the caret window in renderConnField) and the box
// keeps its shape.
const connValueMaxW = 56

// connRow is one body row of the form: a field (field >= 0), the title of
// the group drawn above its first field (field < 0, section set) or the
// blank row before such a title (field < 0, spacer).
type connRow struct {
	field   int
	section i18n.Key // header title, field < 0 only (a spacer's is empty)
	spacer  bool     // the blank row before a group title, first title excepted
}

// connLayout is the computed geometry of the form, shared by rendering and hit
// testing.
type connLayout struct {
	x, y, w, h int // outer box, borders included
	innerW     int
	labelW     int
	valueW     int
	rows       []connRow // body rows between the title and the hint, in draw order
	drawn      int       // how many of the rows fit the box (a short screen clips the tail)
	fieldRow   []int     // field index -> its row in rows (indexable for every field)
	rowY       int       // screen row of the first body row
	valueX     int       // screen column of the value column
	connect    Rect
	cancel     Rect
}

// connLayout computes the form geometry for the current screen size. The
// shared modal rules apply (ui/modal.go): the box wants enough room for the
// label column and a 56-column value column, is never smaller than the shared
// minimum of 60x12 and never wider than the area allows.
func (m Model) connLayout() connLayout {
	fields := m.connDlg.fields

	// Build the body rows first: a field whose group the previous field was
	// not in gets a blank row plus a title row above itself — except the
	// first title of the form (always Server), which skips the blank row: the
	// rows above it are the untitled Name/Type pair and an empty strip there
	// would only echo the dialog's own padding. Rendering and the mouse hit
	// test both walk this list, so the drawn rows and the clickable rows are
	// the same.
	rows := make([]connRow, 0, len(fields)+8)
	prev := secConnection
	sawTitle := false
	for i, f := range fields {
		if f.section != prev {
			if sawTitle {
				rows = append(rows, connRow{field: -1, spacer: true})
			}
			rows = append(rows, connRow{field: -1, section: f.section})
			prev = f.section
			sawTitle = true
		}
		rows = append(rows, connRow{field: i})
	}

	// The measured columns: labels and section titles share the label column,
	// so a title lines up with the labels under it.
	labelW := 0
	prev = secConnection
	for _, f := range fields {
		labelW = max(labelW, lipgloss.Width(m.t(f.key)))
		if f.section != prev {
			labelW = max(labelW, lipgloss.Width(m.t(f.section)))
		}
		prev = f.section
	}
	// The value column wants the widest value plus room for the caret, but it
	// is capped: a longer value scrolls inside the column instead. The text
	// is measured after cleaning, the way the export dialog measures its
	// path: a value that only looks long because it holds control characters
	// must not widen the form, since those characters never reach the
	// terminal.
	valueW := 12
	for _, f := range fields {
		valueW = max(valueW, min(connValueMaxW, lipgloss.Width(stripControl(m.connValueText(f)))+2))
	}

	area := m.paneRects().area
	wantW, wantH := labelW+valueW+6, len(rows)+6
	var w, h int
	if len(fields) <= 3 {
		// The smallest forms (the demo connection: two fields, the sqlite
		// file: three) carry so little content that the full 12-row minimum
		// pads them with blank rows and makes the form look unfinished — the
		// compact floor of eight applies (ui/modal.go). The width rules are
		// untouched, so the two-column form still has its minimum width.
		w, h = modalSizeCompact(wantW, wantH, area.W, area.H)
	} else {
		w, h = modalSize(wantW, wantH, area.W, area.H)
	}
	rect := modalRect(area, w, h)
	x, y := rect.X, rect.Y

	// The rules may hand back a wider box than the content asked for (the
	// shared minimum) or dispense less (a narrow screen): the label column
	// keeps its measured width and the value column takes the slack, still
	// capped so a long DSN scrolls instead of stretching the box.
	innerW := w - 2
	valueW = clamp(innerW-labelW-4, 1, connValueMaxW)

	fieldRow := make([]int, len(fields))
	for r, row := range rows {
		if row.field >= 0 {
			fieldRow[row.field] = r
		}
	}

	l := connLayout{
		x: x, y: y, w: w, h: h,
		innerW: innerW, labelW: labelW, valueW: valueW,
		rows:     rows,
		drawn:    min(len(rows), max(0, h-5)),
		fieldRow: fieldRow,
		rowY:     y + 2,
		valueX:   x + labelW + 4,
	}
	// The two buttons sit on the last row of the box.
	btnY := y + h - 2
	l.connect = Rect{X: x + 2, Y: btnY, W: lipgloss.Width(m.t(m.connPrimaryKey())) + 2, H: 1}
	l.cancel = Rect{X: l.connect.X + l.connect.W + 2, Y: btnY, W: lipgloss.Width(m.t(i18n.KeyConnCancel)) + 2, H: 1}
	return l
}

// connPrimaryKey is the label of the primary button: "Connect" for a new
// connection, "Save" when editing.
func (m Model) connPrimaryKey() i18n.Key {
	if m.connDlg.editing {
		return i18n.KeyConnSave
	}
	return i18n.KeyConnConnect
}

// connHintKey returns the i18n key of the form's hint line: the connect form
// and the edit form word their action differently.
func (m Model) connHintKey() i18n.Key {
	if m.connDlg.editing {
		return i18n.KeyConnSaveHint
	}
	return i18n.KeyConnHint
}

// connRowAt returns the field index under a screen coordinate.
func (m Model) connRowAt(x, y int) (int, bool) {
	l := m.connLayout()
	if !(Rect{X: l.x, Y: l.y, W: l.w, H: l.h}).Contains(x, y) {
		return 0, false
	}
	row := y - l.rowY
	if row < 0 || row >= l.drawn {
		return 0, false
	}
	r := l.rows[row]
	if r.field < 0 {
		// A section title or the blank row before one is padding, not a
		// control: clicking it changes nothing, so the click is reported as
		// "no field here".
		return 0, false
	}
	return r.field, true
}

// connDropdownLayout computes the geometry of the open option list of the
// focused choice field: the screen rectangle (borders included), the first
// visible option and how many options fit. Rendering and hit testing share it,
// like connLayout.
func (m Model) connDropdownLayout() (rect Rect, top, visible int, ok bool) {
	if !m.connDlg.dropdown || m.connDlg.focus < 0 || m.connDlg.focus >= len(m.connDlg.fields) {
		return Rect{}, 0, 0, false
	}
	f := m.connDlg.fields[m.connDlg.focus]
	if len(f.options) == 0 {
		// A choice field always has options and a listable text field gets
		// them from the server: without options there is nothing to show.
		return Rect{}, 0, 0, false
	}
	// The width follows the longest label (plus padding and borders). SSL
	// modes are plain keywords, so an SSL list stays narrow; an empty mode is
	// shown as its localized word (see connChoiceLabel).
	w := 4
	for _, opt := range f.options {
		w = max(w, lipgloss.Width(m.connChoiceLabel(f, opt))+4)
	}
	l := m.connLayout()
	x := clamp(l.valueX, 0, max(0, m.width-w))
	// The list hangs right below the field row — the row of the layout the
	// fields are drawn on, not the field index: section titles shift rows.
	y := l.rowY + l.fieldRow[m.connDlg.focus] + 1
	// The list grows down and is clamped to the rows above the status bar; if
	// the options do not fit, it scrolls to keep the highlight visible.
	visible = clamp(len(f.options), 1, max(1, m.height-1-(y+2)))
	top = clamp(m.connDlg.dropSel-visible+1, 0, max(0, len(f.options)-visible))
	return Rect{X: x, Y: y, W: w, H: visible + 2}, top, visible, true
}

// connDropRowAt maps a coordinate on the open option list to the option under
// it, for a press (which picks) and for a motion (which only hovers) alike —
// the two must agree, or the highlight would not follow the rows that are
// drawn. Only a point inside the frame and on a visible option row counts: a
// point on the border would otherwise map to an option above or beside the
// viewport.
func (m Model) connDropRowAt(x, y int) (int, bool) {
	r, top, visible, ok := m.connDropdownLayout()
	if !ok {
		return 0, false
	}
	row := y - r.Y - 1
	if !(x > r.X && x < r.X+r.W-1 && row >= 0 && row < visible) {
		return 0, false
	}
	options := m.connDlg.fields[m.connDlg.focus].options
	idx := top + row
	if idx < 0 || idx >= len(options) {
		return 0, false
	}
	return idx, true
}

// ---- Values ----

// connValueText is the text shown in a field (secrets are masked, a choice
// field shows its current label plus the dropdown indicator).
func (m Model) connValueText(f connField) string {
	switch f.kind {
	case fieldSecret:
		return strings.Repeat("•", len([]rune(f.value)))
	case fieldChoice:
		return m.connChoiceLabel(f, f.value) + " ▾"
	}
	if f.key == i18n.KeyConnSchema && f.value == "" {
		// No schema means "the database default": say so instead of leaving
		// the row blank, which reads as "not filled in yet".
		return m.t(i18n.KeyConnSchemaDefault)
	}
	return f.value
}

// connChoiceLabel is the name of one option of a choice field as shown in the
// UI. The value kept in the field stays the option ID. Driver names are
// product names, so they are not translated; SSL modes are protocol keywords
// of the driver and keep their literal spelling (connDriverLabel's fallback
// must not capitalize them) — except the empty mode, which names a UI concept
// ("let the driver decide"), not a keyword, and gets its own localized word.
func (m Model) connChoiceLabel(f connField, value string) string {
	switch f.key {
	case i18n.KeyConnSSLMode:
		if value == "" {
			return m.t(i18n.KeyConnSSLDefault)
		}
		return value
	case i18n.KeyConnSchema:
		if value == "" {
			return m.t(i18n.KeyConnSchemaDefault)
		}
		return value
	case i18n.KeyConnDriver:
		// Driver names are product names: "PostgreSQL", not the registry id.
		return connDriverLabel(value)
	}
	// Database names keep their server spelling: a name is data, and
	// "PostgreSQL" is not the database the server calls "postgres" on a
	// case-sensitive build.
	return value
}

// connDriverLabel is the name of a driver as shown in the UI. The value kept in
// the field stays the driver ID. Driver names are product names, so they are
// not translated.
func connDriverLabel(id string) string {
	switch id {
	case "postgres":
		return "PostgreSQL"
	case "mysql":
		return "MySQL"
	case "mariadb":
		return "MariaDB"
	case "sqlite":
		return "SQLite"
	case "sqlserver":
		return "SQL Server"
	case "demo":
		return "Demo"
	}
	// A driver the UI does not know (a saved connection from the future) is
	// shown with a capital first letter.
	r := []rune(id)
	if len(r) == 0 {
		return id
	}
	return string(unicode.ToUpper(r[0])) + string(r[1:])
}

// ---- Interaction ----

// handleConnKey handles a key press while the form is open and reports whether
// it consumed the key. The form is modal, so it consumes everything.
func (m Model) handleConnKey(key string, runes []rune) (Model, tea.Cmd, bool) {
	if len(m.connDlg.fields) == 0 {
		return m, nil, true
	}
	// The open option list is modal inside the form: while it is open, no key
	// can reach the fields.
	if m.connDlg.dropdown {
		return m.handleConnChoiceKey(key)
	}
	f := &m.connDlg.fields[m.connDlg.focus]

	// The type field is a dropdown: Enter/Space/↓ open the option list.
	if f.kind == fieldChoice {
		switch key {
		case "enter", "space", "down":
			m.openConnChoice()
			return m, nil, true
		}
	}

	// A listable field (database, default schema) opens the list the server
	// holds on ↓: the values there cannot be guessed, and typing stays
	// possible for names the list does not offer.
	if f.listable && (key == "down" || key == "ctrl+l") {
		return m.openConnFieldList(*f)
	}

	switch key {
	case "esc":
		m.closeConnDialog()
		return m, nil, true
	case "enter":
		return m.submitConnDialog()
	case "up", "shift+tab":
		m.moveConnFocus(-1)
		return m, nil, true
	case "down", "tab":
		m.moveConnFocus(1)
		return m, nil, true
	}

	switch f.kind {
	case fieldChoice:
		switch key {
		case "left", "right":
			delta := 1
			if key == "left" {
				delta = -1
			}
			before := f.value
			f.cycle(delta)
			if f.value != before {
				m.invalidateConnFieldList()
			}
			if f.key == i18n.KeyConnDriver {
				// The fields depend on the database type: rebuild the form,
				// keeping everything that was typed. The SSL mode only picks
				// a value, so cycling it must not rebuild anything.
				m.applyConnChoice()
			}
		}
		return m, nil, true
	default:
		before := f.value
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
			if len(runes) > 0 {
				r := []rune(f.value)
				insert := string(runes)
				f.value = string(r[:f.caret]) + insert + string(r[f.caret:])
				f.caret += len([]rune(insert))
			}
		}
		if f.value != before && f.key != i18n.KeyConnName {
			m.invalidateConnFieldList()
		}
	}
	return m, nil, true
}

// moveConnFocus moves to the next field in the given direction.
func (m *Model) moveConnFocus(delta int) {
	n := len(m.connDlg.fields)
	m.connDlg.focus = ((m.connDlg.focus+delta)%n + n) % n
}

// openConnFieldList opens the option list of a listable field, asking the
// server for the values the first time it is opened. The fields are a slice, so
// the receiver mutates the focused element directly.
func (m Model) openConnFieldList(f connField) (Model, tea.Cmd, bool) {
	if len(f.options) > 0 {
		m.openConnChoice()
		return m, nil, true
	}
	if m.connDlg.listKey == f.key {
		// A list for this field is already on its way: ↓ again waits instead
		// of starting a second round trip.
		return m, nil, true
	}
	m.invalidateConnFieldList()
	m.connDlg.listSeq++
	params := m.connDlg.toConnection()
	m.connDlg.listKey = f.key
	m.connDlg.listParams = params
	instance, seq := m.connDlg.instance, m.connDlg.listSeq
	return m, tea.Batch(
		m.setMessage(m.t(i18n.KeyAppLoading)),
		m.loadConnFieldOptions(f.key, instance, seq, params),
	), true
}

// connOptionsMsg carries a server-provided list back to the open form. Every
// response is bound to both the form instance and the parameter snapshot that
// produced it.
type connOptionsMsg struct {
	key      i18n.Key
	values   []string
	err      error
	instance int64
	seq      int64
	params   settings.Connection
}

// connListerOpen is the driver opener used to read server lists; a variable so
// tests can inject a fake connection.
var connListerOpen = driver.Open

// loadConnFieldOptions reads the values of one listable field from the server.
// The query runs in the background; the form stays responsive and keeps what
// was typed while the list is on its way.
func (m Model) loadConnFieldOptions(key i18n.Key, instance, seq int64, params settings.Connection) tea.Cmd {
	conn := params
	kind := conn.Driver
	password, err := resolveConnectionPassword(conn)
	if err != nil {
		return func() tea.Msg {
			return connOptionsMsg{key: key, err: err, instance: instance, seq: seq, params: params}
		}
	}
	if kind == "postgres" {
		// The database list is read through the maintenance database: the
		// one the user names may not exist yet (that is exactly what this
		// list helps them get right), and the connection also lists its
		// schemas through the database the form names, so it defaults to the
		// maintenance database when the field is still empty.
		if key == i18n.KeyConnDatabase || strings.TrimSpace(conn.Database) == "" {
			conn.Database = "postgres"
		}
	}
	cfg := driverConfig(conn, password)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()
		opened, err := connListerOpen(ctx, kind, cfg)
		if err != nil {
			return connOptionsMsg{key: key, err: err, instance: instance, seq: seq, params: params}
		}
		defer opened.Close()
		var values []string
		switch key {
		case i18n.KeyConnDatabase:
			lister, ok := opened.(driver.ServerLister)
			if !ok {
				return connOptionsMsg{key: key, err: fmt.Errorf("%s cannot list databases", kind), instance: instance, seq: seq, params: params}
			}
			values, err = lister.Databases(ctx)
		case i18n.KeyConnSchema:
			lister, ok := opened.(driver.SchemaLister)
			if !ok {
				return connOptionsMsg{key: key, err: fmt.Errorf("%s cannot list schemas", kind), instance: instance, seq: seq, params: params}
			}
			values, err = lister.Schemas(ctx)
		default:
			return connOptionsMsg{key: key, err: fmt.Errorf("no list for %s", key), instance: instance, seq: seq, params: params}
		}
		return connOptionsMsg{key: key, values: values, err: err, instance: instance, seq: seq, params: params}
	}
}

// connFocusField returns the index of the field with key, if the form has one.
func (m Model) connFocusField(key i18n.Key) (int, bool) {
	for i := range m.connDlg.fields {
		if m.connDlg.fields[i].key == key {
			return i, true
		}
	}
	return 0, false
}

// applyConnOptions stores a server-provided list on its field and opens it when
// the field is still the one under the caret. The schema list starts with the
// database default: a connection may name no schema at all, and that entry
// makes the choice explicit instead of forcing a name.
func (m *Model) applyConnOptions(msg connOptionsMsg) {
	m.connDlg.listKey = ""
	m.connDlg.listParams = settings.Connection{}
	idx, ok := m.connFocusField(msg.key)
	if !ok {
		return
	}
	field := &m.connDlg.fields[idx]
	if msg.key == i18n.KeyConnSchema {
		field.options = append([]string{""}, msg.values...)
	} else {
		field.options = msg.values
	}
	if idx == m.connDlg.focus {
		m.openConnChoice()
	}
}

// applyConnChoice rebuilds the form for the driver currently chosen in the type
// field (the fields depend on the type), keeping everything that was typed.
// The focus stays on the type field.
func (m *Model) applyConnChoice() {
	value := m.connDlg.fields[m.connDlg.focus].value
	m.connDlg.fields = buildConnFields(value, m.connDlg.fields)
	m.connDlg.focus = min(m.connDlg.focus, len(m.connDlg.fields)-1)
}

// openConnChoice opens the option list of the focused field, starting on the
// current value. Choice fields always have options; listable text fields get
// theirs from the server, and while the list is still empty nothing opens.
func (m *Model) openConnChoice() {
	f := &m.connDlg.fields[m.connDlg.focus]
	if len(f.options) == 0 {
		return
	}
	sel := 0
	for i, opt := range f.options {
		if opt == f.value {
			sel = i
		}
	}
	m.connDlg.dropdown = true
	m.connDlg.dropSel = sel
	m.connDlg.dropHover = -1 // no pointer has been placed on the fresh list
}

// closeConnDropdown closes the option list and forgets the pointer position,
// so a hover recorded on the open list cannot outlive it.
func (c *connDialog) closeConnDropdown() {
	c.dropdown = false
	c.dropHover = -1
}

// handleConnChoiceKey handles a key press while the option list is open. The
// list is modal inside the form and consumes every key, so nothing can leak
// into the fields behind it.
func (m Model) handleConnChoiceKey(key string) (Model, tea.Cmd, bool) {
	n := len(m.connDlg.fields[m.connDlg.focus].options)
	if n == 0 { // defensive: nothing to choose from
		m.connDlg.closeConnDropdown()
		return m, nil, true
	}
	switch key {
	case "esc":
		// Esc closes the list only; the form stays open.
		m.connDlg.closeConnDropdown()
	case "up":
		m.connDlg.dropSel = ((m.connDlg.dropSel-1)%n + n) % n
	case "down":
		m.connDlg.dropSel = (m.connDlg.dropSel + 1) % n
	case "home":
		m.connDlg.dropSel = 0
	case "end":
		m.connDlg.dropSel = n - 1
	case "tab", "shift+tab":
		// Tab closes the list and moves on to the next field.
		m.connDlg.closeConnDropdown()
		if key == "tab" {
			m.moveConnFocus(1)
		} else {
			m.moveConnFocus(-1)
		}
	case "enter", "space":
		return m.selectConnChoice()
	}
	// Any other key (←/→ included: their cycling fallback belongs to the
	// closed form) is swallowed by the list.
	return m, nil, true
}

// selectConnChoice picks the highlighted option. The value stays the option
// ID. The database type is the one choice the form is built around, so only
// its pick rebuilds the form (keeping what was typed); the SSL mode and other
// selects just keep their value.
func (m Model) selectConnChoice() (Model, tea.Cmd, bool) {
	f := &m.connDlg.fields[m.connDlg.focus]
	before := f.value
	if len(f.options) > 0 {
		f.value = f.options[clamp(m.connDlg.dropSel, 0, len(f.options)-1)]
		f.caret = len([]rune(f.value))
	}
	if f.value != before && f.key != i18n.KeyConnName {
		m.invalidateConnFieldList()
	}
	m.connDlg.closeConnDropdown()
	if f.key == i18n.KeyConnDriver {
		m.applyConnChoice()
	}
	if f.key == i18n.KeyConnDatabase {
		// The schemas of the previous database say nothing about the new one:
		// drop both the typed value and the cached list, so ↓ reads them from
		// the database that is now selected.
		if idx, ok := m.connFocusField(i18n.KeyConnSchema); ok {
			schema := &m.connDlg.fields[idx]
			schema.value = ""
			schema.options = nil
			schema.caret = 0
		}
	}
	return m, nil, true
}

// cycle steps through the options of a choice field.
func (f *connField) cycle(delta int) {
	if len(f.options) == 0 {
		return
	}
	i := 0
	for n, opt := range f.options {
		if opt == f.value {
			i = n
		}
	}
	n := len(f.options)
	f.value = f.options[((i+delta)%n+n)%n]
}

// submitConnDialog validates the form and connects (or saves, when editing).
func (m Model) submitConnDialog() (Model, tea.Cmd, bool) {
	// The form is itself a connection-forming entry point: a locked vault
	// refuses it even though the guards on the menu and tree already keep
	// the form from opening (see vaultBlocked).
	if cmd, blocked := m.vaultBlocked(); blocked {
		return m, cmd, true
	}
	conn := m.connDlg.toConnection()
	if conn.Name == "" {
		return m, m.setMessage(m.t(i18n.KeyConnNameRequired)), true
	}
	original, editing := m.connDlg.originalName, m.connDlg.editing
	m.closeConnDialog()
	if !editing {
		return m, m.connectTo(conn), true
	}

	// Editing replaces the old entry (the name may have changed). Editing the
	// active connection reconnects, so the new parameters take effect; editing
	// another one only saves it.
	m.replaceSavedConnection(original, conn)
	if original != "" && original == m.conn.Name {
		return m, m.connectTo(conn), true
	}
	return m, tea.Batch(
		m.setMessage(m.t(i18n.KeyConnSaved, conn.Name)),
		m.persistSettings(),
	), true
}

// handleConnMouse handles a mouse event while the form is open.
func (m Model) handleConnMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	// Motion is handled before anything else: it belongs to the open option
	// list (a hovered row is highlighted and nothing else happens) and, with
	// the list closed, it belongs to the form and does nothing at all. Either
	// way a motion may never press a button, pick a value or reach the panes
	// behind the modal dialog.
	if msg.Action == tea.MouseActionMotion {
		m.connDlg.dropHover = -1
		if m.connDlg.dropdown {
			if idx, ok := m.connDropRowAt(msg.X, msg.Y); ok {
				m.connDlg.dropHover = idx
			}
		}
		return m, nil
	}
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}
	// The open option list is a layer of the form: a click inside picks the
	// option, a click anywhere else only closes it — the same click must never
	// also press a form button or focus a field behind the list.
	if m.connDlg.dropdown {
		if idx, ok := m.connDropRowAt(msg.X, msg.Y); ok {
			m.connDlg.dropSel = idx
			next, cmd, _ := m.selectConnChoice()
			return next, cmd
		}
		m.connDlg.closeConnDropdown()
		return m, nil
	}
	l := m.connLayout()
	switch {
	case l.connect.Contains(msg.X, msg.Y):
		next, cmd, _ := m.submitConnDialog()
		return next, cmd
	case l.cancel.Contains(msg.X, msg.Y):
		m.closeConnDialog()
		return m, nil
	}
	if row, ok := m.connRowAt(msg.X, msg.Y); ok {
		m.connDlg.focus = row
		if m.connDlg.fields[row].kind == fieldChoice {
			// The type row is a dropdown: clicking it opens the list.
			m.openConnChoice()
		}
	}
	return m, nil
}

// ---- Rendering ----

// renderConnDialog renders the form and returns its screen origin plus the
// block of lines to blit.
func (m Model) renderConnDialog() (x, y int, block []string, ok bool) {
	if !m.connDlg.open {
		return 0, 0, nil, false
	}
	l := m.connLayout()
	if l.w < 4 || l.h < 3 {
		return 0, 0, nil, false
	}
	border, box := m.theme.dlgBorder, m.theme.dlgBox
	rule := strings.Repeat("─", l.w-2)

	out := make([]string, 0, l.h)
	out = append(out, border.Render("┌"+rule+"┐"))
	titleKey := i18n.KeyConnTitle
	if m.connDlg.editing {
		titleKey = i18n.KeyConnEditTitle
	}
	hintKey := m.connHintKey()
	title := padANSI(truncateANSI(" "+m.t(titleKey), l.innerW), l.innerW)
	out = append(out, border.Render("│")+m.theme.dlgTitle.Render(title)+border.Render("│"))

	// The body rows are exactly what connLayout.rows describes: fields and
	// the section titles above them, drawn in the same order the mouse hit
	// test reads them. Rows the box is too short to show are not drawn and
	// stay out of the hit test, so the drawn form and the clickable form
	// agree whatever the terminal size.
	for r := 0; r < l.drawn; r++ {
		row := l.rows[r]
		var line string
		if row.field < 0 {
			if row.spacer {
				// The blank row between the groups: padding, not content.
				line = box.Render(strings.Repeat(" ", l.innerW))
			} else {
				line = m.renderConnHeader(row.section, l)
			}
		} else {
			f := m.connDlg.fields[row.field]
			line = m.renderConnField(row.field, f, l)
		}
		out = append(out, border.Render("│")+padANSI(truncateANSI(line, l.innerW), l.innerW)+border.Render("│"))
	}
	blank := border.Render("│") + box.Render(strings.Repeat(" ", l.innerW)) + border.Render("│")

	// Blank padding, the hint on the row above the buttons, and the buttons on
	// the last content row: the drawn rows of that frame part and the
	// l.connect/l.cancel rectangles agree, since both share connLayout.
	for len(out) < l.h-3 {
		out = append(out, blank)
	}
	hint := padANSI(truncateANSI(m.t(hintKey), l.innerW), l.innerW)
	out = append(out, border.Render("│")+m.theme.dlgDesc.Render(hint)+border.Render("│"))

	connect := m.theme.toolBtnHover.Render(" " + m.t(m.connPrimaryKey()) + " ")
	cancel := m.theme.toolBtn.Render(" " + m.t(i18n.KeyConnCancel) + " ")
	row := strings.Repeat(" ", l.connect.X-l.x-1) + connect + "  " + cancel
	out = append(out, border.Render("│")+padANSI(truncateANSI(row, l.innerW), l.innerW)+border.Render("│"))
	out = append(out, border.Render("└"+rule+"┘"))
	return l.x, l.y, out, true
}

// renderConnDropdown renders the open option list of the focused choice
// field. The block is blitted on top of the form by View.
func (m Model) renderConnDropdown() (x, y int, block []string, ok bool) {
	r, top, visible, ok := m.connDropdownLayout()
	if !ok || r.W < 4 || r.H < 3 {
		return 0, 0, nil, false
	}
	f := m.connDlg.fields[m.connDlg.focus]
	options := f.options
	inner := r.W - 2
	border := m.theme.menuBorder

	out := make([]string, 0, r.H)
	out = append(out, border.Render("┌"+strings.Repeat("─", inner)+"┐"))
	for i := 0; i < visible && top+i < len(options); i++ {
		style := m.theme.menuBox
		// The row under the pointer wears the selection look, exactly like
		// the keyboard highlight of dropSel; hovering changes nothing but the
		// drawing, so the two may sit on different rows.
		if top+i == m.connDlg.dropSel || top+i == m.connDlg.dropHover {
			style = m.theme.menuItemSel
		}
		label := padANSI(truncateANSI(m.connChoiceLabel(f, options[top+i]), inner-2), inner-2)
		out = append(out, border.Render("│")+style.Render(" "+label+" ")+border.Render("│"))
	}
	out = append(out, border.Render("└"+strings.Repeat("─", inner)+"┘"))
	return r.X, r.Y, out, true
}

// renderConnHeader draws a section title: the box's quiet style, indented one
// cell like a field row so the titles line up with the labels under them.
// Nothing in this row is interactive; connRowAt reports "no field here" for it.
func (m Model) renderConnHeader(section i18n.Key, l connLayout) string {
	return m.theme.dlgBox.Render(" ") +
		m.theme.dlgDesc.Render(padANSI(m.t(section), l.labelW)) +
		m.theme.dlgBox.Render(strings.Repeat(" ", max(0, l.innerW-l.labelW-1)))
}

// renderConnField renders one field row: the label, the value and a caret in
// the focused field.
func (m Model) renderConnField(i int, f connField, l connLayout) string {
	focused := i == m.connDlg.focus
	labelStyle, valueStyle, padStyle := m.theme.dlgLabel, m.theme.dlgValue, m.theme.dlgBox
	caret := " "
	if focused {
		labelStyle, valueStyle, padStyle = m.theme.dlgRowSel, m.theme.dlgValueSel, m.theme.dlgRowSel
		caret = "▸"
	}

	text := padStyle.Render(caret) +
		labelStyle.Render(padANSI(m.t(f.key), l.labelW)) +
		padStyle.Render("  ")

	// The value is drawn from the cleaned text: it can be prefilled from a
	// settings file or a catalog, and the form must not be the place an
	// escape sequence reaches the terminal (the export dialog's path field
	// does the same). The caret counts runes of the stored value, so it is
	// mapped onto the cleaned text by counting the printable runes in front
	// of it — the position the caret names stays the position the caret
	// points at, and the clamp below only guards a caret that the editing
	// itself already put out of range.
	raw := m.connValueText(f)
	shown := stripControl(raw)
	switch {
	case focused && f.kind != fieldChoice:
		// Long values scroll horizontally: a window of valueW-1 cells slides
		// so the caret cell stays visible (one cell of the column is reserved
		// for it). The rule is the export dialog's path field: the window
		// starts at the caret minus the window, so the caret sits at the right
		// end while typing forward and walks back on ⌂/←. There is no
		// ellipsis marker: the label column never moves, so a value that slid
		// instead of widening the box already shows there is text beyond an
		// end.
		runes := []rune(shown)
		rawRunes := []rune(raw)
		at := clamp(f.caret, 0, len(rawRunes))
		at = clamp(len([]rune(stripControl(string(rawRunes[:at])))), 0, len(runes))
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
	default:
		// A listable field shows a corner marker: ↓ opens the values the
		// server holds, and typing stays possible for names it does not
		// offer. The marker sits outside the value so the caret keeps its
		// position while typing.
		display := shown
		if f.listable {
			display = truncateANSI(display, max(1, l.valueW-2)) + " ▾"
		}
		text += valueStyle.Render(padANSI(display, l.valueW))
	}
	return text + padStyle.Render(" ")
}
