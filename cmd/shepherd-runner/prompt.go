package main

import (
	"fmt"

	"github.com/NissesSenap/shepherd/pkg/runner"
)

// buildPrompt constructs the v1 prompt for Claude Code from task data.
func buildPrompt(task runner.TaskData) string {
	prompt := fmt.Sprintf(`You have been assigned a coding task. Please implement the requested changes.

## Task Description

%s

## Source

This task was created from: %s

## Additional Context

Additional context has been written to ~/task-context.md (outside the repository).

## Instructions

1. Read and understand the existing codebase before making changes
2. Implement the changes described in the task description
3. Run existing tests to verify your changes don't break anything
4. Commit your changes with a clear commit message
5. Create a pull request with a description of what you changed and why
6. Stay focused on the assigned task — do not make unrelated changes`,
		task.Description,
		task.SourceURL,
	)

	return prompt
}
