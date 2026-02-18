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

FROM golang:1.25-alpine AS dev
WORKDIR /app
RUN apk add --no-cache ca-certificates git bash python3 && \
    go install github.com/air-verse/air@v1.63.0
COPY --from=uv /uv /usr/local/bin/uv
COPY --from=uv /uvx /usr/local/bin/uvx
ENV PATH="/go/bin:${PATH}"
COPY go.mod go.sum ./
RUN go mod download
COPY . .
CMD ["air", "-c", ".air.toml"]

FROM alpine:3.20 AS runtime
RUN apk add --no-cache ca-certificates curl git jq ripgrep python3 chromium bash
COPY --from=uv /uv /usr/local/bin/uv
COPY --from=uv /uvx /usr/local/bin/uvx
WORKDIR /
COPY --from=builder /out/agent-runtime /agent-runtime
ENTRYPOINT ["/agent-runtime"]
CMD ["serve"]
