# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
ENV GOMAXPROCS=2 GOMEMLIMIT=768MiB
COPY go.mod go.sum ./
RUN go mod download
# Everything else in the build context (.dockerignore keeps out .env, data and keys).
COPY . .
# Keep compiler caches off the small EC2 root disk. Dependencies remain cached above.
RUN --mount=type=tmpfs,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -p 1 -trimpath -ldflags="-s -w" -o /out/compute-box .

FROM alpine:3.23
RUN apk add --no-cache ca-certificates \
    && addgroup -g 10001 sonic \
    && adduser -D -u 10001 -G sonic sonic \
    && mkdir /data && chown sonic:sonic /data
WORKDIR /app
COPY --from=build /out/compute-box /app/compute-box
COPY scripts ./scripts
ENV SONIC_ADDR=0.0.0.0:8080 SONIC_DATA_DIR=/data SONIC_SCRIPTS_DIR=/app/scripts
USER sonic
EXPOSE 8080
ENTRYPOINT ["/app/compute-box"]
