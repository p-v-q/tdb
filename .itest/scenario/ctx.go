package scenario

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"tdb-itest/harness"
)

var (
	envOnce sync.Once
	envVal  *Env
	envErr  error
)

// SharedEnv returns the process-wide test environment.
func SharedEnv(t *testing.T) *Env {
	t.Helper()
	envOnce.Do(func() { envVal, envErr = LoadEnv() })
	if envErr != nil {
		t.Fatalf("load environment: %v", envErr)
	}
	return envVal
}

// Ctx is one scenario: a test, its environment, an optional TDB session, and
// the artifact directory that collects screenshots.
type Ctx struct {
	T   *testing.T
	Env *Env
	S   *harness.Session

	artDir string
	seq    int
	close  func()
}

// New creates a scenario context and its artifact directory.
func New(t *testing.T) *Ctx {
	t.Helper()
	env := SharedEnv(t)
	art := filepath.Join(env.ArtRoot, sanitizeName(t.Name()))
	if err := os.MkdirAll(art, 0o755); err != nil {
		t.Fatalf("create artifact dir: %v", err)
	}
	return &Ctx{T: t, Env: env, artDir: art}
}

// ArtifactDir is where this scenario writes its screenshots.
func (c *Ctx) ArtifactDir() string { return c.artDir }

// Start launches TDB with the given arguments and a seeded settings file that
// holds the standard saved connections. Extra settings tweaks can be applied
// with StartWithSettings.
func (c *Ctx) Start(args ...string) *harness.Session {
	c.T.Helper()
	settings := c.Env.WriteSettings(c.T, "main", c.Env.StandardConnections()...)
	return c.StartWithSettings(settings, args...)
}

// StartPlain launches TDB with a pristine settings file and no saved
// connections (the first-run welcome experience).
func (c *Ctx) StartPlain(args ...string) *harness.Session {
	c.T.Helper()
	settings := c.Env.WriteSettings(c.T, "plain")
	return c.StartWithSettings(settings, args...)
}

// StartWithSettings launches TDB with an explicit settings file.
func (c *Ctx) StartWithSettings(settingsPath string, args ...string) *harness.Session {
	c.T.Helper()
	full := append([]string{}, args...)
	full = append(full, "-settings", settingsPath)
	s, err := harness.Start(harness.Options{
		Bin:         c.Env.Bin,
		Args:        full,
		Dir:         c.Env.Root,
		ArtifactDir: c.artDir,
		Cols:        harness.DefaultCols,
		Rows:        harness.DefaultRows,
		Env:         c.childEnv(),
	})
	if err != nil {
		c.T.Fatalf("start tdb %v: %v", args, err)
	}
	c.S = s
	c.T.Cleanup(func() {
		if c.T.Failed() {
			_, _ = s.Snapshot(c.artDir, fmt.Sprintf("%03d-failure", c.seq+1))
		}
		s.Close()
	})
	return s
}

// StartAt is Start with a custom terminal geometry.
func (c *Ctx) StartAt(cols, rows int, settingsPath string, args ...string) *harness.Session {
	c.T.Helper()
	full := append([]string{}, args...)
	full = append(full, "-settings", settingsPath)
	s, err := harness.Start(harness.Options{
		Bin:         c.Env.Bin,
		Args:        full,
		Dir:         c.Env.Root,
		ArtifactDir: c.artDir,
		Cols:        cols,
		Rows:        rows,
		Env:         c.childEnv(),
	})
	if err != nil {
		c.T.Fatalf("start tdb %v: %v", args, err)
	}
	c.S = s
	c.T.Cleanup(func() {
		if c.T.Failed() {
			_, _ = s.Snapshot(c.artDir, fmt.Sprintf("%03d-failure", c.seq+1))
		}
		s.Close()
	})
	return s
}

func (c *Ctx) childEnv() []string {
	return []string{
		"TDB_CONFIG=",
		"HOME=" + filepath.Join(c.Env.ArtRoot, sanitizeName(c.T.Name()), "home"),
	}
}

// Shot snapshots the current screen as HTML, text and PNG and returns the PNG
// path (empty when rasterization failed).
func (c *Ctx) Shot(name string) string {
	c.T.Helper()
	c.seq++
	base := fmt.Sprintf("%03d-%s", c.seq, sanitizeName(name))
	png, err := c.S.Snapshot(c.artDir, base)
	if err != nil {
		c.T.Logf("snapshot %s: %v", base, err)
		return ""
	}
	return png
}

// Keys sends a key sequence and snapshots the result.
func (c *Ctx) Keys(name string, keys string) {
	c.T.Helper()
	if err := c.S.Send(keys); err != nil {
		c.T.Fatalf("send %q: %v\nscreen:\n%s", name, err, c.S.Screen())
	}
	c.Shot(name)
}

