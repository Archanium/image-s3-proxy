package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image-proxy/internal/accesslog"
	"image-proxy/internal/types"
	"image-proxy/internal/worker"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

var (
	// Regex 1: Resize request for products/blocks
	resizeRegex = regexp.MustCompile(`^/?(?P<clientId>\d{1,3})(-(?P<group>[\w]+))?/((?P<version>\d{1})?/?)(?P<images>images/)?(?P<folder>products|blocks|branding)/(?P<width>\d{1,4}[.\d]{0,2})/(?P<height>\d{1,4}[.\d]{0,2})/(?P<path>[\w\.\-]+)$`)
	// Regex 2: File request
	fileRegex = regexp.MustCompile(`^/?(?P<clientId>\d{1,3})(-(?P<group>[\w]+))?/files/(?P<fileId>\d{1,3})/(?P<path>[\w\.\-]+)$`)
	// Regex 3: Simple image request (often with format change)
	folderImageRegex = regexp.MustCompile(`^/?(?P<clientId>\d{1,3})(-(?P<group>[\w]+))?/((?P<version>\d{1})?/?)(?P<images>images/)(?P<folder>[^/]+)/(?P<path>[\w\.\-]+)$`)
)

// CacheMode controls how the proxy uses its origin and cache S3 clients.
//
//   - CacheModeOff: cache client is ignored. All reads and writes go to the
//     origin client. This is the no-op default and preserves single-client
//     behavior.
//   - CacheModeShadow: cache is being populated. Default reads come from
//     the origin client; writes are dual-written (origin first, then cache).
//   - CacheModeLive: cache is primary. Default reads come from the cache
//     client; writes are dual-written (cache first, then origin).
type CacheMode int

const (
	CacheModeOff CacheMode = iota
	CacheModeShadow
	CacheModeLive
)

// ParseCacheMode accepts case-insensitive "off", "shadow", "live" or the
// empty string (which maps to off). Any other value returns an error.
func ParseCacheMode(s string) (CacheMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "off":
		return CacheModeOff, nil
	case "shadow":
		return CacheModeShadow, nil
	case "live":
		return CacheModeLive, nil
	default:
		return CacheModeOff, fmt.Errorf("invalid CACHE_MODE %q (expected off|shadow|live)", s)
	}
}

func (m CacheMode) String() string {
	switch m {
	case CacheModeShadow:
		return "shadow"
	case CacheModeLive:
		return "live"
	default:
		return "off"
	}
}

// readHeaderUseCache is the per-request override for the cache-hit read
// source. "true" forces a read from the cache client; "false" forces a read
// from the origin client. Any other value (or absence) defers to the
// configured CacheMode.
const readHeaderUseCache = "X-Use-Cache"

type Server struct {
	originClient types.S3Client
	cacheClient  types.S3Client
	resizer      types.Resizer
	worker       *worker.Worker
	mode         CacheMode
}

// NewServer constructs a Server in CacheModeOff. Both client roles are
// served by the same instance — this is the backwards-compatible
// single-client constructor used by tests and by main.go when CACHE_MODE
// is unset.
func NewServer(s3Client types.S3Client, resizer types.Resizer, sizes [][]int, format string) *Server {
	return NewServerWithMode(s3Client, s3Client, CacheModeOff, resizer, sizes, format)
}

// NewServerWithMode constructs a Server with explicit origin and cache
// clients plus a mode. originClient is where the upstream catalog system
// writes; cacheClient is where the proxy writes resized variants. The
// worker is constructed with both clients so its bulk pre-resize path
// dual-writes when the mode dictates it.
func NewServerWithMode(originClient, cacheClient types.S3Client, mode CacheMode, resizer types.Resizer, sizes [][]int, format string) *Server {
	return &Server{
		originClient: originClient,
		cacheClient:  cacheClient,
		resizer:      resizer,
		worker:       worker.NewWorker(originClient, cacheClient, resizer, sizes, format, false),
		mode:         mode,
	}
}

