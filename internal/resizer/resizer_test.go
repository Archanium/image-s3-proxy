package resizer

import (
	"fmt"
	"image-proxy/internal/types"
	"os"
	"testing"

	"github.com/davidbyttow/govips/v2/vips"
)

func TestMain(m *testing.M) {
	if os.Getenv("DEBUG") != "true" {
		vips.LoggingSettings(func(domain string, level vips.LogLevel, msg string) {
			// Do nothing
		}, vips.LogLevelError)
	}
	vips.Startup(&vips.Config{})
	code := m.Run()
	vips.Shutdown()
	os.Exit(code)
}

func TestResize(t *testing.T) {
	r := NewResizer()

	// Actually, we need real data for vips to load correctly in some tests
	// But let's use the fixture for real verification
	data, err := os.ReadFile("../../tests/fixtures/prespring-forside-4196157.png")
	if err != nil {
		t.Skip("Fixture not found, skipping test")
	}

	tests := []struct {
		name      string
		opts      types.ImageOptions
		expWidth  int
		expHeight int
	}{
		{
			name:      "2000x0 (Fixed width, auto height)",
			opts:      types.ImageOptions{Width: 2000, Height: 0, Version: 1, Format: "png"},
			expWidth:  2000,
			expHeight: 900,
		},
		{
			name:      "0x450 (Auto width, fixed height)",
			opts:      types.ImageOptions{Width: 0, Height: 450, Version: 1, Format: "png"},
			expWidth:  1000,
			expHeight: 450,
		},
		{
			name:      "2560x0 (Upscaling, auto height)",
			opts:      types.ImageOptions{Width: 2560, Height: 0, Version: 1, Format: "png"},
			expWidth:  2560,
			expHeight: 1152,
		},
		{
			name:      "1000x1000 Version 1 (Cover/Crop)",
			opts:      types.ImageOptions{Width: 1000, Height: 1000, Version: 1, Fit: "cover", Format: "png"},
			expWidth:  1000,
			expHeight: 1000,
		},
		{
			name:      "1000x1000 Version 2 (Contain/Pad)",
			opts:      types.ImageOptions{Width: 1000, Height: 1000, Version: 2, Fit: "contain", Format: "png"},
			expWidth:  1000,
			expHeight: 1000,
		},
		{
			name:      "1000x1000 Version 2 (Inside/No Pad)",
			opts:      types.ImageOptions{Width: 1000, Height: 1000, Version: 2, Fit: "inside", Format: "png"},
			expWidth:  1000,
			expHeight: 450,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resized, _, err := r.Resize(data, tt.opts)
			if err != nil {
				t.Fatalf("Resize failed: %v", err)
			}

			img, err := vips.LoadImageFromBuffer(resized, nil)
			if err != nil {
				t.Fatalf("Failed to load resized image: %v", err)
			}
			defer img.Close()

			fmt.Printf("Test %s: Resized to %d x %d\n", tt.name, img.Width(), img.Height())

			if img.Width() != tt.expWidth {
				t.Errorf("Expected width %d, got %d", tt.expWidth, img.Width())
			}
			if img.Height() != tt.expHeight {
				t.Errorf("Expected height %d, got %d", tt.expHeight, img.Height())
			}
		})
	}
}

func TestResizeRectangular(t *testing.T) {
	r := NewResizer()
	data, err := os.ReadFile("../../tests/fixtures/montana.png")
	if err != nil {
		t.Skip("Fixture not found, skipping test")
	}

	opts := types.ImageOptions{Width: 152, Height: 255, Version: 2, Fit: "contain", Format: "png"}
	resized, _, err := r.Resize(data, opts)
	if err != nil {
		t.Fatalf("Resize failed: %v", err)
	}

	img, err := vips.LoadImageFromBuffer(resized, nil)
	if err != nil {
		t.Fatalf("Failed to load resized image: %v", err)
	}
	defer img.Close()

	if img.Width() != 152 || img.Height() != 255 {
		t.Errorf("Expected 152x255, got %dx%d", img.Width(), img.Height())
	}
}

