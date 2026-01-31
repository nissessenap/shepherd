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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

func TestIsTerminal(t *testing.T) {
	tests := []struct {
		name     string
		task     *toolkitv1alpha1.AgentTask
		expected bool
	}{
		{
			name:     "no conditions",
			task:     &toolkitv1alpha1.AgentTask{},
			expected: false,
		},
		{
			name:     "pending (unknown)",
			task:     taskWithCondition(metav1.ConditionUnknown, toolkitv1alpha1.ReasonPending),
			expected: false,
		},
		{
			name:     "running (unknown)",
			task:     taskWithCondition(metav1.ConditionUnknown, toolkitv1alpha1.ReasonRunning),
			expected: false,
		},
		{
			name:     "succeeded (true)",
			task:     taskWithCondition(metav1.ConditionTrue, toolkitv1alpha1.ReasonSucceeded),
			expected: true,
		},
		{
			name:     "failed (false)",
			task:     taskWithCondition(metav1.ConditionFalse, toolkitv1alpha1.ReasonFailed),
			expected: true,
		},
		{
			name:     "timed out (false)",
			task:     taskWithCondition(metav1.ConditionFalse, toolkitv1alpha1.ReasonTimedOut),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.task.IsTerminal())
		})
	}
}

func TestHasCondition(t *testing.T) {
	tests := []struct {
		name     string
		task     *toolkitv1alpha1.AgentTask
		condType string
		expected bool
	}{
		{
			name:     "no conditions",
			task:     &toolkitv1alpha1.AgentTask{},
			condType: toolkitv1alpha1.ConditionSucceeded,
			expected: false,
		},
		{
			name:     "has matching condition",
			task:     taskWithCondition(metav1.ConditionUnknown, toolkitv1alpha1.ReasonPending),
			condType: toolkitv1alpha1.ConditionSucceeded,
			expected: true,
		},
		{
			name:     "has different condition type",
			task:     taskWithCondition(metav1.ConditionUnknown, toolkitv1alpha1.ReasonPending),
			condType: "SomeOtherCondition",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, hasCondition(tt.task, tt.condType))
		})
	}
}

func TestSetCondition(t *testing.T) {
	task := &toolkitv1alpha1.AgentTask{}

	// Set initial condition
	setCondition(task, metav1.Condition{
		Type:   toolkitv1alpha1.ConditionSucceeded,
		Status: metav1.ConditionUnknown,
		Reason: toolkitv1alpha1.ReasonPending,
	})
	assert.Len(t, task.Status.Conditions, 1)
	assert.Equal(t, toolkitv1alpha1.ReasonPending, task.Status.Conditions[0].Reason)

	// Update existing condition — should update in place, not append
	setCondition(task, metav1.Condition{
		Type:   toolkitv1alpha1.ConditionSucceeded,
		Status: metav1.ConditionTrue,
		Reason: toolkitv1alpha1.ReasonSucceeded,
	})
	assert.Len(t, task.Status.Conditions, 1, "should update in place, not append")
	assert.Equal(t, toolkitv1alpha1.ReasonSucceeded, task.Status.Conditions[0].Reason)
	assert.Equal(t, metav1.ConditionTrue, task.Status.Conditions[0].Status)
}

func taskWithCondition(status metav1.ConditionStatus, reason string) *toolkitv1alpha1.AgentTask {
	task := &toolkitv1alpha1.AgentTask{}
	setCondition(task, metav1.Condition{
		Type:   toolkitv1alpha1.ConditionSucceeded,
		Status: status,
		Reason: reason,
	})
	return task
}
