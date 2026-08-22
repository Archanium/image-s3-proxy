package main

import (
	"context"
	"encoding/json"
	"errors"
	"image-proxy/internal/accesslog"
	"image-proxy/internal/resizer"
	"image-proxy/internal/s3"
	"image-proxy/internal/server"
	"image-proxy/internal/urlsign"
	"log"
	"net/http"
	"os"
	"strconv"
)

func main() {
	debug := os.Getenv("DEBUG") == "true"

	bucket := os.Getenv("BUCKET")
	if bucket == "" {
		log.Fatal("BUCKET environment variable is required")
	}

	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if region == "" {
		region = "us-east-1"
	}

	accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
	endpoint := os.Getenv("S3_ENDPOINT")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// IMAGE_TAGS is deprecated — neither Hetzner Object Storage nor
	// Cloudflare R2 implement the S3 Tagging APIs. Keep the env-var read
	// for backwards compat but log a warning and discard the value.
	if tagStr := os.Getenv("IMAGE_TAGS"); tagStr != "" {
		log.Printf("IMAGE_TAGS is deprecated and ignored — HOS and R2 do not implement S3 Tagging APIs")
	}

	sizeStr := os.Getenv("SIZES")
	var sizes [][]int
	if sizeStr != "" {
		if err := json.Unmarshal([]byte(sizeStr), &sizes); err != nil {
			log.Printf("Warning: Failed to parse SIZES environment variable: %v. Using defaults.", err)
		}
	}

	format := os.Getenv("FORMAT")

	concurrency, _ := strconv.Atoi(os.Getenv("VIPS_CONCURRENCY"))
	maxCacheMem, _ := strconv.Atoi(os.Getenv("VIPS_MAX_CACHE_MEM"))
	maxCacheSize, _ := strconv.Atoi(os.Getenv("VIPS_MAX_CACHE_SIZE"))

	// CACHE_MODE selects the storage topology: off (single bucket, today's
	// behavior), shadow (cache populated via dual-write while reads still
	// come from origin), or live (cache is primary; origin is dual-written
	// as belt-and-suspenders).
	modeStr := os.Getenv("CACHE_MODE")
	mode, err := server.ParseCacheMode(modeStr)
	if err != nil {
		log.Fatalf("invalid CACHE_MODE: %v", err)
	}

	cacheBucket := os.Getenv("CACHE_BUCKET")
	if mode != server.CacheModeOff && cacheBucket == "" {
		log.Fatalf("CACHE_MODE=%s requires CACHE_BUCKET to be set", mode)
	}
	if mode == server.CacheModeOff && cacheBucket != "" {
		log.Printf("CACHE_BUCKET is set but CACHE_MODE is off — cache client will not be constructed")
	}

	ctx := context.Background()
	originClient, err := s3.NewClient(ctx, bucket, region, accessKey, secretKey, endpoint)
	if err != nil {
		log.Fatalf("Failed to initialize S3 client: %v", err)
	}

	// Optional fallback client for legacy-bucket migration on the origin
	// side. The cache client never carries a fallback (a cache miss is
	// just a fall-through to the resize pipeline).
	oldBucket := os.Getenv("OLD_S3_BUCKET")
	if oldBucket != "" {
		oldRegion := os.Getenv("OLD_S3_REGION")
		if oldRegion == "" {
			oldRegion = region
		}
		oldAccessKey := os.Getenv("OLD_S3_ACCESS_KEY_ID")
		oldSecretKey := os.Getenv("OLD_S3_SECRET_ACCESS_KEY")
		oldEndpoint := os.Getenv("OLD_S3_ENDPOINT")

		log.Printf("Initializing fallback S3 client for bucket: %s", oldBucket)
		fallbackClient, err := s3.NewClient(ctx, oldBucket, oldRegion, oldAccessKey, oldSecretKey, oldEndpoint)
		if err != nil {
			log.Printf("Warning: Failed to initialize fallback S3 client: %v", err)
		} else {
			originClient.SetFallback(fallbackClient)
		}
	}

	// Build the cache client when split mode is requested. When unset,
	// cacheClient == originClient and the server runs in single-client
	// (no-op) behavior even if the mode label is "off".
	cacheClient := originClient
	if mode != server.CacheModeOff {
		cacheRegion := os.Getenv("CACHE_AWS_REGION")
		if cacheRegion == "" {
			cacheRegion = region
		}
		cacheAccessKey := os.Getenv("CACHE_AWS_ACCESS_KEY_ID")
		cacheSecretKey := os.Getenv("CACHE_AWS_SECRET_ACCESS_KEY")
		cacheEndpoint := os.Getenv("CACHE_S3_ENDPOINT")

		log.Printf("Initializing cache S3 client for bucket: %s (mode=%s)", cacheBucket, mode)
		built, err := s3.NewClient(ctx, cacheBucket, cacheRegion, cacheAccessKey, cacheSecretKey, cacheEndpoint)
		if err != nil {
			log.Fatalf("Failed to initialize cache S3 client: %v", err)
		}
		cacheClient = built
	}

	imgResizer := resizer.NewResizer()
	imgResizer.Startup(debug, concurrency, maxCacheMem, maxCacheSize)
	defer imgResizer.Shutdown()

	srv := server.NewServerWithMode(originClient, cacheClient, mode, imgResizer, sizes, format)

	// Optional bearer-token auth on POST /_/worker/trigger. When the env
	// var is unset or empty, the trigger endpoint stays unauthenticated
	// (today's behavior). When set, every trigger request must carry
	// `Authorization: Bearer <token>` matching this value.
	if workerToken := os.Getenv("WORKER_AUTH_TOKEN"); workerToken != "" {
		srv.SetWorkerAuthToken(workerToken)
		log.Printf("worker-trigger auth: ENABLED")
	} else {
		log.Printf("worker-trigger auth: DISABLED (set WORKER_AUTH_TOKEN to enable)")
	}

	// Optional imgproxy-style processing-option route at /_p/. The route is
	// fail-closed: with no signing key and no explicit unsafe opt-in the
	// verifier stays nil and /_p/ paths 404 like any other unmatched path,
	// so this ships inert on every existing deployment.
	//
	// SIGNATURE_KEY and SIGNATURE_SALT are hex-encoded, matching imgproxy's
	// IMGPROXY_KEY / IMGPROXY_SALT convention, so an off-the-shelf imgproxy
	// signing SDK can generate URLs for this service unmodified.
	signatureSize, _ := strconv.Atoi(os.Getenv("SIGNATURE_SIZE"))
	allowUnsafeURLs := os.Getenv("ALLOW_UNSAFE_URLS") == "true"
	signer, err := urlsign.NewVerifier(
		os.Getenv("SIGNATURE_KEY"),
		os.Getenv("SIGNATURE_SALT"),
		signatureSize,
		allowUnsafeURLs,
	)
	switch {
	case errors.Is(err, urlsign.ErrNotConfigured):
		log.Printf("processing-option route /_p/: DISABLED (set SIGNATURE_KEY and SIGNATURE_SALT to enable)")
	case err != nil:
		log.Fatalf("Invalid URL signing configuration (SIGNATURE_KEY / SIGNATURE_SALT / SIGNATURE_SIZE): %v", err)
	default:
		srv.SetURLSigner(signer)
		log.Printf("processing-option route /_p/: ENABLED")
		if allowUnsafeURLs {
			log.Printf("WARNING: ALLOW_UNSAFE_URLS=true — /_p/ accepts the literal " +
				"\"unsafe\" signature, bypassing verification. Do not use this in production.")
		}
	}

	// MAX_DIMENSION bounds width, height and absolute crop dimensions on the
	// /_p/ route, enforced before any S3 read or libvips call.
	if maxDim := os.Getenv("MAX_DIMENSION"); maxDim != "" {
		n, convErr := strconv.Atoi(maxDim)
		if convErr != nil || n < 1 {
			log.Fatalf("MAX_DIMENSION must be a positive integer, got %q", maxDim)
		}
		srv.SetMaxDimension(n)
		log.Printf("processing-option route /_p/: max dimension %d", n)
	}

	// upstreamHost is logged on every access line. In split mode the
	// dominant write traffic lands on the cache bucket; report the cache
	// endpoint/bucket when configured. In single-client mode fall back
	// to the origin endpoint/bucket (matches pre-track behavior).
	upstreamHost := endpoint
	if mode != server.CacheModeOff {
		if ep := os.Getenv("CACHE_S3_ENDPOINT"); ep != "" {
			upstreamHost = ep
		} else {
			upstreamHost = cacheBucket
		}
	}
	if upstreamHost == "" {
		upstreamHost = bucket
	}
	accessLogger := accesslog.NewLogger(os.Stdout)
	handler := accesslog.Middleware(srv, accessLogger, upstreamHost)

	log.Printf("Starting image proxy on port %s (cache mode: %s)", port, mode)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
