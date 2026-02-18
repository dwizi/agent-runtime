package mcp

import (
	"context"
	"fmt"
	"net/http"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := h.base
	if base == nil {
		base = http.DefaultTransport
	}
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	for key, value := range h.headers {
		clone.Header.Set(key, value)
	}
	return base.RoundTrip(clone)
}

func newHTTPClient(cfg ServerConfig) *http.Client {
	timeout := time.Duration(cfg.HTTP.TimeoutSeconds) * time.Second
	if timeout < 1*time.Second {
		timeout = time.Duration(DefaultHTTPTimeoutSeconds) * time.Second
	}
	transport := &headerRoundTripper{headers: cfg.HTTP.Headers}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

func connectSession(ctx context.Context, cfg ServerConfig) (*sdkmcp.ClientSession, error) {
	transport, err := buildTransport(cfg)
	if err != nil {
		return nil, err
	}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "agent-runtime", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, err
	}
	return session, nil
}

func buildTransport(cfg ServerConfig) (sdkmcp.Transport, error) {
	httpClient := newHTTPClient(cfg)
	switch cfg.Transport.Type {
	case TransportStreamableHTTP:
		return &sdkmcp.StreamableClientTransport{
			Endpoint:   cfg.Transport.Endpoint,
			HTTPClient: httpClient,
		}, nil
	case TransportSSE:
		return &sdkmcp.SSEClientTransport{
			Endpoint:   cfg.Transport.Endpoint,
			HTTPClient: httpClient,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported mcp transport %q", cfg.Transport.Type)
	}
}
