package ui

// confirm_dialog.go implements the modal execution confirmation: the dialog
// that stands between the user and a destructive statement (DDL, or a write
// that can hit every row of a table).
//
//   - ⏎ (or y) runs the statements it was opened for; Esc (or n) cancels.
//   - The dialog is modal: it swallows every other key and click, so nothing
//     leaks into the editor and a stray click can never confirm a DROP.
//   - The same box asks "N pending changes will be lost" before closing a tab,
//     closing tabs in bulk, quitting or switching the connection drops them
//     (see confirmLoss). Accepting it performs the action, not a query.
//
// The classification itself lives in sql/risk.go; this file only presents it.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
	"github.com/p-v-q/tdb/sql"
)

// maxConfirmPreviews is how many dangerous statements are listed verbatim;
// the rest are counted.
const maxConfirmPreviews = 3

// confirmKind says what accepting the dialog does: run the batch it carries,
// or drop pending grid changes and take the lossy action it protects.
type confirmKind int

const (
	// confirmRun is the execution confirmation: accept runs confirm.stmts.
	confirmRun confirmKind = iota
	// confirmLoss is the "N pending changes will be lost" question: accept
	// runs the action in confirm.then.
	confirmLoss
)

// confirmLossAction is the action a confirmLoss dialog performs on accept.
type confirmLossAction int

const (
	confirmLossNone confirmLossAction = iota
	confirmLossCloseTab
	confirmLossCloseOthers
	confirmLossCloseLeft
	confirmLossCloseRight
	confirmLossCloseAll
	confirmLossQuit
	confirmLossReconnect
	confirmLossConnect
)

// confirmDialog is the state of the modal confirmation. It remembers the
// statements exactly as they were classified, so confirming runs what the
// user was shown even if the buffer changed meanwhile.
type confirmDialog struct {
	open  bool
	kind  confirmKind
	label string          // the query the result pane will remember
	stmts []execStmt      // the statements to run on confirmation
	risks []sql.RiskLevel // per-statement classification
	// showAll lists every statement in the dialog, not only the ones the
	// classifier calls dangerous. The grid edit uses it: its statements are
	// ordinary scoped UPDATEs, but the user must read them before they run.
	showAll bool

	// sess and conn bind the dialog to the session and connection it was
	// opened for. Accepting after either changed must not run the batch
	// somewhere else: a background connMsg can make another database live
	// while the dialog is open.
	sess *session
	conn connRef

	// apply is the pending grid batch of a confirmRun dialog. It lives in the
	// dialog, not in the tab's result state, so a background query outcome
	// arriving meanwhile (which resets the result state) cannot silently drop
	// the transaction and row-count safety checks of the batch.
	apply *gridApply

	// loss is how many pending grid changes a confirmLoss dialog protects;
	// then is what accepting it does. target is the tab index it acts on (-1
	// when the action is not tab-specific) and targetConn the connection a
	// confirmLossConnect dialog opens.
	loss       int
	then       confirmLossAction
	target     int
	targetConn settings.Connection
}

// confirmPreview is one dangerous statement shown in the dialog.
type confirmPreview struct {
	level sql.RiskLevel
	text  string // first line of the statement
	args  string // the values the statement is bound to
}

// openConfirm opens the dialog for a classified batch of statements.
func (m *Model) openConfirm(label string, stmts []execStmt, risks []sql.RiskLevel) {
	m.openConfirmWith(label, stmts, risks, false)
}

// openConfirmAll opens the dialog and lists every statement in it, not only
// the dangerous ones. It is used for the statements the user must read
// before they touch the data, whatever the classifier thinks of them.
func (m *Model) openConfirmAll(label string, stmts []execStmt, risks []sql.RiskLevel) {
	m.openConfirmWith(label, stmts, risks, true)
}

// openApplyConfirm opens the dialog for the batch of pending grid changes.
// The batch travels with the dialog (and not with the tab's result state), so
// an unrelated query outcome cannot take its transaction and row-count checks
// away while the user decides.
func (m *Model) openApplyConfirm(label string, stmts []execStmt, risks []sql.RiskLevel, apply *gridApply) {
	m.openModalConfirm(confirmDialog{
		kind: confirmRun, label: label, stmts: stmts, risks: risks,
		showAll: true, apply: apply,
	})
}

