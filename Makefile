.PHONY: build test fmt up down build-alpine build-debian test-alpine test-debian

# Default targets point to Alpine for smaller footprint
build: build-alpine
test: test-alpine

# Build targets for specific environments
build-alpine:
	docker-compose build tester && docker compose run --rm tester
	docker compose build app

build-debian:
	docker compose build tester-debian && docker compose run --rm tester-debian
	docker compose build app-debian

# Run tests in specific environments
test-alpine:
	docker-compose run --rm tester

test-debian:
	docker-compose run --rm tester-debian

# Format Go source code locally
fmt:
	go fmt ./...

# Start the application in the background (Alpine by default)
up:
	docker-compose up -d app

# Stop and remove containers
down:
	docker-compose down
