package agent

import (
	"encoding/json"
	"time"
)

// claudeSDKMessage is a top-level message from the Claude stream-json output.
type claudeSDKMessage struct {
	Type       string                            `json:"type"`
	Message    json.RawMessage                   `json:"message,omitempty"`
	Subtype    string                            `json:"subtype,omitempty"`
	SessionID  string                            `json:"session_id,omitempty"`
	Model      string                            `json:"model,omitempty"`
	ResultText string                            `json:"result,omitempty"`
	IsError    bool                              `json:"is_error,omitempty"`
	Usage      *claudeUsage                      `json:"usage,omitempty"`
	ModelUsage map[string]claudeResultModelUsage `json:"modelUsage,omitempty"`
	Log        *claudeLogEntry                   `json:"log,omitempty"`
	ReceivedAt time.Time                         `json:"-"`
}

type claudeLogEntry struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

type claudeMessageContent struct {
	Role    string               `json:"role"`
	Model   string               `json:"model"`
	Content []claudeContentBlock `json:"content"`
	Usage   *claudeUsage         `json:"usage,omitempty"`
}

type claudeUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

type claudeResultModelUsage struct {
	InputTokens              int64 `json:"inputTokens"`
	OutputTokens             int64 `json:"outputTokens"`
	CacheReadInputTokens     int64 `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int64 `json:"cacheCreationInputTokens"`
}

type claudeContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

type claudeInputTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type claudeInputMessage struct {
	Role    string                 `json:"role"`
	Content []claudeInputTextBlock `json:"content"`
}

type claudeInputPayload struct {
	Type    string             `json:"type"`
	Message claudeInputMessage `json:"message"`
}
