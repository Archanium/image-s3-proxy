package accesslog

import "context"

type ctxKey struct{}

// WithTimings returns a copy of ctx that carries t. The middleware installs
// the per-request Timings this way before calling the inner handler.
func WithTimings(ctx context.Context, t *Timings) context.Context {
	return context.WithValue(ctx, ctxKey{}, t)
}

// TimingsFromContext returns the *Timings stored in ctx, or a fresh empty
// Timings if none is present. The result is never nil: handlers may call
// Record or Track without a nil check even when the middleware is not
// installed (e.g. in unit tests that bypass it).
func TimingsFromContext(ctx context.Context) *Timings {
	if ctx == nil {
		return NewTimings()
	}
	if t, ok := ctx.Value(ctxKey{}).(*Timings); ok && t != nil {
		return t
	}
	return NewTimings()
}
