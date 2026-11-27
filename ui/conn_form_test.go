package ui

// conn_form_test.go covers the round-6 rework of the connection form: the
// fields are grouped under quiet section headers (Server, Credentials,
// Database, Advanced), the SSL mode is a per-driver dropdown, the box follows
// the shared modal geometry with a capped value column, and long values scroll
// horizontally instead of widening the form.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
)

// screenName labels a terminal size for subtests and failure messages.
func screenName(w, h int) string { return fmt.Sprintf("%dx%d", w, h) }

// connFormRowKeys flattens the body rows of the open form into readable pairs:
// a header row ("", <section>) or a field row (<field key>, "").
func connFormRowKeys(m Model) [][2]i18n.Key {
	l := m.connLayout()
	out := make([][2]i18n.Key, len(l.rows))
	for r, row := range l.rows {
		if row.field < 0 {
			out[r] = [2]i18n.Key{"", row.section}
		} else {
			out[r] = [2]i18n.Key{m.connDlg.fields[row.field].key, ""}
		}
	}
	return out
}

// connFormSections returns the sections the rows currently declare, in order.
// The blank rows between the groups are not sections and are skipped.
func connFormSections(m Model) []i18n.Key {
	var out []i18n.Key
	for _, row := range connFormRowKeys(m) {
		if row[0] == "" && row[1] != "" {
			out = append(out, row[1])
		}
	}
	return out
}

// ---- Section grouping ----

func TestConnDialogGroupsFieldsUnderSectionHeaders(t *testing.T) {
	// A tall screen: the whole form, Advanced group included, is drawn on it.
	m := dialogModel(t, 120, 40)
	m.openConnDialog()

	// Demo: the form asks for name and type only and shows no empty group
	// title (the leading pair deliberately belongs to no section).
	if got := connFormSections(m); len(got) != 0 {
		t.Fatalf("demo sections = %v, want none", got)
	}
	if rows := connFormRowKeys(m); len(rows) != 2 || rows[0][0] != i18n.KeyConnName || rows[1][0] != i18n.KeyConnDriver {
		t.Fatalf("demo rows = %v, want name and type only", rows)
	}

	// Postgres: the four groups in order; the leading Name/Type rows carry no
	// section. The groups since round-7: the SSL mode is transport security
	// and lives in Server, next to host and port, not with the names.
	m.connDlg.fields = buildConnFields("postgres", nil)
	want := []i18n.Key{
		i18n.KeyConnSectionServer, i18n.KeyConnSectionCredentials,
		i18n.KeyConnSectionDatabase, i18n.KeyConnSectionAdvanced,
	}
	got := connFormSections(m)
	if len(got) != len(want) {
		t.Fatalf("postgres sections = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("postgres section %d = %v, want %v", i, got[i], want[i])
		}
	}
	rows := connFormRowKeys(m)
	if rows[0][0] != i18n.KeyConnName || rows[1][0] != i18n.KeyConnDriver {
		t.Fatalf("the leading rows must stay Name/Type, got %v", rows[:2])
	}
	// Server → Host, Port, SSL mode.
	if rows[2][1] != i18n.KeyConnSectionServer ||
		rows[3][0] != i18n.KeyConnHost || rows[4][0] != i18n.KeyConnPort || rows[5][0] != i18n.KeyConnSSLMode {
		t.Fatalf("server group = %v, want host, port, ssl mode", rows[2:6])
	}
	// A blank row separates the Server title from the Credentials one (the
	// first title of the form skips it).
	if !connRowIsSpacer(rows[6]) || rows[7][1] != i18n.KeyConnSectionCredentials {
		t.Fatalf("between the groups = %v, want a blank then the Credentials title", rows[6:8])
	}
	// Credentials → User, Password, Password env.
	if rows[8][0] != i18n.KeyConnUser || rows[9][0] != i18n.KeyConnPassword || rows[10][0] != i18n.KeyConnPasswordEnv {
		t.Fatalf("credentials group = %v, want user, password, password env", rows[8:11])
	}
	if !connRowIsSpacer(rows[11]) || rows[12][1] != i18n.KeyConnSectionDatabase {
		t.Fatalf("between the groups = %v, want a blank then the Database title", rows[11:13])
	}
	// Database → Database name, Default schema.
	if rows[13][0] != i18n.KeyConnDatabase || rows[14][0] != i18n.KeyConnSchema {
		t.Fatalf("database group = %v, want database and schema", rows[13:15])
	}
	if !connRowIsSpacer(rows[15]) || rows[16][1] != i18n.KeyConnSectionAdvanced {
		t.Fatalf("between the groups = %v, want a blank then the Advanced title", rows[15:17])
	}
	if rows[17][0] != i18n.KeyConnDSN {
		t.Fatalf("advanced group = %v, want the DSN", rows[17])
	}
	// The titles are drawn (the view names the groups in English).
	if view := stripANSI(m.View()); !strings.Contains(view, "Server") ||
		!strings.Contains(view, "Credentials") || !strings.Contains(view, "Advanced") {
		t.Fatalf("the form must draw its section titles:\n%s", view)
	}
}

