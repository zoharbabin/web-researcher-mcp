# syntax=docker/dockerfile:1

# --- Builder stage ---
FROM golang:1.27-alpine@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS builder

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
FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a

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
LABEL org.opencontainers.image.description="Search the web and primary sources, read pages behind your logins, verify citations. Runs locally."
LABEL org.opencontainers.image.source="https://github.com/zoharbabin/web-researcher-mcp"
LABEL org.opencontainers.image.licenses="MIT"

COPY --from=builder /bin/web-researcher-mcp /usr/local/bin/web-researcher-mcp
COPY lenses/ /lenses/

RUN mkdir -p /tmp/cache && chown 65534:65534 /tmp/cache

USER 65534:65534

ENV CACHE_DIR=/tmp/cache
ENV CHROME_PATH=/usr/bin/chromium-browser
# The container cannot run Chrome's sandbox as a non-root UID; the container is the boundary.
ENV CHROME_NO_SANDBOX=true

ENTRYPOINT ["web-researcher-mcp"]
