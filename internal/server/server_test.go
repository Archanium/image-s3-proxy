package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image-proxy/internal/types"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
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
	return m.existsFunc(ctx, key)
}
func (m *mockS3Client) Get(ctx context.Context, key string) ([]byte, string, error) {
	return m.getFunc(ctx, key)
}
func (m *mockS3Client) Put(ctx context.Context, key string, data []byte, contentType string, tags map[string]string) error {
	return m.putFunc(ctx, key, data, contentType, tags)
}

type mockResizer struct {
	resizeFunc func(data []byte, opts types.ImageOptions) ([]byte, string, error)
}

func (m *mockResizer) Resize(data []byte, opts types.ImageOptions) ([]byte, string, error) {
	return m.resizeFunc(data, opts)
}

func TestServeHTTP_ExistingFile(t *testing.T) {
	s3 := &mockS3Client{
		existsFunc: func(ctx context.Context, key string) (bool, error) { return true, nil },
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return []byte("test-data"), "image/jpeg", nil
		},
	}
	resizer := &mockResizer{}
	srv := NewServer(s3, resizer, nil, nil, "")

	req := httptest.NewRequest("GET", "/test-image.jpg", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("Expected content type image/jpeg, got %s", w.Header().Get("Content-Type"))
	}
}

func TestServeHTTP_Resize(t *testing.T) {
	s3 := &mockS3Client{
		existsFunc: func(ctx context.Context, key string) (bool, error) { return false, nil },
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			if key == "123/catalog/products/images/test-image.jpg" {
				return []byte("original-data"), "image/jpeg", nil
			}
			return nil, "", context.DeadlineExceeded // Should not happen in this test
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string, tags map[string]string) error {
			return nil
		},
	}
	resizer := &mockResizer{
		resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
			return []byte("resized-data"), "image/webp", nil
		},
	}
	srv := NewServer(s3, resizer, nil, nil, "")

	req := httptest.NewRequest("GET", "/123/2/images/products/100/100/test-image.jpg.webp", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "image/webp" {
		t.Errorf("Expected content type image/webp, got %s", w.Header().Get("Content-Type"))
	}
}

func TestWorkerTrigger(t *testing.T) {
	s3 := &mockS3Client{
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return []byte("original-data"), "image/jpeg", nil
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string, tags map[string]string) error {
			return nil
		},
	}
	resizer := &mockResizer{
		resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
			return []byte("resized-data"), "image/webp", nil
		},
	}
	srv := NewServer(s3, resizer, nil, nil, "")

	payload := map[string]string{"key": "catalog/products/images/test.jpg"}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/_/worker/trigger", bytes.NewReader(body))
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("Expected status Accepted, got %d", w.Code)
	}
}

func TestRegexMatching(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		regex    string // resize, file, folderImage
		expected map[string]string
	}{
		{
			name:  "Resize regex match",
			path:  "123-group/2/images/products/150/210/test-image.jpg.webp",
			regex: "resize",
			expected: map[string]string{
				"clientId": "123",
				"group":    "group",
				"version":  "2",
				"folder":   "products",
				"width":    "150",
				"height":   "210",
				"path":     "test-image.jpg.webp",
			},
		},
		{
			name:  "File regex match",
			path:  "123/files/456/document.pdf",
			regex: "file",
			expected: map[string]string{
				"clientId": "123",
				"fileId":   "456",
				"path":     "document.pdf",
			},
		},
		{
			name:  "Folder image regex match",
			path:  "123/images/custom/another-image.png",
			regex: "folderImage",
			expected: map[string]string{
				"clientId": "123",
				"folder":   "custom",
				"path":     "another-image.png",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var groups map[string]string
			switch tt.regex {
			case "resize":
				groups = getNamedGroups(resizeRegex, tt.path)
			case "file":
				groups = getNamedGroups(fileRegex, tt.path)
			case "folderImage":
				groups = getNamedGroups(folderImageRegex, tt.path)
			}

			if groups == nil {
				t.Errorf("Expected match for path %s, but got nil", tt.path)
				return
			}

			for k, v := range tt.expected {
				if groups[k] != v {
					t.Errorf("Expected group %s to be %s, but got %s", k, v, groups[k])
				}
			}
		})
	}
}

func TestSpecificURLMapping(t *testing.T) {
	requestedKey := "9/3/images/blocks/2000/0/prespring-forside-4196157.png.webp"
	expectedOriginalKey1 := "9/catalog/blocks/images/prespring-forside-4196157.png"
	expectedOriginalKey2 := "9/images/blocks/prespring-forside-4196157.png"

	var capturedKeys []string
	s3 := &mockS3Client{
		existsFunc: func(ctx context.Context, key string) (bool, error) { return false, nil },
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			capturedKeys = append(capturedKeys, key)
			if key == expectedOriginalKey2 {
				return []byte("original-data"), "image/png", nil
			}
			return nil, "", errors.New("NotFound")
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string, tags map[string]string) error {
			return nil
		},
	}
	resizer := &mockResizer{
		resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
			if opts.Width != 2000 || opts.Height != 0 || opts.Version != 3 || opts.Format != "webp" {
				t.Errorf("Unexpected resize options: %+v", opts)
			}
			return []byte("resized-data"), "image/webp", nil
		},
	}
	srv := NewServer(s3, resizer, nil, nil, "")

	req := httptest.NewRequest("GET", "/"+requestedKey, nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %d", w.Code)
	}

	found1 := false
	found2 := false
	for _, k := range capturedKeys {
		if k == expectedOriginalKey1 {
			found1 = true
		}
		if k == expectedOriginalKey2 {
			found2 = true
		}
	}

	if !found1 {
		t.Errorf("Expected to try original key %s", expectedOriginalKey1)
	}
	if !found2 {
		t.Errorf("Expected to try alternative key %s", expectedOriginalKey2)
	}
}

