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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
	sandboxextv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"
)

// sandboxConfig holds operator-level configuration needed to build SandboxClaims.
type sandboxConfig struct {
	Scheme *runtime.Scheme
}

func buildSandboxClaim(task *toolkitv1alpha1.AgentTask, cfg sandboxConfig) (*sandboxextv1alpha1.SandboxClaim, error) {
	claimName := task.Name
	if len(claimName) > 63 {
		return nil, fmt.Errorf("task name %q exceeds 63-character limit", claimName)
	}

	if task.Spec.Runner.SandboxTemplateName == "" {
		return nil, fmt.Errorf("sandboxTemplateName is required")
	}

	claim := &sandboxextv1alpha1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: task.Namespace,
			Labels: map[string]string{
				"shepherd.io/task": task.Name,
			},
		},
		Spec: sandboxextv1alpha1.SandboxClaimSpec{
			TemplateRef: sandboxextv1alpha1.SandboxTemplateRef{
				Name: task.Spec.Runner.SandboxTemplateName,
			},
		},
	}

	if err := controllerutil.SetControllerReference(task, claim, cfg.Scheme); err != nil {
		return nil, fmt.Errorf("setting owner reference: %w", err)
	}

	return claim, nil
}
