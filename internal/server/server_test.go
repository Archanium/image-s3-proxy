package server

import (
	"bytes"
	"context"
	"encoding/json"
	"image-proxy/internal/types"
	"net/http"
	"net/http/httptest"
	"testing"
)

type mockS3Client struct {
	existsFunc func(ctx interface{}, key string) (bool, error)
	getFunc    func(ctx interface{}, key string) ([]byte, string, error)
	putFunc    func(ctx interface{}, key string, data []byte, contentType string, tags map[string]string) error
}

func (m *mockS3Client) Exists(ctx interface{}, key string) (bool, error) {
	return m.existsFunc(ctx, key)
}
func (m *mockS3Client) Get(ctx interface{}, key string) ([]byte, string, error) {
	return m.getFunc(ctx, key)
}
func (m *mockS3Client) Put(ctx interface{}, key string, data []byte, contentType string, tags map[string]string) error {
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
		existsFunc: func(ctx interface{}, key string) (bool, error) { return true, nil },
		getFunc: func(ctx interface{}, key string) ([]byte, string, error) {
			return []byte("test-data"), "image/jpeg", nil
		},
	}
	resizer := &mockResizer{}
	srv := NewServer(s3, resizer, nil)

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
		existsFunc: func(ctx interface{}, key string) (bool, error) { return false, nil },
		getFunc: func(ctx interface{}, key string) ([]byte, string, error) {
			if key == "catalog/products/images/test-image.jpg" {
				return []byte("original-data"), "image/jpeg", nil
			}
			return nil, "", context.DeadlineExceeded // Should not happen in this test
		},
		putFunc: func(ctx interface{}, key string, data []byte, contentType string, tags map[string]string) error {
			return nil
		},
	}
	resizer := &mockResizer{
		resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
			return []byte("resized-data"), "image/webp", nil
		},
	}
	srv := NewServer(s3, resizer, nil)

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
		getFunc: func(ctx interface{}, key string) ([]byte, string, error) {
			return []byte("original-data"), "image/jpeg", nil
		},
		putFunc: func(ctx interface{}, key string, data []byte, contentType string, tags map[string]string) error {
			return nil
		},
	}
	resizer := &mockResizer{
		resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
			return []byte("resized-data"), "image/webp", nil
		},
	}
	srv := NewServer(s3, resizer, nil)

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
				"shopId":  "123",
				"group":   "group",
				"version": "2",
				"folder":  "products",
				"width":   "150",
				"height":  "210",
				"path":    "test-image.jpg.webp",
			},
		},
		{
			name:  "File regex match",
			path:  "123/files/456/document.pdf",
			regex: "file",
			expected: map[string]string{
				"shopId": "123",
				"fileId": "456",
				"path":   "document.pdf",
			},
		},
		{
			name:  "Folder image regex match",
			path:  "123/images/custom/another-image.png",
			regex: "folderImage",
			expected: map[string]string{
				"shopId": "123",
				"folder": "custom",
				"path":   "another-image.png",
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