func TestSpecificURLMapping_NoMiddleExtension(t *testing.T) {
	requestedKey := "9/3/images/blocks/2000/0/prespring-forside-4196157.webp"
	expectedOriginalKey := "9/images/blocks/prespring-forside-4196157" // WITHOUT .webp

	var capturedKeys []string
	s3 := &mockS3Client{
		existsFunc: func(ctx context.Context, key string) (bool, error) { return false, nil },
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			capturedKeys = append(capturedKeys, key)
			if key == expectedOriginalKey {
				return []byte("original-data"), "image/png", nil
			}
			return nil, "", errors.New("NotFound")
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string, tags map[string]string) error {
			return nil
		},
	}
	resizer := &mockResizer{
		resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
			return []byte("resized-data"), "image/webp", nil
		},
	}
	srv := NewServer(s3, resizer, nil, nil, "")

	req := httptest.NewRequest("GET", "/"+requestedKey, nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %d. Tried keys: %v", w.Code, capturedKeys)
	}
}

func TestServeHTTP_Resize_Zero(t *testing.T) {
	s3 := &mockS3Client{
		existsFunc: func(ctx context.Context, key string) (bool, error) { return false, nil },
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return []byte("original-data"), "image/jpeg", nil
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string, tags map[string]string) error {
			return nil
		},
	}
	resizer := &mockResizer{
		resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
			if opts.Width == 2560 && opts.Height == 0 {
				return []byte("resized-data"), "image/webp", nil
			}
			return nil, "", fmt.Errorf("unexpected dimensions: %dx%d", opts.Width, opts.Height)
		},
	}
	srv := NewServer(s3, resizer, nil, nil, "")

	req := httptest.NewRequest("GET", "/123/2/images/products/0/0/test-image.jpg.webp", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %d. Body: %s", w.Code, w.Body.String())
	}
}

func TestServeHTTP_FolderImage_Default(t *testing.T) {
	s3 := &mockS3Client{
		existsFunc: func(ctx context.Context, key string) (bool, error) { return false, nil },
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return []byte("original-data"), "image/jpeg", nil
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string, tags map[string]string) error {
			return nil
		},
	}
	resizer := &mockResizer{
		resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
			if opts.Width == 2560 && opts.Height == 0 {
				return []byte("resized-data"), "image/webp", nil
			}
			return nil, "", fmt.Errorf("unexpected dimensions: %dx%d", opts.Width, opts.Height)
		},
	}
	srv := NewServer(s3, resizer, nil, nil, "")

	req := httptest.NewRequest("GET", "/123/images/custom/test-image.jpg.webp", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %d. Body: %s", w.Code, w.Body.String())
	}
}

func TestServeHTTP_NoExtension(t *testing.T) {
	s3 := &mockS3Client{
		existsFunc: func(ctx context.Context, key string) (bool, error) { return false, nil },
	}
	resizer := &mockResizer{}
	srv := NewServer(s3, resizer, nil, nil, "")

	// This path matches resizeRegex but has no extension in the last segment
	req := httptest.NewRequest("GET", "/123/2/images/products/100/100/test-image", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status NotFound for path without extension, got %d", w.Code)
	}
}

func TestServeHTTP_NoExtension_SimpleImage(t *testing.T) {
	s3 := &mockS3Client{
		existsFunc: func(ctx context.Context, key string) (bool, error) { return false, nil },
	}
	resizer := &mockResizer{}
	srv := NewServer(s3, resizer, nil, nil, "")

	// This path matches folderImageRegex but has no extension in the last segment
	req := httptest.NewRequest("GET", "/123/images/custom/another-image", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status NotFound for path without extension, got %d", w.Code)
	}
}

func TestServeHTTP_NoExtension_DirectS3(t *testing.T) {
	s3 := &mockS3Client{
		existsFunc: func(ctx context.Context, key string) (bool, error) { return true, nil },
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return []byte("test-data"), "application/octet-stream", nil
		},
	}
	resizer := &mockResizer{}
	srv := NewServer(s3, resizer, nil, nil, "")

	// Direct request to an existing S3 object without extension
	req := httptest.NewRequest("GET", "/some/path/without/extension", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status NotFound for path without extension (even if it exists in S3), got %d", w.Code)
	}
}
