package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"testing"
)

// makePartial builds a base64 JPEG of w x rows, solid red — mimicking qsstv's
// rx_line payload, which is cropped to the rows decoded so far.
func makePartial(w, rows int) string {
	img := image.NewRGBA(image.Rect(0, 0, w, rows))
	for y := 0; y < rows; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: 90})
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func decodeThumb(t *testing.T, b64 string) image.Image {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("b64: %v", err)
	}
	img, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("jpeg: %v", err)
	}
	return img
}

func isRedish(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return r>>8 > 150 && g>>8 < 110 && b>>8 < 110
}
func isDark(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return r>>8 < 60 && g>>8 < 60 && b>>8 < 60
}

func TestRailThumbPartialFillsTopFraction(t *testing.T) {
	// 100 of 256 lines decoded -> decoded strip should be ~39% of a 120px box.
	out, ok := railThumb(makePartial(320, 100), 100, 256)
	if !ok {
		t.Fatal("railThumb returned !ok")
	}
	img := decodeThumb(t, out)
	if got := img.Bounds().Dx(); got != 160 {
		t.Errorf("width = %d, want 160", got)
	}
	if got := img.Bounds().Dy(); got != 120 {
		t.Errorf("height = %d, want 120", got)
	}
	lines, total, boxH := 100.0, 256.0, 120.0
	wantFill := int(math.Round(boxH * lines / total)) // 47
	if !isRedish(img.At(80, wantFill-4)) {
		t.Errorf("row %d should be inside the decoded strip, got %v", wantFill-4, img.At(80, wantFill-4))
	}
	if !isDark(img.At(80, wantFill+6)) {
		t.Errorf("row %d should be undecoded/black, got %v", wantFill+6, img.At(80, wantFill+6))
	}
	if !isDark(img.At(80, 119)) {
		t.Errorf("last row should be black, got %v", img.At(80, 119))
	}
}

func TestRailThumbCompleteFillsBox(t *testing.T) {
	out, ok := railThumb(makePartial(320, 256), 256, 256)
	if !ok {
		t.Fatal("!ok")
	}
	img := decodeThumb(t, out)
	for _, y := range []int{2, 60, 117} {
		if !isRedish(img.At(80, y)) {
			t.Errorf("full frame: row %d should be filled, got %v", y, img.At(80, y))
		}
	}
}

func TestRailThumbFirstLine(t *testing.T) {
	// The real caller passes ev.Line+1, so the minimum is 1.
	if _, ok := railThumb(makePartial(320, 1), 1, 256); !ok {
		t.Error("single decoded line should still produce a thumb")
	}
}

func TestRailThumbRejectsGarbage(t *testing.T) {
	for name, in := range map[string]string{
		"empty":      "",
		"not-b64":    "!!!!not base64!!!!",
		"b64-notjpg": base64.StdEncoding.EncodeToString([]byte("hello world")),
	} {
		if _, ok := railThumb(in, 10, 256); ok {
			t.Errorf("%s: expected ok=false", name)
		}
	}
}
