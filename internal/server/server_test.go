package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image-proxy/internal/accesslog"
	"image-proxy/internal/types"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
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
	if m.putFunc == nil {
		return nil
	}
	return m.putFunc(ctx, key, data, contentType)
}

type mockResizer struct {
	resizeFunc func(data []byte, opts types.ImageOptions) ([]byte, string, error)
}

func (m *mockResizer) Resize(data []byte, opts types.ImageOptions) ([]byte, string, error) {
	return m.resizeFunc(data, opts)
}

// notFoundGet is a getFunc that returns a typed NoSuchKey error — the
// canonical "clean miss" signal. Used to drive the cache-miss → resize
// path in off-mode tests.
func notFoundGet(ctx context.Context, key string) ([]byte, string, error) {
	return nil, "", &s3types.NoSuchKey{}
}

// --- existing-key path -------------------------------------------------

func TestServeHTTP_ExistingFile(t *testing.T) {
	s3 := &mockS3Client{
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return []byte("test-data"), "image/jpeg", nil
		},
	}
	srv := NewServer(s3, &mockResizer{}, nil, "")

	req := httptest.NewRequest("GET", "/test-image.jpg", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("Expected content type image/jpeg, got %s", w.Header().Get("Content-Type"))
	}
	if w.Header().Get("Cache-Control") != "max-age=31536000" {
		t.Errorf("Expected Cache-Control max-age=31536000, got %s", w.Header().Get("Cache-Control"))
	}
}

func TestServeHTTP_Resize(t *testing.T) {
	s3 := &mockS3Client{
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			if key == "123/catalog/products/images/test-image.jpg" {
				return []byte("original-data"), "image/jpeg", nil
			}
			return nil, "", &s3types.NoSuchKey{}
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string) error {
			return nil
		},
	}
	resizer := &mockResizer{
		resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
			if opts.Version != 2 || opts.Fit != "contain" {
				return nil, "", fmt.Errorf("unexpected opts: version=%d, fit=%s", opts.Version, opts.Fit)
			}
			return []byte("resized-data"), "image/webp", nil
		},
	}
	srv := NewServer(s3, resizer, nil, "")

	req := httptest.NewRequest("GET", "/123/2/images/products/100/100/test-image.jpg.webp", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "image/webp" {
		t.Errorf("Expected content type image/webp, got %s", w.Header().Get("Content-Type"))
	}
	if w.Header().Get("Cache-Control") != "max-age=31536000" {
		t.Errorf("Expected Cache-Control max-age=31536000, got %s", w.Header().Get("Cache-Control"))
	}
}

func TestWorkerTrigger(t *testing.T) {
	s3 := &mockS3Client{
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return []byte("original-data"), "image/jpeg", nil
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string) error {
			return nil
		},
	}
	resizer := &mockResizer{
		resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
			return []byte("resized-data"), "image/webp", nil
		},
	}
	srv := NewServer(s3, resizer, nil, "")

	payload := map[string]string{"key": "catalog/products/images/test.jpg"}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/_/worker/trigger", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("Expected status Accepted, got %d", w.Code)
	}
}

// --- regex + URL mapping ----------------------------------------------

func TestRegexMatching(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		regex    string
		expected map[string]string
	}{
		{
			name:  "Resize regex match",
			path:  "123-group/2/images/products/150/210/test-image.jpg.webp",
			regex: "resize",
			expected: map[string]string{
				"clientId": "123", "group": "group", "version": "2",
				"folder": "products", "width": "150", "height": "210",
				"path": "test-image.jpg.webp",
			},
		},
		{
			name:  "File regex match",
			path:  "123/files/456/document.pdf",
			regex: "file",
			expected: map[string]string{
				"clientId": "123", "fileId": "456", "path": "document.pdf",
			},
		},
		{
			name:  "Folder image regex match",
			path:  "123/images/custom/another-image.png",
			regex: "folderImage",
			expected: map[string]string{
				"clientId": "123", "folder": "custom", "path": "another-image.png",
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
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			capturedKeys = append(capturedKeys, key)
			if key == expectedOriginalKey2 {
				return []byte("original-data"), "image/png", nil
			}
			return nil, "", errors.New("NotFound")
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string) error { return nil },
	}
	resizer := &mockResizer{
		resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
			if opts.Width != 2000 || opts.Height != 0 || opts.Version != 3 || opts.Format != "webp" {
				t.Errorf("Unexpected resize options: %+v", opts)
			}
			return []byte("resized-data"), "image/webp", nil
		},
	}
	srv := NewServer(s3, resizer, nil, "")

	req := httptest.NewRequest("GET", "/"+requestedKey, nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %d", w.Code)
	}
	if !contains(capturedKeys, expectedOriginalKey1) {
		t.Errorf("Expected to try original key %s; tried %v", expectedOriginalKey1, capturedKeys)
	}
	if !contains(capturedKeys, expectedOriginalKey2) {
		t.Errorf("Expected to try alternative key %s; tried %v", expectedOriginalKey2, capturedKeys)
	}
}

