.PHONY: all build clean test server worker cli linux-server linux-worker linux-all

# Build everything
all: build

# Build all binaries for current platform
build: server worker cli

# Build server
server:
	@echo "Building cnc-server..."
	@go build -o cnc-server ./cmd/server/main.go

# Build worker
worker:
	@echo "Building cnc-worker..."
	@go build -o cnc-worker ./cmd/worker/main.go

# Build CLI
cli:
	@echo "Building cnc CLI..."
	@go build -o cnc ./cmd/cnc/main.go

# Build Linux binaries (for deployment)
linux-all: linux-server linux-worker

linux-server:
	@echo "Building cnc-server for Linux..."
	@CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o cnc-server-linux ./cmd/server/main.go

linux-worker:
	@echo "Building cnc-worker for Linux..."
	@CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o cnc-worker-linux ./cmd/worker/main.go

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -f cnc cnc-server cnc-worker cnc-server-linux cnc-worker-linux
	@rm -rf cnc_data worker_data
	@echo "Clean complete"

# Run tests
test:
	@echo "Running tests..."
	@go test -v ./...

# Run server locally
run-server: server
	@./cnc-server

# Run worker locally
run-worker: worker
	@./cnc-worker

# Install dependencies
deps:
	@echo "Installing dependencies..."
	@go mod download
	@go mod tidy
