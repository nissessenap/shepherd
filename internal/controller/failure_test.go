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
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	sandboxextv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"
)

func TestClassifyClaimTermination(t *testing.T) {
	tests := []struct {
		name           string
		claim          *sandboxextv1alpha1.SandboxClaim
		expectedReason string
		expectedMsg    string
	}{
		{
			name:           "nil Ready condition returns Failed",
			claim:          &sandboxextv1alpha1.SandboxClaim{},
			expectedReason: toolkitv1alpha1.ReasonFailed,
			expectedMsg:    "SandboxClaim status unavailable",
		},
		{
			name: "SandboxExpired reason returns TimedOut",
			claim: claimWithReadyCondition(
				metav1.ConditionFalse,
				reasonSandboxExpired,
				"sandbox lifetime exceeded",
			),
			expectedReason: toolkitv1alpha1.ReasonTimedOut,
			expectedMsg:    "Sandbox expired",
		},
		{
			name: "ClaimExpired reason returns TimedOut",
			claim: claimWithReadyCondition(
				metav1.ConditionFalse,
				reasonClaimExpired,
				"claim lifetime exceeded",
			),
			expectedReason: toolkitv1alpha1.ReasonTimedOut,
			expectedMsg:    "Sandbox expired",
		},
		{
			name: "other reason returns Failed with message",
			claim: claimWithReadyCondition(
				metav1.ConditionFalse,
				"SandboxNotReady",
				"pod terminated unexpectedly",
			),
			expectedReason: toolkitv1alpha1.ReasonFailed,
			expectedMsg:    "Sandbox terminated: pod terminated unexpectedly",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, msg := classifyClaimTermination(tt.claim)
			assert.Equal(t, tt.expectedReason, reason)
			assert.Equal(t, tt.expectedMsg, msg)
		})
	}
}

func claimWithReadyCondition(status metav1.ConditionStatus, reason, message string) *sandboxextv1alpha1.SandboxClaim {
	return &sandboxextv1alpha1.SandboxClaim{
		Status: sandboxextv1alpha1.SandboxClaimStatus{
			Conditions: []metav1.Condition{
				{
					Type:               string(sandboxv1alpha1.SandboxConditionReady),
					Status:             status,
					Reason:             reason,
					Message:            message,
					LastTransitionTime: metav1.Now(),
				},
			},
		},
	}
}
