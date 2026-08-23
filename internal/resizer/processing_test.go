package resizer

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"image-proxy/internal/procopts"
	"image-proxy/internal/types"

	"github.com/davidbyttow/govips/v2/vips"
)

// synthPNG builds an opaque PNG of exactly w x h. The four quadrants get
// distinct colours so gravity assertions can check which region survived a
// crop, and a per-pixel wobble keeps the image from being uniform (a flat
// image gives libvips' attention heuristic nothing to work with).
func synthPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBA{A: 255}
			switch {
			case x < w/2 && y < h/2:
				c.R = 255 // north-west: red
			case x >= w/2 && y < h/2:
				c.G = 255 // north-east: green
			case x < w/2 && y >= h/2:
				c.B = 255 // south-west: blue
			default:
				c.R, c.G = 255, 255 // south-east: yellow
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("failed to encode synthetic png: %v", err)
	}
	return buf.Bytes()
}

// synthTransparentPNG builds a fully transparent PNG of exactly w x h.
func synthTransparentPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("failed to encode synthetic png: %v", err)
	}
	return buf.Bytes()
}

// processed runs the imgproxy pipeline for the given option segments and
// returns the decoded result. Options are written in URL syntax so the test
// reads the way a client would write the request.
func processed(t *testing.T, src []byte, format string, segments ...string) *vips.ImageRef {
	t.Helper()
	opts, _, err := procopts.Parse(append(append([]string{}, segments...), "13", "products", "foo."+format))
	if err != nil {
		t.Fatalf("failed to parse %q: %v", segments, err)
	}
	out, _, err := NewResizer().Resize(src, types.ImageOptions{Format: format, Processing: &opts})
	if err != nil {
		t.Fatalf("Resize(%q) failed: %v", segments, err)
	}
	img, err := vips.LoadImageFromBuffer(out, nil)
	if err != nil {
		t.Fatalf("failed to load result of %q: %v", segments, err)
	}
	return img
}

func assertSize(t *testing.T, img *vips.ImageRef, wantW, wantH int, what string) {
	t.Helper()
	if img.Width() != wantW || img.Height() != wantH {
		t.Errorf("%s: got %dx%d, want %dx%d", what, img.Width(), img.Height(), wantW, wantH)
	}
}

func TestProcessing_ResizingTypeGeometry(t *testing.T) {
	landscape := synthPNG(t, 400, 200)
	portrait := synthPNG(t, 200, 400)
	small := synthPNG(t, 100, 50)

	tests := []struct {
		name         string
		src          []byte
		segments     []string
		wantW, wantH int
	}{
		// fit: scale to sit inside the box, aspect ratio preserved.
		{"fit landscape into a square", landscape, []string{"rt:fit", "w:200", "h:200"}, 200, 100},
		{"fit portrait into a square", portrait, []string{"rt:fit", "w:200", "h:200"}, 100, 200},
		{"fit is the default resizing type", landscape, []string{"w:200", "h:200"}, 200, 100},
		{"fit will not enlarge by default", small, []string{"rt:fit", "w:400", "h:400"}, 100, 50},
		{"fit enlarges when asked", small, []string{"rt:fit", "w:400", "h:400", "el:1"}, 400, 200},

		// fill: cover the box exactly, cropping the excess.
		{"fill landscape into a square", landscape, []string{"rt:fill", "w:200", "h:200"}, 200, 200},
		{"fill portrait into a square", portrait, []string{"rt:fill", "w:200", "h:200"}, 200, 200},
		{"fill enlarges when asked", small, []string{"rt:fill", "w:400", "h:400", "el:1"}, 400, 400},

		// force: exact box, aspect ratio ignored.
		{"force stretches to the exact box", landscape, []string{"rt:force", "w:200", "h:300"}, 200, 300},
		{"force ignores enlarge", small, []string{"rt:force", "w:400", "h:400"}, 400, 400},

		// auto: fill when orientations agree, fit when they do not.
		{"auto picks fill for a matching orientation", landscape, []string{"rt:auto", "w:300", "h:100"}, 300, 100},
		{"auto picks fit for a differing orientation", landscape, []string{"rt:auto", "w:100", "h:300"}, 100, 50},

		// A single dimension derives the other from the source ratio.
		{"width only derives height", landscape, []string{"w:100"}, 100, 50},
		{"height only derives width", landscape, []string{"h:100"}, 200, 100},
		{"width only on a portrait source", portrait, []string{"w:100"}, 100, 200},

		// No dimensions at all is a pure format conversion.
		{"no dimensions leaves the image alone", landscape, []string{}, 400, 200},
		{"background alone does not resize", landscape, []string{"bg:fff"}, 400, 200},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			img := processed(t, tt.src, "png", tt.segments...)
			defer img.Close()
			assertSize(t, img, tt.wantW, tt.wantH, tt.name)
		})
	}
}

