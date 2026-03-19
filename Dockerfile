# Stage 1: Build
FROM golang:1.19-bullseye AS builder

# Install dependencies for libvips and CGO
RUN apt-get update && apt-get install -y \
    git \
    build-essential \
    pkg-config \
    libvips-dev

WORKDIR /app

# Copy go.mod and go.sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . .

# Build the application with CGO enabled
RUN CGO_ENABLED=1 GOOS=linux go build -o /image-proxy ./cmd/image-proxy

# Stage 2: Final
FROM debian:bullseye-slim

# Install libvips dependencies in the final image
RUN apt-get update && apt-get install -y \
    ca-certificates \
    libvips42 \
    && rm -rf /var/lib/apt/lists/*

# Create a non-root user
RUN useradd -m appuser
USER appuser

WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder /image-proxy .

# Expose port 8080 to the outside world
EXPOSE 8080

# Command to run the executable
CMD ["./image-proxy"]
