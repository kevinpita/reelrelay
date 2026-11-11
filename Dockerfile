# syntax=docker/dockerfile:1
FROM docker.io/library/golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY *.go ./
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /reelrelay .

FROM docker.io/library/python:3.15.0-slim-trixie@sha256:2da676046630d7bf14c6764ae1143dc7fa0377a53f94bf5a642f0b214aca5bce AS downloader
COPY requirements.txt /requirements.txt
# Match the interpreter path in distroless for the generated yt-dlp launcher.
RUN ln -s /usr/local/bin/python /usr/bin/python \
    && /usr/bin/python -m pip install --no-cache-dir --no-compile --require-hashes --target=/opt/yt-dlp -r /requirements.txt

FROM docker.io/mwader/static-ffmpeg:9.0.2-1@sha256:f5d100184884eb3e82b0eb14bb5ebf98da99826ac824989720297fb9ef21c822 AS ffmpeg
FROM gcr.io/distroless/python3-debian13:nonroot@sha256:f3d5ddc6c64a019fe520e7f005f2880be21e6afc461b10a3c15ef2e4edc71e33
COPY --from=build /reelrelay /usr/local/bin/reelrelay
COPY --from=downloader /opt/yt-dlp /opt/yt-dlp
COPY --from=ffmpeg /ffmpeg /ffprobe /usr/local/bin/
ENV YT_DLP_PATH=/opt/yt-dlp/bin/yt-dlp \
    PYTHONPATH=/opt/yt-dlp \
    PYTHONDONTWRITEBYTECODE=1 \
    HOME=/tmp
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/reelrelay"]
