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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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
		},
	}
}

func baseCfg() jobConfig {
	return jobConfig{
		AllowedRunnerImage:   "registry.example.com/shepherd-runner:v1",
		RunnerSecretName:     "shepherd-runner-app-key",
		InitImage:            "shepherd-init:latest",
		Scheme:               testScheme(),
		GithubAppID:          12345,
		GithubInstallationID: 67890,
		GithubAPIURL:         "https://api.github.com",
	}
}

func TestBuildJob_Name(t *testing.T) {
	task := baseTask()
	task.Generation = 3

	job, err := buildJob(task, baseCfg())
	require.NoError(t, err)
	assert.Equal(t, "my-task-3-job", job.Name)
	assert.Equal(t, "default", job.Namespace)
}

func TestBuildJob_Labels(t *testing.T) {
	job, err := buildJob(baseTask(), baseCfg())
	require.NoError(t, err)

	assert.Equal(t, "my-task", job.Labels["shepherd.io/task"])
	assert.Equal(t, "my-task", job.Spec.Template.Labels["shepherd.io/task"])
}

func TestBuildJob_BackoffLimit(t *testing.T) {
	job, err := buildJob(baseTask(), baseCfg())
	require.NoError(t, err)

	require.NotNil(t, job.Spec.BackoffLimit)
	assert.Equal(t, int32(0), *job.Spec.BackoffLimit)
}

func TestBuildJob_ActiveDeadlineSeconds_Default(t *testing.T) {
	job, err := buildJob(baseTask(), baseCfg())
	require.NoError(t, err)

	require.NotNil(t, job.Spec.ActiveDeadlineSeconds)
	assert.Equal(t, int64(1800), *job.Spec.ActiveDeadlineSeconds, "should default to 30 minutes")
}

func TestBuildJob_ActiveDeadlineSeconds_Custom(t *testing.T) {
	task := baseTask()
	task.Spec.Runner.Timeout = metav1.Duration{Duration: 10 * time.Minute}

	job, err := buildJob(task, baseCfg())
	require.NoError(t, err)

	require.NotNil(t, job.Spec.ActiveDeadlineSeconds)
	assert.Equal(t, int64(600), *job.Spec.ActiveDeadlineSeconds, "should use custom timeout")
}

func TestBuildJob_InitContainerImage_UsesConfiguredImage(t *testing.T) {
	cfg := baseCfg()
	cfg.InitImage = "custom-registry.io/shepherd-init:v2.0"

	job, err := buildJob(baseTask(), cfg)
	require.NoError(t, err)

	initContainer := job.Spec.Template.Spec.InitContainers[0]
	assert.Equal(t, "custom-registry.io/shepherd-init:v2.0", initContainer.Image)
}

func TestBuildJob_InitContainerEnv(t *testing.T) {
	task := baseTask()
	task.Spec.Repo.Ref = "feature-branch"

	job, err := buildJob(task, baseCfg())
	require.NoError(t, err)

	initContainer := job.Spec.Template.Spec.InitContainers[0]
	assert.Equal(t, "shepherd-init", initContainer.Name)
	assert.Equal(t, "shepherd-init:latest", initContainer.Image)

	envMap := envToMap(initContainer.Env)
	assert.Equal(t, "https://github.com/test-org/test-repo.git", envMap["REPO_URL"])
	assert.Equal(t, "Fix the login bug", envMap["TASK_DESCRIPTION"])
	assert.NotContains(t, envMap, "REPO_REF", "REPO_REF should not be in init container env")
}

func TestBuildJob_InitContainerEnv_NoRef(t *testing.T) {
	job, err := buildJob(baseTask(), baseCfg())
	require.NoError(t, err)

	initContainer := job.Spec.Template.Spec.InitContainers[0]
	envMap := envToMap(initContainer.Env)
	assert.Contains(t, envMap, "REPO_URL")
	assert.Contains(t, envMap, "TASK_DESCRIPTION")
	assert.Contains(t, envMap, "TASK_CONTEXT", "TASK_CONTEXT should be present when context is set")
	assert.NotContains(t, envMap, "REPO_REF", "REPO_REF should be omitted when ref is empty")
	assert.NotContains(t, envMap, "CONTEXT_ENCODING", "CONTEXT_ENCODING should be omitted when contextEncoding is empty")
}

func TestBuildJob_InitContainerEnv_WithContext(t *testing.T) {
	task := baseTask()
	task.Spec.Task.Context = "base64-encoded-gzip-data"
	task.Spec.Task.ContextEncoding = "gzip"

	job, err := buildJob(task, baseCfg())
	require.NoError(t, err)

	initContainer := job.Spec.Template.Spec.InitContainers[0]
	envMap := envToMap(initContainer.Env)
	assert.Equal(t, "base64-encoded-gzip-data", envMap["TASK_CONTEXT"])
	assert.Equal(t, "gzip", envMap["CONTEXT_ENCODING"])
}

