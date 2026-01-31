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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallbackSender_HMACSignature(t *testing.T) {
	secret := "test-secret"
	payload := CallbackPayload{
		TaskID:  "task-abc",
		Event:   "completed",
		Message: "Task completed successfully",
	}

	var receivedSig string
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSig = r.Header.Get("X-Shepherd-Signature")
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender := newCallbackSender(secret)
	err := sender.send(context.Background(), srv.URL, payload)
	require.NoError(t, err)

	// Verify the HMAC signature
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(receivedBody)
	expectedSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	assert.Equal(t, expectedSig, receivedSig)

	// Verify the body is the serialized payload
	var got CallbackPayload
	require.NoError(t, json.Unmarshal(receivedBody, &got))
	assert.Equal(t, payload.TaskID, got.TaskID)
	assert.Equal(t, payload.Event, got.Event)
}

func TestCallbackSender_EmptySecretSkipsSignature(t *testing.T) {
	var receivedSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSig = r.Header.Get("X-Shepherd-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender := newCallbackSender("")
	err := sender.send(context.Background(), srv.URL, CallbackPayload{TaskID: "task-abc", Event: "started"})
	require.NoError(t, err)
	assert.Empty(t, receivedSig, "no signature header when secret is empty")
}

func TestCallbackSender_Non2xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	sender := newCallbackSender("secret")
	err := sender.send(context.Background(), srv.URL, CallbackPayload{TaskID: "task-abc", Event: "completed"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "returned status 500")
}

func TestCallbackSender_NetworkError(t *testing.T) {
	sender := newCallbackSender("secret")
	// Use a URL that will refuse the connection
	err := sender.send(context.Background(), "http://127.0.0.1:1", CallbackPayload{TaskID: "task-abc", Event: "completed"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sending callback")
}

func TestCallbackSender_RespectsTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender := &callbackSender{
		secret: "secret",
		httpClient: &http.Client{
			Timeout: 50 * time.Millisecond,
		},
	}

	err := sender.send(context.Background(), srv.URL, CallbackPayload{TaskID: "task-abc", Event: "completed"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sending callback")
}

func TestCallbackSender_ContentType(t *testing.T) {
	var receivedContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender := newCallbackSender("secret")
	err := sender.send(context.Background(), srv.URL, CallbackPayload{TaskID: "task-abc", Event: "started"})
	require.NoError(t, err)
	assert.Equal(t, "application/json", receivedContentType)
}