func TestSpecificURLMapping_NoMiddleExtension(t *testing.T) {
	requestedKey := "9/3/images/blocks/2000/0/prespring-forside-4196157.webp"
	expectedOriginalKey := "9/images/blocks/prespring-forside-4196157.webp"

	s3 := &mockS3Client{
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			if key == expectedOriginalKey {
				return []byte("original-data"), "image/png", nil
			}
			return nil, "", errors.New("NotFound")
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string) error { return nil },
	}
	resizer := &mockResizer{
		resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
			if opts.Version != 3 || opts.Fit != "contain" {
				return nil, "", fmt.Errorf("unexpected opts: version=%d, fit=%s", opts.Version, opts.Fit)
			}
			return []byte("resized-data"), "image/webp", nil
		},
	}
	srv := NewServer(s3, resizer, nil, "")

	req := httptest.NewRequest("GET", "/"+requestedKey, nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %d", w.Code)
	}
}

func TestServeHTTP_Resize_Zero(t *testing.T) {
	s3 := &mockS3Client{
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return []byte("original-data"), "image/jpeg", nil
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string) error { return nil },
	}
	resizer := &mockResizer{
		resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
			if opts.Width == 5120 && opts.Height == 0 {
				return []byte("resized-data"), "image/webp", nil
			}
			return nil, "", fmt.Errorf("unexpected dimensions: %dx%d", opts.Width, opts.Height)
		},
	}
	srv := NewServer(s3, resizer, nil, "")

	req := httptest.NewRequest("GET", "/123/2/images/products/0/0/test-image.jpg.webp", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %d. Body: %s", w.Code, w.Body.String())
	}
}

func TestServeHTTP_FolderImage_Default(t *testing.T) {
	s3 := &mockS3Client{
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return []byte("original-data"), "image/jpeg", nil
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string) error { return nil },
	}
	resizer := &mockResizer{
		resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
			if opts.Width == 5120 && opts.Height == 0 {
				return []byte("resized-data"), "image/webp", nil
			}
			return nil, "", fmt.Errorf("unexpected dimensions: %dx%d", opts.Width, opts.Height)
		},
	}
	srv := NewServer(s3, resizer, nil, "")

	req := httptest.NewRequest("GET", "/123/images/custom/test-image.jpg.webp", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %d. Body: %s", w.Code, w.Body.String())
	}
}

func TestServeHTTP_NoExtension(t *testing.T) {
	s3 := &mockS3Client{}
	srv := NewServer(s3, &mockResizer{}, nil, "")

	req := httptest.NewRequest("GET", "/123/2/images/products/100/100/test-image", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status NotFound for path without extension, got %d", w.Code)
	}
}

func TestServeHTTP_NoExtension_SimpleImage(t *testing.T) {
	s3 := &mockS3Client{}
	srv := NewServer(s3, &mockResizer{}, nil, "")

	req := httptest.NewRequest("GET", "/123/images/custom/another-image", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status NotFound for path without extension, got %d", w.Code)
	}
}

func TestServeHTTP_NoExtension_DirectS3(t *testing.T) {
	// Even if a key without an extension exists in S3, the proxy rejects
	// before the Get because the filename has no '.'.
	s3 := &mockS3Client{
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return []byte("test-data"), "application/octet-stream", nil
		},
	}
	srv := NewServer(s3, &mockResizer{}, nil, "")

	req := httptest.NewRequest("GET", "/some/path/without/extension", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected 404 for path without extension; got %d", w.Code)
	}
}

