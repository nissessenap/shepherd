package main

import (
	"encoding/json"
	"time"

	"github.com/NissesSenap/shepherd/pkg/api"
)

const (
	maxThinkingLen    = 200
	maxBashInputLen   = 500
	maxResultLen      = 200
	truncationSuffix  = "... (truncated)"
	maxEditSummaryLen = 200
)

// StreamParser translates Claude Code stream-json NDJSON lines into TaskEvents.
type StreamParser struct {
	toolMap    map[string]string // tool_use_id → tool_name
	sequence   int64
	lastResult *ResultMetrics
}

// NewStreamParser creates a new stream-json parser.
func NewStreamParser() *StreamParser {
	return &StreamParser{
		toolMap: make(map[string]string),
	}
}

// ResultMetrics holds the metrics extracted from a CC result message.
type ResultMetrics struct {
	SessionID    string  `json:"session_id"`
	NumTurns     int     `json:"num_turns"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	DurationMS   int64   `json:"duration_ms"`
	Result       string  `json:"result"`
}

// LastResult returns the parsed result metrics from the last "result" message, if any.
func (p *StreamParser) LastResult() *ResultMetrics {
	return p.lastResult
}

// ccMessage is the top-level structure of a CC stream-json NDJSON line.
type ccMessage struct {
	Type    string     `json:"type"`
	Subtype string     `json:"subtype,omitempty"`
	Message *ccPayload `json:"message,omitempty"`

	// Result message fields (flattened at top level)
	SessionID    string  `json:"session_id,omitempty"`
	NumTurns     int     `json:"num_turns,omitempty"`
	TotalCostUSD float64 `json:"total_cost_usd,omitempty"`
	DurationMS   int64   `json:"duration_ms,omitempty"`
	IsError      bool    `json:"is_error,omitempty"`
	Result       string  `json:"result,omitempty"`
}

type ccPayload struct {
	Content []ccContent `json:"content"`
}

type ccContent struct {
	Type      string `json:"type"`                  // "text", "tool_use", "tool_result"
	Text      string `json:"text,omitempty"`        // for type="text"
	ID        string `json:"id,omitempty"`          // for type="tool_use"
	Name      string `json:"name,omitempty"`        // for type="tool_use"
	Input     any    `json:"input,omitempty"`       // for type="tool_use" (raw JSON)
	ToolUseID string `json:"tool_use_id,omitempty"` // for type="tool_result"
	Content   any    `json:"content,omitempty"`     // for type="tool_result" (string or structured)
	IsError   bool   `json:"is_error,omitempty"`    // for type="tool_result"
}

// ParseLine processes one NDJSON line and returns zero or more TaskEvents.
func (p *StreamParser) ParseLine(line []byte) []api.TaskEvent {
	if len(line) == 0 {
		return nil
	}

	var msg ccMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return p.errorEvent("failed to parse stream-json line: " + err.Error())
	}

	switch msg.Type {
	case "assistant":
		return p.parseAssistant(&msg)
	case "user":
		return p.parseUser(&msg)
	case "result":
		p.parseResult(&msg)
		return nil
	default:
		// system, init, etc. — skip silently
		return nil
	}
}

func (p *StreamParser) parseAssistant(msg *ccMessage) []api.TaskEvent {
	if msg.Message == nil {
		return nil
	}

	events := make([]api.TaskEvent, 0, len(msg.Message.Content))
	for _, content := range msg.Message.Content {
		switch content.Type {
		case "text":
			if content.Text == "" {
				continue
			}
			p.sequence++
			events = append(events, api.TaskEvent{
				Sequence:  p.sequence,
				Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
				Type:      api.EventTypeThinking,
				Summary:   truncate(content.Text, maxThinkingLen),
			})

		case "tool_use":
			if content.ID != "" && content.Name != "" {
				p.toolMap[content.ID] = content.Name
			}
			p.sequence++
			event := api.TaskEvent{
				Sequence:  p.sequence,
				Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
				Type:      api.EventTypeToolCall,
				Summary:   toolCallSummary(content.Name, content.Input),
				Tool:      content.Name,
			}
			if content.Input != nil {
				event.Input = condensedInput(content.Name, content.Input)
			}
			events = append(events, event)
		}
	}
	return events
}

func (p *StreamParser) parseUser(msg *ccMessage) []api.TaskEvent {
	if msg.Message == nil {
		return nil
	}

	events := make([]api.TaskEvent, 0, len(msg.Message.Content))
	for _, content := range msg.Message.Content {
		if content.Type != "tool_result" {
			continue
		}

		toolName := p.toolMap[content.ToolUseID]
		p.sequence++

		resultText := extractToolResultText(content.Content)

		events = append(events, api.TaskEvent{
			Sequence:  p.sequence,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Type:      api.EventTypeToolResult,
			Summary:   truncate(resultText, maxResultLen),
			Tool:      toolName,
			Output: &api.TaskEventOutput{
				Success: !content.IsError,
				Summary: truncate(resultText, maxResultLen),
			},
		})
	}
	return events
}

func (p *StreamParser) parseResult(msg *ccMessage) {
	p.lastResult = &ResultMetrics{
		SessionID:    msg.SessionID,
		NumTurns:     msg.NumTurns,
		TotalCostUSD: msg.TotalCostUSD,
		DurationMS:   msg.DurationMS,
		Result:       msg.Result,
	}
}

func (p *StreamParser) errorEvent(message string) []api.TaskEvent {
	p.sequence++
	return []api.TaskEvent{{
		Sequence:  p.sequence,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Type:      api.EventTypeError,
		Summary:   message,
	}}
}

// toolCallSummary generates a human-readable one-liner for a tool call.
func toolCallSummary(toolName string, input any) string {
	inputMap, ok := toStringMap(input)
	if !ok {
		return toolName
	}

	switch toolName {
	case "Read":
		if fp, ok := inputMap["file_path"].(string); ok {
			return "Reading " + fp
		}
	case "Write":
		if fp, ok := inputMap["file_path"].(string); ok {
			return "Writing " + fp
		}
	case "Edit":
		if fp, ok := inputMap["file_path"].(string); ok {
			return "Editing " + fp
		}
	case "Bash":
		if cmd, ok := inputMap["command"].(string); ok {
			return truncate(cmd, maxBashInputLen)
		}
	case "Glob":
		if pattern, ok := inputMap["pattern"].(string); ok {
			return "Searching for " + pattern
		}
	case "Grep":
		if pattern, ok := inputMap["pattern"].(string); ok {
			return "Searching for " + pattern
		}
	}
	return toolName
}

// condensedInput returns a truncated representation of tool input for the event.
func condensedInput(toolName string, input any) map[string]any {
	inputMap, ok := toStringMap(input)
	if !ok {
		return nil
	}

	switch toolName {
	case "Bash":
		result := make(map[string]any)
		if cmd, ok := inputMap["command"].(string); ok {
			result["command"] = truncate(cmd, maxBashInputLen)
		}
		return result
	case "Read", "Write", "Glob", "Grep":
		// Pass through small structured inputs
		result := make(map[string]any)
		for k, v := range inputMap {
			if s, ok := v.(string); ok {
				result[k] = truncate(s, maxResultLen)
			} else {
				result[k] = v
			}
		}
		return result
	case "Edit":
		// Only include file path and sizes, not the full old/new strings
		result := make(map[string]any)
		if fp, ok := inputMap["file_path"].(string); ok {
			result["file_path"] = fp
		}
		if old, ok := inputMap["old_string"].(string); ok {
			result["old_string_length"] = len(old)
		}
		if new_, ok := inputMap["new_string"].(string); ok {
			result["new_string_length"] = len(new_)
		}
		return result
	default:
		result := make(map[string]any)
		for k, v := range inputMap {
			if s, ok := v.(string); ok {
				result[k] = truncate(s, maxEditSummaryLen)
			} else {
				result[k] = v
			}
		}
		return result
	}
}

// extractToolResultText extracts a string from tool_result content.
// Content can be a plain string or a structured array.
func extractToolResultText(content any) string {
	if s, ok := content.(string); ok {
		return s
	}

	// Try as array of content blocks
	if arr, ok := content.([]any); ok {
		var parts []string
		for _, item := range arr {
			if m, ok := item.(map[string]any); ok {
				if text, ok := m["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		if len(parts) > 0 {
			return parts[0] // just use first block
		}
	}
	return ""
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= len(truncationSuffix) {
		return s[:maxLen]
	}
	return s[:maxLen-len(truncationSuffix)] + truncationSuffix
}

func toStringMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}
