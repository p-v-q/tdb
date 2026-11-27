package ui

// confirm_dialog_test.go covers what the execution confirmation shows: the
// statements, the values they are bound to and the relations they touch. The
// statement alone does not tell the user what will happen — an UPDATE shows
// its placeholders, not the value that goes into them.

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/sql"
)

// confirmModel opens the confirmation dialog for a batch of statements.
func confirmModel(t *testing.T, stmts []execStmt, risks []sql.RiskLevel) Model {
	t.Helper()
	m := sized("SELECT 1")
	m.openConfirmAll("the batch", stmts, risks)
	if !m.confirm.open {
		t.Fatal("the dialog must be open")
	}
	return m
}

// confirmView returns the plain text of the dialog.
func confirmView(m Model) string {
	return stripANSI(m.View())
}

// ---- The bound values ----

// TestConfirmShowsTheBoundValues pins that the user sees the value that is
// about to be written, not only the placeholder of the statement.
func TestConfirmShowsTheBoundValues(t *testing.T) {
	m := confirmModel(t, []execStmt{{
		Text: `UPDATE "main"."users" SET "email" = $1 WHERE "id" = $2`,
		Args: []any{"new@example.com", "1"},
	}}, []sql.RiskLevel{sql.RiskWrite})

	view := confirmView(m)
	if !strings.Contains(view, "new@example.com") {
		t.Fatalf("the dialog must show the value:\n%s", view)
	}
	if !strings.Contains(view, "$1 = 'new@example.com'") {
		t.Fatalf("the dialog must bind the value to its placeholder:\n%s", view)
	}
	if !strings.Contains(view, "$2 = '1'") {
		t.Fatalf("the dialog must show the key value:\n%s", view)
	}
}

// TestConfirmShowsNullForANilArgument pins that a NULL is told apart from the
// empty string: they are not the same thing.
func TestConfirmShowsNullForANilArgument(t *testing.T) {
	m := confirmModel(t, []execStmt{{
		Text: `UPDATE "main"."users" SET "name" = $1 WHERE "id" = $2`,
		Args: []any{nil, "1"},
	}}, []sql.RiskLevel{sql.RiskWrite})

	view := confirmView(m)
	if !strings.Contains(view, "$1 = NULL") {
		t.Fatalf("the dialog must show NULL:\n%s", view)
	}
	if strings.Contains(view, "$1 = ''") {
		t.Fatalf("a NULL must not look like an empty string:\n%s", view)
	}
}

// TestConfirmShowsAnEmptyStringAsAnEmptyString is the other half of the NULL
// case: the empty string is a value of its own.
func TestConfirmShowsAnEmptyStringAsAnEmptyString(t *testing.T) {
	m := confirmModel(t, []execStmt{{
		Text: `UPDATE "main"."users" SET "name" = $1 WHERE "id" = $2`,
		Args: []any{"", "1"},
	}}, []sql.RiskLevel{sql.RiskWrite})

	view := confirmView(m)
	if !strings.Contains(view, "$1 = ''") {
		t.Fatalf("the dialog must show the empty string:\n%s", view)
	}
}

// TestConfirmShortensALongValue pins that a long value does not blow the
// dialog up: it is cut, and the cut is visible.
func TestConfirmShortensALongValue(t *testing.T) {
	long := strings.Repeat("x", 200)
	m := confirmModel(t, []execStmt{{
		Text: `UPDATE "main"."users" SET "note" = $1`,
		Args: []any{long},
	}}, []sql.RiskLevel{sql.RiskWrite})

	view := confirmView(m)
	if strings.Contains(view, long) {
		t.Fatalf("the value must be shortened:\n%s", view)
	}
	if !strings.Contains(view, "…") {
		t.Fatalf("the shortened value must say that it was cut:\n%s", view)
	}
}

