# syntax=docker/dockerfile:1

# --- Builder stage ---
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
    -o /bin/web-researcher-mcp \
    ./cmd/web-researcher-mcp

# --- Runtime stage ---
FROM alpine:3.23

RUN apk add --no-cache ca-certificates curl

LABEL org.opencontainers.image.title="web-researcher-mcp"
LABEL org.opencontainers.image.description="AI research without the made-up sources — you pick the sites, every citation is real"
LABEL org.opencontainers.image.source="https://github.com/zoharbabin/web-researcher-mcp"
LABEL org.opencontainers.image.licenses="MIT"

COPY --from=builder /bin/web-researcher-mcp /usr/local/bin/web-researcher-mcp
COPY lenses/ /lenses/

RUN mkdir -p /tmp/cache && chown 65534:65534 /tmp/cache

USER 65534:65534

ENV CACHE_DIR=/tmp/cache

ENTRYPOINT ["web-researcher-mcp"]
