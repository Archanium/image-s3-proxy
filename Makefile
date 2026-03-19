.PHONY: build test fmt up down

# Build the application images using Docker Compose
build:
	docker-compose build

# Run tests inside the Docker build environment (includes libvips-dev)
test:
	docker-compose run --rm tester

# Format Go source code locally
fmt:
	go fmt ./...

# Start the application in the background
up:
	docker-compose up -d app

# Stop and remove containers
down:
	docker-compose down
