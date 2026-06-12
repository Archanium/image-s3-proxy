package accesslog

import "net/http"

// responseRecorder wraps an http.ResponseWriter to capture the status code
// and bytes written, and to set the Server-Timing header on the first call
// to WriteHeader (or the first implicit WriteHeader via Write) from the
// bound *Timings.
//
// If the bound Timings has no phases recorded at the time of the first
// header write, Server-Timing is not set; phases recorded afterwards still
// appear in the access log line but cannot be added to the header (the
// headers have already been sent).
type responseRecorder struct {
	http.ResponseWriter
	timings       *Timings
	status        int
	bytesWritten  int64
	headerWritten bool
}

func newResponseRecorder(w http.ResponseWriter, t *Timings) *responseRecorder {
	return &responseRecorder{
		ResponseWriter: w,
		timings:        t,
		status:         http.StatusOK,
	}
}

// WriteHeader captures the status, sets the Server-Timing header if any
// phases have been recorded, and forwards the call to the underlying
// ResponseWriter. Subsequent calls are ignored — Go's stdlib also rejects
// duplicate WriteHeader calls.
func (r *responseRecorder) WriteHeader(code int) {
	if r.headerWritten {
		return
	}
	if r.timings != nil {
		if h := r.timings.ServerTimingHeader(); h != "" {
			r.ResponseWriter.Header().Set("Server-Timing", h)
		}
	}
	r.status = code
	r.headerWritten = true
	r.ResponseWriter.WriteHeader(code)
}

// Write delegates to the underlying ResponseWriter and accumulates the
// total number of bytes written. If WriteHeader was never called, Write
// triggers an implicit WriteHeader(200) — matching the stdlib's contract.
func (r *responseRecorder) Write(b []byte) (int, error) {
	if !r.headerWritten {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytesWritten += int64(n)
	return n, err
}

// Status returns the status code that was (or will be) sent. Defaults to
// 200 OK if the handler never called WriteHeader explicitly.
func (r *responseRecorder) Status() int { return r.status }

// BytesWritten returns the total bytes written to the response body.
func (r *responseRecorder) BytesWritten() int64 { return r.bytesWritten }
