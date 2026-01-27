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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.conditions[?(@.type=="Succeeded")].reason`
// +kubebuilder:printcolumn:name="PR",type=string,JSONPath=`.status.result.prUrl`,priority=1
// +kubebuilder:printcolumn:name="Job",type=string,JSONPath=`.status.jobName`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// AgentTask is the Schema for the agenttasks API.
type AgentTask struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`

	Spec   AgentTaskSpec   `json:"spec,omitempty"`
	Status AgentTaskStatus `json:"status,omitempty"`
}

type AgentTaskSpec struct {
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="repo is immutable"
	Repo RepoSpec `json:"repo"`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="task is immutable"
	Task     TaskSpec     `json:"task"`
	Callback CallbackSpec `json:"callback"`
	Runner   RunnerSpec   `json:"runner,omitempty"`
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
	Description     string `json:"description"`
	Context         string `json:"context,omitempty"`
	ContextEncoding string `json:"contextEncoding,omitempty"`
	ContextURL      string `json:"contextUrl,omitempty"`
}

type CallbackSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https?://`
	URL       string                    `json:"url"`
	SecretRef *corev1.SecretKeySelector `json:"secretRef,omitempty"`
}

type RunnerSpec struct {
	// +kubebuilder:default="shepherd-runner:latest"
	Image              string                      `json:"image,omitempty"`
	Timeout            metav1.Duration             `json:"timeout,omitempty"`
	ServiceAccountName string                      `json:"serviceAccountName,omitempty"`
	Resources          corev1.ResourceRequirements `json:"resources,omitempty"`
}

type AgentTaskStatus struct {
	ObservedGeneration int64        `json:"observedGeneration,omitempty"`
	StartTime          *metav1.Time `json:"startTime,omitempty"`
	CompletionTime     *metav1.Time `json:"completionTime,omitempty"`
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	JobName    string             `json:"jobName,omitempty"`
	Result     TaskResult         `json:"result,omitempty"`
}

type TaskResult struct {
	PRUrl string `json:"prUrl,omitempty"`
	Error string `json:"error,omitempty"`
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