func TestErrorCaching(t *testing.T) {
	s3 := &mockS3Client{
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return nil, "", &s3types.NoSuchKey{}
		},
	}
	srv := NewServer(s3, &mockResizer{}, nil, "")

	tests := []struct {
		name   string
		path   string
		status int
	}{
		{"NotFound - No extension", "/123/2/images/products/100/100/test-image", http.StatusNotFound},
		{"NotFound - No pattern match", "/invalid/path.jpg", http.StatusNotFound},
		{"NotFound - Original not found", "/123/2/images/products/100/100/notfound.jpg.webp", http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)

			if w.Code != tt.status {
				t.Errorf("Expected status %d, got %d", tt.status, w.Code)
			}
			if got := w.Header().Get("Cache-Control"); got != "max-age=30" {
				t.Errorf("Expected Cache-Control max-age=30, got %s", got)
			}
		})
	}
}

func TestExtensionHandling(t *testing.T) {
	tests := []struct {
		name                string
		requestedKey        string
		expectedOriginalKey string
		expectedFormat      string
	}{
		{
			name:                "Single extension - webp",
			requestedKey:        "/123/2/images/products/100/100/image.webp",
			expectedOriginalKey: "123/catalog/products/images/image.webp",
			expectedFormat:      "webp",
		},
		{
			name:                "Double extension - png.webp",
			requestedKey:        "/123/2/images/products/100/100/test.png.webp",
			expectedOriginalKey: "123/catalog/products/images/test.png",
			expectedFormat:      "webp",
		},
		{
			name:                "Single extension - jpg",
			requestedKey:        "/123/2/images/products/100/100/image.jpg",
			expectedOriginalKey: "123/catalog/products/images/image.jpg",
			expectedFormat:      "jpg",
		},
		{
			name:                "Triple extension - test.jpg.png.webp",
			requestedKey:        "/123/2/images/products/100/100/test.jpg.png.webp",
			expectedOriginalKey: "123/catalog/products/images/test.jpg.png",
			expectedFormat:      "webp",
		},
		{
			name:                "Double extension - jpg.avif",
			requestedKey:        "/123/2/images/products/100/100/image.jpg.avif",
			expectedOriginalKey: "123/catalog/products/images/image.jpg",
			expectedFormat:      "avif",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedOriginalKey string
			var capturedFormat string

			s3 := &mockS3Client{
				getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
					// First Get on the speculative cache check returns
					// NoSuchKey (clean miss). Subsequent candidate-key
					// attempts capture the first one that matches.
					if key == tt.requestedKey[1:] { // request URL minus leading "/"
						return nil, "", &s3types.NoSuchKey{}
					}
					if capturedOriginalKey == "" {
						capturedOriginalKey = key
					}
					if key == tt.expectedOriginalKey {
						return []byte("original-data"), "image/jpeg", nil
					}
					return nil, "", fmt.Errorf("NotFound: %s", key)
				},
				putFunc: func(ctx context.Context, key string, data []byte, contentType string) error { return nil },
			}

			resizer := &mockResizer{
				resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
					capturedFormat = opts.Format
					return []byte("resized-data"), "image/webp", nil
				},
			}

			srv := NewServer(s3, resizer, nil, "")
			req := httptest.NewRequest("GET", tt.requestedKey, nil)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("Expected status OK, got %d. OriginalKey tried: %s", w.Code, capturedOriginalKey)
			}
			if capturedOriginalKey != tt.expectedOriginalKey {
				t.Errorf("Expected original key %s, got %s", tt.expectedOriginalKey, capturedOriginalKey)
			}
			if capturedFormat != tt.expectedFormat {
				t.Errorf("Expected format %s, got %s", tt.expectedFormat, capturedFormat)
			}
		})
	}
}

