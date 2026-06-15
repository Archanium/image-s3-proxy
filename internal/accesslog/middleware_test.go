package accesslog

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"
)

func newMiddlewareTest(t *testing.T, h http.Handler, upstreamHost string) (*Logger, *bytes.Buffer, http.Handler) {
	t.Helper()
	var buf bytes.Buffer
	lg := NewLogger(&buf)
	return lg, &buf, Middleware(h, lg, upstreamHost)
}

func parseLastEntry(t *testing.T, buf *bytes.Buffer) Entry {
	t.Helper()
	line := bytes.TrimRight(buf.Bytes(), "\n")
	var e Entry
	if err := json.Unmarshal(line, &e); err != nil {
		t.Fatalf("emitted line is not valid JSON: %v\nline: %s", err, line)
	}
	return e
}

// --- correlationId precedence ---

func TestMiddleware_CorrelationIDFromXRequestID(t *testing.T) {
	_, buf, mw := newMiddlewareTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), "bucket")

	req := httptest.NewRequest("GET", "/foo.jpg", nil)
	req.Header.Set("X-Request-ID", "from-client-001")
	req.Header.Set("CF-Ray", "should-be-ignored")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got != "from-client-001" {
		t.Errorf("response X-Request-ID = %q, want from-client-001", got)
	}
	if got := parseLastEntry(t, buf).Extra.CorrelationID; got != "from-client-001" {
		t.Errorf("log correlationId = %q, want from-client-001", got)
	}
}

func TestMiddleware_CorrelationIDFromCFRay(t *testing.T) {
	_, buf, mw := newMiddlewareTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), "bucket")

	req := httptest.NewRequest("GET", "/foo.jpg", nil)
	req.Header.Set("CF-Ray", "ray-xyz")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got != "ray-xyz" {
		t.Errorf("response X-Request-ID = %q, want ray-xyz", got)
	}
	if got := parseLastEntry(t, buf).Extra.CorrelationID; got != "ray-xyz" {
		t.Errorf("log correlationId = %q, want ray-xyz", got)
	}
}

func TestMiddleware_CorrelationIDGenerated(t *testing.T) {
	_, buf, mw := newMiddlewareTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), "bucket")

	req := httptest.NewRequest("GET", "/foo.jpg", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	got := rec.Header().Get("X-Request-ID")
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(got) {
		t.Errorf("generated X-Request-ID = %q, want 32 hex chars", got)
	}
	if logID := parseLastEntry(t, buf).Extra.CorrelationID; logID != got {
		t.Errorf("response X-Request-ID = %q but log correlationId = %q", got, logID)
	}
}

// --- schema ---

func TestMiddleware_UserBlockHasExactlyFiveKeysNoCart(t *testing.T) {
	_, buf, mw := newMiddlewareTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), "bucket")

	req := httptest.NewRequest("GET", "/foo.jpg", nil)
	// Send a 'cart' cookie deliberately — it must not appear anywhere in the log line.
	req.AddCookie(&http.Cookie{Name: "cart", Value: "session-value-do-not-log"})
	mw.ServeHTTP(httptest.NewRecorder(), req)

	if bytes.Contains(buf.Bytes(), []byte("cart")) {
		t.Errorf("log line contained 'cart': %s", buf.String())
	}
	if bytes.Contains(buf.Bytes(), []byte("session-value-do-not-log")) {
		t.Errorf("log line contained the cart cookie value: %s", buf.String())
	}

	// Re-parse via map and assert the user block has exactly the 5 documented keys.
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &generic); err != nil {
		t.Fatalf("parse: %v", err)
	}
	var userObj map[string]json.RawMessage
	if err := json.Unmarshal(generic["user"], &userObj); err != nil {
		t.Fatalf("parse user: %v", err)
	}
	want := map[string]bool{"ip": true, "cloudflare": true, "name": true, "referrer": true, "agent": true}
	if len(userObj) != len(want) {
		t.Errorf("user block keys = %v, want exactly %v", keysOf(userObj), keysOfBool(want))
	}
	for k := range userObj {
		if !want[k] {
			t.Errorf("unexpected user key %q", k)
		}
	}
}

func TestMiddleware_SchemeHTTPSFromXForwardedProto(t *testing.T) {
	_, buf, mw := newMiddlewareTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), "bucket")

	req := httptest.NewRequest("GET", "/foo.jpg", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	mw.ServeHTTP(httptest.NewRecorder(), req)

	if got := parseLastEntry(t, buf).Request.Scheme; got != "https" {
		t.Errorf("request.scheme = %q, want https", got)
	}
}