// TestConfirmShowsNoValuesForAPlainStatement pins that a statement without
// arguments has nothing to show: the dialog stays as it was.
func TestConfirmShowsNoValuesForAPlainStatement(t *testing.T) {
	m := confirmModel(t, []execStmt{{Text: "DROP TABLE users"}},
		[]sql.RiskLevel{sql.RiskDangerousWrite})

	if args := m.confirmPreviews()[0].args; args != "" {
		t.Fatalf("the preview = %q, want no values", args)
	}
}

// ---- The batch ----

// TestConfirmShowsTheTotalWhenStatementsAreHidden pins that the user learns
// how many statements the batch runs, not only how many are shown.
func TestConfirmShowsTheTotalWhenStatementsAreHidden(t *testing.T) {
	stmts := make([]execStmt, 0, 5)
	risks := make([]sql.RiskLevel, 0, 5)
	for i := 0; i < 5; i++ {
		stmts = append(stmts, execStmt{Text: "DROP TABLE t" + string(rune('a'+i))})
		risks = append(risks, sql.RiskDangerousWrite)
	}
	m := confirmModel(t, stmts, risks)

	view := confirmView(m)
	if !strings.Contains(view, "5 statement(s)") {
		t.Fatalf("the dialog must show the total number of statements:\n%s", view)
	}
	if !strings.Contains(view, "and 2 more") {
		t.Fatalf("the dialog must say how many statements are not shown:\n%s", view)
	}
}

// TestConfirmNamesTheTargetTable pins that the dialog says which relation the
// batch writes to.
func TestConfirmNamesTheTargetTable(t *testing.T) {
	m := confirmModel(t, []execStmt{{
		Text: `UPDATE "main"."users" SET "email" = $1 WHERE "id" = $2`,
		Args: []any{"new@example.com", "1"},
	}}, []sql.RiskLevel{sql.RiskWrite})

	view := confirmView(m)
	if !strings.Contains(view, "main.users") {
		t.Fatalf("the dialog must name the table it writes to:\n%s", view)
	}
}

