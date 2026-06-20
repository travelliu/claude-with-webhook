package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Backend is the interface that all agent providers must implement.
type Backend interface {
	Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error)
	Name() string
	CLIPath() (string, bool)
}

// ExecOptions configures how a prompt is executed.
type ExecOptions struct {
	Cwd             string            `json:"cwd,omitempty"`
	Model           string            `json:"model,omitempty"`
	SystemPrompt    string            `json:"systemPrompt,omitempty"`
	MaxTurns        int               `json:"maxTurns,omitempty"`
	Timeout         time.Duration     `json:"timeout,omitempty"`
	ResumeSessionID string            `json:"resumeSessionId,omitempty"`
	ExtraArgs       []string          `json:"extraArgs,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	Logger          *slog.Logger      `json:"-"` // Logger with task context attached
}

// Session holds channels for streaming messages and the final result.
type Session struct {
	Messages <-chan Message
	Result   <-chan Result
}

// Message type constants.
const (
	MessageText       = "text"
	MessageThinking   = "thinking"
	MessageToolUse    = "tool_use"
	MessageToolResult = "tool_result"
	MessageStatus     = "status"
	MessageError      = "error"
	MessageLog        = "log"
)

// Message represents a streaming message from the agent.
type Message struct {
	Type    string `json:"type"`
	Content string `json:"content,omitempty"`
	// Tool use/result fields
	Tool   string         `json:"tool,omitempty"`
	CallID string         `json:"callId,omitempty"`
	Input  map[string]any `json:"input,omitempty"`
	Output string         `json:"output,omitempty"`
	// Status fields
	Status    string `json:"status,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	// Log fields
	Level string `json:"level,omitempty"`
	// ReceivedAt captures when the AI stream produced this message.
	ReceivedAt time.Time `json:"receivedAt,omitempty"`
}

// Result is the final outcome of an execution.
type Result struct {
	Status     string                `json:"status"`
	Output     string                `json:"output"`
	Error      string                `json:"error"`
	DurationMs int64                 `json:"durationMs"`
	SessionID  string                `json:"sessionId"`
	Model      string                `json:"model,omitempty"`
	Usage      map[string]TokenUsage `json:"usage"`
}

// TokenUsage tracks token consumption per model.
type TokenUsage struct {
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
}

// trySend sends to the channel, dropping if full.
func trySend(ch chan<- Message, msg Message) {
	if msg.ReceivedAt.IsZero() {
		msg.ReceivedAt = time.Now()
	}
	select {
	case ch <- msg:
	default:
	}
}

// buildEnv merges os.Environ with extra env vars, filtering out CLAUDECODE vars.
func buildEnv(extra ...map[string]string) []string {
	base := os.Environ()
	env := make([]string, 0, len(base)+20)
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "CLAUDECODE") || strings.HasPrefix(key, "CLAUDE_CODE_") {
			continue
		}
		env = append(env, entry)
	}
	for _, v := range extra {
		for k, val := range v {
			env = append(env, k+"="+val)
		}
	}
	return env
}

// buildCmd creates an exec.Cmd with standard settings.
func buildCmd(runCtx context.Context, execPath string, args []string, opts ExecOptions) *exec.Cmd {
	cmd := exec.CommandContext(runCtx, execPath, args...)
	cmd.WaitDelay = 10 * time.Second
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	cmd.Env = buildEnv(opts.Env)
	return cmd
}

// RunSync is a convenience wrapper that calls Execute and waits for the result.
// It drains the Messages channel for logging but only returns the final Result.
func RunSync(b Backend, ctx context.Context, prompt string, opts ExecOptions) (*Result, error) {
	sess, err := b.Execute(ctx, prompt, opts)
	if err != nil {
		return nil, err
	}
	// Drain messages, log key events
	go func() {
		log := opts.Logger
		if log == nil {
			log = slog.Default()
		}
		var toolCount int32
		for msg := range sess.Messages {
			switch msg.Type {
			case MessageText:
				log.Debug("agent: text", "bytes", len(msg.Content))
			case MessageToolUse:
				toolCount++
				log.Info(fmt.Sprintf("agent: tool #%d: %s", toolCount, msg.Tool))
			case MessageToolResult:
				log.Debug("agent: tool_result", "call_id", msg.CallID)
			case MessageLog:
				log.Debug("agent: log", "level", msg.Level, "msg", msg.Content)
			}
		}
	}()
	res := <-sess.Result
	if res.Status == "failed" || res.Status == "timeout" {
		return &res, fmt.Errorf("agent %s: %s: %s", b.Name(), res.Status, res.Error)
	}
	return &res, nil
}

// stderrTail captures the last N bytes of stderr for error reporting.
type stderrTail struct {
	buf  []byte
	size int
}

func newStderrTail(size int) *stderrTail {
	return &stderrTail{size: size}
}

func (t *stderrTail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.size {
		t.buf = t.buf[len(t.buf)-t.size:]
	}
	return len(p), nil
}

func (t *stderrTail) Tail() string {
	return string(t.buf)
}
