package harness

import (
	"context"
	"fmt"
	"html"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// ChromePath is the browser used to rasterize terminal snapshots.
var ChromePath = `C:\Program Files\Google\Chrome\Application\chrome.exe`

// screenshotFontFamily starts with the Nerd Fonts v3 family name for
// CaskaydiaMono Nerd Font Mono. Besides keeping a fixed-width terminal grid, it
// supplies the private-use-area icons that otherwise render as empty boxes.
const screenshotFontFamily = "'CaskaydiaMono NFM','Cascadia Mono','Consolas','Courier New',monospace"

// SnapshotHTML renders the current screen as a standalone HTML document that
// looks like a terminal: one <span> per styled run, monospace font, true
// colors. The browser gives us font fallback for CJK, emoji and box-drawing
// characters for free.
func (s *Session) SnapshotHTML(title string) string {
	w, h := s.Size()
	defFg, defBg := s.defaultColors()

	var b strings.Builder
	b.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"><title>")
	b.WriteString(html.EscapeString(title))
	b.WriteString("</title><style>")
	fmt.Fprintf(&b, "html,body{margin:0;padding:0;background:%s;}", defBg)
	b.WriteString("pre{margin:0;padding:10px;font-family:")
	b.WriteString(screenshotFontFamily)
	b.WriteString(";font-size:14px;line-height:17px;color:")
	b.WriteString(defFg)
	b.WriteString(";white-space:pre;tab-size:4;-webkit-font-smoothing:antialiased}")
	b.WriteString("</style></head><body><pre>")

	// Build lines run by run, keeping the full width so background colors of
	// the UI (panes, status bar) are painted.
	for y := 0; y < h; y++ {
		type styled struct {
			text  string
			fg    string
			bg    string
			attrs string
		}
		var runs []styled
		var cur styled
		flush := func() {
			if cur.text != "" {
				runs = append(runs, cur)
				cur.text = ""
			}
		}
		for x := 0; x < w; x++ {
			cell := s.vt.CellAt(x, y)
			if cell == nil {
				cell = uv.EmptyCell.Clone()
			}
			content := cell.Content
			if content == "" {
				content = " "
			}
			fg, bg := cellColors(cell, defFg, defBg)
			attrs := attrCSS(cell.Style.Attrs, cell.Style.Underline)
			if cur.fg != fg || cur.bg != bg || cur.attrs != attrs {
				flush()
				cur.fg, cur.bg, cur.attrs = fg, bg, attrs
			}
			cur.text += content
			if cell.Width > 1 {
				// The trailing cells of a wide grapheme are covered by it.
				x += cell.Width - 1
			}
		}
		flush()

		// Trailing spaces on the default background need no markup.
		for i := len(runs) - 1; i >= 0; i-- {
			if runs[i].bg != defBg {
				break
			}
			runs[i].text = strings.TrimRight(runs[i].text, " ")
			if runs[i].text == "" {
				runs = runs[:i]
			} else {
				break
			}
		}
		for _, r := range runs {
			b.WriteString("<span style=\"")
			if r.fg != defFg {
				b.WriteString("color:" + r.fg + ";")
			}
			if r.bg != defBg {
				b.WriteString("background:" + r.bg + ";")
			}
			b.WriteString(r.attrs)
			b.WriteString("\">")
			b.WriteString(html.EscapeString(r.text))
			b.WriteString("</span>")
		}
		if y < h-1 {
			b.WriteByte('\n')
		}
	}
	b.WriteString("</pre></body></html>")
	return b.String()
}

// Snapshot writes an HTML snapshot and a text dump of the screen. When Chrome
// is available it also rasterizes a PNG next to them.
func (s *Session) Snapshot(dir, name string) (pngPath string, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	htmlPath := filepath.Join(dir, name+".html")
	if err := os.WriteFile(htmlPath, []byte(s.SnapshotHTML(name)), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, name+".txt"), []byte(s.Screen()), 0o644); err != nil {
		return "", err
	}
	return s.rasterize(htmlPath, filepath.Join(dir, name+".png"))
}

