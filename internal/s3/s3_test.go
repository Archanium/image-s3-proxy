package s3

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func TestMain(m *testing.M) {
	if os.Getenv("DEBUG") != "true" {
		log.SetOutput(io.Discard)
	}
	os.Exit(m.Run())
}

type mockS3API struct {
	headFunc func(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	getFunc  func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	putFunc  func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

func (m *mockS3API) HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	return m.headFunc(ctx, params, optFns...)
}
func (m *mockS3API) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	return m.getFunc(ctx, params, optFns...)
}
func (m *mockS3API) PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	return m.putFunc(ctx, params, optFns...)
}

func TestFallback(t *testing.T) {
	ctx := context.Background()

	var putCalled bool
	// Mock primary S3 — string-rendered NotFound (HOS-style)
	primaryMock := &mockS3API{
		headFunc: func(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
			return nil, errors.New("NotFound: Not found")
		},
		getFunc: func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			return nil, errors.New("NotFound: Not found")
		},
		putFunc: func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			putCalled = true
			if params.Tagging != nil {
				t.Errorf("Tagging must not be set on PutObject (HOS/R2 don't support it); got %v", *params.Tagging)
			}
			return nil, nil
		},
	}
	primary := &Client{client: primaryMock, bucket: "primary"}

	// Mock fallback S3
	fallbackMock := &mockS3API{
		headFunc: func(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
			return &s3.HeadObjectOutput{}, nil
		},
		getFunc: func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{
				Body: io.NopCloser(bytes.NewReader([]byte("fallback-data"))),
			}, nil
		},
	}
	fallback := &Client{client: fallbackMock, bucket: "fallback"}

	primary.SetFallback(fallback)

	// Test Exists
	exists, err := primary.Exists(ctx, "test-key")
	if err != nil {
		t.Errorf("Unexpected error in Exists: %v", err)
	}
	if !exists {
		t.Error("Expected Exists to be true (via fallback)")
	}

	// Test Get
	data, _, err := primary.Get(ctx, "test-key")
	if err != nil {
		t.Errorf("Unexpected error in Get: %v", err)
	}
	if string(data) != "fallback-data" {
		t.Errorf("Expected fallback-data, got %s", string(data))
	}

	if !putCalled {
		t.Error("Expected PutObject to be called on fallback Get")
	}
}

func TestFallbackWithStrippedKey(t *testing.T) {
	ctx := context.Background()

	var putCalled bool
	var putKey string
	// Mock primary S3: not found
	primaryMock := &mockS3API{
		headFunc: func(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
			return nil, errors.New("NotFound: Not found")
		},
		getFunc: func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			return nil, errors.New("NotFound: Not found")
		},
		putFunc: func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			putCalled = true
			if params.Key != nil {
				putKey = *params.Key
			}
			return nil, nil
		},
	}
	primary := &Client{client: primaryMock, bucket: "primary"}

	// Mock fallback S3: not found with original key, found with stripped key
	fallbackMock := &mockS3API{
		headFunc: func(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
			if *params.Key == "123/catalog/img.jpg" {
				return nil, errors.New("NotFound: Not found")
			}
			if *params.Key == "catalog/img.jpg" {
				return &s3.HeadObjectOutput{}, nil
			}
			return nil, errors.New("NotFound: Not found")
		},
		getFunc: func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			if *params.Key == "123/catalog/img.jpg" {
				return nil, errors.New("NotFound: Not found")
			}
			if *params.Key == "catalog/img.jpg" {
				return &s3.GetObjectOutput{
					Body: io.NopCloser(bytes.NewReader([]byte("stripped-data"))),
				}, nil
			}
			return nil, errors.New("NotFound: Not found")
		},
	}
	fallback := &Client{client: fallbackMock, bucket: "fallback"}

	primary.SetFallback(fallback)

	// Test Exists
	exists, err := primary.Exists(ctx, "123/catalog/img.jpg")
	if err != nil {
		t.Errorf("Unexpected error in Exists: %v", err)
	}
	if !exists {
		t.Error("Expected Exists to be true (via fallback with stripped key)")
	}

	// Test Get
	data, _, err := primary.Get(ctx, "123/catalog/img.jpg")
	if err != nil {
		t.Errorf("Unexpected error in Get: %v", err)
	}
	if string(data) != "stripped-data" {
		t.Errorf("Expected stripped-data, got %s", string(data))
	}

	if !putCalled {
		t.Error("Expected PutObject to be called on fallback Get")
	}
	if putKey != "123/catalog/img.jpg" {
		t.Errorf("Expected PutObject key to be 123/catalog/img.jpg, got %s", putKey)
	}
}

