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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	ctrl "sigs.k8s.io/controller-runtime"
)

func signedRequest(t *testing.T, secret string, body []byte, event string) *http.Request {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("X-GitHub-Event", event)
	return req
}

func TestWebhookHandler_SignatureVerification(t *testing.T) {
	secret := "test-secret"
	handler := NewWebhookHandler(secret, nil, ctrl.Log.WithName("test"))

	t.Run("valid signature", func(t *testing.T) {
		body := []byte(`{"action":"created"}`)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

		assert.True(t, handler.verifySignature(body, sig))
	})

	t.Run("invalid signature", func(t *testing.T) {
		body := []byte(`{"action":"created"}`)
		assert.False(t, handler.verifySignature(body, "sha256=invalid"))
	})

	t.Run("missing prefix", func(t *testing.T) {
		body := []byte(`{"action":"created"}`)
		assert.False(t, handler.verifySignature(body, "invalid"))
	})

	t.Run("empty secret allows all", func(t *testing.T) {
		h := NewWebhookHandler("", nil, ctrl.Log.WithName("test"))
		assert.True(t, h.verifySignature([]byte(`{}`), ""))
	})
}

func TestWebhookHandler_ServeHTTP(t *testing.T) {
	secret := "test-secret"
	handler := NewWebhookHandler(secret, nil, ctrl.Log.WithName("test"))

	t.Run("rejects GET requests", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})

	t.Run("rejects invalid signature", func(t *testing.T) {
		body := []byte(`{"action":"created"}`)
		req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
		req.Header.Set("X-Hub-Signature-256", "sha256=invalid")
		req.Header.Set("X-GitHub-Event", "ping")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("accepts valid ping", func(t *testing.T) {
		body := []byte(`{"zen":"test"}`)
		req := signedRequest(t, secret, body, "ping")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestShepherdMentionRegex(t *testing.T) {
	tests := []struct {
		input string
		match bool
	}{
		{"@shepherd fix this bug", true},
		{"@SHEPHERD fix this bug", true},
		{"@Shepherd fix this bug", true},
		{"Hey @shepherd can you help?", true},
		{"@shepherd", true},
		{"\n@shepherd fix it", true},
		{"@shepherding", false},
		{"no mention here", false},
		{"email@shepherd.io", false},
		{"user@shepherd", false},
		{"test@shepherd.com stuff", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.match, shepherdMentionRegex.MatchString(tc.input))
		})
	}
}
