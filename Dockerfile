# syntax=docker/dockerfile:1.7

FROM golang:1.25-alpine AS builder
WORKDIR /src
ARG TARGETOS
ARG TARGETARCH

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -o /out/agent-runtime ./cmd/agent-runtime

FROM ghcr.io/astral-sh/uv:0.9.28 AS uv
FROM oven/bun:1.2.22-alpine AS bun

FROM golang:1.25-alpine AS dev
WORKDIR /app
RUN apk add --no-cache ca-certificates git bash python3 nodejs npm && \
    go install github.com/air-verse/air@v1.63.0
COPY --from=uv /uv /usr/local/bin/uv
COPY --from=uv /uvx /usr/local/bin/uvx
COPY --from=bun /usr/local/bin/bun /usr/local/bin/bun
COPY --from=bun /usr/local/bin/bunx /usr/local/bin/bunx
ENV PATH="/go/bin:${PATH}"
COPY go.mod go.sum ./
RUN go mod download
COPY . .
CMD ["air", "-c", ".air.toml"]

FROM alpine:3.20 AS runtime
RUN apk add --no-cache ca-certificates curl git jq ripgrep python3 chromium bash nodejs npm
COPY --from=uv /uv /usr/local/bin/uv
COPY --from=uv /uvx /usr/local/bin/uvx
COPY --from=bun /usr/local/bin/bun /usr/local/bin/bun
COPY --from=bun /usr/local/bin/bunx /usr/local/bin/bunx
WORKDIR /
COPY --from=builder /out/agent-runtime /agent-runtime
ENTRYPOINT ["/agent-runtime"]
CMD ["serve"]
