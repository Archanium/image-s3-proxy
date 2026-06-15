package worker

import (
	"context"
	"errors"
	"image-proxy/internal/types"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("DEBUG") != "true" {
		log.SetOutput(io.Discard)
	}
	os.Exit(m.Run())
}

type mockS3Client struct {
	mu         sync.Mutex
	existsFunc func(ctx context.Context, key string) (bool, error)
	getFunc    func(ctx context.Context, key string) ([]byte, string, error)
	putFunc    func(ctx context.Context, key string, data []byte, contentType string) error
	putKeys    []string
}

func (m *mockS3Client) Exists(ctx context.Context, key string) (bool, error) {
	if m.existsFunc != nil {
		return m.existsFunc(ctx, key)
	}
	return false, nil
}
func (m *mockS3Client) Get(ctx context.Context, key string) ([]byte, string, error) {
	if m.getFunc == nil {
		return nil, "", errors.New("getFunc not set")
	}
	return m.getFunc(ctx, key)
}
func (m *mockS3Client) Put(ctx context.Context, key string, data []byte, contentType string) error {
	m.mu.Lock()
	m.putKeys = append(m.putKeys, key)
	m.mu.Unlock()
	if m.putFunc == nil {
		return nil
	}
	return m.putFunc(ctx, key, data, contentType)
}

func (m *mockS3Client) PutKeys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.putKeys))
	copy(out, m.putKeys)
	return out
}

type mockResizer struct {
	mu         sync.Mutex
	calls      int
	failOnCall int // 1-indexed; 0 disables
	resizeFunc func(data []byte, opts types.ImageOptions) ([]byte, string, error)
}

func (m *mockResizer) Resize(data []byte, opts types.ImageOptions) ([]byte, string, error) {
	m.mu.Lock()
	m.calls++
	thisCall := m.calls
	m.mu.Unlock()
	if m.failOnCall != 0 && thisCall == m.failOnCall {
		return nil, "", errors.New("synthetic resize failure")
	}
	if m.resizeFunc != nil {
		return m.resizeFunc(data, opts)
	}
	return []byte("resized"), "image/" + opts.Format, nil
}

func (m *mockResizer) Calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func newGet(data []byte, contentType string) func(ctx context.Context, key string) ([]byte, string, error) {
	return func(ctx context.Context, key string) ([]byte, string, error) {
		return data, contentType, nil
	}
}

// --- happy paths --------------------------------------------------------

func TestProcessBatch_HappyPath_SingleImage_SingleSize_SingleFormat(t *testing.T) {
	s3 := &mockS3Client{getFunc: newGet([]byte("original"), "image/jpeg")}
	rz := &mockResizer{}
	w := NewWorker(s3, nil, rz, [][]int{{100, 100}}, "avif", false)

	if err := w.ProcessBatch(context.Background(), BatchRequest{
		ClientID: "39", Version: 3,
		Images:  []string{"catalog/products/images/foo.jpg"},
		Formats: []string{"avif"},
	}); err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}

	if got := rz.Calls(); got != 1 {
		t.Errorf("resize calls = %d, want 1", got)
	}
	puts := s3.PutKeys()
	if len(puts) != 1 {
		t.Fatalf("put count = %d, want 1; keys = %v", len(puts), puts)
	}
	want := "39/3/images/products/100/100/foo.jpg.avif"
	if puts[0] != want {
		t.Errorf("put key = %q, want %q", puts[0], want)
	}
}

func TestProcessBatch_MultiImage(t *testing.T) {
	s3 := &mockS3Client{getFunc: newGet([]byte("original"), "image/jpeg")}
	w := NewWorker(s3, nil, &mockResizer{}, [][]int{{100, 100}}, "avif", false)

	if err := w.ProcessBatch(context.Background(), BatchRequest{
		ClientID: "39", Version: 3,
		Images:  []string{"img1.jpg", "img2.png"},
		Formats: []string{"avif"},
	}); err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}

	puts := s3.PutKeys()
	if len(puts) != 2 {
		t.Fatalf("put count = %d, want 2; got %v", len(puts), puts)
	}
	joined := strings.Join(puts, "\n")
	if !strings.Contains(joined, "img1.jpg.avif") {
		t.Errorf("expected an output for img1.jpg; got %v", puts)
	}
	if !strings.Contains(joined, "img2.png.avif") {
		t.Errorf("expected an output for img2.png; got %v", puts)
	}
}

