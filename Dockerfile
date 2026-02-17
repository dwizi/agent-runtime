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

FROM golang:1.25-alpine AS dev
WORKDIR /app
RUN apk add --no-cache ca-certificates git bash && \
    go install github.com/air-verse/air@v1.63.0
ENV PATH="/go/bin:${PATH}"
COPY go.mod go.sum ./
RUN go mod download
COPY . .
CMD ["air", "-c", ".air.toml"]

FROM alpine:3.20 AS runtime
RUN apk add --no-cache ca-certificates curl git jq ripgrep python3 chromium bash
WORKDIR /
COPY --from=builder /out/agent-runtime /agent-runtime
ENTRYPOINT ["/agent-runtime"]
CMD ["serve"]