// effectiveReadClient returns the S3Client to use for the cache-hit Get on
// this request. The default comes from the CacheMode (origin in off /
// shadow, cache in live). An X-Use-Cache header on the request can flip
// the choice for a single request.
func (s *Server) effectiveReadClient(r *http.Request) types.S3Client {
	if s.mode == CacheModeOff {
		return s.originClient
	}
	useCache := s.mode == CacheModeLive
	switch strings.ToLower(r.Header.Get(readHeaderUseCache)) {
	case "true":
		useCache = true
	case "false":
		useCache = false
	}
	if useCache {
		return s.cacheClient
	}
	return s.originClient
}

// putBoth writes data under key to the proxy's write targets, respecting
// the CacheMode:
//   - off: a single timed "s3-put" against the cache client (which is the
//     same as the origin in off mode), preserving the historical phase name.
//   - shadow: origin first as "s3-put-origin", then cache as "s3-put-cache".
//   - live: cache first as "s3-put-cache", then origin as "s3-put-origin".
//
// Failures on either side are logged but do not stop the second write or
// fail the caller's request. This matches the existing "Put failure does
// not break the response" contract.
func (s *Server) putBoth(ctx context.Context, key string, data []byte, contentType string) {
	if s.mode == CacheModeOff {
		if err := s.time(ctx, "s3-put", func() error {
			return s.cacheClient.Put(ctx, key, data, contentType)
		}); err != nil {
			log.Printf("S3 Save error: %v", err)
		}
		return
	}

	type writeTarget struct {
		side   string // "origin" or "cache"
		phase  string
		client types.S3Client
	}
	var order []writeTarget
	switch s.mode {
	case CacheModeShadow:
		order = []writeTarget{
			{"origin", "s3-put-origin", s.originClient},
			{"cache", "s3-put-cache", s.cacheClient},
		}
	case CacheModeLive:
		order = []writeTarget{
			{"cache", "s3-put-cache", s.cacheClient},
			{"origin", "s3-put-origin", s.originClient},
		}
	}
	for _, wt := range order {
		wt := wt
		if err := s.time(ctx, wt.phase, func() error {
			return wt.client.Put(ctx, key, data, contentType)
		}); err != nil {
			log.Printf("dual-write %s failed for %s: %v", wt.side, key, err)
		}
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == "/_/worker/trigger" {
		s.handleWorkerTrigger(w, r)
		return
	}

	key := strings.TrimPrefix(r.URL.Path, "/")
	ctx := r.Context()

	log.Printf("Received request for key: %s", key)

	// 0. Ensure there is at least one extension in the filename
	lastSlash := strings.LastIndex(key, "/")
	filename := key
	if lastSlash != -1 {
		filename = key[lastSlash+1:]
	}
	if !strings.Contains(filename, ".") {
		log.Printf("Key %s does not contain an extension in filename %s", key, filename)
		s.httpError(w, "Not Found", http.StatusNotFound)
		return
	}

	// 1. Speculative GET on the effective read client (cache or origin,
	// depending on mode + X-Use-Cache header). A NoSuchKey / NotFound is
	// treated as a clean miss; any other error is logged and we fall
	// through (fail-open, matching the historical Exists-then-fall-through
	// behavior — but now explicitly classified, not silently misclassified).
	readClient := s.effectiveReadClient(r)
	var data []byte
	var contentType string
	getErr := s.time(ctx, "s3-get", func() error {
		var e error
		data, contentType, e = readClient.Get(ctx, key)
		return e
	})
	if getErr == nil {
		log.Printf("Key %s found in cache layer, serving directly", key)
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "max-age=31536000")
		w.Write(data)
		return
	}
	if !isNotFoundErr(getErr) {
		log.Printf("cache client error for %s: %v", key, getErr)
	}

	// 2. If not found, try to match patterns for resizing
	if match := getNamedGroups(resizeRegex, key); match != nil {
		log.Printf("Key %s matched resize regex", key)
		normalizedKey := s.getNormalizedKey(match, 1)
		if normalizedKey != key {
			log.Printf("Normalized key: %s", normalizedKey)
		}
		s.handleResize(w, r, normalizedKey, match, 1)
		return
	}

	if match := getNamedGroups(fileRegex, key); match != nil {
		log.Printf("Key %s matched file regex", key)
		s.handleFile(w, r, key, match)
		return
	}

	if match := getNamedGroups(folderImageRegex, key); match != nil {
		log.Printf("Key %s matched folder image regex", key)
		normalizedKey := s.getNormalizedKey(match, 3)
		if normalizedKey != key {
			log.Printf("Normalized key: %s", normalizedKey)
			// Re-check the normalized key with the effective read client.
			var nData []byte
			var nContentType string
			nErr := s.time(ctx, "s3-get", func() error {
				var e error
				nData, nContentType, e = readClient.Get(ctx, normalizedKey)
				return e
			})
			if nErr == nil {
				log.Printf("Normalized key %s found in cache layer, serving", normalizedKey)
				w.Header().Set("Content-Type", nContentType)
				w.Header().Set("Cache-Control", "max-age=31536000")
				w.Write(nData)
				return
			}
			if !isNotFoundErr(nErr) {
				log.Printf("cache client error for normalized key %s: %v", normalizedKey, nErr)
			}
		}
		s.handleResize(w, r, normalizedKey, match, 3)
		return
	}

	log.Printf("Key %s did not match any pattern", key)
	// 3. Not found and no pattern matches
	s.httpError(w, "Not Found", http.StatusNotFound)
}

