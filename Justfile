# attest project quality gate
# This is the single entry point for "my code is clean" (spec section 11.2)

version := `git describe --tags --always --dirty 2>/dev/null || echo "dev"`
commit := `git rev-parse --short HEAD 2>/dev/null || echo "unknown"`
build_date := `date -u +"%Y-%m-%dT%H:%M:%SZ"`
ldflags := "-X main.Version=" + version + " -X main.GitCommit=" + commit + " -X main.BuildDate=" + build_date

default:
    @just --list

# Run all quality checks: vet, lint, format, test (the quality gate)
check: vet lint fmt test

# Run full dev suite: quality gate + vulnerability scan + roam
dev: check vuln roam
    @echo "All checks passed!"

# Build the attest binary with version info
build:
    go build -ldflags '{{ldflags}}' -o bin/attest ./cmd/attest

# Go vet
vet:
    go vet ./...

# Lint with golangci-lint
lint:
    golangci-lint run

# Check formatting with gofumpt (fails if any file needs formatting)
fmt:
    @test -z "$(gofumpt --extra -l .)" || (echo "gofumpt: unformatted files:" && gofumpt --extra -l . && exit 1)

# Run all tests with race detector
test:
    go test -race ./...

# Run tests with coverage report
cover:
    go test -race -coverprofile=coverage.out -covermode=atomic ./...
    go tool cover -html=coverage.out -o coverage.html
    @echo "Coverage report: coverage.html"

# Scan for known vulnerabilities
vuln:
    @if command -v govulncheck >/dev/null 2>&1; then \
        govulncheck ./...; \
    else \
        echo "govulncheck not installed, skipping (run: just install-dev)"; \
    fi

# Run roam architectural checks (optional, skip if not installed)
roam:
    @if command -v roam >/dev/null 2>&1; then \
        roam index && roam fitness && roam pr-risk main..HEAD; \
    else \
        echo "roam not installed, skipping roam checks"; \
    fi

# Format all Go files in-place
format:
    gofumpt --extra -w .

# Install required development tools
install-dev:
    @echo "Installing Go tools..."
    go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
    go install mvdan.cc/gofumpt@latest
    go install golang.org/x/vuln/cmd/govulncheck@latest
    @echo "Done! Development environment ready."

# Remove build artifacts
clean:
    rm -rf bin/ coverage.out coverage.html
    go clean
