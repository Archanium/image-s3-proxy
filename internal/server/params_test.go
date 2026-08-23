package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"image-proxy/internal/types"
	"image-proxy/internal/urlsign"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Hex encodings of the plaintext key "secret" and salt "hello".
const (
	paramKeyHex  = "736563726574"
	paramSaltHex = "68656c6c6f"
)

func paramSigner(t *testing.T, allowUnsafe bool) *urlsign.Verifier {
	t.Helper()
	v, err := urlsign.NewVerifier(paramKeyHex, paramSaltHex, 0, allowUnsafe)
	if err != nil {
		t.Fatalf("NewVerifier failed: %v", err)
	}
	return v
}

// signedPath builds "/_p/{signature}/{tail}" for the given tail segments.
func signedPath(t *testing.T, v *urlsign.Verifier, tail ...string) string {
	t.Helper()
	joined := strings.Join(tail, "/")
	sig, err := v.Sign("/" + joined)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}
	return "/_p/" + sig + "/" + joined
}

// recordingS3 captures every Get and Put so tests can assert on which keys
// were touched, and how many times.
type recordingS3 struct {
	mu       sync.Mutex
	gets     []string
	puts     []string
	getFn    func(key string) ([]byte, string, error)
	putErr   error
	putBytes map[string][]byte
}

func (m *recordingS3) Exists(ctx context.Context, key string) (bool, error) { return false, nil }

func (m *recordingS3) Get(ctx context.Context, key string) ([]byte, string, error) {
	m.mu.Lock()
	m.gets = append(m.gets, key)
	m.mu.Unlock()
	if m.getFn != nil {
		return m.getFn(key)
	}
	return nil, "", &s3types.NoSuchKey{}
}

func (m *recordingS3) Put(ctx context.Context, key string, data []byte, contentType string) error {
	m.mu.Lock()
	m.puts = append(m.puts, key)
	if m.putBytes == nil {
		m.putBytes = map[string][]byte{}
	}
	m.putBytes[key] = data
	m.mu.Unlock()
	return m.putErr
}

func (m *recordingS3) getKeys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.gets...)
}

func (m *recordingS3) putKeys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.puts...)
}

// serveOnly returns a getFn that resolves exactly one key and reports a
// clean miss for everything else.
func serveOnly(key string, body, contentType string) func(string) ([]byte, string, error) {
	return func(k string) ([]byte, string, error) {
		if k == key {
			return []byte(body), contentType, nil
		}
		return nil, "", &s3types.NoSuchKey{}
	}
}

func passthroughResizer() *mockResizer {
	return &mockResizer{resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
		return []byte("resized"), "image/webp", nil
	}}
}

func doParamRequest(srv *Server, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

// --- enablement -----------------------------------------------------------

// TestParams_RouteDisabledWithoutSigner covers the fail-closed default: with
// no signing configured the /_p/ prefix is not special and 404s like any
// other unmatched path.
func TestParams_RouteDisabledWithoutSigner(t *testing.T) {
	s3 := &recordingS3{}
	srv := NewServer(s3, passthroughResizer(), nil, "")
	// Deliberately no SetURLSigner.

	w := doParamRequest(srv, "/_p/unsafe/w:240/13/products/foo.jpg")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when the route is disabled", w.Code)
	}
	for _, k := range s3.putKeys() {
		t.Errorf("disabled route wrote to S3: %q", k)
	}
}

// --- signature ------------------------------------------------------------

func TestParams_SignatureRejections(t *testing.T) {
	signer := paramSigner(t, false)
	valid := signedPath(t, signer, "w:240", "13", "products", "foo.jpg")

	tests := []struct {
		name string
		path string
	}{
		{"unsigned literal without the opt-in", "/_p/unsafe/w:240/13/products/foo.jpg"},
		{"garbage signature", "/_p/notasignature/w:240/13/products/foo.jpg"},
		{"empty signature segment", "/_p//w:240/13/products/foo.jpg"},
		{"tampered option", strings.Replace(valid, "w:240", "w:2400", 1)},
		{"tampered source", strings.Replace(valid, "foo.jpg", "bar.jpg", 1)},
		{"added option", strings.Replace(valid, "/w:240/", "/w:240/h:99/", 1)},
		{"removed option", strings.Replace(valid, "/w:240", "", 1)},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			s3 := &recordingS3{}
			srv := NewServer(s3, passthroughResizer(), nil, "")
			srv.SetURLSigner(signer)

			w := doParamRequest(srv, tt.path)
			if w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", w.Code)
			}
			if got := w.Header().Get("Cache-Control"); got != "max-age=30" {
				t.Errorf("Cache-Control = %q, want max-age=30", got)
			}
			// Nothing beyond verification may run for a bad signature.
			if keys := s3.getKeys(); len(keys) != 0 {
				t.Errorf("rejected request still read S3: %v", keys)
			}
		})
	}
}

