package worker

import (
	"context"
	"image-proxy/internal/types"
	"io"
	"log"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("DEBUG") != "true" {
		log.SetOutput(io.Discard)
	}
	os.Exit(m.Run())
}

type mockS3Client struct {
	existsFunc func(ctx context.Context, key string) (bool, error)
	getFunc    func(ctx context.Context, key string) ([]byte, string, error)
	putFunc    func(ctx context.Context, key string, data []byte, contentType string) error
}

func (m *mockS3Client) Exists(ctx context.Context, key string) (bool, error) {
	if m.existsFunc != nil {
		return m.existsFunc(ctx, key)
	}
	return false, nil
}
func (m *mockS3Client) Get(ctx context.Context, key string) ([]byte, string, error) {
	return m.getFunc(ctx, key)
}
func (m *mockS3Client) Put(ctx context.Context, key string, data []byte, contentType string) error {
	return m.putFunc(ctx, key, data, contentType)
}

type mockResizer struct{}

func (m *mockResizer) Resize(data []byte, opts types.ImageOptions) ([]byte, string, error) {
	return []byte("resized"), "image/avif", nil
}

func TestProcessProductImage(t *testing.T) {
	putCount := 0
	s3 := &mockS3Client{
		existsFunc: func(ctx context.Context, key string) (bool, error) {
			return false, nil
		},
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return []byte("original"), "image/jpeg", nil
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string) error {
			putCount++
			return nil
		},
	}
	resizer := &mockResizer{}
	w := NewWorker(s3, nil, resizer, nil, "", false)

	ctx := context.Background()
	err := w.ProcessProductImage(ctx, "catalog/products/images/test.jpg")
	if err != nil {
		t.Errorf("ProcessProductImage failed: %v", err)
	}

	// Default sizes count is 33
	if putCount != 33 {
		t.Errorf("Expected 33 puts, got %d", putCount)
	}
}

func TestProcessProductImage_CustomSizes(t *testing.T) {
	putCount := 0
	s3 := &mockS3Client{
		existsFunc: func(ctx context.Context, key string) (bool, error) {
			return false, nil
		},
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return []byte("original"), "image/jpeg", nil
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string) error {
			putCount++
			return nil
		},
	}
	resizer := &mockResizer{}
	customSizes := [][]int{{100, 100}, {200, 200}}
	w := NewWorker(s3, nil, resizer, customSizes, "webp", false)

	ctx := context.Background()
	err := w.ProcessProductImage(ctx, "catalog/products/images/test.jpg")
	if err != nil {
		t.Errorf("ProcessProductImage failed: %v", err)
	}

	if putCount != 2 {
		t.Errorf("Expected 2 puts, got %d", putCount)
	}

	if w.format != "webp" {
		t.Errorf("Expected format webp, got %s", w.format)
	}
}

func TestProcessProductImage_DualWritesWhenClientsDiffer(t *testing.T) {
	var originPuts, destPuts int
	s3 := &mockS3Client{
		existsFunc: func(ctx context.Context, key string) (bool, error) { return false, nil },
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return []byte("original"), "image/jpeg", nil
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string) error {
			originPuts++
			return nil
		},
	}
	destS3 := &mockS3Client{
		existsFunc: func(ctx context.Context, key string) (bool, error) { return false, nil },
		putFunc: func(ctx context.Context, key string, data []byte, contentType string) error {
			destPuts++
			return nil
		},
	}
	resizer := &mockResizer{}
	w := NewWorker(s3, destS3, resizer, nil, "", false)

	ctx := context.Background()
	if err := w.ProcessProductImage(ctx, "catalog/products/images/test.jpg"); err != nil {
		t.Errorf("ProcessProductImage failed: %v", err)
	}

	// 33 default sizes × 2 destinations = 33 puts on each side.
	if originPuts != 33 {
		t.Errorf("origin puts = %d, want 33", originPuts)
	}
	if destPuts != 33 {
		t.Errorf("dest puts = %d, want 33", destPuts)
	}
}

func TestProcessProductImage_ExistsCheckTargetsDestWhenSplit(t *testing.T) {
	// The exists-check should hit destS3Client (the cache we're
	// populating). If destS3.exists returns true, we skip; the origin
	// Put must also be skipped.
	var originPuts, destPuts int
	s3 := &mockS3Client{
		existsFunc: func(ctx context.Context, key string) (bool, error) {
			t.Errorf("exists must not be called on origin in split mode; was called on %s", key)
			return false, nil
		},
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return []byte("original"), "image/jpeg", nil
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string) error {
			originPuts++
			return nil
		},
	}
	destS3 := &mockS3Client{
		existsFunc: func(ctx context.Context, key string) (bool, error) {
			return true, nil // already cached → skip
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string) error {
			destPuts++
			return nil
		},
	}
	resizer := &mockResizer{}
	w := NewWorker(s3, destS3, resizer, nil, "", false)

	ctx := context.Background()
	if err := w.ProcessProductImage(ctx, "catalog/products/images/test.jpg"); err != nil {
		t.Errorf("ProcessProductImage failed: %v", err)
	}
	if originPuts != 0 || destPuts != 0 {
		t.Errorf("expected zero puts when destS3 reports existing; got origin=%d dest=%d", originPuts, destPuts)
	}
}

func TestProcessProductImage_ForceOverwrite_Skip(t *testing.T) {
	s3 := &mockS3Client{
		existsFunc: func(ctx context.Context, key string) (bool, error) {
			return true, nil // Already exists
		},
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return []byte("original"), "image/jpeg", nil
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string) error {
			t.Errorf("Put should not be called when file exists and forceOverwrite is false")
			return nil
		},
	}
	resizer := &mockResizer{}
	w := NewWorker(s3, nil, resizer, nil, "", false) // forceOverwrite = false

	ctx := context.Background()
	err := w.ProcessProductImage(ctx, "catalog/products/images/test.jpg")
	if err != nil {
		t.Errorf("ProcessProductImage failed: %v", err)
	}
}

func TestProcessProductImage_ForceOverwrite_True(t *testing.T) {
	putCount := 0
	s3 := &mockS3Client{
		existsFunc: func(ctx context.Context, key string) (bool, error) {
			return true, nil // Already exists
		},
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return []byte("original"), "image/jpeg", nil
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string) error {
			putCount++
			return nil
		},
	}
	resizer := &mockResizer{}
	w := NewWorker(s3, nil, resizer, nil, "", true) // forceOverwrite = true

	ctx := context.Background()
	err := w.ProcessProductImage(ctx, "catalog/products/images/test.jpg")
	if err != nil {
		t.Errorf("ProcessProductImage failed: %v", err)
	}

	if putCount != 33 {
		t.Errorf("Expected 33 puts even though files exist (forceOverwrite=true), got %d", putCount)
	}
}