func TestServeHTTP_BrandingResize_AttemptsResizeAndCachesWhenMissing(t *testing.T) {
	requestedKey := "39/3/images/branding/350/438/byflou-com-logo.jpeg"
	expectedOriginalKey := "39/catalog/branding/images/byflou-com-logo.jpeg"

	var getKeys []string
	var putKey string
	var putData []byte
	var putContentType string
	var resized bool

	s3 := &mockS3Client{
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			getKeys = append(getKeys, key)
			if key == expectedOriginalKey {
				return []byte("original-branding-logo"), "image/jpeg", nil
			}
			return nil, "", &s3types.NoSuchKey{}
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string) error {
			putKey = key
			putData = data
			putContentType = contentType
			return nil
		},
	}

	resizer := &mockResizer{
		resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
			resized = true
			if string(data) != "original-branding-logo" {
				t.Errorf("Expected original branding logo data, got %q", string(data))
			}
			if opts.Width != 350 || opts.Height != 438 || opts.Version != 3 || opts.Fit != "contain" || opts.Format != "jpeg" {
				t.Errorf("Unexpected resize opts: %+v", opts)
			}
			return []byte("resized-branding-logo"), "image/jpeg", nil
		},
	}

	srv := NewServer(s3, resizer, nil, "")

	req := httptest.NewRequest("GET", "/"+requestedKey, nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status OK, got %d. Body: %s", w.Code, w.Body.String())
	}
	// First Get hits the speculative cache check (the requested key
	// itself). After NoSuchKey, the candidate-key loop begins.
	if len(getKeys) == 0 || getKeys[0] != requestedKey {
		t.Errorf("Expected first Get on the requested key %s; got %v", requestedKey, getKeys)
	}
	if !resized {
		t.Error("Expected resize to be attempted")
	}
	if putKey != requestedKey {
		t.Errorf("Expected resized image to be cached at %s, got %s", requestedKey, putKey)
	}
	if string(putData) != "resized-branding-logo" {
		t.Errorf("Expected cached resized data, got %q", string(putData))
	}
	if putContentType != "image/jpeg" {
		t.Errorf("Expected cached content type image/jpeg, got %s", putContentType)
	}
	if w.Body.String() != "resized-branding-logo" {
		t.Errorf("Expected resized response body, got %q", w.Body.String())
	}
}

func TestServeHTTP_BrandingResize_ServesCachedObjectWhenExisting(t *testing.T) {
	requestedKey := "39/3/images/branding/350/438/byflou-com-logo.jpeg"

	s3 := &mockS3Client{
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			if key != requestedKey {
				t.Errorf("Expected Get key %s, got %s", requestedKey, key)
			}
			return []byte("cached-branding-logo"), "image/jpeg", nil
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string) error {
			t.Errorf("Put should not be called when cached object exists")
			return nil
		},
	}

	resizer := &mockResizer{
		resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
			t.Errorf("Resize should not be called when cached object exists")
			return nil, "", nil
		},
	}

	srv := NewServer(s3, resizer, nil, "")

	req := httptest.NewRequest("GET", "/"+requestedKey, nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status OK, got %d. Body: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "cached-branding-logo" {
		t.Errorf("Expected cached response body, got %q", w.Body.String())
	}
}

// --- Server-Timing integration tests ----------------------------------

func newMiddlewareWrapped(srv *Server) http.Handler {
	return accesslog.Middleware(srv, accesslog.NewLogger(io.Discard), "test-bucket")
}

func parseServerTimingPhases(t *testing.T, header string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	if header == "" {
		return out
	}
	for _, part := range strings.Split(header, ", ") {
		name, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		if name != "" {
			out[name] = true
		}
	}
	return out
}

func TestServeHTTP_ServerTiming_OffMode_CachedHit_SingleGet(t *testing.T) {
	s3 := &mockS3Client{
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return []byte("cached"), "image/jpeg", nil
		},
	}
	handler := newMiddlewareWrapped(NewServer(s3, &mockResizer{}, nil, ""))

	req := httptest.NewRequest("GET", "/cached-image.jpg", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	got := parseServerTimingPhases(t, w.Header().Get("Server-Timing"))
	if !got["s3-get"] {
		t.Errorf("Server-Timing missing s3-get; got %v", w.Header().Get("Server-Timing"))
	}
	for _, unwanted := range []string{"s3-exists", "resize", "s3-put", "s3-put-cache", "s3-put-origin"} {
		if got[unwanted] {
			t.Errorf("Server-Timing should not contain %q on off-mode cache hit; got %v", unwanted, w.Header().Get("Server-Timing"))
		}
	}
}