func TestBuildJob_InitContainerEnv_ContextWithoutEncoding(t *testing.T) {
	task := baseTask()
	task.Spec.Task.Context = "plain-context-data"

	job, err := buildJob(task, baseCfg())
	require.NoError(t, err)

	initContainer := job.Spec.Template.Spec.InitContainers[0]
	envMap := envToMap(initContainer.Env)
	assert.Equal(t, "plain-context-data", envMap["TASK_CONTEXT"])
	assert.NotContains(t, envMap, "CONTEXT_ENCODING", "CONTEXT_ENCODING should be omitted when contextEncoding is empty")
}

func TestBuildJob_InitContainerEnv_GithubAppConfig(t *testing.T) {
	job, err := buildJob(baseTask(), baseCfg())
	require.NoError(t, err)

	initContainer := job.Spec.Template.Spec.InitContainers[0]
	envMap := envToMap(initContainer.Env)
	assert.Equal(t, "12345", envMap["GITHUB_APP_ID"])
	assert.Equal(t, "67890", envMap["GITHUB_INSTALLATION_ID"])
	assert.Equal(t, "https://api.github.com", envMap["GITHUB_API_URL"])
}

func TestBuildJob_InitContainerEnv_GithubAppConfig_CustomValues(t *testing.T) {
	cfg := baseCfg()
	cfg.GithubAppID = 99999
	cfg.GithubInstallationID = 11111
	cfg.GithubAPIURL = "https://github.example.com/api/v3"

	job, err := buildJob(baseTask(), cfg)
	require.NoError(t, err)

	initContainer := job.Spec.Template.Spec.InitContainers[0]
	envMap := envToMap(initContainer.Env)
	assert.Equal(t, "99999", envMap["GITHUB_APP_ID"])
	assert.Equal(t, "11111", envMap["GITHUB_INSTALLATION_ID"])
	assert.Equal(t, "https://github.example.com/api/v3", envMap["GITHUB_API_URL"])
}

func TestBuildJob_RunnerContainerEnv(t *testing.T) {
	task := baseTask()
	task.Spec.Repo.Ref = "main"

	job, err := buildJob(task, baseCfg())
	require.NoError(t, err)

	runner := job.Spec.Template.Spec.Containers[0]
	assert.Equal(t, "runner", runner.Name)

	envMap := envToMap(runner.Env)
	assert.Equal(t, "my-task", envMap["SHEPHERD_TASK_ID"])
	assert.Equal(t, "https://github.com/test-org/test-repo.git", envMap["SHEPHERD_REPO_URL"])
	assert.Equal(t, "https://example.com/callback", envMap["SHEPHERD_CALLBACK_URL"])
	assert.Equal(t, "/task/description.txt", envMap["SHEPHERD_TASK_FILE"])
	assert.Equal(t, "/task/context.txt", envMap["SHEPHERD_CONTEXT_FILE"])
	assert.Equal(t, "main", envMap["SHEPHERD_REPO_REF"])
	assert.NotContains(t, envMap, "SHEPHERD_TASK_DESCRIPTION",
		"SHEPHERD_TASK_DESCRIPTION should not be set — runner reads from file instead")
}

func TestBuildJob_RunnerContainerEnv_NoRef(t *testing.T) {
	job, err := buildJob(baseTask(), baseCfg())
	require.NoError(t, err)

	runner := job.Spec.Template.Spec.Containers[0]
	envMap := envToMap(runner.Env)
	assert.NotContains(t, envMap, "SHEPHERD_REPO_REF", "SHEPHERD_REPO_REF should be omitted when ref is empty")
}

func TestBuildJob_RunnerImage_UsesAllowedImage(t *testing.T) {
	job, err := buildJob(baseTask(), baseCfg())
	require.NoError(t, err)

	runner := job.Spec.Template.Spec.Containers[0]
	assert.Equal(t, "registry.example.com/shepherd-runner:v1", runner.Image,
		"should use AllowedRunnerImage from config, NOT spec.runner.image")
}