func TestParams_ValidSignatureIsAccepted(t *testing.T) {
	signer := paramSigner(t, false)
	origin := &recordingS3{getFn: serveOnly("13/catalog/products/images/foo.jpg", "original", "image/jpeg")}
	srv := NewServer(origin, passthroughResizer(), nil, "")
	srv.SetURLSigner(signer)

	w := doParamRequest(srv, signedPath(t, signer, "w:240", "13", "products", "foo.jpg"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", w.Code, w.Body.String())
	}
	if w.Body.String() != "resized" {
		t.Errorf("body = %q, want resized", w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "max-age=31536000" {
		t.Errorf("Cache-Control = %q, want max-age=31536000", got)
	}
}

func TestParams_UnsafeAcceptedWhenAllowed(t *testing.T) {
	origin := &recordingS3{getFn: serveOnly("13/catalog/products/images/foo.jpg", "original", "image/jpeg")}
	srv := NewServer(origin, passthroughResizer(), nil, "")
	srv.SetURLSigner(paramSigner(t, true))

	w := doParamRequest(srv, "/_p/unsafe/w:240/13/products/foo.jpg")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", w.Code, w.Body.String())
	}
}

// --- option validation ----------------------------------------------------

func TestParams_BadOptionsReturn400(t *testing.T) {
	signer := paramSigner(t, true)

	tests := []struct {
		name        string
		segments    []string
		wantMessage string
	}{
		{"unknown option", []string{"zoom:2"}, "zoom"},
		{"non-numeric width", []string{"w:abc"}, "w"},
		{"unknown resizing type", []string{"rt:squish"}, "squish"},
		{"bad colour", []string{"bg:xyz"}, "bg"},
		{"unknown gravity", []string{"g:up"}, "g"},
		{"too many arguments", []string{"w:1:2"}, "at most"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			s3 := &recordingS3{}
			srv := NewServer(s3, passthroughResizer(), nil, "")
			srv.SetURLSigner(signer)

			path := "/_p/unsafe/" + strings.Join(tt.segments, "/") + "/13/products/foo.jpg"
			w := doParamRequest(srv, path)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
			if !strings.Contains(w.Body.String(), tt.wantMessage) {
				t.Errorf("body = %q, want it to name %q", strings.TrimSpace(w.Body.String()), tt.wantMessage)
			}
			if keys := s3.getKeys(); len(keys) != 0 {
				t.Errorf("rejected request still read S3: %v", keys)
			}
		})
	}
}

// TestParams_DimensionCapRejectsBeforeAnyIO is the load-bearing DoS guard:
// an oversized request must cost a string parse and nothing else. The mocks
// fail the test if they are reached at all.
func TestParams_DimensionCapRejectsBeforeAnyIO(t *testing.T) {
	signer := paramSigner(t, true)

	for _, segment := range []string{"w:5121", "h:99999", "c:99999:10", "rs:fill:6000:6000"} {
		segment := segment
		t.Run(segment, func(t *testing.T) {
			s3 := &recordingS3{getFn: func(string) ([]byte, string, error) {
				t.Error("S3 was read for a request that exceeds the dimension cap")
				return nil, "", errors.New("must not be called")
			}}
			rz := &mockResizer{resizeFunc: func([]byte, types.ImageOptions) ([]byte, string, error) {
				t.Error("libvips was invoked for a request that exceeds the dimension cap")
				return nil, "", errors.New("must not be called")
			}}
			srv := NewServer(s3, rz, nil, "")
			srv.SetURLSigner(signer)

			w := doParamRequest(srv, "/_p/unsafe/"+segment+"/13/products/foo.jpg")
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
			if !strings.Contains(w.Body.String(), "exceeds the maximum") {
				t.Errorf("body = %q, want it to explain the cap", strings.TrimSpace(w.Body.String()))
			}
		})
	}
}

func TestParams_DimensionCapIsConfigurable(t *testing.T) {
	origin := &recordingS3{getFn: serveOnly("13/catalog/products/images/foo.jpg", "original", "image/jpeg")}
	srv := NewServer(origin, passthroughResizer(), nil, "")
	srv.SetURLSigner(paramSigner(t, true))
	srv.SetMaxDimension(100)

	if w := doParamRequest(srv, "/_p/unsafe/w:101/13/products/foo.jpg"); w.Code != http.StatusBadRequest {
		t.Errorf("w:101 status = %d, want 400 with a cap of 100", w.Code)
	}
	if w := doParamRequest(srv, "/_p/unsafe/w:100/13/products/foo.jpg"); w.Code != http.StatusOK {
		t.Errorf("w:100 status = %d, want 200 with a cap of 100", w.Code)
	}
}

// --- source resolution ----------------------------------------------------

func TestParams_SourceResolutionMatchesLegacyCandidates(t *testing.T) {
	origin := &recordingS3{}
	srv := NewServer(origin, passthroughResizer(), nil, "")
	srv.SetURLSigner(paramSigner(t, true))

	w := doParamRequest(srv, "/_p/unsafe/w:240/13/products/foo.jpg")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when no candidate resolves", w.Code)
	}

	gets := origin.getKeys()
	// The first Get is the cache-key probe; the origin candidates follow.
	if len(gets) < 2 {
		t.Fatalf("expected a cache probe plus origin candidates, got %v", gets)
	}
	candidates := gets[1:]
	// splitOutputFormat leaves a simple "foo.jpg" name intact — it only
	// strips when the extension is compound, e.g. "foo.png.webp".
	want := originCandidateKeys("13", "products", "foo.jpg", "jpg")
	if len(candidates) != len(want) {
		t.Fatalf("tried %d candidates, want %d\n got: %v\nwant: %v", len(candidates), len(want), candidates, want)
	}
	for i := range want {
		if candidates[i] != want[i] {
			t.Errorf("candidate %d = %q, want %q", i, candidates[i], want[i])
		}
	}
}

