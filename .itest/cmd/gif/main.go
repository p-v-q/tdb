// Command gif builds the README demo GIF from recorded terminal frames.
//
// usage: gif FRAMES_DIR MANIFEST OUT_DIR OUTPUT_GIF
//
// FRAMES_DIR holds 00000.png ... frames rendered by the demo recorder; MANIFEST
// holds "<name> <delay-ms>" lines. Frames are downscaled, quantized to one shared
// palette, identical consecutive frames are merged, and the result is written as
// a looping GIF. It is the standard-library replacement for demo/make_gif.py.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	gifWidth     = 980
	maxGIFFrames = 120
	gifUsageText = "usage: gif FRAMES_DIR MANIFEST OUT_DIR OUTPUT_GIF"
	miB          = 1024 * 1024
)

type manifestEntry struct {
	name    string
	delayMS int
}

type gifFrame struct {
	img     *image.Paletted
	delayMS int
}

type buildStats struct {
	sourceFrames int
	frames       int
	durationMS   int
	size         int64
}

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, gifUsageText)
		os.Exit(2)
	}
	stats, err := buildGIF(os.Args[1], os.Args[2], os.Args[3], os.Args[4])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("gif: %s  frames=%d  source_frames=%d  duration=%.1fs  size=%.2f MiB\n",
		os.Args[4], stats.frames, stats.sourceFrames, float64(stats.durationMS)/1000, float64(stats.size)/miB)
}

func buildGIF(framesDir, manifestPath, outDir, outGIF string) (buildStats, error) {
	entries, err := readManifest(manifestPath)
	if err != nil {
		return buildStats{}, err
	}
	frames, err := loadFrames(framesDir, entries, gifWidth)
	if err != nil {
		return buildStats{}, err
	}

	merged := mergeFrames(frames)
	sampled := sampleFrames(merged, maxGIFFrames)

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return buildStats{}, fmt.Errorf("create output directory: %w", err)
	}
	if err := encodeGIF(outGIF, sampled); err != nil {
		return buildStats{}, err
	}
	info, err := os.Stat(outGIF)
	if err != nil {
		return buildStats{}, fmt.Errorf("stat gif: %w", err)
	}
	duration := 0
	for _, frame := range sampled {
		duration += frame.delayMS
	}
	return buildStats{
		sourceFrames: len(frames),
		frames:       len(sampled),
		durationMS:   duration,
		size:         info.Size(),
	}, nil
}

func readManifest(path string) ([]manifestEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open manifest: %w", err)
	}
	defer f.Close()

	var entries []manifestEntry
	scanner := bufio.NewScanner(f)
	for line := 1; scanner.Scan(); line++ {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("manifest line %d: expected <name> <delay-ms>", line)
		}
		delay, err := strconv.Atoi(fields[1])
		if err != nil || delay < 0 {
			return nil, fmt.Errorf("manifest line %d: invalid delay %q", line, fields[1])
		}
		entries = append(entries, manifestEntry{name: fields[0], delayMS: delay})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	return entries, nil
}

func loadFrames(framesDir string, entries []manifestEntry, width int) ([]gifFrame, error) {
	frames := make([]gifFrame, 0, len(entries))
	for _, entry := range entries {
		path := filepath.Join(framesDir, entry.name+".png")
		img, err := readPNG(path)
		if errors.Is(err, os.ErrNotExist) {
			fmt.Printf("skip missing frame %s\n", path)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read frame %s: %w", path, err)
		}
		resized, err := resizeImage(img, width)
		if err != nil {
			return nil, fmt.Errorf("resize frame %s: %w", path, err)
		}
		// Quantize immediately so the full-size RGBA frames do not accumulate.
		// One fixed palette keeps colors stable; nearest-color mapping (rather
		// than dithering) also keeps the animation small.
		quantized := image.NewPaletted(resized.Bounds(), palette.Plan9)
		draw.Draw(quantized, quantized.Bounds(), resized, resized.Bounds().Min, draw.Src)
		frames = append(frames, gifFrame{img: quantized, delayMS: entry.delayMS})
	}
	if len(frames) == 0 {
		return nil, errors.New("no frames")
	}
	return frames, nil
}

func readPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

