package ui

// welcome.go implements the welcome tab (docs/ux-round7.md, §2): the page the
// session starts on while nothing is open, and the page the workspace falls
// back to when the last tab is closed.
//
// The tab is a real session with an empty buffer; only its `welcome` marker is
// set, so tab switching, the tab bar, the pane chrome and every editor helper
// keep working on it untouched. Only two things differ: the body of the editor
// pane is drawn by this file, and a few keys are handled here. Everything else
// turns the tab into a normal editor tab, so the page can never trap a user
// who starts writing.

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/textbuf"
)

// welcomeKind tells what an action row of the welcome page does.
type welcomeKind int

// The action rows.
const (
	welcomeNewQuery   welcomeKind = iota // the tab itself becomes an editor tab
	welcomeOpenFile                      // open the file picker
	welcomeConnection                    // open a saved connection
	welcomeSettings                      // open the settings dialog
	welcomeKeys                          // open the key-bindings overlay
	welcomeNotice                        // informative row, never activated
)

// welcomeItem is one row of the welcome page's action list.
type welcomeItem struct {
	kind  welcomeKind
	label string
	accel string // accelerator printed on the right edge ("" = no accelerator)
	conn  int    // index into settings.Connections for welcomeConnection
}

// welcomeLayout says where the parts of the page sit in the editor body. It is
// computed from the body height, the number of action rows and the selection,
// and rendering, hit testing and the keyboard all read it from the one
// function below — what is drawn, what is clickable and what the arrow keys
// reach can never disagree.
//
// The window may be too short for everything (a 100x20 terminal leaves the
// editor six body rows next to the results pane), so the page shortens in a
// fixed order: the actions, the title, the orientation line and the key hint
// are all kept as long as one row each fits, the decorative blank rows go
// first, and the actions themselves scroll so every one stays reachable. Only
// a pane too short for title + prose + an action drops the prose whole.
type welcomeLayout struct {
	titleRow  int // row of the title (-1 when there is no room at all)
	leadRow   int // row of the orientation line (-1 when dropped)
	firstItem int // row of the first action slot (-1 when no slot fits)
	slots     int // number of action rows that fit on screen
	scroll    int // index of the action drawn in the first slot
	hintRow   int // row of the key hint (-1 when dropped)
}

// layoutWelcome computes the page for a body of bodyH rows holding n actions
// with the selection on sel.
func layoutWelcome(bodyH, n, sel int) welcomeLayout {
	l := welcomeLayout{titleRow: -1, leadRow: -1, firstItem: -1, hintRow: -1}
	if bodyH < 1 {
		return l
	}
	l.titleRow = 0
	if bodyH < 2 {
		return l // a single row: the title alone, nothing can share it
	}
	// The orientation line and the key hint are kept whenever the actions
	// still get a row of their own next to them: they are what tells a
	// newcomer where they landed and how to leave. Below that the page shows
	// bare rows instead of clipping an action away.
	prose := 0
	if bodyH >= 4 {
		prose = 2 // lead + hint
	}
	l.slots = min(n, bodyH-1-prose) // the actions always outrank the prose
	sel = clamp(sel, 0, max(0, n-1))
	if n > l.slots {
		// Scroll the smallest amount that keeps the selected row on screen.
		l.scroll = clamp(sel-l.slots+1, 0, n-l.slots)
	}
	// What is left after title, prose and actions becomes blank rows.
	slack := bodyH - (1 + prose + l.slots)

	row := 1 // right below the title
	if prose >= 1 {
		l.leadRow = row
		row++
		if slack >= 2 { // one blank row sets the lead apart when there is one
			row++
		}
	}
	l.firstItem = row
	if prose >= 2 {
		l.hintRow = bodyH - 1 // the hint sits on the last row of the body
	}
	return l
}

// welcomeBodyHeight is the height of the editor body: the pane minus the title
// row the block above the body draws.
func welcomeBodyHeight(editor Rect) int { return max(0, editor.H-1) }