func (s *Server) handleResize(w http.ResponseWriter, r *http.Request, key string, groups map[string]string, regexType int) {
	ctx := r.Context()
	var originalKey string
	var opts types.ImageOptions

	path := groups["path"]
	folder := groups["folder"]
	versionStr := groups["version"]
	clientId := groups["clientId"]
	version := 1
	if regexType == 3 {
		version = 0
	}
	if versionStr != "" {
		version, _ = strconv.Atoi(versionStr)
	}

	var format string
	// Always the last extension is the format
	pathParts := strings.Split(path, ".")
	if len(pathParts) >= 2 {
		format = pathParts[len(pathParts)-1]
		// Only strip the extension if it named like ".png.webp" or ".jpg.avif" (3 or more parts)
		if len(pathParts) > 2 {
			pathParts = pathParts[:len(pathParts)-1]
			path = strings.Join(pathParts, ".")
		}
	}

	originalKey = clientId + "/catalog/" + folder + "/images/" + path
	log.Printf("Calculated originalKey: %s", originalKey)
	opts.Version = version
	opts.Format = format

	if regexType == 1 {
		wVal, _ := strconv.Atoi(groups["width"])
		hVal, _ := strconv.Atoi(groups["height"])
		if wVal == 0 && hVal == 0 {
			wVal = 5120
			hVal = 0
		}
		opts.Width = wVal
		opts.Height = hVal
		if opts.Version == 1 {
			opts.Fit = "cover"
		} else {
			opts.Fit = "contain" // Default for Regex 1 in Node.js (Version 2/3)
		}
	} else if regexType == 3 {
		opts.Width = 5120
		opts.Height = 0
		opts.Fit = "inside"
	}

	// Fetch original from S3 (origin client only — originals live where
	// the upstream catalog system writes them).
	var data []byte
	var err error

	// Try multiple possible locations for the original image
	var keysToTry []string
	seen := make(map[string]bool)
	addKey := func(k string) {
		if k != "" && !seen[k] {
			keysToTry = append(keysToTry, k)
			seen[k] = true
		}
	}

	addKey(originalKey)
	if format != "" {
		addKey(originalKey + "." + format)
	}
	altKey := clientId + "/images/" + folder + "/" + path
	addKey(altKey)
	if format != "" {
		addKey(altKey + "." + format)
	}

	// Fallback: if path has an extension, try common ones
	if lastDot := strings.LastIndex(path, "."); lastDot > 0 {
		basePath := path[:lastDot]
		for _, ext := range []string{"jpg", "jpeg", "png", "webp", "gif", "avif"} {
			addKey(clientId + "/catalog/" + folder + "/images/" + basePath + "." + ext)
			addKey(clientId + "/images/" + folder + "/" + basePath + "." + ext)
		}
		addKey(clientId + "/catalog/" + folder + "/images/" + basePath)
		addKey(clientId + "/images/" + folder + "/" + basePath)
	}

	for _, k := range keysToTry {
		k := k
		err = s.time(ctx, "s3-get", func() error {
			var e error
			data, _, e = s.originClient.Get(ctx, k)
			return e
		})
		if err == nil {
			break
		}
	}

	if err != nil {
		log.Printf("Original not found after trying keys: %v", keysToTry)
		s.httpError(w, "Original not found", http.StatusNotFound)
		return
	}

	// Resize
	var resizedData []byte
	var contentType string
	err = s.time(ctx, "resize", func() error {
		var e error
		resizedData, contentType, e = s.resizer.Resize(data, opts)
		return e
	})
	if err != nil {
		log.Printf("Resize error: %v", err)
		s.httpError(w, "Resize error", http.StatusInternalServerError)
		return
	}

	// Save back via the dual-write helper. The mode dictates target(s)
	// and ordering; failure is logged but does not fail the request.
	s.putBoth(ctx, key, resizedData, contentType)

	// Serve
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "max-age=31536000")
	w.Write(resizedData)
}

