# syntax=docker/dockerfile:1
FROM docker.io/library/golang:1.27.1-trixie@sha256:9baa6b4187bbb98d240372a8a235ac0bb6b5ddd52bba1431dc2f7c0705862728 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY *.go ./
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /reelrelay .

FROM docker.io/library/python:3.13.15-slim-trixie@sha256:9d2e5553305c7c7b0097999bb17187c69b921ccd6bc9d40e4bb5ebe652c00285 AS downloader
COPY requirements.txt /requirements.txt
# Match the interpreter path in distroless for the generated yt-dlp launcher.
RUN ln -s /usr/local/bin/python /usr/bin/python \
    && /usr/bin/python -m pip install --no-cache-dir --no-compile --require-hashes --target=/opt/yt-dlp -r /requirements.txt

FROM docker.io/mwader/static-ffmpeg:9.0.1@sha256:54e55b0cb8f672870fc38ceb2e6c411855cb3b39c505f5f3b2505ee01ed5f2b7 AS ffmpeg
FROM gcr.io/distroless/python3-debian13:nonroot@sha256:8ee214843129f43e2ebf5e0ca9f2e4e6d8292143d1b8a6787f169b5898578884
COPY --from=build /reelrelay /usr/local/bin/reelrelay
COPY --from=downloader /opt/yt-dlp /opt/yt-dlp
COPY --from=ffmpeg /ffmpeg /ffprobe /usr/local/bin/
ENV YT_DLP_PATH=/opt/yt-dlp/bin/yt-dlp \
    PYTHONPATH=/opt/yt-dlp \
    PYTHONDONTWRITEBYTECODE=1 \
    HOME=/tmp
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/reelrelay"]