func TestProcessBatch_MultiSize(t *testing.T) {
	s3 := &mockS3Client{getFunc: newGet([]byte("original"), "image/jpeg")}
	w := NewWorker(s3, nil, &mockResizer{}, nil, "avif", false)

	err := w.ProcessBatch(context.Background(), BatchRequest{
		ClientID: "39", Version: 3,
		Images:  []string{"foo.jpg"},
		Sizes:   [][]int{{100, 100}, {200, 200}, {300, 300}},
		Formats: []string{"avif"},
	})
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	if got := len(s3.PutKeys()); got != 3 {
		t.Errorf("put count = %d, want 3; got %v", got, s3.PutKeys())
	}
}

func TestProcessBatch_MultiFormat(t *testing.T) {
	s3 := &mockS3Client{getFunc: newGet([]byte("original"), "image/jpeg")}
	w := NewWorker(s3, nil, &mockResizer{}, [][]int{{100, 100}}, "avif", false)

	err := w.ProcessBatch(context.Background(), BatchRequest{
		ClientID: "39", Version: 3,
		Images:  []string{"foo.jpg"},
		Formats: []string{"avif", "webp"},
	})
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	puts := s3.PutKeys()
	if len(puts) != 2 {
		t.Fatalf("put count = %d, want 2; got %v", len(puts), puts)
	}
	if !strings.HasSuffix(puts[0], ".avif") || !strings.HasSuffix(puts[1], ".webp") {
		t.Errorf("expected .avif then .webp outputs, got %v", puts)
	}
}

func TestProcessBatch_FullCartesian(t *testing.T) {
	s3 := &mockS3Client{getFunc: newGet([]byte("original"), "image/jpeg")}
	rz := &mockResizer{}
	w := NewWorker(s3, nil, rz, nil, "avif", false)

	err := w.ProcessBatch(context.Background(), BatchRequest{
		ClientID: "39", Version: 3,
		Images:  []string{"a.jpg", "b.jpg"},
		Sizes:   [][]int{{100, 100}, {200, 200}},
		Formats: []string{"avif", "webp"},
	})
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	want := 2 * 2 * 2 // images × sizes × formats
	if got := rz.Calls(); got != want {
		t.Errorf("resize calls = %d, want %d", got, want)
	}
	if got := len(s3.PutKeys()); got != want {
		t.Errorf("put count = %d, want %d", got, want)
	}
}

func TestProcessBatch_SizesNil_UsesEnvDefaults(t *testing.T) {
	s3 := &mockS3Client{getFunc: newGet([]byte("original"), "image/jpeg")}
	envSizes := [][]int{{50, 50}, {75, 75}}
	w := NewWorker(s3, nil, &mockResizer{}, envSizes, "avif", false)

	err := w.ProcessBatch(context.Background(), BatchRequest{
		ClientID: "39", Version: 3,
		Images:  []string{"foo.jpg"},
		Sizes:   nil, // → env defaults
		Formats: []string{"avif"},
	})
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	if got := len(s3.PutKeys()); got != 2 {
		t.Errorf("put count = %d, want 2 (one per env size); got %v", got, s3.PutKeys())
	}
}

func TestProcessBatch_ClientIDInOutputKey(t *testing.T) {
	s3 := &mockS3Client{getFunc: newGet([]byte("original"), "image/jpeg")}
	w := NewWorker(s3, nil, &mockResizer{}, [][]int{{100, 100}}, "avif", false)

	err := w.ProcessBatch(context.Background(), BatchRequest{
		ClientID: "39", Version: 3,
		Images:  []string{"foo.jpg"},
		Formats: []string{"avif"},
	})
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	puts := s3.PutKeys()
	if len(puts) != 1 || !strings.HasPrefix(puts[0], "39/") {
		t.Errorf("put key must start with clientId prefix '39/'; got %v", puts)
	}
	for _, k := range puts {
		if strings.HasPrefix(k, "13/") {
			t.Errorf("regression: put key still has old hardcoded '13/' prefix: %s", k)
		}
	}
}

// --- dual-write + skip-existing ----------------------------------------

