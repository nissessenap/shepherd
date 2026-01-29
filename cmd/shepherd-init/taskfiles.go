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
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

const (
	taskDir             = "/task"
	descriptionFilename = "description.txt"
	contextFilename     = "context.txt"
)

func writeTaskFiles() error {
	return writeTaskFilesToDir(taskDir)
}

func writeTaskFilesToDir(dir string) error {
	desc := os.Getenv("TASK_DESCRIPTION")
	if desc == "" {
		return fmt.Errorf("TASK_DESCRIPTION is required")
	}

	descPath := filepath.Join(dir, descriptionFilename)
	descData := []byte(desc)
	if err := writeFile(descPath, descData, 0644); err != nil {
		return fmt.Errorf("writing description: %w", err)
	}
	slog.Info("wrote task file", "path", descPath, "bytes", len(descData))

	context := os.Getenv("TASK_CONTEXT")
	if context == "" {
		// Write empty file so runner doesn't need to check existence
		contextPath := filepath.Join(dir, contextFilename)
		if err := writeFile(contextPath, nil, 0644); err != nil {
			return fmt.Errorf("writing empty context: %w", err)
		}
		slog.Info("wrote task file", "path", contextPath, "bytes", 0)
		return nil
	}

	encoding := os.Getenv("CONTEXT_ENCODING")
	data, err := decodeContext(context, encoding)
	if err != nil {
		return fmt.Errorf("decoding context: %w", err)
	}

	contextPath := filepath.Join(dir, contextFilename)
	if err := writeFile(contextPath, data, 0644); err != nil {
		return fmt.Errorf("writing context: %w", err)
	}
	slog.Info("wrote task file", "path", contextPath, "bytes", len(data))

	return nil
}

func decodeContext(raw, encoding string) ([]byte, error) {
	if encoding != "gzip" {
		// Plaintext — return as-is
		return []byte(raw), nil
	}

	// base64-decode, then gzip-decompress
	compressed, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}

	gr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()

	decompressed, err := io.ReadAll(gr)
	if err != nil {
		return nil, fmt.Errorf("gzip decompress: %w", err)
	}

	return decompressed, nil
}

func writeFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}
