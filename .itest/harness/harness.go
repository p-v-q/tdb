// Package harness drives the real TDB binary inside a Windows ConPTY pseudo
// terminal, parses the byte stream with a VT terminal emulator, and offers
// screen assertions and snapshots. It is the integration-test equivalent of a
// user sitting in front of the terminal: every interaction is real keystrokes
// over a real console, and assertions read the rendered screen.
package harness

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aymanbagabas/go-pty"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// Defaults for the pseudo terminal. 160x48 is wide enough to show the object
// browser, a SQL editor and the result grid of a real session.
const (
	DefaultCols = 160
	DefaultRows = 48
)

// Options configures a session.
type Options struct {
	Bin  string
	Args []string
	Dir  string
	Env  []string
	Cols int
	Rows int
	// ArtifactDir, when set, receives raw output and screen dumps on close.
	ArtifactDir string
}

// Session is one running TDB instance attached to a pseudo terminal.
type Session struct {
	opts Options
	pty  pty.Pty
	cmd  *pty.Cmd
	vt   *vt.SafeEmulator

	mu     sync.Mutex
	raw    bytes.Buffer
	err    error
	done   chan struct{}
	closed sync.Once
}

// Start launches the binary and waits until it paints its first full screen.
func Start(opts Options) (*Session, error) {
	if opts.Cols == 0 {
		opts.Cols = DefaultCols
	}
	if opts.Rows == 0 {
		opts.Rows = DefaultRows
	}
	if opts.ArtifactDir != "" {
		if err := os.MkdirAll(opts.ArtifactDir, 0o755); err != nil {
			return nil, fmt.Errorf("create artifact dir: %w", err)
		}
	}

	p, err := pty.New()
	if err != nil {
		return nil, fmt.Errorf("create pty: %w", err)
	}
	cmd := p.Command(opts.Bin, opts.Args...)
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	cmd.Env = childEnv(opts.Env)

	emu := vt.NewSafeEmulator(opts.Cols, opts.Rows)

	s := &Session{opts: opts, pty: p, cmd: cmd, vt: emu, done: make(chan struct{})}
	if err := cmd.Start(); err != nil {
		_ = p.Close()
		return nil, fmt.Errorf("start %s: %w", opts.Bin, err)
	}
	go s.copyOutput()
	go func() {
		s.err = cmd.Wait()
		close(s.done)
	}()

	if err := p.Resize(opts.Cols, opts.Rows); err != nil {
		s.Close()
		return nil, fmt.Errorf("resize pty: %w", err)
	}
	emu.Resize(opts.Cols, opts.Rows)

	if err := s.WaitReady(20 * time.Second); err != nil {
		return s, err
	}
	return s, nil
}

func (s *Session) copyOutput() {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			s.mu.Lock()
			if s.raw.Len() < 8<<20 {
				s.raw.Write(chunk)
			}
			s.mu.Unlock()
			_, _ = s.vt.Write(chunk)
		}
		if err != nil {
			return
		}
	}
}

// Close terminates the process and releases the pty.
func (s *Session) Close() {
	s.closed.Do(func() {
		if s.opts.ArtifactDir != "" {
			_ = os.MkdirAll(s.opts.ArtifactDir, 0o755)
			_ = os.WriteFile(filepath.Join(s.opts.ArtifactDir, "raw-output.bin"), s.RawOutput(), 0o644)
			_ = os.WriteFile(filepath.Join(s.opts.ArtifactDir, "final-screen.txt"), []byte(s.Screen()), 0o644)
		}
		_ = s.pty.Close()
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
	})
}

// RawOutput returns everything the terminal printed so far.
func (s *Session) RawOutput() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.raw.Bytes()...)
}

// ExitErr returns the process error once it exited, nil if it exited cleanly.
func (s *Session) ExitErr() error {
	select {
	case <-s.done:
		return s.err
	default:
		return nil
	}
}