// newWelcomeSession returns the session behind a welcome tab: an empty buffer
// with the marker set. hover starts at -1 like every other pointer view, so no
// row is highlighted before a pointer has been on the page.
func newWelcomeSession(now time.Time) *session {
	return &session{
		createdAt:    now,
		buf:          textbuf.New(""),
		welcome:      true,
		welcomeSel:   0,
		welcomeHover: -1,
		result:       newResultState(),
	}
}

// welcomeItems builds the action list: what to start, the saved connections
// (or the one line that says why there are none), and where to configure the
// client. It is rebuilt on every read because the list depends on state that
// changes while the page is up — a vault that was just unlocked has entries
// the page did not have a moment ago.
//
// Connections come from settings.Connections on purpose: while the vault is
// sealed that list is empty by construction, so a locked session can never
// leak a connection name through this page (objectsRows has the same rule for
// the object browser).
func (m Model) welcomeItems() []welcomeItem {
	items := []welcomeItem{
		{kind: welcomeNewQuery, label: m.t(i18n.KeyWelcomeNewQuery), accel: "n"},
		{kind: welcomeOpenFile, label: m.t(i18n.KeyMenuOpenFile), accel: "o"},
	}
	switch {
	case m.settings.Locked():
		// The notice replaces the whole connection list. It is not an action:
		// the object browser owns the unlock dialog, and connectTo refuses
		// while the vault is sealed anyway (see vaultBlocked).
		items = append(items, welcomeItem{
			kind:  welcomeNotice,
			label: m.t(i18n.KeyVaultLockedTree),
		})
	case len(m.settings.Connections) == 0:
		items = append(items, welcomeItem{
			kind:  welcomeNotice,
			label: m.t(i18n.KeyWelcomeNoConns),
		})
	default:
		for i, c := range m.settings.Connections {
			items = append(items, welcomeItem{
				kind: welcomeConnection,
				// Name and driver, the way the object browser shows them.
				label: stripControl(c.Name) + " (" + c.Driver + ")",
				conn:  i,
			})
		}
	}
	return append(items,
		welcomeItem{kind: welcomeSettings, label: m.t(i18n.KeyWelcomeSettings), accel: "s"},
		welcomeItem{kind: welcomeKeys, label: m.t(i18n.KeyWelcomeKeys), accel: "f1"},
	)
}

// welcomeGeometry is the layout of the page for the current pane size and
// selection, together with the action rows it is computed from.
func (m Model) welcomeGeometry() (welcomeLayout, []welcomeItem) {
	items := m.welcomeItems()
	return layoutWelcome(welcomeBodyHeight(m.paneRects().editor), len(items), m.welcomeSel), items
}

// welcomeToEditor turns the welcome tab into a normal editor tab. The session
// itself is kept — its empty buffer is exactly what an editor tab starts with
// — so only the marker and the page-only state go away.
//
// The focus is deliberately left alone: the page lives in the editor pane, but
// the keyboard may be somewhere else (the object browser has its own), and
// typing a character into the editor never moves the focus on any other tab
// either.
func (m *Model) welcomeToEditor() {
	if !m.welcome {
		return
	}
	m.welcome = false
	m.welcomeSel = 0
	m.welcomeHover = -1
	m.popup.close()
}

// revealEditor steps the welcome page aside before an action reads or writes
// the buffer behind it. The page stands in front of the text, so an action
// that ran on the hidden text would be invisible: the results of a query the
// user never saw, a buffer rewritten under the page, a name inserted at a
// caret that is not drawn. Callers reveal first and then perform their action
// unchanged, so what they operate on is exactly what is on screen.
//
// It is deliberately not called from welcomeToEditor (that stays a pure
// "turn the page") and never from the page's own actions — welcomeKey and
// welcomeActivate own those, and they keep their current behaviour.
func (m *Model) revealEditor() {
	if m.welcome {
		m.welcomeToEditor()
	}
}

// welcomeStartQuery performs the "New query" action: the page becomes the
// editor and takes the keyboard, because that is what the user asked for by
// activating the row (as opposed to typing, which only converts the tab).
func (m *Model) welcomeStartQuery() {
	m.welcomeToEditor()
	m.layout.focus = paneEditor
}

