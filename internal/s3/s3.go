package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type s3API interface {
	HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

type Client struct {
	client         s3API
	bucket         string
	fallbackClient *Client
}

func NewClient(ctx context.Context, bucket, region, accessKey, secretKey, endpoint string) (*Client, error) {
	var cfg aws.Config
	var err error

	opts := []func(*config.LoadOptions) error{
		config.WithRegion(region),
	}

	if accessKey != "" && secretKey != "" {
		log.Printf("Using static credentials with AccessKey: %s", accessKey)
		opts = append(opts, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")))
	} else {
		log.Println("Using default credentials chain")
	}

	if endpoint != "" {
		log.Printf("Using custom S3 endpoint: %s", endpoint)
		resolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			return aws.Endpoint{
				URL:           endpoint,
				SigningRegion: region,
			}, nil
		})
		opts = append(opts, config.WithEndpointResolverWithOptions(resolver))
	}

	cfg, err = config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config: %w", err)
	}

	log.Printf("Initialized S3 client for bucket: %s, region: %s", bucket, region)

	return &Client{
		client: s3.NewFromConfig(cfg),
		bucket: bucket,
	}, nil
}

func (c *Client) SetFallback(fallback *Client) {
	c.fallbackClient = fallback
}

// isNotFound classifies an S3 error as "object does not exist".
//
// Strategy: prefer AWS SDK v2 typed errors (*types.NoSuchKey / *types.NotFound)
// which are the canonical signals on real AWS S3 endpoints. Some S3-compatible
// providers (Hetzner Object Storage among them) wrap the underlying response
// such that the typed error is not directly extractable; fall back to
// string-matching the rendered error text to preserve operational behavior
// against those providers.
func isNotFound(err error) bool {
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

func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	_, err := c.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			// Check fallback if configured
			if c.fallbackClient != nil {
				log.Printf("Not found in primary bucket %s, checking fallback bucket %s for existence", c.bucket, c.fallbackClient.bucket)
				exists, fallbackErr := c.fallbackClient.Exists(ctx, key)
				// If not found in fallback with original key, try stripping the first prefix (clientId)
				if fallbackErr == nil && !exists {
					if parts := strings.SplitN(key, "/", 2); len(parts) > 1 {
						log.Printf("Not found in fallback with original key, checking fallback bucket %s for stripped key: %s", c.fallbackClient.bucket, parts[1])
						return c.fallbackClient.Exists(ctx, parts[1])
					}
				}
				return exists, fallbackErr
			}
			return false, nil
		}
		// Non-not-found errors propagate to the caller so they can be
		// classified at the server layer (logged vs. silently dropped).
		return false, err
	}
	return true, nil
}

func (c *Client) Get(ctx context.Context, key string) ([]byte, string, error) {
	log.Printf("Fetching from S3: bucket=%s, key=%s", c.bucket, key)
	output, err := c.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) && c.fallbackClient != nil {
			log.Printf("Not found in primary bucket %s, trying fallback bucket %s", c.bucket, c.fallbackClient.bucket)
			data, contentType, fallbackErr := c.fallbackClient.Get(ctx, key)

			// If not found in fallback with original key, try stripping the first prefix (clientId)
			if isNotFound(fallbackErr) {
				if parts := strings.SplitN(key, "/", 2); len(parts) > 1 {
					log.Printf("Not found in fallback bucket %s with original key, trying stripped key: %s", c.fallbackClient.bucket, parts[1])
					data, contentType, fallbackErr = c.fallbackClient.Get(ctx, parts[1])
				}
			}

			if fallbackErr == nil {
				log.Printf("Found in fallback bucket, moving to primary bucket: %s", key)
				// Save it back to the primary bucket for future requests.
				// Failure here is logged but not propagated — the client
				// still gets the bytes we already have.
				if putErr := c.Put(ctx, key, data, contentType); putErr != nil {
					log.Printf("Warning: Failed to move object to primary bucket %s: %v", c.bucket, putErr)
				}
				return data, contentType, nil
			}
			return nil, "", fallbackErr
		}
		log.Printf("Failed to get object from S3: %v", err)
		return nil, "", err
	}
	defer output.Body.Close()

	data, err := io.ReadAll(output.Body)
	if err != nil {
		return nil, "", err
	}

	contentType := ""
	if output.ContentType != nil {
		contentType = *output.ContentType
	}

	return data, contentType, nil
}

func (c *Client) Put(ctx context.Context, key string, data []byte, contentType string) error {
	log.Printf("Uploading to S3: bucket=%s, key=%s, contentType=%s", c.bucket, key, contentType)
	_, err := c.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String(contentType),
	})
	return err
}
