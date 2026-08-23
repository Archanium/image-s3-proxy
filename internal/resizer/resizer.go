package resizer

import (
	"fmt"
	"math"
	"strings"

	"image-proxy/internal/procopts"
	"image-proxy/internal/types"

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

// Resize loads the image, applies one of two pipelines, and encodes the
// result.
//
// opts.Processing selects the pipeline. When it is nil — which is every
// pre-existing call site, i.e. the three legacy URL families and the worker
// pre-warm path — applyLegacy runs, byte-identically to the behaviour that
// shipped before imgproxy-style options existed. When it is non-nil the
// request came through the /_p/ route and applyProcessing runs instead.
//
// Both pipelines share the loader and the encoder.
func (r *LibvipsResizer) Resize(data []byte, opts types.ImageOptions) ([]byte, string, error) {
	image, err := loadImage(data, opts.IsAnimated)
	if err != nil {
		return nil, "", err
	}
	defer image.Close()

	if opts.Processing != nil {
		err = applyProcessing(image, *opts.Processing, opts.Format)
	} else {
		// Whether the original is a vector SVG governs the legacy pipeline's
		// default alpha policy. DetermineImageType sniffs the buffer, so it is
		// independent of the resize pipeline.
		inputIsSVG := vips.DetermineImageType(data) == vips.ImageTypeSVG
		err = applyLegacy(image, opts, inputIsSVG)
	}
	if err != nil {
		return nil, "", err
	}

	return export(image, opts.Format)
}

func loadImage(data []byte, isAnimated bool) (*vips.ImageRef, error) {
	var image *vips.ImageRef
	var err error

	if isAnimated {
		// For animated images (GIF, WebP, AVIF), we might need to load all pages
		importParams := vips.NewImportParams()
		importParams.NumPages.Set(-1)
		image, err = vips.LoadImageFromBuffer(data, importParams)
	} else {
		image, err = vips.LoadImageFromBuffer(data, nil)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to load image: %w", err)
	}
	return image, nil
}

// applyLegacy is the original Node.js-compatible pipeline, moved here
// verbatim from the body of Resize. Do not "improve" it — the three legacy
// URL families are a frozen contract, and the pre-existing test suite is the
// proof that this behaviour has not shifted.
//
// inputIsSVG is sniffed from the source buffer by the caller; it governs the
// AlphaAuto policy, under which SVG originals keep transparency and raster
// originals flatten.
func applyLegacy(image *vips.ImageRef, opts types.ImageOptions, inputIsSVG bool) error {
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
	err := image.ThumbnailWithSize(width, height, interesting, vips.SizeBoth)
	if err != nil {
		return err
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
				return fmt.Errorf("failed to embed image: %w", err)
			}
		}
	}

	// Handle alpha/background: flatten to a white background unless the
	// effective policy preserves transparency for this source + format.
	if !effectiveKeepAlpha(opts.AlphaMode, inputIsSVG, opts.Format) && image.HasAlpha() {
		err = image.Flatten(&vips.Color{R: 255, G: 255, B: 255})
		if err != nil {
			return err
		}
	}

	return nil
}

// applyProcessing runs the imgproxy-style pipeline in imgproxy's documented
// order: crop, then resize, then extend, then background flatten. Encoding
// happens in export, shared with the legacy pipeline.
func applyProcessing(image *vips.ImageRef, p procopts.Options, format string) error {
	if err := applyCropStage(image, p); err != nil {
		return err
	}
	if err := applyResizeStage(image, p); err != nil {
		return err
	}
	if err := applyExtendStage(image, p, format); err != nil {
		return err
	}
	return applyBackgroundStage(image, p, format)
}

// applyCropStage selects a source region before any scaling happens, which
// is the ordering imgproxy specifies.
func applyCropStage(image *vips.ImageRef, p procopts.Options) error {
	if p.Crop.IsDefault() {
		return nil
	}
	srcW, srcH := image.Width(), image.Height()
	w := resolveCropDimension(p.Crop.Width, srcW)
	h := resolveCropDimension(p.Crop.Height, srcH)
	if w >= srcW && h >= srcH {
		// The crop covers the whole image, so it is a no-op.
		return nil
	}
	w, h = minInt(w, srcW), minInt(h, srcH)

	if p.Crop.Gravity.Type == procopts.GravitySmart {
		return image.SmartCrop(w, h, vips.InterestingAttention)
	}
	left, top := anchorOffset(p.Crop.Gravity, srcW, srcH, w, h)
	return image.ExtractArea(left, top, w, h)
}

