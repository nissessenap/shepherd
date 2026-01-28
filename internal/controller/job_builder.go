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
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

// jobConfig holds operator-level configuration needed to build Jobs.
type jobConfig struct {
	AllowedRunnerImage string
	RunnerSecretName   string
	InitImage          string
	Scheme             *runtime.Scheme
}

const defaultTimeout = 30 * time.Minute

func buildJob(task *toolkitv1alpha1.AgentTask, cfg jobConfig) (*batchv1.Job, error) {
	// Validate runner image against operator-configured allowlist.
	// For MVP, the operator admin sets SHEPHERD_RUNNER_IMAGE and only that
	// image is permitted. The spec.runner.image field is ignored — the admin
	// controls what runs in the cluster.
	if cfg.AllowedRunnerImage == "" {
		return nil, fmt.Errorf("no allowed runner image configured (set SHEPHERD_RUNNER_IMAGE)")
	}
	if cfg.InitImage == "" {
		return nil, fmt.Errorf("no init image configured (set SHEPHERD_INIT_IMAGE)")
	}

	// Job name includes generation to avoid collision on delete/recreate
	jobName := fmt.Sprintf("%s-%d-job", task.Name, task.Generation)
	if len(jobName) > 63 {
		return nil, fmt.Errorf("generated job name %q exceeds 63-character limit (task name too long)", jobName)
	}

	// backoffLimit: 0 — no K8s-level retries, operator handles retries
	backoffLimit := int32(0)

	// activeDeadlineSeconds from spec.runner.timeout, defaulting to 30m
	timeout := task.Spec.Runner.Timeout.Duration
	if timeout == 0 {
		timeout = defaultTimeout
	}
	activeDeadlineSecs := int64(timeout.Seconds())

	// Build init container env — include ref if specified
	// Init container writes task description and context files for the runner.
	initEnv := []corev1.EnvVar{
		{Name: "REPO_URL", Value: task.Spec.Repo.URL},
		{Name: "TASK_DESCRIPTION", Value: task.Spec.Task.Description},
	}
	if task.Spec.Repo.Ref != "" {
		initEnv = append(initEnv, corev1.EnvVar{Name: "REPO_REF", Value: task.Spec.Repo.Ref})
	}
	if task.Spec.Task.Context != "" {
		initEnv = append(initEnv, corev1.EnvVar{
			Name: "TASK_CONTEXT", Value: task.Spec.Task.Context,
		})
	}
	if task.Spec.Task.ContextEncoding != "" {
		initEnv = append(initEnv, corev1.EnvVar{
			Name: "CONTEXT_ENCODING", Value: task.Spec.Task.ContextEncoding,
		})
	}

	// Build main container env — runner reads task input from files written by init container
	runnerEnv := []corev1.EnvVar{
		{Name: "SHEPHERD_TASK_ID", Value: task.Name},
		{Name: "SHEPHERD_REPO_URL", Value: task.Spec.Repo.URL},
		{Name: "SHEPHERD_CALLBACK_URL", Value: task.Spec.Callback.URL},
		{Name: "SHEPHERD_TASK_FILE", Value: "/task/description.txt"},
		{Name: "SHEPHERD_CONTEXT_FILE", Value: "/task/context.txt"},
	}
	if task.Spec.Repo.Ref != "" {
		runnerEnv = append(runnerEnv, corev1.EnvVar{Name: "SHEPHERD_REPO_REF", Value: task.Spec.Repo.Ref})
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: task.Namespace,
			Labels: map[string]string{
				"shepherd.io/task": task.Name,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:          &backoffLimit,
			ActiveDeadlineSeconds: &activeDeadlineSecs,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"shepherd.io/task": task.Name,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: task.Spec.Runner.ServiceAccountName,
					RestartPolicy:      corev1.RestartPolicyNever,
					InitContainers: []corev1.Container{
						{
							Name:  "github-auth",
							Image: cfg.InitImage,
							Env:   initEnv,
							VolumeMounts: []corev1.VolumeMount{
								{Name: "github-creds", MountPath: "/creds"},
								{Name: "runner-app-key", MountPath: "/secrets/runner-app-key", ReadOnly: true},
								{Name: "task-files", MountPath: "/task"},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:      "runner",
							Image:     cfg.AllowedRunnerImage,
							Env:       runnerEnv,
							Resources: task.Spec.Runner.Resources,
							VolumeMounts: []corev1.VolumeMount{
								{Name: "github-creds", MountPath: "/creds", ReadOnly: true},
								{Name: "task-files", MountPath: "/task", ReadOnly: true},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "github-creds",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "runner-app-key",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: cfg.RunnerSecretName,
								},
							},
						},
						{
							Name: "task-files",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
		},
	}

	// Set owner reference so Job is garbage-collected with the AgentTask
	if err := controllerutil.SetControllerReference(task, job, cfg.Scheme); err != nil {
		return nil, err
	}

	return job, nil
}
