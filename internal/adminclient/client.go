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

	"github.com/dwizi/agent-runtime/internal/config"
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

type StartPairingResponse struct {
	ID              string `json:"id"`
	Token           string `json:"token"`
	TokenHint       string `json:"token_hint"`
	Connector       string `json:"connector"`
	ConnectorUserID string `json:"connector_user_id"`
	DisplayName     string `json:"display_name"`
	Status          string `json:"status"`
	ExpiresAtUnix   int64  `json:"expires_at_unix"`
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

type Objective struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	ContextID   string `json:"context_id"`
	Title       string `json:"title"`
	Prompt      string `json:"prompt"`
	TriggerType string `json:"trigger_type"`
	EventKey    string `json:"event_key"`
	CronExpr    string `json:"cron_expr"`
	Timezone    string `json:"timezone"`
	Active      bool   `json:"active"`
	NextRunUnix *int64 `json:"next_run_unix"`
	LastRunUnix *int64 `json:"last_run_unix"`
	LastError   string `json:"last_error"`

	RunCount             int    `json:"run_count"`
	SuccessCount         int    `json:"success_count"`
	FailureCount         int    `json:"failure_count"`
	ConsecutiveFailures  int    `json:"consecutive_failures"`
	ConsecutiveSuccesses int    `json:"consecutive_successes"`
	TotalRunDurationMs   int64  `json:"total_run_duration_ms"`
	AvgRunDurationMs     int64  `json:"avg_run_duration_ms"`
	LastSuccessUnix      *int64 `json:"last_success_unix"`
	LastFailureUnix      *int64 `json:"last_failure_unix"`
}

type ListObjectivesResponse struct {
	Items []Objective `json:"items"`
	Count int         `json:"count"`
}

type Task struct {
	ID             string `json:"id"`
	WorkspaceID    string `json:"workspace_id"`
	ContextID      string `json:"context_id"`
	Kind           string `json:"kind"`
	Title          string `json:"title"`
	Prompt         string `json:"prompt"`
	Status         string `json:"status"`
	Attempts       int    `json:"attempts"`
	WorkerID       int    `json:"worker_id"`
	StartedAtUnix  int64  `json:"started_at_unix"`
	FinishedAtUnix int64  `json:"finished_at_unix"`
	ResultSummary  string `json:"result_summary"`
	ResultPath     string `json:"result_path"`
	ErrorMessage   string `json:"error_message"`
	CreatedAtUnix  int64  `json:"created_at_unix"`
	UpdatedAtUnix  int64  `json:"updated_at_unix"`
}

type ListTasksResponse struct {
	Items []Task `json:"items"`
	Count int    `json:"count"`
}