func TestServeHTTP_ServerTiming_OffMode_ResizePath_BareS3Put(t *testing.T) {
	s3 := &mockS3Client{
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			if key == "123/catalog/products/images/test-image.jpg" {
				return []byte("original"), "image/jpeg", nil
			}
			return nil, "", &s3types.NoSuchKey{}
		},
		putFunc: func(ctx context.Context, key string, data []byte, contentType string) error { return nil },
	}
	resizer := &mockResizer{
		resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
			return []byte("resized"), "image/webp", nil
		},
	}
	handler := newMiddlewareWrapped(NewServer(s3, resizer, nil, ""))

	req := httptest.NewRequest("GET", "/123/2/images/products/100/100/test-image.jpg.webp", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. Body=%s", w.Code, w.Body.String())
	}
	got := parseServerTimingPhases(t, w.Header().Get("Server-Timing"))
	for _, phase := range []string{"s3-get", "resize", "s3-put"} {
		if !got[phase] {
			t.Errorf("Server-Timing missing %q on resize path; got %v", phase, w.Header().Get("Server-Timing"))
		}
	}
	for _, unwanted := range []string{"s3-exists", "s3-put-cache", "s3-put-origin"} {
		if got[unwanted] {
			t.Errorf("Server-Timing should not contain %q in off mode; got %v", unwanted, w.Header().Get("Server-Timing"))
		}
	}
}

func TestServeHTTP_ServerTiming_ErrorPathStillHasS3Get(t *testing.T) {
	s3 := &mockS3Client{
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return nil, "", &s3types.NoSuchKey{}
		},
	}
	handler := newMiddlewareWrapped(NewServer(s3, &mockResizer{}, nil, ""))

	req := httptest.NewRequest("GET", "/123/2/images/products/100/100/missing.jpg.webp", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	got := parseServerTimingPhases(t, w.Header().Get("Server-Timing"))
	if !got["s3-get"] {
		t.Errorf("Server-Timing missing s3-get on 404 path; got %v", w.Header().Get("Server-Timing"))
	}
}

func TestServeHTTP_XRequestIDEcho(t *testing.T) {
	s3 := &mockS3Client{
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return []byte("ok"), "image/jpeg", nil
		},
	}
	handler := newMiddlewareWrapped(NewServer(s3, &mockResizer{}, nil, ""))

	req := httptest.NewRequest("GET", "/cached-image.jpg", nil)
	req.Header.Set("X-Request-ID", "trace-abc-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got := w.Header().Get("X-Request-ID"); got != "trace-abc-123" {
		t.Errorf("response X-Request-ID = %q, want trace-abc-123", got)
	}
}

// --- Mode parsing -----------------------------------------------------

