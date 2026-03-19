# Image Proxy

A Go-based serverless image resizer and proxy with libvips, mirroring the logic of the original Node.js implementation.

## Features
- Fetches images from S3 (or any S3-compatible storage like Hetzner Object Storage).
- Resizes images on-the-fly based on URL patterns.
- Caches resized images back to S3.
- Supports worker trigger for bulk resizing.
- Configurable via environment variables.

## Usage with Makefile

A `Makefile` is provided to simplify common tasks:

- **Build images (Alpine)**: `make build` (or `make build-alpine`)
- **Build images (Debian)**: `make build-debian`
- **Run tests (Alpine)**: `make test` (or `make test-alpine`)
- **Run tests (Debian)**: `make test-debian`
- **Format code**: `make fmt`
- **Start application (Alpine)**: `make up`
- **Stop application**: `make down`

## Running the Proxy

You can use Docker Compose to run the proxy locally:

```bash
make up
```

The server will be available at `http://localhost:8080`.

### Environment Variables
- `BUCKET`: The S3 bucket name (required).
- `AWS_REGION`: AWS region (defaults to `us-east-1`).
- `AWS_ACCESS_KEY_ID`: AWS access key.
- `AWS_SECRET_ACCESS_KEY`: AWS secret key.
- `PORT`: Server port (defaults to `8080`).
- `IMAGE_TAGS`: Comma-separated list of tags (e.g., `Project=Dreamabout,Environment=Production`).
- `SIZES`: (Worker only) JSON array of target sizes for bulk resizing (e.g., `[[150,210],[240,0]]`). Defaults to a predefined list.
- `FORMAT`: (Worker only) Target format for bulk resizing (e.g., `webp`, `avif`, `jpg`). Defaults to `avif`.