func TestMiddleware_SchemeHTTPByDefault(t *testing.T) {
	_, buf, mw := newMiddlewareTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), "bucket")

	mw.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/foo.jpg", nil))

	if got := parseLastEntry(t, buf).Request.Scheme; got != "http" {
		t.Errorf("request.scheme = %q, want http", got)
	}
}

// --- response.status / response.bytes ---

func TestMiddleware_ResponseStatusAndBytesReflectActual(t *testing.T) {
	_, buf, mw := newMiddlewareTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("12345"))
		_, _ = w.Write([]byte("67"))
	}), "bucket")

	mw.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/foo.jpg", nil))

	e := parseLastEntry(t, buf)
	if e.Response.Status != http.StatusTeapot {
		t.Errorf("response.status = %d, want %d", e.Response.Status, http.StatusTeapot)
	}
	if e.Response.Bytes != 7 {
		t.Errorf("response.bytes = %d, want 7", e.Response.Bytes)
	}
}

func TestMiddleware_DefaultsStatus200WhenHandlerOnlyWrites(t *testing.T) {
	_, buf, mw := newMiddlewareTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}), "bucket")

	mw.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/foo.jpg", nil))

	if got := parseLastEntry(t, buf).Response.Status; got != http.StatusOK {
		t.Errorf("response.status with implicit WriteHeader = %d, want 200", got)
	}
}

// --- upstream block ---

func TestMiddleware_UpstreamResponseTimeIsSumOfPhases(t *testing.T) {
	_, buf, mw := newMiddlewareTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tm := TimingsFromContext(r.Context())
		tm.Record("s3-get", 10*time.Millisecond)
		tm.Record("resize", 20*time.Millisecond)
		tm.Record("s3-put", 5*time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	}), "bucket.s3.example")

	mw.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/foo.jpg", nil))

	e := parseLastEntry(t, buf)
	if e.Upstream.ResponseTime != 0.035 {
		t.Errorf("upstream.responseTime = %v, want 0.035", e.Upstream.ResponseTime)
	}
	if e.Upstream.UpstreamHost != "bucket.s3.example" {
		t.Errorf("upstream.upstreamHost = %q, want bucket.s3.example", e.Upstream.UpstreamHost)
	}
	if e.Upstream.Version != "" || e.Upstream.Preloading != "" {
		t.Errorf("upstream.version/preloading must be empty for schema parity, got version=%q preloading=%q", e.Upstream.Version, e.Upstream.Preloading)
	}
}

// --- Server-Timing header on response ---

func TestMiddleware_ServerTimingHeaderSetWhenPhasesRecorded(t *testing.T) {
	_, _, mw := newMiddlewareTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tm := TimingsFromContext(r.Context())
		tm.Record("s3-get", 10*time.Millisecond)
		tm.Record("resize", 20*time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	}), "bucket")

	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, httptest.NewRequest("GET", "/foo.jpg", nil))

	got := rec.Header().Get("Server-Timing")
	want := "s3-get;dur=10.0, resize;dur=20.0"
	if got != want {
		t.Errorf("Server-Timing = %q, want %q", got, want)
	}
}

// --- client IP via X-Forwarded-For ---

func TestMiddleware_ClientIPFromXForwardedFor(t *testing.T) {
	_, buf, mw := newMiddlewareTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), "bucket")

	req := httptest.NewRequest("GET", "/foo.jpg", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.1")
	req.RemoteAddr = "10.0.0.2:1234"
	mw.ServeHTTP(httptest.NewRecorder(), req)

	if got := parseLastEntry(t, buf).User.IP; got != "203.0.113.5" {
		t.Errorf("user.ip = %q, want 203.0.113.5", got)
	}
}

func TestMiddleware_ClientIPFromRemoteAddrStripsPort(t *testing.T) {
	_, buf, mw := newMiddlewareTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), "bucket")

	req := httptest.NewRequest("GET", "/foo.jpg", nil)
	req.RemoteAddr = "10.0.0.2:54321"
	mw.ServeHTTP(httptest.NewRecorder(), req)

	if got := parseLastEntry(t, buf).User.IP; got != "10.0.0.2" {
		t.Errorf("user.ip = %q, want 10.0.0.2", got)
	}
}

