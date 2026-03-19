package worker

import (
	"context"
	"image-proxy/internal/types"
	"testing"
)

type mockS3Client struct {
	getFunc func(ctx context.Context, key string) ([]byte, string, error)
	putFunc func(ctx context.Context, key string, data []byte, contentType string, tags map[string]string) error
}

func (m *mockS3Client) Exists(ctx context.Context, key string) (bool, error) { return true, nil }
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
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return []byte("original"), "image/jpeg", nil
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string, tags map[string]string) error {
			putCount++
			return nil
		},
	}
	resizer := &mockResizer{}
	w := NewWorker(s3, resizer, nil, nil, "")

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
	w := NewWorker(s3, resizer, nil, customSizes, "webp")

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