// Type types text and snapshots the result.
func (c *Ctx) Type(name, text string) {
	c.T.Helper()
	if err := c.S.Type(text); err != nil {
		c.T.Fatalf("type %q: %v\nscreen:\n%s", name, err, c.S.Screen())
	}
	c.Shot(name)
}

// ReplaceField clears a text field (End then Backspace over its content) and
// types a new value, for fields that come prefilled (host, port).
func (c *Ctx) ReplaceField(name, value string) {
	c.T.Helper()
	c.Keys(name+"-end", harness.KeyEnd)
	c.Keys(name+"-clear", strings.Repeat(harness.KeyBackspace, 48))
	c.Type(name, value)
}

// tabBarRow is the screen row the tab titles are drawn on (menu bar 0,
// toolbar 1), for tests that drive the tab bar with the mouse.
const tabBarRow = 2

// TypeSQL types a multi-line statement the way a user writes it: line by line,
// dismissing the completion popup (Esc) before each newline. Typing a qualified
// name like "c." opens the popup, and a newline delivered while it is open
// accepts the selected candidate instead of inserting a line break — which
// re-writes the text under the caret. Esc only closes the popup here.
func (c *Ctx) TypeSQL(name, sql string) {
	c.T.Helper()
	lines := strings.Split(strings.TrimRight(sql, "\n"), "\n")
	for i, line := range lines {
		if i > 0 {
			c.Keys("dismiss-popup", harness.KeyEsc)
			c.Keys("newline", harness.KeyEnter)
		}
		c.Type(fmt.Sprintf("%s-line-%d", name, i+1), line)
	}
	c.Keys("dismiss-popup-end", harness.KeyEsc)
}

// WaitFor waits until the screen contains want.
func (c *Ctx) WaitFor(want string, timeout time.Duration) {
	c.T.Helper()
	if err := c.S.WaitFor(want, timeout); err != nil {
		c.T.Fatalf("%v", err)
	}
}

// WaitGone waits until the screen no longer contains want.
func (c *Ctx) WaitGone(want string, timeout time.Duration) {
	c.T.Helper()
	if err := c.S.WaitGone(want, timeout); err != nil {
		c.T.Fatalf("%v", err)
	}
}

// ClickLeft clicks at 0-based terminal coordinates.
func (c *Ctx) ClickLeft(x, y int) {
	c.T.Helper()
	if err := c.S.MouseClick(x, y); err != nil {
		c.T.Fatalf("click (%d,%d): %v\nscreen:\n%s", x, y, err, c.S.Screen())
	}
}

// ClickListRow clicks the row of an open dropdown whose visible text is want.
// Dropdown rows are drawn inside a frame, so the row is matched after trimming
// the box characters and padding: an exact match would never see the text.
func (c *Ctx) ClickListRow(want string) {
	c.T.Helper()
	trim := "│─┌┐└┘ "
	for y, line := range strings.Split(c.S.Screen(), "\n") {
		cleaned := strings.Trim(strings.TrimSpace(line), trim)
		if cleaned != want {
			continue
		}
		col, ok := c.S.FindTextInRow(want, y)
		if !ok {
			continue
		}
		c.ClickLeft(col+len([]rune(want))/2, y)
		return
	}
	c.Fail("cannot click the list row %q: no row shows exactly that", want)
}

// PickFromList picks an entry of an already open dropdown: values is the list
// the server returned, in the order it is shown. The caret moves from the first
// entry with Home and lands on target with Down, then Enter picks it.
func (c *Ctx) PickFromList(name string, values []string, target string) {
	c.T.Helper()
	idx := -1
	for i, v := range values {
		if v == target {
			idx = i
			break
		}
	}
	if idx < 0 {
		c.Fail("%s: %q is not in the list %v", name, target, values)
	}
	c.Keys(name+"-home", harness.KeyHome)
	for i := 0; i < idx; i++ {
		c.Keys(name+"-down", harness.KeyDown)
	}
	c.Keys(name+"-pick", harness.KeyEnter)
}

// ClickExactRow clicks the first screen row whose trimmed text equals want.
// It is the right tool for dropdown items: a substring search can hit the same
// word inside a sentence elsewhere on screen.
func (c *Ctx) ClickExactRow(want string) {
	c.T.Helper()
	for y, line := range strings.Split(c.S.Screen(), "\n") {
		if strings.TrimSpace(line) != want {
			continue
		}
		col, ok := c.S.FindTextInRow(want, y)
		if !ok {
			continue
		}
		c.ClickLeft(col+len([]rune(want))/2, y)
		return
	}
	c.Fail("cannot click the row %q: no row matches exactly", want)
}

