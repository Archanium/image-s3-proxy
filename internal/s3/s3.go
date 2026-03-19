package s3

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type Client struct {
	client *s3.Client
	bucket string
}

func NewClient(ctx context.Context, bucket, region, accessKey, secretKey string) (*Client, error) {
	var cfg aws.Config
	var err error

	if accessKey != "" && secretKey != "" {
		cfg, err = config.LoadDefaultConfig(ctx,
			config.WithRegion(region),
			config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
		)
	} else {
		cfg, err = config.LoadDefaultConfig(ctx, config.WithRegion(region))
	}

	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config: %w", err)
	}

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
		var nsk *types.NoSuchKey
		var nf *types.NotFound
		if strings.Contains(err.Error(), "NotFound") || strings.Contains(err.Error(), "NoSuchKey") {
			return false, nil
		}
		// In AWS SDK v2, errors are usually types.NotFound or types.NoSuchKey but HeadObject might return 404
		return false, err
	}
	return true, nil
}

func (c *Client) Get(ctx context.Context, key string) ([]byte, string, error) {
	output, err := c.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
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
