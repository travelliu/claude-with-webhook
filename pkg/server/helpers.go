package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"claude-with-webhook/pkg/agent"
)

const (
	planTimeout      = 30 * time.Minute
	followUpTimeout  = 30 * time.Minute
	implementTimeout = 60 * time.Minute
	polishTimeout    = 30 * time.Minute
	gitTimeout       = 30 * time.Second
	maxErrorLen      = 500
	maxDiffLen       = 10000

	spinnerImg = `<div align="center">

![](https://raw.githubusercontent.com/htlin222/claude-with-webhook/e19f046c9ae189880d65d778f2cb1305978cc52c/assests/spinner.svg)

</div>`

	planCommentTemplate = `## Claude's Plan

> Running with elevated permissions in isolated worktree

%s

---

Comment **%s** to interact:

%s`
)

var (
	// Patterns for files that should never be staged.
	dangerousFilePatterns = []string{
		".env*", "*.pem", "*.key", "*credential*", "*secret*", "*token*",
		"node_modules/", ".git/",
	}

	// Source code extensions that should not be filtered by credential/secret/token patterns.
	// Files with these extensions are legitimate source code, not secrets.
	// This prevents false positives where source files like token_service.go get filtered.
	sourceCodeExtensions = map[string]bool{
		".go": true, ".py": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true,
		".java": true, ".c": true, ".cpp": true, ".h": true, ".hpp": true,
		".rs": true, ".rb": true, ".php": true, ".swift": true, ".kt": true,
		".scala": true, ".cs": true, ".vb": true, ".fs": true,
		".html": true, ".css": true, ".scss": true, ".less": true,
		".vue": true, ".svelte": true, ".astro": true,
		".sql": true, ".graphql": true, ".gql": true,
		".sh": true, ".bash": true, ".zsh": true, ".fish": true,
		".dart": true, ".lua": true, ".r": true, ".R": true, ".jl": true,
		".ex": true, ".exs": true, ".erl": true, ".hrl": true,
		".ml": true, ".mli": true, ".hs": true, ".lhs": true,
		".clj": true, ".cljs": true, ".cljc": true,
		".elm": true, ".purs": true, ".nix": true,
		".zig": true, ".nim": true, ".cr": true, ".d": true,
		".asm": true, ".s": true, ".S": true,
		".pl": true, ".pm": true,
		".proto": true, ".thrift": true, ".avsc": true,
		".tf": true, ".hcl": true,
	}

	// Patterns for lines to redact from error output.
	secretLinePattern = regexp.MustCompile(`(?i)(token|key|secret|password|credential)`)
	absPathPattern    = regexp.MustCompile(`/Users/[^\s]+`)
)

// ghComment represents a GitHub issue/PR comment from the API.
type ghComment struct {
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

// runCmd executes a command in the given directory with a timeout.
func runCmd(dir string, timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	start := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)

	label := name
	if len(args) > 0 {
		label += " " + args[0]
	}

	if ctx.Err() == context.DeadlineExceeded {
		slog.Error("command timeout", "cmd", label, "timeout", timeout)
		return string(out), fmt.Errorf("%s %s: timed out after %s", name, strings.Join(args, " "), timeout)
	}
	if err != nil {
		slog.Error("command failed", "cmd", label, "elapsed", elapsed.Round(time.Millisecond), "error", err)
		return string(out), fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, out)
	}
	if elapsed > 5*time.Second {
		slog.Debug("command done", "cmd", label, "elapsed", elapsed.Round(time.Millisecond))
	}
	return string(out), nil
}

// runCmdWithToken executes a command with a custom GITHUB_TOKEN env var.
func runCmdWithToken(dir string, timeout time.Duration, token string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if token != "" {
		cmd.Env = append(os.Environ(), "GITHUB_TOKEN="+token)
	}
	start := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)

	label := name
	if len(args) > 0 {
		label += " " + args[0]
	}

	if ctx.Err() == context.DeadlineExceeded {
		slog.Error("command timeout", "cmd", label, "timeout", timeout)
		return string(out), fmt.Errorf("%s %s: timed out after %s", name, strings.Join(args, " "), timeout)
	}
	if err != nil {
		slog.Error("command failed", "cmd", label, "elapsed", elapsed.Round(time.Millisecond), "error", err)
		return string(out), fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, out)
	}
	if elapsed > 5*time.Second {
		slog.Debug("command done", "cmd", label, "elapsed", elapsed.Round(time.Millisecond))
	}
	return string(out), nil
}