func TestParseCacheMode(t *testing.T) {
	tests := []struct {
		in   string
		want CacheMode
		err  bool
	}{
		{"", CacheModeOff, false},
		{"off", CacheModeOff, false},
		{"OFF", CacheModeOff, false},
		{" shadow ", CacheModeShadow, false},
		{"Shadow", CacheModeShadow, false},
		{"live", CacheModeLive, false},
		{"LIVE", CacheModeLive, false},
		{"bogus", CacheModeOff, true},
	}
	for _, tt := range tests {
		got, err := ParseCacheMode(tt.in)
		if tt.err {
			if err == nil {
				t.Errorf("ParseCacheMode(%q) expected error, got nil", tt.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseCacheMode(%q) unexpected error: %v", tt.in, err)
		}
		if got != tt.want {
			t.Errorf("ParseCacheMode(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestCacheMode_String(t *testing.T) {
	cases := map[CacheMode]string{
		CacheModeOff:    "off",
		CacheModeShadow: "shadow",
		CacheModeLive:   "live",
	}
	for m, want := range cases {
		if got := m.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", m, got, want)
		}
	}
}

// --- Mode + header dispatch (read source) -----------------------------

// callCounter wraps a mockS3Client.getFunc to count calls.
type callCounter struct {
	getCount int32
	putCount int32
}

func (c *callCounter) wrapGet(data []byte, contentType string, err error) func(ctx context.Context, key string) ([]byte, string, error) {
	return func(ctx context.Context, key string) ([]byte, string, error) {
		atomic.AddInt32(&c.getCount, 1)
		return data, contentType, err
	}
}

func (c *callCounter) wrapPut() func(ctx context.Context, key string, data []byte, contentType string) error {
	return func(ctx context.Context, key string, data []byte, contentType string) error {
		atomic.AddInt32(&c.putCount, 1)
		return nil
	}
}

func TestServeHTTP_OffMode_HeaderHasNoEffect(t *testing.T) {
	var origC callCounter
	origin := &mockS3Client{getFunc: origC.wrapGet([]byte("ok"), "image/jpeg", nil)}
	// In off mode both roles point to the same mock — verify the header is ignored.
	srv := NewServerWithMode(origin, origin, CacheModeOff, &mockResizer{}, nil, "")

	for _, h := range []string{"", "true", "false"} {
		atomic.StoreInt32(&origC.getCount, 0)
		req := httptest.NewRequest("GET", "/k.jpg", nil)
		if h != "" {
			req.Header.Set("X-Use-Cache", h)
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("X-Use-Cache=%q: status %d", h, w.Code)
		}
		if atomic.LoadInt32(&origC.getCount) != 1 {
			t.Errorf("X-Use-Cache=%q: origin getCount = %d, want 1", h, origC.getCount)
		}
	}
}

func TestServeHTTP_ShadowMode_DefaultReadsFromOrigin(t *testing.T) {
	var origC, cacheC callCounter
	origin := &mockS3Client{getFunc: origC.wrapGet([]byte("origin-data"), "image/jpeg", nil)}
	cache := &mockS3Client{getFunc: cacheC.wrapGet(nil, "", errors.New("should not be called"))}

	srv := NewServerWithMode(origin, cache, CacheModeShadow, &mockResizer{}, nil, "")

	req := httptest.NewRequest("GET", "/k.jpg", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if origC.getCount != 1 {
		t.Errorf("origin getCount = %d, want 1", origC.getCount)
	}
	if cacheC.getCount != 0 {
		t.Errorf("cache getCount = %d, want 0 (shadow default reads from origin)", cacheC.getCount)
	}
	if w.Body.String() != "origin-data" {
		t.Errorf("body = %q, want origin-data", w.Body.String())
	}
}

func TestServeHTTP_ShadowMode_HeaderForceCache(t *testing.T) {
	var origC, cacheC callCounter
	origin := &mockS3Client{getFunc: origC.wrapGet(nil, "", errors.New("should not be called"))}
	cache := &mockS3Client{getFunc: cacheC.wrapGet([]byte("cache-data"), "image/jpeg", nil)}

	srv := NewServerWithMode(origin, cache, CacheModeShadow, &mockResizer{}, nil, "")

	req := httptest.NewRequest("GET", "/k.jpg", nil)
	req.Header.Set("X-Use-Cache", "true")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if cacheC.getCount != 1 {
		t.Errorf("cache getCount = %d, want 1", cacheC.getCount)
	}
	if origC.getCount != 0 {
		t.Errorf("origin getCount = %d, want 0 (header forced cache read)", origC.getCount)
	}
	if w.Body.String() != "cache-data" {
		t.Errorf("body = %q, want cache-data", w.Body.String())
	}
}

func TestServeHTTP_LiveMode_DefaultReadsFromCache(t *testing.T) {
	var origC, cacheC callCounter
	origin := &mockS3Client{getFunc: origC.wrapGet(nil, "", errors.New("should not be called"))}
	cache := &mockS3Client{getFunc: cacheC.wrapGet([]byte("cache-data"), "image/jpeg", nil)}

	srv := NewServerWithMode(origin, cache, CacheModeLive, &mockResizer{}, nil, "")

	req := httptest.NewRequest("GET", "/k.jpg", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if cacheC.getCount != 1 {
		t.Errorf("cache getCount = %d, want 1", cacheC.getCount)
	}
	if origC.getCount != 0 {
		t.Errorf("origin getCount = %d, want 0 (live default reads from cache)", origC.getCount)
	}
}

func TestServeHTTP_LiveMode_HeaderForceOrigin(t *testing.T) {
	var origC, cacheC callCounter
	origin := &mockS3Client{getFunc: origC.wrapGet([]byte("origin-data"), "image/jpeg", nil)}
	cache := &mockS3Client{getFunc: cacheC.wrapGet(nil, "", errors.New("should not be called"))}

	srv := NewServerWithMode(origin, cache, CacheModeLive, &mockResizer{}, nil, "")

	req := httptest.NewRequest("GET", "/k.jpg", nil)
	req.Header.Set("X-Use-Cache", "false")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if origC.getCount != 1 {
		t.Errorf("origin getCount = %d, want 1", origC.getCount)
	}
	if cacheC.getCount != 0 {
		t.Errorf("cache getCount = %d, want 0 (header forced origin read)", cacheC.getCount)
	}
}

func TestServeHTTP_NoSuchKey_CleanFallThrough(t *testing.T) {
	// Read source returns typed NoSuchKey → clean miss → fall through to
	// regex matching → 404 because no regex matches.
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(io.Discard) })

	s3 := &mockS3Client{
		getFunc: notFoundGet,
	}
	srv := NewServer(s3, &mockResizer{}, nil, "")

	req := httptest.NewRequest("GET", "/no-pattern.jpg", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", w.Code)
	}
	if strings.Contains(logBuf.String(), "cache client error") {
		t.Errorf("NoSuchKey miss should not log 'cache client error'; log:\n%s", logBuf.String())
	}
}

func TestServeHTTP_NonNotFoundError_LogsAndFallsThrough(t *testing.T) {
	// Read source returns a transient non-not-found error → log line is
	// emitted AND request still falls through to the regex matching path.
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(io.Discard) })

	s3 := &mockS3Client{
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return nil, "", errors.New("503 ServiceUnavailable")
		},
	}
	srv := NewServer(s3, &mockResizer{}, nil, "")

	req := httptest.NewRequest("GET", "/no-pattern.jpg", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 (fail-open fall-through)", w.Code)
	}
	if !strings.Contains(logBuf.String(), "cache client error") {
		t.Errorf("expected 'cache client error' in log; got:\n%s", logBuf.String())
	}
}

// --- Dual-write tests --------------------------------------------------

type putRecorder struct {
	mu    sync.Mutex
	order []string
}

func (p *putRecorder) record(side string) func(ctx context.Context, key string, data []byte, contentType string) error {
	return func(ctx context.Context, key string, data []byte, contentType string) error {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.order = append(p.order, side)
		return nil
	}
}

func (p *putRecorder) recordWithErr(side string, err error) func(ctx context.Context, key string, data []byte, contentType string) error {
	return func(ctx context.Context, key string, data []byte, contentType string) error {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.order = append(p.order, side)
		return err
	}
}

func (p *putRecorder) Order() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.order))
	copy(out, p.order)
	return out
}

