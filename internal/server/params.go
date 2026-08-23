package server

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"image-proxy/internal/procopts"
	"image-proxy/internal/types"
)

// paramRoutePrefix is the reserved path prefix for the processing-option
// route. It cannot collide with the three legacy URL families, every one of
// which requires a numeric client id in the first segment.
const paramRoutePrefix = "_p/"

// paramCacheSegment separates the tenant prefix from the canonical option
// string in a cache key, so processed variants are trivially distinguishable
// from legacy ones when listing a bucket.
const paramCacheSegment = "_p"

// paramOutputFormats is the set of formats the resizer can encode. A request
// for anything else — a PDF under files/, say — is served as a passthrough
// rather than run through libvips, which would otherwise silently re-encode
// it as JPEG.
var paramOutputFormats = map[string]bool{
	"png":  true,
	"jpg":  true,
	"jpeg": true,
	"webp": true,
	"avif": true,
}

// handleParams serves the /_p/ processing-option route.
//
// Shape: /_p/{signature}/{option}/…/{source-tail}
//
// The order below is deliberate and load-bearing:
//
//  1. Verify the signature. Nothing else runs for an unsigned request, so an
//     attacker cannot reach the parser, S3 or libvips.
//  2. Parse and bound the options. The dimension cap is enforced here,
//     before any S3 read or libvips call, so an oversized request costs a
//     regex-free string parse and nothing more.
//  3. Resolve the source, short-circuiting passthrough requests before the
//     cache lookup — they are never cache-written, so looking would always
//     miss.
//  4. Only then read, resize and write.
func (s *Server) handleParams(w http.ResponseWriter, r *http.Request, key string) {
	ctx := r.Context()

	segments := splitSegments(strings.TrimPrefix(key, paramRoutePrefix))
	if len(segments) < 2 {
		log.Printf("param route: %s has too few segments", key)
		s.httpError(w, "Not Found", http.StatusNotFound)
		return
	}
	signature, tail := segments[0], segments[1:]

	// The signed payload deliberately excludes the /_p/{signature} prefix,
	// so it is exactly what an off-the-shelf imgproxy signer produces.
	if err := s.urlSigner.Verify(signature, "/"+strings.Join(tail, "/")); err != nil {
		log.Printf("param route: signature rejected for %s: %v", key, err)
		s.httpError(w, "Forbidden", http.StatusForbidden)
		return
	}

	opts, source, err := procopts.Parse(tail)
	if err != nil {
		log.Printf("param route: %s: %v", key, err)
		// A parse error that names no option means the URL has no source
		// tail at all. That is a shape problem, so it 404s alongside every
		// other unrecognised shape; only a genuinely bad option is a 400.
		var perr *procopts.Error
		if errors.As(err, &perr) && perr.Option == "" {
			s.httpError(w, "Not Found", http.StatusNotFound)
			return
		}
		s.httpError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := opts.Validate(s.maxDimension); err != nil {
		log.Printf("param route: %s: %v", key, err)
		s.httpError(w, err.Error(), http.StatusBadRequest)
		return
	}

	src, ok := parseSourceTail(source)
	if !ok {
		log.Printf("param route: %s has an unrecognised source tail %v", key, source)
		s.httpError(w, "Not Found", http.StatusNotFound)
		return
	}

	// A passthrough serves the origin bytes untouched. It is never cached:
	// the object already exists in the origin bucket under its own key, and
	// a second copy would buy nothing.
	passthrough := opts.Raw ||
		opts.SkipsFormat(src.format) ||
		!paramOutputFormats[strings.ToLower(src.format)]

	if !passthrough {
		cacheKey := paramCacheKey(src, opts.Canonical())
		var data []byte
		var contentType string
		getErr := s.time(ctx, "s3-get", func() error {
			var e error
			data, contentType, e = s.effectiveReadClient(r).Get(ctx, cacheKey)
			return e
		})
		if getErr == nil {
			log.Printf("param route: %s found in cache layer, serving directly", cacheKey)
			s.serveImage(w, data, contentType)
			return
		}
		if !isNotFoundErr(getErr) {
			log.Printf("cache client error for %s: %v", cacheKey, getErr)
		}
	}

	// Originals live where the upstream catalog system writes them, so this
	// is an origin-client read and inherits the fallback-bucket lazy
	// migration for free.
	var data []byte
	var contentType string
	var getErr error
	for _, k := range src.candidates {
		k := k
		getErr = s.time(ctx, "s3-get", func() error {
			var e error
			data, contentType, e = s.originClient.Get(ctx, k)
			return e
		})
		if getErr == nil {
			break
		}
	}
	if getErr != nil {
		log.Printf("param route: original not found after trying keys: %v", src.candidates)
		s.httpError(w, "Original not found", http.StatusNotFound)
		return
	}

	if passthrough {
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		s.serveImage(w, data, contentType)
		return
	}

	var resized []byte
	err = s.time(ctx, "resize", func() error {
		var e error
		resized, contentType, e = s.resizer.Resize(data, types.ImageOptions{
			Format:     src.format,
			Processing: &opts,
		})
		return e
	})
	if err != nil {
		log.Printf("param route: resize error for %s: %v", key, err)
		s.httpError(w, "Resize error", http.StatusInternalServerError)
		return
	}

	s.putBoth(ctx, paramCacheKey(src, opts.Canonical()), resized, contentType)
	s.serveImage(w, resized, contentType)
}

// serveImage writes a successful image response with the long-lived
// cache-control the whole service contracts on.
func (s *Server) serveImage(w http.ResponseWriter, data []byte, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "max-age=31536000")
	w.Write(data)
}

