package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
)

// Claude CLI flag constants.
const (
	claudeFlagPrompt         = "-p"
	claudeFlagOutputFormat   = "--output-format"
	claudeFlagInputFormat    = "--input-format"
	claudeFlagVerbose        = "--verbose"
	claudeFlagPermissionMode = "--permission-mode"
	claudeFlagModel          = "--model"
	claudeFlagMaxTurns       = "--max-turns"
	claudeFlagAppendSystem   = "--append-system-prompt"
	claudeFlagResume         = "--resume"
)

// ClaudeProvider implements Backend for the Claude CLI.
type ClaudeProvider struct {
	cliProvider
}

func NewClaudeProvider() *ClaudeProvider {
	return &ClaudeProvider{
		cliProvider: cliProvider{
			name:       "claude",
			envPathVar: "CLAUDE_PATH",
			cmdName:    "claude",
		},
	}
}

// Execute runs a prompt using `claude -p` with stream-json output.
// The prompt is written to stdin as a stream-json user message.
func (p *ClaudeProvider) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	execPath, err := p.resolveCLIPath()
	if err != nil {
		return nil, err
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 20 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)

	args := buildClaudeArgs(opts)
	cmd := buildCmd(runCtx, execPath, args, opts)

	slog.Debug("claude exec", "cmd", execPath, "args", args, "cwd", opts.Cwd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("claude stdout pipe: %w", err)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("claude stdin pipe: %w", err)
	}

	stderrBuf := newStderrTail(4096)
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		stdin.Close()
		cancel()
		return nil, fmt.Errorf("start claude: %w", err)
	}

	// Write prompt via stdin as stream-json
	slog.Debug("claude: writing prompt to stdin", "prompt_len", len(prompt))
	if err := writeClaudeInput(stdin, prompt); err != nil {
		stdin.Close()
		cancel()
		_ = cmd.Wait()
		return nil, fmt.Errorf("write claude input: %v (stderr: %s)", err, stderrBuf.Tail())
	}
	stdin.Close()

	msgCh := make(chan Message, 256)
	resCh := make(chan Result, 1)

	go func() {
		defer cancel()
		defer close(msgCh)
		defer close(resCh)

		startTime := time.Now()
		var output strings.Builder
		var sessionID string
		finalStatus := "completed"
		var finalError string
		usage := make(map[string]TokenUsage)

		go func() {
			<-runCtx.Done()
			_ = stdout.Close()
		}()

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			var msg claudeSDKMessage
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			msg.ReceivedAt = time.Now()

			switch msg.Type {
			case "assistant":
				handleClaudeAssistant(msg, msgCh, &output, usage)
			case "user":
				handleClaudeUser(msg, msgCh)
			case "system":
				if msg.SessionID != "" {
					sessionID = msg.SessionID
				}
				slog.Debug("claude system", "subtype", msg.Subtype, "session", sessionID)
				trySend(msgCh, Message{Type: MessageStatus, Status: "running", SessionID: sessionID, ReceivedAt: msg.ReceivedAt})
			case "result":
				sessionID = msg.SessionID
				if msg.ResultText != "" {
					output.Reset()
					output.WriteString(msg.ResultText)
				}
				if resultUsage := claudeResultUsage(msg, opts.Model); len(resultUsage) > 0 {
					usage = resultUsage
				}
				if msg.IsError {
					finalStatus = "failed"
					finalError = msg.ResultText
				}
				slog.Info("claude result", "session", sessionID, "is_error", msg.IsError, "result_len", len(msg.ResultText))
			case "log":
				if msg.Log != nil {
					slog.Debug("claude log", "level", msg.Log.Level, "msg", msg.Log.Message)
					trySend(msgCh, Message{
						Type:       MessageLog,
						Level:      msg.Log.Level,
						Content:    msg.Log.Message,
						ReceivedAt: msg.ReceivedAt,
					})
				}
			}
		}

		exitErr := cmd.Wait()
		duration := time.Since(startTime)

		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			finalStatus = "timeout"
			finalError = fmt.Sprintf("claude timed out after %s", timeout)
		} else if errors.Is(runCtx.Err(), context.Canceled) {
			finalStatus = "aborted"
			finalError = "execution cancelled"
		} else if exitErr != nil && finalStatus == "completed" {
			finalStatus = "failed"
			finalError = fmt.Sprintf("claude exited with error: %v", exitErr)
		}

		if tail := stderrBuf.Tail(); tail != "" {
			if finalError != "" {
				finalError = finalError + "\n--- stderr ---\n" + tail
			} else {
				finalError = "--- stderr ---\n" + tail
			}
		}

		slog.Info("claude finished", "status", finalStatus, "duration_ms", duration.Milliseconds(), "session", sessionID)

		resCh <- Result{
			Status:     finalStatus,
			Output:     output.String(),
			Error:      finalError,
			DurationMs: duration.Milliseconds(),
			SessionID:  sessionID,
			Usage:      usage,
		}
	}()

	return &Session{Messages: msgCh, Result: resCh}, nil
}

// buildClaudeArgs assembles CLI args for a claude invocation.
// The prompt is written to stdin as stream-json, not passed as -p argument.
func buildClaudeArgs(opts ExecOptions) []string {
	args := []string{
		claudeFlagPrompt, // -p without value: read from stdin
		claudeFlagOutputFormat, "stream-json",
		claudeFlagInputFormat, "stream-json",
		claudeFlagVerbose,
		claudeFlagPermissionMode, "bypassPermissions",
	}
	if opts.Model != "" {
		args = append(args, claudeFlagModel, opts.Model)
	}
	if opts.MaxTurns > 0 {
		args = append(args, claudeFlagMaxTurns, fmt.Sprintf("%d", opts.MaxTurns))
	}
	if opts.SystemPrompt != "" {
		args = append(args, claudeFlagAppendSystem, opts.SystemPrompt)
	}
	if opts.ResumeSessionID != "" {
		args = append(args, claudeFlagResume, opts.ResumeSessionID)
	}
	args = append(args, opts.ExtraArgs...)
	return args
}

// writeClaudeInput marshals the prompt as a Claude stream-json input payload and writes it to stdin.
func writeClaudeInput(w io.Writer, prompt string) error {
	payload := claudeInputPayload{
		Type: "user",
		Message: claudeInputMessage{
			Role: "user",
			Content: []claudeInputTextBlock{
				{Type: "text", Text: prompt},
			},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}
