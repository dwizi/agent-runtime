# syntax=docker/dockerfile:1.7

FROM golang:1.23-bookworm AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/spinner ./cmd/spinner

FROM oven/bun:1.2.21-alpine AS qmd-runtime
RUN apk add --no-cache bash git ca-certificates && \
    bun install -g github:tobi/qmd && \
    qmd --help >/dev/null

FROM qmd-runtime AS spinner-runtime
WORKDIR /
COPY --from=builder /out/spinner /spinner
ENTRYPOINT ["/spinner"]
CMD ["serve"]
