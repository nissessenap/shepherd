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
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// readPrivateKey reads and parses an RSA private key from a PEM file.
// Supports both PKCS1 and PKCS8 formats.
func readPrivateKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading key file: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in %s", path)
	}

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS8 format as fallback
		parsed, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("parsing private key (PKCS1: %v; PKCS8: %w)", err, err2)
		}
		rsaKey, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key is not RSA")
		}
		return rsaKey, nil
	}
	return key, nil
}

// createJWT creates a GitHub App JWT signed with the given RSA private key.
func createJWT(appID int64, key *rsa.PrivateKey) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Issuer:    strconv.FormatInt(appID, 10),
		IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)), // Clock drift tolerance
		ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),  // GitHub max: 10 min
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(key)
}

// parseRepoName extracts "repo" from "https://github.com/org/repo.git" or "https://github.com/org/repo".
func parseRepoName(repoURL string) (string, error) {
	if repoURL == "" {
		return "", fmt.Errorf("repo URL is required")
	}
	u, err := url.Parse(repoURL)
	if err != nil {
		return "", fmt.Errorf("invalid repo URL: %w", err)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 {
		return "", fmt.Errorf("repo URL must be owner/repo format: %s", repoURL)
	}
	name := parts[1]
	return strings.TrimSuffix(name, ".git"), nil
}

// exchangeToken calls GitHub API to exchange a JWT for an installation access token.
// If repoName is non-empty, the token is scoped to that repository only.
func exchangeToken(ctx context.Context, httpClient *http.Client, baseURL string, installationID int64, jwtToken, repoName string) (string, string, error) {
	endpoint := fmt.Sprintf("%s/app/installations/%d/access_tokens", baseURL, installationID)

	var bodyReader io.Reader
	if repoName != "" {
		body := map[string]any{
			"repositories": []string{repoName},
		}
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return "", "", fmt.Errorf("marshaling request body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bodyReader)
	if err != nil {
		return "", "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "shepherd-api")
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("POST %s: %w", endpoint, err)
	}
	defer resp.Body.Close() //nolint:errcheck // Best-effort close on read-only response body

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusCreated {
		// Truncate error body to prevent excessive log sizes
		errBody := string(respBody)
		if len(errBody) > 200 {
			errBody = errBody[:200] + "..."
		}
		return "", "", fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, errBody)
	}

	var result struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", "", fmt.Errorf("parsing response: %w", err)
	}
	if result.Token == "" {
		return "", "", fmt.Errorf("empty token in response")
	}

	return result.Token, result.ExpiresAt, nil
}
