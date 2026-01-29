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
	"fmt"
	"log/slog"
	"os"
)

func main() {
	if err := run(); err != nil {
		slog.Error("shepherd-init failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	slog.Info("shepherd-init starting")

	if err := writeTaskFiles(); err != nil {
		return fmt.Errorf("writing task files: %w", err)
	}

	if err := generateGitHubToken(); err != nil {
		return fmt.Errorf("generating github token: %w", err)
	}

	slog.Info("shepherd-init completed successfully")
	return nil
}
