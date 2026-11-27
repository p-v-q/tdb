// Command demo records the README's terminal demo.
//
// It starts the real binary inside the PTY harness, drives a scripted session
// against the SQLite shop database, captures the emulated screen as HTML frames
// while it runs, then rasterizes the frames with headless Chrome and builds an
// animated GIF with the standard-library Go GIF builder.
//
// Run from the repository root after building bin/tdb.exe:
//
//	go run ./cmd/demo       (inside .itest; see .itest/env.ps1 for the Go env)
//
// or: pwsh -File .itest/demo/record.ps1
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"tdb-itest/harness"
)

const (
	cols = 150
	rows = 42
	// frameEvery is how often the screen is captured. The GIF builder keeps the
	// delay of every frame, so slow parts of the session stay readable.
	frameEvery = 110 * time.Millisecond
)

type frame struct {
	index int
	html  string
	at    time.Time
}

type recorder struct {
	s  *harness.Session
	mu sync.Mutex
	fr []frame
}

func (r *recorder) captureLoop(stop <-chan struct{}, done chan<- struct{}) {
	t := time.NewTicker(frameEvery)
	defer t.Stop()
	defer close(done)
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			r.mu.Lock()
			r.fr = append(r.fr, frame{index: len(r.fr), html: r.s.SnapshotHTML("demo"), at: time.Now()})
			r.mu.Unlock()
		}
	}
}

// hold sleeps while the capture loop keeps collecting frames.
func hold(d time.Duration) { time.Sleep(d) }

func must(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
		os.Exit(1)
	}
}

func main() {
	root := flag.String("root", "..", "repository root (default: parent of .itest)")
	out := flag.String("out", filepath.Join("..", ".artifacts", "demo"), "output directory for frames and the GIF")
	flag.String("python", "python", "deprecated and ignored; GIFs are built with Go")
	flag.Parse()

	repo, err := filepath.Abs(*root)
	must(err, "resolve root")
	bin := filepath.Join(repo, "bin", "tdb.exe")
	db := filepath.Join(repo, ".dbs", "sqlite", "shop.db")
	if _, err := os.Stat(bin); err != nil {
		must(fmt.Errorf("%s is missing: run `go build -o bin/tdb.exe ./cmd/tdb` first", bin), "binary")
	}
	if _, err := os.Stat(db); err != nil {
		must(fmt.Errorf("%s is missing: run .itest/datasets/load.ps1 first", db), "database")
	}

	outDir, err := filepath.Abs(*out)
	must(err, "resolve output")
	must(os.MkdirAll(outDir, 0o755), "create output directory")

	settingsPath := filepath.Join(outDir, "settings.json")
	writeSettings(settingsPath, db)
	must(os.RemoveAll(filepath.Join(outDir, "frames")), "clean frames")

	s, err := harness.Start(harness.Options{
		Bin:         bin,
		Args:        []string{"-d", "sqlite", "-dsn", db, "-settings", settingsPath},
		Dir:         repo,
		Cols:        cols,
		Rows:        rows,
		ArtifactDir: filepath.Join(outDir, "run"),
	})
	must(err, "start tdb")

	rec := &recorder{s: s}
	stop := make(chan struct{})
	done := make(chan struct{})
	go rec.captureLoop(stop, done)

	script(s)

	close(stop)
	<-done
	s.Close()

	framesDir := filepath.Join(outDir, "frames")
	must(os.MkdirAll(framesDir, 0o755), "create frames directory")
	rec.mu.Lock()
	frames := append([]frame(nil), rec.fr...)
	rec.mu.Unlock()
	if len(frames) < 2 {
		must(fmt.Errorf("captured only %d frames", len(frames)), "record")
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i].index < frames[j].index })

	// Write the HTML frames and the frame timing manifest.
	var manifest strings.Builder
	for i, f := range frames {
		name := fmt.Sprintf("%05d", i)
		htmlPath := filepath.Join(framesDir, name+".html")
		must(os.WriteFile(htmlPath, []byte(f.html), 0o644), "write frame")
		d := frameEvery
		if i+1 < len(frames) {
			d = frames[i+1].at.Sub(f.at)
		}
		if d <= 0 || d > 2*time.Second {
			d = frameEvery
		}
		fmt.Fprintf(&manifest, "%s %d\n", name, d.Milliseconds())
	}
	must(os.WriteFile(filepath.Join(outDir, "manifest.txt"), []byte(manifest.String()), 0o644), "write manifest")

	// Rasterize every frame with headless Chrome.
	fmt.Printf("rasterizing %d frames...\n", len(frames))
	pngs := make([]string, 0, len(frames))
	for i := range frames {
		name := fmt.Sprintf("%05d", i)
		htmlPath := filepath.Join(framesDir, name+".html")
		pngPath := filepath.Join(framesDir, name+".png")
		if _, err := harness.RasterizeHTML(htmlPath, pngPath, cols, rows); err != nil {
			must(err, "rasterize %s"+name)
		}
		pngs = append(pngs, pngPath)
	}

	// Build the GIF. cmd/gif is a command (and therefore not importable), so
	// run it in the independent .itest module. No Python or Pillow is needed.
	gifPath := filepath.Join(outDir, "demo.gif")
	cmd := exec.Command("go", "run", "./cmd/gif", framesDir, filepath.Join(outDir, "manifest.txt"), outDir, gifPath)
	cmd.Dir = filepath.Join(repo, ".itest")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	must(cmd.Run(), "build gif")
	fmt.Printf("demo: %s (%d frames)\n", gifPath, len(frames))
}