// openConfirmWith opens the dialog for a batch of statements.
func (m *Model) openConfirmWith(label string, stmts []execStmt, risks []sql.RiskLevel, showAll bool) {
	m.openModalConfirm(confirmDialog{kind: confirmRun, label: label, stmts: stmts, risks: risks, showAll: showAll})
}

// openLossConfirm asks before an action drops pending grid changes. It names
// how many changes would be lost, so a stray Ctrl+W or Ctrl+Q cannot discard
// work the grid is still showing as pending.
func (m *Model) openLossConfirm(then confirmLossAction, loss, target int, conn settings.Connection) {
	m.openModalConfirm(confirmDialog{
		kind: confirmLoss, loss: loss, then: then, target: target, targetConn: conn,
	})
}

// openModalConfirm closes the other overlays and opens the dialog, bound to
// the session and connection that are live when it opens.
func (m *Model) openModalConfirm(d confirmDialog) {
	m.closeMenu()
	m.closeObjectsMenu()
	m.closeResultMenu()
	m.closeSettings()
	m.closeConnDialog()
	m.closeExportDialog()
	m.closeHistoryDialog()
	d.open = true
	d.sess = m.session
	d.conn = m.connRef()
	if d.apply != nil {
		// The batch runs on the connection it was built for, not on whatever
		// is connected when the user confirms.
		d.apply.conn = d.conn
	}
	m.confirm = d
}

// closeConfirm closes the dialog without running anything.
func (m *Model) closeConfirm() { m.confirm = confirmDialog{} }

// acceptConfirm closes the dialog and runs the statements the user vouched
// for, or performs the lossy action a loss confirmation protected.
//
// Nothing runs before the binding is checked again: the dialog remembers the
// session and connection it was opened for, and a batch built from one
// database must never be written through another one.
func (m Model) acceptConfirm() (Model, tea.Cmd, bool) {
	if !m.confirmLayout().accept {
		// Keep the question open so a terminal resize can make the real button
		// available again; never execute a batch from a clipped frame.
		return m, m.setMessage(m.t(i18n.KeyConfirmTooSmall)), true
	}
	if m.confirm.kind == confirmLoss {
		return m.acceptLossConfirm()
	}
	label, stmts := m.confirm.label, m.confirm.stmts
	apply, sess := m.confirm.apply, m.confirm.sess
	conn := m.confirm.conn
	m.closeConfirm()

	if !conn.same(m.connRef()) {
		// A switch, a reconnect or a lost connection replaced the session
		// while the dialog was open: the batch belongs to the old one.
		return m, m.setMessage(m.confirmLostBatchText(apply)), true
	}
	if apply != nil && (sess == nil || !m.hasTab(sess)) {
		// The tab the changes belong to is gone; there is no grid left to
		// apply them for.
		return m, m.setMessage(m.t(i18n.KeyResultEditWrongConnection)), true
	}
	return m, m.executeStatements(label, stmts, apply), true
}

// confirmLostBatchText is the message for a confirmation that was dropped
// because the connection it was opened for is no longer the live one.
func (m Model) confirmLostBatchText(apply *gridApply) string {
	if apply != nil {
		return m.t(i18n.KeyResultEditWrongConnection)
	}
	return m.t(i18n.KeyMsgQueryCancelled)
}

// hasTab reports whether the session is still one of the open tabs.
func (m Model) hasTab(sess *session) bool {
	if sess == nil {
		return false
	}
	for _, s := range m.tabs {
		if s == sess {
			return true
		}
	}
	return false
}

// cancelConfirm closes the dialog and reports the cancellation.
func (m Model) cancelConfirm() (Model, tea.Cmd, bool) {
	loss := m.confirm.kind == confirmLoss
	m.closeConfirm()
	if loss {
		// The question protected a lossy action; cancelling simply leaves the
		// workspace as it was, so there is nothing to report.
		return m, nil, true
	}
	return m, m.setMessage(m.t(i18n.KeyMsgQueryCancelled)), true
}