func TestBuildJob_EmptyAllowedRunnerImage_ReturnsError(t *testing.T) {
	cfg := baseCfg()
	cfg.AllowedRunnerImage = ""

	_, err := buildJob(baseTask(), cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no allowed runner image configured")
}

func TestBuildJob_EmptyInitImage_ReturnsError(t *testing.T) {
	cfg := baseCfg()
	cfg.InitImage = ""

	_, err := buildJob(baseTask(), cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no init image configured")
}

func TestBuildJob_InvalidGithubAppID_ReturnsError(t *testing.T) {
	cfg := baseCfg()
	cfg.GithubAppID = 0

	_, err := buildJob(baseTask(), cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid GitHub App ID")
}

func TestBuildJob_NegativeGithubAppID_ReturnsError(t *testing.T) {
	cfg := baseCfg()
	cfg.GithubAppID = -1

	_, err := buildJob(baseTask(), cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid GitHub App ID")
}

func TestBuildJob_InvalidGithubInstallationID_ReturnsError(t *testing.T) {
	cfg := baseCfg()
	cfg.GithubInstallationID = 0

	_, err := buildJob(baseTask(), cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid GitHub Installation ID")
}

func TestBuildJob_NegativeGithubInstallationID_ReturnsError(t *testing.T) {
	cfg := baseCfg()
	cfg.GithubInstallationID = -1

	_, err := buildJob(baseTask(), cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid GitHub Installation ID")
}

func TestBuildJob_JobNameTooLong_ReturnsError(t *testing.T) {
	task := baseTask()
	task.Name = "this-is-a-very-long-task-name-that-will-exceed-the-kubernetes-limit"
	task.Generation = 999999999

	_, err := buildJob(task, baseCfg())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds 63-character limit")
}

func TestBuildJob_OwnerReference(t *testing.T) {
	job, err := buildJob(baseTask(), baseCfg())
	require.NoError(t, err)

	require.Len(t, job.OwnerReferences, 1)
	ownerRef := job.OwnerReferences[0]
	assert.Equal(t, "my-task", ownerRef.Name)
	assert.Equal(t, "AgentTask", ownerRef.Kind)
	assert.NotNil(t, ownerRef.Controller)
	assert.True(t, *ownerRef.Controller)
}

func TestBuildJob_Resources(t *testing.T) {
	task := baseTask()
	task.Spec.Runner.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("500m"),
			corev1.ResourceMemory: resource.MustParse("256Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("2"),
			corev1.ResourceMemory: resource.MustParse("1Gi"),
		},
	}

	job, err := buildJob(task, baseCfg())
	require.NoError(t, err)

	runner := job.Spec.Template.Spec.Containers[0]
	assert.Equal(t, resource.MustParse("500m"), runner.Resources.Requests[corev1.ResourceCPU])
	assert.Equal(t, resource.MustParse("1Gi"), runner.Resources.Limits[corev1.ResourceMemory])
}

func TestBuildJob_Volumes(t *testing.T) {
	job, err := buildJob(baseTask(), baseCfg())
	require.NoError(t, err)

	volumes := job.Spec.Template.Spec.Volumes
	require.Len(t, volumes, 3)

	// github-creds volume (emptyDir)
	assert.Equal(t, "github-creds", volumes[0].Name)
	assert.NotNil(t, volumes[0].EmptyDir)

	// runner-app-key volume (secret)
	assert.Equal(t, "runner-app-key", volumes[1].Name)
	require.NotNil(t, volumes[1].Secret)
	assert.Equal(t, "shepherd-runner-app-key", volumes[1].Secret.SecretName)

	// task-files volume (emptyDir)
	assert.Equal(t, "task-files", volumes[2].Name)
	assert.NotNil(t, volumes[2].EmptyDir)
}

func TestBuildJob_VolumeMounts(t *testing.T) {
	job, err := buildJob(baseTask(), baseCfg())
	require.NoError(t, err)

	// Init container mounts
	initMounts := job.Spec.Template.Spec.InitContainers[0].VolumeMounts
	require.Len(t, initMounts, 3)
	assert.Equal(t, "github-creds", initMounts[0].Name)
	assert.Equal(t, "/creds", initMounts[0].MountPath)
	assert.False(t, initMounts[0].ReadOnly)
	assert.Equal(t, "runner-app-key", initMounts[1].Name)
	assert.Equal(t, "/secrets/runner-app-key", initMounts[1].MountPath)
	assert.True(t, initMounts[1].ReadOnly)
	assert.Equal(t, "task-files", initMounts[2].Name)
	assert.Equal(t, "/task", initMounts[2].MountPath)
	assert.False(t, initMounts[2].ReadOnly, "init container needs write access to task-files")

	// Runner container mounts
	runnerMounts := job.Spec.Template.Spec.Containers[0].VolumeMounts
	require.Len(t, runnerMounts, 2)
	assert.Equal(t, "github-creds", runnerMounts[0].Name)
	assert.Equal(t, "/creds", runnerMounts[0].MountPath)
	assert.True(t, runnerMounts[0].ReadOnly)
	assert.Equal(t, "task-files", runnerMounts[1].Name)
	assert.Equal(t, "/task", runnerMounts[1].MountPath)
	assert.True(t, runnerMounts[1].ReadOnly, "runner should have read-only access to task-files")
}

func TestBuildJob_RunnerSecretName_FromConfig(t *testing.T) {
	cfg := baseCfg()
	cfg.RunnerSecretName = "custom-secret-name"

	job, err := buildJob(baseTask(), cfg)
	require.NoError(t, err)

	secretVolume := job.Spec.Template.Spec.Volumes[1]
	require.NotNil(t, secretVolume.Secret)
	assert.Equal(t, "custom-secret-name", secretVolume.Secret.SecretName)
}

func TestBuildJob_RestartPolicy(t *testing.T) {
	job, err := buildJob(baseTask(), baseCfg())
	require.NoError(t, err)

	assert.Equal(t, corev1.RestartPolicyNever, job.Spec.Template.Spec.RestartPolicy)
}

func TestBuildJob_ServiceAccountName(t *testing.T) {
	task := baseTask()
	task.Spec.Runner.ServiceAccountName = "custom-sa"

	job, err := buildJob(task, baseCfg())
	require.NoError(t, err)

	assert.Equal(t, "custom-sa", job.Spec.Template.Spec.ServiceAccountName)
}

func TestBuildJob_PodFailurePolicy(t *testing.T) {
	job, err := buildJob(baseTask(), baseCfg())
	require.NoError(t, err)

	require.NotNil(t, job.Spec.PodFailurePolicy)
	rules := job.Spec.PodFailurePolicy.Rules
	require.Len(t, rules, 2)

	// Rule 0: exit code 137 → FailJob
	assert.Equal(t, batchv1.PodFailurePolicyActionFailJob, rules[0].Action)
	require.NotNil(t, rules[0].OnExitCodes)
	assert.Equal(t, batchv1.PodFailurePolicyOnExitCodesOpIn, rules[0].OnExitCodes.Operator)
	assert.Equal(t, []int32{137}, rules[0].OnExitCodes.Values)

	// Rule 1: DisruptionTarget → Ignore
	assert.Equal(t, batchv1.PodFailurePolicyActionIgnore, rules[1].Action)
	require.Len(t, rules[1].OnPodConditions, 1)
	assert.Equal(t, corev1.DisruptionTarget, rules[1].OnPodConditions[0].Type)
	assert.Equal(t, corev1.ConditionTrue, rules[1].OnPodConditions[0].Status)
}

func TestBuildJob_PodSecurityContext(t *testing.T) {
	job, err := buildJob(baseTask(), baseCfg())
	require.NoError(t, err)

	secCtx := job.Spec.Template.Spec.SecurityContext
	require.NotNil(t, secCtx, "SecurityContext should be set")

	require.NotNil(t, secCtx.RunAsNonRoot)
	assert.True(t, *secCtx.RunAsNonRoot, "RunAsNonRoot should be true")

	require.NotNil(t, secCtx.RunAsUser)
	assert.Equal(t, int64(65532), *secCtx.RunAsUser, "RunAsUser should be 65532")

	require.NotNil(t, secCtx.RunAsGroup)
	assert.Equal(t, int64(65532), *secCtx.RunAsGroup, "RunAsGroup should be 65532")

	require.NotNil(t, secCtx.FSGroup)
	assert.Equal(t, int64(65532), *secCtx.FSGroup, "FSGroup should be 65532")

	require.NotNil(t, secCtx.SeccompProfile, "SeccompProfile should be set")
	assert.Equal(t, corev1.SeccompProfileTypeRuntimeDefault, secCtx.SeccompProfile.Type,
		"SeccompProfile type should be RuntimeDefault")
}

func TestBuildJob_InitContainerResources(t *testing.T) {
	job, err := buildJob(baseTask(), baseCfg())
	require.NoError(t, err)

	initContainer := job.Spec.Template.Spec.InitContainers[0]

	// Verify requests
	assert.Equal(t, resource.MustParse("10m"), initContainer.Resources.Requests[corev1.ResourceCPU])
	assert.Equal(t, resource.MustParse("64Mi"), initContainer.Resources.Requests[corev1.ResourceMemory])

	// Verify limits
	assert.Equal(t, resource.MustParse("100m"), initContainer.Resources.Limits[corev1.ResourceCPU])
	assert.Equal(t, resource.MustParse("128Mi"), initContainer.Resources.Limits[corev1.ResourceMemory])
}

// envToMap converts a slice of EnvVar to a map for easy lookup.
func envToMap(envVars []corev1.EnvVar) map[string]string {
	m := make(map[string]string, len(envVars))
	for _, e := range envVars {
		m[e.Name] = e.Value
	}
	return m
}