// rasterize renders an HTML snapshot with headless Chrome.
func (s *Session) rasterize(htmlPath, pngPath string) (string, error) {
	w, h := s.Size()
	return RasterizeHTML(htmlPath, pngPath, w, h)
}

// RasterizeHTML renders a snapshot HTML file to PNG with headless Chrome. cols
// and rows are the terminal grid size the HTML was rendered for: they size the
// browser window so no line wraps.
func RasterizeHTML(htmlPath, pngPath string, cols, rows int) (string, error) {
	if _, statErr := os.Stat(ChromePath); statErr != nil {
		return "", fmt.Errorf("chrome not found at %s", ChromePath)
	}
	w, h := cols, rows
	// CaskaydiaMono NFM at 14px advances ~8.4px per cell; add room for padding
	// so no line wraps and the whole grid is visible.
	width := int(float64(w)*8.6) + 40
	height := h*17 + 40
	tmp, err := os.MkdirTemp(filepath.Dir(htmlPath), ".chrome-tmp-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	_ = os.Remove(pngPath)
	args := []string{
		"--headless=new",
		"--no-sandbox",
		"--disable-gpu",
		"--disable-dev-shm-usage",
		"--hide-scrollbars",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--user-data-dir=" + tmp,
		fmt.Sprintf("--window-size=%d,%d", width, height),
		"--screenshot=" + pngPath,
		"file:///" + filepath.ToSlash(htmlPath),
	}

	// Chrome must not inherit piped stdio: under the harness's file sandbox
	// named pipes are unavailable, and the browser would crash on startup.
	// (CommandContext below leaves both nil, which is the same.)
	// A hung browser must not hang the test: the screenshot is evidence, and
	// a missing PNG is better than a suite that never returns.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, ChromePath, args...).Run(); err != nil {
		return "", fmt.Errorf("chrome screenshot: %v", err)
	}
	if _, err := os.Stat(pngPath); err != nil {
		return "", fmt.Errorf("chrome did not write %s: %v", pngPath, err)
	}
	return pngPath, nil
}

// defaultColors returns the terminal's default foreground and background.
func (s *Session) defaultColors() (fg, bg string) {
	fg, bg = "#d4d4d4", "#1e1e1e"
	if c := s.vt.ForegroundColor(); c != nil {
		if _, _, _, a := c.RGBA(); a != 0 {
			fg = cssColor(c)
		}
	}
	if c := s.vt.BackgroundColor(); c != nil {
		if _, _, _, a := c.RGBA(); a != 0 {
			bg = cssColor(c)
		}
	}
	return fg, bg
}

func cellColors(cell *uv.Cell, defFg, defBg string) (fg, bg string) {
	fg, bg = defFg, defBg
	if cell.Style.Fg != nil {
		fg = cssColor(cell.Style.Fg)
	}
	if cell.Style.Bg != nil {
		bg = cssColor(cell.Style.Bg)
	}
	if cell.Style.Attrs&uv.AttrReverse != 0 {
		fg, bg = bg, fg
	}
	return fg, bg
}

func attrCSS(attrs uint8, u uv.Underline) string {
	var b strings.Builder
	if attrs&uv.AttrBold != 0 {
		b.WriteString("font-weight:bold;")
	}
	if attrs&uv.AttrFaint != 0 {
		b.WriteString("opacity:.65;")
	}
	if attrs&uv.AttrItalic != 0 {
		b.WriteString("font-style:italic;")
	}
	if u != uv.UnderlineNone {
		b.WriteString("text-decoration:underline;")
	}
	if attrs&uv.AttrStrikethrough != 0 {
		b.WriteString("text-decoration:line-through;")
	}
	if attrs&uv.AttrConceal != 0 {
		b.WriteString("visibility:hidden;")
	}
	return b.String()
}

func cssColor(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", uint8(r>>8), uint8(g>>8), uint8(b>>8))
}
