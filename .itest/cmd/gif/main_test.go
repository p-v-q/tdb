package main

import (
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadManifest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.txt")
	data := "\n  00000\t110  \n00001 250\n\n00002 0\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readManifest(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	want := []manifestEntry{
		{name: "00000", delayMS: 110},
		{name: "00001", delayMS: 250},
		{name: "00002", delayMS: 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %#v, want %#v", got, want)
	}
}

func TestReadManifestRejectsInvalidLines(t *testing.T) {
	for _, line := range []string{"00000", "00000 slow", "00000 -1", "00000 10 extra"} {
		t.Run(line, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "manifest.txt")
			if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := readManifest(path); err == nil {
				t.Fatalf("readManifest accepted %q", line)
			}
		})
	}
}

func TestMergeFramesAddsIdenticalFrameDelays(t *testing.T) {
	frames := []gifFrame{
		testFrame(1, 100),
		testFrame(1, 200),
		testFrame(2, 300),
		testFrame(2, 400),
		testFrame(1, 500),
	}
	got := mergeFrames(frames)
	if len(got) != 3 {
		t.Fatalf("merged %d frames, want 3", len(got))
	}
	for i, want := range []struct {
		pixel   byte
		delayMS int
	}{{1, 300}, {2, 700}, {1, 500}} {
		if got[i].img.Pix[0] != want.pixel || got[i].delayMS != want.delayMS {
			t.Errorf("frame %d = (pixel %d, delay %d), want (%d, %d)",
				i, got[i].img.Pix[0], got[i].delayMS, want.pixel, want.delayMS)
		}
	}
	if frames[0].delayMS != 100 {
		t.Fatal("mergeFrames mutated its input")
	}
}

func TestSampleFramesBoundsCountAndPreservesDuration(t *testing.T) {
	frames := make([]gifFrame, 250)
	total := 0
	for i := range frames {
		frames[i] = testFrame(byte(i), i+1)
		total += i + 1
	}
	got := sampleFrames(frames, maxGIFFrames)
	if len(got) != maxGIFFrames {
		t.Fatalf("sampled %d frames, want %d", len(got), maxGIFFrames)
	}
	gotTotal := 0
	previous := -1
	for i, frame := range got {
		pixel := int(frame.img.Pix[0])
		if pixel <= previous {
			t.Fatalf("frame %d pixel %d is not after %d", i, pixel, previous)
		}
		previous = pixel
		gotTotal += frame.delayMS
	}
	if gotTotal != total {
		t.Fatalf("sampled duration = %dms, want %dms", gotTotal, total)
	}
	if got[0].img.Pix[0] != 0 || got[len(got)-1].img.Pix[0] != 249 {
		t.Fatalf("sampled endpoints = %d, %d; want 0, 249", got[0].img.Pix[0], got[len(got)-1].img.Pix[0])
	}
}

func TestSampleFramesCapsJustOverLimit(t *testing.T) {
	frames := make([]gifFrame, maxGIFFrames+1)
	total := 0
	for i := range frames {
		frames[i] = testFrame(byte(i), 10)
		total += 10
	}
	got := sampleFrames(frames, maxGIFFrames)
	if len(got) != maxGIFFrames {
		t.Fatalf("sampled %d frames, want %d", len(got), maxGIFFrames)
	}
	if got[len(got)-1].img.Pix[0] != byte(len(frames)-1) {
		t.Fatalf("final sampled pixel = %d, want %d", got[len(got)-1].img.Pix[0], len(frames)-1)
	}
	gotTotal := 0
	for _, frame := range got {
		gotTotal += frame.delayMS
	}
	if gotTotal != total {
		t.Fatalf("sampled duration = %dms, want %dms", gotTotal, total)
	}
}

