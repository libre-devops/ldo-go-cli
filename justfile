# ldo-go task runner. Run `just` to list the recipes.

set shell := ["bash", "-euo", "pipefail", "-c"]
set windows-shell := ["powershell.exe", "-NoLogo", "-NoProfile", "-Command"]

# Go installed per user (~/.local/go) is found without being on PATH.
export PATH := env_var_or_default('HOME', '') / ".local/go/bin" + ":" + env_var_or_default('HOME', '') / "go/bin" + ":" + env_var('PATH')

# The share of statements the tests must cover, fakes and main left out.
coverage_floor := "85"

# Tools run at a pinned version, never added to go.mod.
staticcheck := "honnef.co/go/tools/cmd/staticcheck@v0.8.1"
govulncheck := "golang.org/x/vuln/cmd/govulncheck@v1.8.0"

# List the recipes
default:
    @just --list --unsorted

# Format check, vet and the tests with coverage: run before calling anything done
[group('check')]
check: fmt-check vet test

# Everything CI checks: check, then staticcheck and govulncheck
[group('check')]
ci: check lint vuln

# Format every Go file
[group('check')]
fmt:
    gofmt -w .

# Fail when a Go file is not formatted
[group('check')]
[unix]
[script("bash")]
fmt-check:
    set -euo pipefail
    unformatted="$(gofmt -l .)"
    if [ -n "$unformatted" ]; then echo "not formatted:"; echo "$unformatted"; exit 1; fi

# go vet on every package
[group('check')]
vet:
    go vet ./...

# staticcheck on every package
[group('check')]
lint:
    go run {{staticcheck}} ./...

# Known vulnerabilities in the code and the modules it uses
[group('check')]
vuln:
    go run {{govulncheck}} ./...

# The tests, with the coverage floor (a statement counts when any test runs it)
[group('check')]
[unix]
[script("bash")]
test:
    set -euo pipefail
    go test -count=1 -coverpkg=./internal/... -coverprofile=coverage.out ./...
    grep -v -e '/internal/fakes/' -e '/cmd/' coverage.out > coverage.kept.out
    total="$(go tool cover -func=coverage.kept.out | awk '/^total:/ { sub("%", "", $3); print $3 }')"
    rm -f coverage.kept.out
    echo "coverage: ${total}% (floor {{coverage_floor}}%)"
    awk -v total="$total" -v floor="{{coverage_floor}}" 'BEGIN { exit (total + 0 < floor + 0) }' \
        || { echo "coverage is under the floor"; exit 1; }

# What the tests leave uncovered, as a page to open (runs the tests first)
[group('check')]
coverage: test
    go tool cover -html=coverage.out -o coverage.html
    @echo "wrote coverage.html"

# The secret scan, uncommitted changes included (needs gitleaks on PATH)
[group('check')]
secrets:
    gitleaks dir --config .gitleaks.toml --redact .

# One package's tests, verbosely: just test-one ./internal/core/util
[group('check')]
test-one package:
    go test -count=1 -v {{package}}

# Record the JSON output shapes again, after a change to one that is meant
[group('check')]
record-json:
    LDO_RECORD_JSON_OUTPUT=1 go test -count=1 -run TestJSONOutputShapes ./internal/cli/

# Build bin/ldo-go
[group('build')]
build:
    go build -trimpath -o bin/ldo-go ./cmd/ldo-go

# Build and run: just run graph whoami -p dev
[group('build')]
run *args: build
    ./bin/ldo-go {{args}}

# Build for every platform a release carries, into dist/
[group('build')]
[unix]
[script("bash")]
dist version="":
    set -euo pipefail
    rm -rf dist && mkdir -p dist
    for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
        os="${target%/*}"; arch="${target#*/}"; suffix=""
        if [ "$os" = windows ]; then suffix=".exe"; fi
        CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
            -ldflags "-s -w -X github.com/libre-devops/ldo-go-cli/internal/core/brand.buildVersion={{version}}" \
            -o "dist/ldo-go-${os}-${arch}${suffix}" ./cmd/ldo-go
    done
    ls -l dist

# Put the working tree's ldo-go on PATH, in Go's bin folder (~/go/bin)
[group('build')]
install:
    go install -trimpath ./cmd/ldo-go

# The release workflow then runs CI, builds and publishes it.
# Tag a release and push the tag, once it can be one: just release 0.1.0
[group('release')]
[unix]
[script("bash")]
release version:
    set -euo pipefail
    fail() { echo "error: $1" >&2; exit 1; }
    [[ "{{version}}" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]] || fail "'{{version}}' is not a version such as 0.1.0 or 0.2.0-rc.1"
    [ -z "$(git status --porcelain)" ] || fail "the working tree has uncommitted changes"
    [ "$(git rev-parse --abbrev-ref HEAD)" = main ] || fail "releases are tagged on main"
    git fetch --quiet origin main
    [ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || fail "main is not the same as origin/main"
    grep -qx "## {{version}}" CHANGELOG.md || fail "CHANGELOG.md has no '## {{version}}' section"
    if git rev-parse --quiet --verify "refs/tags/v{{version}}" > /dev/null; then fail "v{{version}} already exists"; fi
    git tag --annotate "v{{version}}" --message "{{version}}"
    git push origin "v{{version}}"
    echo "pushed v{{version}}: the release workflow tests, builds and publishes it"

# Remove build and coverage output
[group('build')]
clean:
    rm -rf bin dist coverage.out coverage.kept.out coverage.html
