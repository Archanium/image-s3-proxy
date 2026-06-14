package types

import "context"

type ImageOptions struct {
	Width      int
	Height     int
	Version    int
	Format     string // png, jpg, avif, etc.
	Fit        string // contain, inside, etc.
	KeepAlpha  bool
	IsAnimated bool
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
