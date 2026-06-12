// Package accesslog implements per-request structured access logging and
// Server-Timing header emission for the image proxy.
//
// The package exposes three pieces wired together by Middleware:
//
//   - Timings — a per-request phase timer accumulator (Record, Track).
//   - Logger  — a single-line JSON emitter over an io.Writer.
//   - Middleware — an http.Handler wrapper that builds a *Timings, threads it
//     through context, captures status + bytes via a responseRecorder, and
//     emits one JSON log line per request when the inner handler returns.
//
// Handlers that want to contribute a phase timing call
// accesslog.TimingsFromContext(r.Context()).Track("phase-name", fn) — when
// the middleware is not installed, the helper returns a no-op Timings so
// handlers never need a nil check.
package accesslog

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// knownPhaseOrder controls the order phases appear in the Server-Timing
// header. Phases not listed here are appended after, sorted alphabetically.
var knownPhaseOrder = []string{"s3-exists", "s3-get", "resize", "s3-put"}

// Timings accumulates phase durations for a single request. Methods are safe
// for concurrent use, though in this codebase a single request is handled by
// one goroutine.
type Timings struct {
	mu     sync.Mutex
	phases map[string]time.Duration
}

// NewTimings returns an empty Timings ready for use.
func NewTimings() *Timings {
	return &Timings{phases: make(map[string]time.Duration)}
}

// Record adds d to the named phase. Multiple records to the same phase
// accumulate. A nil receiver is a no-op so handlers can call Record without
// a nil check when the middleware is not installed.
func (t *Timings) Record(phase string, d time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.phases[phase] += d
}

// Track times fn under the named phase and returns fn's error. The phase is
// recorded whether fn succeeds or fails.
func (t *Timings) Track(phase string, fn func() error) error {
	start := time.Now()
	err := fn()
	t.Record(phase, time.Since(start))
	return err
}

// Total returns the sum of all recorded phase durations.
func (t *Timings) Total() time.Duration {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var sum time.Duration
	for _, d := range t.phases {
		sum += d
	}
	return sum
}

// Phases returns a snapshot copy of the recorded phases. Mutating the
// returned map does not affect the receiver.
func (t *Timings) Phases() map[string]time.Duration {
	if t == nil {
		return map[string]time.Duration{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]time.Duration, len(t.phases))
	for k, v := range t.phases {
		out[k] = v
	}
	return out
}

// ServerTimingHeader returns the formatted Server-Timing header value, e.g.
//
//	"s3-exists;dur=2.1, s3-get;dur=14.7, resize;dur=63.2, s3-put;dur=9.8"
//
// Durations are emitted in milliseconds with one decimal. Known phases
// (s3-exists, s3-get, resize, s3-put) appear first in fixed order; any
// other phases follow in alphabetical order. Returns empty string when no
// phases have been recorded.
func (t *Timings) ServerTimingHeader() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.phases) == 0 {
		return ""
	}

	parts := make([]string, 0, len(t.phases))
	seen := make(map[string]bool, len(t.phases))
	for _, name := range knownPhaseOrder {
		if d, ok := t.phases[name]; ok {
			parts = append(parts, formatPhase(name, d))
			seen[name] = true
		}
	}
	extra := make([]string, 0)
	for name := range t.phases {
		if !seen[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	for _, name := range extra {
		parts = append(parts, formatPhase(name, t.phases[name]))
	}
	return strings.Join(parts, ", ")
}

func formatPhase(name string, d time.Duration) string {
	return fmt.Sprintf("%s;dur=%.1f", name, float64(d.Microseconds())/1000.0)
}