// TestProcessing_FillDownNeverEnlarges pins the property that distinguishes
// fill-down from fill: the result is never larger than the source, but it
// still lands on the requested aspect ratio.
func TestProcessing_FillDownNeverEnlarges(t *testing.T) {
	small := synthPNG(t, 100, 50)

	img := processed(t, small, "png", "rt:fill-down", "w:400", "h:400")
	defer img.Close()

	// The largest 1:1 box available inside a 100x50 source is 50x50.
	assertSize(t, img, 50, 50, "fill-down on a source smaller than the request")

	// enlarge:1 must not override fill-down's defining behaviour.
	forced := processed(t, small, "png", "rt:fill-down", "w:400", "h:400", "el:1")
	defer forced.Close()
	if forced.Width() > 100 || forced.Height() > 50 {
		t.Errorf("fill-down with enlarge:1 enlarged the source: got %dx%d", forced.Width(), forced.Height())
	}
}

func TestProcessing_Crop(t *testing.T) {
	landscape := synthPNG(t, 400, 200)

	tests := []struct {
		name         string
		segments     []string
		wantW, wantH int
	}{
		{"absolute crop", []string{"c:200:100"}, 200, 100},
		{"relative crop", []string{"c:0.5:0.5"}, 200, 100},
		{"zero means the full dimension", []string{"c:100:0"}, 100, 200},
		{"a crop larger than the source is clamped", []string{"c:800:800"}, 400, 200},
		{"a crop covering the whole image is a no-op", []string{"c:0:0"}, 400, 200},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			img := processed(t, landscape, "png", tt.segments...)
			defer img.Close()
			assertSize(t, img, tt.wantW, tt.wantH, tt.name)
		})
	}
}

// TestProcessing_CropHappensBeforeResize is the discriminating test for
// operation order. Cropping 200x200 out of a 400x200 source and then fitting
// to width 100 gives a square; resizing first would give 100x50.
func TestProcessing_CropHappensBeforeResize(t *testing.T) {
	img := processed(t, synthPNG(t, 400, 200), "png", "c:200:200", "w:100")
	defer img.Close()
	assertSize(t, img, 100, 100, "crop before resize")
}

func TestProcessing_CropGravity(t *testing.T) {
	// Quadrant colours from synthPNG: NW red, NE green, SW blue, SE yellow.
	src := synthPNG(t, 400, 200)

	tests := []struct {
		gravity string
		wantR   float64
		wantG   float64
		wantB   float64
		corner  string
	}{
		{"nowe", 255, 0, 0, "north-west is red"},
		{"noea", 0, 255, 0, "north-east is green"},
		{"sowe", 0, 0, 255, "south-west is blue"},
		{"soea", 255, 255, 0, "south-east is yellow"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.gravity, func(t *testing.T) {
			// Take a quarter-size window so exactly one quadrant survives.
			img := processed(t, src, "png", "c:200:100:"+tt.gravity)
			defer img.Close()
			assertSize(t, img, 200, 100, "crop "+tt.gravity)

			pixel, err := img.GetPoint(100, 50)
			if err != nil {
				t.Fatalf("GetPoint failed: %v", err)
			}
			if pixel[0] != tt.wantR || pixel[1] != tt.wantG || pixel[2] != tt.wantB {
				t.Errorf("%s: centre pixel = (%.0f, %.0f, %.0f), want (%.0f, %.0f, %.0f)",
					tt.corner, pixel[0], pixel[1], pixel[2], tt.wantR, tt.wantG, tt.wantB)
			}
		})
	}
}

func TestProcessing_Extend(t *testing.T) {
	small := synthPNG(t, 100, 50)

	t.Run("extend pads out to the requested box", func(t *testing.T) {
		img := processed(t, small, "png", "w:400", "h:400", "ex:1")
		defer img.Close()
		assertSize(t, img, 400, 400, "extend")
	})

	// extend_aspect_ratio pads to the requested RATIO, not the requested
	// size, which is what separates it from extend.
	t.Run("extend_aspect_ratio pads to the ratio only", func(t *testing.T) {
		img := processed(t, small, "png", "w:400", "h:400", "exar:1")
		defer img.Close()
		assertSize(t, img, 100, 100, "extend_aspect_ratio")
	})

	t.Run("extend_aspect_ratio on a non-square ratio", func(t *testing.T) {
		// 100x50 source, requested ratio 1:2 -> pad the height to 200.
		img := processed(t, small, "png", "w:100", "h:200", "exar:1")
		defer img.Close()
		assertSize(t, img, 100, 200, "extend_aspect_ratio 1:2")
	})

	t.Run("extend never shrinks", func(t *testing.T) {
		img := processed(t, synthPNG(t, 400, 200), "png", "w:100", "h:100", "ex:1", "el:0")
		defer img.Close()
		// fit to 100x100 gives 100x50, then extend pads to 100x100.
		assertSize(t, img, 100, 100, "extend after fit")
	})
}