// ---- Keyboard ----

// welcomeKey handles a key press while the welcome page owns the keyboard. It
// reports whether the key belongs to the page:
//
//   - handled with a command: the page acted on the key (navigation, Enter,
//     Esc, an accelerator);
//   - handled without a command: the key means nothing here and is swallowed,
//     so it cannot half-reach the editor below;
//   - not handled: the key carries text — the caller turns the tab into a
//     normal editor tab and lets the very same press reach the editor.
//
// Everything global (Ctrl+O, Ctrl+T, Ctrl+Q, F1 through the key map, the
// Alt+letter menu accelerators) is resolved before this runs, so the
// accelerators below are the plain letters that only this page owns. F1 needs
// no branch of its own for the same reason: its binding lives in menuDefs and
// reaches the key-bindings overlay through keymapAction.
func (m *Model) welcomeKey(msg tea.KeyMsg, key string) (tea.Cmd, bool) {
	items := m.welcomeItems()
	n := len(items)
	sel := clamp(m.welcomeSel, 0, max(0, n-1))

	switch key {
	case "up", "shift+tab":
		// No wrap-around: the list is a handful of rows and the first one is
		// one press away from the last.
		if n > 0 {
			m.welcomeSel = max(0, sel-1)
		}
		return nil, true
	case "down", "tab":
		if n > 0 {
			m.welcomeSel = min(n-1, sel+1)
		}
		return nil, true
	case "enter":
		return m.welcomeActivate(sel), true
	case "esc":
		// Esc leaves the page only when another tab can take over. Alone it
		// stays, because closing the workspace is Ctrl+Q's job, not Esc's.
		if len(m.tabs) > 1 {
			m.closeTab()
		}
		return nil, true
	case "n":
		// New query: this very tab becomes the editor. Opening another tab
		// instead would leave the welcome page sitting on the left.
		m.welcomeStartQuery()
		return nil, true
	case "o":
		m.openSQLFilePicker()
		return nil, true
	case "s":
		m.openSettings()
		return nil, true
	}
	if msg.Paste || len(msg.Runes) > 0 {
		// The user starts writing: not handled, so the caller converts the
		// tab and the text lands in the buffer below, like in any editor.
		return nil, false
	}
	// A key with no meaning on this page (a caret movement on an empty
	// buffer, a function key without a binding) does nothing at all.
	return nil, true
}

// welcomeActivate runs the action of an action row and returns the command it
// produces. Rows that are not actions (the "none saved" and the locked
// notices) do nothing.
func (m *Model) welcomeActivate(idx int) tea.Cmd {
	items := m.welcomeItems()
	if idx < 0 || idx >= len(items) {
		return nil
	}
	switch it := items[idx]; it.kind {
	case welcomeNewQuery:
		m.welcomeStartQuery()
	case welcomeOpenFile:
		m.openSQLFilePicker()
	case welcomeConnection:
		// connectTo is the same entry point the object browser uses; it
		// refuses while the vault is sealed, and the page never offers a
		// connection row then.
		if it.conn < 0 || it.conn >= len(m.settings.Connections) {
			return nil
		}
		return m.connectTo(m.settings.Connections[it.conn])
	case welcomeSettings:
		m.openSettings()
	case welcomeKeys:
		m.openKeybindings()
	}
	return nil
}

// welcomeItemAt maps a screen coordinate on the editor pane to the action row
// under it, using the same layout the page was drawn with. Only rows that were
// rendered count, so a list clipped by a short window cannot be clicked
// through to an invisible entry, and the pane's title row is not a list row.
func (m Model) welcomeItemAt(rect Rect, x, y int) (int, bool) {
	if !rect.Contains(x, y) || y <= rect.Y {
		return 0, false
	}
	l, items := m.welcomeGeometry()
	if l.firstItem < 0 || l.slots < 1 {
		return 0, false
	}
	row := y - rect.Y - 1
	if row < l.firstItem || row >= l.firstItem+l.slots {
		return 0, false
	}
	idx := l.scroll + row - l.firstItem
	if idx < 0 || idx >= len(items) {
		return 0, false
	}
	return idx, true
}