type RetryTaskResponse struct {
	TaskID      string `json:"task_id"`
	RetryOfTask string `json:"retry_of_task"`
	WorkspaceID string `json:"workspace_id"`
	ContextID   string `json:"context_id"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
}

type ChatRequest struct {
	Connector   string `json:"connector"`
	ExternalID  string `json:"external_id"`
	DisplayName string `json:"display_name"`
	FromUserID  string `json:"from_user_id"`
	Text        string `json:"text"`
}

type ChatResponse struct {
	Handled bool   `json:"handled"`
	Reply   string `json:"reply"`
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
			return nil, fmt.Errorf("both AGENT_RUNTIME_ADMIN_TLS_CERT_FILE and AGENT_RUNTIME_ADMIN_TLS_KEY_FILE are required")
		}
		clientCert, err := tls.LoadX509KeyPair(cfg.AdminTLSCertFile, cfg.AdminTLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load admin tls client cert: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{clientCert}
	}

	timeout := time.Duration(cfg.AdminHTTPTimeoutSec) * time.Second
	if timeout < time.Second {
		timeout = 120 * time.Second
	}

	return &Client{
		baseURL: strings.TrimRight(cfg.AdminAPIURL, "/"),
		http: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: tlsConfig,
			},
			Timeout: timeout,
		},
	}, nil
}

func (c *Client) WithTimeout(timeout time.Duration) *Client {
	if c == nil {
		return nil
	}
	if timeout < time.Second {
		return c
	}
	clone := *c
	if c.http == nil {
		clone.http = &http.Client{Timeout: timeout}
		return &clone
	}
	httpClone := *c.http
	httpClone.Timeout = timeout
	clone.http = &httpClone
	return &clone
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

func (c *Client) StartPairing(ctx context.Context, connector, connectorUserID, displayName string, expiresInSec int) (StartPairingResponse, error) {
	payload := map[string]any{
		"connector":         strings.TrimSpace(connector),
		"connector_user_id": strings.TrimSpace(connectorUserID),
		"display_name":      strings.TrimSpace(displayName),
	}
	if expiresInSec > 0 {
		payload["expires_in_sec"] = expiresInSec
	}
	requestBody, err := json.Marshal(payload)
	if err != nil {
		return StartPairingResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/pairings/start", bytes.NewReader(requestBody))
	if err != nil {
		return StartPairingResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	var response StartPairingResponse
	if err := c.doJSON(req, &response); err != nil {
		return StartPairingResponse{}, err
	}
	return response, nil
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

func (c *Client) ListObjectives(ctx context.Context, workspaceID string, activeOnly bool, limit int) ([]Objective, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace id is required")
	}
	query := url.Values{}
	query.Set("workspace_id", workspaceID)
	if !activeOnly {
		query.Set("active_only", "false")
	}
	if limit > 0 {
		query.Set("limit", fmt.Sprintf("%d", limit))
	}
	endpoint := c.baseURL + "/api/v1/objectives?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	var response ListObjectivesResponse
	if err := c.doJSON(req, &response); err != nil {
		return nil, err
	}
	return response.Items, nil
}

func (c *Client) SetObjectiveActive(ctx context.Context, objectiveID string, active bool) (Objective, error) {
	payload := map[string]any{
		"id":     strings.TrimSpace(objectiveID),
		"active": active,
	}
	requestBody, err := json.Marshal(payload)
	if err != nil {
		return Objective{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/objectives/active", bytes.NewReader(requestBody))
	if err != nil {
		return Objective{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	var response Objective
	if err := c.doJSON(req, &response); err != nil {
		return Objective{}, err
	}
	return response, nil
}

func (c *Client) DeleteObjective(ctx context.Context, objectiveID string) error {
	payload := map[string]any{
		"id": strings.TrimSpace(objectiveID),
	}
	requestBody, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/objectives/delete", bytes.NewReader(requestBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doJSON(req, nil)
}

func (c *Client) ListTasks(ctx context.Context, workspaceID, status string, limit int) ([]Task, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace id is required")
	}
	query := url.Values{}
	query.Set("workspace_id", workspaceID)
	if strings.TrimSpace(status) != "" {
		query.Set("status", strings.TrimSpace(status))
	}
	if limit > 0 {
		query.Set("limit", fmt.Sprintf("%d", limit))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/tasks?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var response ListTasksResponse
	if err := c.doJSON(req, &response); err != nil {
		return nil, err
	}
	return response.Items, nil
}

func (c *Client) RetryTask(ctx context.Context, taskID string) (RetryTaskResponse, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return RetryTaskResponse{}, fmt.Errorf("task id is required")
	}
	payload := map[string]string{
		"task_id": taskID,
	}
	requestBody, err := json.Marshal(payload)
	if err != nil {
		return RetryTaskResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/tasks/retry", bytes.NewReader(requestBody))
	if err != nil {
		return RetryTaskResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	var response RetryTaskResponse
	if err := c.doJSON(req, &response); err != nil {
		return RetryTaskResponse{}, err
	}
	return response, nil
}

func (c *Client) Chat(ctx context.Context, input ChatRequest) (ChatResponse, error) {
	input.Text = strings.TrimSpace(input.Text)
	if input.Text == "" {
		return ChatResponse{}, fmt.Errorf("text is required")
	}
	requestBody, err := json.Marshal(input)
	if err != nil {
		return ChatResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/chat", bytes.NewReader(requestBody))
	if err != nil {
		return ChatResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	var response ChatResponse
	if err := c.doJSON(req, &response); err != nil {
		return ChatResponse{}, err
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
