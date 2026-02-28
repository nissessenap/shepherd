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
	"sync"

	"k8s.io/apimachinery/pkg/util/rand"
)

const maxEventsPerTask = 1000

// EventHub provides in-memory per-task event fan-out for WebSocket streaming.
type EventHub struct {
	mu    sync.RWMutex
	tasks map[string]*taskStream
}

type taskStream struct {
	mu          sync.RWMutex
	events      []TaskEvent
	subscribers map[string]chan TaskEvent
	done        bool
}

// NewEventHub creates a new EventHub.
func NewEventHub() *EventHub {
	return &EventHub{
		tasks: make(map[string]*taskStream),
	}
}

// getOrCreateStream returns the taskStream for the given task, creating it if needed.
func (h *EventHub) getOrCreateStream(taskID string) *taskStream {
	h.mu.RLock()
	ts, ok := h.tasks[taskID]
	h.mu.RUnlock()
	if ok {
		return ts
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	// Double-check after acquiring write lock
	if ts, ok := h.tasks[taskID]; ok {
		return ts
	}
	ts = &taskStream{
		subscribers: make(map[string]chan TaskEvent),
	}
	h.tasks[taskID] = ts
	return ts
}

// Publish appends events to the ring buffer and fans out to subscribers.
func (h *EventHub) Publish(taskID string, events []TaskEvent) {
	ts := h.getOrCreateStream(taskID)

	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.done {
		return
	}

	for _, e := range events {
		if len(ts.events) >= maxEventsPerTask {
			// Ring buffer: drop oldest
			ts.events = ts.events[1:]
		}
		ts.events = append(ts.events, e)
	}

	// Fan out to subscribers
	for id, ch := range ts.subscribers {
	events:
		for _, e := range events {
			select {
			case ch <- e:
			default:
				// Subscriber too slow — drop and remove
				close(ch)
				delete(ts.subscribers, id)
				break events
			}
		}
	}
}

// Subscribe returns historical events with sequence > after, plus a channel for live events.
// Returns nil channel if the stream is already done.
func (h *EventHub) Subscribe(taskID string, after int64) (history []TaskEvent, ch <-chan TaskEvent, unsubscribe func()) {
	ts := h.getOrCreateStream(taskID)

	ts.mu.Lock()
	defer ts.mu.Unlock()

	// Replay historical events
	for _, e := range ts.events {
		if e.Sequence > after {
			history = append(history, e)
		}
	}

	if ts.done {
		return history, nil, func() {}
	}

	// Create subscriber channel
	subCh := make(chan TaskEvent, 64)
	subID := rand.String(8)
	ts.subscribers[subID] = subCh

	unsubscribe = func() {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		if _, ok := ts.subscribers[subID]; ok {
			delete(ts.subscribers, subID)
			close(subCh)
		}
	}

	return history, subCh, unsubscribe
}

// Complete marks a task stream as done and closes all subscriber channels.
func (h *EventHub) Complete(taskID string) {
	h.mu.RLock()
	ts, ok := h.tasks[taskID]
	h.mu.RUnlock()
	if !ok {
		return
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.done {
		return
	}
	ts.done = true

	for id, ch := range ts.subscribers {
		close(ch)
		delete(ts.subscribers, id)
	}
}

// IsStreamDone reports whether the given task stream has been completed via Complete().
// Returns false if the stream does not exist or has not been completed.
func (h *EventHub) IsStreamDone(taskID string) bool {
	h.mu.RLock()
	ts, ok := h.tasks[taskID]
	h.mu.RUnlock()
	if !ok {
		return false
	}
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.done
}

// Cleanup removes a task stream entirely.
// It calls Complete first to close any subscriber channels so that goroutines
// blocked on "for e := range ch" are not leaked.
func (h *EventHub) Cleanup(taskID string) {
	h.Complete(taskID)

	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.tasks, taskID)
}
