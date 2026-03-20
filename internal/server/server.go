package server

import (
	"context"
	"encoding/json"
	"image-proxy/internal/types"
	"image-proxy/internal/worker"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var (
	// Regex 1: Resize request for products/blocks
	resizeRegex = regexp.MustCompile(`^/?(?P<clientId>\d{1,3})(-(?P<group>[\w]+))?/((?P<version>\d{1})?/?)(?P<images>images/)?(?P<folder>products|blocks)/(?P<width>\d{1,4}[.\d]{0,2})/(?P<height>\d{1,4}[.\d]{0,2})/(?P<path>[\w\.\-]+)$`)
	// Regex 2: File request
	fileRegex = regexp.MustCompile(`^/?(?P<clientId>\d{1,3})(-(?P<group>[\w]+))?/files/(?P<fileId>\d{1,3})/(?P<path>[\w\.\-]+)$`)
	// Regex 3: Simple image request (often with format change)
	folderImageRegex = regexp.MustCompile(`^/?(?P<clientId>\d{1,3})(-(?P<group>[\w]+))?/((?P<version>\d{1})?/?)(?P<images>images/)(?P<folder>[^/]+)/(?P<path>[\w\.\-]+)$`)
)

type Server struct {
	s3Client types.S3Client
	resizer  types.Resizer
	tags     map[string]string
	worker   *worker.Worker
}

func NewServer(s3Client types.S3Client, resizer types.Resizer, tags map[string]string, sizes [][]int, format string) *Server {
	return &Server{
		s3Client: s3Client,
		resizer:  resizer,
		tags:     tags,
		worker:   worker.NewWorker(s3Client, resizer, tags, sizes, format),
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
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	// 1. Check if the key exists in S3
	exists, err := s.s3Client.Exists(ctx, key)
	if err != nil {
		log.Printf("Error checking S3 existence for %s: %v", key, err)
	}

	if exists {
		log.Printf("Key %s found in S3, serving directly", key)
		// Serve from S3
		data, contentType, err := s.s3Client.Get(ctx, key)
		if err == nil {
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Cache-Control", "max-age=31536000")
			w.Write(data)
			return
		}
		log.Printf("Error getting from S3 %s: %v", key, err)
	}

	// 2. If not found, try to match patterns for resizing
	if match := getNamedGroups(resizeRegex, key); match != nil {
		log.Printf("Key %s matched resize regex", key)
		normalizedKey := s.getNormalizedKey(match, 1)
		if normalizedKey != key {
			log.Printf("Normalized key: %s", normalizedKey)
			// Check if the normalized key exists in S3
			exists, err := s.s3Client.Exists(ctx, normalizedKey)
			if err == nil && exists {
				log.Printf("Normalized key %s found in S3, serving", normalizedKey)
				data, contentType, err := s.s3Client.Get(ctx, normalizedKey)
				if err == nil {
					w.Header().Set("Content-Type", contentType)
					w.Header().Set("Cache-Control", "max-age=31536000")
					w.Write(data)
					return
				}
			}
		}
		s.handleResize(w, ctx, normalizedKey, match, 1)
		return
	}

	if match := getNamedGroups(fileRegex, key); match != nil {
		log.Printf("Key %s matched file regex", key)
		s.handleFile(w, ctx, key, match)
		return
	}

	if match := getNamedGroups(folderImageRegex, key); match != nil {
		log.Printf("Key %s matched folder image regex", key)
		normalizedKey := s.getNormalizedKey(match, 3)
		if normalizedKey != key {
			log.Printf("Normalized key: %s", normalizedKey)
			// Check if the normalized key exists in S3
			exists, err := s.s3Client.Exists(ctx, normalizedKey)
			if err == nil && exists {
				log.Printf("Normalized key %s found in S3, serving", normalizedKey)
				data, contentType, err := s.s3Client.Get(ctx, normalizedKey)
				if err == nil {
					w.Header().Set("Content-Type", contentType)
					w.Header().Set("Cache-Control", "max-age=31536000")
					w.Write(data)
					return
				}
			}
		}
		s.handleResize(w, ctx, normalizedKey, match, 3)
		return
	}

	log.Printf("Key %s did not match any pattern", key)
	// 3. Not found and no pattern matches
	http.Error(w, "Not Found", http.StatusNotFound)
}

func (s *Server) handleResize(w http.ResponseWriter, ctx context.Context, key string, groups map[string]string, regexType int) {
	var originalKey string
	var opts types.ImageOptions

	path := groups["path"]
	folder := groups["folder"]
	versionStr := groups["version"]
	clientId := groups["clientId"]
	version := 1
	if versionStr != "" {
		version, _ = strconv.Atoi(versionStr)
	}

	var format string
	// Replicate popping logic: the last extension is the requested format, the rest is the path to the original
	pathParts := strings.Split(path, ".")
	if len(pathParts) >= 2 {
		format = pathParts[len(pathParts)-1]
		pathParts = pathParts[:len(pathParts)-1]
		path = strings.Join(pathParts, ".")
	}

	originalKey = clientId + "/catalog/" + folder + "/images/" + path
	log.Printf("Calculated originalKey: %s", originalKey)
	opts.Version = version
	opts.Format = format

	if regexType == 1 {
		wVal, _ := strconv.Atoi(groups["width"])
		hVal, _ := strconv.Atoi(groups["height"])
		if wVal == 0 && hVal == 0 {
			wVal = 2560
			hVal = 0
		}
		opts.Width = wVal
		opts.Height = hVal
		opts.Fit = "contain" // Default for Regex 1 in Node.js
	} else if regexType == 3 {
		opts.Width = 2560
		opts.Height = 0
		opts.Fit = "inside"
	}

	// Fetch original from S3
	data, _, err := s.s3Client.Get(ctx, originalKey)
	if err != nil {
		// If not found, try alternative mapping: clientId + "/" + "images/" + folder + "/" + path
		altKey := clientId + "/images/" + folder + "/" + path
		log.Printf("Original not found at %s, trying alternative mapping: %s", originalKey, altKey)
		data, _, err = s.s3Client.Get(ctx, altKey)
	}

	if err != nil {
		log.Printf("Original not found: %s", originalKey)
		http.Error(w, "Original not found", http.StatusNotFound)
		return
	}

	// Resize
	resizedData, contentType, err := s.resizer.Resize(data, opts)
	if err != nil {
		log.Printf("Resize error: %v", err)
		http.Error(w, "Resize error", http.StatusInternalServerError)
		return
	}

	// Save back to S3
	err = s.s3Client.Put(ctx, key, resizedData, contentType, s.tags)
	if err != nil {
		log.Printf("S3 Save error: %v", err)
	}

	// Serve
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "max-age=31536000")
	w.Write(resizedData)
}

func (s *Server) handleFile(w http.ResponseWriter, ctx context.Context, key string, groups map[string]string) {
	fileId := groups["fileId"]
	path := groups["path"]
	clientId := groups["clientId"]
	originalKey := clientId + "/files/" + fileId + "/" + path

	data, contentType, err := s.s3Client.Get(ctx, originalKey)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Save back to S3 (caching)
	err = s.s3Client.Put(ctx, key, data, contentType, s.tags)
	if err != nil {
		log.Printf("S3 Save error: %v", err)
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "max-age=31536000")
	w.Write(data)
}

func (s *Server) handleWorkerTrigger(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if payload.Key == "" {
		http.Error(w, "Missing key in payload", http.StatusBadRequest)
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

	var sb strings.Builder
	sb.WriteString(clientId)
	if group != "" {
		sb.WriteString("-")
		sb.WriteString(group)
	}
	sb.WriteString("/")
	// Skip version
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
