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
	"compress/gzip"
	"encoding/base64"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testContextString = "Issue #42: login page throws NPE on empty password"

func TestCompressContext_Empty(t *testing.T) {
	compressed, encoding, err := compressContext("")
	require.NoError(t, err)
	assert.Equal(t, "", compressed)
	assert.Equal(t, "", encoding)
}

func TestCompressContext_NonEmpty(t *testing.T) {
	input := testContextString
	compressed, encoding, err := compressContext(input)
	require.NoError(t, err)
	assert.NotEmpty(t, compressed)
	assert.Equal(t, "gzip", encoding)

	// Verify it's valid base64
	decoded, err := base64.StdEncoding.DecodeString(compressed)
	require.NoError(t, err)
	assert.NotEmpty(t, decoded)
}

func TestCompressContext_Roundtrip(t *testing.T) {
	input := testContextString
	compressed, _, err := compressContext(input)
	require.NoError(t, err)

	// Decompress
	decoded, err := base64.StdEncoding.DecodeString(compressed)
	require.NoError(t, err)

	gz, err := gzip.NewReader(bytes.NewReader(decoded))
	require.NoError(t, err)
	defer func() { require.NoError(t, gz.Close()) }()

	decompressed, err := io.ReadAll(gz)
	require.NoError(t, err)
	assert.Equal(t, input, string(decompressed))
}

func TestCompressContext_LargeInput(t *testing.T) {
	// 1 MiB of repeated text
	input := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 25000)
	compressed, encoding, err := compressContext(input)
	require.NoError(t, err)
	assert.Equal(t, "gzip", encoding)
	assert.NotEmpty(t, compressed)

	// Compressed should be smaller than the original (base64 adds ~33% overhead)
	decoded, err := base64.StdEncoding.DecodeString(compressed)
	require.NoError(t, err)
	assert.Less(t, len(decoded), len(input), "gzip-compressed data should be smaller than original for repetitive input")
}