func TestParams_UnrecognisedSourceTails(t *testing.T) {
	signer := paramSigner(t, true)
	tests := []struct {
		name string
		path string
	}{
		{"too few segments", "/_p/unsafe/w:240/foo.jpg"},
		{"no options and too few segments", "/_p/unsafe/13/foo.jpg"},
		{"filename without an extension", "/_p/unsafe/w:240/13/products/foo"},
		{"deeper than the legacy layout", "/_p/unsafe/w:240/13/products/sub/foo.jpg"},
		{"files form with the wrong arity", "/_p/unsafe/13/files/foo.pdf"},
		{"no source at all", "/_p/unsafe/w:240"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			s3 := &recordingS3{}
			srv := NewServer(s3, passthroughResizer(), nil, "")
			srv.SetURLSigner(signer)
			if w := doParamRequest(srv, tt.path); w.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", w.Code)
			}
		})
	}
}

func TestParams_FilesTailResolvesToTheLiteralKey(t *testing.T) {
	origin := &recordingS3{getFn: serveOnly("13/files/42/doc.pdf", "%PDF-1.4", "application/pdf")}
	srv := NewServer(origin, passthroughResizer(), nil, "")
	srv.SetURLSigner(paramSigner(t, true))

	w := doParamRequest(srv, "/_p/unsafe/13/files/42/doc.pdf")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	// A pdf is not an encodable output format, so it is passed through
	// untouched rather than silently re-encoded as JPEG.
	if w.Body.String() != "%PDF-1.4" {
		t.Errorf("body = %q, want the original bytes", w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/pdf" {
		t.Errorf("Content-Type = %q, want application/pdf", got)
	}
	if keys := origin.putKeys(); len(keys) != 0 {
		t.Errorf("passthrough wrote to S3: %v", keys)
	}
}

// --- cache behaviour ------------------------------------------------------

func TestParams_CacheKeyShape(t *testing.T) {
	origin := &recordingS3{getFn: serveOnly("13/catalog/products/images/foo.jpg", "original", "image/jpeg")}
	srv := NewServer(origin, passthroughResizer(), nil, "")
	srv.SetURLSigner(paramSigner(t, true))

	w := doParamRequest(srv, "/_p/unsafe/rt:fill/w:240/h:336/bg:fff/13/products/foo.jpg")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	want := "13/_p/background=ffffff,height=336,resizing_type=fill,width=240/products/foo.jpg"
	puts := origin.putKeys()
	if len(puts) != 1 || puts[0] != want {
		t.Errorf("put keys = %v, want exactly [%q]", puts, want)
	}
	if origin.getKeys()[0] != want {
		t.Errorf("cache probe key = %q, want %q", origin.getKeys()[0], want)
	}
}

// TestParams_EquivalentURLsShareOneCacheKey is the cache contract end to
// end: differently spelled but equivalent URLs must not each mint an object.
func TestParams_EquivalentURLsShareOneCacheKey(t *testing.T) {
	spellings := []string{
		"/_p/unsafe/h:336/rs:fill/w:240/bg:ffffff/13/products/foo.jpg",
		"/_p/unsafe/rt:fill/w:240/h:336/bg:fff/13/products/foo.jpg",
		"/_p/unsafe/background:255:255:255/width:240/height:336/resizing_type:fill/13/products/foo.jpg",
		"/_p/unsafe/rs:fill:240:336/bg:FFF/el:0/13/products/foo.jpg",
	}
	origin := &recordingS3{getFn: serveOnly("13/catalog/products/images/foo.jpg", "original", "image/jpeg")}
	srv := NewServer(origin, passthroughResizer(), nil, "")
	srv.SetURLSigner(paramSigner(t, true))

	for _, p := range spellings {
		if w := doParamRequest(srv, p); w.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", p, w.Code)
		}
	}

	seen := map[string]bool{}
	for _, k := range origin.putKeys() {
		seen[k] = true
	}
	if len(seen) != 1 {
		t.Errorf("%d distinct cache keys for equivalent URLs, want 1: %v", len(seen), origin.putKeys())
	}
}

