# Filestash (this fork) built from the local source tree, with a recent rclone
# for iCloud / OneDrive / extra Dropbox & Google Drive accounts.
FROM rclone/rclone:latest AS rclone

FROM golang:1.26-trixie AS builder
WORKDIR /src
RUN apt-get update > /dev/null && \
    apt-get install -y --no-install-recommends curl make git \
        libjpeg-dev libtiff-dev libpng-dev libwebp-dev libraw-dev libheif-dev libgif-dev libvips-dev \
        libavcodec-dev libavdevice-dev libavfilter-dev libavformat-dev libswresample-dev libswscale-dev libavutil-dev > /dev/null
COPY . .
RUN sh home/prepare.sh && go build --tags "fts5" -o dist/filestash cmd/main.go

FROM debian:stable-slim
WORKDIR /app/
RUN apt-get update > /dev/null && \
    apt-get -y upgrade > /dev/null && \
    apt-get install -y --no-install-recommends ca-certificates curl ffmpeg libbrotli1 poppler-utils fuse3 > /dev/null && \
    rm -rf /var/lib/apt/lists/* /tmp/*
COPY --from=builder /src/dist/filestash /app/filestash
COPY --from=rclone /usr/local/bin/rclone /usr/local/bin/rclone
COPY home/entrypoint.sh /app/entrypoint.sh
RUN chmod 755 /app/filestash /app/entrypoint.sh && mkdir -p /app/data/state /data/home /config/rclone && \
    useradd -u 1000 -m filestash && chown -R filestash:filestash /app /data /config
USER filestash
ENV RCLONE_CONFIG=/config/rclone/rclone.conf
EXPOSE 8334
ENTRYPOINT ["/app/entrypoint.sh"]
