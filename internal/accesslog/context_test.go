package accesslog

import (
	"context"
	"testing"
	"time"
)

func TestWithTimings_RoundTrip(t *testing.T) {
	tm := NewTimings()
	tm.Record("s3-get", 7*time.Millisecond)

	ctx := WithTimings(context.Background(), tm)
	got := TimingsFromContext(ctx)
	if got != tm {
		t.Fatalf("TimingsFromContext returned a different value than was stored")
	}
	if d := got.Phases()["s3-get"]; d != 7*time.Millisecond {
		t.Errorf("round-tripped Timings lost state: got %v", d)
	}
}

func TestTimingsFromContext_AbsentReturnsUsableValue(t *testing.T) {
	got := TimingsFromContext(context.Background())
	if got == nil {
		t.Fatalf("TimingsFromContext on a bare context returned nil; must always return a usable value")
	}
	// Record should not panic and should be observable on the same instance.
	got.Record("s3-get", 1*time.Millisecond)
	if d := got.Phases()["s3-get"]; d != 1*time.Millisecond {
		t.Errorf("fallback Timings is not writable: got %v", d)
	}
}

func TestTimingsFromContext_NilContext(t *testing.T) {
	got := TimingsFromContext(nil) //nolint:staticcheck // exercising defensive nil handling
	if got == nil {
		t.Fatalf("TimingsFromContext(nil) returned nil")
	}
	got.Record("resize", 2*time.Millisecond) // must not panic
}

func TestTimingsFromContext_WrongValueType(t *testing.T) {
	// A context carrying a value at the same key shape but of a different
	// type should not crash; we fall back to a fresh Timings.
	ctx := context.WithValue(context.Background(), ctxKey{}, "not-a-timings")
	got := TimingsFromContext(ctx)
	if got == nil {
		t.Fatalf("TimingsFromContext with wrong-typed value returned nil")
	}
	got.Record("resize", 1*time.Millisecond)
}