func makeResizeFixture(t *testing.T, mode CacheMode, originalKey string, origPut, cachePut func(ctx context.Context, key string, data []byte, contentType string) error) *Server {
	t.Helper()
	origin := &mockS3Client{
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			if key == originalKey {
				return []byte("original"), "image/jpeg", nil
			}
			return nil, "", &s3types.NoSuchKey{}
		},
		putFunc: origPut,
	}
	cache := &mockS3Client{
		// Cache returns NoSuchKey on the speculative GET so the resize
		// pipeline kicks in.
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			return nil, "", &s3types.NoSuchKey{}
		},
		putFunc: cachePut,
	}
	resizer := &mockResizer{
		resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
			return []byte("resized"), "image/webp", nil
		},
	}
	return NewServerWithMode(origin, cache, mode, resizer, nil, "")
}

func TestHandleResize_ShadowMode_DualWritesOriginFirst(t *testing.T) {
	var p putRecorder
	srv := makeResizeFixture(t, CacheModeShadow,
		"123/catalog/products/images/test-image.jpg",
		p.record("origin"), p.record("cache"))

	req := httptest.NewRequest("GET", "/123/2/images/products/100/100/test-image.jpg.webp", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	got := p.Order()
	if len(got) != 2 {
		t.Fatalf("expected 2 puts (dual-write), got %d: %v", len(got), got)
	}
	if got[0] != "origin" || got[1] != "cache" {
		t.Errorf("shadow-mode put order = %v, want [origin cache]", got)
	}
}

func TestHandleResize_LiveMode_DualWritesCacheFirst(t *testing.T) {
	var p putRecorder
	srv := makeResizeFixture(t, CacheModeLive,
		"123/catalog/products/images/test-image.jpg",
		p.record("origin"), p.record("cache"))

	req := httptest.NewRequest("GET", "/123/2/images/products/100/100/test-image.jpg.webp", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	got := p.Order()
	if len(got) != 2 {
		t.Fatalf("expected 2 puts (dual-write), got %d: %v", len(got), got)
	}
	if got[0] != "cache" || got[1] != "origin" {
		t.Errorf("live-mode put order = %v, want [cache origin]", got)
	}
}

func TestHandleResize_DualWriteCacheFailure_StillSucceeds(t *testing.T) {
	var p putRecorder
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(io.Discard) })

	srv := makeResizeFixture(t, CacheModeShadow,
		"123/catalog/products/images/test-image.jpg",
		p.record("origin"),
		p.recordWithErr("cache", errors.New("R2 transient error")))

	req := httptest.NewRequest("GET", "/123/2/images/products/100/100/test-image.jpg.webp", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 (cache failure must not fail request)", w.Code)
	}
	if w.Body.String() != "resized" {
		t.Errorf("body = %q, want resized", w.Body.String())
	}
	if len(p.Order()) != 2 {
		t.Errorf("both puts must still be attempted; got order = %v", p.Order())
	}
	if !strings.Contains(logBuf.String(), "dual-write cache failed") {
		t.Errorf("expected 'dual-write cache failed' log line; got:\n%s", logBuf.String())
	}
}

