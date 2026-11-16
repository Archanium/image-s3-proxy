package server

import (
	"context"
	"log"
	"net/http"
	"time"
)

// defaultBackfillSlots bounds how many cache backfills may be in flight at
// once. A backfill holds the image bytes until the cache write finishes, so
// an unbounded number would let a burst of misses pile up memory. When every
// slot is taken the copy is skipped; the next miss for that key tries again.
const defaultBackfillSlots = 32

// backfillTimeout caps a single cache write made after the response.
const backfillTimeout = 30 * time.Second

// readCached is the "is this variant already rendered?" read used before
// resizing. It reads from the request's effective read client and, in live
// mode, falls back to the origin bucket when the cache does not answer.
//
// Live mode needs the fallback because variants rendered while the proxy
// ran in shadow or off mode were written only to the origin bucket. Without
// it every such variant would be rendered again from its original. A
// variant found in the origin after a clean cache miss is copied into the
// cache in the background, so the cache fills up with what is requested.
//
// The returned error follows the old single read: nil means serve the
// bytes, a not-found error means a clean miss (go on to resize), and any
// other error is a failed read the caller logs and falls through on.
func (s *Server) readCached(ctx context.Context, r *http.Request, key string) ([]byte, string, error) {
	readClient := s.effectiveReadClient(r)
	var data []byte
	var contentType string
	err := s.time(ctx, "s3-get", func() error {
		var e error
		data, contentType, e = readClient.Get(ctx, key)
		return e
	})
	if err == nil || !s.readsThroughToOrigin(readClient) {
		return data, contentType, err
	}

	cacheMiss := isNotFoundErr(err)
	if !cacheMiss {
		log.Printf("cache bucket error for %s, reading origin instead: %v", key, err)
	}

	originErr := s.time(ctx, "s3-get-origin", func() error {
		var e error
		data, contentType, e = s.originClient.Get(ctx, key)
		return e
	})
	if originErr != nil {
		return nil, "", originErr
	}
	if cacheMiss {
		s.backfill(key, data, contentType)
	}
	return data, contentType, nil
}

// readsThroughToOrigin reports whether a read from readClient should fall
// back to the origin: only when it was the cache client, and the cache is a
// separate bucket.
func (s *Server) readsThroughToOrigin(readClient interface{}) bool {
	return s.mode == CacheModeLive &&
		s.cacheClient != s.originClient &&
		readClient == s.cacheClient
}

// backfill copies a variant found in the origin into the cache after the
// response has been written. It never blocks the request.
func (s *Server) backfill(key string, data []byte, contentType string) {
	select {
	case s.backfillSlots <- struct{}{}:
	default:
		log.Printf("cache backfill skipped for %s: %d backfills already in flight", key, cap(s.backfillSlots))
		return
	}
	s.backfills.Add(1)
	go func() {
		defer s.backfills.Done()
		defer func() { <-s.backfillSlots }()
		ctx, cancel := context.WithTimeout(context.Background(), backfillTimeout)
		defer cancel()
		if err := s.cacheClient.Put(ctx, key, data, contentType); err != nil {
			log.Printf("cache backfill failed for %s: %v", key, err)
			return
		}
		log.Printf("cache backfill: copied %s from origin into the cache", key)
	}()
}

// waitBackfills blocks until every backfill started so far has finished.
// Used by tests.
func (s *Server) waitBackfills() {
	s.backfills.Wait()
}
