### Go Development Best Practices

#### Project Structure
- Follow standard Go project layout (e.g., `cmd/`, `internal/`, `pkg/`).
- Keep the `main` package in `cmd/image-proxy`.
- Use `internal/` for packages you don't want others to import.

#### HTTP Server
- Use the standard `net/http` package.
- The server should listen on HTTP (no TLS) as it's expected to be behind a proxy/load balancer.
- Always configure an `http.Server` with explicit timeouts:
  ```go
  server := &http.Server{
      Addr:         ":8080",
      Handler:      handler,
      ReadTimeout:  5 * time.Second,
      WriteTimeout: 10 * time.Second,
      IdleTimeout:  120 * time.Second,
  }
  ```
- Implement graceful shutdown using `context.WithCancel` or `context.WithTimeout` and `os/signal`.
- Use `http.ServeMux` or a lightweight router like `chi` for routing.

#### Unit Testing
- Use the standard `testing` package.
- Write **table-driven tests** for clarity and scalability:
  ```go
  func TestSum(t *testing.T) {
      tests := []struct {
          name string
          a, b int
          want int
      }{
          {"positive numbers", 1, 2, 3},
          {"negative numbers", -1, -2, -3},
      }
      for _, tt := range tests {
          t.Run(tt.name, func(t *testing.T) {
              if got := Sum(tt.a, tt.b); got != tt.want {
                  t.Errorf("Sum() = %v, want %v", got, tt.want)
              }
          })
      }
  }
  ```
- Use `net/http/httptest` for testing HTTP handlers:
  ```go
  func TestHandler(t *testing.T) {
      req := httptest.NewRequest("GET", "/test", nil)
      rr := httptest.NewRecorder()
      handler := http.HandlerFunc(MyHandler)
      handler.ServeHTTP(rr, req)
      if status := rr.Code; status != http.StatusOK {
          t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
      }
  }
  ```
- Mock external dependencies using interfaces to keep tests isolated and fast.
- Aim for high coverage of business logic, but focus on meaningful assertions.

#### Error Handling
- Use `errors.New` or `fmt.Errorf` with `%w` for wrapping.
- Use `errors.Is` and `errors.As` for checking and unwrapping errors.
- Don't just ignore errors; log them or handle them appropriately.

#### Concurrency
- Use goroutines and channels sparingly and only when necessary.
- Avoid shared memory; communicate by sharing memory via channels.
- Use `sync.WaitGroup` or `context` for coordination and cancellation.

#### General Guidelines
- Use `gofmt` or `goimports` for consistent formatting.
- Follow Go's naming conventions (e.g., camelCase for internal symbols, PascalCase for exported symbols).
- Keep functions small and focused on a single task.

#### Docker
- Use **multi-stage builds** to keep the final image small and secure.
- Use a lightweight base image like `alpine` for the final stage.
- Ensure the application listens on the port exposed in the Dockerfile (default `:8080`).
- Use `.dockerignore` to exclude unnecessary files (like `.git`, local binaries, and sensitive data) from the build context.
- Run as a non-root user for better security.
- Set `CGO_ENABLED=0` when building to ensure the binary is statically linked and doesn't depend on C libraries not present in the base image.