// writeSettings writes a settings file with saved connections, so the object
// browser looks like a normal user's: a few saved connections, one of them the
// SQLite shop that the script opens.
func writeSettings(path, db string) {
	doc := map[string]any{
		"version": 1,
		"locale":  "en",
		"theme":   "dark",
		"mouse":   true,
		"gutter":  true,
		"layout": map[string]any{
			"objectsVisible": true,
			"objectsWidth":   30,
			"resultsVisible": true,
			"resultsHeight":  12,
		},
		"connections": []map[string]any{
			{"name": "sqlite-shop", "driver": "sqlite", "file": db},
			{"name": "pg-shop", "driver": "postgres", "dsn": "postgres://tdb:tdbpass@127.0.0.1:55432/shop?sslmode=disable"},
			{"name": "mysql-shop", "driver": "mysql", "dsn": "mysql://tdb:tdbpass@127.0.0.1:53306/shop"},
		},
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	must(err, "marshal settings")
	must(os.WriteFile(path, data, 0o644), "write settings")
}

// ---- Script helpers ----

func key(s *harness.Session, k string) {
	must(s.Write([]byte(k)), "send key")
	must(s.Settle(120*time.Millisecond), "settle")
}

func waitFor(s *harness.Session, text string, timeout time.Duration) {
	must(s.WaitFor(text, timeout), "wait for "+text)
}

// typeChunks types text in small chunks, so the recording shows the typing and
// the completion popup appearing.
func typeChunks(s *harness.Session, text string, chunk int, every time.Duration) {
	runes := []rune(text)
	for i := 0; i < len(runes); i += chunk {
		end := min(i+chunk, len(runes))
		must(s.Write([]byte(string(runes[i:end]))), "type")
		time.Sleep(every)
	}
}

// doubleClickText finds text on screen and double-clicks it.
func doubleClickText(s *harness.Session, text string) {
	row, start, end, ok := s.FindTextSpan(text)
	if !ok {
		must(fmt.Errorf("%q is not on screen", text), "double click")
	}
	must(s.DoubleClick((start+end)/2, row), "double click")
}

func (r *recorder) rightClickText(text string) {
	row, start, end, ok := r.s.FindTextSpan(text)
	if !ok {
		must(fmt.Errorf("%q is not on screen", text), "right click")
	}
	must(r.s.MouseClickButton((start+end)/2, row, harness.MouseRight), "right click")
	must(r.s.Settle(300*time.Millisecond), "settle")
}

// script drives the session. Every action is visible in the recording; the
// holds give a viewer time to read the screen.
func script(s *harness.Session) {
	waitFor(s, "Welcome to TDB", 25*time.Second)
	hold(900 * time.Millisecond)

	// Object browser: open the connection, the schema and the tables group.
	key(s, "\x1b1") // Alt+1 focuses the object browser
	key(s, harness.KeyRight)
	hold(150 * time.Millisecond)
	key(s, harness.KeyDown)
	key(s, harness.KeyRight)
	hold(200 * time.Millisecond)
	key(s, harness.KeyDown)
	key(s, harness.KeyRight)
	waitFor(s, "users", 10*time.Second)
	hold(700 * time.Millisecond)

	// Double-click a table: View data opens in a tab and runs. The 100k-row
	// users table is wide enough to show the horizontal scrollbar too.
	doubleClickText(s, "users")
	waitFor(s, "user1@example.com", 20*time.Second)
	hold(1500 * time.Millisecond)

	// Sort the grid by status, then filter it down to one row.
	key(s, "\x1b3") // Alt+3 focuses the results
	for i := 0; i < 6; i++ {
		key(s, harness.KeyRight)
	}
	key(s, "s")
	hold(900 * time.Millisecond)
	key(s, "/")
	typeChunks(s, "user42@", 2, 150*time.Millisecond)
	hold(300 * time.Millisecond)
	key(s, harness.KeyEnter)
	waitFor(s, "1 row in", 15*time.Second)
	hold(1200 * time.Millisecond)
	key(s, harness.KeyEsc)
	hold(200 * time.Millisecond)

	// Editor: completion while typing, then an aggregate query.
	key(s, "\x1b2") // Alt+2 focuses the editor
	key(s, harness.KeyCtrlA)
	typeChunks(s, "SELECT status, count(*) AS n FROM us", 3, 140*time.Millisecond)
	hold(1000 * time.Millisecond) // the completion popup is on screen
	key(s, harness.KeyTab)
	typeChunks(s, " GROUP BY status ORDER BY n DESC;", 3, 130*time.Millisecond)
	hold(250 * time.Millisecond)
	key(s, harness.KeyF5)
	waitFor(s, "churned", 20*time.Second)
	hold(1600 * time.Millisecond)

	// Key bindings overlay.
	key(s, harness.KeyF1)
	hold(1500 * time.Millisecond)
	key(s, harness.KeyEsc)
	hold(200 * time.Millisecond)

	// The tab menu with the bulk closes.
	row := 2 // the tab titles
	if y, ok := s.FindRow("main.users [data]"); ok {
		row = y
	}
	if col, ok := s.FindTextInRow("main.users [data]", row); ok {
		must(s.MouseClickButton(col+2, row, harness.MouseRight), "right click tab")
	}
	hold(1300 * time.Millisecond)
	key(s, harness.KeyEsc)
	hold(300 * time.Millisecond)

	// Resize the result pane: the drag shows the cheap preview, the release
	// brings the contents back.
	if y, ok := s.FindRow("Results"); ok && y >= 3 {
		divider := y - 1
		must(s.PressLeft(70, divider), "press divider")
		for _, up := range []int{1, 2, 2, 2} {
			must(s.DragTo(70, divider-up), "drag divider")
		}
		hold(700 * time.Millisecond)
		must(s.ReleaseLeft(70, divider-2), "release divider")
	}
	hold(1400 * time.Millisecond)

	// End on the editor with the last result still visible.
	key(s, "\x1b2")
	hold(800 * time.Millisecond)
}
