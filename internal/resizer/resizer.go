package resizer

import (
	"fmt"
	"image-proxy/internal/types"
	"strings"

	"github.com/davidbyttow/govips/v2/vips"
)

type LibvipsResizer struct{}

func NewResizer() *LibvipsResizer {
	return &LibvipsResizer{}
}

func (r *LibvipsResizer) Startup(debug bool) {
	if !debug {
		vips.LoggingSettings(func(domain string, level vips.LogLevel, msg string) {
			// Do nothing
		}, vips.LogLevelError)
	}
	vips.Startup(nil)
}

func (r *LibvipsResizer) Shutdown() {
	vips.Shutdown()
}

func (r *LibvipsResizer) Resize(data []byte, opts types.ImageOptions) ([]byte, string, error) {
	var image *vips.ImageRef
	var err error

	if opts.IsAnimated {
		// For animated images (GIF, WebP), we might need to load all pages
		importParams := vips.NewImportParams()
		importParams.NumPages.Set(-1)
		image, err = vips.LoadImageFromBuffer(data, importParams)
	} else {
		image, err = vips.LoadImageFromBuffer(data, nil)
	}

	if err != nil {
		return nil, "", fmt.Errorf("failed to load image: %w", err)
	}
	defer image.Close()

	// Logic for resizing
	width := opts.Width
	height := opts.Height

	if width <= 0 {
		width = 10000000
	}
	if height <= 0 {
		height = 10000000
	}

	// Handle version 1 logic (simple resize)
	if opts.Version == 1 {
		err = image.Thumbnail(width, height, vips.InterestingNone)
		if err != nil {
			return nil, "", err
		}
	} else {
		// Version 2/3 logic
		interesting := vips.InterestingNone
		if opts.Fit == "cover" {
			interesting = vips.InterestingAll
		}

		// govips.Thumbnail uses "inside" by default if no crop is specified
		// If width or height is 0, it preserves aspect ratio.
		err = image.Thumbnail(width, height, interesting)
		if err != nil {
			return nil, "", err
		}

		// Handle alpha/background
		if (!opts.KeepAlpha || opts.Format == "jpg") && image.HasAlpha() {
			err = image.Flatten(&vips.Color{R: 255, G: 255, B: 255})
			if err != nil {
				return nil, "", err
			}
		}
	}

	// Export to buffer
	var buf []byte
	var contentType string

	switch strings.ToLower(opts.Format) {
	case "png":
		params := vips.NewPngExportParams()
		buf, _, err = image.ExportPng(params)
		contentType = "image/png"
	case "webp":
		params := vips.NewWebpExportParams()
		buf, _, err = image.ExportWebp(params)
		contentType = "image/webp"
	case "avif":
		params := vips.NewAvifExportParams()
		buf, _, err = image.ExportAvif(params)
		contentType = "image/avif"
	case "jpg", "jpeg":
		params := vips.NewJpegExportParams()
		buf, _, err = image.ExportJpeg(params)
		contentType = "image/jpeg"
	default:
		// Default to original format if not specified, or JPEG
		params := vips.NewJpegExportParams()
		buf, _, err = image.ExportJpeg(params)
		contentType = "image/jpeg"
	}

	if err != nil {
		return nil, "", fmt.Errorf("failed to export image: %w", err)
	}

	return buf, contentType, nil
}
