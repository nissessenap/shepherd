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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventHub_PublishAndSubscribe(t *testing.T) {
	hub := NewEventHub()

	events := []TaskEvent{
		{Sequence: 1, Timestamp: "2026-01-01T00:00:00Z", Type: EventTypeThinking, Summary: "Analyzing code"},
		{Sequence: 2, Timestamp: "2026-01-01T00:00:01Z", Type: EventTypeToolCall, Summary: "Reading file", Tool: "Read"},
	}

	hub.Publish("task-1", events)

	history, ch, unsub := hub.Subscribe("task-1", 0)
	defer unsub()

	assert.Len(t, history, 2)
	assert.Equal(t, int64(1), history[0].Sequence)
	assert.Equal(t, int64(2), history[1].Sequence)
	assert.NotNil(t, ch)
}

func TestEventHub_SubscribeAfterParameter(t *testing.T) {
	hub := NewEventHub()

	events := []TaskEvent{
		{Sequence: 1, Timestamp: "2026-01-01T00:00:00Z", Type: EventTypeThinking, Summary: "First"},
		{Sequence: 2, Timestamp: "2026-01-01T00:00:01Z", Type: EventTypeToolCall, Summary: "Second"},
		{Sequence: 3, Timestamp: "2026-01-01T00:00:02Z", Type: EventTypeToolResult, Summary: "Third"},
	}

	hub.Publish("task-1", events)

	// Subscribe with after=1 should only get events 2 and 3
	history, _, unsub := hub.Subscribe("task-1", 1)
	defer unsub()

	assert.Len(t, history, 2)
	assert.Equal(t, int64(2), history[0].Sequence)
	assert.Equal(t, int64(3), history[1].Sequence)
}

func TestEventHub_LiveEvents(t *testing.T) {
	hub := NewEventHub()

	// Subscribe first, then publish
	_, ch, unsub := hub.Subscribe("task-1", 0)
	defer unsub()

	require.NotNil(t, ch)

	hub.Publish("task-1", []TaskEvent{
		{Sequence: 1, Timestamp: "2026-01-01T00:00:00Z", Type: EventTypeThinking, Summary: "Live event"},
	})

	select {
	case e := <-ch:
		assert.Equal(t, int64(1), e.Sequence)
		assert.Equal(t, "Live event", e.Summary)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live event")
	}
}

func TestEventHub_RingBufferOverflow(t *testing.T) {
	hub := NewEventHub()

	// Publish more than maxEventsPerTask events
	events := make([]TaskEvent, maxEventsPerTask+50)
	for i := range events {
		events[i] = TaskEvent{
			Sequence:  int64(i + 1),
			Timestamp: "2026-01-01T00:00:00Z",
			Type:      EventTypeThinking,
			Summary:   "Event",
		}
	}

	hub.Publish("task-1", events)

	history, _, unsub := hub.Subscribe("task-1", 0)
	defer unsub()

	// Should have exactly maxEventsPerTask events (oldest dropped)
	assert.Len(t, history, maxEventsPerTask)
	// Oldest should be event 51 (first 50 dropped)
	assert.Equal(t, int64(51), history[0].Sequence)
	assert.Equal(t, int64(maxEventsPerTask+50), history[len(history)-1].Sequence)
}

func TestEventHub_CompleteClosesSubscribers(t *testing.T) {
	hub := NewEventHub()

	_, ch, unsub := hub.Subscribe("task-1", 0)
	defer unsub()

	require.NotNil(t, ch)

	hub.Complete("task-1")

	// Channel should be closed
	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel should be closed after Complete")
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for channel close")
	}
}

func TestEventHub_CompleteAfterDone_IsNoop(t *testing.T) {
	hub := NewEventHub()

	_, ch, unsub := hub.Subscribe("task-1", 0)
	defer unsub()

	hub.Complete("task-1")

	// Channel should be closed
	<-ch

	// Second Complete should not panic
	hub.Complete("task-1")
}

func TestEventHub_PublishAfterComplete_IsIgnored(t *testing.T) {
	hub := NewEventHub()

	// Publish first to create the stream, then complete
	hub.Publish("task-1", []TaskEvent{
		{Sequence: 1, Timestamp: "2026-01-01T00:00:00Z", Type: EventTypeThinking, Summary: "Before complete"},
	})

	hub.Complete("task-1")

	// Events published after complete should be ignored
	hub.Publish("task-1", []TaskEvent{
		{Sequence: 2, Timestamp: "2026-01-01T00:00:01Z", Type: EventTypeThinking, Summary: "After complete"},
	})

	history, ch, _ := hub.Subscribe("task-1", 0)

	assert.Len(t, history, 1, "should only have the event published before complete")
	assert.Equal(t, "Before complete", history[0].Summary)
	assert.Nil(t, ch, "channel should be nil for completed stream")
}