// resizeImage scales to width with bilinear interpolation. The fixed-point
// weights avoid allocating a color for every source and destination pixel.
func resizeImage(src image.Image, width int) (*image.RGBA, error) {
	bounds := src.Bounds()
	srcWidth, srcHeight := bounds.Dx(), bounds.Dy()
	if width <= 0 || srcWidth <= 0 || srcHeight <= 0 {
		return nil, errors.New("invalid image dimensions")
	}
	height := int(math.Round(float64(srcHeight) * float64(width) / float64(srcWidth)))
	if height < 1 {
		height = 1
	}

	rgba := image.NewRGBA(bounds)
	draw.Draw(rgba, bounds, src, bounds.Min, draw.Src)

	type sample struct {
		lo, hi int
		weight int
	}
	const weightScale = 1 << 16
	xs := make([]sample, width)
	scaleX := float64(srcWidth) / float64(width)
	for x := range xs {
		pos := (float64(x)+0.5)*scaleX - 0.5
		lo := int(math.Floor(pos))
		fraction := pos - float64(lo)
		if lo < 0 {
			lo, fraction = 0, 0
		}
		hi := min(lo+1, srcWidth-1)
		xs[x] = sample{lo: lo, hi: hi, weight: int(math.Round(fraction * weightScale))}
	}

	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	srcStride, dstStride := rgba.Stride, dst.Stride
	scaleY := float64(srcHeight) / float64(height)
	for y := 0; y < height; y++ {
		pos := (float64(y)+0.5)*scaleY - 0.5
		loY := int(math.Floor(pos))
		fractionY := pos - float64(loY)
		if loY < 0 {
			loY, fractionY = 0, 0
		}
		hiY := min(loY+1, srcHeight-1)
		weightY := int(math.Round(fractionY * weightScale))
		row0, row1 := loY*srcStride, hiY*srcStride
		for x, sx := range xs {
			i0, i1 := row0+sx.lo*4, row0+sx.hi*4
			j0, j1 := row1+sx.lo*4, row1+sx.hi*4
			topR := (int(rgba.Pix[i0])*sx.weight + int(rgba.Pix[i1])*(weightScale-sx.weight) + weightScale/2) >> 16
			botR := (int(rgba.Pix[j0])*sx.weight + int(rgba.Pix[j1])*(weightScale-sx.weight) + weightScale/2) >> 16
			topG := (int(rgba.Pix[i0+1])*sx.weight + int(rgba.Pix[i1+1])*(weightScale-sx.weight) + weightScale/2) >> 16
			botG := (int(rgba.Pix[j0+1])*sx.weight + int(rgba.Pix[j1+1])*(weightScale-sx.weight) + weightScale/2) >> 16
			topB := (int(rgba.Pix[i0+2])*sx.weight + int(rgba.Pix[i1+2])*(weightScale-sx.weight) + weightScale/2) >> 16
			botB := (int(rgba.Pix[j0+2])*sx.weight + int(rgba.Pix[j1+2])*(weightScale-sx.weight) + weightScale/2) >> 16
			topA := (int(rgba.Pix[i0+3])*sx.weight + int(rgba.Pix[i1+3])*(weightScale-sx.weight) + weightScale/2) >> 16
			botA := (int(rgba.Pix[j0+3])*sx.weight + int(rgba.Pix[j1+3])*(weightScale-sx.weight) + weightScale/2) >> 16
			k := y*dstStride + x*4
			dst.Pix[k] = uint8((topR*weightY + botR*(weightScale-weightY) + weightScale/2) >> 16)
			dst.Pix[k+1] = uint8((topG*weightY + botG*(weightScale-weightY) + weightScale/2) >> 16)
			dst.Pix[k+2] = uint8((topB*weightY + botB*(weightScale-weightY) + weightScale/2) >> 16)
			dst.Pix[k+3] = uint8((topA*weightY + botA*(weightScale-weightY) + weightScale/2) >> 16)
		}
	}
	return dst, nil
}

func mergeFrames(frames []gifFrame) []gifFrame {
	if len(frames) == 0 {
		return nil
	}
	merged := make([]gifFrame, len(frames))
	copy(merged, frames)
	n := 0
	for _, frame := range frames {
		// All frames use the same palette, so equal indices mean equal colors.
		if n > 0 && equalPaletted(merged[n-1].img, frame.img) {
			merged[n-1].delayMS += frame.delayMS
			continue
		}
		merged[n] = frame
		n++
	}
	return merged[:n]
}

func equalPaletted(a, b *image.Paletted) bool {
	return a.Bounds() == b.Bounds() && a.Stride == b.Stride && bytes.Equal(a.Pix, b.Pix)
}

// sampleFrames keeps the first frame of each timing group and adds the delays
// of the frames it replaces. The groups cover the input exactly once, so both
// the frame count and total duration stay bounded without losing screen time.
func sampleFrames(frames []gifFrame, maxFrames int) []gifFrame {
	if maxFrames <= 0 {
		return nil
	}
	if len(frames) <= maxFrames {
		return append([]gifFrame(nil), frames...)
	}
	if maxFrames == 1 {
		frame := gifFrame{img: frames[len(frames)-1].img}
		for i := range frames {
			frame.delayMS += frames[i].delayMS
		}
		return []gifFrame{frame}
	}

	// Reserve one group for the final screen and spread all earlier frames
	// evenly over the remaining groups.
	step := float64(len(frames)-1) / float64(maxFrames-1)
	sampled := make([]gifFrame, 0, maxFrames)
	start := 0
	for group := 0; group < maxFrames; group++ {
		end := len(frames)
		if group < maxFrames-1 {
			end = min(len(frames)-1, int(math.Ceil(float64(group+1)*step)))
		}
		frame := frames[start]
		for i := start + 1; i < end; i++ {
			frame.delayMS += frames[i].delayMS
		}
		sampled = append(sampled, frame)
		start = end
	}
	return sampled
}

func encodeGIF(path string, frames []gifFrame) error {
	if len(frames) == 0 {
		return errors.New("no frames")
	}
	bounds := frames[0].img.Bounds()
	encoded := make([]*image.Paletted, len(frames))
	delays := make([]int, len(frames))
	disposals := make([]byte, len(frames))
	for i, frame := range frames {
		if frame.img.Bounds() != bounds {
			return fmt.Errorf("frame %d dimensions changed", i)
		}
		// GIF delays are in centiseconds. Round to the nearest 10ms, as Pillow
		// does for the millisecond manifest durations.
		delay := (frame.delayMS + 5) / 10
		if delay > 0xffff {
			return fmt.Errorf("frame %d delay is too long: %dms", i, frame.delayMS)
		}
		encoded[i] = frame.img
		delays[i] = delay
		disposals[i] = 2 // restore the background before the next frame
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create gif: %w", err)
	}
	err = gif.EncodeAll(f, &gif.GIF{
		LoopCount: 0,
		Config: image.Config{
			ColorModel: color.Palette(palette.Plan9),
			Width:      bounds.Dx(),
			Height:     bounds.Dy(),
		},
		BackgroundIndex: 0,
		Image:           encoded,
		Delay:           delays,
		Disposal:        disposals,
	})
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("encode gif: %w", err)
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close gif: %w", closeErr)
	}
	return nil
}
