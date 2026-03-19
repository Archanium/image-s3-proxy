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
	"strings"
)

func main() {
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

	ctx := context.Background()
	s3Client, err := s3.NewClient(ctx, bucket, region, accessKey, secretKey, endpoint)
	if err != nil {
		log.Fatalf("Failed to initialize S3 client: %v", err)
	}

	imgResizer := resizer.NewResizer()
	imgResizer.Startup()
	defer imgResizer.Shutdown()

	srv := server.NewServer(s3Client, imgResizer, tags, sizes, format)

	log.Printf("Starting image proxy on port %s", port)
	if err := http.ListenAndServe(":"+port, srv); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
