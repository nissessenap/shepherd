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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateTestKey creates a 2048-bit RSA key for testing.
func generateTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}

// writePKCS1Key writes an RSA private key in PKCS1 PEM format to a temp file.
func writePKCS1Key(t *testing.T, dir string, key *rsa.PrivateKey) string {
	t.Helper()
	path := filepath.Join(dir, "private-key.pem")
	data := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	require.NoError(t, os.WriteFile(path, data, 0600))
	return path
}

// writePKCS8Key writes an RSA private key in PKCS8 PEM format to a temp file.
func writePKCS8Key(t *testing.T, dir string, key *rsa.PrivateKey) string {
	t.Helper()
	path := filepath.Join(dir, "private-key.pem")
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	data := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: der,
	})
	require.NoError(t, os.WriteFile(path, data, 0600))
	return path
}

// --- parseRepoName tests ---

func TestParseRepoName_WithGitSuffix(t *testing.T) {
	name, err := parseRepoName("https://github.com/org/repo.git")
	require.NoError(t, err)
	assert.Equal(t, "repo", name)
}

func TestParseRepoName_WithoutGitSuffix(t *testing.T) {
	name, err := parseRepoName("https://github.com/org/repo")
	require.NoError(t, err)
	assert.Equal(t, "repo", name)
}

func TestParseRepoName_EmptyURL(t *testing.T) {
	name, err := parseRepoName("")
	require.NoError(t, err)
	assert.Equal(t, "", name)
}

func TestParseRepoName_MalformedURL_OnlyOrg(t *testing.T) {
	_, err := parseRepoName("https://github.com/org")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "owner/repo")
}

func TestParseRepoName_HostOnly(t *testing.T) {
	_, err := parseRepoName("https://github.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "owner/repo")
}

func TestParseRepoName_ExtraPathSegments(t *testing.T) {
	_, err := parseRepoName("https://github.com/org/repo/tree/main")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "owner/repo")
}

// --- readPrivateKey tests ---

func TestReadPrivateKey_PKCS1(t *testing.T) {
	key := generateTestKey(t)
	dir := t.TempDir()
	path := writePKCS1Key(t, dir, key)

	got, err := readPrivateKey(path)
	require.NoError(t, err)
	assert.True(t, key.Equal(got))
}

func TestReadPrivateKey_PKCS8(t *testing.T) {
	key := generateTestKey(t)
	dir := t.TempDir()
	path := writePKCS8Key(t, dir, key)

	got, err := readPrivateKey(path)
	require.NoError(t, err)
	assert.True(t, key.Equal(got))
}

func TestReadPrivateKey_NonPEMFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not-pem.txt")
	require.NoError(t, os.WriteFile(path, []byte("not a PEM file"), 0600))

	_, err := readPrivateKey(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no PEM block found")
}

func TestReadPrivateKey_FileNotFound(t *testing.T) {
	_, err := readPrivateKey("/nonexistent/path/key.pem")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading key file")
}

func TestReadPrivateKey_NonRSAKey(t *testing.T) {
	// Write an EC key in PKCS8 format — readPrivateKey should reject it
	dir := t.TempDir()
	path := filepath.Join(dir, "ec-key.pem")

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(ecKey)
	require.NoError(t, err)

	data := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: pkcs8Bytes,
	})
	require.NoError(t, os.WriteFile(path, data, 0600))

	_, err = readPrivateKey(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "private key is not RSA")
}

// --- createJWT tests ---

func TestCreateJWT_ProducesValidRS256Token(t *testing.T) {
	key := generateTestKey(t)
	appID := int64(12345)

	tokenStr, err := createJWT(appID, key)
	require.NoError(t, err)

	// Parse and verify
	token, err := jwt.ParseWithClaims(tokenStr, &jwt.RegisteredClaims{}, func(tok *jwt.Token) (interface{}, error) {
		assert.Equal(t, jwt.SigningMethodRS256, tok.Method)
		return &key.PublicKey, nil
	})
	require.NoError(t, err)
	assert.True(t, token.Valid)
}

