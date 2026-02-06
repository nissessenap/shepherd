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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	sandboxextv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"

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

func TestBuildSandboxClaim_Lifecycle_DefaultTimeout(t *testing.T) {
	task := baseTask()
	// No timeout set, should use default 30m

	beforeBuild := time.Now()
	claim, err := buildSandboxClaim(task, baseSandboxCfg())
	afterBuild := time.Now()
	require.NoError(t, err)

	require.NotNil(t, claim.Spec.Lifecycle, "Lifecycle should be set")
	require.NotNil(t, claim.Spec.Lifecycle.ShutdownTime, "ShutdownTime should be set")

	// ShutdownTime should be ~30 minutes from now
	expectedMin := beforeBuild.Add(30 * time.Minute)
	expectedMax := afterBuild.Add(30 * time.Minute)
	shutdownTime := claim.Spec.Lifecycle.ShutdownTime.Time
	assert.True(t, shutdownTime.After(expectedMin) || shutdownTime.Equal(expectedMin),
		"ShutdownTime should be at least 30m from build start")
	assert.True(t, shutdownTime.Before(expectedMax) || shutdownTime.Equal(expectedMax),
		"ShutdownTime should be at most 30m from build end")

	assert.Equal(t, sandboxextv1alpha1.ShutdownPolicyRetain, claim.Spec.Lifecycle.ShutdownPolicy)
}

func TestBuildSandboxClaim_Lifecycle_CustomTimeout(t *testing.T) {
	task := baseTask()
	task.Spec.Runner.Timeout = metav1.Duration{Duration: 15 * time.Minute}

	beforeBuild := time.Now()
	claim, err := buildSandboxClaim(task, baseSandboxCfg())
	afterBuild := time.Now()
	require.NoError(t, err)

	require.NotNil(t, claim.Spec.Lifecycle)
	require.NotNil(t, claim.Spec.Lifecycle.ShutdownTime)

	// ShutdownTime should be ~15 minutes from now
	expectedMin := beforeBuild.Add(15 * time.Minute)
	expectedMax := afterBuild.Add(15 * time.Minute)
	shutdownTime := claim.Spec.Lifecycle.ShutdownTime.Time
	assert.True(t, shutdownTime.After(expectedMin) || shutdownTime.Equal(expectedMin),
		"ShutdownTime should be at least 15m from build start")
	assert.True(t, shutdownTime.Before(expectedMax) || shutdownTime.Equal(expectedMax),
		"ShutdownTime should be at most 15m from build end")

	assert.Equal(t, sandboxextv1alpha1.ShutdownPolicyRetain, claim.Spec.Lifecycle.ShutdownPolicy)
}