// acceptLossConfirm performs the action a loss confirmation protected.
func (m Model) acceptLossConfirm() (Model, tea.Cmd, bool) {
	d := m.confirm
	m.closeConfirm()
	// The workspace may have changed while the question was open (a
	// background connection switch): the question no longer describes the
	// state it was asked about, so it is dropped.
	if !d.conn.same(m.connRef()) {
		return m, m.setMessage(m.t(i18n.KeyMsgQueryCancelled)), true
	}
	switch d.then {
	case confirmLossCloseTab:
		m.forceCloseTabAt(d.target)
	case confirmLossCloseOthers:
		m.forceCloseTabsExcept(d.target)
	case confirmLossCloseLeft:
		m.forceCloseTabsToLeft(d.target)
	case confirmLossCloseRight:
		m.forceCloseTabsToRight(d.target)
	case confirmLossCloseAll:
		m.forceCloseAllTabs()
	case confirmLossQuit:
		return m, m.quitNow(), true
	case confirmLossReconnect:
		return m, m.reconnectNow(), true
	case confirmLossConnect:
		return m, m.connectToAs(d.targetConn, false, ""), true
	}
	return m, nil, true
}

// ---- Content ----

// confirmTitle is the title of the dialog: the execution confirmation keeps
// its own, a loss question names what accepting it discards.
func (m Model) confirmTitle() string {
	if m.confirm.kind == confirmLoss {
		return m.t(i18n.KeyResultDiscardChanges)
	}
	return m.t(i18n.KeyConfirmTitle)
}

// confirmAcceptLabel is the label of the accept button.
func (m Model) confirmAcceptLabel() string {
	if m.confirm.kind == confirmLoss {
		return m.confirmLossAcceptLabel()
	}
	return m.t(i18n.KeyConfirmRun)
}

// confirmHint is the key hint under the dialog body.
func (m Model) confirmHint() string {
	if m.confirm.kind == confirmLoss {
		// The hint names the two keys with the same labels the buttons carry,
		// so no wording of the execution confirmation leaks into a close
		// question.
		return " ⏎ " + m.confirmLossAcceptLabel() + "  Esc " + m.t(i18n.KeyConnCancel) + " "
	}
	return m.t(i18n.KeyConfirmHint)
}

// confirmLossAcceptLabel names the action a loss confirmation is about.
func (m Model) confirmLossAcceptLabel() string {
	switch m.confirm.then {
	case confirmLossCloseTab:
		return m.t(i18n.KeyTabClose)
	case confirmLossCloseOthers:
		return m.t(i18n.KeyTabCloseOthers)
	case confirmLossCloseLeft:
		return m.t(i18n.KeyTabCloseLeft)
	case confirmLossCloseRight:
		return m.t(i18n.KeyTabCloseRight)
	case confirmLossCloseAll:
		return m.t(i18n.KeyTabCloseAll)
	case confirmLossQuit:
		return m.t(i18n.KeyMenuQuit)
	case confirmLossReconnect:
		return m.t(i18n.KeyConnReconnect)
	case confirmLossConnect:
		return m.t(i18n.KeyObjMenuConnect)
	}
	return m.t(i18n.KeyResultDiscardChanges)
}

// confirmLossSummary says how many pending changes the question is about.
func (m Model) confirmLossSummary() string {
	if m.confirm.loss == 1 {
		return m.t(i18n.KeyResultOnePendingChange)
	}
	return m.t(i18n.KeyResultPendingChanges, m.confirm.loss)
}

// confirmSummary describes the batch: how many statements, how many write,
// how many asked for this dialog.
func (m Model) confirmSummary() string {
	writes, dangerous := 0, 0
	for _, r := range m.confirm.risks {
		if r.Writes() {
			writes++
		}
		if r.NeedsConfirmation() {
			dangerous++
		}
	}
	if len(m.confirm.stmts) == 1 {
		return m.t(i18n.KeyConfirmOneSummary, writes, dangerous)
	}
	return m.t(i18n.KeyConfirmSummary, len(m.confirm.stmts), writes, dangerous)
}