// Size returns the terminal geometry.
func (s *Session) Size() (w, h int) { return s.vt.Width(), s.vt.Height() }

// CellAt returns the emulated cell at (x, y).
func (s *Session) CellAt(x, y int) *uv.Cell { return s.vt.CellAt(x, y) }

// screenRow is one rendered row: the text a reader sees and, for every rune,
// the screen column it starts at. A wide grapheme covers two columns but is one
// rune of text, so text matching and cell coordinates stay related without
// inserting fake spaces between the characters of a Chinese word.
type screenRow struct {
	text   string
	cellOf []int // cell column of each rune, plus one past the last
}

func (s *Session) row(y int) screenRow {
	w, _ := s.Size()
	var runes []rune
	cellOf := make([]int, 0, w+1)
	for x := 0; x < w; {
		cell := s.vt.CellAt(x, y)
		if cell == nil || cell.Content == "" || cell.Width == 0 {
			runes = append(runes, ' ')
			cellOf = append(cellOf, x)
			x++
			continue
		}
		for _, r := range cell.Content {
			runes = append(runes, r)
			cellOf = append(cellOf, x)
		}
		x += max(1, cell.Width)
	}
	cellOf = append(cellOf, w)
	return screenRow{text: strings.TrimRight(string(runes), " "), cellOf: cellOf}
}

// Screen returns the visible screen as plain text with trailing blanks trimmed.
func (s *Session) Screen() string {
	_, h := s.Size()
	var b strings.Builder
	for y := 0; y < h; y++ {
		b.WriteString(s.row(y).text)
		if y < h-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Line returns one screen row with trailing blanks trimmed.
func (s *Session) Line(y int) string {
	_, h := s.Size()
	if y < 0 || y >= h {
		return ""
	}
	return s.row(y).text
}

// FindRow returns the first screen row containing text.
func (s *Session) FindRow(text string) (row int, ok bool) {
	for i, line := range strings.Split(s.Screen(), "\n") {
		if strings.Contains(line, text) {
			return i, true
		}
	}
	return 0, false
}

// FindTextInRow returns the cell column where text starts on row y.
func (s *Session) FindTextInRow(text string, y int) (col int, ok bool) {
	r := s.row(y)
	idx := strings.Index(r.text, text)
	if idx < 0 {
		return 0, false
	}
	return r.cellOf[idx], true
}

// FindText returns the screen row and the cell column where text starts.
func (s *Session) FindText(text string) (row, col int, ok bool) {
	row, col, _, ok = s.FindTextSpan(text)
	return row, col, ok
}

// FindTextSpan returns the screen row and the cell span of text: the first cell
// of its first rune and the cell after its last rune. Both are usable for
// mouse events.
func (s *Session) FindTextSpan(text string) (row, startCol, endCol int, ok bool) {
	_, h := s.Size()
	for y := 0; y < h; y++ {
		r := s.row(y)
		idx := strings.Index(r.text, text)
		if idx < 0 {
			continue
		}
		start := idx
		end := idx + len(text)
		if end > len(r.cellOf)-1 {
			end = len(r.cellOf) - 1
		}
		return y, r.cellOf[start], r.cellOf[end], true
	}
	return 0, 0, 0, false
}

// Cursor returns the cursor position (0-based).
func (s *Session) Cursor() (x, y int) {
	p := s.vt.CursorPosition()
	return p.X, p.Y
}

// Write sends raw bytes to the terminal input.
func (s *Session) Write(data []byte) error {
	_, err := s.pty.Write(data)
	return err
}

// Send writes a single key sequence and waits for the screen to settle.
func (s *Session) Send(keys string) error {
	if err := s.Write([]byte(keys)); err != nil {
		return err
	}
	return s.Settle(600 * time.Millisecond)
}

// Type writes text as a user types it. The text is sent in small chunks: a
// pseudo console translates the byte stream into key events one at a time, and
// a single huge write can lose characters (a long multi-line statement would
// arrive with pieces missing). Chunking by runes also keeps UTF-8 sequences
// whole.
func (s *Session) Type(text string) error {
	const chunkRunes = 16
	runes := []rune(text)
	for len(runes) > 0 {
		n := chunkRunes
		if len(runes) < n {
			n = len(runes)
		}
		if err := s.Write([]byte(string(runes[:n]))); err != nil {
			return err
		}
		runes = runes[n:]
		time.Sleep(6 * time.Millisecond)
	}
	return s.Settle(800 * time.Millisecond)
}

// Settle waits until the screen stops changing for a while, or timeout passes.
func (s *Session) Settle(timeout time.Duration) error {
	prev := s.Screen()
	stable := 0
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cur := s.Screen()
		if cur == prev {
			stable++
			if stable >= 2 {
				return nil
			}
		} else {
			stable = 0
			prev = cur
		}
		time.Sleep(70 * time.Millisecond)
	}
	return nil
}

// WaitReady waits until the screen shows some non-blank content.
func (s *Session) WaitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.TrimSpace(s.Screen()) != "" {
			return s.Settle(2 * time.Second)
		}
		select {
		case <-s.done:
			return fmt.Errorf("process exited before painting:\n%s", s.Screen())
		case <-time.After(80 * time.Millisecond):
		}
	}
	return fmt.Errorf("timed out waiting for the first screen; raw tail:\n%s", tail(s.RawOutput(), 4000))
}

