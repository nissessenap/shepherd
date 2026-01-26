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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RepoSpec defines the repository to work on
type RepoSpec struct {
	// URL is the git repository URL
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https://`
	URL string `json:"url"`
}

// TaskSpec defines the task to perform
type TaskSpec struct {
	// Description is the task description for the AI agent
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Description string `json:"description"`

	// Context provides additional context (issue body, comments, etc.)
	// +optional
	Context string `json:"context,omitempty"`
}

// CallbackSpec defines where to send status updates
type CallbackSpec struct {
	// URL is the callback endpoint
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https?://`
	URL string `json:"url"`

	// SecretRef references a secret containing the callback authentication
	// +optional
	SecretRef string `json:"secretRef,omitempty"`
}

// RunnerSpec defines the runner configuration
type RunnerSpec struct {
	// Image is the runner container image (must be pre-approved)
	// +kubebuilder:default="shepherd-runner:latest"
	Image string `json:"image,omitempty"`

	// Timeout is the maximum duration for the task
	// +kubebuilder:default="30m"
	Timeout metav1.Duration `json:"timeout,omitempty"`
}

// AgentTaskSpec defines the desired state of AgentTask
type AgentTaskSpec struct {
	// Repo specifies the repository to work on
	// +kubebuilder:validation:Required
	Repo RepoSpec `json:"repo"`

	// Task specifies what the agent should do
	// +kubebuilder:validation:Required
	Task TaskSpec `json:"task"`

	// Callback specifies where to send status updates
	// +kubebuilder:validation:Required
	Callback CallbackSpec `json:"callback"`

	// Runner configures the runner job
	// +optional
	Runner RunnerSpec `json:"runner,omitempty"`
}

// TaskEvent represents a status event during task execution
type TaskEvent struct {
	// Timestamp of the event
	Timestamp metav1.Time `json:"timestamp"`

	// Message describing the event
	Message string `json:"message"`
}

// TaskResult contains the outcome of a completed task
type TaskResult struct {
	// PRUrl is the URL of the created pull request (if any)
	// +optional
	PRUrl string `json:"prUrl,omitempty"`

	// Error contains error details if the task failed
	// +optional
	Error string `json:"error,omitempty"`
}

// AgentTaskStatus defines the observed state of AgentTask
type AgentTaskStatus struct {
	// Conditions represent the latest available observations
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// JobName is the name of the K8s Job running this task
	// +optional
	JobName string `json:"jobName,omitempty"`

	// Result contains the task outcome
	// +optional
	Result TaskResult `json:"result,omitempty"`

	// Events contains status updates from the runner
	// +optional
	Events []TaskEvent `json:"events,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.conditions[?(@.type=="Succeeded")].reason`
// +kubebuilder:printcolumn:name="Job",type=string,JSONPath=`.status.jobName`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentTask is the Schema for the agenttasks API
type AgentTask struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentTaskSpec   `json:"spec,omitempty"`
	Status AgentTaskStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentTaskList contains a list of AgentTask
type AgentTaskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentTask `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentTask{}, &AgentTaskList{})
}
