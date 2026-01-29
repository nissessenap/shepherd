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
	"compress/gzip"
	"encoding/base64"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	// Suppress log output during ALL tests in this package (affects github_test.go too).
	logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	os.Exit(m.Run())
}

// gzipBase64 compresses plaintext with gzip and base64-encodes the result.
func gzipBase64(t *testing.T, plaintext string) string {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, err := gw.Write([]byte(plaintext))
	require.NoError(t, err)
	require.NoError(t, gw.Close())
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestWriteTaskFilesToDir_Description(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TASK_DESCRIPTION", "Fix the login bug")
	t.Setenv("TASK_CONTEXT", "")

	err := writeTaskFilesToDir(dir)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, descriptionFilename))
	require.NoError(t, err)
	assert.Equal(t, "Fix the login bug", string(data))
}

func TestWriteTaskFilesToDir_DescriptionPermissions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TASK_DESCRIPTION", "some task")
	t.Setenv("TASK_CONTEXT", "")

	err := writeTaskFilesToDir(dir)
	require.NoError(t, err)

	info, err := os.Stat(filepath.Join(dir, descriptionFilename))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0644), info.Mode().Perm())
}

func TestWriteTaskFilesToDir_EmptyDescription_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TASK_DESCRIPTION", "")

	err := writeTaskFilesToDir(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TASK_DESCRIPTION is required")
}

func TestWriteTaskFilesToDir_EmptyContext_WritesEmptyFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TASK_DESCRIPTION", "some task")
	t.Setenv("TASK_CONTEXT", "")

	err := writeTaskFilesToDir(dir)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, contextFilename))
	require.NoError(t, err)
	assert.Empty(t, data)
}

func TestWriteTaskFilesToDir_PlaintextContext(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TASK_DESCRIPTION", "some task")
	t.Setenv("TASK_CONTEXT", "plain context data")
	t.Setenv("CONTEXT_ENCODING", "")

	err := writeTaskFilesToDir(dir)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, contextFilename))
	require.NoError(t, err)
	assert.Equal(t, "plain context data", string(data))
}

func TestWriteTaskFilesToDir_GzipContext(t *testing.T) {
	dir := t.TempDir()
	original := "This is the decompressed context content"
	encoded := gzipBase64(t, original)

	t.Setenv("TASK_DESCRIPTION", "some task")
	t.Setenv("TASK_CONTEXT", encoded)
	t.Setenv("CONTEXT_ENCODING", "gzip")

	err := writeTaskFilesToDir(dir)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, contextFilename))
	require.NoError(t, err)
	assert.Equal(t, original, string(data))
}

func TestWriteTaskFilesToDir_ContextPermissions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TASK_DESCRIPTION", "some task")
	t.Setenv("TASK_CONTEXT", "context data")
	t.Setenv("CONTEXT_ENCODING", "")

	err := writeTaskFilesToDir(dir)
	require.NoError(t, err)

	info, err := os.Stat(filepath.Join(dir, contextFilename))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0644), info.Mode().Perm())
}

func TestDecodeContext_EmptyEncoding_ReturnsRawBytes(t *testing.T) {
	raw := "hello world"
	data, err := decodeContext(raw, "")
	require.NoError(t, err)
	assert.Equal(t, []byte(raw), data)
}

func TestDecodeContext_PlainEncoding_ReturnsRawBytes(t *testing.T) {
	raw := "hello world"
	data, err := decodeContext(raw, "plain")
	require.NoError(t, err)
	assert.Equal(t, []byte(raw), data)
}

func TestDecodeContext_UnknownEncoding_ReturnsError(t *testing.T) {
	raw := "hello world"
	_, err := decodeContext(raw, "unknown")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported encoding")
	assert.Contains(t, err.Error(), "unknown")
}

func TestDecodeContext_Gzip_DecompressesCorrectly(t *testing.T) {
	original := "This is the decompressed context content"
	encoded := gzipBase64(t, original)

	data, err := decodeContext(encoded, "gzip")
	require.NoError(t, err)
	assert.Equal(t, []byte(original), data)
}

func TestDecodeContext_Gzip_InvalidBase64_ReturnsError(t *testing.T) {
	_, err := decodeContext("not-valid-base64!!", "gzip")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base64 decode")
}

func TestDecodeContext_Gzip_InvalidGzipData_ReturnsError(t *testing.T) {
	// Valid base64, but not gzip data
	notGzip := base64.StdEncoding.EncodeToString([]byte("this is not gzip"))

	_, err := decodeContext(notGzip, "gzip")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gzip")
}

func TestWriteFile_RespectsPermissions(t *testing.T) {
	dir := t.TempDir()

	// Test 0644 (task files)
	path644 := filepath.Join(dir, "task.txt")
	err := writeFile(path644, []byte("task data"), 0644)
	require.NoError(t, err)
	info, err := os.Stat(path644)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0644), info.Mode().Perm())

	// Test 0600 (token file)
	path600 := filepath.Join(dir, "token")
	err = writeFile(path600, []byte("secret-token"), 0600)
	require.NoError(t, err)
	info, err = os.Stat(path600)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}
