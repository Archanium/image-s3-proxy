package accesslog

import (
	"encoding/json"
	"io"
	"log"
	"time"
)

// Entry is the JSON shape emitted per request. Field order on the wire is
// fixed by the struct field declaration order; nested key order is
// similarly fixed by the embedded struct declarations below.
//
// Timings is a sparse map of phase name to wall-clock duration in seconds
// (3-decimal precision), mirroring the per-phase data also surfaced in the
// Server-Timing response header. The map is always non-nil (even when no
// phases ran) so log-parsing queries that test for the field's presence
// see it on every line. Phase keys are emitted in alphabetical order by
// encoding/json's documented map behaviour.
type Entry struct {
	Timestamp string             `json:"@timestamp"`
	Extra     EntryExtra         `json:"extra"`
	User      EntryUser          `json:"user"`
	Request   EntryRequest       `json:"request"`
	Response  EntryResponse      `json:"response"`
	Upstream  EntryUpstream      `json:"upstream"`
	Timings   map[string]float64 `json:"timings"`
}

// EntryExtra carries the per-request correlation identifier.
type EntryExtra struct {
	CorrelationID string `json:"correlationId"`
}

// EntryUser carries client-attributed metadata. The five fields below match
// the platform's nginx access-log schema; no cookie is read.
type EntryUser struct {
	IP         string `json:"ip"`
	Cloudflare string `json:"cloudflare"`
	Name       string `json:"name"`
	Referrer   string `json:"referrer"`
	Agent      string `json:"agent"`
}

// EntryRequest carries the inbound request shape and total wall time.
type EntryRequest struct {
	Time        float64 `json:"time"`
	URL         string  `json:"url"`
	Method      string  `json:"method"`
	Scheme      string  `json:"scheme"`
	Size        int64   `json:"size"`
	Host        string  `json:"host"`
	Query       string  `json:"query"`
	ContentType string  `json:"contentType"`
}

// EntryResponse carries the response shape. RoutingGroup is preserved for
// schema compatibility with the platform's nginx logs; this service does
// not populate it.
type EntryResponse struct {
	Status       int    `json:"status"`
	Bytes        int64  `json:"bytes"`
	RoutingGroup string `json:"routing_group"`
}

// EntryUpstream is repurposed for this Go origin: ResponseTime is the sum of
// recorded phase durations (S3 + libvips) in seconds; UpstreamHost names
// the S3 endpoint or bucket. Version and Preloading are kept as empty
// strings so the schema matches the platform's nginx logs.
type EntryUpstream struct {
	ResponseTime float64 `json:"responseTime"`
	UpstreamHost string  `json:"upstreamHost"`
	Version      string  `json:"version"`
	Preloading   string  `json:"preloading"`
}

// Logger emits a single-line JSON access log entry per request. The
// underlying log.Logger is configured with no prefix and no flags so the
// emitted line is exactly the JSON object followed by '\n'.
type Logger struct {
	l *log.Logger
}

// NewLogger constructs a Logger writing to out.
func NewLogger(out io.Writer) *Logger {
	return &Logger{l: log.New(out, "", 0)}
}

// Emit marshals entry to JSON and prints it as a single line. A marshal
// failure (which should be impossible for the typed Entry) is reported via
// the package-default log and the entry is dropped — we never want a log
// emission to fail the request.
func (lg *Logger) Emit(entry *Entry) {
	b, err := json.Marshal(entry)
	if err != nil {
		log.Printf("accesslog: failed to marshal entry: %v", err)
		return
	}
	lg.l.Println(string(b))
}

// FormatTimestamp returns the @timestamp value used in entries: RFC 3339
// with nanosecond precision, in UTC.
func FormatTimestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
