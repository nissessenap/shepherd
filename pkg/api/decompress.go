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
	"fmt"
	"io"
)

const maxDecompressedSize = 10 << 20 // 10MiB decompression bomb protection

// decompressContext decodes and decompresses context stored in the CRD.
// Handles empty encoding (returns raw string) and "gzip" encoding (base64-decode + gunzip).
func decompressContext(raw, encoding string) (string, error) {
	if raw == "" {
		return "", nil
	}

	switch encoding {
	case "":
		return raw, nil
	case "gzip":
		compressed, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return "", fmt.Errorf("base64 decode: %w", err)
		}

		gr, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			return "", fmt.Errorf("gzip reader: %w", err)
		}
		defer gr.Close() //nolint:errcheck // Best-effort close on read-only gzip reader

		decompressed, err := io.ReadAll(io.LimitReader(gr, maxDecompressedSize+1))
		if err != nil {
			return "", fmt.Errorf("gzip decompress: %w", err)
		}
		if len(decompressed) > maxDecompressedSize {
			return "", fmt.Errorf("decompressed context exceeds %d byte limit", maxDecompressedSize)
		}

		return string(decompressed), nil
	default:
		return "", fmt.Errorf("unsupported encoding: %q", encoding)
	}
}