// connRowIsSpacer reports whether a flattened row pair marks one of the blank
// rows between the groups (field-less, section-less).
func connRowIsSpacer(row [2]i18n.Key) bool {
	return row[0] == "" && row[1] == ""
}

func TestConnDialogPerDriverGrouping(t *testing.T) {
	m := sized("")

	// MySQL/MariaDB: the SSL mode is offered, the default schema is not.
	m.connDlg.fields = buildConnFields("mysql", nil)
	if !connFieldHas(m.connDlg.fields, i18n.KeyConnSSLMode) {
		t.Fatal("the mysql form must ask for the SSL mode")
	}
	if connFieldHas(m.connDlg.fields, i18n.KeyConnSchema) {
		t.Fatal("the mysql form must not ask for a default schema")
	}
	if sections := connFormSections(m); len(sections) != 4 {
		t.Fatalf("mysql sections = %v, want the four server groups", sections)
	}
	// SQLite: only the file, under the Database header.
	m.connDlg.fields = buildConnFields("sqlite", nil)
	rows := connFormRowKeys(m)
	if len(rows) != 4 ||
		rows[0][0] != i18n.KeyConnName || rows[1][0] != i18n.KeyConnDriver ||
		rows[2][1] != i18n.KeyConnSectionDatabase || rows[3][0] != i18n.KeyConnFile {
		t.Fatalf("sqlite rows = %v, want name, type, Database header, file", rows)
	}
}

// TestConnDialogSectionHeadersAreNotFocusable pins that the group titles are
// padding: ↑/↓/Tab walk the fields only, a mouse click on a title row does
// nothing, and connRowAt reports no field for them.
func TestConnDialogSectionHeadersAreNotFocusable(t *testing.T) {
	m := sized("")
	m.openConnDialogFor(settings.Connection{Name: "shop", Driver: "postgres"})
	l := m.connLayout()

	// The first title row is right below the type field. Its screen row maps
	// to no field, and clicking it keeps the focus where it was.
	titleY := 0
	for r, row := range l.rows {
		if row.field < 0 {
			titleY = l.rowY + r
		}
	}
	if titleY == 0 {
		t.Fatal("the postgres form must declare section titles")
	}
	if _, ok := m.connRowAt(l.valueX, titleY); ok {
		t.Fatal("connRowAt must refuse a section title row")
	}
	m = send(m, mousePress(l.valueX, titleY))
	if m.connDlg.focus != 0 {
		t.Fatalf("clicking a title moved the focus to %d, want no change", m.connDlg.focus)
	}
	if m.connDlg.dropdown {
		t.Fatal("clicking a title must not open the choice list")
	}

	// ↑/↓/Tab visit only fields, wrapping like before; a full Tab round trip
	// lands back on the start.
	start := m.connDlg.focus
	for i := 0; i < len(m.connDlg.fields); i++ {
		m = keyByName(m, "tab")
	}
	if m.connDlg.focus != start {
		t.Fatalf("a full Tab round trip moved the focus to %d, want back at %d", m.connDlg.focus, start)
	}
	m = keyByName(m, "up")
	m = keyByName(m, "down")
	if m.connDlg.focus != start {
		t.Fatalf("up then down moved the focus to %d, want %d", m.connDlg.focus, start)
	}

	// The field right after the first title stays reachable by both paths.
	var fieldAfterTitle int
	for r, row := range l.rows {
		if r > 0 && row.field >= 0 && l.rows[r-1].field < 0 {
			fieldAfterTitle = row.field
			break // the first title, not the last
		}
	}
	if idx, ok := m.connRowAt(l.valueX, l.rowY+l.fieldRow[fieldAfterTitle]); !ok || idx != fieldAfterTitle {
		t.Fatalf("connRowAt after the title = %d, %v, want the host field", idx, ok)
	}
	m = send(m, mousePress(l.valueX, l.rowY+l.fieldRow[fieldAfterTitle]))
	if m.connDlg.focus != fieldAfterTitle {
		t.Fatalf("focus = %d, want the first field after the title", m.connDlg.focus)
	}
}

