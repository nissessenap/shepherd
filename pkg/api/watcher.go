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

package api

import (
	"context"
	"fmt"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	toolscache "k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

// statusWatcher watches AgentTask resources for terminal states
// and sends adapter callbacks. Uses a standalone controller-runtime
// cache for typed informers without the full manager overhead.
type statusWatcher struct {
	client   client.Client
	callback *callbackSender
	cache    ctrlcache.Cache
}

// run starts the cache informer and blocks until the context is cancelled.
func (w *statusWatcher) run(ctx context.Context) error {
	log := ctrl.Log.WithName("status-watcher")

	// Get a typed informer for AgentTask — no unstructured conversion needed
	informer, err := w.cache.GetInformer(ctx, &toolkitv1alpha1.AgentTask{})
	if err != nil {
		return fmt.Errorf("getting AgentTask informer: %w", err)
	}

	_, err = informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		UpdateFunc: func(_, newObj any) {
			newTask, ok := newObj.(*toolkitv1alpha1.AgentTask)
			if !ok {
				log.Error(nil, "unexpected object type in update", "type", fmt.Sprintf("%T", newObj))
				return
			}
			w.handleTerminalTransition(ctx, newTask)
		},
	})
	if err != nil {
		return fmt.Errorf("adding event handler: %w", err)
	}

	log.Info("status watcher ready")
	// Block until context is cancelled (cache.Start is called separately in server.go)
	<-ctx.Done()
	return nil
}

// handleTerminalTransition checks if a task has reached a terminal state
// and sends the adapter callback if not already notified.
func (w *statusWatcher) handleTerminalTransition(ctx context.Context, task *toolkitv1alpha1.AgentTask) {
	log := ctrl.Log.WithName("status-watcher")

	// Check if task is terminal
	if !task.IsTerminal() {
		return
	}

	// Check if already notified (any status means someone is handling it)
	notifiedCond := apimeta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionNotified)
	if notifiedCond != nil {
		return
	}

	// Determine event type from Succeeded condition
	succeededCond := apimeta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
	event := EventFailed
	if succeededCond.Status == metav1.ConditionTrue {
		event = EventCompleted
	}

	// Build callback payload
	payload := CallbackPayload{
		TaskID:  task.Name,
		Event:   event,
		Message: succeededCond.Message,
		Details: map[string]any{},
	}
	if task.Status.Result.PRUrl != "" {
		payload.Details["pr_url"] = task.Status.Result.PRUrl
	}
	if task.Status.Result.Error != "" {
		payload.Details["error"] = task.Status.Result.Error
	}

	// Send callback
	callbackURL := task.Spec.Callback.URL
	if err := w.callback.send(ctx, callbackURL, payload); err != nil {
		log.Error(err, "failed to send terminal callback",
			"task", task.Name, "event", event, "callbackURL", callbackURL)

		// Set Notified condition as failed
		w.setNotifiedCondition(ctx, task, toolkitv1alpha1.ReasonCallbackFailed,
			fmt.Sprintf("Callback failed: %v", err))
		return
	}

	log.Info("sent terminal callback to adapter",
		"task", task.Name, "event", event, "callbackURL", callbackURL)

	// Set Notified condition as sent
	w.setNotifiedCondition(ctx, task, toolkitv1alpha1.ReasonCallbackSent,
		fmt.Sprintf("Adapter notified: %s", event))
}

func (w *statusWatcher) setNotifiedCondition(ctx context.Context, task *toolkitv1alpha1.AgentTask, reason, message string) {
	log := ctrl.Log.WithName("status-watcher")

	// Re-fetch to avoid conflicts
	var fresh toolkitv1alpha1.AgentTask
	if err := w.client.Get(ctx, client.ObjectKeyFromObject(task), &fresh); err != nil {
		log.Error(err, "failed to re-fetch task for Notified condition", "task", task.Name)
		return
	}

	apimeta.SetStatusCondition(&fresh.Status.Conditions, metav1.Condition{
		Type:               toolkitv1alpha1.ConditionNotified,
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: fresh.Generation,
	})

	if err := w.client.Status().Update(ctx, &fresh); err != nil {
		log.Error(err, "failed to set Notified condition", "task", task.Name)
	}
}