// ---- Rendering ----

// renderWelcome renders the welcome page into the editor body: title, one line
// of orientation, the action list and the hint. Every row is truncated and
// padded to the pane width, so a narrow window can never see the page cross
// its edge.
func (m Model) renderWelcome(rect Rect) []string {
	body := welcomeBodyHeight(rect)
	l, items := m.welcomeGeometry()
	sel := clamp(m.welcomeSel, 0, max(0, len(items)-1))

	out := make([]string, 0, body)
	for row := 0; row < body; row++ {
		out = append(out, m.welcomeRow(row, l, items, sel, rect.W))
	}
	return out
}

// welcomeRow renders one body row of the welcome page; every row the layout
// does not own comes out blank.
func (m Model) welcomeRow(row int, l welcomeLayout, items []welcomeItem, sel, width int) string {
	switch {
	case row == l.titleRow:
		return welcomeLine(m.theme.appName, m.t(i18n.KeyWelcomeTitle), width)
	case row == l.leadRow:
		return welcomeLine(m.theme.header, m.t(i18n.KeyWelcomeLead), width)
	case row == l.hintRow:
		return welcomeLine(m.theme.paneHint, m.t(i18n.KeyWelcomeHint), width)
	case l.firstItem >= 0 && row >= l.firstItem && row < l.firstItem+l.slots:
		idx := l.scroll + row - l.firstItem
		if idx >= 0 && idx < len(items) {
			return m.welcomeItemRow(items[idx], idx, sel, width)
		}
	}
	return padANSI("", width)
}

// welcomeLine styles one full row of the page (indented like the action rows)
// and fits it to the width.
func welcomeLine(style lipgloss.Style, text string, width int) string {
	return padANSI(truncateANSI(style.Render("  "+stripControl(text)), width), width)
}

// welcomeItemRow renders one action row: the label on the left, the
// accelerator aligned to the right edge, and the highlight of the selected
// row and the hovered row over the whole line.
//
// The highlight wraps the finished row instead of being nested into it: a row
// that carries two styles breaks halfway through when the background changes
// (the object tree renders the same way, see renderObjectRow). sel and hover
// share that one style on purpose — hover only draws and never moves the row
// Enter acts on (round 7 §1).
func (m Model) welcomeItemRow(it welcomeItem, idx, sel, width int) string {
	label := stripControl(it.label)
	indent := "  "

	style := m.theme.objTable
	if it.kind == welcomeNotice {
		// "No saved connections yet" and the locked-vault line are not
		// actions, so they read as a hint rather than as an entry.
		style = m.theme.paneHint
	}
	// The accelerator is aligned to the right edge when the row is wide
	// enough for label and key alike; when it is not, the label takes the
	// whole row — the longer of the two readings is the useful one.
	shown := truncateANSI(label, max(1, width-lipgloss.Width(indent)))
	pad := 0
	if it.accel != "" {
		if room := width - lipgloss.Width(indent) - lipgloss.Width(it.accel) - 2; room >= 1 {
			candidate := truncateANSI(label, room)
			if p := width - lipgloss.Width(indent) - lipgloss.Width(candidate) - lipgloss.Width(it.accel); p >= 1 {
				shown, pad = candidate, p
			}
		}
	}
	// The highlight must cover rows without an accelerator too: a saved
	// connection has none, and without the highlight the keyboard selection
	// would be invisible on that row while Enter still connects to it.
	row := indent + shown
	if pad >= 1 {
		row = indent + shown + strings.Repeat(" ", pad) + it.accel
	}
	if idx == sel || idx == m.welcomeHover {
		return m.theme.menuItemSel.Render(padANSI(truncateANSI(row, width), width))
	}
	if pad < 1 {
		return padANSI(truncateANSI(style.Render(row), width), width)
	}
	return style.Render(indent+shown) +
		strings.Repeat(" ", pad) +
		m.theme.header.Render(it.accel)
}