// runCmdWithGitConfig executes a git command with custom author/committer identity.
func runCmdWithGitConfig(dir string, timeout time.Duration, gitName, gitEmail string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	env := os.Environ()
	if gitName != "" {
		env = append(env, "GIT_AUTHOR_NAME="+gitName)
		env = append(env, "GIT_COMMITTER_NAME="+gitName)
	}
	if gitEmail != "" {
		env = append(env, "GIT_AUTHOR_EMAIL="+gitEmail)
		env = append(env, "GIT_COMMITTER_EMAIL="+gitEmail)
	}
	cmd.Env = env
	start := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)

	label := name
	if len(args) > 0 {
		label += " " + args[0]
	}

	if ctx.Err() == context.DeadlineExceeded {
		slog.Error("command timeout", "cmd", label, "timeout", timeout)
		return string(out), fmt.Errorf("%s %s: timed out after %s", name, strings.Join(args, " "), timeout)
	}
	if err != nil {
		slog.Error("command failed", "cmd", label, "elapsed", elapsed.Round(time.Millisecond), "error", err)
		return string(out), fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, out)
	}
	if elapsed > 5*time.Second {
		slog.Debug("command done", "cmd", label, "elapsed", elapsed.Round(time.Millisecond))
	}
	return string(out), nil
}

// runCmdWithStdin executes a command with stdin input and optional GitHub token.
func runCmdWithStdin(dir string, timeout time.Duration, token string, stdin string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if token != "" {
		cmd.Env = append(os.Environ(), "GITHUB_TOKEN="+token)
	}
	cmd.Stdin = strings.NewReader(stdin)
	start := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)

	label := name
	if len(args) > 0 {
		label += " " + args[0]
	}

	if ctx.Err() == context.DeadlineExceeded {
		slog.Error("command timeout", "cmd", label, "timeout", timeout)
		return string(out), fmt.Errorf("%s %s: timed out after %s", name, strings.Join(args, " "), timeout)
	}
	if err != nil {
		slog.Error("command failed", "cmd", label, "elapsed", elapsed.Round(time.Millisecond), "error", err)
		return string(out), fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, out)
	}
	if elapsed > 5*time.Second {
		slog.Debug("command done", "cmd", label, "elapsed", elapsed.Round(time.Millisecond))
	}
	return string(out), nil
}

// progressBody formats the in-progress comment with SVG spinner and partial output.
func progressBody(action, partial string) string {
	header := fmt.Sprintf("🤖 %s\n\n%s", action, spinnerImg)
	if partial == "" {
		return header
	}
	return header + "\n\n" + partial
}

// botToken returns the GitHub token for the given bot, falling back to legacy config.
func (s *Server) botToken(bot *BotConfig) string {
	if bot != nil && bot.Token != "" {
		return bot.Token
	}
	return s.config.BotGitHubToken
}

// botPrefix returns the command prefix for the given bot, falling back to legacy config.
func (s *Server) botPrefix(bot *BotConfig) string {
	if bot != nil && bot.Prefix != "" {
		return bot.Prefix
	}
	return s.config.CommandPrefix
}

// botGitName returns the git author name for the given bot, falling back to legacy config.
func (s *Server) botGitName(bot *BotConfig) string {
	if bot != nil && bot.GitName != "" {
		return bot.GitName
	}
	return s.config.BotGitName
}

// botGitEmail returns the git author email for the given bot, falling back to legacy config.
func (s *Server) botGitEmail(bot *BotConfig) string {
	if bot != nil && bot.GitEmail != "" {
		return bot.GitEmail
	}
	return s.config.BotGitEmail
}

// runAgent executes a prompt via the specified agent backend.
// If bot is nil, falls back to "claude" backend with legacy config.
func (s *Server) runAgent(dir string, timeout time.Duration, prompt string, taskID string, finalOnly bool, bot *BotConfig) (*agent.Result, error) {
	agentName := "claude"
	if bot != nil && bot.Agent != "" {
		agentName = bot.Agent
	}

	backend, ok := s.agentRegistry.Get(agentName)
	if !ok {
		return nil, fmt.Errorf("agent backend %q not found", agentName)
	}

	systemPrompt := s.promptManager.LoadSystemPrompt("")
	token := s.botToken(bot)
	taskLog := slog.Default().With("task", taskID)
	opts := agent.ExecOptions{
		Cwd:          dir,
		Timeout:      timeout,
		SystemPrompt: systemPrompt,
		Logger:       taskLog,
		Env: map[string]string{
			"GITHUB_TOKEN": token,
		},
	}

	result, err := agent.RunSync(backend, context.Background(), prompt, opts)
	if err != nil {
		return nil, err
	}

	if finalOnly {
		slog.Info("agent output", "task", taskID, "output", truncateLog(result.Output, 10))
	}

	return result, nil
}

// totalCostUSD computes the total cost from token usage.
func totalCostUSD(usage map[string]agent.TokenUsage) float64 {
	var total float64
	for _, u := range usage {
		total += float64(u.InputTokens)*0.003/1000 + float64(u.OutputTokens)*0.015/1000
	}
	return total
}

