package accesslog

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLogger_EmitProducesOneJSONLine(t *testing.T) {
	var buf bytes.Buffer
	lg := NewLogger(&buf)

	lg.Emit(&Entry{
		Timestamp: "2026-06-12T12:00:00Z",
		Extra:     EntryExtra{CorrelationID: "abc"},
		User:      EntryUser{IP: "1.2.3.4", Agent: "ua"},
		Request:   EntryRequest{Time: 0.123, URL: "/foo.jpg", Method: "GET", Scheme: "https", Host: "img.example"},
		Response:  EntryResponse{Status: 200, Bytes: 5},
		Upstream:  EntryUpstream{ResponseTime: 0.05, UpstreamHost: "bucket.s3"},
	})

	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("emitted line is not newline-terminated: %q", out)
	}
	if strings.Count(out, "\n") != 1 {
		t.Errorf("expected exactly one line, got %d: %q", strings.Count(out, "\n"), out)
	}

	var back Entry
	if err := json.Unmarshal([]byte(strings.TrimRight(out, "\n")), &back); err != nil {
		t.Fatalf("emitted line is not valid JSON: %v\nline: %s", err, out)
	}
	if back.Extra.CorrelationID != "abc" {
		t.Errorf("round-trip correlationId = %q, want abc", back.Extra.CorrelationID)
	}
	if back.Request.Time != 0.123 {
		t.Errorf("round-trip request.time = %v, want 0.123", back.Request.Time)
	}
}

func TestLogger_TopLevelKeyOrder(t *testing.T) {
	var buf bytes.Buffer
	lg := NewLogger(&buf)

	lg.Emit(&Entry{
		Timestamp: "t",
		Extra:     EntryExtra{CorrelationID: "c"},
		User:      EntryUser{},
		Request:   EntryRequest{},
		Response:  EntryResponse{},
		Upstream:  EntryUpstream{},
	})

	line := strings.TrimRight(buf.String(), "\n")
	// Confirm the top-level keys appear in the documented order. This is a
	// structural promise that monitoring queries depend on.
	wantOrder := []string{"@timestamp", "extra", "user", "request", "response", "upstream"}
	prev := -1
	for _, key := range wantOrder {
		needle := `"` + key + `":`
		idx := strings.Index(line, needle)
		if idx < 0 {
			t.Fatalf("top-level key %q missing from emitted line: %s", key, line)
		}
		if idx <= prev {
			t.Errorf("top-level key %q out of order in emitted line:\n  %s", key, line)
		}
		prev = idx
	}
}

func TestLogger_UserBlockHasExactlyFiveKeys(t *testing.T) {
	var buf bytes.Buffer
	lg := NewLogger(&buf)
	lg.Emit(&Entry{})

	var generic map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &generic); err != nil {
		t.Fatalf("parse: %v", err)
	}
	var userObj map[string]json.RawMessage
	if err := json.Unmarshal(generic["user"], &userObj); err != nil {
		t.Fatalf("parse user: %v", err)
	}

	wantKeys := map[string]bool{
		"ip": true, "cloudflare": true, "name": true, "referrer": true, "agent": true,
	}
	if len(userObj) != len(wantKeys) {
		t.Errorf("user block has %d keys, want %d (keys=%v)", len(userObj), len(wantKeys), keys(userObj))
	}
	for k := range userObj {
		if !wantKeys[k] {
			t.Errorf("unexpected user key %q (cart must not appear)", k)
		}
	}
}

func TestFormatTimestamp_RFC3339NanoUTC(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	in := time.Date(2026, 6, 12, 8, 0, 0, 123456789, loc)
	got := FormatTimestamp(in)
	// New York is UTC-4 in June (DST); 08:00 EDT = 12:00 UTC.
	want := "2026-06-12T12:00:00.123456789Z"
	if got != want {
		t.Errorf("FormatTimestamp = %q, want %q", got, want)
	}
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
