/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/NissesSenap/shepherd/pkg/api"
)

const unknownErrorMessage = "unknown error"

// APIClient communicates with the Shepherd API.
type APIClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewAPIClient creates a new API client.
func NewAPIClient(baseURL string) *APIClient {
	return &APIClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// GetActiveTasks queries for active tasks matching the given labels.
func (c *APIClient) GetActiveTasks(ctx context.Context, repoLabel, issueLabel string) ([]api.TaskResponse, error) {
	u, err := url.Parse(c.baseURL + "/api/v1/tasks")
	if err != nil {
		return nil, fmt.Errorf("parsing URL: %w", err)
	}

	q := u.Query()
	q.Set("repo", repoLabel)
	q.Set("issue", issueLabel)
	q.Set("active", "true")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp api.ErrorResponse
		if err := json.Unmarshal(body, &errResp); err != nil || errResp.Error == "" {
			msg := string(bytes.TrimSpace(body))
			if len(msg) > 1024 {
				msg = msg[:1024]
			}
			if msg == "" {
				msg = unknownErrorMessage
			}
			return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, errResp.Error)
	}

	var tasks []api.TaskResponse
	if err := json.Unmarshal(body, &tasks); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return tasks, nil
}

// GetTask fetches a single task by ID. Used by CallbackHandler to resolve
// task metadata for callbacks received after a restart (stateless recovery).
func (c *APIClient) GetTask(ctx context.Context, taskID string) (*api.TaskResponse, error) {
	reqURL := c.baseURL + "/api/v1/tasks/" + url.PathEscape(taskID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp api.ErrorResponse
		if err := json.Unmarshal(body, &errResp); err != nil || errResp.Error == "" {
			msg := string(bytes.TrimSpace(body))
			if len(msg) > 1024 {
				msg = msg[:1024]
			}
			if msg == "" {
				msg = unknownErrorMessage
			}
			return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, errResp.Error)
	}

	var task api.TaskResponse
	if err := json.Unmarshal(body, &task); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return &task, nil
}

// CreateTask creates a new task via the API.
func (c *APIClient) CreateTask(ctx context.Context, createReq api.CreateTaskRequest) (*api.TaskResponse, error) {
	body, err := json.Marshal(createReq)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/tasks", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusCreated {
		var errResp api.ErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err != nil || errResp.Error == "" {
			msg := string(bytes.TrimSpace(respBody))
			if len(msg) > 1024 {
				msg = msg[:1024]
			}
			if msg == "" {
				msg = unknownErrorMessage
			}
			return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, errResp.Error)
	}

	var taskResp api.TaskResponse
	if err := json.Unmarshal(respBody, &taskResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return &taskResp, nil
}