// TestConnDialogSpacerRowsAreBlankAndTitlesQuiet pins how the groups read on
// screen: the blank row in front of every title but the first draws nothing
// but padding, and no title row carries the focus caret — a group title is
// chrome (the quiet dlgDesc style), never a field.
func TestConnDialogSpacerRowsAreBlankAndTitlesQuiet(t *testing.T) {
	m := dialogModel(t, 120, 40)
	m.connDlg = connDialog{open: true, fields: buildConnFields("postgres", nil)}
	_, _, block, ok := m.renderConnDialog()
	if !ok {
		t.Fatal("the form must render")
	}
	l := m.connLayout()
	spacers, titles := 0, 0
	for r, row := range l.rows {
		if r >= l.drawn {
			break
		}
		// Block index r+2: the top border and the title line come first.
		// The borders are stripped with the spaces: an empty padded row is
		// "│ ··· │" after the ANSI cut.
		line := strings.Trim(stripANSI(block[r+2]), "│ ")
		switch {
		case row.spacer:
			spacers++
			if line != "" {
				t.Fatalf("the blank row between the groups shows %q, want empty", line)
			}
		case row.field < 0 && !row.spacer:
			titles++
			if strings.Contains(line, "▸") {
				t.Fatalf("the title row %q carries a caret", line)
			}
		}
	}
	if spacers != 3 {
		t.Fatalf("the postgres form has %d spacer rows, want 3", spacers)
	}
	if titles != 4 {
		t.Fatalf("the postgres form has %d title rows, want 4", titles)
	}
}

// TestConnDialogMiniFormsAreCompact pins the compact sizing of the smallest
// forms: with a field or three the full 12-row minimum would pad the form
// with blank rows (measured five on a real terminal), so the compact floor
// of eight applies. The width stays at the shared minimum.
func TestConnDialogMiniFormsAreCompact(t *testing.T) {
	m := dialogModel(t, 120, 40)
	m.connDlg.fields = buildConnFields("demo", nil)
	l := m.connLayout()
	if l.w != modalMinW {
		t.Fatalf("demo box width = %d, want the shared minimum %d", l.w, modalMinW)
	}
	if blanks := l.h - (len(l.rows) + 6); blanks > 2 {
		t.Fatalf("demo box = %d rows for %d content rows, want at most 2 blank", l.h, len(l.rows))
	}
	if l.h < modalMinHCompact {
		t.Fatalf("demo box = %d rows, want at least the compact floor %d", l.h, modalMinHCompact)
	}

	// The sqlite form has one field more (and its first — and only — group
	// title): still compact, still at the width minimum.
	m.connDlg.fields = buildConnFields("sqlite", nil)
	l = m.connLayout()
	if blanks := l.h - (len(l.rows) + 6); blanks > 2 {
		t.Fatalf("sqlite box = %d rows for %d content rows, want at most 2 blank", l.h, len(l.rows))
	}
	if l.w != modalMinW {
		t.Fatalf("sqlite box width = %d, want the shared minimum %d", l.w, modalMinW)
	}
}

// ---- The SSL mode ----