// confirmPreviews returns the first maxConfirmPreviews dangerous statements,
// each with its localized risk label and the values it is bound to. A dialog
// that shows every statement (the grid edit) lists them all.
func (m Model) confirmPreviews() []confirmPreview {
	out := make([]confirmPreview, 0, maxConfirmPreviews)
	for i, r := range m.confirm.risks {
		if !m.confirm.showAll && !r.NeedsConfirmation() {
			continue
		}
		if len(out) == maxConfirmPreviews {
			break
		}
		text, args := "", ""
		if i < len(m.confirm.stmts) {
			// The statement is drawn as plain text, so the control
			// characters a table name may carry are dropped.
			text = firstLine(stripControl(m.confirm.stmts[i].Text))
			args = confirmArgsLine(m.cfg.Dialect, m.confirm.stmts[i])
		}
		out = append(out, confirmPreview{level: r, text: text, args: args})
	}
	return out
}

// confirmHidden counts the statements not shown verbatim because the preview
// list is full.
func (m Model) confirmHidden() int {
	n := 0
	for _, r := range m.confirm.risks {
		if m.confirm.showAll || r.NeedsConfirmation() {
			n++
		}
	}
	return max(0, n-maxConfirmPreviews)
}

// confirmTargetLine names the relations the batch touches, so the user knows
// what the statements are about before they run.
//
// The names are read from the statements themselves; a statement whose target
// cannot be read contributes nothing, and the line is left out entirely
// rather than naming something wrong.
func (m Model) confirmTargetLine() string {
	targets := confirmTargets(m.confirm.stmts)
	if len(targets) == 0 {
		return ""
	}
	return m.t(i18n.KeyCtxTable) + ": " + strings.Join(targets, ", ")
}

// confirmLine is one line of the dialog body: the text and the style it is
// drawn in.
type confirmLine struct {
	style lipgloss.Style
	text  string
}

// confirmBodyLines returns the lines of the dialog between the title and the
// buttons: the summary of the batch, the warning, the relations it touches,
// the statements themselves with their bound values, and the count of the
// statements that are not shown.
//
// The layout and the renderer share them, so the drawn box always fits what
// it shows.
func (m Model) confirmBodyLines(previews []confirmPreview) []confirmLine {
	if m.confirm.kind == confirmLoss {
		// The question is about pending work that never reaches the
		// database: the count and the action say everything there is to say.
		return []confirmLine{
			{m.theme.dlgBox, m.confirmLossSummary()},
			{m.theme.dlgDesc, m.confirmLossAcceptLabel()},
		}
	}
	lines := []confirmLine{
		{m.theme.dlgBox, m.confirmSummary()},
		{m.theme.dlgDesc, m.t(i18n.KeyConfirmWarning)},
	}
	if target := m.confirmTargetLine(); target != "" {
		lines = append(lines, confirmLine{m.theme.dlgDesc, target})
	}
	for _, p := range previews {
		lines = append(lines, confirmLine{
			m.theme.dlgLabel, "⚠ " + m.confirmRiskLabel(p.level) + "  " + p.text,
		})
		if p.args != "" {
			lines = append(lines, confirmLine{m.theme.dlgDesc, "   " + p.args})
		}
	}
	if n := m.confirmHidden(); n > 0 {
		lines = append(lines, confirmLine{m.theme.dlgDesc, m.t(i18n.KeyConfirmMore, n)})
	}
	return lines
}

// confirmRiskLabel is the localized name of a risk level in the previews. A
// plain write (an UPDATE/DELETE that names its rows, the grid apply included)
// is labelled "write", not "unsafe": the unsafe label belongs to statements
// that can touch data they never named.
func (m Model) confirmRiskLabel(r sql.RiskLevel) string {
	switch r {
	case sql.RiskDDL:
		return m.t(i18n.KeyRiskDDL)
	case sql.RiskDangerousWrite:
		return m.t(i18n.KeyRiskDangerousWrite)
	default:
		return m.t(i18n.KeyRiskWrite)
	}
}

