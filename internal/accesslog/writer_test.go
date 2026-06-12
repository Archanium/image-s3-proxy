package accesslog

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResponseRecorder_DefaultStatusIs200(t *testing.T) {
	rec := httptest.NewRecorder()
	rr := newResponseRecorder(rec, NewTimings())

	if got := rr.Status(); got != http.StatusOK {
		t.Errorf("default Status = %d, want %d", got, http.StatusOK)
	}
}

func TestResponseRecorder_ImplicitWriteHeaderOnWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	rr := newResponseRecorder(rec, NewTimings())

	if _, err := rr.Write([]byte("hello")); err != nil {
		t.Fatalf("Write returned %v", err)
	}

	if got := rr.Status(); got != http.StatusOK {
		t.Errorf("Status after implicit WriteHeader = %d, want %d", got, http.StatusOK)
	}
	if got := rec.Code; got != http.StatusOK {
		t.Errorf("underlying recorder code = %d, want %d", got, http.StatusOK)
	}
}

func TestResponseRecorder_ExplicitWriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	rr := newResponseRecorder(rec, NewTimings())

	rr.WriteHeader(http.StatusNotFound)
	if got, want := rr.Status(), http.StatusNotFound; got != want {
		t.Errorf("Status = %d, want %d", got, want)
	}
	if got, want := rec.Code, http.StatusNotFound; got != want {
		t.Errorf("underlying recorder code = %d, want %d", got, want)
	}
}

func TestResponseRecorder_BytesAccumulate(t *testing.T) {
	rec := httptest.NewRecorder()
	rr := newResponseRecorder(rec, NewTimings())

	rr.WriteHeader(http.StatusOK)
	_, _ = rr.Write([]byte("hello "))
	_, _ = rr.Write([]byte("world"))

	if got, want := rr.BytesWritten(), int64(len("hello world")); got != want {
		t.Errorf("BytesWritten = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), "hello world"; got != want {
		t.Errorf("underlying body = %q, want %q", got, want)
	}
}

func TestResponseRecorder_ServerTimingSetOnFirstWriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	tm := NewTimings()
	tm.Record("s3-get", 12*time.Millisecond)
	tm.Record("resize", 8*time.Millisecond)
	rr := newResponseRecorder(rec, tm)

	rr.WriteHeader(http.StatusOK)

	got := rec.Header().Get("Server-Timing")
	want := "s3-get;dur=12.0, resize;dur=8.0"
	if got != want {
		t.Errorf("Server-Timing = %q, want %q", got, want)
	}
}

func TestResponseRecorder_ServerTimingAbsentWhenNoPhases(t *testing.T) {
	rec := httptest.NewRecorder()
	rr := newResponseRecorder(rec, NewTimings())

	rr.WriteHeader(http.StatusOK)

	if got := rec.Header().Get("Server-Timing"); got != "" {
		t.Errorf("Server-Timing on empty Timings = %q, want empty", got)
	}
}

func TestResponseRecorder_PhasesRecordedAfterWriteAreNotInHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	tm := NewTimings()
	tm.Record("s3-get", 5*time.Millisecond)
	rr := newResponseRecorder(rec, tm)

	rr.WriteHeader(http.StatusOK)
	// Record additional phase AFTER the header has been written. The header
	// must reflect only the pre-write set; the post-write phase is still
	// captured in the Timings (for the access log line) but not in the
	// header.
	tm.Record("s3-put", 7*time.Millisecond)

	if got, want := rec.Header().Get("Server-Timing"), "s3-get;dur=5.0"; got != want {
		t.Errorf("Server-Timing after post-write Record = %q, want %q", got, want)
	}
	if d := tm.Phases()["s3-put"]; d != 7*time.Millisecond {
		t.Errorf("post-write phase not recorded in Timings: got %v", d)
	}
}

func TestResponseRecorder_DuplicateWriteHeaderIgnored(t *testing.T) {
	rec := httptest.NewRecorder()
	rr := newResponseRecorder(rec, NewTimings())

	rr.WriteHeader(http.StatusOK)
	rr.WriteHeader(http.StatusTeapot) // ignored — first wins

	if got, want := rr.Status(), http.StatusOK; got != want {
		t.Errorf("Status after duplicate WriteHeader = %d, want %d", got, want)
	}
}

func TestResponseRecorder_NilTimingsSafe(t *testing.T) {
	rec := httptest.NewRecorder()
	rr := newResponseRecorder(rec, nil)

	rr.WriteHeader(http.StatusOK) // must not panic
	if got := rec.Header().Get("Server-Timing"); got != "" {
		t.Errorf("Server-Timing with nil Timings = %q, want empty", got)
	}
}