// --- CF-Connecting-IP separate field ---

func TestMiddleware_CloudflareIPField(t *testing.T) {
	_, buf, mw := newMiddlewareTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), "bucket")

	req := httptest.NewRequest("GET", "/foo.jpg", nil)
	req.Header.Set("CF-Connecting-IP", "198.51.100.7")
	mw.ServeHTTP(httptest.NewRecorder(), req)

	if got := parseLastEntry(t, buf).User.Cloudflare; got != "198.51.100.7" {
		t.Errorf("user.cloudflare = %q, want 198.51.100.7", got)
	}
}

// --- timings field (track add-timings-to-access-log) ---

func TestMiddleware_TimingsPopulatedFromContext(t *testing.T) {
	_, buf, mw := newMiddlewareTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tm := TimingsFromContext(r.Context())
		tm.Record("s3-get", 12*time.Millisecond)
		tm.Record("resize", 7*time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	}), "bucket")

	mw.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/k.jpg", nil))

	e := parseLastEntry(t, buf)
	if got, want := e.Timings["s3-get"], 0.012; got != want {
		t.Errorf("timings.s3-get = %v, want %v", got, want)
	}
	if got, want := e.Timings["resize"], 0.007; got != want {
		t.Errorf("timings.resize = %v, want %v", got, want)
	}
	if len(e.Timings) != 2 {
		t.Errorf("expected exactly 2 timing entries; got %d: %v", len(e.Timings), e.Timings)
	}
}

func TestMiddleware_TimingsEmptyWhenNoPhases(t *testing.T) {
	_, buf, mw := newMiddlewareTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), "bucket")

	mw.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/k.jpg", nil))

	if !bytes.Contains(buf.Bytes(), []byte(`"timings":{}`)) {
		t.Errorf("expected '\"timings\":{}' in emitted line; got:\n%s", buf.String())
	}

	e := parseLastEntry(t, buf)
	if e.Timings == nil {
		t.Errorf("timings must be non-nil; got nil")
	}
	if len(e.Timings) != 0 {
		t.Errorf("timings must be empty when no phases ran; got %v", e.Timings)
	}
}

func TestMiddleware_TimingsSumEqualsUpstreamResponseTime(t *testing.T) {
	_, buf, mw := newMiddlewareTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tm := TimingsFromContext(r.Context())
		tm.Record("s3-get", 25*time.Millisecond)
		tm.Record("resize", 50*time.Millisecond)
		tm.Record("s3-put", 25*time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	}), "bucket")

	mw.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/k.jpg", nil))

	e := parseLastEntry(t, buf)
	var sum float64
	for _, v := range e.Timings {
		sum += v
	}
	const epsilon = 1e-9
	diff := e.Upstream.ResponseTime - sum
	if diff < 0 {
		diff = -diff
	}
	if diff > epsilon {
		t.Errorf("upstream.responseTime (%v) != sum(timings) (%v); diff = %v", e.Upstream.ResponseTime, sum, diff)
	}
}

func TestMiddleware_TimingsRespectsRound3(t *testing.T) {
	// 1234567 ns = 0.001234567 s → round3 = 0.001 s
	_, buf, mw := newMiddlewareTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		TimingsFromContext(r.Context()).Record("s3-get", 1234567*time.Nanosecond)
		w.WriteHeader(http.StatusOK)
	}), "bucket")

	mw.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/k.jpg", nil))

	e := parseLastEntry(t, buf)
	if got, want := e.Timings["s3-get"], 0.001; got != want {
		t.Errorf("timings.s3-get = %v, want %v (round3 of 0.001234567 s)", got, want)
	}
}

// --- one log line per request ---

func TestMiddleware_ExactlyOneLogLinePerRequest(t *testing.T) {
	_, buf, mw := newMiddlewareTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}), "bucket")

	for i := 0; i < 3; i++ {
		mw.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/foo.jpg", nil))
	}
	if got := bytes.Count(buf.Bytes(), []byte("\n")); got != 3 {
		t.Errorf("expected 3 log lines, got %d:\n%s", got, buf.String())
	}
}

// --- benchmark ---

func BenchmarkMiddleware_NoopHandler(b *testing.B) {
	var buf bytes.Buffer
	lg := NewLogger(&buf)
	mw := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), lg, "bucket")

	req := httptest.NewRequest("GET", "/foo.jpg", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		buf.Reset()
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func keysOfBool(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
