package s3

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

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
	// Mock primary S3
	primaryMock := &mockS3API{
		headFunc: func(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
			return nil, errors.New("NotFound: Not found")
		},
		getFunc: func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			return nil, errors.New("NotFound: Not found")
		},
		putFunc: func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			putCalled = true
			if params.Tagging == nil || *params.Tagging != "Migrated=true" {
				t.Errorf("Expected tagging Migrated=true, got %v", params.Tagging)
			}
			return nil, nil
		},
	}
	primary := &Client{client: primaryMock, bucket: "primary"}
	primary.SetDefaultTags(map[string]string{"Migrated": "true"})

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