// firstLine returns the first line of a statement, for the previews.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// ---- Geometry ----

// confirmLayout is the computed geometry of the dialog. Rendering and mouse
// hit testing share it, so the drawn buttons are the clickable buttons.
type confirmLayout struct {
	x, y, w, h int // outer box, borders included
	innerW     int // content width between the borders
	accept     bool
	run        Rect
	cancel     Rect
}

func (m Model) confirmLayout() confirmLayout {
	previews := m.confirmPreviews()
	body := m.confirmBodyLines(previews)
	innerW := lipgloss.Width(m.confirmTitle())
	for _, b := range body {
		innerW = max(innerW, lipgloss.Width(b.text))
	}
	innerW = max(innerW, lipgloss.Width(m.confirmHint()))
	if len(previews) > 0 {
		innerW += 2 // one space of padding on each side
	}
	runW := lipgloss.Width(m.confirmAcceptLabel()) + 2
	cancelW := lipgloss.Width(m.t(i18n.KeyConnCancel)) + 2
	innerW = max(innerW, 1+runW+2+cancelW)

	// The shared modal rules apply. The content decides the wanted size (the
	// dialog hugs what it must show), but the box is never narrower than the
	// shared minimum and never wider than min(screen-4, 100): a DROP must be
	// readable in one glance, not stretched over the whole terminal.
	area := m.paneRects().area
	w, h := modalSize(innerW+2, 7+len(body), area.W, area.H)
	innerW = max(0, w-2) // the guaranteed minimum is filled by the body background

	// Centre it in the area between the menu bar and the status bar.
	r := modalRect(area, w, h)
	x, y := r.X, r.Y

	// Acceptance needs a complete frame with a title, the two essential body
	// lines and a fully drawn accept button. Below that floor the dialog stays
	// open with a minimal warning and only Cancel/Esc can dismiss it.
	accept := h >= 6 && w >= 4 && innerW >= runW+1
	btnY := y + h - 2
	rowX, rowEnd := x+1, x+1+innerW
	clip := func(r Rect) Rect {
		if r.X > rowEnd {
			r.X = rowEnd
		}
		if r.X+r.W > rowEnd {
			r.W = rowEnd - r.X
		}
		return r
	}
	run, cancel := Rect{X: rowX, Y: btnY}, Rect{X: rowX, Y: btnY}
	if accept {
		run = clip(Rect{X: x + 2, Y: btnY, W: runW, H: 1})
		cancel = clip(Rect{X: x + 2 + runW + 2, Y: btnY, W: cancelW, H: 1})
	} else if h >= 4 && cancelW <= innerW {
		// The minimal frame prioritizes the warning, then a fully drawn Cancel
		// button when there is a row for it. There is deliberately no Run hit
		// rectangle at all.
		cancel = Rect{X: x + 2, Y: btnY, W: cancelW, H: 1}
	}
	return confirmLayout{
		x: x, y: y, w: w, h: h, innerW: innerW, accept: accept,
		run: run, cancel: cancel,
	}
}

// ---- Interaction ----

// handleConfirmKey handles a key press while the dialog is open and reports
// whether it consumed the key. The dialog is modal, so it consumes
// everything.
func (m Model) handleConfirmKey(key string) (Model, tea.Cmd, bool) {
	switch key {
	case "enter", "y":
		return m.acceptConfirm()
	case "esc", "n":
		return m.cancelConfirm()
	}
	return m, nil, true
}

// handleConfirmMouse handles a press while the dialog is open. Only the two
// buttons do anything; every other click is swallowed.
func (m Model) handleConfirmMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}
	l := m.confirmLayout()
	switch {
	case l.run.Contains(msg.X, msg.Y):
		next, cmd, _ := m.acceptConfirm()
		return next, cmd
	case l.cancel.Contains(msg.X, msg.Y):
		next, cmd, _ := m.cancelConfirm()
		return next, cmd
	}
	return m, nil
}

