# attest project quality gate
# This is the single entry point for "my code is clean" (spec section 11.2)

default:
    @just --list

# Run all quality checks: vet, lint, format, test
check: vet lint fmt test

# Go vet
vet:
    go vet ./...

# Lint with golangci-lint
lint:
    golangci-lint run

# Check formatting with gofumpt (fails if any file needs formatting)
fmt:
    @test -z "$(gofumpt --extra -l .)" || (echo "gofumpt: unformatted files:" && gofumpt --extra -l . && exit 1)

# Run all tests
test:
    go test ./...

# Format all Go files in-place
format:
    gofumpt --extra -w .
