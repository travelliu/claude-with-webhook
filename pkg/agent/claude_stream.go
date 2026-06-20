package agent

import (
	"encoding/json"
	"log/slog"
	"strings"
)

// Claude SDK content block type constants.
const (
	claudeBlockText             = "text"
	claudeBlockThinking         = "thinking"
	claudeBlockRedactedThinking = "redacted_thinking"
	claudeBlockToolUse          = "tool_use"
	claudeBlockToolResult       = "tool_result"
)

func handleClaudeAssistant(msg claudeSDKMessage, ch chan<- Message, output *strings.Builder, usage map[string]TokenUsage) {
	var content claudeMessageContent
	if err := json.Unmarshal(msg.Message, &content); err != nil {
		return
	}

	if content.Usage != nil && content.Model != "" {
		u := usage[content.Model]
		u.InputTokens += content.Usage.InputTokens
		u.OutputTokens += content.Usage.OutputTokens
		u.CacheReadTokens += content.Usage.CacheReadInputTokens
		u.CacheWriteTokens += content.Usage.CacheCreationInputTokens
		usage[content.Model] = u
	}

	for _, block := range content.Content {
		switch strings.ToLower(block.Type) {
		case claudeBlockText:
			if block.Text != "" {
				output.WriteString(block.Text)
				slog.Debug("claude text", "len", len(block.Text), "preview", truncateForLog(block.Text, 100))
				trySend(ch, Message{Type: MessageText, Content: block.Text, ReceivedAt: msg.ReceivedAt})
			}
		case claudeBlockThinking:
			if block.Text != "" {
				trySend(ch, Message{Type: MessageThinking, Content: block.Text, ReceivedAt: msg.ReceivedAt})
			}
			if block.Thinking != "" {
				trySend(ch, Message{Type: MessageThinking, Content: block.Thinking, ReceivedAt: msg.ReceivedAt})
			}
		case claudeBlockRedactedThinking:
			// Thinking content redacted by the API — skip silently.
		case claudeBlockToolUse:
			var input map[string]any
			if block.Input != nil {
				_ = json.Unmarshal(block.Input, &input)
			}
			slog.Debug("claude tool_use", "tool", block.Name, "call_id", block.ID)
			trySend(ch, Message{
				Type:       MessageToolUse,
				Tool:       block.Name,
				CallID:     block.ID,
				Input:      input,
				ReceivedAt: msg.ReceivedAt,
			})
		case claudeBlockToolResult:
			slog.Debug("claude tool_result", "call_id", block.ToolUseID)
		default:
			slog.Debug("claude unknown block", "type", block.Type)
		}
	}
}

func handleClaudeUser(msg claudeSDKMessage, ch chan<- Message) {
	var content claudeMessageContent
	if err := json.Unmarshal(msg.Message, &content); err != nil {
		return
	}
	for _, block := range content.Content {
		switch strings.ToLower(block.Type) {
		case claudeBlockToolResult:
			resultStr := ""
			if block.Content != nil {
				var s string
				if err := json.Unmarshal(block.Content, &s); err == nil {
					resultStr = s
				} else {
					resultStr = string(block.Content)
				}
			}
			trySend(ch, Message{
				Type:       MessageToolResult,
				CallID:     block.ToolUseID,
				Output:     resultStr,
				ReceivedAt: msg.ReceivedAt,
			})
		case claudeBlockText:
			// Embedded text in user messages — no action needed.
		default:
			slog.Warn("unhandled user block type", "type", block.Type)
		}
	}
}

// claudeResultUsage extracts per-model token usage from a result message.
func claudeResultUsage(msg claudeSDKMessage, fallbackModel string) map[string]TokenUsage {
	if len(msg.ModelUsage) > 0 {
		usage := make(map[string]TokenUsage, len(msg.ModelUsage))
		for model, u := range msg.ModelUsage {
			if model == "" {
				continue
			}
			if u.InputTokens == 0 && u.OutputTokens == 0 && u.CacheReadInputTokens == 0 && u.CacheCreationInputTokens == 0 {
				continue
			}
			usage[model] = TokenUsage{
				InputTokens:      u.InputTokens,
				OutputTokens:     u.OutputTokens,
				CacheReadTokens:  u.CacheReadInputTokens,
				CacheWriteTokens: u.CacheCreationInputTokens,
			}
		}
		if len(usage) > 0 {
			return usage
		}
	}

	model := msg.Model
	if model == "" {
		model = fallbackModel
	}
	if msg.Usage == nil || model == "" {
		return nil
	}
	if msg.Usage.InputTokens == 0 && msg.Usage.OutputTokens == 0 && msg.Usage.CacheReadInputTokens == 0 && msg.Usage.CacheCreationInputTokens == 0 {
		return nil
	}
	return map[string]TokenUsage{
		model: {
			InputTokens:      msg.Usage.InputTokens,
			OutputTokens:     msg.Usage.OutputTokens,
			CacheReadTokens:  msg.Usage.CacheReadInputTokens,
			CacheWriteTokens: msg.Usage.CacheCreationInputTokens,
		},
	}
}

// truncateForLog returns s truncated to maxLen characters for compact logging.
func truncateForLog(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