func TestConnDialogSSLModesFollowTheDriver(t *testing.T) {
	cases := map[string][]string{
		"postgres": {"", "disable", "allow", "prefer", "require", "verify-ca", "verify-full"},
		// go-sql-driver has no tls=require (unknown values fail the connection
		// open); skip-verify carries the ssl-mode=REQUIRED semantics instead.
		"mysql":   {"", "false", "preferred", "skip-verify", "true"},
		"mariadb": {"", "false", "preferred", "skip-verify", "true"},
	}
	for driverName, want := range cases {
		fields := buildConnFields(driverName, nil)
		idx := connFieldIndex(fields, i18n.KeyConnSSLMode)
		if idx < 0 {
			t.Fatalf("the %s form must ask for the SSL mode", driverName)
		}
		if got := fields[idx].options; strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("the %s SSL options = %v, want %v", driverName, got, want)
		}
	}
	// Regression: require is a pgx sslmode word. go-sql-driver's tls parameter
	// has no require — a saved connection with one fails its open with
	// "unknown config name: require" — so it must never come back.
	fields := buildConnFields("mysql", nil)
	got := fields[connFieldIndex(fields, i18n.KeyConnSSLMode)].options
	if strings.Contains("|"+strings.Join(got, "|")+"|", "|require|") {
		t.Fatalf("the mysql SSL options = %v, must not offer require", got)
	}
	for _, driverName := range []string{"demo", "sqlite", "sqlserver"} {
		if connFieldHas(buildConnFields(driverName, nil), i18n.KeyConnSSLMode) {
			t.Fatalf("the %s form must not ask for the SSL mode", driverName)
		}
	}
}

func TestConnDialogSSLModeRoundTrip(t *testing.T) {
	// Editing a saved connection shows the stored mode and keeps it.
	m := sized("")
	m.openConnDialogFor(settings.Connection{Name: "shop", Driver: "postgres", SSLMode: "require"})
	if got := connFieldValue(m, i18n.KeyConnSSLMode); got != "require" {
		t.Fatalf("the ssl field = %q, want require", got)
	}
	if got := m.connDlg.toConnection().SSLMode; got != "require" {
		t.Fatalf("the saved ssl mode = %q, want require", got)
	}
	if cfg := driverConfig(m.connDlg.toConnection(), ""); !strings.Contains(cfg.DSN, "sslmode=require") {
		t.Fatalf("the composed DSN = %q, want the sslmode parameter", cfg.DSN)
	}

	// Picking another mode from the dropdown replaces it.
	ssl := connFieldIndex(m.connDlg.fields, i18n.KeyConnSSLMode)
	m.connDlg.focus = ssl
	m = keyByName(m, "enter")
	if !m.connDlg.dropdown {
		t.Fatal("Enter on the SSL field must open its option list")
	}
	m = keyByName(m, "end") // verify-full, the last postgres mode
	m = keyByName(m, "enter")
	if got := connFieldValue(m, i18n.KeyConnSSLMode); got != "verify-full" {
		t.Fatalf("the ssl field = %q, want verify-full after the pick", got)
	}
	if got := m.connDlg.toConnection().SSLMode; got != "verify-full" {
		t.Fatalf("the saved ssl mode = %q, want verify-full", got)
	}

	// Switching the driver resets a value the new vocabulary does not know.
	m.connDlg.dropdown = false
	m = setConnDriver(m, "mysql")
	if got := connFieldValue(m, i18n.KeyConnSSLMode); got != "" {
		t.Fatalf("the ssl field = %q after switching to mysql, want the default", got)
	}
}

