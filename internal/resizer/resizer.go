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

func (r *LibvipsResizer) Startup(debug bool, concurrency int, maxCacheMem int, maxCacheSize int) {
	if !debug {
		vips.LoggingSettings(func(domain string, level vips.LogLevel, msg string) {
			// Do nothing
		}, vips.LogLevelError)
	}

	config := &vips.Config{
		ConcurrencyLevel: concurrency,
		MaxCacheMem:      maxCacheMem,
		MaxCacheSize:     maxCacheSize,
	}
	vips.Startup(config)
}

func (r *LibvipsResizer) Shutdown() {
	vips.Shutdown()
}

func (r *LibvipsResizer) Resize(data []byte, opts types.ImageOptions) ([]byte, string, error) {
	var image *vips.ImageRef
	var err error

	if opts.IsAnimated {
		// For animated images (GIF, WebP, AVIF), we might need to load all pages
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

	// Whether the original is a vector SVG governs the default alpha policy:
	// SVG originals keep transparency, raster originals flatten (see
	// effectiveKeepAlpha). DetermineImageType sniffs the buffer, so it is
	// independent of the resize pipeline.
	inputIsSVG := vips.DetermineImageType(data) == vips.ImageTypeSVG

	// Logic for resizing
	width := opts.Width
	height := opts.Height

	origW := image.Width()
	origH := image.Height()

	if width <= 0 && height > 0 {
		width = int(float64(origW) * float64(height) / float64(origH))
	} else if height <= 0 && width > 0 {
		height = int(float64(origH) * float64(width) / float64(origW))
	}

	// Handle version logic
	interesting := vips.InterestingNone
	if opts.Fit == "cover" {
		interesting = vips.InterestingCentre
	}

	if opts.Fit == "contain" && opts.Width > 0 && opts.Height > 0 {
		targetRatio := float64(opts.Width) / float64(opts.Height)
		origRatio := float64(origW) / float64(origH)

		if origRatio > targetRatio {
			// Width is limiting
			height = int(float64(opts.Width) / origRatio)
			width = opts.Width
		} else {
			// Height is limiting
			width = int(float64(opts.Height) * origRatio)
			height = opts.Height
		}
	}

	// Use ThumbnailWithSize to ensure we don't stretch the image.
	err = image.ThumbnailWithSize(width, height, interesting, vips.SizeBoth)
	if err != nil {
		return nil, "", err
	}

	// Handle "contain" padding if both dimensions were provided
	if opts.Fit == "contain" && opts.Width > 0 && opts.Height > 0 {
		currW := image.Width()
		currH := image.Height()
		if currW != opts.Width || currH != opts.Height {
			left := (opts.Width - currW) / 2
			top := (opts.Height - currH) / 2
			extend := vips.ExtendWhite // Default to white for Version 2/3 behavior
			// If keepAlpha is true, maybe use ExtendBackground?
			// But Node.js default for contain uses white.
			err = image.Embed(left, top, opts.Width, opts.Height, extend)
			if err != nil {
				return nil, "", fmt.Errorf("failed to embed image: %w", err)
			}
		}
	}

	// Handle alpha/background: flatten to a white background unless the
	// effective policy preserves transparency for this source + format.
	if !effectiveKeepAlpha(opts.AlphaMode, inputIsSVG, opts.Format) && image.HasAlpha() {
		err = image.Flatten(&vips.Color{R: 255, G: 255, B: 255})
		if err != nil {
			return nil, "", err
		}
	}

	// Export to buffer
	var buf []byte
	var contentType string

	switch strings.ToLower(opts.Format) {
	case "png":
		params := vips.NewPngExportParams()
		params.Interlace = true
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
		params.Interlace = true
		params.OptimizeCoding = true
		buf, _, err = image.ExportJpeg(params)
		contentType = "image/jpeg"
	default:
		// Default to original format if not specified, or JPEG
		params := vips.NewJpegExportParams()
		params.Interlace = true
		params.OptimizeCoding = true
		buf, _, err = image.ExportJpeg(params)
		contentType = "image/jpeg"
	}

	if err != nil {
		return nil, "", fmt.Errorf("failed to export image: %w", err)
	}

	return buf, contentType, nil
}

// formatSupportsAlpha reports whether the given output format can carry an
// alpha channel. jpg/jpeg cannot, so transparency there always flattens.
func formatSupportsAlpha(format string) bool {
	switch strings.ToLower(format) {
	case "png", "webp", "avif":
		return true
	default:
		return false
	}
}

// effectiveKeepAlpha resolves the tri-state AlphaMode against the input type
// and output format into a concrete keep-vs-flatten decision:
//
//   - AlphaKeep    → keep when the format supports alpha (jpg still flattens).
//   - AlphaFlatten → always flatten.
//   - AlphaAuto    → keep only for SVG originals into alpha-capable formats;
//     raster originals flatten (historical behavior).
func effectiveKeepAlpha(mode types.AlphaMode, inputIsSVG bool, format string) bool {
	switch mode {
	case types.AlphaKeep:
		return formatSupportsAlpha(format)
	case types.AlphaFlatten:
		return false
	default: // AlphaAuto
		return inputIsSVG && formatSupportsAlpha(format)
	}
}