func TestParams_CacheHitSkipsOriginAndResizer(t *testing.T) {
	cacheKey := "13/_p/width=240/products/foo.jpg"
	origin := &recordingS3{getFn: serveOnly(cacheKey, "cached", "image/webp")}
	rz := &mockResizer{resizeFunc: func([]byte, types.ImageOptions) ([]byte, string, error) {
		t.Error("resizer ran on a cache hit")
		return nil, "", errors.New("must not be called")
	}}
	srv := NewServer(origin, rz, nil, "")
	srv.SetURLSigner(paramSigner(t, true))

	w := doParamRequest(srv, "/_p/unsafe/w:240/13/products/foo.jpg")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != "cached" {
		t.Errorf("body = %q, want cached", w.Body.String())
	}
	if got := origin.getKeys(); len(got) != 1 || got[0] != cacheKey {
		t.Errorf("gets = %v, want exactly [%q]", got, cacheKey)
	}
	if keys := origin.putKeys(); len(keys) != 0 {
		t.Errorf("cache hit wrote to S3: %v", keys)
	}
}

func TestParams_DualWritePerCacheMode(t *testing.T) {
	const cacheKey = "13/_p/width=240/products/foo.jpg"
	tests := []struct {
		mode          CacheMode
		wantOriginPut int
		wantCachePut  int
	}{
		{CacheModeOff, 0, 1}, // off writes once, through the cache-role client
		{CacheModeShadow, 1, 1},
		{CacheModeLive, 1, 1},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.mode.String(), func(t *testing.T) {
			origin := &recordingS3{getFn: serveOnly("13/catalog/products/images/foo.jpg", "original", "image/jpeg")}
			cache := &recordingS3{}
			var srv *Server
			if tt.mode == CacheModeOff {
				// In off mode both roles are the same client, matching how
				// main.go wires it.
				srv = NewServerWithMode(origin, origin, CacheModeOff, passthroughResizer(), nil, "")
			} else {
				srv = NewServerWithMode(origin, cache, tt.mode, passthroughResizer(), nil, "")
			}
			srv.SetURLSigner(paramSigner(t, true))

			w := doParamRequest(srv, "/_p/unsafe/w:240/13/products/foo.jpg")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %q)", w.Code, w.Body.String())
			}

			if tt.mode == CacheModeOff {
				if got := len(origin.putKeys()); got != 1 {
					t.Errorf("off mode put %d times, want 1", got)
				}
				return
			}
			if got := len(origin.putKeys()); got != tt.wantOriginPut {
				t.Errorf("origin puts = %d, want %d", got, tt.wantOriginPut)
			}
			if got := len(cache.putKeys()); got != tt.wantCachePut {
				t.Errorf("cache puts = %d, want %d", got, tt.wantCachePut)
			}
			if keys := cache.putKeys(); len(keys) > 0 && keys[0] != cacheKey {
				t.Errorf("cache put key = %q, want %q", keys[0], cacheKey)
			}
		})
	}
}