// WaitFor waits until the screen contains want, then settles.
func (s *Session) WaitFor(want string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(s.Screen(), want) {
			return s.Settle(600 * time.Millisecond)
		}
		select {
		case <-s.done:
			return fmt.Errorf("process exited while waiting for %q:\n%s", want, s.Screen())
		case <-time.After(60 * time.Millisecond):
		}
	}
	s.dumpRaw()
	return fmt.Errorf("timed out waiting for %q;\nscreen:\n%s", want, s.Screen())
}

// WaitForRegexp waits until the screen matches re.
func (s *Session) WaitForRegexp(re *regexp.Regexp, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if re.MatchString(s.Screen()) {
			return s.Settle(600 * time.Millisecond)
		}
		select {
		case <-s.done:
			return fmt.Errorf("process exited while waiting for /%s/:\n%s", re, s.Screen())
		case <-time.After(60 * time.Millisecond):
		}
	}
	s.dumpRaw()
	return fmt.Errorf("timed out waiting for /%s/;\nscreen:\n%s", re, s.Screen())
}

// WaitGone waits until the screen no longer contains want.
func (s *Session) WaitGone(want string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !strings.Contains(s.Screen(), want) {
			return s.Settle(400 * time.Millisecond)
		}
		time.Sleep(60 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %q to disappear;\nscreen:\n%s", want, s.Screen())
}

// WaitUntil waits until pred returns true.
func (s *Session) WaitUntil(what string, pred func(screen string) bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pred(s.Screen()) {
			return s.Settle(400 * time.Millisecond)
		}
		select {
		case <-s.done:
			return fmt.Errorf("process exited while waiting for %s:\n%s", what, s.Screen())
		case <-time.After(60 * time.Millisecond):
		}
	}
	s.dumpRaw()
	return fmt.Errorf("timed out waiting for %s;\nscreen:\n%s", what, s.Screen())
}

// WaitExit waits for the process to exit.
func (s *Session) WaitExit(timeout time.Duration) error {
	select {
	case <-s.done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("process did not exit within %s", timeout)
	}
}

// Alive reports whether the process is still running.
func (s *Session) Alive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

// DumpScreen writes the current screen to dir/name.txt.
func (s *Session) DumpScreen(dir, name string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name+".txt"), []byte(s.Screen()), 0o644)
}

func (s *Session) dumpRaw() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.opts.ArtifactDir != "" {
		_ = os.MkdirAll(s.opts.ArtifactDir, 0o755)
		_ = os.WriteFile(filepath.Join(s.opts.ArtifactDir, "failure-raw.bin"), s.raw.Bytes(), 0o644)
	}
}