// resolveCropDimension implements imgproxy's crop sizing: 0 means the full
// source dimension, a value below 1 is a fraction of it, and anything else
// is absolute pixels.
func resolveCropDimension(v float64, src int) int {
	switch {
	case v == 0:
		return src
	case v < 1:
		return maxInt(1, int(math.Round(v*float64(src))))
	default:
		return maxInt(1, int(math.Round(v)))
	}
}

// applyResizeStage scales the image according to the resizing type. It is a
// no-op when neither dimension was requested, which is how a pure
// format-conversion request passes through untouched.
func applyResizeStage(image *vips.ImageRef, p procopts.Options) error {
	if p.Width == 0 && p.Height == 0 {
		return nil
	}
	srcW, srcH := image.Width(), image.Height()
	tw, th := targetDimensions(p.Width, p.Height, srcW, srcH)

	rt := p.EffectiveResizingType()
	if rt == procopts.ResizingTypeAuto {
		rt = resolveAuto(srcW, srcH, tw, th)
	}

	switch rt {
	case procopts.ResizingTypeForce:
		// force stretches to exactly the requested box, so it ignores
		// enlarge by definition.
		return image.ThumbnailWithSize(tw, th, vips.InterestingNone, vips.SizeForce)

	case procopts.ResizingTypeFill, procopts.ResizingTypeFillDown:
		// fill-down never enlarges regardless of the enlarge flag; that is
		// what distinguishes it from plain fill. fill with enlarge:0 lands
		// on the same behaviour, which matches imgproxy.
		down := rt == procopts.ResizingTypeFillDown || !p.Enlarge
		return fillTo(image, tw, th, p.Gravity, down)

	default: // fit
		return image.ThumbnailWithSize(tw, th, vips.InterestingNone, sizeStrategy(!p.Enlarge))
	}
}

// targetDimensions fills in whichever of width/height was left at 0 by
// deriving it from the source aspect ratio. This is where aspect-ratio
// preservation comes from — there is no separate option for it.
func targetDimensions(reqW, reqH, srcW, srcH int) (int, int) {
	tw, th := reqW, reqH
	if srcW <= 0 || srcH <= 0 {
		return maxInt(1, tw), maxInt(1, th)
	}
	if tw == 0 {
		tw = int(math.Round(float64(srcW) * float64(th) / float64(srcH)))
	}
	if th == 0 {
		th = int(math.Round(float64(srcH) * float64(tw) / float64(srcW)))
	}
	return maxInt(1, tw), maxInt(1, th)
}

// resolveAuto picks fill when the source and the requested box share an
// orientation, and fit otherwise.
func resolveAuto(srcW, srcH, tw, th int) procopts.ResizingType {
	if (srcW >= srcH) == (tw >= th) {
		return procopts.ResizingTypeFill
	}
	return procopts.ResizingTypeFit
}

func sizeStrategy(down bool) vips.Size {
	if down {
		return vips.SizeDown
	}
	return vips.SizeBoth
}

// fillTo scales the image to cover the requested box, then crops the box out
// at the requested anchor.
//
// This is implemented explicitly rather than by handing the box straight to
// ThumbnailWithSize, because libvips' SizeDown short-circuits: when the
// source is already smaller than the requested box it returns the image
// untouched and skips the crop, so the result never reaches the requested
// aspect ratio. fill-down's entire purpose is to reach that ratio without
// enlarging, so the scale and the crop are separated here.
func fillTo(image *vips.ImageRef, tw, th int, g procopts.Gravity, down bool) error {
	srcW, srcH := image.Width(), image.Height()
	if srcW <= 0 || srcH <= 0 {
		return nil
	}
	scale := math.Max(float64(tw)/float64(srcW), float64(th)/float64(srcH))
	if down && scale > 1 {
		scale = 1
	}
	sw := maxInt(1, int(math.Round(float64(srcW)*scale)))
	sh := maxInt(1, int(math.Round(float64(srcH)*scale)))
	if err := image.ThumbnailWithSize(sw, sh, vips.InterestingNone, vips.SizeForce); err != nil {
		return err
	}

	cw, ch := fitBox(tw, th, image.Width(), image.Height())
	if cw == image.Width() && ch == image.Height() {
		return nil
	}
	if g.Type == procopts.GravitySmart {
		return image.SmartCrop(cw, ch, vips.InterestingAttention)
	}
	left, top := anchorOffset(g, image.Width(), image.Height(), cw, ch)
	return image.ExtractArea(left, top, cw, ch)
}