func TestParams_UseCacheHeaderOverridesReadSource(t *testing.T) {
	const cacheKey = "13/_p/width=240/products/foo.jpg"

	tests := []struct {
		name        string
		mode        CacheMode
		header      string
		wantFromKey string
	}{
		{"shadow defaults to origin", CacheModeShadow, "", "origin"},
		{"shadow honours true", CacheModeShadow, "true", "cache"},
		{"live defaults to cache", CacheModeLive, "", "cache"},
		{"live honours false", CacheModeLive, "false", "origin"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			origin := &recordingS3{getFn: serveOnly(cacheKey, "origin", "image/webp")}
			cache := &recordingS3{getFn: serveOnly(cacheKey, "cache", "image/webp")}
			srv := NewServerWithMode(origin, cache, tt.mode, passthroughResizer(), nil, "")
			srv.SetURLSigner(paramSigner(t, true))

			req := httptest.NewRequest("GET", "/_p/unsafe/w:240/13/products/foo.jpg", nil)
			if tt.header != "" {
				req.Header.Set("X-Use-Cache", tt.header)
			}
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", w.Code)
			}
			if w.Body.String() != tt.wantFromKey {
				t.Errorf("body = %q, want %q", w.Body.String(), tt.wantFromKey)
			}
		})
	}
}

// --- passthrough ----------------------------------------------------------

func TestParams_PassthroughNeverWrites(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"raw", "/_p/unsafe/raw:1/13/products/foo.jpg"},
		{"raw alongside other options", "/_p/unsafe/w:240/raw:1/13/products/foo.jpg"},
		{"skip_processing matches the source format", "/_p/unsafe/skp:jpg/w:240/13/products/foo.jpg"},
		{"skip_processing among several", "/_p/unsafe/skp:png:jpg/13/products/foo.jpg"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			origin := &recordingS3{getFn: serveOnly("13/catalog/products/images/foo.jpg", "original-bytes", "image/jpeg")}
			rz := &mockResizer{resizeFunc: func([]byte, types.ImageOptions) ([]byte, string, error) {
				t.Error("resizer ran for a passthrough request")
				return nil, "", errors.New("must not be called")
			}}
			srv := NewServer(origin, rz, nil, "")
			srv.SetURLSigner(paramSigner(t, true))

			w := doParamRequest(srv, tt.path)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %q)", w.Code, w.Body.String())
			}
			if w.Body.String() != "original-bytes" {
				t.Errorf("body = %q, want the untouched original", w.Body.String())
			}
			if keys := origin.putKeys(); len(keys) != 0 {
				t.Errorf("passthrough wrote to S3: %v", keys)
			}
			// A passthrough must not even probe the cache — it would always miss.
			for _, k := range origin.getKeys() {
				if strings.Contains(k, "/_p/") {
					t.Errorf("passthrough probed the cache key %q", k)
				}
			}
		})
	}
}