func tail(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[len(b)-n:])
}

// Key sequences, as a terminal sends them.
const (
	KeyEnter      = "\r"
	KeyEsc        = "\x1b"
	KeyTab        = "\t"
	KeyShiftTab   = "\x1b[Z"
	KeyUp         = "\x1b[A"
	KeyDown       = "\x1b[B"
	KeyRight      = "\x1b[C"
	KeyLeft       = "\x1b[D"
	KeyHome       = "\x1b[H"
	KeyEnd        = "\x1b[F"
	KeyPgUp       = "\x1b[5~"
	KeyPgDn       = "\x1b[6~"
	KeyInsert     = "\x1b[2~"
	KeyDelete     = "\x1b[3~"
	KeyBackspace  = "\x7f"
	KeyF1         = "\x1bOP"
	KeyF2         = "\x1bOQ"
	KeyF3         = "\x1bOR"
	KeyF4         = "\x1bOS"
	KeyF5         = "\x1b[15~"
	KeyF6         = "\x1b[17~"
	KeyF7         = "\x1b[18~"
	KeyF8         = "\x1b[19~"
	KeyF9         = "\x1b[20~"
	KeyF10        = "\x1b[21~"
	KeyShiftF10   = "\x1b[21;2~"
	KeyCtrlA      = "\x01"
	KeyCtrlB      = "\x02"
	KeyCtrlC      = "\x03"
	KeyCtrlE      = "\x05"
	KeyCtrlF      = "\x06"
	KeyCtrlH      = "\x08"
	KeyCtrlK      = "\x0b"
	KeyCtrlN      = "\x0e"
	KeyCtrlO      = "\x0f"
	KeyCtrlP      = "\x10"
	KeyCtrlQ      = "\x11"
	KeyCtrlR      = "\x12"
	KeyCtrlS      = "\x13"
	KeyCtrlT      = "\x14"
	KeyCtrlU      = "\x15"
	KeyCtrlV      = "\x16"
	KeyCtrlW      = "\x17"
	KeyCtrlX      = "\x18"
	KeyCtrlY      = "\x19"
	KeyCtrlZ      = "\x1a"
	KeyCtrlSlash  = "\x1f"
	KeyCtrlSpace  = "\x00"
	KeyCtrlEnter  = "\r"
	KeyCtrlPgUp   = "\x1b[5;5~"
	KeyCtrlPgDown = "\x1b[6;5~"
	KeyAltLetter  = "\x1b" // prefix; use Alt("f") for Alt+f
)

// Alt returns the sequence for Alt+<letter>.
func Alt(letter byte) string { return "\x1b" + string(letter) }

// Mouse button numbers used by the SGR mouse protocol.
const (
	MouseLeft      = 0
	MouseMiddle    = 1
	MouseRight     = 2
	MouseWheelUp   = 64
	MouseWheelDown = 65
)

// MouseClick clicks the left button at 0-based terminal coordinates.
func (s *Session) MouseClick(x, y int) error {
	return s.MouseClickButton(x, y, MouseLeft)
}

// MouseClickButton clicks a mouse button at 0-based coordinates.
func (s *Session) MouseClickButton(x, y, button int) error {
	press := fmt.Sprintf("\x1b[<%d;%d;%dM", button, x+1, y+1)
	release := fmt.Sprintf("\x1b[<%d;%d;%dm", button, x+1, y+1)
	if err := s.Write([]byte(press)); err != nil {
		return err
	}
	time.Sleep(40 * time.Millisecond)
	if err := s.Write([]byte(release)); err != nil {
		return err
	}
	return s.Settle(800 * time.Millisecond)
}