func TestProcessing_Background(t *testing.T) {
	t.Run("transparent source flattens to white for jpeg", func(t *testing.T) {
		img := processed(t, synthTransparentPNG(t, 10, 10), "jpg")
		defer img.Close()
		pixel, err := img.GetPoint(5, 5)
		if err != nil {
			t.Fatalf("GetPoint failed: %v", err)
		}
		if pixel[0] < 250 || pixel[1] < 250 || pixel[2] < 250 {
			t.Errorf("got (%.0f, %.0f, %.0f), want white", pixel[0], pixel[1], pixel[2])
		}
	})

	t.Run("explicit background flattens transparency", func(t *testing.T) {
		img := processed(t, synthTransparentPNG(t, 10, 10), "png", "bg:ff0000")
		defer img.Close()
		pixel, err := img.GetPoint(5, 5)
		if err != nil {
			t.Fatalf("GetPoint failed: %v", err)
		}
		if pixel[0] != 255 || pixel[1] != 0 || pixel[2] != 0 {
			t.Errorf("got (%.0f, %.0f, %.0f), want red", pixel[0], pixel[1], pixel[2])
		}
	})

	t.Run("transparent source keeps alpha for png without a background", func(t *testing.T) {
		img := processed(t, synthTransparentPNG(t, 10, 10), "png")
		defer img.Close()
		if !img.HasAlpha() {
			t.Error("alpha channel was dropped from a png with no background requested")
		}
	})

	t.Run("extend pads with the requested background", func(t *testing.T) {
		img := processed(t, synthPNG(t, 100, 50), "png", "w:100", "h:100", "ex:1", "bg:ff0000")
		defer img.Close()
		assertSize(t, img, 100, 100, "extend with background")
		// The image is centred vertically, so the top row is pad.
		pixel, err := img.GetPoint(50, 2)
		if err != nil {
			t.Fatalf("GetPoint failed: %v", err)
		}
		if pixel[0] != 255 || pixel[1] != 0 || pixel[2] != 0 {
			t.Errorf("pad pixel = (%.0f, %.0f, %.0f), want red", pixel[0], pixel[1], pixel[2])
		}
	})
}

// TestProcessing_BackgroundSpellingsAreEquivalent backs the canonicalisation
// contract with real bytes: if three spellings of white produced different
// output, collapsing them to one cache key would serve the wrong image.
func TestProcessing_BackgroundSpellingsAreEquivalent(t *testing.T) {
	src := synthTransparentPNG(t, 10, 10)
	var first []byte
	for _, spelling := range []string{"bg:fff", "bg:ffffff", "bg:FFFFFF", "bg:255:255:255"} {
		opts, _, err := procopts.Parse([]string{spelling, "13", "products", "foo.png"})
		if err != nil {
			t.Fatalf("failed to parse %q: %v", spelling, err)
		}
		out, _, err := NewResizer().Resize(src, types.ImageOptions{Format: "png", Processing: &opts})
		if err != nil {
			t.Fatalf("Resize(%q) failed: %v", spelling, err)
		}
		if first == nil {
			first = out
			continue
		}
		if !bytes.Equal(first, out) {
			t.Errorf("%q produced different bytes than the first spelling", spelling)
		}
	}
}

// TestProcessing_LegacyPipelineUntouched guards spec N2: a nil Processing
// must take the legacy branch, byte-for-byte.
func TestProcessing_LegacyPipelineUntouched(t *testing.T) {
	src := synthPNG(t, 400, 200)
	legacy := types.ImageOptions{Width: 100, Height: 100, Version: 1, Fit: "cover", Format: "png"}

	out, _, err := NewResizer().Resize(src, legacy)
	if err != nil {
		t.Fatalf("legacy Resize failed: %v", err)
	}
	img, err := vips.LoadImageFromBuffer(out, nil)
	if err != nil {
		t.Fatalf("failed to load legacy result: %v", err)
	}
	defer img.Close()
	assertSize(t, img, 100, 100, "legacy cover")

	// The same request through the new pipeline is a different code path;
	// it must not accidentally be wired to the legacy fields.
	opts := procopts.Options{Width: 100, Height: 100}
	out2, _, err := NewResizer().Resize(src, types.ImageOptions{
		Width: 999, Height: 999, Fit: "cover", Format: "png", Processing: &opts,
	})
	if err != nil {
		t.Fatalf("processing Resize failed: %v", err)
	}
	img2, err := vips.LoadImageFromBuffer(out2, nil)
	if err != nil {
		t.Fatalf("failed to load processing result: %v", err)
	}
	defer img2.Close()
	// fit (the default) into 100x100 from a 2:1 source gives 100x50 — the
	// legacy Width/Height/Fit fields were correctly ignored.
	assertSize(t, img2, 100, 50, "processing ignores legacy fields")
}
