# syntax=docker/dockerfile:1

# --- Builder stage ---
FROM golang:1.27-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS builder

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
FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6

RUN apk add --no-cache \
    ca-certificates \
    curl \
    chromium \
    font-noto \
    font-noto-cjk \
    font-noto-emoji \
    harfbuzz \
    nss \
    freetype \
    ttf-freefont \
    && rm -rf /var/cache/apk/*

LABEL org.opencontainers.image.title="web-researcher-mcp"
LABEL org.opencontainers.image.description="Your AI research assistant that cites real sources and stays honest"
LABEL org.opencontainers.image.source="https://github.com/zoharbabin/web-researcher-mcp"
LABEL org.opencontainers.image.licenses="MIT"

COPY --from=builder /bin/web-researcher-mcp /usr/local/bin/web-researcher-mcp
COPY lenses/ /lenses/

RUN mkdir -p /tmp/cache && chown 65534:65534 /tmp/cache

USER 65534:65534

ENV CACHE_DIR=/tmp/cache
ENV CHROME_PATH=/usr/bin/chromium-browser

ENTRYPOINT ["web-researcher-mcp"]
