package resizer

import (
	"fmt"
	"image-proxy/internal/types"
	"os"
	"testing"

	"github.com/davidbyttow/govips/v2/vips"
)

func TestMain(m *testing.M) {
	vips.Startup(nil)
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