func TestSampleFramesSpreadsLongRecording(t *testing.T) {
	frames := make([]gifFrame, 1000)
	for i := range frames {
		frames[i] = testFrame(1, 1)
	}
	got := sampleFrames(frames, maxGIFFrames)
	if len(got) != maxGIFFrames {
		t.Fatalf("sampled %d frames, want %d", len(got), maxGIFFrames)
	}
	total, maxDelay := 0, 0
	for _, frame := range got {
		total += frame.delayMS
		maxDelay = max(maxDelay, frame.delayMS)
	}
	if total != len(frames) {
		t.Fatalf("sampled duration = %dms, want %dms", total, len(frames))
	}
	if maxDelay > 10 {
		t.Fatalf("largest sampled delay = %dms, want at most 10ms", maxDelay)
	}
}

func TestSampleFramesSingleFrameLimit(t *testing.T) {
	frames := []gifFrame{testFrame(1, 100), testFrame(2, 200), testFrame(3, 300)}
	got := sampleFrames(frames, 1)
	if len(got) != 1 || got[0].img.Pix[0] != 3 || got[0].delayMS != 600 {
		t.Fatalf("sampleFrames with limit 1 = %#v, want final frame with 600ms", got)
	}
}

func TestSampleFramesLeavesShortRecordingAlone(t *testing.T) {
	frames := []gifFrame{testFrame(1, 100), testFrame(2, 200), testFrame(3, 300)}
	got := sampleFrames(frames, maxGIFFrames)
	if !reflect.DeepEqual(got, frames) {
		t.Fatalf("sampleFrames changed a short recording: %#v", got)
	}
}

func TestBuildGIFMergesSkipsAndLoops(t *testing.T) {
	dir := t.TempDir()
	framesDir := filepath.Join(dir, "frames")
	if err := os.MkdirAll(framesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestPNG(t, filepath.Join(framesDir, "00000.png"), color.RGBA{R: 255, A: 255})
	writeTestPNG(t, filepath.Join(framesDir, "00001.png"), color.RGBA{R: 255, A: 255})
	writeTestPNG(t, filepath.Join(framesDir, "00002.png"), color.RGBA{B: 255, A: 255})
	manifest := filepath.Join(dir, "manifest.txt")
	if err := os.WriteFile(manifest, []byte("00000 100\n00001 200\nmissing 50\n00002 300\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outGIF := filepath.Join(dir, "demo.gif")
	stats, err := buildGIF(framesDir, manifest, dir, outGIF)
	if err != nil {
		t.Fatalf("build gif: %v", err)
	}
	if stats.sourceFrames != 3 || stats.frames != 2 || stats.durationMS != 600 {
		t.Fatalf("stats = %#v, want 3 source, 2 merged, 600ms", stats)
	}

	f, err := os.Open(outGIF)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	decoded, err := gif.DecodeAll(f)
	if err != nil {
		t.Fatalf("decode gif: %v", err)
	}
	if decoded.LoopCount != 0 {
		t.Errorf("loop count = %d, want 0", decoded.LoopCount)
	}
	if len(decoded.Image) != 2 {
		t.Fatalf("decoded %d frames, want 2", len(decoded.Image))
	}
	if decoded.Config.Width != gifWidth || decoded.Config.Height != 490 {
		t.Errorf("dimensions = %dx%d, want %dx490", decoded.Config.Width, decoded.Config.Height, gifWidth)
	}
	for i, delay := range decoded.Delay {
		if delay != 30 {
			t.Errorf("frame %d delay = %dcs, want 30cs", i, delay)
		}
	}
}

func testFrame(pixel byte, delayMS int) gifFrame {
	img := image.NewPaletted(image.Rect(0, 0, 2, 1), palette.Plan9)
	img.Pix[0], img.Pix[1] = pixel, pixel
	return gifFrame{img: img, delayMS: delayMS}
}

func writeTestPNG(t *testing.T, path string, c color.Color) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 20, 10))
	r, g, b, a := c.RGBA()
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = byte(r>>8), byte(g>>8), byte(b>>8), byte(a>>8)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
