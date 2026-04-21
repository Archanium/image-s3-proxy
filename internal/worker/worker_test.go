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
	putFunc    func(ctx context.Context, key string, data []byte, contentType string, tags map[string]string) error
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
func (m *mockS3Client) Put(ctx context.Context, key string, data []byte, contentType string, tags map[string]string) error {
	return m.putFunc(ctx, key, data, contentType, tags)
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
		putFunc: func(ctx context.Context, key string, data []byte, contentType string, tags map[string]string) error {
			putCount++
			return nil
		},
	}
	resizer := &mockResizer{}
	w := NewWorker(s3, nil, resizer, nil, nil, "", false)

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
		putFunc: func(ctx context.Context, key string, data []byte, contentType string, tags map[string]string) error {
			putCount++
			return nil
		},
	}
	resizer := &mockResizer{}
	customSizes := [][]int{{100, 100}, {200, 200}}
	w := NewWorker(s3, nil, resizer, nil, customSizes, "webp", false)

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

func TestProcessProductImage_DestClient(t *testing.T) {
	s3 := &mockS3Client{
		existsFunc: func(ctx context.Context, key string) (bool, error) { return false, nil },
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return []byte("original"), "image/jpeg", nil
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string, tags map[string]string) error {
			t.Errorf("Put should not be called on s3 client")
			return nil
		},
	}
	destS3 := &mockS3Client{
		putFunc: func(ctx context.Context, key string, data []byte, contentType string, tags map[string]string) error {
			return nil
		},
	}
	resizer := &mockResizer{}
	w := NewWorker(s3, destS3, resizer, nil, nil, "", false)

	ctx := context.Background()
	err := w.ProcessProductImage(ctx, "catalog/products/images/test.jpg")
	if err != nil {
		t.Errorf("ProcessProductImage failed: %v", err)
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
		putFunc: func(ctx context.Context, key string, data []byte, contentType string, tags map[string]string) error {
			t.Errorf("Put should not be called when file exists and forceOverwrite is false")
			return nil
		},
	}
	resizer := &mockResizer{}
	w := NewWorker(s3, nil, resizer, nil, nil, "", false) // forceOverwrite = false

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
		putFunc: func(ctx context.Context, key string, data []byte, contentType string, tags map[string]string) error {
			putCount++
			return nil
		},
	}
	resizer := &mockResizer{}
	w := NewWorker(s3, nil, resizer, nil, nil, "", true) // forceOverwrite = true

	ctx := context.Background()
	err := w.ProcessProductImage(ctx, "catalog/products/images/test.jpg")
	if err != nil {
		t.Errorf("ProcessProductImage failed: %v", err)
	}

	if putCount != 33 {
		t.Errorf("Expected 33 puts even though files exist (forceOverwrite=true), got %d", putCount)
	}
}