func TestCreateJWT_CorrectIssuer(t *testing.T) {
	key := generateTestKey(t)
	appID := int64(12345)

	tokenStr, err := createJWT(appID, key)
	require.NoError(t, err)

	token, err := jwt.ParseWithClaims(tokenStr, &jwt.RegisteredClaims{}, func(t *jwt.Token) (interface{}, error) {
		return &key.PublicKey, nil
	})
	require.NoError(t, err)

	claims := token.Claims.(*jwt.RegisteredClaims)
	assert.Equal(t, "12345", claims.Issuer)
}

func TestCreateJWT_IssuedAt60SecondsInPast(t *testing.T) {
	key := generateTestKey(t)
	before := time.Now().Add(-60 * time.Second)

	tokenStr, err := createJWT(42, key)
	require.NoError(t, err)

	after := time.Now().Add(-60 * time.Second)

	token, err := jwt.ParseWithClaims(tokenStr, &jwt.RegisteredClaims{}, func(t *jwt.Token) (interface{}, error) {
		return &key.PublicKey, nil
	})
	require.NoError(t, err)

	claims := token.Claims.(*jwt.RegisteredClaims)
	iat := claims.IssuedAt.Time
	assert.True(t, !iat.Before(before.Add(-time.Second)), "iat %v should be >= %v", iat, before)
	assert.True(t, !iat.After(after.Add(time.Second)), "iat %v should be <= %v", iat, after)
}

func TestCreateJWT_ExpiresIn10Minutes(t *testing.T) {
	key := generateTestKey(t)
	before := time.Now().Add(10 * time.Minute)

	tokenStr, err := createJWT(42, key)
	require.NoError(t, err)

	after := time.Now().Add(10 * time.Minute)

	token, err := jwt.ParseWithClaims(tokenStr, &jwt.RegisteredClaims{}, func(t *jwt.Token) (interface{}, error) {
		return &key.PublicKey, nil
	})
	require.NoError(t, err)

	claims := token.Claims.(*jwt.RegisteredClaims)
	exp := claims.ExpiresAt.Time
	assert.True(t, !exp.Before(before.Add(-time.Second)), "exp %v should be >= %v", exp, before)
	assert.True(t, !exp.After(after.Add(time.Second)), "exp %v should be <= %v", exp, after)
}

// --- exchangeToken tests ---

func TestExchangeToken_SuccessWithRepoScope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify method and path
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/app/installations/999/access_tokens", r.URL.Path)

		// Verify headers
		assert.Equal(t, "Bearer test-jwt", r.Header.Get("Authorization"))
		assert.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"))
		assert.Equal(t, "shepherd-init", r.Header.Get("User-Agent"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		// Verify body has repositories scope
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var payload map[string]interface{}
		require.NoError(t, json.Unmarshal(body, &payload))
		repos, ok := payload["repositories"].([]interface{})
		require.True(t, ok)
		assert.Equal(t, []interface{}{"my-repo"}, repos)

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"token": "ghs_test_token_123"})
	}))
	defer srv.Close()

	token, err := exchangeToken(srv.URL, 999, "test-jwt", "my-repo")
	require.NoError(t, err)
	assert.Equal(t, "ghs_test_token_123", token)
}

func TestExchangeToken_SuccessWithoutRepoScope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify no Content-Type header (no body)
		assert.Empty(t, r.Header.Get("Content-Type"))

		// Verify body is empty
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.Empty(t, body)

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"token": "ghs_unscoped_token"})
	}))
	defer srv.Close()

	token, err := exchangeToken(srv.URL, 999, "test-jwt", "")
	require.NoError(t, err)
	assert.Equal(t, "ghs_unscoped_token", token)
}