func TestConnDialogSSLDefaultWordAndNoLeak(t *testing.T) {
	m := dialogModel(t, 120, 40)
	m.openConnDialog()
	m = setConnDriver(m, "postgres")
	ssl := connFieldIndex(m.connDlg.fields, i18n.KeyConnSSLMode)
	m.connDlg.focus = ssl

	// The empty mode is spelled out as "default" — a UI concept, not a keyword.
	if view := stripANSI(m.View()); !strings.Contains(view, "default") {
		t.Fatalf("the empty ssl mode must show the default word:\n%s", view)
	}
	if label := m.connChoiceLabel(m.connDlg.fields[ssl], ""); label != "default" {
		t.Fatalf("the empty option label = %q, want default", label)
	}
	if label := m.connChoiceLabel(m.connDlg.fields[ssl], "require"); label != "require" {
		t.Fatalf("the require label = %q, want the keyword verbatim", label)
	}

	// The open list is modal within the form: no key reaches the fields.
	keys := connFieldKeys(m)
	snapshot := make([]string, len(m.connDlg.fields))
	for i, f := range m.connDlg.fields {
		snapshot[i] = f.value
	}
	m = keyByName(m, "enter")
	if !m.connDlg.dropdown {
		t.Fatal("Enter on the ssl field must open its option list")
	}
	m = typeText(m, "zzz")
	if got := connFieldValue(m, i18n.KeyConnSSLMode); got != "" {
		t.Fatalf("typing with the list open leaked into the ssl field: %q", got)
	}
	for i, f := range m.connDlg.fields {
		if f.value != snapshot[i] && f.key != i18n.KeyConnDriver {
			t.Fatalf("typing with the list open leaked into %v (%v → %q)", f.key, keys[i], f.value)
		}
	}
}

// ---- SSL mode composing ----

func TestComposeDSNAppendsSSLQueryParameters(t *testing.T) {
	// PostgreSQL composes sslmode=…
	c := settings.Connection{
		Driver: "postgres", Host: "db.example", Port: 5432, Database: "shop",
		SSLMode: " require ", // the form trims; composeDSN defends too
	}
	if got, want := composeDSN(c, ""), "postgres://db.example:5432/shop?sslmode=require"; got != want {
		t.Fatalf("composeDSN = %q, want %q", got, want)
	}
	// MySQL/MariaDB compose tls=…
	c = settings.Connection{Driver: "mysql", Host: "db.example", Port: 3306, Database: "shop", SSLMode: "skip-verify"}
	if got, want := composeDSN(c, ""), "mysql://db.example:3306/shop?tls=skip-verify"; got != want {
		t.Fatalf("composeDSN = %q, want %q", got, want)
	}
	// An empty mode (and drivers without the field) compose no parameters.
	c = settings.Connection{Driver: "postgres", Host: "db.example", Port: 5432, Database: "shop"}
	if got := composeDSN(c, ""); strings.Contains(got, "?") {
		t.Fatalf("composeDSN = %q, want no query without a mode", got)
	}
	c = settings.Connection{Driver: "mysql", Host: "db.example"}
	if got := composeDSN(c, ""); strings.Contains(got, "?") {
		t.Fatalf("composeDSN = %q, want no query without a mode", got)
	}
}

// TestDriverConfigKeepsAHandWrittenDSNWithSSLMode pins that a hand-written DSN
// wins wholesale: the SSL mode does not sneak into the DSN text (it still
// applies where it belongs — as a query parameter of a composed DSN only).
func TestDriverConfigKeepsAHandWrittenDSNWithSSLMode(t *testing.T) {
	c := settings.Connection{
		Driver: "postgres", DSN: "postgres://ada@example.test/shop",
		SSLMode: "verify-full", Schema: "sales", Host: "ignored",
	}
	cfg := driverConfig(c, "")
	if cfg.DSN != "postgres://ada@example.test/shop" {
		t.Fatalf("cfg.DSN = %q, want the hand-written DSN untouched", cfg.DSN)
	}
	if strings.Contains(cfg.DSN, "sslmode") {
		t.Fatal("the composed parameters must not leak into a hand-written DSN")
	}
}

// ---- The capped value column and its horizontal scroll ----

func TestConnDialogLongValueScrollsHorizontally(t *testing.T) {
	// A wide screen so the whole form, DSN row included, is on it.
	m := dialogModel(t, 120, 40)
	m.openConnDialogFor(settings.Connection{Name: "shop", Driver: "postgres"})

	dsn := "secretStart" + strings.Repeat("x", 70) + "secretEnd"
	idx := connFieldIndex(m.connDlg.fields, i18n.KeyConnDSN)
	m.connDlg.focus = idx
	m.connDlg.fields[idx].value = dsn
	m.connDlg.fields[idx].caret = len([]rune(dsn))

	// The value column measures generously from the content but gets capped:
	// with the long DSN on the form it sits at the cap.
	l := m.connLayout()
	if l.valueW != connValueMaxW {
		t.Fatalf("valueW = %d, want the %d-column cap", l.valueW, connValueMaxW)
	}

	view := stripANSI(m.View())
	if !strings.Contains(view, "secretEnd") {
		t.Fatalf("the window must show the tail around the caret:\n%s", view)
	}
	if strings.Contains(view, "secretStart") {
		t.Fatalf("the head beyond the window must not be shown:\n%s", view)
	}

	// Moving the caret home brings the head back into view.
	m.connDlg.fields[idx].caret = 0
	view = stripANSI(m.View())
	if !strings.Contains(view, "secretStart") || strings.Contains(view, "secretEnd") {
		t.Fatalf("⌂ must slide the window back to the head:\n%s", view)
	}
}