func TestEventHub_SubscribeAfterComplete_ReturnsNilChannel(t *testing.T) {
	hub := NewEventHub()

	hub.Publish("task-1", []TaskEvent{
		{Sequence: 1, Timestamp: "2026-01-01T00:00:00Z", Type: EventTypeThinking, Summary: "Before complete"},
	})

	hub.Complete("task-1")

	history, ch, _ := hub.Subscribe("task-1", 0)

	assert.Len(t, history, 1)
	assert.Nil(t, ch, "channel should be nil for completed stream")
}

func TestEventHub_Cleanup(t *testing.T) {
	hub := NewEventHub()

	hub.Publish("task-1", []TaskEvent{
		{Sequence: 1, Timestamp: "2026-01-01T00:00:00Z", Type: EventTypeThinking, Summary: "Event"},
	})

	hub.Cleanup("task-1")

	// After cleanup, subscribe returns empty history and a fresh stream
	history, ch, unsub := hub.Subscribe("task-1", 0)
	defer unsub()

	assert.Empty(t, history)
	assert.NotNil(t, ch, "should get fresh stream after cleanup")
}

func TestEventHub_CompleteNonexistentTask(t *testing.T) {
	hub := NewEventHub()
	// Should not panic
	hub.Complete("nonexistent")
}

func TestEventHub_SlowSubscriberEviction(t *testing.T) {
	hub := NewEventHub()
	taskID := "task-slow"

	// Subscribe before publishing so the channel is registered
	_, ch, unsub := hub.Subscribe(taskID, 0)
	defer unsub()

	require.NotNil(t, ch)

	// Publish more than the subscriber channel capacity (64) without reading,
	// so Publish() hits the default branch and closes the slow subscriber's channel.
	events := make([]TaskEvent, 65)
	for i := range events {
		events[i] = TaskEvent{
			Sequence:  int64(i + 1),
			Timestamp: "2026-01-01T00:00:00Z",
			Type:      EventTypeThinking,
			Summary:   "Slow event",
		}
	}
	hub.Publish(taskID, events)

	// Drain the channel until we observe it closed (ok=false).
	// The first 64 events fill the buffer; the 65th triggers eviction and close().
	// We must read past the buffered values to reach the close signal.
	deadline := time.After(time.Second)
	closed := false
	for !closed {
		select {
		case _, ok := <-ch:
			if !ok {
				closed = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for channel close after eviction")
		}
	}

	// IsStreamDone should be false — eviction is not the same as task completion
	assert.False(t, hub.IsStreamDone(taskID), "stream should NOT be marked done after slow-subscriber eviction")
}

func TestEventHub_CleanupWithActiveSubscriber(t *testing.T) {
	hub := NewEventHub()

	_, ch, unsub := hub.Subscribe("task-cleanup-active", 0)
	defer unsub()

	require.NotNil(t, ch)

	// Cleanup calls Complete internally, which must close subscriber channels
	hub.Cleanup("task-cleanup-active")

	// The subscriber's channel should be closed so a goroutine ranging over it can exit
	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel should be closed after Cleanup")
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for channel close after Cleanup")
	}
}

func TestEventHub_ConcurrentPublishSubscribe(t *testing.T) {
	hub := NewEventHub()
	taskID := "task-concurrent"

	var wg sync.WaitGroup
	const numPublishers = 5
	const numEventsPerPublisher = 100

	// Start subscribers
	_, ch, unsub := hub.Subscribe(taskID, 0)
	defer unsub()

	received := make([]TaskEvent, 0, numPublishers*numEventsPerPublisher)
	var receivedMu sync.Mutex

	wg.Go(func() {
		for e := range ch {
			receivedMu.Lock()
			received = append(received, e)
			receivedMu.Unlock()
		}
	})

	// Start publishers concurrently
	for p := range numPublishers {
		wg.Go(func() {
			for i := range numEventsPerPublisher {
				hub.Publish(taskID, []TaskEvent{
					{
						Sequence:  int64(p*numEventsPerPublisher + i + 1),
						Timestamp: "2026-01-01T00:00:00Z",
						Type:      EventTypeThinking,
						Summary:   "Concurrent event",
					},
				})
			}
		})
	}

	// Wait for publishers, then complete
	time.Sleep(100 * time.Millisecond)
	hub.Complete(taskID)
	wg.Wait()

	receivedMu.Lock()
	assert.NotEmpty(t, received, "should have received some events")
	receivedMu.Unlock()
}