func TestExchangeToken_Non201Response(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer srv.Close()

	_, err := exchangeToken(srv.URL, 999, "bad-jwt", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GitHub API returned 401")
	assert.Contains(t, err.Error(), "Bad credentials")
}

func TestExchangeToken_EmptyTokenInResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"token": ""})
	}))
	defer srv.Close()

	_, err := exchangeToken(srv.URL, 999, "test-jwt", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty token in response")
}

func TestExchangeToken_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	_, err := exchangeToken(srv.URL, 999, "test-jwt", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing response")
}

func TestExchangeToken_CorrectEndpointPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"token": "ghs_test"})
	}))
	defer srv.Close()

	_, err := exchangeToken(srv.URL, 42, "jwt", "")
	require.NoError(t, err)
	assert.Equal(t, "/app/installations/42/access_tokens", gotPath)
}

// --- githubConfigFromEnv tests ---

func TestGithubConfigFromEnv_AllVars(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_INSTALLATION_ID", "67890")
	t.Setenv("GITHUB_API_URL", "https://ghe.example.com/api/v3")
	t.Setenv("REPO_URL", "https://github.com/org/repo")

	cfg, err := githubConfigFromEnv()
	require.NoError(t, err)
	assert.Equal(t, int64(12345), cfg.AppID)
	assert.Equal(t, int64(67890), cfg.InstallationID)
	assert.Equal(t, "https://ghe.example.com/api/v3", cfg.BaseURL)
	assert.Equal(t, "https://github.com/org/repo", cfg.RepoURL)
	assert.Equal(t, filepath.Join(secretDir, keyFilename), cfg.PrivateKeyPath)
	assert.Equal(t, filepath.Join(credsDir, tokenFilename), cfg.TokenPath)
}

func TestGithubConfigFromEnv_DefaultBaseURL(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "1")
	t.Setenv("GITHUB_INSTALLATION_ID", "2")
	t.Setenv("GITHUB_API_URL", "")

	cfg, err := githubConfigFromEnv()
	require.NoError(t, err)
	assert.Equal(t, "https://api.github.com", cfg.BaseURL)
}

func TestGithubConfigFromEnv_MissingAppID(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "")
	t.Setenv("GITHUB_INSTALLATION_ID", "2")

	_, err := githubConfigFromEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GITHUB_APP_ID")
}

func TestGithubConfigFromEnv_InvalidAppID(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "not-a-number")
	t.Setenv("GITHUB_INSTALLATION_ID", "2")

	_, err := githubConfigFromEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GITHUB_APP_ID")
}

func TestGithubConfigFromEnv_MissingInstallationID(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "1")
	t.Setenv("GITHUB_INSTALLATION_ID", "")

	_, err := githubConfigFromEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GITHUB_INSTALLATION_ID")
}

func TestGithubConfigFromEnv_InvalidInstallationID(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "1")
	t.Setenv("GITHUB_INSTALLATION_ID", "not-a-number")

	_, err := githubConfigFromEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GITHUB_INSTALLATION_ID")
}

// --- Integration-style test: full JWT round-trip ---

func TestCreateJWT_RoundTrip_VerifiableWithPublicKey(t *testing.T) {
	key := generateTestKey(t)
	appID := int64(99999)

	tokenStr, err := createJWT(appID, key)
	require.NoError(t, err)

	// Parse with public key — should succeed
	token, err := jwt.ParseWithClaims(tokenStr, &jwt.RegisteredClaims{}, func(tok *jwt.Token) (interface{}, error) {
		if _, ok := tok.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return &key.PublicKey, nil
	})
	require.NoError(t, err)
	require.True(t, token.Valid)

	claims := token.Claims.(*jwt.RegisteredClaims)
	assert.Equal(t, strconv.FormatInt(appID, 10), claims.Issuer)
	assert.NotNil(t, claims.IssuedAt)
	assert.NotNil(t, claims.ExpiresAt)

	// Verify timing: exp - iat should be ~11 minutes (10m future + 60s past)
	diff := claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time)
	assert.InDelta(t, 11*time.Minute, diff, float64(2*time.Second))
}
