package main

import (
	"context"
	"encoding/json"
	"image-proxy/internal/resizer"
	"image-proxy/internal/s3"
	"image-proxy/internal/server"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
)

func main() {
	debug := os.Getenv("DEBUG") == "true"
	//if !debug {
	//	log.SetOutput(io.Discard)
	//}

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

	tagStr := os.Getenv("IMAGE_TAGS")
	tags := make(map[string]string)
	if tagStr != "" {
		pairs := strings.Split(tagStr, ",")
		for _, pair := range pairs {
			kv := strings.SplitN(pair, "=", 2)
			if len(kv) == 2 {
				tags[kv[0]] = kv[1]
			}
		}
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

	ctx := context.Background()
	s3Client, err := s3.NewClient(ctx, bucket, region, accessKey, secretKey, endpoint)
	if err != nil {
		log.Fatalf("Failed to initialize S3 client: %v", err)
	}
	s3Client.SetDefaultTags(tags)

	// Optional fallback client for migration
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
			s3Client.SetFallback(fallbackClient)
		}
	}

	imgResizer := resizer.NewResizer()
	imgResizer.Startup(debug, concurrency, maxCacheMem, maxCacheSize)
	defer imgResizer.Shutdown()

	srv := server.NewServer(s3Client, imgResizer, tags, sizes, format)

	log.Printf("Starting image proxy on port %s", port)
	if err := http.ListenAndServe(":"+port, srv); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
