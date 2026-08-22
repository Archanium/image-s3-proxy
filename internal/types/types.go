package types

import (
	"context"

	"image-proxy/internal/procopts"
)

// AlphaMode controls how source transparency is handled when producing the
// output image. It is set by the request layer (from an optional URL segment)
// and interpreted by the resizer.
type AlphaMode int

const (
	// AlphaAuto is the default (zero value): keep transparency for SVG
	// originals when the output format supports alpha (png/webp/avif), and
	// flatten raster originals to white — preserving historical behavior for
	// existing raster sources.
	AlphaAuto AlphaMode = iota
	// AlphaKeep forces transparency to be preserved when the output format
	// supports it. jpg/jpeg have no alpha channel, so they still flatten.
	AlphaKeep
	// AlphaFlatten forces flattening to a white background regardless of the
	// source or output format.
	AlphaFlatten
)

// ImageOptions describes one resize request.
//
// Two pipelines share this struct. The legacy fields (Width, Height, Version,
// Fit, AlphaMode) drive the original Node.js-compatible pipeline used by the
// three legacy URL families. Processing, when non-nil, selects the
// imgproxy-style pipeline instead and supersedes Width/Height/Fit — those
// values come from the option set rather than from the URL regex.
//
// Processing is a pointer rather than a set of inlined fields so that the
// option vocabulary has exactly one definition (in procopts) and so that
// "which pipeline runs" is a single nil check rather than a convention about
// which field happens to be non-zero.
//
// AlphaMode applies to the legacy pipeline only. The /_p/ route follows
// imgproxy's rule instead: transparency survives into any alpha-capable
// format, and the `bg:` option is what flattens it.
type ImageOptions struct {
	Width      int
	Height     int
	Version    int
	Format     string    // png, jpg, avif, etc.
	Fit        string    // contain, inside, etc.
	AlphaMode  AlphaMode // how to handle source transparency (default AlphaAuto)
	IsAnimated bool

	// Processing selects the imgproxy-style pipeline. Nil means the legacy
	// pipeline, which is what every pre-existing call site produces.
	Processing *procopts.Options
}

type Resizer interface {
	Resize(data []byte, opts ImageOptions) ([]byte, string, error)
}

// S3Client is the minimal storage surface used by the proxy.
//
// Put intentionally does NOT carry a tags parameter — neither Hetzner Object
// Storage nor Cloudflare R2 implement the S3 Tagging APIs. The IMAGE_TAGS
// env var is preserved at the main.go layer for backwards-compat but is
// logged-and-ignored at startup.
type S3Client interface {
	Exists(ctx context.Context, key string) (bool, error)
	Get(ctx context.Context, key string) ([]byte, string, error) // data, contentType, err
	Put(ctx context.Context, key string, data []byte, contentType string) error
}

type Storage interface {
	Exists(key string) (bool, error)
	Get(key string) ([]byte, string, error)
	Put(key string, data []byte, contentType string) error
}
