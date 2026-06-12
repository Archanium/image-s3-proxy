package accesslog

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTimings_RecordAccumulates(t *testing.T) {
	tm := NewTimings()
	tm.Record("s3-get", 5*time.Millisecond)
	tm.Record("s3-get", 7*time.Millisecond)
	tm.Record("resize", 10*time.Millisecond)

	phases := tm.Phases()
	if got, want := phases["s3-get"], 12*time.Millisecond; got != want {
		t.Errorf("s3-get total = %v, want %v", got, want)
	}
	if got, want := phases["resize"], 10*time.Millisecond; got != want {
		t.Errorf("resize total = %v, want %v", got, want)
	}
}

func TestTimings_TrackReturnsError(t *testing.T) {
	tm := NewTimings()
	wantErr := errors.New("boom")

	got := tm.Track("s3-get", func() error {
		time.Sleep(2 * time.Millisecond)
		return wantErr
	})

	if got != wantErr {
		t.Errorf("Track returned %v, want %v", got, wantErr)
	}
	if d := tm.Phases()["s3-get"]; d < 1*time.Millisecond {
		t.Errorf("Track did not record duration; got %v", d)
	}
}

func TestTimings_TrackRecordsOnNilError(t *testing.T) {
	tm := NewTimings()
	err := tm.Track("resize", func() error {
		time.Sleep(1 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Errorf("Track returned %v, want nil", err)
	}
	if _, ok := tm.Phases()["resize"]; !ok {
		t.Errorf("Track did not record phase after success")
	}
}

func TestTimings_TotalSums(t *testing.T) {
	tm := NewTimings()
	tm.Record("s3-get", 5*time.Millisecond)
	tm.Record("resize", 10*time.Millisecond)
	tm.Record("s3-put", 3*time.Millisecond)

	if got, want := tm.Total(), 18*time.Millisecond; got != want {
		t.Errorf("Total = %v, want %v", got, want)
	}
}

func TestTimings_ServerTimingHeader_EmptyWhenNoPhases(t *testing.T) {
	tm := NewTimings()
	if got := tm.ServerTimingHeader(); got != "" {
		t.Errorf("ServerTimingHeader on empty Timings = %q, want \"\"", got)
	}
}

func TestTimings_ServerTimingHeader_KnownOrder(t *testing.T) {
	tm := NewTimings()
	// Record out of order to confirm the header reorders to the canonical order.
	tm.Record("s3-put", 9800*time.Microsecond)
	tm.Record("resize", 63200*time.Microsecond)
	tm.Record("s3-exists", 2100*time.Microsecond)
	tm.Record("s3-get", 14700*time.Microsecond)

	got := tm.ServerTimingHeader()
	want := "s3-exists;dur=2.1, s3-get;dur=14.7, resize;dur=63.2, s3-put;dur=9.8"
	if got != want {
		t.Errorf("ServerTimingHeader =\n  %q\n  want %q", got, want)
	}
}

func TestTimings_ServerTimingHeader_UnknownPhasesAppendedAlphabetically(t *testing.T) {
	tm := NewTimings()
	tm.Record("s3-get", 10*time.Millisecond)
	tm.Record("zeta", 5*time.Millisecond)
	tm.Record("alpha", 2*time.Millisecond)

	got := tm.ServerTimingHeader()
	// s3-get is a known phase → first. alpha and zeta are unknown → after, sorted.
	if !strings.HasPrefix(got, "s3-get;") {
		t.Errorf("expected s3-get first, got %q", got)
	}
	if !strings.Contains(got, "alpha;dur=2.0") {
		t.Errorf("expected alpha;dur=2.0 present, got %q", got)
	}
	if !strings.Contains(got, "zeta;dur=5.0") {
		t.Errorf("expected zeta;dur=5.0 present, got %q", got)
	}
	if strings.Index(got, "alpha") > strings.Index(got, "zeta") {
		t.Errorf("expected alpha before zeta, got %q", got)
	}
}

func TestTimings_PhasesReturnsCopy(t *testing.T) {
	tm := NewTimings()
	tm.Record("s3-get", 5*time.Millisecond)

	snapshot := tm.Phases()
	snapshot["s3-get"] = 99 * time.Millisecond
	snapshot["new-phase"] = 1 * time.Millisecond

	if got, want := tm.Phases()["s3-get"], 5*time.Millisecond; got != want {
		t.Errorf("mutating snapshot leaked into Timings: got %v, want %v", got, want)
	}
	if _, ok := tm.Phases()["new-phase"]; ok {
		t.Errorf("mutating snapshot leaked a new key into Timings")
	}
}

func TestTimings_NilReceiverIsNoOp(t *testing.T) {
	var tm *Timings
	tm.Record("s3-get", 5*time.Millisecond) // must not panic
	if got := tm.Total(); got != 0 {
		t.Errorf("nil.Total() = %v, want 0", got)
	}
	if got := tm.ServerTimingHeader(); got != "" {
		t.Errorf("nil.ServerTimingHeader() = %q, want \"\"", got)
	}
	if got := tm.Phases(); len(got) != 0 {
		t.Errorf("nil.Phases() = %v, want empty", got)
	}
}

func TestTimings_ConcurrentRecord(t *testing.T) {
	tm := NewTimings()
	var wg sync.WaitGroup
	const goroutines = 50
	const recordsPerGoroutine = 200
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < recordsPerGoroutine; j++ {
				tm.Record("s3-get", 1*time.Microsecond)
			}
		}()
	}
	wg.Wait()

	want := time.Duration(goroutines*recordsPerGoroutine) * time.Microsecond
	if got := tm.Phases()["s3-get"]; got != want {
		t.Errorf("concurrent Record total = %v, want %v", got, want)
	}
}
