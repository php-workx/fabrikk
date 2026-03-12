# attest project quality gate
# This is the single entry point for "my code is clean" (spec section 11.2)

# Pinned tool versions — keep in sync with CI (.github/workflows/ci.yml)
golangci_lint_ver := "v2.11.3"
gofumpt_ver := "v0.7.0"
govulncheck_ver := "v1.1.4"
actionlint_ver := "v1.7.11"

version := `git describe --tags --always --dirty 2>/dev/null || echo "dev"`
commit := `git rev-parse --short HEAD 2>/dev/null || echo "unknown"`
build_date := `date -u +"%Y-%m-%dT%H:%M:%SZ"`
ldflags := "-X main.Version=" + version + " -X main.GitCommit=" + commit + " -X main.BuildDate=" + build_date

default:
    @just --list

# Run all quality checks (the quality gate)
check: vet lint actionlint fmt mod-tidy test build-check

# Run full dev suite: quality gate + vulnerability scan + roam + sonar
dev: check vuln roam sonar
    @echo "All checks passed!"

# Build the attest binary with version info
build:
    mkdir -p bin
    go build -ldflags '{{ldflags}}' -o bin/attest ./cmd/attest

# Go vet
vet:
    go vet ./...

# Lint with golangci-lint
lint:
    golangci-lint run

# Lint GitHub Actions workflows
actionlint:
    @command -v actionlint >/dev/null 2>&1 || (echo "actionlint not installed (run: just install-dev)" && exit 1)
    actionlint .github/workflows/*.yml

# Check formatting with gofumpt (fails if any file needs formatting)
fmt:
    @command -v gofumpt >/dev/null 2>&1 || (echo "gofumpt not installed (run: just install-dev)" && exit 1)
    @test -z "$(gofumpt --extra -l .)" || (echo "gofumpt: unformatted files:" && gofumpt --extra -l . && exit 1)

# Verify go.mod and go.sum are tidy
mod-tidy:
    @cp go.mod go.mod.bak
    @if [ -f go.sum ]; then cp go.sum go.sum.bak; fi
    @go mod tidy
    @DIRTY=0; \
        diff -q go.mod go.mod.bak >/dev/null 2>&1 || DIRTY=1; \
        if [ -f go.sum.bak ]; then diff -q go.sum go.sum.bak >/dev/null 2>&1 || DIRTY=1; \
        elif [ -f go.sum ]; then DIRTY=1; fi; \
        mv go.mod.bak go.mod; \
        if [ -f go.sum.bak ]; then mv go.sum.bak go.sum; elif [ -f go.sum ]; then rm go.sum; fi; \
        if [ "$$DIRTY" = "1" ]; then echo "go.mod/go.sum not tidy — run 'go mod tidy'" && exit 1; fi

# Verify the project compiles (fast, no binary output)
build-check:
    go build ./...

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

# Run SonarQube scan (requires SONAR_TOKEN in .env and local SonarQube on localhost:9000)
sonar:
    @if ! command -v sonar-scanner >/dev/null 2>&1; then \
        echo "sonar-scanner not installed, skipping"; \
    elif [ ! -f .env ]; then \
        echo ".env missing, skipping sonar scan"; \
    else \
        TOKEN=$(grep -E '^SONAR_TOKEN=[A-Za-z0-9_]+$$' .env | cut -d= -f2); \
        if [ -z "$$TOKEN" ]; then \
            echo "error: SONAR_TOKEN not found or invalid in .env"; exit 1; \
        fi; \
        SONAR_TOKEN="$$TOKEN" sonar-scanner; \
    fi

# Format all Go files in-place
format:
    gofumpt --extra -w .

# Set up git hooks and development environment
setup: install-dev
    git config core.hooksPath .githooks
    @echo "Git hooks configured (.githooks/)"

# Install required development tools (pinned versions)
install-dev:
    @echo "Installing Go tools..."
    go install github.com/golangci/golangci-lint/cmd/golangci-lint@{{golangci_lint_ver}}
    go install mvdan.cc/gofumpt@{{gofumpt_ver}}
    go install golang.org/x/vuln/cmd/govulncheck@{{govulncheck_ver}}
    go install github.com/rhysd/actionlint/cmd/actionlint@{{actionlint_ver}}
    @echo "Done! Development environment ready."

# Remove build artifacts
clean:
    rm -rf bin/ coverage.out coverage.html
    go clean
