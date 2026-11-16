package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"image-proxy/internal/types"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func errNoSuchKey() error { return &s3types.NoSuchKey{} }

// Live mode reads the cache bucket first. A variant that was rendered
// while the proxy ran in shadow (or off) mode only exists in the origin
// bucket, so a cache miss must look there before re-rendering it, and copy
// what it finds into the cache so the next request is a cache hit.

type putCall struct {
	key         string
	data        string
	contentType string
}

func recordingPut(ch chan<- putCall) func(ctx context.Context, key string, data []byte, contentType string) error {
	return func(ctx context.Context, key string, data []byte, contentType string) error {
		ch <- putCall{key, string(data), contentType}
		return nil
	}
}

func failingResizer(t *testing.T) *mockResizer {
	return &mockResizer{resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
		t.Errorf("resize must not run when the origin already has the variant")
		return nil, "", errors.New("unexpected resize")
	}}
}

func TestLiveMode_CacheMiss_ServesVariantFromOriginAndBackfillsCache(t *testing.T) {
	const key = "123/3/images/products/600/0/chair.jpg"
	var originGets int32
	origin := &mockS3Client{getFunc: func(ctx context.Context, k string) ([]byte, string, error) {
		atomic.AddInt32(&originGets, 1)
		if k == key {
			return []byte("variant"), "image/jpeg", nil
		}
		return nil, "", errors.New("unexpected origin key " + k)
	}}
	puts := make(chan putCall, 1)
	cache := &mockS3Client{getFunc: notFoundGet, putFunc: recordingPut(puts)}

	srv := NewServerWithMode(origin, cache, CacheModeLive, failingResizer(t), nil, "")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", "/"+key, nil))

	if w.Code != http.StatusOK || w.Body.String() != "variant" {
		t.Fatalf("got %d %q, want 200 \"variant\"", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "max-age=31536000" {
		t.Errorf("Cache-Control = %q", w.Header().Get("Cache-Control"))
	}
	if n := atomic.LoadInt32(&originGets); n != 1 {
		t.Errorf("origin gets = %d, want 1", n)
	}
	select {
	case p := <-puts:
		if p.key != key || p.data != "variant" || p.contentType != "image/jpeg" {
			t.Errorf("backfill put = %+v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("variant was not copied into the cache")
	}
}

func TestLiveMode_CacheAndOriginMiss_ResizesAsBefore(t *testing.T) {
	origin := &mockS3Client{getFunc: func(ctx context.Context, k string) ([]byte, string, error) {
		if k == "123/catalog/products/images/chair.jpg" {
			return []byte("original"), "image/jpeg", nil
		}
		return nil, "", errNoSuchKey()
	}}
	cache := &mockS3Client{getFunc: notFoundGet}
	var resized int32
	rz := &mockResizer{resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
		atomic.AddInt32(&resized, 1)
		return []byte("resized"), "image/webp", nil
	}}

	srv := NewServerWithMode(origin, cache, CacheModeLive, rz, nil, "")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", "/123/3/images/products/600/0/chair.jpg.webp", nil))

	if w.Code != http.StatusOK || w.Body.String() != "resized" {
		t.Fatalf("got %d %q, want 200 \"resized\"", w.Code, w.Body.String())
	}
	if atomic.LoadInt32(&resized) != 1 {
		t.Errorf("resize calls = %d, want 1", resized)
	}
}

func TestLiveMode_CacheError_FallsBackToOriginWithoutBackfill(t *testing.T) {
	const key = "123/3/images/products/600/0/chair.jpg"
	origin := &mockS3Client{getFunc: func(ctx context.Context, k string) ([]byte, string, error) {
		return []byte("variant"), "image/jpeg", nil
	}}
	var cachePuts int32
	cache := &mockS3Client{
		getFunc: func(ctx context.Context, k string) ([]byte, string, error) {
			return nil, "", errors.New("503 ServiceUnavailable")
		},
		putFunc: func(ctx context.Context, k string, d []byte, ct string) error {
			atomic.AddInt32(&cachePuts, 1)
			return nil
		},
	}

	srv := NewServerWithMode(origin, cache, CacheModeLive, failingResizer(t), nil, "")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", "/"+key, nil))

	if w.Code != http.StatusOK || w.Body.String() != "variant" {
		t.Fatalf("got %d %q, want 200 \"variant\"", w.Code, w.Body.String())
	}
	srv.waitBackfills()
	if n := atomic.LoadInt32(&cachePuts); n != 0 {
		t.Errorf("cache puts = %d, want 0 (cache is failing, do not write to it)", n)
	}
}

func TestShadowMode_DoesNotReadThrough(t *testing.T) {
	// Shadow reads the origin; the cache is never consulted for reads, so
	// nothing changes there.
	var cacheGets int32
	origin := &mockS3Client{getFunc: func(ctx context.Context, k string) ([]byte, string, error) {
		return []byte("variant"), "image/jpeg", nil
	}}
	cache := &mockS3Client{getFunc: func(ctx context.Context, k string) ([]byte, string, error) {
		atomic.AddInt32(&cacheGets, 1)
		return nil, "", errNoSuchKey()
	}}
	srv := NewServerWithMode(origin, cache, CacheModeShadow, failingResizer(t), nil, "")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", "/123/3/images/products/600/0/chair.jpg", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if cacheGets != 0 {
		t.Errorf("cache gets = %d, want 0", cacheGets)
	}
}

func TestLiveMode_BackfillIsBounded(t *testing.T) {
	// When the backfill queue is full, the request is still served and the
	// copy is skipped; the next miss will try again.
	origin := &mockS3Client{getFunc: func(ctx context.Context, k string) ([]byte, string, error) {
		return []byte("variant"), "image/jpeg", nil
	}}
	release := make(chan struct{})
	var started int32
	cache := &mockS3Client{
		getFunc: notFoundGet,
		putFunc: func(ctx context.Context, k string, d []byte, ct string) error {
			atomic.AddInt32(&started, 1)
			<-release
			return nil
		},
	}
	srv := NewServerWithMode(origin, cache, CacheModeLive, failingResizer(t), nil, "")
	srv.backfillSlots = make(chan struct{}, 1)

	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest("GET", "/123/3/images/products/600/0/chair.jpg", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: status %d", i, w.Code)
		}
	}
	close(release)
	srv.waitBackfills()
	if n := atomic.LoadInt32(&started); n != 1 {
		t.Errorf("backfills started = %d, want 1 (one slot)", n)
	}
}
