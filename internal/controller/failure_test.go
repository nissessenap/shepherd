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
	corev1 "k8s.io/api/core/v1"
)

func TestClassifyJobFailure(t *testing.T) {
	tests := []struct {
		name     string
		job      *batchv1.Job
		pods     []corev1.Pod
		expected failureType
	}{
		{
			name: "no pods — infrastructure failure",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{Type: batchv1.JobFailed, Status: corev1.ConditionTrue},
					},
				},
			},
			pods:     nil,
			expected: failureInfrastructure,
		},
		{
			name: "evicted pod — infrastructure failure",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{Type: batchv1.JobFailed, Status: corev1.ConditionTrue},
					},
				},
			},
			pods: []corev1.Pod{
				{
					Status: corev1.PodStatus{
						Phase:  corev1.PodFailed,
						Reason: "Evicted",
					},
				},
			},
			expected: failureInfrastructure,
		},
		{
			name: "OOMKilled container — OOM failure",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{Type: batchv1.JobFailed, Status: corev1.ConditionTrue},
					},
				},
			},
			pods: []corev1.Pod{
				{
					Status: corev1.PodStatus{
						Phase: corev1.PodFailed,
						ContainerStatuses: []corev1.ContainerStatus{
							{
								Name: "runner",
								State: corev1.ContainerState{
									Terminated: &corev1.ContainerStateTerminated{
										Reason:   "OOMKilled",
										ExitCode: 137,
									},
								},
							},
						},
					},
				},
			},
			expected: failureOOM,
		},
		{
			name: "OOMKilled init container — OOM failure",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{Type: batchv1.JobFailed, Status: corev1.ConditionTrue},
					},
				},
			},
			pods: []corev1.Pod{
				{
					Status: corev1.PodStatus{
						Phase: corev1.PodFailed,
						InitContainerStatuses: []corev1.ContainerStatus{
							{
								Name: "github-auth",
								State: corev1.ContainerState{
									Terminated: &corev1.ContainerStateTerminated{
										Reason:   "OOMKilled",
										ExitCode: 137,
									},
								},
							},
						},
					},
				},
			},
			expected: failureOOM,
		},
		{
			name: "DeadlineExceeded — timeout failure",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{
							Type:   batchv1.JobFailed,
							Status: corev1.ConditionTrue,
							Reason: "DeadlineExceeded",
						},
					},
				},
			},
			pods: []corev1.Pod{
				{
					Status: corev1.PodStatus{
						Phase: corev1.PodFailed,
					},
				},
			},
			expected: failureTimeout,
		},
		{
			name: "normal failure (exit code 1) — application failure",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{
							Type:    batchv1.JobFailed,
							Status:  corev1.ConditionTrue,
							Message: "BackoffLimitExceeded",
						},
					},
				},
			},
			pods: []corev1.Pod{
				{
					Status: corev1.PodStatus{
						Phase: corev1.PodFailed,
						ContainerStatuses: []corev1.ContainerStatus{
							{
								Name: "runner",
								State: corev1.ContainerState{
									Terminated: &corev1.ContainerStateTerminated{
										ExitCode: 1,
										Reason:   "Error",
									},
								},
							},
						},
					},
				},
			},
			expected: failureApplication,
		},
		{
			name: "DeadlineExceeded takes priority over no pods",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{
							Type:   batchv1.JobFailed,
							Status: corev1.ConditionTrue,
							Reason: "DeadlineExceeded",
						},
					},
				},
			},
			pods:     nil,
			expected: failureTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyJobFailure(tt.job, tt.pods)
			assert.Equal(t, tt.expected, result)
		})
	}
}