func (s *Server) httpError(w http.ResponseWriter, error string, code int) {
	w.Header().Set("Cache-Control", "max-age=30")
	http.Error(w, error, code)
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request, key string, groups map[string]string) {
	ctx := r.Context()
	fileId := groups["fileId"]
	path := groups["path"]
	clientId := groups["clientId"]
	originalKey := clientId + "/files/" + fileId + "/" + path

	var data []byte
	var contentType string
	err := s.time(ctx, "s3-get", func() error {
		var e error
		data, contentType, e = s.originClient.Get(ctx, originalKey)
		return e
	})
	if err != nil {
		s.httpError(w, "File not found", http.StatusNotFound)
		return
	}

	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Dual-write the cached copy.
	s.putBoth(ctx, key, data, contentType)

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "max-age=31536000")
	w.Write(data)
}

func (s *Server) handleWorkerTrigger(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		s.httpError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if payload.Key == "" {
		s.httpError(w, "Missing key in payload", http.StatusBadRequest)
		return
	}

	go func() {
		ctx := context.Background()
		if err := s.worker.ProcessS3Event(ctx, "", payload.Key); err != nil {
			log.Printf("Worker processing error for key %s: %v", payload.Key, err)
		}
	}()

	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte("Accepted"))
}

func (s *Server) getNormalizedKey(groups map[string]string, regexType int) string {
	clientId := groups["clientId"]
	group := groups["group"]
	images := groups["images"]
	folder := groups["folder"]
	path := groups["path"]
	version := groups["version"]
	if version == "" {
		if regexType == 3 {
			version = "0"
		} else {
			version = "1"
		}
	}

	var sb strings.Builder
	sb.WriteString(clientId)
	if group != "" {
		sb.WriteString("-")
		sb.WriteString(group)
	}
	sb.WriteString("/")
	sb.WriteString(version)
	sb.WriteString("/")
	if images != "" {
		sb.WriteString(images)
	}
	if regexType == 1 {
		sb.WriteString(folder)
		sb.WriteString("/")
		sb.WriteString(groups["width"])
		sb.WriteString("/")
		sb.WriteString(groups["height"])
		sb.WriteString("/")
	} else if regexType == 3 {
		sb.WriteString(folder)
		sb.WriteString("/")
	}
	sb.WriteString(path)
	return sb.String()
}

func getNamedGroups(re *regexp.Regexp, s string) map[string]string {
	match := re.FindStringSubmatch(s)
	if match == nil {
		return nil
	}
	result := make(map[string]string)
	for i, name := range re.SubexpNames() {
		if i != 0 && name != "" {
			result[name] = match[i]
		}
	}
	return result
}

// time wraps an S3 or resize call site, recording the elapsed time under
// phase in the *Timings carried by ctx. When the access-log middleware is
// not installed (e.g. in unit tests that construct Server directly), the
// underlying Timings is a discard no-op so the call site behaves
// identically except for the closure indirection.
func (s *Server) time(ctx context.Context, phase string, fn func() error) error {
	return accesslog.TimingsFromContext(ctx).Track(phase, fn)
}

// isNotFoundErr classifies an error as "object does not exist". Mirrors
// the s3 package's classifier (typed errors first, string fallback for
// S3-compatible providers).
func isNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	var nsk *s3types.NoSuchKey
	var nf *s3types.NotFound
	if errors.As(err, &nsk) || errors.As(err, &nf) {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "NoSuchKey") || strings.Contains(s, "NotFound")
}
