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

import "fmt"

// Comment templates for different events.
const (
	commentAcknowledge = `Shepherd is working on your request.

Task ID: %s

I'll update this issue when I'm done.`

	commentAlreadyRunning = `A Shepherd task is already running for this issue.

Task ID: %s
Status: %s

Please wait for it to complete before triggering a new one.`

	commentCompleted = `Shepherd has completed the task.

Pull Request: %s

Please review the changes.`

	commentFailed = `Shepherd was unable to complete the task.

Error: %s

You can trigger a new attempt by commenting with @shepherd again.`
)

func formatAcknowledge(taskID string) string {
	return fmt.Sprintf(commentAcknowledge, taskID)
}

func formatAlreadyRunning(taskID, status string) string {
	return fmt.Sprintf(commentAlreadyRunning, taskID, status)
}

func formatCompleted(prURL string) string {
	return fmt.Sprintf(commentCompleted, prURL)
}

func formatFailed(errorMsg string) string {
	if errorMsg == "" {
		errorMsg = "Unknown error"
	}
	return fmt.Sprintf(commentFailed, errorMsg)
}