// formatMetadataFooter returns a markdown footer with run metadata.
func formatMetadataFooter(r *agent.Result) string {
	secs := r.DurationMs / 1000
	cost := totalCostUSD(r.Usage)
	return fmt.Sprintf("\n\n---\n⏱️ %ds | 💰 $%.4f", secs, cost)
}

// branchExists checks if a git branch exists.
func branchExists(dir, branch string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", branch)
	cmd.Dir = dir
	return cmd.Run() == nil
}

// verifySignature checks the HMAC-SHA256 signature from GitHub.
func verifySignature(payload []byte, header, secret string) bool {
	if !strings.HasPrefix(header, "sha256=") {
		return false
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hmac.Equal(sig, mac.Sum(nil))
}

// postIssueComment posts a comment on a GitHub issue using gh CLI.
func (s *Server) postIssueComment(repo, repoDir string, num int, body string, token string) error {
	if token == "" {
		token = s.config.BotGitHubToken
	}
	_, err := runCmdWithToken(repoDir, gitTimeout, token, "gh", "issue", "comment", strconv.Itoa(num), "--repo", repo, "--body", body)
	return err
}

// postProgressComment posts a placeholder and returns update/delete functions.
func (s *Server) postProgressComment(repo, repoDir string, num int, placeholder string, token string) (update func(string), delete func()) {
	if token == "" {
		token = s.config.BotGitHubToken
	}
	out, err := runCmdWithToken(repoDir, gitTimeout, token, "gh", "api",
		fmt.Sprintf("repos/%s/issues/%d/comments", repo, num),
		"-X", "POST",
		"-f", "body="+placeholder,
		"--jq", ".id")
	if err != nil {
		slog.Error("failed to post progress comment", "repo", repo, "issue", num, "error", err)
		return func(body string) {
			s.postIssueComment(repo, repoDir, num, body, token)
		}, func() {}
	}

	commentID := strings.TrimSpace(out)
	slog.Info("progress comment created", "repo", repo, "issue", num, "comment_id", commentID)

	update = func(body string) {
		jsonBody, _ := json.Marshal(map[string]string{"body": body})
		_, err := runCmdWithStdin(repoDir, gitTimeout, token, string(jsonBody), "gh", "api",
			fmt.Sprintf("repos/%s/issues/comments/%s", repo, commentID),
			"-X", "PATCH",
			"--input", "-")
		if err != nil {
			slog.Error("failed to update comment, posting new", "repo", repo, "issue", num, "comment_id", commentID, "error", err)
			runCmdWithToken(repoDir, gitTimeout, token, "gh", "api",
				fmt.Sprintf("repos/%s/issues/comments/%s", repo, commentID),
				"-X", "DELETE")
			s.postIssueComment(repo, repoDir, num, body, token)
		}
	}

	delete = func() {
		_, err := runCmdWithToken(repoDir, gitTimeout, token, "gh", "api",
			fmt.Sprintf("repos/%s/issues/comments/%s", repo, commentID),
			"-X", "DELETE")
		if err != nil {
			slog.Error("failed to delete progress comment", "repo", repo, "issue", num, "comment_id", commentID, "error", err)
		}
	}

	return update, delete
}

// setIssueLabel sets the workflow label on an issue.
func (s *Server) setIssueLabel(repo, repoDir string, num int, label string, token string) {
	if token == "" {
		token = s.config.BotGitHubToken
	}
	workflowLabels := map[string]bool{
		"planning": true, "planned": true, "implementing": true, "review": true, "done": true,
	}

	endpoint := fmt.Sprintf("repos/%s/issues/%d/labels", repo, num)
	out, err := runCmdWithToken(repoDir, gitTimeout, token, "gh", "api", endpoint, "--jq", ".[].name")
	if err == nil {
		for _, existing := range strings.Split(strings.TrimSpace(out), "\n") {
			existing = strings.TrimSpace(existing)
			if existing != label && workflowLabels[existing] {
				rmEndpoint := fmt.Sprintf("repos/%s/issues/%d/labels/%s", repo, num, existing)
				exec.Command("gh", "api", rmEndpoint, "--method", "DELETE").Run()
			}
		}
	}

	_, err = runCmdWithToken(repoDir, gitTimeout, token, "gh", "api", endpoint, "-f", fmt.Sprintf("labels[]=%s", label))
	if err != nil {
		slog.Error("failed to set label", "repo", repo, "issue", num, "label", label, "error", err)
	}
}

// reactToIssue adds an eyes emoji reaction to an issue.
func (s *Server) reactToIssue(repo, repoDir string, num int, token string) {
	if token == "" {
		token = s.config.BotGitHubToken
	}
	endpoint := fmt.Sprintf("repos/%s/issues/%d/reactions", repo, num)
	_, err := runCmdWithToken(repoDir, gitTimeout, token, "gh", "api", endpoint, "-f", "content=eyes")
	if err != nil {
		slog.Error("failed to react to issue", "issue", num, "error", err)
	}
}

// reactToComment adds an eyes emoji reaction to a comment.
func (s *Server) reactToComment(repo, repoDir string, commentID int, token string) {
	if token == "" {
		token = s.config.BotGitHubToken
	}
	endpoint := fmt.Sprintf("repos/%s/issues/comments/%d/reactions", repo, commentID)
	_, err := runCmdWithToken(repoDir, gitTimeout, token, "gh", "api", endpoint, "-f", "content=eyes")
	if err != nil {
		slog.Error("failed to react to comment", "comment_id", commentID, "error", err)
	}
}

// commentError posts a sanitized error message on the issue.
func (s *Server) commentError(repo, repoDir string, num int, msg string, err error, token string) {
	slog.Error("issue error", "repo", repo, "issue", num, "msg", msg, "error", err)
	s.postIssueComment(repo, repoDir, num, formatError(msg, err), token)
}

// formatError creates a sanitized error message for GitHub comments.
func formatError(msg string, err error) string {
	sanitized := sanitizeError(err.Error())
	return fmt.Sprintf("**Error**: %s\n\n```\n%s\n```", msg, sanitized)
}

// sanitizeError truncates, strips secrets, and redacts paths from error output.
func sanitizeError(s string) string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if !secretLinePattern.MatchString(line) {
			lines = append(lines, line)
		}
	}
	s = strings.Join(lines, "\n")

	s = absPathPattern.ReplaceAllString(s, "<redacted-path>/")

	if len(s) > maxErrorLen {
		s = s[:maxErrorLen] + "\n... (truncated)"
	}
	return s
}

