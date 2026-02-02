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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecompressContext_Empty(t *testing.T) {
	result, err := decompressContext("", "gzip")
	require.NoError(t, err)
	assert.Equal(t, "", result)
}

func TestDecompressContext_NoEncoding(t *testing.T) {
	result, err := decompressContext("plain text context", "")
	require.NoError(t, err)
	assert.Equal(t, "plain text context", result)
}

func TestDecompressContext_Roundtrip(t *testing.T) {
	original := "Issue #42: login page throws NPE on empty password"

	compressed, encoding, err := compressContext(original)
	require.NoError(t, err)
	assert.Equal(t, "gzip", encoding)

	decompressed, err := decompressContext(compressed, encoding)
	require.NoError(t, err)
	assert.Equal(t, original, decompressed)
}

func TestDecompressContext_InvalidBase64(t *testing.T) {
	_, err := decompressContext("not-valid-base64!!!", "gzip")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base64 decode")
}

func TestDecompressContext_InvalidGzip(t *testing.T) {
	// Valid base64 but not valid gzip
	_, err := decompressContext("aGVsbG8=", "gzip") // base64("hello")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gzip reader")
}

func TestDecompressContext_UnsupportedEncoding(t *testing.T) {
	_, err := decompressContext("data", "zstd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported encoding")
}

func TestDecompressContext_SizeLimit(t *testing.T) {
	// Create input that will decompress to more than 10MiB
	largeInput := strings.Repeat("A", 11<<20) // 11MiB
	compressed, encoding, err := compressContext(largeInput)
	require.NoError(t, err)

	_, err = decompressContext(compressed, encoding)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}