func TestParams_SkipProcessingDoesNotMatchOtherFormats(t *testing.T) {
	origin := &recordingS3{getFn: serveOnly("13/catalog/products/images/foo.jpg", "original", "image/jpeg")}
	srv := NewServer(origin, passthroughResizer(), nil, "")
	srv.SetURLSigner(paramSigner(t, true))

	// skip_processing names png, but the request asks for jpg output.
	w := doParamRequest(srv, "/_p/unsafe/skp:png/w:240/13/products/foo.jpg")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != "resized" {
		t.Errorf("body = %q, want the resized bytes (skip_processing must not apply)", w.Body.String())
	}
	if len(origin.putKeys()) != 1 {
		t.Errorf("put keys = %v, want exactly one cache write", origin.putKeys())
	}
}

// --- error paths ----------------------------------------------------------

func TestParams_ResizeErrorReturns500(t *testing.T) {
	origin := &recordingS3{getFn: serveOnly("13/catalog/products/images/foo.jpg", "original", "image/jpeg")}
	rz := &mockResizer{resizeFunc: func([]byte, types.ImageOptions) ([]byte, string, error) {
		return nil, "", errors.New("boom")
	}}
	srv := NewServer(origin, rz, nil, "")
	srv.SetURLSigner(paramSigner(t, true))

	w := doParamRequest(srv, "/_p/unsafe/w:240/13/products/foo.jpg")
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
	if got := w.Header().Get("Cache-Control"); got != "max-age=30" {
		t.Errorf("Cache-Control = %q, want max-age=30", got)
	}
	if keys := origin.putKeys(); len(keys) != 0 {
		t.Errorf("failed resize still wrote to S3: %v", keys)
	}
}

// TestParams_ProcessingOptionsReachTheResizer guards the wiring between the
// parsed option set and libvips.
func TestParams_ProcessingOptionsReachTheResizer(t *testing.T) {
	origin := &recordingS3{getFn: serveOnly("13/catalog/products/images/foo.jpg", "original", "image/jpeg")}
	var got types.ImageOptions
	rz := &mockResizer{resizeFunc: func(data []byte, opts types.ImageOptions) ([]byte, string, error) {
		got = opts
		return []byte("resized"), "image/webp", nil
	}}
	srv := NewServer(origin, rz, nil, "")
	srv.SetURLSigner(paramSigner(t, true))

	w := doParamRequest(srv, "/_p/unsafe/rs:fill:240:336:1/bg:ff0000/g:sm/13/products/foo.webp")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got.Processing == nil {
		t.Fatal("Processing was nil — the legacy pipeline would have run")
	}
	p := got.Processing
	if p.Width != 240 || p.Height != 336 {
		t.Errorf("dimensions = %dx%d, want 240x336", p.Width, p.Height)
	}
	if p.ResizingType != "fill" {
		t.Errorf("ResizingType = %q, want fill", p.ResizingType)
	}
	if !p.Enlarge {
		t.Error("Enlarge = false, want true")
	}
	if !p.HasBackground || p.Background.Hex() != "ff0000" {
		t.Errorf("Background = %+v (set=%v), want ff0000", p.Background, p.HasBackground)
	}
	if p.Gravity.Type != "sm" {
		t.Errorf("Gravity = %q, want sm", p.Gravity.Type)
	}
	if got.Format != "webp" {
		t.Errorf("Format = %q, want webp (from the source extension)", got.Format)
	}
}

// TestParams_LegacyRoutesAreUnaffected guards the frozen contract: enabling
// the new route must not change how a legacy URL behaves.
func TestParams_LegacyRoutesAreUnaffected(t *testing.T) {
	origin := &recordingS3{getFn: serveOnly("13/catalog/products/images/foo.jpg", "original", "image/jpeg")}
	srv := NewServer(origin, passthroughResizer(), nil, "")
	srv.SetURLSigner(paramSigner(t, true))

	w := doParamRequest(srv, "/13/1/images/products/240/336/foo.jpg")
	if w.Code != http.StatusOK {
		t.Fatalf("legacy resize status = %d, want 200", w.Code)
	}
	want := "13/1/images/products/240/336/foo.jpg"
	if keys := origin.putKeys(); len(keys) != 1 || keys[0] != want {
		t.Errorf("legacy put keys = %v, want [%q]", keys, want)
	}
}
