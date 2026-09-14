# ReelRelay

**Send an Instagram or Twitter/X link to Telegram. Receive the video in the same chat.**

A small Go bot for Instagram posts, reels, and share links, plus Twitter/X video posts. It uses Telegram long polling, so it needs no public URL or webhook.

```text
Telegram message → Go bot → yt-dlp → temporary video → Telegram upload
                                      └── deleted after the request
```

## What it does

- Accepts links in message text and captions.
- Sends the first video from a post, up to 50 MiB.
- Runs two downloads at a time by default, with a three-minute download timeout.
- Supports Instagram cookie files, session cookies, and local browser cookies.
- Stops downloads on `SIGTERM` and removes temporary files.
- Provides health endpoints for Kubernetes.

`yt-dlp` is an external dependency. This repo contains no copy of its source or executable. The bot never installs or updates tools at startup.

## Quick start

Install [Nix](https://nixos.org/download/) and [devenv](https://devenv.sh/getting-started/) 2.3 or later. Get a bot token from [@BotFather](https://t.me/BotFather).

From the repo root:

```bash
# Keep an existing local configuration.
test -e .env || cp .env.example .env
chmod 600 .env
```

Set the token in `.env`. Do not add quotes or an `export` prefix if you also use the container commands.

```dotenv
TELEGRAM_BOT_TOKEN=your-bot-token
```

Start the development shell and the bot:

```bash
devenv shell
just run
```

Send `/start`, then an Instagram post, reel, or Twitter/X post link to the bot.

Twitter/X links must use `twitter.com` or `x.com` with a post path such as `/user/status/123`. The `www` and `mobile` hosts and `/i/web/status/123` paths are also accepted. Short `t.co` links are not supported.

The locked development environment includes Go, a C compiler for race tests, `yt-dlp`, FFmpeg, Just, and validation tools. Without Nix, install these tools yourself. Use `just run` to load `.env`; the Go executable reads only process environment variables.

## Commands

Run `just` to list all commands.

| Command | Result |
| --- | --- |
| `just run` | Run the bot with `.env` |
| `just build` | Build `bin/reelrelay` for Linux x64 (`linux/amd64`) |
| `just fmt` | Format Go with gofumpt, plus Nix and Just files |
| `just fmt-check` | Check gofumpt formatting without changing files |
| `just lint` | Run the selected Go linters and local file checks |
| `just check` | Check formatting, static analysis, tests, vulnerabilities, and Kubernetes resources |
| `just test` | Run race tests in shuffled order with a five-minute timeout |
| `just docker-build` | Build `reelrelay:dev` locally |
| `just docker-smoke` | Test the image without credentials or network access |
| `just docker-run` | Run the image with `.env` |
| `just k8s-render` | Render the Helm chart without cluster access |
| `just k8s-check` | Lint the Helm chart and validate its rendered resources |
| `just update-tools` | Update `devenv.lock`; review and test the changes |
| `just clean` | Remove generated Go build and coverage files |

Go tests use a local Telegram HTTP server and a test downloader process. They need no credentials or live platform access. Dependency downloads, the vulnerability check, and Kubernetes schema validation need network access.

## Go style and lint policy

Use **gofumpt**, not plain gofmt. `just fmt` and CI use the same pinned gofumpt release. They select project Go files through Git instead of scanning the development environment's module cache. The first run can download the formatter.

[`.golangci.yml`](.golangci.yml) selects checks explicitly instead of using a preset:

- Correctness: `govet`, `staticcheck`, `unused`, and `ineffassign`.
- Errors and resources: `errcheck`, `errorlint`, `bodyclose`, `nilerr`, and `durationcheck`.
- Contexts and modern Go: `noctx`, `fatcontext`, `modernize`, `copyloopvar`, and `usetesting`, including `t.Context()`.
- Low-noise checks: `predeclared`, `unconvert`, and `nolintlint`.

There are no custom naming rules, function-length limits, or blanket test-file exclusions. Any `nolint` directive must name its linter and explain the exception.

Gofumpt runs separately because the current golangci-lint release embeds an older formatter. `golangci-lint run` handles the other Go checks.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `TELEGRAM_BOT_TOKEN` | Required | Telegram bot token; `BOT_TOKEN` is also accepted |
| `YT_DLP_PATH` | `yt-dlp` on `PATH` | Explicit executable path; an invalid path fails at startup |
| `INSTAGRAM_COOKIES_FILE` | `cookies.txt`, if present | Netscape-format Instagram cookie file |
| `INSTAGRAM_SESSION_ID` | Unset | Instagram `sessionid` cookie |
| `INSTAGRAM_COOKIES_BROWSER` | Unset | Local browser supported by `yt-dlp`, such as `firefox` |
| `MAX_CONCURRENT_DOWNLOADS` | `2` | Concurrent requests, from `1` to `16`; excess requests receive a busy reply |
| `HEALTH_ADDR` | `:8080` | Health server bind address |
| `TMPDIR` | OS default; `/tmp` in the image | Temporary download storage |

Use one Instagram authentication method. A cookie file takes priority over a session ID; a session ID takes priority over browser cookies. Each request gets a private, writable cookie copy. The source file stays unchanged, including when it comes from a read-only Kubernetes Secret.

Twitter/X downloads use public access. Instagram cookie settings apply only to Instagram. Twitter/X authentication is not configured, so posts that require login may fail.

Keep `.env` and cookie files private. Git ignores them. The container build accepts only source files and dependency manifests, so local credentials cannot enter the image.

## Container

Install Docker or Podman separately. The final image uses distroless Python, a static Go executable (`CGO_ENABLED=0`), a hash-verified `yt-dlp` package, and static FFmpeg/FFprobe binaries from [static-ffmpeg](https://github.com/wader/static-ffmpeg). Build stages prepare these files; the runtime has no shell, compiler, pip, or package manager.

The target is Linux x64 (`linux/amd64`). It runs as UID/GID `10001` and supports a read-only root filesystem with writable `/tmp`.

```bash
just docker-build
just docker-smoke
just docker-run
```

For Podman:

```bash
CONTAINER_ENGINE=podman just docker-build localhost/reelrelay:dev
CONTAINER_ENGINE=podman just docker-smoke localhost/reelrelay:dev
CONTAINER_ENGINE=podman just docker-run localhost/reelrelay:dev
```

The local run command binds health port 8080 only to `127.0.0.1`. It does not mount a cookie file or a browser profile. Use `INSTAGRAM_SESSION_ID` in `.env`, or add a read-only cookie-file mount to a custom container command.

- `GET /healthz`: the health server is running.
- `GET /readyz`: startup succeeded and shutdown has not started.

Readiness does not test current Telegram, Instagram, or Twitter/X availability. These services can fail after startup. Do not expose the health port through a public Ingress.

## CI and deployment

[GitHub Actions](.github/workflows/ci.yml) runs three independent jobs in parallel:

- **Go lint:** gofumpt, module consistency, and golangci-lint through its official action.
- **Go tests:** race detection, shuffled test order, and a five-minute test timeout.
- **Go vulnerabilities:** `govulncheck`.

```text
Go lint ─────────────┐
Go tests ────────────┼─→ Image: build → test → publish when allowed
Go vulnerabilities ─┘
```

The image job depends on all three checks. A failed, cancelled, or skipped check prevents the image job from starting. The image is built once, tested, then published without rebuilding. Only the image job has package-write permission.

Require **Go lint**, **Go tests**, and **Go vulnerabilities** in GitHub branch protection. Do not rely only on **Image**, because GitHub treats skipped jobs differently from failed checks.

CI does not install Nix, devenv, or Just. Run `just check` locally for the additional Nix formatting, workflow, Dockerfile, Helm, and Kubernetes schema checks.

Pull requests do **not** publish images. Pushes to `main` and version tags such as `v1.0.0` publish `linux/amd64` images to:

```text
ghcr.io/kevinpita/reelrelay
```

Images receive only the first eight characters of the commit SHA as their tag, with no `sha-` prefix. For example, commit `d5c8c3bdae712df7053669d69db2672f6a928175` produces `ghcr.io/kevinpita/reelrelay:d5c8c3bd`. The executable's `--version` output keeps the full commit SHA. A version tag triggers CI but does not create a second image tag. CI records the published image digest in its job summary. It uses `GITHUB_TOKEN`; no bot credentials belong in CI. The workflow does not configure SBOM or provenance attestations.

The [Helm chart](infra/chart/) uses image and Secret references from [`infra/chart/values.yaml`](infra/chart/values.yaml). Replace the `<pending>` image tag with a published commit tag before deployment. Helm does not select newer registry tags. To deploy another build, update `image.tag` or set `image.digest` to its published `sha256:...` digest. A non-empty digest takes priority over the tag.

For OpenBao, follow [the application access setup](infra/openbao/README.md). Terraform manages the application policy and authentication role. The chart uses ESO to copy the single `kv/apps/reelrelay` entry into the Secret named by `existingSecret`. OpenBao sync is disabled until you set the Kubernetes API audience and enable it in the chart values.

Without OpenBao, create the Secret named by `existingSecret` in the deployment namespace with a `TELEGRAM_BOT_TOKEN` key. Do not commit credentials. For private images, configure `imagePullSecrets`. The chart uses one replica and the `Recreate` strategy because Telegram permits only one long-polling consumer per bot token.

### Argo CD

CI builds, tests, and publishes images. Argo CD applies the deployment configuration from Git. Configure the Argo CD Application to track this repository's `main` branch at `infra/chart`, with destination namespace `reelrelay` and the `CreateNamespace=true` sync option.

```text
CI → publish GHCR image → update image reference in Git → Argo CD sync → Kubernetes
```

For an existing installation, the renamed chart changes the Deployment's immutable label selector. Plan a one-time Deployment replacement and make sure `reelrelay-secrets` is ready first. Do not run the old and new deployments together with the same bot token.

Registry publication alone does not trigger a deployment. Commit the new image tag or digest to the values file that Argo CD uses. Argo CD then deploys it when [automatic sync](https://argo-cd.readthedocs.io/en/stable/user-guide/auto_sync/) is enabled; otherwise, sync the application manually. CI needs no cluster credentials.

Start with reviewed values changes. For automatic image updates, extend CI to open a values-update pull request after a successful image push. [Argo CD Image Updater with Git write-back](https://argocd-image-updater.readthedocs.io/en/stable/basics/update-methods/#git-write-back-method) is an alternative. Use only one of these to update image references. Before enabling either method, exclude values-only commits from image publication to prevent an update loop. Neither method is configured here.

Without Argo CD, deploy with Helm after creating the required Secret:

```bash
helm upgrade --install reelrelay infra/chart --namespace reelrelay --create-namespace
```

## Tool versions

Versions were checked on **2026-09-08**:

- Go **1.27.1** in `go.mod` and the builder image.
- golangci-lint **2.13.2**, gofumpt **0.12.0**, and govulncheck **1.7.0**.
- Python **3.13** in the distroless Debian 13 runtime; the dependency stage uses the same Python minor version.
- FFmpeg and FFprobe **9.0.1** as static binaries.
- `yt-dlp` **2026.08.19** in `requirements.txt` and the development environment.
- devenv **2.3** or later for local development; Just **1.58.0** and Helm **4.2.4** in the locked environment.

Container bases use digest pins. Actions use commit pins. `devenv.lock` fixes the current `nixpkgs-unstable` snapshot; packaged tools can lag upstream releases. The distroless base supplies Debian-maintained Python and system libraries. FFmpeg comes from a separately pinned image.

Dependabot checks Go modules, Python requirements, container bases, and Actions each week. Update Nix tools with `just update-tools`. Review version changes and run `just check`, `just docker-build`, and `just docker-smoke` before use. Update Go in both `go.mod` and `Dockerfile` when required. Keep the gofumpt version in `justfile` and CI aligned. Inline Go-tool and linter version pins need a separate review; Dependabot does not update those pins.

## Limits and responsible use

- The bot accepts requests from anyone who can message it. There is no user allowlist or persistent job queue.
- Downloads in progress are cancelled during a restart. Jobs are not durable and may need to be sent again.
- Instagram and Twitter/X can require login, change its API, or limit requests. Cookies do not guarantee access.
- Photo-only posts are not supported. Multi-item posts return at most one video.
- In groups, Telegram privacy mode can prevent the bot from receiving ordinary links. Configure it through BotFather if needed.
- Download only media you have permission to use. Follow Instagram, Twitter/X, and Telegram terms.

## Repository map

```text
main.go                 Telegram handling, configuration, shutdown, and health
main_test.go            HTTP upload, configuration, and health tests
downloader.go           URL validation and external downloader execution
downloader_test.go      Download, cookie, timeout, and cleanup tests
devenv.nix / devenv.lock Development tools and fixed dependency versions
.golangci.yml           Explicit modern Go lint policy
justfile                Local development commands
Dockerfile              Non-root runtime image
.github/                CI and dependency updates
infra/chart/            Helm chart, ESO resources, and deployment values
infra/openbao/          Terraform policy, authentication role, and setup steps
```