// TestConfirmTargetTableReadsTheRelation pins which relation the dialog names
// for the statements it knows.
func TestConfirmTargetTableReadsTheRelation(t *testing.T) {
	cases := []struct {
		stmt string
		want string
	}{
		{`UPDATE "main"."users" SET "email" = $1 WHERE "id" = $2`, "main.users"},
		{"DELETE FROM users", "users"},
		{"INSERT INTO users (name) VALUES ($1)", "users"},
		{"DROP TABLE users", "users"},
		{"ALTER TABLE users ADD COLUMN x INT", "users"},
		{"TRUNCATE users", "users"},
		{"CREATE INDEX idx ON users (name)", "users"},
		{"SELECT * FROM users", ""},
		{"DROP INDEX idx", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := confirmTargetTable(tc.stmt); got != tc.want {
			t.Errorf("confirmTargetTable(%q) = %q, want %q", tc.stmt, got, tc.want)
		}
	}
}

// ---- The grid edit ----

// TestGridEditConfirmationShowsTheNewValue pins the whole path: the value the
// user typed is in the confirmation, so the user can see what is about to be
// written before it is written.
// TestConfirmDialogHeightSafety pins the short-terminal contract: the drawn
// block never grows past its declared layout, the accept control disappears
// before there is room to show it safely, and keyboard acceptance is disabled
// whenever the button is not drawn.
func TestConfirmDialogHeightSafety(t *testing.T) {
	for _, width := range []int{1, 6, 20} {
		for height := 1; height <= 12; height++ {
			t.Run(screenName(width, height), func(t *testing.T) {
				m := dialogModel(t, width, height)
				m.openConfirmAll("the batch", []execStmt{
					{Text: "DROP TABLE users"},
					{Text: "DROP TABLE orders"},
					{Text: "DROP TABLE audit"},
				}, []sql.RiskLevel{sql.RiskDDL, sql.RiskDDL, sql.RiskDDL})

				l := m.confirmLayout()
				x, y, block, ok := m.renderConfirmDialog()
				if !ok {
					t.Fatal("even a tiny terminal must render a minimal confirmation warning")
				}
				if len(block) != l.h {
					t.Fatalf("rendered %d rows, layout declares %d", len(block), l.h)
				}
				if x != l.x || y != l.y {
					t.Fatalf("render origin = %d,%d, layout = %d,%d", x, y, l.x, l.y)
				}
				for row, line := range block {
					if got := lipgloss.Width(line); got != l.w {
						t.Fatalf("row %d width = %d, want %d", row, got, l.w)
					}
				}

				if !l.accept {
					if l.run.W != 0 || l.run.H != 0 {
						t.Fatalf("a hidden accept button still has a hit rectangle: %+v", l.run)
					}
					next, _, _ := m.handleConfirmKey("enter")
					if !next.confirm.open {
						t.Fatal("Enter accepted a dangerous batch without a drawn accept button")
					}
					next, _ = m.handleConfirmMouse(tea.MouseMsg{
						X: l.run.X, Y: l.run.Y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress,
					})
					if !next.confirm.open {
						t.Fatal("a click in the blank button row accepted a dangerous batch")
					}
					view := stripANSI(strings.Join(block, "\n"))
					if l.w >= 8 && l.h >= 3 && !strings.Contains(view, "Resize") {
						t.Fatalf("the minimal warning must explain how to recover:\n%s", view)
					}
				} else if row := l.run.Y - y; row >= 0 && row < len(block) {
					if !strings.Contains(stripANSI(block[row]), m.confirmAcceptLabel()) {
						t.Fatalf("the accept hit rectangle does not point at its drawn button:\n%s", block[row])
					}
				} else {
					t.Fatalf("accept hit rectangle row %d is outside the rendered block", l.run.Y-y)
				}
			})
		}
	}
}

func TestConfirmDialog80x12KeepsWarningAndButtonsVisible(t *testing.T) {
	m := dialogModel(t, 80, 12)
	m.openConfirmAll("the batch", []execStmt{
		{Text: "DROP TABLE users"},
		{Text: "DROP TABLE orders"},
		{Text: "DROP TABLE audit"},
	}, []sql.RiskLevel{sql.RiskDDL, sql.RiskDDL, sql.RiskDDL})

	l := m.confirmLayout()
	_, _, block, ok := m.renderConfirmDialog()
	if !ok || len(block) != l.h {
		t.Fatalf("render ok=%v rows=%d, want %d", ok, len(block), l.h)
	}
	view := stripANSI(strings.Join(block, "\n"))
	for _, want := range []string{"Confirm execution", "This script can modify or drop data.", "Run anyway", "Cancel"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the 80x12 confirmation is missing %q:\n%s", want, view)
		}
	}
	if !l.accept || l.run.Y != l.y+l.h-2 || l.cancel.Y != l.y+l.h-2 {
		t.Fatalf("buttons are not safely available at 80x12: %+v", l)
	}
}

func TestGridEditConfirmationShowsTheNewValue(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	cat, err := conn.Catalog(context.Background())
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	m.cat = cat
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}
	m.layout.focus = paneResults
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight}) // the email column

	// Open the cell editor, replace its content and commit the change.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if m.session.result.editing == nil {
		t.Fatalf("the cell editor did not open: %q", m.status.message)
	}
	for i := 0; i < len([]rune(m.session.result.editing.text)); i++ {
		m = send(m, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	m = typeText(m, "new@example.com")
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})

	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if !m.confirm.open {
		t.Fatal("applying the changes must ask for confirmation")
	}
	view := confirmView(m)
	if !strings.Contains(view, "new@example.com") {
		t.Fatalf("the dialog must show the value that is about to be written:\n%s", view)
	}
	if !strings.Contains(view, "users") {
		t.Fatalf("the dialog must name the table it writes to:\n%s", view)
	}
}
