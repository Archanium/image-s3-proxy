# Image Proxy

A Go-based serverless image resizer and proxy with libvips, mirroring the logic of the original Node.js implementation.

## Features
- Fetches images from S3 (or any S3-compatible storage like Hetzner Object Storage).
- Resizes images on-the-fly based on URL patterns.
- Caches resized images back to S3.
- Supports worker trigger for bulk resizing.
- Configurable via environment variables.

## Running the Proxy

You can use Docker Compose to run the proxy locally:

```bash
docker-compose up app
```

The server will be available at `http://localhost:8080`.

### Environment Variables
- `BUCKET`: The S3 bucket name (required).
- `AWS_REGION`: AWS region (defaults to `us-east-1`).
- `AWS_ACCESS_KEY_ID`: AWS access key.
- `AWS_SECRET_ACCESS_KEY`: AWS secret key.
- `PORT`: Server port (defaults to `8080`).
- `IMAGE_TAGS`: Comma-separated list of tags (e.g., `Project=Dreamabout,Environment=Production`).

## Running Tests

To run tests in a consistent environment that mirrors the build environment:

```bash
docker-compose run tester
```

This will run all Go unit tests using the build container, ensuring that `libvips` and other dependencies are correctly set up.
