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

FROM alpine:3.20 AS runtime
RUN apk add --no-cache ca-certificates curl git jq ripgrep
WORKDIR /
COPY --from=builder /out/agent-runtime /agent-runtime
ENTRYPOINT ["/agent-runtime"]
CMD ["serve"]