// DoubleClick clicks the left button twice at 0-based coordinates, inside the
// application's double-click window, and settles.
func (s *Session) DoubleClick(x, y int) error {
	press := fmt.Sprintf("\x1b[<%d;%d;%dM", MouseLeft, x+1, y+1)
	release := fmt.Sprintf("\x1b[<%d;%d;%dm", MouseLeft, x+1, y+1)
	if _, err := s.pty.Write([]byte(press + release + press + release)); err != nil {
		return err
	}
	return s.Settle(900 * time.Millisecond)
}

// PressLeft presses the left button at 0-based coordinates and leaves it held.
func (s *Session) PressLeft(x, y int) error {
	if _, err := s.pty.Write([]byte(fmt.Sprintf("\x1b[<0;%d;%dM", x+1, y+1))); err != nil {
		return err
	}
	return s.Settle(120 * time.Millisecond)
}

// DragTo moves the pointer with the left button held (a drag motion).
func (s *Session) DragTo(x, y int) error {
	if _, err := s.pty.Write([]byte(fmt.Sprintf("\x1b[<32;%d;%dM", x+1, y+1))); err != nil {
		return err
	}
	return s.Settle(150 * time.Millisecond)
}

// ReleaseLeft releases the left button at 0-based coordinates.
func (s *Session) ReleaseLeft(x, y int) error {
	if _, err := s.pty.Write([]byte(fmt.Sprintf("\x1b[<0;%d;%dm", x+1, y+1))); err != nil {
		return err
	}
	return s.Settle(200 * time.Millisecond)
}

// MouseMove reports a hover at 0-based coordinates (all-motion mouse mode).
func (s *Session) MouseMove(x, y int) error {
	if err := s.Write([]byte(fmt.Sprintf("\x1b[<35;%d;%dM", x+1, y+1))); err != nil {
		return err
	}
	return s.Settle(400 * time.Millisecond)
}

// MouseWheel scrolls at 0-based coordinates; delta > 0 scrolls up.
func (s *Session) MouseWheel(x, y, delta int) error {
	button := MouseWheelDown
	if delta > 0 {
		button = MouseWheelUp
	}
	for i := 0; i < abs(delta); i++ {
		if err := s.Write([]byte(fmt.Sprintf("\x1b[<%d;%d;%dM", button, x+1, y+1))); err != nil {
			return err
		}
	}
	return s.Settle(600 * time.Millisecond)
}

// Drag drags from (x0,y0) to (x1,y1) with the left button.
func (s *Session) Drag(x0, y0, x1, y1 int) error {
	if err := s.Write([]byte(fmt.Sprintf("\x1b[<0;%d;%dM", x0+1, y0+1))); err != nil {
		return err
	}
	time.Sleep(60 * time.Millisecond)
	steps := 6
	for i := 1; i <= steps; i++ {
		x := x0 + (x1-x0)*i/steps
		y := y0 + (y1-y0)*i/steps
		if err := s.Write([]byte(fmt.Sprintf("\x1b[<32;%d;%dM", x+1, y+1))); err != nil {
			return err
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := s.Write([]byte(fmt.Sprintf("\x1b[<0;%d;%dm", x1+1, y1+1))); err != nil {
		return err
	}
	return s.Settle(800 * time.Millisecond)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// childEnv builds the child environment: provided entries win, everything else
// is inherited, with a few terminal-friendly values.
func childEnv(explicit []string) []string {
	base := map[string]string{
		"TERM":                 "xterm-256color",
		"COLORTERM":            "truecolor",
		"TERM_PROGRAM":         "tdb-itest",
		"TERM_PROGRAM_VERSION": "1",
		"NO_COLOR":             "",
	}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			base[kv[:i]] = kv[i+1:]
		}
	}
	for _, kv := range explicit {
		if i := strings.IndexByte(kv, '='); i > 0 {
			base[kv[:i]] = kv[i+1:]
		}
	}
	delete(base, "NO_COLOR")
	out := make([]string, 0, len(base))
	for k, v := range base {
		out = append(out, k+"="+v)
	}
	return out
}