func TestProcessBatch_DualWritesWhenClientsDiffer(t *testing.T) {
	origin := &mockS3Client{getFunc: newGet([]byte("original"), "image/jpeg")}
	cache := &mockS3Client{}
	w := NewWorker(origin, cache, &mockResizer{}, [][]int{{100, 100}}, "avif", false)

	err := w.ProcessBatch(context.Background(), BatchRequest{
		ClientID: "39", Version: 3,
		Images:  []string{"foo.jpg"},
		Formats: []string{"avif"},
	})
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	if got := len(origin.PutKeys()); got != 1 {
		t.Errorf("origin put count = %d, want 1", got)
	}
	if got := len(cache.PutKeys()); got != 1 {
		t.Errorf("cache put count = %d, want 1", got)
	}
}

func TestProcessBatch_SkipExistingTargetsCache(t *testing.T) {
	origin := &mockS3Client{
		existsFunc: func(ctx context.Context, key string) (bool, error) {
			t.Errorf("origin.Exists must not be called in split mode; was called on %s", key)
			return false, nil
		},
		getFunc: newGet([]byte("original"), "image/jpeg"),
	}
	cache := &mockS3Client{
		existsFunc: func(ctx context.Context, key string) (bool, error) {
			return true, nil // already populated → skip
		},
	}
	w := NewWorker(origin, cache, &mockResizer{}, [][]int{{100, 100}}, "avif", false)

	err := w.ProcessBatch(context.Background(), BatchRequest{
		ClientID: "39", Version: 3,
		Images:  []string{"foo.jpg"},
		Formats: []string{"avif"},
	})
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	if len(origin.PutKeys()) != 0 || len(cache.PutKeys()) != 0 {
		t.Errorf("expected zero puts when cache reports existing; got origin=%v cache=%v",
			origin.PutKeys(), cache.PutKeys())
	}
}

// --- failure isolation -------------------------------------------------

func TestProcessBatch_PerOutputResizeFailure_DoesNotAbortBatch(t *testing.T) {
	s3 := &mockS3Client{getFunc: newGet([]byte("original"), "image/jpeg")}
	// 3 sizes × 1 format = 3 resize calls; fail the 2nd.
	rz := &mockResizer{failOnCall: 2}
	w := NewWorker(s3, nil, rz, nil, "avif", false)

	err := w.ProcessBatch(context.Background(), BatchRequest{
		ClientID: "39", Version: 3,
		Images:  []string{"foo.jpg"},
		Sizes:   [][]int{{100, 100}, {200, 200}, {300, 300}},
		Formats: []string{"avif"},
	})
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	puts := s3.PutKeys()
	if len(puts) != 2 {
		t.Errorf("put count = %d, want 2 (one resize failure skipped); got %v", len(puts), puts)
	}
	for _, k := range puts {
		if strings.Contains(k, "200/200") {
			t.Errorf("size 200x200 should have been skipped (resize failed); got key %s", k)
		}
	}
}

func TestProcessBatch_PerImageGetFailure_SkipsImageContinuesBatch(t *testing.T) {
	getCalls := 0
	s3 := &mockS3Client{
		getFunc: func(ctx context.Context, key string) ([]byte, string, error) {
			getCalls++
			if key == "img2.jpg" {
				return nil, "", errors.New("synthetic 5xx")
			}
			return []byte("original"), "image/jpeg", nil
		},
	}
	w := NewWorker(s3, nil, &mockResizer{}, [][]int{{100, 100}}, "avif", false)

	err := w.ProcessBatch(context.Background(), BatchRequest{
		ClientID: "39", Version: 3,
		Images:  []string{"img1.jpg", "img2.jpg", "img3.jpg"},
		Formats: []string{"avif"},
	})
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	if getCalls != 3 {
		t.Errorf("Get must be attempted on every image; got %d calls", getCalls)
	}
	puts := s3.PutKeys()
	if len(puts) != 2 {
		t.Errorf("put count = %d, want 2 (img2 skipped); got %v", len(puts), puts)
	}
	joined := strings.Join(puts, "\n")
	if strings.Contains(joined, "img2.jpg") {
		t.Errorf("img2.jpg should have been skipped due to Get failure; got %v", puts)
	}
}
