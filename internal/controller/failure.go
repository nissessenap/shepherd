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

package controller

import (
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

type failureType int

const (
	failureNone           failureType = iota
	failureInfrastructure             // pod missing, evicted — retryable
	failureApplication                // non-zero exit — permanent
	failureOOM                        // OOMKilled — permanent
	failureTimeout                    // activeDeadlineSeconds exceeded — permanent
)

// classifyJobFailure examines a failed Job and its Pods to determine
// whether the failure is retryable (infrastructure) or permanent (application).
func classifyJobFailure(job *batchv1.Job, pods []corev1.Pod) failureType {
	// Check for timeout (DeadlineExceeded)
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Reason == "DeadlineExceeded" {
			return failureTimeout
		}
	}

	// No pods found — infrastructure failure (node loss, eviction)
	if len(pods) == 0 {
		return failureInfrastructure
	}

	// Check pod status
	for _, pod := range pods {
		// Evicted pods
		if pod.Status.Phase == corev1.PodFailed && pod.Status.Reason == "Evicted" {
			return failureInfrastructure
		}

		// Check container statuses for OOM
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Terminated != nil && cs.State.Terminated.Reason == "OOMKilled" {
				return failureOOM
			}
		}
		for _, cs := range pod.Status.InitContainerStatuses {
			if cs.State.Terminated != nil && cs.State.Terminated.Reason == "OOMKilled" {
				return failureOOM
			}
		}
	}

	// Pod exists with failure — application error
	return failureApplication
}