// ClickText finds the first occurrence of text on screen and left-clicks the
// middle of it.
func (c *Ctx) ClickText(text string) (x, y int) {
	c.T.Helper()
	row, start, end, ok := c.S.FindTextSpan(text)
	if !ok {
		c.Fail("cannot click %q: not on screen", text)
	}
	x = (start + end) / 2
	c.ClickLeft(x, row)
	return x, row
}

// ClickTextOnRow clicks text on a specific screen row (the toolbar, for
// example, where the same label also appears in the menu bar).
func (c *Ctx) ClickTextOnRow(text string, y int) {
	c.T.Helper()
	start, ok := c.S.FindTextInRow(text, y)
	if !ok {
		c.Fail("cannot click %q on row %d (row: %q)", text, y, c.S.Line(y))
	}
	c.ClickLeft(start+len([]rune(text))/2, y)
}

// RightClickTextOnRow right-clicks the first occurrence of text on one screen
// row (the tab bar, for instance, where the editor may hold the same words).
func (c *Ctx) RightClickTextOnRow(text string, row int) {
	c.T.Helper()
	col, ok := c.S.FindTextInRow(text, row)
	if !ok {
		c.Fail("cannot right-click %q on row %d: not on that row", text, row)
	}
	if err := c.S.MouseClickButton(col, row, harness.MouseRight); err != nil {
		c.T.Fatalf("right-click %q: %v", text, err)
	}
}

// RightClickTabAt right-clicks the title of the n-th tab (0-based). The slot is
// found from the close glyphs on the tab bar, so the test does not have to know
// the pixel geometry of the tab bar.
func (c *Ctx) RightClickTabAt(n int) {
	c.T.Helper()
	runes := []rune(c.S.Line(tabBarRow))
	seen := 0
	for i, r := range runes {
		if string(r) != "×" {
			continue
		}
		if seen == n {
			x := max(i-3, 0)
			if err := c.S.MouseClickButton(x, tabBarRow, harness.MouseRight); err != nil {
				c.T.Fatalf("right-click tab %d: %v", n, err)
			}
			return
		}
		seen++
	}
	c.Fail("tab %d not found on the tab bar", n)
}

// ColOf returns the first screen column where text appears.
func (c *Ctx) ColOf(text string) int {
	c.T.Helper()
	_, col, ok := c.S.FindText(text)
	if !ok {
		c.Fail("cannot find %q on screen", text)
	}
	return col
}

// RowOfLineEnd returns the last column of a screen row.
func (c *Ctx) RowOfLineEnd(row int) int {
	c.T.Helper()
	n := len([]rune(c.S.Line(row)))
	if n == 0 {
		c.Fail("row %d is empty", row)
	}
	return n - 1
}

// RowOfFrom returns the first screen row at or after fromRow that contains
// text. Tests use it to look below a header that repeats the same name.
func (c *Ctx) RowOfFrom(text string, fromRow int) int {
	c.T.Helper()
	lines := strings.Split(c.S.Screen(), "\n")
	for y := max(fromRow, 0); y < len(lines); y++ {
		if strings.Contains(lines[y], text) {
			return y
		}
	}
	c.Fail("no row at or after %d contains %q", fromRow, text)
	return -1
}

// TabCount reports how many tab titles are on the tab bar.
func (c *Ctx) TabCount() int {
	c.T.Helper()
	n := 0
	for _, r := range c.S.Line(tabBarRow) {
		if string(r) == "×" {
			n++
		}
	}
	return n
}

