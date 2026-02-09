package adminclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/carlos/spinner/internal/config"
)

type Client struct {
	baseURL string
	http    *http.Client
}

type Pairing struct {
	ID              string `json:"id"`
	TokenHint       string `json:"token_hint"`
	Connector       string `json:"connector"`
	ConnectorUserID string `json:"connector_user_id"`
	DisplayName     string `json:"display_name"`
	Status          string `json:"status"`
	ExpiresAtUnix   int64  `json:"expires_at_unix"`
	ApprovedUserID  string `json:"approved_user_id"`
	ApproverUserID  string `json:"approver_user_id"`
	DeniedReason    string `json:"denied_reason"`
}

type ApprovePairingResponse struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	ApprovedUserID  string `json:"approved_user_id"`
	ApproverUserID  string `json:"approver_user_id"`
	IdentityID      string `json:"identity_id"`
	Connector       string `json:"connector"`
	ConnectorUserID string `json:"connector_user_id"`
}

func New(cfg config.Config) (*Client, error) {
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: cfg.AdminTLSSkipVerify,
	}
	if cfg.AdminTLSCAFile != "" {
		caBytes, err := os.ReadFile(cfg.AdminTLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("read admin tls ca file: %w", err)
		}
		certPool := x509.NewCertPool()
		if ok := certPool.AppendCertsFromPEM(caBytes); !ok {
			return nil, fmt.Errorf("parse admin tls ca file")
		}
		tlsConfig.RootCAs = certPool
	}
	if cfg.AdminTLSCertFile != "" || cfg.AdminTLSKeyFile != "" {
		if cfg.AdminTLSCertFile == "" || cfg.AdminTLSKeyFile == "" {
			return nil, fmt.Errorf("both SPINNER_ADMIN_TLS_CERT_FILE and SPINNER_ADMIN_TLS_KEY_FILE are required")
		}
		clientCert, err := tls.LoadX509KeyPair(cfg.AdminTLSCertFile, cfg.AdminTLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load admin tls client cert: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{clientCert}
	}

	return &Client{
		baseURL: strings.TrimRight(cfg.AdminAPIURL, "/"),
		http: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: tlsConfig,
			},
			Timeout: 15 * time.Second,
		},
	}, nil
}

func (c *Client) LookupPairing(ctx context.Context, token string) (Pairing, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/pairings/lookup?token="+url.QueryEscape(token), nil)
	if err != nil {
		return Pairing{}, err
	}
	var pairing Pairing
	if err := c.doJSON(req, &pairing); err != nil {
		return Pairing{}, err
	}
	return pairing, nil
}

func (c *Client) ApprovePairing(ctx context.Context, token, approverUserID, role, targetUserID string) (ApprovePairingResponse, error) {
	payload := map[string]string{
		"token":            token,
		"approver_user_id": approverUserID,
		"role":             role,
		"target_user_id":   targetUserID,
	}
	requestBody, err := json.Marshal(payload)
	if err != nil {
		return ApprovePairingResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/pairings/approve", bytes.NewReader(requestBody))
	if err != nil {
		return ApprovePairingResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	var response ApprovePairingResponse
	if err := c.doJSON(req, &response); err != nil {
		return ApprovePairingResponse{}, err
	}
	return response, nil
}

func (c *Client) DenyPairing(ctx context.Context, token, approverUserID, reason string) (Pairing, error) {
	payload := map[string]string{
		"token":            token,
		"approver_user_id": approverUserID,
		"reason":           reason,
	}
	requestBody, err := json.Marshal(payload)
	if err != nil {
		return Pairing{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/pairings/deny", bytes.NewReader(requestBody))
	if err != nil {
		return Pairing{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	var response Pairing
	if err := c.doJSON(req, &response); err != nil {
		return Pairing{}, err
	}
	return response, nil
}

func (c *Client) doJSON(req *http.Request, out any) error {
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode >= http.StatusBadRequest {
		var apiError struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&apiError)
		if strings.TrimSpace(apiError.Error) == "" {
			apiError.Error = res.Status
		}
		return fmt.Errorf(apiError.Error)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
