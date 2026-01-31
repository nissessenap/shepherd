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
)

// compressContext gzip-compresses the context string and returns base64-encoded result.
// Returns ("", "", nil) if context is empty.
func compressContext(context string) (compressed string, encoding string, err error) {
	if context == "" {
		return "", "", nil
	}

	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return "", "", fmt.Errorf("creating gzip writer: %w", err)
	}
	if _, err := gz.Write([]byte(context)); err != nil {
		return "", "", fmt.Errorf("writing gzip data: %w", err)
	}
	if err := gz.Close(); err != nil {
		return "", "", fmt.Errorf("closing gzip writer: %w", err)
	}

	return base64.StdEncoding.EncodeToString(buf.Bytes()), "gzip", nil
}
