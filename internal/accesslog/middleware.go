package accesslog

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// Middleware wraps next so each request emits exactly one structured access
// log line to logger and, when phases have been recorded, a Server-Timing
// response header.
//
// upstreamHost is logged as upstream.upstreamHost on every entry — set it
// to the S3 endpoint URL or bucket name; it is informational only.
//
// The X-Request-ID response header is set to the resolved correlationId
// before the inner handler runs so it appears on the wire even if the
// handler writes the body before the middleware finishes.
func Middleware(next http.Handler, logger *Logger, upstreamHost string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		correlationID := resolveCorrelationID(r)
		w.Header().Set("X-Request-ID", correlationID)

		t := NewTimings()
		ctx := WithTimings(r.Context(), t)
		rr := newResponseRecorder(w, t)
		next.ServeHTTP(rr, r.WithContext(ctx))

		entry := &Entry{
			Timestamp: FormatTimestamp(start),
			Extra:     EntryExtra{CorrelationID: correlationID},
			User: EntryUser{
				IP:         clientIP(r),
				Cloudflare: r.Header.Get("CF-Connecting-IP"),
				Name:       basicAuthUser(r),
				Referrer:   r.Header.Get("Referer"),
				Agent:      r.Header.Get("User-Agent"),
			},
			Request: EntryRequest{
				Time:        round3(time.Since(start).Seconds()),
				URL:         r.URL.Path,
				Method:      r.Method,
				Scheme:      requestScheme(r),
				Size:        contentLength(r),
				Host:        r.Host,
				Query:       r.URL.RawQuery,
				ContentType: r.Header.Get("Content-Type"),
			},
			Response: EntryResponse{
				Status:       rr.Status(),
				Bytes:        rr.BytesWritten(),
				RoutingGroup: "",
			},
			Upstream: EntryUpstream{
				ResponseTime: round3(t.Total().Seconds()),
				UpstreamHost: upstreamHost,
				Version:      "",
				Preloading:   "",
			},
		}
		logger.Emit(entry)
	})
}

// resolveCorrelationID picks the per-request correlation identifier in
// priority order: X-Request-ID > CF-Ray > freshly generated random hex.
func resolveCorrelationID(r *http.Request) string {
	if v := r.Header.Get("X-Request-ID"); v != "" {
		return v
	}
	if v := r.Header.Get("CF-Ray"); v != "" {
		return v
	}
	return newRandomID()
}

// newRandomID returns a 32-character lowercase hex string sourced from
// crypto/rand. Fail-closed: if the system entropy source returns an
// error (rare; mostly seen in unusual container configurations), fall
// back to a timestamp-derived value so we never emit an empty
// correlationId on the wire.
func newRandomID() string {
	var b [16]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b[:])
}

// clientIP returns the best-effort original client IP. Prefers the first
// entry of X-Forwarded-For when present (set by Cloudflare / load
// balancer); otherwise strips the port from RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if i := strings.LastIndexByte(r.RemoteAddr, ':'); i > 0 {
		return r.RemoteAddr[:i]
	}
	return r.RemoteAddr
}

// basicAuthUser returns the username portion of the Authorization header
// when it carries a Basic credential. The password is never read.
func basicAuthUser(r *http.Request) string {
	if u, _, ok := r.BasicAuth(); ok {
		return u
	}
	return ""
}

// requestScheme returns "https" when the request arrived over TLS or when
// X-Forwarded-Proto identifies the original scheme as https.
func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return "https"
	}
	return "http"
}

// contentLength returns the request body size in bytes, or 0 when not
// reported. r.ContentLength can be -1 (unknown) on chunked-transfer-encoded
// requests — coerce to 0 so the field is always a non-negative integer.
func contentLength(r *http.Request) int64 {
	if r.ContentLength < 0 {
		return 0
	}
	return r.ContentLength
}

func round3(f float64) float64 {
	return math.Round(f*1000) / 1000
}