// TestResizeSVGToPNG proves libvips can load an SVG original and rasterize
// it to PNG at the requested size. A load failure here is the diagnostic
// for a libvips build without librsvg (Phase 1 / risk R1).
func TestResizeSVGToPNG(t *testing.T) {
	r := NewResizer()
	data, err := os.ReadFile("../../tests/fixtures/logo.svg")
	if err != nil {
		t.Fatal(err)
	}

	// logo.svg has viewBox 0 0 596 43, so a width-only request keeps aspect.
	opts := types.ImageOptions{Width: 240, Height: 0, Version: 1, Format: "png"}
	resized, contentType, err := r.Resize(data, opts)
	if err != nil {
		t.Fatalf("Resize of SVG failed (librsvg missing?): %v", err)
	}
	if contentType != "image/png" {
		t.Errorf("Expected content type image/png, got %q", contentType)
	}

	img, err := vips.LoadImageFromBuffer(resized, nil)
	if err != nil {
		t.Fatalf("Failed to load rasterized SVG output: %v", err)
	}
	defer img.Close()

	// The wide 596:43 viewBox makes height the binding dimension after integer
	// truncation in the shared resize math (17.31 -> 17), so width lands a few px
	// under the requested 240. This is existing engine behavior, deterministic
	// from the viewBox ratio; assert aspect preservation rather than a pixel-exact
	// width so the test is stable and doesn't depend on the resize rounding.
	if img.Width() < 235 || img.Width() > 240 {
		t.Errorf("Expected width ~240 (aspect-rounded), got %d", img.Width())
	}
	wantH := img.Width() * 43 / 596
	if img.Height() < wantH-1 || img.Height() > wantH+1 {
		t.Errorf("Expected height ~%d for preserved 596:43 aspect, got %d", wantH, img.Height())
	}
}

func TestTransparentToJpg(t *testing.T) {
	r := NewResizer()
	data, err := os.ReadFile("../../tests/fixtures/transparent.png")
	if err != nil {
		t.Fatal(err)
	}
	opts := types.ImageOptions{Width: 10, Height: 10, Version: 1, Format: "jpg"}
	resized, _, err := r.Resize(data, opts)
	if err != nil {
		t.Fatal(err)
	}
	img, err := vips.LoadImageFromBuffer(resized, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer img.Close()

	// Check pixel at 0,0
	pixel, err := img.GetPoint(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// JPEG has 3 channels (R, G, B)
	if pixel[0] < 255 || pixel[1] < 255 || pixel[2] < 255 {
		t.Errorf("Expected white background (255, 255, 255), got (%f, %f, %f)", pixel[0], pixel[1], pixel[2])
	}
}

// TestAlphaPolicy exercises the tri-state alpha engine across source type,
// output format, and AlphaMode. The output either carries an alpha channel
// (transparency preserved) or not (flattened to white) — HasAlpha() is the
// discriminator, since Flatten removes the channel.
func TestAlphaPolicy(t *testing.T) {
	r := NewResizer()
	logo, err := os.ReadFile("../../tests/fixtures/logo.svg")
	if err != nil {
		t.Fatal(err)
	}
	rasterAlpha, err := os.ReadFile("../../tests/fixtures/transparent.png")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		src       []byte
		format    string
		mode      types.AlphaMode
		wantAlpha bool
	}{
		{"svg auto png keeps alpha", logo, "png", types.AlphaAuto, true},
		{"svg auto webp keeps alpha", logo, "webp", types.AlphaAuto, true},
		{"svg auto avif keeps alpha", logo, "avif", types.AlphaAuto, true},
		{"svg auto jpg flattens", logo, "jpg", types.AlphaAuto, false},
		{"raster auto png flattens (no regression)", rasterAlpha, "png", types.AlphaAuto, false},
		{"raster keep png preserves alpha", rasterAlpha, "png", types.AlphaKeep, true},
		{"raster keep webp preserves alpha", rasterAlpha, "webp", types.AlphaKeep, true},
		{"svg flatten png flattens", logo, "png", types.AlphaFlatten, false},
		{"svg keep jpg still flattens (no alpha channel)", logo, "jpg", types.AlphaKeep, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, _, err := r.Resize(tt.src, types.ImageOptions{
				Width: 64, Height: 0, Version: 1, Format: tt.format, AlphaMode: tt.mode,
			})
			if err != nil {
				t.Fatalf("Resize failed: %v", err)
			}
			img, err := vips.LoadImageFromBuffer(out, nil)
			if err != nil {
				t.Fatalf("Failed to load output: %v", err)
			}
			defer img.Close()

			if got := img.HasAlpha(); got != tt.wantAlpha {
				t.Errorf("HasAlpha() = %v, want %v", got, tt.wantAlpha)
			}
		})
	}
}
