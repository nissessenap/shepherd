package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/NissesSenap/shepherd/pkg/api"
)

func TestParseAssistantThinking(t *testing.T) {
	p := NewStreamParser()
	line := mustJSON(t, map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": "Let me analyze the codebase structure to understand the project layout..."},
			},
		},
	})

	events := p.ParseLine(line)
	require.Len(t, events, 1)
	assert.Equal(t, api.EventTypeThinking, events[0].Type)
	assert.Equal(t, int64(1), events[0].Sequence)
	assert.Contains(t, events[0].Summary, "Let me analyze")
	assert.Empty(t, events[0].Tool)
}

func TestParseAssistantToolUse(t *testing.T) {
	p := NewStreamParser()
	line := mustJSON(t, map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []any{
				map[string]any{
					"type":  "tool_use",
					"id":    "toolu_01ABC",
					"name":  "Read",
					"input": map[string]any{"file_path": "/src/auth.go"},
				},
			},
		},
	})

	events := p.ParseLine(line)
	require.Len(t, events, 1)
	assert.Equal(t, api.EventTypeToolCall, events[0].Type)
	assert.Equal(t, int64(1), events[0].Sequence)
	assert.Equal(t, "Read", events[0].Tool)
	assert.Equal(t, "Reading /src/auth.go", events[0].Summary)
	assert.Equal(t, "/src/auth.go", events[0].Input["file_path"])
}

func TestParseAssistantMixedContent(t *testing.T) {
	p := NewStreamParser()
	line := mustJSON(t, map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": "Let me read the file..."},
				map[string]any{
					"type":  "tool_use",
					"id":    "toolu_01XYZ",
					"name":  "Read",
					"input": map[string]any{"file_path": "/src/main.go"},
				},
			},
		},
	})

	events := p.ParseLine(line)
	require.Len(t, events, 2)
	assert.Equal(t, api.EventTypeThinking, events[0].Type)
	assert.Equal(t, int64(1), events[0].Sequence)
	assert.Equal(t, api.EventTypeToolCall, events[1].Type)
	assert.Equal(t, int64(2), events[1].Sequence)
}

func TestParseUserToolResult(t *testing.T) {
	p := NewStreamParser()

	// First, register the tool call so the result can be correlated
	p.ParseLine(mustJSON(t, map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []any{
				map[string]any{
					"type":  "tool_use",
					"id":    "toolu_01ABC",
					"name":  "Read",
					"input": map[string]any{"file_path": "/src/auth.go"},
				},
			},
		},
	}))

	// Now parse the result
	line := mustJSON(t, map[string]any{
		"type": "user",
		"message": map[string]any{
			"content": []any{
				map[string]any{
					"type":        "tool_result",
					"tool_use_id": "toolu_01ABC",
					"content":     "package auth\n\nfunc Login() {}",
				},
			},
		},
	})

	events := p.ParseLine(line)
	require.Len(t, events, 1)
	assert.Equal(t, api.EventTypeToolResult, events[0].Type)
	assert.Equal(t, "Read", events[0].Tool)
	assert.True(t, events[0].Output.Success)
	assert.Contains(t, events[0].Output.Summary, "package auth")
}

func TestParseUserToolResultError(t *testing.T) {
	p := NewStreamParser()

	p.ParseLine(mustJSON(t, map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []any{
				map[string]any{
					"type":  "tool_use",
					"id":    "toolu_01ERR",
					"name":  "Bash",
					"input": map[string]any{"command": "go test ./..."},
				},
			},
		},
	}))

	line := mustJSON(t, map[string]any{
		"type": "user",
		"message": map[string]any{
			"content": []any{
				map[string]any{
					"type":        "tool_result",
					"tool_use_id": "toolu_01ERR",
					"content":     "FAIL: tests failed",
					"is_error":    true,
				},
			},
		},
	})

	events := p.ParseLine(line)
	require.Len(t, events, 1)
	assert.Equal(t, api.EventTypeToolResult, events[0].Type)
	assert.False(t, events[0].Output.Success)
}

func TestParseResultMessage(t *testing.T) {
	p := NewStreamParser()
	line := mustJSON(t, map[string]any{
		"type":           "result",
		"subtype":        "success",
		"is_error":       false,
		"total_cost_usd": 0.34,
		"num_turns":      4,
		"duration_ms":    3400,
		"session_id":     "sess-abc123",
	})

	events := p.ParseLine(line)
	assert.Empty(t, events, "result messages should not produce TaskEvents")

	metrics := p.LastResult()
	require.NotNil(t, metrics)
	assert.Equal(t, "sess-abc123", metrics.SessionID)
	assert.Equal(t, 4, metrics.NumTurns)
	assert.InDelta(t, 0.34, metrics.TotalCostUSD, 0.001)
	assert.Equal(t, int64(3400), metrics.DurationMS)
}

func TestParseSystemMessage(t *testing.T) {
	p := NewStreamParser()
	line := mustJSON(t, map[string]any{
		"type":    "system",
		"subtype": "init",
	})

	events := p.ParseLine(line)
	assert.Empty(t, events, "system messages should be silently skipped")
}

func TestSequenceMonotonicallyIncreases(t *testing.T) {
	p := NewStreamParser()

	for range 5 {
		p.ParseLine(mustJSON(t, map[string]any{
			"type": "assistant",
			"message": map[string]any{
				"content": []any{
					map[string]any{"type": "text", "text": "thinking..."},
				},
			},
		}))
	}

	// Parse one more to check sequence
	events := p.ParseLine(mustJSON(t, map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": "more thinking"},
			},
		},
	}))
	require.Len(t, events, 1)
	assert.Equal(t, int64(6), events[0].Sequence)
}

