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
FROM alpine:3.19

RUN apk add --no-cache ca-certificates curl

LABEL org.opencontainers.image.title="web-researcher-mcp"
LABEL org.opencontainers.image.description="MCP server providing web search, content extraction, and multi-source research tools"
LABEL org.opencontainers.image.source="https://github.com/zoharbabin/web-researcher-mcp"
LABEL org.opencontainers.image.licenses="MIT"

COPY --from=builder /bin/web-researcher-mcp /usr/local/bin/web-researcher-mcp

USER 65534:65534

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/health/live || exit 1

ENTRYPOINT ["web-researcher-mcp"]
