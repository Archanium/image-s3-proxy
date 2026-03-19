package worker

import (
	"context"
	"fmt"
	"image-proxy/internal/types"
	"log"
	"path/filepath"
	"strings"
)

type Worker struct {
	s3Client types.S3Client
	resizer  types.Resizer
	tags     map[string]string
}

func NewWorker(s3Client types.S3Client, resizer types.Resizer, tags map[string]string) *Worker {
	return &Worker{
		s3Client: s3Client,
		resizer:  resizer,
		tags:     tags,
	}
}

func (w *Worker) ProcessProductImage(ctx context.Context, origKey string) error {
	data, _, err := w.s3Client.Get(ctx, origKey)
	if err != nil {
		return fmt.Errorf("failed to get original image: %w", err)
	}

	sizes := [][]int{
		{150, 210}, {200, 280}, {240, 0}, {240, 336}, {242, 339}, {340, 0}, {350, 490}, {384, 538},
		{415, 581}, {450, 630}, {461, 645}, {480, 0}, {615, 861}, {680, 0}, {700, 980}, {768, 0},
		{768, 1075}, {819, 0}, {819, 1147}, {900, 1260}, {920, 0}, {920, 1288}, {922, 1291}, {930, 1302},
		{1200, 1680}, {1230, 1722}, {1280, 0}, {1280, 1792}, {1536, 0}, {1600, 2240}, {1638, 0}, {1840, 0}, {2560, 0},
	}

	folder := "products"
	format := "avif"
	version := 3

	origFilename := filepath.Base(origKey)

	for _, size := range sizes {
		width := size[0]
		height := size[1]

		opts := types.ImageOptions{
			Width:      width,
			Height:     height,
			Version:    version,
			Format:     format,
			Fit:        "contain",
			IsAnimated: true,
		}

		resizedData, contentType, err := w.resizer.Resize(data, opts)
		if err != nil {
			log.Printf("Failed to resize to %dx%d: %v", width, height, err)
			continue
		}

		thumbKey := fmt.Sprintf("13/%d/images/%s/%d/%d/%s.%s", version, folder, width, height, origFilename, format)

		err = w.s3Client.Put(ctx, thumbKey, resizedData, contentType, w.tags)
		if err != nil {
			log.Printf("Failed to save thumbnail %s: %v", thumbKey, err)
		} else {
			log.Printf("Successfully saved thumbnail %s", thumbKey)
		}
	}

	return nil
}

func (w *Worker) ProcessS3Event(ctx context.Context, bucket, key string) error {
	// Simple routing for events
	if strings.Contains(key, "catalog/products/images/") || strings.Contains(key, "originals/") {
		return w.ProcessProductImage(ctx, key)
	}
	return nil
}
