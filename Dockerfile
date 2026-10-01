# syntax=docker/dockerfile:1
FROM golang:1.26-bookworm AS builder

WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/fileservice . \
    && mkdir -p /data

FROM scratch

COPY --from=builder /out/fileservice /fileservice
COPY --from=builder --chown=65532:65532 /data /data

ENV PORT=8080 \
    FILES_DIRECTORY=/data \
    MAX_FILE_SIZE=10MB \
    MAX_STORAGE_SIZE=1GB

VOLUME /data
EXPOSE 8080
USER 65532:65532

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
    CMD ["/fileservice", "-healthcheck"]

ENTRYPOINT ["/fileservice"]
