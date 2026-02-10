package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-logr/logr"
)

// APIClient communicates with the shepherd API server.
type APIClient interface {
	FetchTaskData(ctx context.Context, taskID string) (*TaskData, error)
	FetchToken(ctx context.Context, taskID string) (token string, expiresAt time.Time, err error)
	ReportStatus(ctx context.Context, taskID string, event, message string, details map[string]any) error
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithHTTPClient sets the HTTP client used for API requests.
func WithHTTPClient(c *http.Client) ClientOption {
	return func(cl *Client) { cl.httpClient = c }
}

// WithClientLogger sets the logger for the client.
func WithClientLogger(l logr.Logger) ClientOption {
	return func(cl *Client) { cl.logger = l }
}

// Client implements APIClient for the shepherd API server.
type Client struct {
	baseURL    string
	httpClient *http.Client
	logger     logr.Logger
}

// NewClient creates an API client for the given base URL.
func NewClient(baseURL string, opts ...ClientOption) *Client {
	c := &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     logr.Discard(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// taskDataResponse mirrors pkg/api.TaskDataResponse for JSON decoding.
type taskDataResponse struct {
	Description string `json:"description"`
	Context     string `json:"context"`
	SourceURL   string `json:"sourceURL,omitempty"`
	Repo        struct {
		URL string `json:"url"`
		Ref string `json:"ref,omitempty"`
	} `json:"repo"`
}

// tokenResponse mirrors pkg/api.TokenResponse for JSON decoding.
type tokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
}

// statusUpdateRequest mirrors pkg/api.StatusUpdateRequest for JSON encoding.
type statusUpdateRequest struct {
	Event   string         `json:"event"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// errorResponse mirrors pkg/api.ErrorResponse for JSON decoding.
type errorResponse struct {
	Error   string `json:"error"`
	Details string `json:"details,omitempty"`
}

// FetchTaskData retrieves task details from the API.
func (c *Client) FetchTaskData(ctx context.Context, taskID string) (*TaskData, error) {
	url := c.baseURL + "/api/v1/tasks/" + taskID + "/data"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching task data: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		// success, parse below
	case http.StatusNotFound:
		return nil, fmt.Errorf("task %s not found", taskID)
	case http.StatusGone:
		return nil, fmt.Errorf("task %s is terminal", taskID)
	default:
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var data taskDataResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("decoding task data: %w", err)
	}

	return &TaskData{
		TaskID:      taskID,
		APIURL:      c.baseURL,
		Description: data.Description,
		Context:     data.Context,
		SourceURL:   data.SourceURL,
		RepoURL:     data.Repo.URL,
		RepoRef:     data.Repo.Ref,
	}, nil
}

// FetchToken retrieves a GitHub installation token.
// Returns a fatal error on 409 Conflict (token already issued, non-retriable).
func (c *Client) FetchToken(ctx context.Context, taskID string) (string, time.Time, error) {
	url := c.baseURL + "/api/v1/tasks/" + taskID + "/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("fetching token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("reading response body: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		// success, parse below
	case http.StatusConflict:
		return "", time.Time{}, fmt.Errorf("token already issued for task %s (non-retriable)", taskID)
	default:
		return "", time.Time{}, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var tok tokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", time.Time{}, fmt.Errorf("decoding token response: %w", err)
	}

	expiresAt, err := time.Parse(time.RFC3339, tok.ExpiresAt)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("parsing expiresAt: %w", err)
	}

	return tok.Token, expiresAt, nil
}

// ReportStatus sends a status update to the API.
func (c *Client) ReportStatus(ctx context.Context, taskID string, event, message string, details map[string]any) error {
	url := c.baseURL + "/api/v1/tasks/" + taskID + "/status"

	payload := statusUpdateRequest{
		Event:   event,
		Message: message,
		Details: details,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling status update: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("reporting status: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