func TestFallbackWithGroupPrefix(t *testing.T) {
	ctx := context.Background()

	var putKey string
	primaryMock := &mockS3API{
		headFunc: func(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
			return nil, errors.New("NotFound: Not found")
		},
		getFunc: func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			return nil, errors.New("NotFound: Not found")
		},
		putFunc: func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			if params.Key != nil {
				putKey = *params.Key
			}
			return nil, nil
		},
	}
	primary := &Client{client: primaryMock, bucket: "primary"}

	fallbackMock := &mockS3API{
		getFunc: func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			if *params.Key == "catalog/img.jpg" {
				return &s3.GetObjectOutput{
					Body: io.NopCloser(bytes.NewReader([]byte("data"))),
				}, nil
			}
			return nil, errors.New("NotFound: Not found")
		},
	}
	fallback := &Client{client: fallbackMock, bucket: "fallback"}
	primary.SetFallback(fallback)

	_, _, err := primary.Get(ctx, "123-group/catalog/img.jpg")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if putKey != "123-group/catalog/img.jpg" {
		t.Errorf("Expected PutObject key to be 123-group/catalog/img.jpg, got %s", putKey)
	}
}

// --- Typed-error classification (track split-origin-and-cache-buckets) ---

func TestExists_TypedNoSuchKey(t *testing.T) {
	ctx := context.Background()
	primary := &Client{
		client: &mockS3API{
			headFunc: func(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
				return nil, &s3types.NoSuchKey{}
			},
		},
		bucket: "primary",
	}
	exists, err := primary.Exists(ctx, "missing")
	if err != nil {
		t.Errorf("typed NoSuchKey should not propagate as error from Exists; got %v", err)
	}
	if exists {
		t.Errorf("Exists on typed NoSuchKey should be false")
	}
}

func TestExists_TypedNotFound(t *testing.T) {
	ctx := context.Background()
	primary := &Client{
		client: &mockS3API{
			headFunc: func(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
				return nil, &s3types.NotFound{}
			},
		},
		bucket: "primary",
	}
	exists, err := primary.Exists(ctx, "missing")
	if err != nil {
		t.Errorf("typed NotFound should not propagate as error from Exists; got %v", err)
	}
	if exists {
		t.Errorf("Exists on typed NotFound should be false")
	}
}

func TestExists_NonNotFoundErrorPropagates(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("503 Service Unavailable")
	primary := &Client{
		client: &mockS3API{
			headFunc: func(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
				return nil, wantErr
			},
		},
		bucket: "primary",
	}
	exists, err := primary.Exists(ctx, "key")
	if err != wantErr {
		t.Errorf("expected the 503 error to propagate from Exists, got %v", err)
	}
	if exists {
		t.Errorf("Exists on transient error should be false")
	}
}

func TestGet_TypedNoSuchKey_NoFallback(t *testing.T) {
	ctx := context.Background()
	primary := &Client{
		client: &mockS3API{
			getFunc: func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
				return nil, &s3types.NoSuchKey{}
			},
		},
		bucket: "primary",
	}
	data, contentType, err := primary.Get(ctx, "missing")
	if err == nil {
		t.Errorf("Get without fallback should propagate the not-found error")
	}
	if data != nil || contentType != "" {
		t.Errorf("Get on miss should return zero values; got data=%v contentType=%q", data, contentType)
	}
}

func TestGet_TypedNoSuchKey_WithFallback(t *testing.T) {
	ctx := context.Background()
	primary := &Client{
		client: &mockS3API{
			getFunc: func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
				return nil, &s3types.NoSuchKey{}
			},
			putFunc: func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
				return nil, nil
			},
		},
		bucket: "primary",
	}
	primary.SetFallback(&Client{
		client: &mockS3API{
			getFunc: func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
				return &s3.GetObjectOutput{
					Body: io.NopCloser(bytes.NewReader([]byte("from-fallback"))),
				}, nil
			},
		},
		bucket: "fallback",
	})

	data, _, err := primary.Get(ctx, "test-key")
	if err != nil {
		t.Fatalf("Get should succeed via fallback; got %v", err)
	}
	if string(data) != "from-fallback" {
		t.Errorf("expected from-fallback body; got %q", string(data))
	}
}

func TestPut_NoTaggingHeader(t *testing.T) {
	ctx := context.Background()
	var sawTagging bool
	primary := &Client{
		client: &mockS3API{
			putFunc: func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
				if params.Tagging != nil {
					sawTagging = true
				}
				return nil, nil
			},
		},
		bucket: "primary",
	}

	if err := primary.Put(ctx, "key", []byte("body"), "image/jpeg"); err != nil {
		t.Fatalf("Put returned %v", err)
	}
	if sawTagging {
		t.Errorf("PutObject was called with Tagging set; expected nil after Tagging removal")
	}
}