func TestHandleResize_DualWriteOriginFailure_StillSucceeds(t *testing.T) {
	var p putRecorder
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(io.Discard) })

	srv := makeResizeFixture(t, CacheModeLive,
		"123/catalog/products/images/test-image.jpg",
		p.recordWithErr("origin", errors.New("HOS transient")),
		p.record("cache"))

	req := httptest.NewRequest("GET", "/123/2/images/products/100/100/test-image.jpg.webp", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 (origin failure must not fail request)", w.Code)
	}
	if len(p.Order()) != 2 {
		t.Errorf("both puts must still be attempted; got order = %v", p.Order())
	}
	if !strings.Contains(logBuf.String(), "dual-write origin failed") {
		t.Errorf("expected 'dual-write origin failed' log line; got:\n%s", logBuf.String())
	}
}

// --- Server-Timing phase distinctions per mode ------------------------

func TestServeHTTP_ServerTiming_ShadowMode_PhasesSplit(t *testing.T) {
	var p putRecorder
	srv := makeResizeFixture(t, CacheModeShadow,
		"123/catalog/products/images/test-image.jpg",
		p.record("origin"), p.record("cache"))
	handler := accesslog.Middleware(srv, accesslog.NewLogger(io.Discard), "test")

	req := httptest.NewRequest("GET", "/123/2/images/products/100/100/test-image.jpg.webp", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	got := parseServerTimingPhases(t, w.Header().Get("Server-Timing"))
	for _, phase := range []string{"s3-get", "resize", "s3-put-origin", "s3-put-cache"} {
		if !got[phase] {
			t.Errorf("Server-Timing missing %q in shadow mode; got %v", phase, w.Header().Get("Server-Timing"))
		}
	}
	if got["s3-put"] {
		t.Errorf("Server-Timing must not contain bare 's3-put' in shadow mode; got %v", w.Header().Get("Server-Timing"))
	}
}

func TestServeHTTP_ServerTiming_LiveMode_PhasesSplit(t *testing.T) {
	var p putRecorder
	srv := makeResizeFixture(t, CacheModeLive,
		"123/catalog/products/images/test-image.jpg",
		p.record("origin"), p.record("cache"))
	handler := accesslog.Middleware(srv, accesslog.NewLogger(io.Discard), "test")

	req := httptest.NewRequest("GET", "/123/2/images/products/100/100/test-image.jpg.webp", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	got := parseServerTimingPhases(t, w.Header().Get("Server-Timing"))
	for _, phase := range []string{"s3-get", "resize", "s3-put-cache", "s3-put-origin"} {
		if !got[phase] {
			t.Errorf("Server-Timing missing %q in live mode; got %v", phase, w.Header().Get("Server-Timing"))
		}
	}
	if got["s3-put"] {
		t.Errorf("Server-Timing must not contain bare 's3-put' in live mode; got %v", w.Header().Get("Server-Timing"))
	}
}

// --- helpers -----------------------------------------------------------

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
