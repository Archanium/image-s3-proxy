package s3

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type s3API interface {
	HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

type Client struct {
	client         s3API
	bucket         string
	defaultTags    map[string]string
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

func (c *Client) SetDefaultTags(tags map[string]string) {
	c.defaultTags = tags
}

func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	_, err := c.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if strings.Contains(err.Error(), "NotFound") || strings.Contains(err.Error(), "NoSuchKey") {
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
		// In AWS SDK v2, errors are usually types.NotFound or types.NoSuchKey but HeadObject might return 404
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
		if (strings.Contains(err.Error(), "NotFound") || strings.Contains(err.Error(), "NoSuchKey")) && c.fallbackClient != nil {
			log.Printf("Not found in primary bucket %s, trying fallback bucket %s", c.bucket, c.fallbackClient.bucket)
			data, contentType, fallbackErr := c.fallbackClient.Get(ctx, key)

			// If not found in fallback with original key, try stripping the first prefix (clientId)
			if fallbackErr != nil && (strings.Contains(fallbackErr.Error(), "NotFound") || strings.Contains(fallbackErr.Error(), "NoSuchKey")) {
				if parts := strings.SplitN(key, "/", 2); len(parts) > 1 {
					log.Printf("Not found in fallback bucket %s with original key, trying stripped key: %s", c.fallbackClient.bucket, parts[1])
					data, contentType, fallbackErr = c.fallbackClient.Get(ctx, parts[1])
				}
			}

			if fallbackErr == nil {
				log.Printf("Found in fallback bucket, moving to primary bucket: %s", key)
				// Save it back to the primary bucket for future requests
				if putErr := c.Put(ctx, key, data, contentType, c.defaultTags); putErr != nil {
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

func (c *Client) Put(ctx context.Context, key string, data []byte, contentType string, tags map[string]string) error {
	log.Printf("Uploading to S3: bucket=%s, key=%s, contentType=%s, tags=%v", c.bucket, key, contentType, tags)
	var tagging *string
	if len(tags) > 0 {
		var pairs []string
		for k, v := range tags {
			pairs = append(pairs, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
		t := strings.Join(pairs, "&")
		tagging = &t
	}

	_, err := c.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String(contentType),
		Tagging:     tagging,
	})
	return err
}
