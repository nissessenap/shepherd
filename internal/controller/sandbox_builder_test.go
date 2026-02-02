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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = toolkitv1alpha1.AddToScheme(s)
	return s
}

func baseTask() *toolkitv1alpha1.AgentTask {
	return &toolkitv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "my-task",
			Namespace:  "default",
			Generation: 1,
			UID:        "test-uid-123",
		},
		Spec: toolkitv1alpha1.AgentTaskSpec{
			Repo: toolkitv1alpha1.RepoSpec{
				URL: "https://github.com/test-org/test-repo.git",
			},
			Task: toolkitv1alpha1.TaskSpec{
				Description: "Fix the login bug",
				Context:     "Issue #42: login page throws NPE on empty password",
			},
			Callback: toolkitv1alpha1.CallbackSpec{
				URL: "https://example.com/callback",
			},
			Runner: toolkitv1alpha1.RunnerSpec{
				SandboxTemplateName: "secure-runner-template",
			},
		},
	}
}

func baseSandboxCfg() sandboxConfig {
	return sandboxConfig{
		Scheme: testScheme(),
	}
}

func TestBuildSandboxClaim_Name(t *testing.T) {
	task := baseTask()

	claim, err := buildSandboxClaim(task, baseSandboxCfg())
	require.NoError(t, err)
	assert.Equal(t, "my-task", claim.Name)
	assert.Equal(t, "default", claim.Namespace)
}

func TestBuildSandboxClaim_Labels(t *testing.T) {
	claim, err := buildSandboxClaim(baseTask(), baseSandboxCfg())
	require.NoError(t, err)

	assert.Equal(t, "my-task", claim.Labels["shepherd.io/task"])
}

func TestBuildSandboxClaim_TemplateRef(t *testing.T) {
	task := baseTask()
	task.Spec.Runner.SandboxTemplateName = "custom-template"

	claim, err := buildSandboxClaim(task, baseSandboxCfg())
	require.NoError(t, err)

	assert.Equal(t, "custom-template", claim.Spec.TemplateRef.Name)
}

func TestBuildSandboxClaim_OwnerReference(t *testing.T) {
	claim, err := buildSandboxClaim(baseTask(), baseSandboxCfg())
	require.NoError(t, err)

	require.Len(t, claim.OwnerReferences, 1)
	ownerRef := claim.OwnerReferences[0]
	assert.Equal(t, "my-task", ownerRef.Name)
	assert.Equal(t, "AgentTask", ownerRef.Kind)
	assert.NotNil(t, ownerRef.Controller)
	assert.True(t, *ownerRef.Controller)
}

func TestBuildSandboxClaim_NamespacePropagated(t *testing.T) {
	task := baseTask()
	task.Namespace = "custom-ns"

	claim, err := buildSandboxClaim(task, baseSandboxCfg())
	require.NoError(t, err)

	assert.Equal(t, "custom-ns", claim.Namespace)
}

func TestBuildSandboxClaim_EmptySandboxTemplateName_ReturnsError(t *testing.T) {
	task := baseTask()
	task.Spec.Runner.SandboxTemplateName = ""

	_, err := buildSandboxClaim(task, baseSandboxCfg())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sandboxTemplateName is required")
}

func TestBuildSandboxClaim_NameTooLong_ReturnsError(t *testing.T) {
	task := baseTask()
	task.Name = strings.Repeat("a", 64)

	_, err := buildSandboxClaim(task, baseSandboxCfg())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds 63-character limit")
}
