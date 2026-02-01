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
	"testing"

	"github.com/stretchr/testify/assert"
	batchv1 "k8s.io/api/batch/v1"
)

func TestClassifyJobFailure(t *testing.T) {
	tests := []struct {
		name     string
		cond     batchv1.JobCondition
		expected failureClass
	}{
		{
			name: "DeadlineExceeded → timeout",
			cond: batchv1.JobCondition{
				Type:   batchv1.JobFailed,
				Reason: "DeadlineExceeded",
			},
			expected: failureTimeout,
		},
		{
			name: "PodFailurePolicy → application",
			cond: batchv1.JobCondition{
				Type:    batchv1.JobFailed,
				Reason:  "PodFailurePolicy",
				Message: "Container runner for pod default/my-task-1-job-xyz failed with exit code 137 matching FailJob rule at index 0",
			},
			expected: failureApplication,
		},
		{
			name: "BackoffLimitExceeded → application",
			cond: batchv1.JobCondition{
				Type:   batchv1.JobFailed,
				Reason: "BackoffLimitExceeded",
			},
			expected: failureApplication,
		},
		{
			name: "unknown reason → application",
			cond: batchv1.JobCondition{
				Type:   batchv1.JobFailed,
				Reason: "SomeUnknownReason",
			},
			expected: failureApplication,
		},
		{
			name: "empty reason → application",
			cond: batchv1.JobCondition{
				Type: batchv1.JobFailed,
			},
			expected: failureApplication,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, classifyJobFailure(tt.cond))
		})
	}
}