// ---- Rendering ----

// renderConfirmDialog renders the dialog and returns its screen origin plus
// the block of lines to blit.
func (m Model) renderConfirmDialog() (x, y int, block []string, ok bool) {
	if !m.confirm.open {
		return 0, 0, nil, false
	}
	l := m.confirmLayout()
	warning := m.t(i18n.KeyConfirmTooSmall)
	if l.w < 3 || l.h < 2 {
		// A one-column/one-row terminal cannot draw a frame. Still return
		// exactly the declared block and show as much of the warning as fits.
		out := make([]string, l.h)
		for row := range out {
			text := ""
			if row == 0 {
				text = warning
			}
			out[row] = padANSI(truncateANSI(text, l.w), l.w)
		}
		return l.x, l.y, out, true
	}

	border, box := m.theme.dlgBorder, m.theme.dlgBox
	rule := strings.Repeat("─", l.w-2)
	line := func(style lipgloss.Style, text string) string {
		return border.Render("│") +
			style.Render(padANSI(truncateANSI(text, l.innerW), l.innerW)) +
			border.Render("│")
	}
	run := m.theme.toolBtnHover.Render(" " + m.confirmAcceptLabel() + " ")
	cancel := m.theme.toolBtn.Render(" " + m.t(i18n.KeyConnCancel) + " ")
	buttonRow := func() string {
		row := ""
		switch {
		case l.run.W > 0:
			row = strings.Repeat(" ", max(0, l.run.X-l.x-1)) + run
			if l.cancel.W > 0 {
				gap := max(0, l.cancel.X-(l.run.X+l.run.W))
				row += strings.Repeat(" ", gap) + cancel
			}
		case l.cancel.W > 0:
			row = strings.Repeat(" ", max(0, l.cancel.X-l.x-1)) + cancel
		}
		return border.Render("│") + padANSI(truncateANSI(row, l.innerW), l.innerW) + border.Render("│")
	}

	out := make([]string, 0, l.h)
	out = append(out, border.Render("┌"+rule+"┐"))
	capacity := l.h - 2 // rows between the top and bottom borders
	if !l.accept {
		// Minimal safe frame: warning first, Cancel last only when both rows
		// exist. No accept affordance is rendered in this mode.
		hasCancel := l.cancel.W > 0 && capacity >= 2
		bodyRows := capacity
		if hasCancel {
			bodyRows--
		}
		if bodyRows > 0 {
			out = append(out, line(m.theme.dlgDesc, " "+warning))
			bodyRows--
		}
		for ; bodyRows > 0; bodyRows-- {
			out = append(out, line(box, ""))
		}
		if hasCancel {
			out = append(out, buttonRow())
		}
	} else {
		body := m.confirmBodyLines(m.confirmPreviews())
		priority := body
		if len(priority) > 2 {
			priority = priority[:2] // summary/action and warning
		}
		beforeButtons := capacity - 1 // the last content row is the button row
		out = append(out, line(m.theme.dlgTitle, " "+m.confirmTitle()))
		beforeButtons--
		switch {
		case beforeButtons >= len(body)+2: // blank + full body + hint
			out = append(out, line(box, ""))
			for _, b := range body {
				out = append(out, line(b.style, " "+b.text))
			}
			out = append(out, line(m.theme.dlgDesc, m.confirmHint()))
		case beforeButtons >= len(priority)+1: // essential body + hint
			for _, b := range priority {
				out = append(out, line(b.style, " "+b.text))
			}
			out = append(out, line(m.theme.dlgDesc, m.confirmHint()))
		default: // h == 6: title + the two essential body lines
			for _, b := range priority {
				out = append(out, line(b.style, " "+b.text))
			}
		}
		for len(out) < l.h-2 { // top border plus all content except the button row
			out = append(out, line(box, ""))
		}
		out = append(out, buttonRow())
	}
	out = append(out, border.Render("└"+rule+"┘"))
	return l.x, l.y, out, true
}