// paramSource is a resolved source tail: which tenant it belongs to, which
// origin keys to try, what to call the cached variant, and which format to
// encode.
type paramSource struct {
	tenant     string // clientId, optionally with a "-group" suffix
	rest       []string
	format     string
	candidates []string
}

// parseSourceTail resolves the segments after the processing options into
// origin candidate keys, using exactly the layouts the legacy routes use.
//
// Two shapes are recognised, mirroring the legacy families:
//
//	{clientId}[-{group}]/{folder}/{file}   — catalog image, via originCandidateKeys
//	{clientId}/files/{fileId}/{file}       — passthrough file, a literal key
//
// Anything else is rejected rather than guessed at, so a malformed tail 404s
// instead of probing the bucket with invented keys.
func parseSourceTail(source []string) (paramSource, bool) {
	var src paramSource
	if len(source) < 3 {
		return src, false
	}
	src.tenant = source[0]
	src.rest = source[1:]

	clientId := src.tenant
	if i := strings.Index(clientId, "-"); i > 0 {
		clientId = clientId[:i]
	}
	if clientId == "" {
		return src, false
	}

	name, format := splitOutputFormat(source[len(source)-1])
	if format == "" {
		// No extension means no output format, and the legacy routes
		// already treat an extensionless filename as not-found.
		return src, false
	}
	src.format = format

	if source[1] == "files" {
		if len(source) != 4 {
			return src, false
		}
		src.candidates = []string{strings.Join(source, "/")}
		return src, true
	}

	if len(source) != 3 {
		return src, false
	}
	src.candidates = originCandidateKeys(clientId, source[1], name, format)
	return src, true
}

// paramCacheKey builds the cache key for a processed variant:
//
//	{clientId}[-{group}]/_p/{canonical}/{rest-of-source-tail}
//
// The tenant stays in the first segment so bucket lifecycle rules and the
// fallback bucket's clientId-prefix handling keep working unchanged, and the
// _p segment keeps processed variants from ever colliding with a key a
// legacy URL could produce.
func paramCacheKey(src paramSource, canonical string) string {
	return src.tenant + "/" + paramCacheSegment + "/" + canonical + "/" + strings.Join(src.rest, "/")
}

// splitSegments splits a path into non-empty segments, so a doubled or
// trailing slash cannot produce an empty segment that would be mistaken for
// the start of the source tail.
func splitSegments(path string) []string {
	parts := strings.Split(path, "/")
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
