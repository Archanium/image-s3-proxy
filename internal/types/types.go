package types

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

type S3Client interface {
	Exists(ctx interface{}, key string) (bool, error)
	Get(ctx interface{}, key string) ([]byte, string, error) // data, contentType, err
	Put(ctx interface{}, key string, data []byte, contentType string, tags map[string]string) error
}

type Storage interface {
	Exists(key string) (bool, error)
	Get(key string) ([]byte, string, error)
	Put(key string, data []byte, contentType string, tags map[string]string) error
}