// ---- The shared geometry ----

func TestConnDialogFitsAndClicksTheDrawnButtons(t *testing.T) {
	// A long DSN on purpose: the value column must scroll, not widen the box.
	big := settings.Connection{
		Name:   "shop",
		Driver: "postgres",
		User:   "ada",
		DSN:    "postgres://ada:verylongsecret@example.test:5432/shop?sslmode=verify-full",
		Schema: "sales",
	}
	for _, dim := range [][2]int{{40, 80}, {120, 40}} {
		width, height := dim[0], dim[1]
		t.Run(screenName(width, height), func(t *testing.T) {
			m := dialogModel(t, width, height)
			m.openConnDialogFor(big)
			area := m.paneRects().area
			l := m.connLayout()
			if l.w > area.W || l.h > area.H {
				t.Fatalf("the %dx%d box does not fit the area %dx%d", l.w, l.h, area.W, area.H)
			}
			x, y, block, ok := m.renderConnDialog()
			if !ok {
				t.Fatal("the form must render")
			}
			blockInsideRect(t, "connection form", x, y, block, area)
			// The drawn block is exactly the declared box, and the buttons sit
			// on the row their click rectangles claim: a click lands where the
			// user sees the button.
			if len(block) != l.h || lipgloss.Width(block[0]) != l.w {
				t.Fatalf("block %dx%d, want the declared box %dx%d",
					lipgloss.Width(block[0]), len(block), l.w, l.h)
			}
			if l.connect.Y != y+l.h-2 || l.cancel.Y != y+l.h-2 {
				t.Fatalf("button rects at row %d/%d, want the last drawn row %d",
					l.connect.Y, l.cancel.Y, y+l.h-2)
			}
			// A server form is not a compact case: it keeps the shared
			// minimum height whatever the screen.
			if l.h < modalMinH {
				t.Fatalf("the server form is %d rows, want at least %d", l.h, modalMinH)
			}

			// The SSL dropdown opens by click and stays inside the screen. A
			// 40-column screen leaves no value column to the right of the
			// labels, so rows are clicked at a column inside the box.
			rowX := min(l.valueX, l.x+l.w-3)
			ssl := connFieldIndex(m.connDlg.fields, i18n.KeyConnSSLMode)
			m = send(m, mousePress(rowX, l.rowY+l.fieldRow[ssl]))
			if !m.connDlg.dropdown {
				t.Fatal("clicking the ssl row must open its list")
			}
			r, _, _, ok := m.connDropdownLayout()
			if !ok {
				t.Fatal("the open ssl list must have a layout")
			}
			if r.X < 0 || r.X+r.W > m.width || r.Y < 0 || r.Y+r.H > m.height {
				t.Fatalf("the %v dropdown escapes the %dx%d screen", r, m.width, m.height)
			}
			if view := stripANSI(m.View()); !strings.Contains(view, "verify-full") {
				t.Fatalf("the open ssl list must show its options:\n%s", view)
			}

			// A click on the Cancel button: with the list open it only closes
			// the list, with the models list closed it closes the form — both
			// on the drawn coordinates.
			m = send(m, mousePress(l.cancel.X+1, l.cancel.Y))
			if m.connDlg.dropdown {
				t.Fatal("a click outside the dropdown must close it")
			}
			m.connDlg.dropdown = false
			m = send(m, mousePress(l.cancel.X+1, l.cancel.Y))
			if m.connDlg.open {
				t.Fatal("the click on the drawn Cancel must close the form")
			}
		})
	}
}
