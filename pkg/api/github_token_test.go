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

package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRepoName(t *testing.T) {
	tests := []struct {
		name      string
		repoURL   string
		want      string
		wantError bool
		errorMsg  string
	}{
		{
			name:    "standard URL",
			repoURL: "https://github.com/org/repo",
			want:    "repo",
		},
		{
			name:    "URL with .git suffix",
			repoURL: "https://github.com/org/repo.git",
			want:    "repo",
		},
		{
			name:      "empty string",
			repoURL:   "",
			wantError: true,
			errorMsg:  "repo URL is required",
		},
		{
			name:      "single path segment",
			repoURL:   "https://github.com/org",
			wantError: true,
			errorMsg:  "repo URL must be owner/repo format",
		},
		{
			name:      "too many segments",
			repoURL:   "https://github.com/a/b/c",
			wantError: true,
			errorMsg:  "repo URL must be owner/repo format",
		},
		{
			name:      "invalid URL",
			repoURL:   "://invalid",
			wantError: true,
			errorMsg:  "invalid repo URL",
		},
		{
			name:    "URL with trailing slash",
			repoURL: "https://github.com/org/repo/",
			want:    "repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRepoName(tt.repoURL)
			if tt.wantError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGitHubClient_GetToken(t *testing.T) {
	// Generate a test RSA private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	// Encode private key to PEM format
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	// Track the request to verify repository scoping
	var receivedRepoRequest string
	var requestCount int

	// Create test server that mimics GitHub's token endpoint
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++

		// GitHub expects POST to /app/installations/{id}/access_tokens
		if !strings.HasPrefix(r.URL.Path, "/app/installations/") ||
			!strings.HasSuffix(r.URL.Path, "/access_tokens") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		// Read request body to check repository scoping
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Capture request body for verification
		receivedRepoRequest = string(body)

		// Return a token response (GitHub returns 201 Created)
		w.WriteHeader(http.StatusCreated)
		resp := map[string]string{
			"token":      "ghs_test_installation_token",
			"expires_at": "2026-02-08T13:00:00Z",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	// Create AppsTransport with test server URL
	atr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, 12345, privateKeyPEM)
	require.NoError(t, err)
	atr.BaseURL = ts.URL

	// Create GitHubClient
	client := &GitHubClient{
		appsTransport:  atr,
		installationID: 67890,
	}

	// Test getting a token scoped to a repository
	ctx := context.Background()
	token, expiresAt, err := client.GetToken(ctx, "https://github.com/myorg/myrepo.git")

	require.NoError(t, err)
	assert.Equal(t, "ghs_test_installation_token", token)
	assert.False(t, expiresAt.IsZero(), "expiresAt should not be zero")

	// Verify the request body included repository scoping
	assert.Contains(t, receivedRepoRequest, `"repositories":["myrepo"]`,
		"request should include repository scoping")

	// Verify at least one request was made
	assert.GreaterOrEqual(t, requestCount, 1, "should have made at least one request to token endpoint")
}

func TestGitHubClient_GetToken_EmptyRepoURL(t *testing.T) {
	// Generate a test RSA private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	var requestCount int

	// Create test server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++

		if !strings.HasPrefix(r.URL.Path, "/app/installations/") ||
			!strings.HasSuffix(r.URL.Path, "/access_tokens") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		w.WriteHeader(http.StatusCreated)
		resp := map[string]string{
			"token":      "ghs_test_installation_token",
			"expires_at": "2026-02-08T13:00:00Z",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	atr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, 12345, privateKeyPEM)
	require.NoError(t, err)
	atr.BaseURL = ts.URL

	client := &GitHubClient{
		appsTransport:  atr,
		installationID: 67890,
	}

	// Test getting a token without repository scoping (empty repoURL)
	ctx := context.Background()
	token, expiresAt, err := client.GetToken(ctx, "")

	require.NoError(t, err)
	assert.Equal(t, "ghs_test_installation_token", token)
	assert.False(t, expiresAt.IsZero(), "expiresAt should not be zero")

	// Verify at least one request was made
	assert.GreaterOrEqual(t, requestCount, 1, "should have made at least one request to token endpoint")
}

func TestGitHubClient_GetToken_InvalidRepoURL(t *testing.T) {
	// Generate a test RSA private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	// No need for test server since we should fail before making a request
	atr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, 12345, privateKeyPEM)
	require.NoError(t, err)

	client := &GitHubClient{
		appsTransport:  atr,
		installationID: 67890,
	}

	// Test with invalid repo URL
	ctx := context.Background()
	_, _, err = client.GetToken(ctx, "https://github.com/org")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "repo URL must be owner/repo format")
}

func TestGitHubClient_GetToken_APIError(t *testing.T) {
	// Generate a test RSA private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	// Create test server that returns an error
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/app/installations/") &&
			strings.HasSuffix(r.URL.Path, "/access_tokens") {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer ts.Close()

	atr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, 12345, privateKeyPEM)
	require.NoError(t, err)
	atr.BaseURL = ts.URL

	client := &GitHubClient{
		appsTransport:  atr,
		installationID: 67890,
	}

	// Test getting a token when API returns error
	ctx := context.Background()
	_, _, err = client.GetToken(ctx, "https://github.com/org/repo")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "getting installation token")
}
