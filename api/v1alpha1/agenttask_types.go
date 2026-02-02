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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.conditions[?(@.type=="Succeeded")].reason`
// +kubebuilder:printcolumn:name="PR",type=string,JSONPath=`.status.result.prUrl`,priority=1
// +kubebuilder:printcolumn:name="Claim",type=string,JSONPath=`.status.sandboxClaimName`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// AgentTask is the Schema for the agenttasks API.
type AgentTask struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`

	Spec AgentTaskSpec `json:"spec,omitzero"`
	// +optional
	Status AgentTaskStatus `json:"status,omitzero"`
}

type AgentTaskSpec struct {
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="repo is immutable"
	Repo RepoSpec `json:"repo"`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="task is immutable"
	Task     TaskSpec     `json:"task"`
	Callback CallbackSpec `json:"callback"`
	// +optional
	Runner RunnerSpec `json:"runner,omitzero"`
}

type RepoSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https://`
	URL string `json:"url"`
	Ref string `json:"ref,omitempty"`
}

type TaskSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Description string `json:"description"`

	// Context is additional context, gzip-compressed then base64-encoded.
	// The API accepts raw text, compresses for CRD storage.
	// +optional
	Context string `json:"context,omitempty"`

	// +kubebuilder:validation:Enum="";gzip
	ContextEncoding string `json:"contextEncoding,omitempty"`

	// SourceURL is the origin of the task (e.g., GitHub issue URL). Informational only.
	SourceURL string `json:"sourceURL,omitempty"`

	// SourceType identifies the trigger type: "issue", "pr", or "fleet".
	// +kubebuilder:validation:Enum="";issue;pr;fleet
	SourceType string `json:"sourceType,omitempty"`

	// SourceID identifies the specific trigger instance (e.g., issue number).
	SourceID string `json:"sourceID,omitempty"`
}

type CallbackSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https?://`
	URL string `json:"url"`
}

type RunnerSpec struct {
	// SandboxTemplateName references a SandboxTemplate for the runner environment.
	// +kubebuilder:validation:Required
	SandboxTemplateName string `json:"sandboxTemplateName"`

	// Timeout is the maximum duration for task execution.
	// The operator enforces this via its own timer since agent-sandbox v0.1.0
	// does not support Lifecycle/ShutdownTime in released versions.
	// +kubebuilder:default="30m"
	// +optional
	Timeout metav1.Duration `json:"timeout,omitzero"`

	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitzero"`
}

type AgentTaskStatus struct {
	ObservedGeneration int64        `json:"observedGeneration,omitempty"`
	StartTime          *metav1.Time `json:"startTime,omitempty"`
	CompletionTime     *metav1.Time `json:"completionTime,omitempty"`
	// +listType=map
	// +listMapKey=type
	Conditions       []metav1.Condition `json:"conditions,omitempty"`
	SandboxClaimName string             `json:"sandboxClaimName,omitempty"`
	// +optional
	Result TaskResult `json:"result,omitzero"`
}

type TaskResult struct {
	PRUrl string `json:"prUrl,omitempty"`
	Error string `json:"error,omitempty"`
}

// IsTerminal returns true if the task has reached a terminal condition.
// A task is terminal when the Succeeded condition exists and its status is not Unknown.
func (t *AgentTask) IsTerminal() bool {
	cond := meta.FindStatusCondition(t.Status.Conditions, ConditionSucceeded)
	if cond == nil {
		return false
	}
	return cond.Status != metav1.ConditionUnknown
}

// +kubebuilder:object:root=true

// AgentTaskList contains a list of AgentTask.
type AgentTaskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AgentTask `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentTask{}, &AgentTaskList{})
}
