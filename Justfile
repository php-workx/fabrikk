# fabrikk project quality gate
# Single source of truth for "my code is clean" — hooks and CI delegate here.

# Developer tools are pinned in tools.mod and invoked through go tool.
go_tool := "go tool -modfile=tools.mod"

version := `git describe --tags --always --dirty 2>/dev/null || echo "dev"`
commit := `git rev-parse --short HEAD 2>/dev/null || echo "unknown"`
build_date := `date -u +"%Y-%m-%dT%H:%M:%SZ"`
ldflags := "-X main.Version=" + version + " -X main.GitCommit=" + commit + " -X main.BuildDate=" + build_date

default:
    @just --list

# --- Quality gates ---

# Pre-commit: fast local checks + fresh non-race tests
pre-commit: fmt vet lint build-check mod-tidy actionlint gitleaks test-fast

# Pre-push: pre-commit checks + race tests + vulnerability scan
pre-push: pre-commit test-race vuln

# Full quality gate: same checks as pre-push
check: pre-push

# Full dev suite: quality gate + roam + sonar
dev: check roam sonar
    @echo "All checks passed!"

# --- Static analysis ---

# Check formatting with gofumpt (detect-only, no auto-fix)
fmt:
    @test -z "$({{go_tool}} gofumpt --extra -l .)" || (echo "gofumpt: unformatted files:" && {{go_tool}} gofumpt --extra -l . && exit 1)

# Go vet
vet:
    go vet ./...

# Lint with golangci-lint
lint:
    {{go_tool}} golangci-lint run

# Lint GitHub Actions workflows
actionlint:
    @if [ -d .github/workflows ]; then \
        {{go_tool}} actionlint .github/workflows/*.yml; \
    fi

# --- Security ---

# Scan for leaked secrets
gitleaks:
    @if command -v gitleaks >/dev/null 2>&1; then \
        gitleaks git --no-banner; \
    else \
        echo "warning: gitleaks not installed, skipping secret scan"; \
    fi

# Scan for known vulnerabilities in dependencies
vuln:
    {{go_tool}} govulncheck ./...

# --- Testing ---

# Verify the project compiles (fast, no binary output)
build-check:
    go build ./...

# Verify go.mod and go.sum are tidy (detect-only)
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

# Run all tests without race detector (fresh)
test: test-fast

# Run all tests without race detector (fresh)
test-fast:
    go test -count=1 ./...

# Run all tests with race detector (fresh)
test-race:
    go test -race -count=1 ./...

# Run tests with coverage report
cover:
    go test -race -coverprofile=coverage.out -covermode=atomic ./...
    go tool cover -html=coverage.out -o coverage.html
    @echo "Coverage report: coverage.html"

# --- External analysis ---

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
    elif ! command -v curl >/dev/null 2>&1; then \
        echo "curl not installed, skipping sonar scan"; \
    elif ! curl -fsS http://localhost:9000/api/server/version >/dev/null 2>&1; then \
        echo "SonarQube server unavailable on localhost:9000, skipping sonar scan"; \
    else \
        TOKEN=$(grep -m1 '^SONAR_TOKEN=' .env | sed 's/^SONAR_TOKEN=//' | sed 's/^"//; s/"$//'); \
        if [ -z "$TOKEN" ]; then \
            echo "error: SONAR_TOKEN not found or invalid in .env"; exit 1; \
        fi; \
        SONAR_TOKEN="$TOKEN" sonar-scanner -Dsonar.qualitygate.wait=true; \
    fi

# --- Build targets ---

# Build the fabrikk binary with version info
build:
    mkdir -p bin
    go build -ldflags '{{ldflags}}' -o bin/fabrikk ./cmd/fabrikk
    ln -sf fabrikk bin/fab

# Install fabrikk to $GOPATH/bin (or $GOBIN)
install:
    go install -ldflags '{{ldflags}}' ./cmd/fabrikk

# --- Setup ---

# Format all Go files in-place (use when `just fmt` fails)
format:
    {{go_tool}} gofumpt --extra -w .

# Set up git hooks and development environment
setup: install-dev
    git config core.hooksPath .githooks
    @echo "Git hooks configured (.githooks/)"
    mkdir -p .claude/skills
    ln -sfn ../../skills/fabrikk .claude/skills/fabrikk
    @echo "Claude Code skill symlinked (skills/fabrikk → .claude/skills/fabrikk)"

# Cache required development tools (pinned in tools.mod)
install-dev:
    @echo "Caching Go tool dependencies from tools.mod..."
    go mod download -modfile=tools.mod
    @echo "Done! Development tools are available through go tool -modfile=tools.mod."

# Remove build artifacts
clean:
    rm -rf bin/ coverage.out coverage.html
    go clean