// WaitTextInRow waits until text appears on one specific screen row.
func (c *Ctx) WaitTextInRow(text string, row int, timeout time.Duration) {
	c.T.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, ok := c.S.FindTextInRow(text, row); ok {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	c.Fail("timed out waiting for %q on row %d", text, row)
}

// DoubleClickText finds the first occurrence of text on screen and double-clicks
// it, the way a user opens a table's data from the object browser.
func (c *Ctx) DoubleClickText(text string) (x, y int) {
	c.T.Helper()
	row, start, end, ok := c.S.FindTextSpan(text)
	if !ok {
		c.Fail("cannot double-click %q: not on screen", text)
	}
	x = (start + end) / 2
	if err := c.S.DoubleClick(x, row); err != nil {
		c.T.Fatalf("double-click (%d,%d): %v", x, row, err)
	}
	return x, row
}

// ClickRow finds the first screen row containing text and left-clicks it.
func (c *Ctx) ClickRow(text string) (x, y int) {
	c.T.Helper()
	row, ok := c.S.FindRow(text)
	if !ok {
		c.Fail("cannot click row %q: not on screen", text)
	}
	c.ClickLeft(40, row)
	return 40, row
}

// Menu opens a top-level menu with its Alt accelerator and activates an item
// with the item's accelerator letter, exactly like a keyboard user. The pseudo
// console occasionally drops the letter that follows the menu-opening escape
// sequence, so the letter is retried when the screen did not change at all.
func (c *Ctx) Menu(menuAccel byte, itemAccel byte) {
	c.T.Helper()
	if err := c.S.Send("\x1b" + string(menuAccel)); err != nil {
		c.T.Fatalf("open menu %q: %v", string(menuAccel), err)
	}
	before := c.S.Screen()
	for attempt := 0; attempt < 3; attempt++ {
		if err := c.S.Send(string(itemAccel)); err != nil {
			c.T.Fatalf("activate menu item %q: %v", string(itemAccel), err)
		}
		after := c.S.Screen()
		if after != before {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	c.Fail("menu item %q did not activate (menu %q):\n%s",
		string(itemAccel), string(menuAccel), c.S.Screen())
}

// FocusObjects moves the keyboard focus to the object browser.
func (c *Ctx) FocusObjects() { c.Keys("focus-objects", "\x1b1") }

// FocusEditor moves the keyboard focus to the SQL editor.
func (c *Ctx) FocusEditor() { c.Keys("focus-editor", "\x1b2") }

// FocusResults moves the keyboard focus to the result grid.
func (c *Ctx) FocusResults() { c.Keys("focus-results", "\x1b3") }

// Screen returns the current rendered screen.
func (c *Ctx) Screen() string { return c.S.Screen() }

// RequireContains asserts the screen contains every substring.
func (c *Ctx) RequireContains(wants ...string) {
	c.T.Helper()
	screen := c.S.Screen()
	for _, want := range wants {
		if !strings.Contains(screen, want) {
			c.Fail("screen does not contain %q", want)
		}
	}
}

// RequireNotContains asserts the screen contains none of the substrings.
func (c *Ctx) RequireNotContains(unwants ...string) {
	c.T.Helper()
	screen := c.S.Screen()
	for _, u := range unwants {
		if strings.Contains(screen, u) {
			c.Fail("screen must not contain %q", u)
		}
	}
}

// RequireMatch asserts the screen matches re.
func (c *Ctx) RequireMatch(re string) {
	c.T.Helper()
	if !regexp.MustCompile(re).MatchString(c.S.Screen()) {
		c.Fail("screen does not match /%s/", re)
	}
}

// RequireLineContains asserts the given 0-based screen row contains want.
func (c *Ctx) RequireLineContains(y int, want string) {
	c.T.Helper()
	line := c.S.Line(y)
	if !strings.Contains(line, want) {
		c.Fail("screen row %d does not contain %q (row: %q)", y, want, line)
	}
}

// RequireOrderBefore asserts a appears on an earlier screen row than b.
func (c *Ctx) RequireOrderBefore(a, b string) {
	c.T.Helper()
	ra, okA := c.S.FindRow(a)
	rb, okB := c.S.FindRow(b)
	if !okA || !okB {
		c.Fail("cannot compare order: %q found=%v, %q found=%v", a, okA, b, okB)
	}
	if ra > rb {
		c.Fail("%q (row %d) must appear before %q (row %d)", a, ra, b, rb)
	}
}

// RowOf returns the first screen row containing text.
func (c *Ctx) RowOf(text string) int {
	c.T.Helper()
	row, ok := c.S.FindRow(text)
	if !ok {
		c.Fail("%q is not on screen", text)
	}
	return row
}

// fail reports a failure with the full screen and a labeled snapshot.
func (c *Ctx) Fail(format string, args ...any) {
	c.T.Helper()
	c.seq++
	png, err := c.S.Snapshot(c.artDir, fmt.Sprintf("%03d-FAIL", c.seq))
	shot := ""
	if err == nil {
		shot = "\nsnapshot: " + png
	}
	c.T.Fatalf(format+"%s\n--- screen ---\n%s", append(args, shot, c.S.Screen())...)
}

// Logf logs a line to the test output and the artifacts log.
func (c *Ctx) Logf(format string, args ...any) {
	c.T.Helper()
	line := fmt.Sprintf(format, args...)
	c.T.Log(line)
	f, err := os.OpenFile(filepath.Join(c.artDir, "scenario.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		_, _ = fmt.Fprintln(f, line)
		_ = f.Close()
	}
}

func sanitizeName(s string) string {
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	s = strings.ReplaceAll(s, ":", "_")
	s = strings.ReplaceAll(s, " ", "_")
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}