// cleanupWorktree removes a worktree and its branch.
func cleanupWorktree(repoDir, dir, branch string) {
	slog.Info("cleaning up worktree", "dir", dir)
	runCmd(repoDir, gitTimeout, "git", "worktree", "remove", "--force", dir)
	runCmd(repoDir, gitTimeout, "git", "branch", "-D", branch)
}

// filterSafeFiles parses git status --porcelain output and returns files safe to stage.
func filterSafeFiles(porcelain string) []string {
	var safe []string
	for _, line := range strings.Split(porcelain, "\n") {
		if len(line) < 4 {
			continue
		}
		file := line[3:]
		if idx := strings.Index(file, " -> "); idx != -1 {
			file = file[idx+4:]
		}
		file = strings.TrimSpace(file)

		if file == "" {
			continue
		}
		if isDangerousFile(file) {
			slog.Warn("skipping dangerous file", "file", file)
			continue
		}
		safe = append(safe, file)
	}
	return safe
}

// isDangerousFile checks if a file matches any dangerous pattern.
// Source code files with known extensions are not filtered by
// credential/secret/token patterns to avoid false positives.
func isDangerousFile(file string) bool {
	base := filepath.Base(file)
	ext := filepath.Ext(file)
	isSourceCode := sourceCodeExtensions[ext]

	for _, pattern := range dangerousFilePatterns {
		if strings.HasSuffix(pattern, "/") && strings.HasPrefix(file, pattern) {
			return true
		}

		// Skip credential/secret/token patterns for source code files.
		// These patterns are meant for actual secret files (.env, .pem, etc.),
		// not source code that implements token/secret/credential functionality.
		if isSourceCode && (strings.Contains(pattern, "credential") || strings.Contains(pattern, "secret") || strings.Contains(pattern, "token")) {
			continue
		}

		if matched, _ := filepath.Match(pattern, base); matched {
			return true
		}
		if matched, _ := filepath.Match(pattern, file); matched {
			return true
		}
	}
	return false
}

// isBotNoise returns true if a bot comment is noise rather than useful context.
func isBotNoise(body string) bool {
	if strings.Contains(body, "## Claude's Plan") {
		return false
	}
	noiseMarkers := []string{
		"🤖",
		"spinner.svg",
		"No changes were made",
		"Nothing to commit",
		"Claude implementation failed",
		"Claude retry failed",
		"Failed to ",
		"were filtered out by security policy",
	}
	for _, marker := range noiseMarkers {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

// truncateLog returns the first N lines of s for compact logging.
// Shows the beginning of the text where commands/@mentions typically appear.
func truncateLog(s string, maxLines int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) <= maxLines {
		return strings.Join(lines, " | ")
	}
	head := lines[:maxLines]
	return fmt.Sprintf("%s | ...(%d lines total)", strings.Join(head, " | "), len(lines))
}