// fitBox shrinks a boxW x boxH rectangle, preserving its ratio, until it fits
// inside availW x availH. It is what lets a non-enlarging fill still land on
// the requested aspect ratio: the box keeps its shape at the largest scale
// the available pixels allow.
func fitBox(boxW, boxH, availW, availH int) (int, int) {
	if boxW <= availW && boxH <= availH {
		return boxW, boxH
	}
	s := math.Min(float64(availW)/float64(boxW), float64(availH)/float64(boxH))
	return maxInt(1, int(math.Round(float64(boxW)*s))), maxInt(1, int(math.Round(float64(boxH)*s)))
}

// applyExtendStage pads the image out after resizing. extend targets the
// requested box; extend_aspect_ratio targets the requested ratio. When both
// are set, extend runs first so the result lands on the requested box and
// the aspect-ratio pass becomes a no-op.
func applyExtendStage(image *vips.ImageRef, p procopts.Options, format string) error {
	col := padColor(p, format)

	if p.Extend.Enabled && p.Width > 0 && p.Height > 0 {
		if err := embedTo(image, p.Width, p.Height, p.Extend.Gravity, col); err != nil {
			return err
		}
	}

	if p.ExtendAspectRatio.Enabled && p.Width > 0 && p.Height > 0 {
		w, h := aspectRatioTarget(image.Width(), image.Height(), p.Width, p.Height)
		if err := embedTo(image, w, h, p.ExtendAspectRatio.Gravity, col); err != nil {
			return err
		}
	}
	return nil
}

// aspectRatioTarget grows the current dimensions to the requested aspect
// ratio. It only ever grows, so nothing is cropped by an extend.
func aspectRatioTarget(currW, currH, reqW, reqH int) (int, int) {
	if currW <= 0 || currH <= 0 || reqW <= 0 || reqH <= 0 {
		return currW, currH
	}
	target := float64(reqW) / float64(reqH)
	current := float64(currW) / float64(currH)
	if current < target {
		return maxInt(currW, int(math.Round(float64(currH)*target))), currH
	}
	return currW, maxInt(currH, int(math.Round(float64(currW)/target)))
}

// embedTo places the image on a larger canvas of exactly w x h, anchored by
// gravity and filled with col. It never shrinks the image.
func embedTo(image *vips.ImageRef, w, h int, g procopts.Gravity, col *vips.ColorRGBA) error {
	currW, currH := image.Width(), image.Height()
	w, h = maxInt(w, currW), maxInt(h, currH)
	if w == currW && h == currH {
		return nil
	}
	left, top := anchorOffset(g, w, h, currW, currH)

	if col.A < 255 && !image.HasAlpha() {
		// A transparent pad needs somewhere to put the transparency.
		if err := image.AddAlpha(); err != nil {
			return fmt.Errorf("failed to add alpha channel: %w", err)
		}
	}
	if err := image.EmbedBackgroundRGBA(left, top, w, h, col); err != nil {
		return fmt.Errorf("failed to embed image: %w", err)
	}
	return nil
}

// padColor decides what fills the area an extend adds. An explicit
// background always wins. Otherwise the pad is transparent when the output
// format can carry alpha, and white when it cannot.
func padColor(p procopts.Options, format string) *vips.ColorRGBA {
	if p.HasBackground {
		return &vips.ColorRGBA{R: p.Background.R, G: p.Background.G, B: p.Background.B, A: 255}
	}
	if formatSupportsAlpha(format) {
		return &vips.ColorRGBA{A: 0}
	}
	return &vips.ColorRGBA{R: 255, G: 255, B: 255, A: 255}
}

