package worker

import (
	"context"
	"fmt"
	"image-proxy/internal/types"
	"log"
	"path/filepath"
)

type Worker struct {
	s3Client       types.S3Client
	destS3Client   types.S3Client
	resizer        types.Resizer
	sizes          [][]int
	format         string
	forceOverwrite bool
}

var DefaultSizes = [][]int{
	{150, 210}, {200, 280}, {240, 0}, {240, 336}, {242, 339}, {340, 0}, {350, 490}, {384, 538},
	{415, 581}, {450, 630}, {461, 645}, {480, 0}, {615, 861}, {680, 0}, {700, 980}, {768, 0},
	{768, 1075}, {819, 0}, {819, 1147}, {900, 1260}, {920, 0}, {920, 1288}, {922, 1291}, {930, 1302},
	{1200, 1680}, {1230, 1722}, {1280, 0}, {1280, 1792}, {1536, 0}, {1600, 2240}, {1638, 0}, {1840, 0}, {2560, 0},
}

func NewWorker(s3Client types.S3Client, destS3Client types.S3Client, resizer types.Resizer, sizes [][]int, format string, forceOverwrite bool) *Worker {
	if len(sizes) == 0 {
		sizes = DefaultSizes
	}
	if format == "" {
		format = "avif"
	}
	return &Worker{
		s3Client:       s3Client,
		destS3Client:   destS3Client,
		resizer:        resizer,
		sizes:          sizes,
		format:         format,
		forceOverwrite: forceOverwrite,
	}
}

// BatchRequest is the work envelope dispatched to ProcessBatch. The fields
// mirror the trigger payload one-to-one after validation: the server layer
// is responsible for parsing JSON, applying defaults (Version=3 when
// absent, Sizes=nil to mean "fall back to the worker's env-configured
// sizes"), and validating preconditions (Formats non-empty with every
// entry in the resizer's supported set, ClientID non-empty, Images
// non-empty).
type BatchRequest struct {
	ClientID string
	Version  int
	Images   []string
	Sizes    [][]int  // empty or nil → use w.sizes
	Formats  []string // non-empty (precondition: validated upstream)
}

// ProcessBatch fans out a (images × sizes × formats) cartesian product
// against the worker's resizer + storage. Each image is fetched from the
// origin client once and resized once per (size, format) output.
//
// Failure model:
//   - Per-image Get failure: log + skip the image; remaining images still
//     run.
//   - Per-output Resize failure: log + skip the output; remaining outputs
//     for the same image still run.
//   - Per-output Put failure (origin or cache side): log + continue;
//     dual-write semantics from the split-bucket track are preserved
//     verbatim.
//
// Returns nil unconditionally — there is no batch-level fatal error.
// The trigger goroutine that calls this is fire-and-forget, so the
// return value is only consumed for logging.
func (w *Worker) ProcessBatch(ctx context.Context, req BatchRequest) error {
	effectiveSizes := req.Sizes
	if len(effectiveSizes) == 0 {
		effectiveSizes = w.sizes
	}

	log.Printf("Worker: batch start — clientId=%s version=%d images=%d sizes=%d formats=%d (total outputs=%d)",
		req.ClientID, req.Version, len(req.Images), len(effectiveSizes), len(req.Formats),
		len(req.Images)*len(effectiveSizes)*len(req.Formats))

	for _, origKey := range req.Images {
		log.Printf("Worker: processing image: %s", origKey)
		data, _, err := w.s3Client.Get(ctx, origKey)
		if err != nil {
			log.Printf("Worker: failed to get original %s: %v — skipping image", origKey, err)
			continue
		}
		origFilename := filepath.Base(origKey)

		for _, size := range effectiveSizes {
			width := size[0]
			height := size[1]

			for _, format := range req.Formats {
				w.processOutput(ctx, data, origFilename, req.ClientID, req.Version, width, height, format)
			}
		}
	}

	return nil
}

// processOutput is the per-(image, size, format) leaf: resize, build key,
// skip-existing check, dual-write. Failures are logged and absorbed —
// callers continue with the next output regardless.
func (w *Worker) processOutput(ctx context.Context, data []byte, origFilename, clientID string, version, width, height int, format string) {
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
		log.Printf("Worker: resize %s @ %dx%d.%s failed: %v", origFilename, width, height, format, err)
		return
	}

	thumbKey := fmt.Sprintf("%s/%d/images/products/%d/%d/%s.%s",
		clientID, version, width, height, origFilename, format)

	// Skip-existing check targets the cache client when split mode is
	// active (that's the bucket we're populating, and skipping when it
	// already has the variant is the correct pre-warm behavior). In
	// single-client mode destS3Client == s3Client and this is unchanged
	// from historical behavior.
	checkClient := w.s3Client
	if w.destS3Client != nil {
		checkClient = w.destS3Client
	}
	if !w.forceOverwrite {
		exists, err := checkClient.Exists(ctx, thumbKey)
		if err == nil && exists {
			log.Printf("Worker: %s already exists, skipping", thumbKey)
			return
		}
	}

	// Dual-write when origin and dest are distinct (split mode); otherwise
	// a single write. Per-side failures are logged but never abort.
	if err := w.s3Client.Put(ctx, thumbKey, resizedData, contentType); err != nil {
		log.Printf("Worker: failed to save %s to origin: %v", thumbKey, err)
	}
	if w.destS3Client != nil && w.destS3Client != w.s3Client {
		if err := w.destS3Client.Put(ctx, thumbKey, resizedData, contentType); err != nil {
			log.Printf("Worker: failed to save %s to cache: %v", thumbKey, err)
		}
	}
	log.Printf("Worker: saved %s", thumbKey)
}
