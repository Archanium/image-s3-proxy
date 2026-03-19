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

type Client struct {
	client *s3.Client
	bucket string
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

func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	_, err := c.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if strings.Contains(err.Error(), "NotFound") || strings.Contains(err.Error(), "NoSuchKey") {
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