// applyBackgroundStage flattens the alpha channel onto a solid colour. It
// runs when a background was requested, or when the output format cannot
// carry alpha and would otherwise render the transparency as black.
func applyBackgroundStage(image *vips.ImageRef, p procopts.Options, format string) error {
	if !image.HasAlpha() {
		return nil
	}
	switch {
	case p.HasBackground:
		return image.Flatten(&vips.Color{R: p.Background.R, G: p.Background.G, B: p.Background.B})
	case !formatSupportsAlpha(format):
		return image.Flatten(&vips.Color{R: 255, G: 255, B: 255})
	default:
		return nil
	}
}

// formatSupportsAlpha reports whether the given output format can carry an
// alpha channel. jpg/jpeg cannot, so transparency there always flattens.
// Shared by both pipelines: the legacy one via effectiveKeepAlpha, the /_p/
// one via padColor and applyBackgroundStage.
func formatSupportsAlpha(format string) bool {
	switch strings.ToLower(format) {
	case "png", "webp", "avif":
		return true
	default:
		return false
	}
}

// anchorOffset returns the top-left position of a boxW x boxH rectangle
// inside a canvasW x canvasH area, anchored per g.
//
// It serves both roles in this file: choosing which part of a larger source
// to keep (crop), and choosing where a smaller image sits on a larger canvas
// (extend). The arithmetic is identical.
//
// Gravity offsets move the box inward from the edge the gravity names. For a
// centred gravity they are a plain shift right/down. A focus point instead
// treats X and Y as fractional coordinates of the point that should end up
// at the centre of the box.
func anchorOffset(g procopts.Gravity, canvasW, canvasH, boxW, boxH int) (int, int) {
	freeW := canvasW - boxW
	freeH := canvasH - boxH

	if g.Type == procopts.GravityFocusPoint {
		left := int(math.Round(g.X*float64(canvasW))) - boxW/2
		top := int(math.Round(g.Y*float64(canvasH))) - boxH/2
		return clamp(left, 0, freeW), clamp(top, 0, freeH)
	}

	var left, top int
	switch g.Type {
	case procopts.GravityNorth:
		left, top = freeW/2, 0
	case procopts.GravitySouth:
		left, top = freeW/2, freeH
	case procopts.GravityEast:
		left, top = freeW, freeH/2
	case procopts.GravityWest:
		left, top = 0, freeH/2
	case procopts.GravityNorthEast:
		left, top = freeW, 0
	case procopts.GravityNorthWest:
		left, top = 0, 0
	case procopts.GravitySouthEast:
		left, top = freeW, freeH
	case procopts.GravitySouthWest:
		left, top = 0, freeH
	default: // centre, smart, or unset
		left, top = freeW/2, freeH/2
	}

	left += offsetToward(g.X, g.Type, procopts.GravityEast, procopts.GravityNorthEast, procopts.GravitySouthEast)
	top += offsetToward(g.Y, g.Type, procopts.GravitySouth, procopts.GravitySouthEast, procopts.GravitySouthWest)

	return clamp(left, 0, freeW), clamp(top, 0, freeH)
}

// offsetToward converts a gravity offset into a signed shift. An offset
// moves the box away from the edge its gravity names, so anchors on the far
// edge shift in the negative direction.
func offsetToward(v float64, t procopts.GravityType, farEdges ...procopts.GravityType) int {
	shift := int(math.Round(v))
	for _, e := range farEdges {
		if t == e {
			return -shift
		}
	}
	return shift
}

func export(image *vips.ImageRef, format string) ([]byte, string, error) {
	var buf []byte
	var contentType string
	var err error

	switch strings.ToLower(format) {
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

// effectiveKeepAlpha resolves the tri-state AlphaMode against the input type
// and output format into a concrete keep-vs-flatten decision:
//
//   - AlphaKeep    → keep when the format supports alpha (jpg still flattens).
//   - AlphaFlatten → always flatten.
//   - AlphaAuto    → keep only for SVG originals into alpha-capable formats;
//     raster originals flatten (historical behavior).
//
// This governs the legacy pipeline only. The /_p/ route follows imgproxy's
// rule instead — see applyBackgroundStage.
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

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