func TestParseMalformedJSON(t *testing.T) {
	p := NewStreamParser()
	events := p.ParseLine([]byte(`{this is not valid json`))
	require.Len(t, events, 1)
	assert.Equal(t, api.EventTypeError, events[0].Type)
	assert.Contains(t, events[0].Summary, "failed to parse")
}

func TestParseEmptyLine(t *testing.T) {
	p := NewStreamParser()
	events := p.ParseLine([]byte(""))
	assert.Empty(t, events)
}

func TestParseNilMessage(t *testing.T) {
	p := NewStreamParser()
	line := mustJSON(t, map[string]any{
		"type": "assistant",
		// no message field
	})
	events := p.ParseLine(line)
	assert.Empty(t, events)
}

func TestToolCallSummaries(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		input    map[string]any
		want     string
	}{
		{
			name:     "Read file",
			toolName: "Read",
			input:    map[string]any{"file_path": "src/main.go"},
			want:     "Reading src/main.go",
		},
		{
			name:     "Write file",
			toolName: "Write",
			input:    map[string]any{"file_path": "src/new.go"},
			want:     "Writing src/new.go",
		},
		{
			name:     "Edit file",
			toolName: "Edit",
			input:    map[string]any{"file_path": "src/fix.go"},
			want:     "Editing src/fix.go",
		},
		{
			name:     "Bash command",
			toolName: "Bash",
			input:    map[string]any{"command": "go test ./..."},
			want:     "go test ./...",
		},
		{
			name:     "Glob pattern",
			toolName: "Glob",
			input:    map[string]any{"pattern": "**/*.go"},
			want:     "Searching for **/*.go",
		},
		{
			name:     "Grep pattern",
			toolName: "Grep",
			input:    map[string]any{"pattern": "TODO"},
			want:     "Searching for TODO",
		},
		{
			name:     "Unknown tool",
			toolName: "CustomTool",
			input:    map[string]any{"foo": "bar"},
			want:     "CustomTool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toolCallSummary(tt.toolName, tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTruncation(t *testing.T) {
	t.Run("short string unchanged", func(t *testing.T) {
		assert.Equal(t, "hello", truncate("hello", 200))
	})

	t.Run("long string truncated", func(t *testing.T) {
		long := make([]byte, 300)
		for i := range long {
			long[i] = 'a'
		}
		result := truncate(string(long), 200)
		assert.Len(t, result, 200)
		assert.Contains(t, result, truncationSuffix)
	})
}

func TestCondensedInputEdit(t *testing.T) {
	input := map[string]any{
		"file_path":  "src/main.go",
		"old_string": "func old() {}",
		"new_string": "func new() { return nil }",
	}

	result := condensedInput("Edit", input)
	assert.Equal(t, "src/main.go", result["file_path"])
	assert.Equal(t, len("func old() {}"), result["old_string_length"])
	assert.Equal(t, len("func new() { return nil }"), result["new_string_length"])
	// old_string and new_string themselves should NOT be present
	assert.NotContains(t, result, "old_string")
	assert.NotContains(t, result, "new_string")
}

func TestCondensedInputBash(t *testing.T) {
	longCmd := make([]byte, 600)
	for i := range longCmd {
		longCmd[i] = 'x'
	}
	input := map[string]any{
		"command": string(longCmd),
	}

	result := condensedInput("Bash", input)
	cmd, ok := result["command"].(string)
	require.True(t, ok)
	assert.LessOrEqual(t, len(cmd), maxBashInputLen)
}

func TestToolResultCorrelation(t *testing.T) {
	p := NewStreamParser()

	// Register two tool calls
	p.ParseLine(mustJSON(t, map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []any{
				map[string]any{"type": "tool_use", "id": "toolu_A", "name": "Read", "input": map[string]any{"file_path": "a.go"}},
				map[string]any{"type": "tool_use", "id": "toolu_B", "name": "Bash", "input": map[string]any{"command": "ls"}},
			},
		},
	}))

	// Result for toolu_B should correlate to Bash
	events := p.ParseLine(mustJSON(t, map[string]any{
		"type": "user",
		"message": map[string]any{
			"content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "toolu_B", "content": "file1\nfile2"},
			},
		},
	}))
	require.Len(t, events, 1)
	assert.Equal(t, "Bash", events[0].Tool)

	// Result for toolu_A should correlate to Read
	events = p.ParseLine(mustJSON(t, map[string]any{
		"type": "user",
		"message": map[string]any{
			"content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "toolu_A", "content": "package main"},
			},
		},
	}))
	require.Len(t, events, 1)
	assert.Equal(t, "Read", events[0].Tool)
}

func TestExtractToolResultTextStructured(t *testing.T) {
	// Tool result content can be a structured array of content blocks
	content := []any{
		map[string]any{"type": "text", "text": "first block"},
		map[string]any{"type": "text", "text": "second block"},
	}
	result := extractToolResultText(content)
	assert.Equal(t, "first block", result)
}

func TestThinkingTruncation(t *testing.T) {
	p := NewStreamParser()
	longText := make([]byte, 500)
	for i := range longText {
		longText[i] = 'a'
	}

	line := mustJSON(t, map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": string(longText)},
			},
		},
	})

	events := p.ParseLine(line)
	require.Len(t, events, 1)
	assert.LessOrEqual(t, len(events[0].Summary), maxThinkingLen)
	assert.Contains(t, events[0].Summary, truncationSuffix)
}

func TestEmptyTextSkipped(t *testing.T) {
	p := NewStreamParser()
	line := mustJSON(t, map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": ""},
			},
		},
	})
	events := p.ParseLine(line)
	assert.Empty(t, events)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
