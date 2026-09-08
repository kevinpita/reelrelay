set dotenv-load
set shell := ["bash", "-euo", "pipefail", "-c"]

image := "igbot:dev"
engine := env("CONTAINER_ENGINE", "docker")
gofumpt := "mvdan.cc/gofumpt@v0.12.0"

# List available commands.
default:
    @just --list

# Run the bot with local credentials.
run:
    go run .

# Build a static Linux x64 executable.
build:
    mkdir -p bin
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o bin/igbot .

# Format project Go files with gofumpt, plus Nix and Just files.
fmt:
    git ls-files --cached --others --exclude-standard -z -- '*.go' | xargs -0 -r go run {{ gofumpt }} -w --
    nixfmt devenv.nix
    just --fmt --unstable

# Check Go formatting without changing files or scanning ignored dependencies.
fmt-check:
    #!/usr/bin/env bash
    set -euo pipefail
    files="$(git ls-files --cached --others --exclude-standard -z -- '*.go' | xargs -0 -r go run {{ gofumpt }} -l --)"
    if [[ -n "$files" ]]; then
        printf 'Run just fmt for these files:\n%s\n' "$files"
        exit 1
    fi

# Check formatting, dependencies, and static analysis.
lint: fmt-check
    nixfmt --check devenv.nix
    just --fmt --check --unstable
    go mod tidy -diff
    golangci-lint config verify
    golangci-lint run
    actionlint
    hadolint Dockerfile

# Run race tests in shuffled order. Requires a C compiler.
test:
    go test -race -shuffle=on -count=1 -timeout=5m ./...

# Check known vulnerabilities in reachable Go code.
audit:
    govulncheck ./...

# Render the Helm chart without accessing a cluster.
k8s-render:
    helm template igbot infra/chart --namespace igbot

# Lint the Helm chart and validate the rendered Kubernetes resources.
k8s-check:
    helm lint --strict infra/chart
    helm template igbot infra/chart --namespace igbot | kubeconform -strict -summary

# Run all local checks.
check: lint test audit k8s-check

# Build the runtime image. No image is published.
docker-build tag=image:
    {{ engine }} build --platform linux/amd64 --tag {{ quote(tag) }} .

# Check image tools and non-root execution without credentials.
docker-smoke tag=image:
    {{ engine }} run --rm --network=none --read-only --tmpfs /tmp:rw,nosuid,nodev,size=256m {{ quote(tag) }} --version
    {{ engine }} run --rm --network=none --read-only --tmpfs /tmp:rw,nosuid,nodev,size=256m --entrypoint /usr/bin/python {{ quote(tag) }} -c 'import os, shutil, subprocess; assert os.getuid() == 10001; assert shutil.which("sh") is None; assert shutil.which("pip") is None; subprocess.run([os.environ["YT_DLP_PATH"], "--version"], check=True); subprocess.run(["ffmpeg", "-version"], check=True, stdout=subprocess.DEVNULL); subprocess.run(["ffprobe", "-version"], check=True, stdout=subprocess.DEVNULL)'

# Run the local image with credentials and writable temporary storage.
docker-run tag=image:
    {{ engine }} run --rm --env-file .env --read-only --tmpfs /tmp:rw,nosuid,nodev,size=1g --publish 127.0.0.1:8080:8080 {{ quote(tag) }}

# Update the pinned development tools. Review and test the resulting lockfile.
update-tools:
    devenv update

# Delete generated Go artifacts only.
clean:
    rm -rf bin coverage.out
