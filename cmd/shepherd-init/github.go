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

package main

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	credsDir       = "/creds"
	tokenFilename  = "token"
	secretDir      = "/secrets/runner-app-key"
	keyFilename    = "private-key.pem"
	defaultBaseURL = "https://api.github.com"
)

// githubConfig holds all configuration needed for token generation.
type githubConfig struct {
	AppID          int64
	InstallationID int64
	BaseURL        string
	PrivateKeyPath string
	RepoURL        string // For token scoping
	TokenPath      string // Output path
}

func githubConfigFromEnv() (githubConfig, error) {
	appID, err := strconv.ParseInt(os.Getenv("GITHUB_APP_ID"), 10, 64)
	if err != nil {
		return githubConfig{}, fmt.Errorf("GITHUB_APP_ID invalid or missing: %w", err)
	}

	installID, err := strconv.ParseInt(os.Getenv("GITHUB_INSTALLATION_ID"), 10, 64)
	if err != nil {
		return githubConfig{}, fmt.Errorf("GITHUB_INSTALLATION_ID invalid or missing: %w", err)
	}

	baseURL := os.Getenv("GITHUB_API_URL")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	return githubConfig{
		AppID:          appID,
		InstallationID: installID,
		BaseURL:        baseURL,
		PrivateKeyPath: filepath.Join(secretDir, keyFilename),
		RepoURL:        os.Getenv("REPO_URL"),
		TokenPath:      filepath.Join(credsDir, tokenFilename),
	}, nil
}

func generateGitHubToken() error {
	logger.Info("generating GitHub installation token")

	cfg, err := githubConfigFromEnv()
	if err != nil {
		return err
	}

	key, err := readPrivateKey(cfg.PrivateKeyPath)
	if err != nil {
		return fmt.Errorf("reading private key: %w", err)
	}

	jwtToken, err := createJWT(cfg.AppID, key)
	if err != nil {
		return fmt.Errorf("creating JWT: %w", err)
	}

	repoName, err := parseRepoName(cfg.RepoURL)
	if err != nil {
		return fmt.Errorf("parsing repo URL: %w", err)
	}

	token, err := exchangeToken(cfg.BaseURL, cfg.InstallationID, jwtToken, repoName)
	if err != nil {
		return fmt.Errorf("exchanging token: %w", err)
	}

	if err := writeFile(cfg.TokenPath, []byte(token), 0600); err != nil {
		return fmt.Errorf("writing token: %w", err)
	}

	logger.Info("GitHub installation token generated successfully")
	return nil
}

// Note: The token is written with mode 0600 (owner read-write only). The Job spec must ensure
// both init and runner containers run as the same UID (via securityContext.runAsUser), or use
// group-readable permissions (0640) with a shared fsGroup in the pod security context. The
// operator's job_builder.go should enforce this to ensure the runner container can read the token.

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
			return nil, fmt.Errorf("parsing private key (tried PKCS1 and PKCS8): %w", err)
		}
		rsaKey, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key is not RSA")
		}
		return rsaKey, nil
	}
	return key, nil
}

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
// Returns error if repoURL is non-empty but malformed.
func parseRepoName(repoURL string) (string, error) {
	if repoURL == "" {
		return "", nil
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

// exchangeToken calls GitHub API to exchange JWT for an installation access token.
// If repoName is non-empty, the token is scoped to that repository only.
func exchangeToken(baseURL string, installationID int64, jwtToken, repoName string) (string, error) {
	endpoint := fmt.Sprintf("%s/app/installations/%d/access_tokens", baseURL, installationID)

	// Build request body — scope to repo if provided
	var bodyReader io.Reader
	if repoName != "" {
		body := map[string]any{
			"repositories": []string{repoName},
		}
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return "", fmt.Errorf("marshaling request body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequest("POST", endpoint, bodyReader)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "shepherd-init")
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST %s: %w", endpoint, err)
	}
	defer resp.Body.Close() //nolint:errcheck // Best-effort close on read-only response body

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}
	if result.Token == "" {
		return "", fmt.Errorf("empty token in response")
	}

	return result.Token, nil
}
