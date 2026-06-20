package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"claude-with-webhook/pkg/agent"
)

// webhookPayload is the minimal JSON structure for GitHub webhook payloads.
type webhookPayload struct {
	Action string `json:"action"`
	Issue  struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		User   struct {
			Login string `json:"login"`
		} `json:"user"`
		PullRequest *struct {
			URL string `json:"url"`
		} `json:"pull_request"`
	} `json:"issue"`
	Comment struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"comment"`
	Sender struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"sender"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

// isUserAllowed checks if a user is allowed to trigger automation.
// Check order: repo-level allowed_users → global ALLOWED_USERS → GitHub collaborator permission.
func (s *Server) isUserAllowed(repo, username string) bool {
	// 1. Repo-level allowed_users from repos.yaml
	if rc, ok := s.config.GetRepoConfig(repo); ok {
		for _, u := range rc.AllowedUsers {
			if u == username {
				s.log.Info("user allowed via repo config", "repo", repo, "user", username)
				return true
			}
		}
	}

	// 2. Global ALLOWED_USERS (backward compat)
	if s.config.AllowedUsers[username] {
		return true
	}

	// 3. GitHub collaborator permission check
	repoDir, ok := s.config.GetRepo(repo)
	if !ok {
		return false
	}
	token := s.config.BotGitHubToken
	if rc, ok := s.config.GetRepoConfig(repo); ok && rc.WebhookToken != "" {
		token = rc.WebhookToken
	}
	output, err := runCmdWithToken(repoDir, gitTimeout, token, "gh", "api",
		fmt.Sprintf("repos/%s/collaborators/%s/permission", repo, username),
		"--jq", ".permission")
	if err != nil {
		s.log.Error("failed to check permission", "repo", repo, "user", username, "error", err)
		return false
	}
	perm := strings.TrimSpace(output)
	switch perm {
	case "admin", "maintain", "write":
		s.log.Info("user allowed via GitHub permission", "repo", repo, "user", username, "perm", perm)
		return true
	default:
		return false
	}
}

// handleWebhook is the main HTTP handler for webhook requests.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request, repoFromURL string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	if !verifySignature(body, r.Header.Get("X-Hub-Signature-256"), s.config.WebhookSecret) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	deliveryID := r.Header.Get("X-GitHub-Delivery")
	if deliveryID != "" {
		if _, loaded := s.deliveryCache.LoadOrStore(deliveryID, time.Now()); loaded {
			s.log.Debug("duplicate delivery, skipping", "delivery_id", deliveryID)
			w.WriteHeader(http.StatusOK)
			return
		}
	}

	event := r.Header.Get("X-GitHub-Event")
	if event != "issues" && event != "issue_comment" {
		w.WriteHeader(http.StatusOK)
		return
	}

	var payload webhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	repo := payload.Repository.FullName
	if repo != repoFromURL {
		s.log.Warn("repo mismatch", "url", repoFromURL, "payload", repo)
		http.Error(w, "repo mismatch", http.StatusBadRequest)
		return
	}
	s.log.Info("handleWebhook", "repo", repo)
	repoDir, ok := s.config.GetRepo(repo)
	if !ok {
		s.log.Warn("repo not registered", "repo", repo)
		http.Error(w, fmt.Sprintf("repo %s not registered", repo), http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)

	go func() {
		num := payload.Issue.Number
		lockKey := fmt.Sprintf("%s#%d", repo, num)

		mu, _ := s.issueMu.LoadOrStore(lockKey, &sync.Mutex{})
		mu.(*sync.Mutex).Lock()
		defer mu.(*sync.Mutex).Unlock()

		s.semaphore <- struct{}{}
		defer func() { <-s.semaphore }()

		switch event {
		case "issues":
			if payload.Action == "opened" {
				s.handleIssueOpened(repo, repoDir, num, payload)
			}
		case "issue_comment":
			if payload.Action == "created" {
				s.handleIssueComment(repo, repoDir, num, payload)
			}
		}
	}()
}

// handleIssueOpened handles newly opened issues.
func (s *Server) handleIssueOpened(repo, repoDir string, num int, p webhookPayload) {
	sender := p.Issue.User.Login
	if !s.isUserAllowed(repo, sender) {
		s.log.Info("ignoring issue from non-allowed user", "issue", num, "user", sender)
		return
	}

	s.reactToIssue(repo, repoDir, num, "")
	s.runPlan(repo, repoDir, num, p.Issue.Title, p.Issue.Body, nil)
}

// handlePlan re-triggers planning from a comment.
func (s *Server) handlePlan(repo, repoDir string, num int, p webhookPayload, bot *BotConfig) {
	s.log.Info("re-planning issue via comment", "repo", repo, "issue", num)

	token := s.botToken(bot)
	title, err := runCmdWithToken(repoDir, gitTimeout, token, "gh", "issue", "view", strconv.Itoa(num), "--repo", repo, "--json", "title,body", "--jq", ".title")
	if err != nil {
		s.commentError(repo, repoDir, num, "Failed to fetch issue details", err, token)
		return
	}
	body, err := runCmdWithToken(repoDir, gitTimeout, token, "gh", "issue", "view", strconv.Itoa(num), "--repo", repo, "--json", "body", "--jq", ".body")
	if err != nil {
		s.commentError(repo, repoDir, num, "Failed to fetch issue details", err, token)
		return
	}

	s.runPlan(repo, repoDir, num, strings.TrimSpace(title), strings.TrimSpace(body), bot)
}

// runPlan generates a Claude plan for an issue and posts it as a comment.
func (s *Server) runPlan(repo, repoDir string, num int, title, issueBody string, bot *BotConfig) {
	s.log.Info("planning for issue", "repo", repo, "issue", num, "title", title)
	token := s.botToken(bot)
	s.setIssueLabel(repo, repoDir, num, "planning", token)

	updateComment, _ := s.postProgressComment(repo, repoDir, num, fmt.Sprintf("🤖 Planning…\n\n%s", spinnerImg), token)

	prompt := fmt.Sprintf("## Task: Plan Implementation\n\nAnalyze the GitHub issue below and produce a clear, actionable implementation plan. Include:\n- Files to create or modify\n- Key changes in each file\n- Edge cases to handle\n- Testing approach\n\nDo NOT implement — only plan.\n\n### Issue Title\n%s\n\n### Issue Body\n%s", title, issueBody)
	taskID := fmt.Sprintf("%s#%d", repo, num)
	s.log.Info("agent started", "task", taskID, "action", "planning")
	result, err := s.runAgent(repoDir, planTimeout, prompt, taskID, false, bot)
	if err != nil {
		updateComment(formatError("Failed to generate plan", err))
		return
	}

	planText := result.Output
	if idx := strings.Index(planText, "## "); idx > 0 {
		planText = planText[idx:]
	}

	prefix := s.botPrefix(bot)
	examples := fmt.Sprintf(`
%s approve
%s approve --auto-merge
%s approve --polish
%s approve [extra guidance]
%s plan (re-generate this plan)
%s <follow-up question>

**Flags:**
- --auto-merge: Enable auto-merge after PR creation
- --polish: Run code review and refinement before creating PR

**Examples:**
- %s approve focus on error handling
- %s approve add tests for edge cases
- %s approve use TypeScript strict mode
`, prefix, prefix, prefix, prefix, prefix, prefix, prefix, prefix, prefix)
	commentBody := fmt.Sprintf(planCommentTemplate, planText, prefix, examples+formatMetadataFooter(result))
	updateComment(commentBody)
	s.setIssueLabel(repo, repoDir, num, "planned", token)
}

// matchBot finds the bot whose prefix matches the comment body.
// Only matches on explicit @mention prefix. No @mention = no match.
// Returns the matched bot config and the parsed action, or ("", nil) if no match.
func (s *Server) matchBot(repo, sender, body string) (string, *BotConfig) {
	trimmed := strings.TrimSpace(body)
	firstLine := strings.ToLower(strings.SplitN(trimmed, "\n", 2)[0])
	firstLine = strings.TrimSpace(firstLine)

	for i := range s.bots {
		bot := &s.bots[i]
		prefix := strings.ToLower(bot.Prefix)
		if strings.HasPrefix(firstLine, prefix) {
			s.log.Info("bot matched by @mention prefix", "repo", repo, "bot", bot.Name, "prefix", bot.Prefix, "sender", sender)
			return "", bot
		}
	}

	return "skip-no-prefix", nil
}

// classifyComment determines what action to take on a comment.
func (s *Server) classifyComment(repo, sender, senderType, body string) string {
	if senderType == "Bot" {
		return "skip-bot"
	}

	// Check against all bots for self-comments
	for _, bot := range s.bots {
		if bot.Username != "" && strings.EqualFold(sender, bot.Username) {
			return "skip-self"
		}
	}

	if !s.isUserAllowed(repo, sender) {
		s.log.Warn("user not allowed", "repo", repo, "user", sender, "reason", "not in allowed_users and not a collaborator")
		return "skip-user"
	}

	trimmed := strings.TrimSpace(body)
	firstLine := strings.ToLower(strings.SplitN(trimmed, "\n", 2)[0])
	firstLine = strings.TrimSpace(firstLine)

	// Check against all bot prefixes
	for _, bot := range s.bots {
		prefix := strings.ToLower(bot.Prefix)
		if strings.HasPrefix(firstLine, prefix) {
			cmd := strings.TrimSpace(strings.TrimPrefix(firstLine, prefix))
			return classifyCommand(cmd)
		}
	}

	return "skip-no-prefix"
}

// classifyCommand parses the command action from the trimmed command string.
func classifyCommand(cmd string) string {
	switch {
	case cmd == "approve" || cmd == "approved" || cmd == "lgtm":
		return "approve"
	case strings.HasPrefix(cmd, "approve ") || strings.HasPrefix(cmd, "approved "):
		return "approve"
	case cmd == "plan":
		return "plan"
	case cmd == "":
		return "skip-bare-mention"
	default:
		return "followup"
	}
}

// handleIssueComment handles new issue comments.
func (s *Server) handleIssueComment(repo, repoDir string, num int, p webhookPayload) {
	sender := p.Comment.User.Login
	s.log.Info("comment received", "repo", repo, "issue", num, "user", sender, "type", p.Sender.Type, "body", truncateLog(p.Comment.Body, 10))

	if p.Sender.Type == "Bot" {
		s.log.Debug("skipping bot-type sender", "repo", repo, "issue", num, "user", sender)
		return
	}

	// Find which bot this comment is addressed to
	action, bot := s.matchBot(repo, sender, p.Comment.Body)
	if bot == nil {
		s.log.Info("no bot matched, ignoring comment", "repo", repo, "issue", num, "user", sender, "body", truncateLog(p.Comment.Body, 2))
		return
	}

	// Skip bot's own comments (self-comment detection)
	if bot.Username != "" && strings.EqualFold(sender, bot.Username) {
		s.log.Debug("skipping bot's own comment", "repo", repo, "issue", num, "user", sender, "bot", bot.Name)
		return
	}

	// Check user permissions
	if !s.isUserAllowed(repo, sender) {
		s.log.Warn("user not allowed to trigger bot, skipping",
			"repo", repo, "issue", num, "user", sender, "bot", bot.Name,
			"hint", "add user to repos.yaml allowed_users or ensure they are a repo collaborator")
		return
	}

	s.log.Info("user authorized, processing comment", "repo", repo, "issue", num, "user", sender, "bot", bot.Name)

	// Parse the command action
	body := strings.TrimSpace(p.Comment.Body)
	prefix := strings.ToLower(bot.Prefix)
	firstLine := strings.ToLower(strings.SplitN(body, "\n", 2)[0])
	cmd := strings.TrimSpace(strings.TrimPrefix(firstLine, prefix))
	action = classifyCommand(cmd)

	if action == "skip-bare-mention" {
		s.log.Debug("ignoring bare mention", "repo", repo, "issue", num, "prefix", bot.Prefix)
		return
	}

	s.log.Info("comment routed", "repo", repo, "issue", num, "bot", bot.Name, "action", action)

	s.reactToComment(repo, repoDir, p.Comment.ID, s.botToken(bot))

	extra := ""
	autoMerge := false
	polish := false
	if action == "approve" {
		if cmd == "approve" || cmd == "approved" || cmd == "lgtm" {
			if idx := strings.Index(body, "\n"); idx != -1 {
				extra = strings.TrimSpace(body[idx+1:])
			}
		} else {
			extra = strings.TrimSpace(cmd[strings.Index(cmd, " ")+1:])
		}
		if strings.Contains(extra, "--auto-merge") {
			autoMerge = true
			extra = strings.TrimSpace(strings.ReplaceAll(extra, "--auto-merge", ""))
		}
		if strings.Contains(cmd, "--auto-merge") {
			autoMerge = true
		}
		if strings.Contains(extra, "--polish") {
			polish = true
			extra = strings.TrimSpace(strings.ReplaceAll(extra, "--polish", ""))
		}
		if strings.Contains(cmd, "--polish") {
			polish = true
		}
		if extra != "" {
			s.log.Info("approve with extra guidance", "repo", repo, "issue", num, "guidance", truncateLog(extra, 3))
		}
		if autoMerge {
			s.log.Info("auto-merge requested", "repo", repo, "issue", num)
		}
		if polish {
			s.log.Info("polish requested", "repo", repo, "issue", num)
		}
	}

	if p.Issue.PullRequest != nil {
		switch action {
		case "approve":
			s.handlePRComment(repo, repoDir, num, p, extra, bot)
		case "plan":
			s.handlePRComment(repo, repoDir, num, p, "", bot)
		case "followup":
			s.handlePRComment(repo, repoDir, num, p, "", bot)
		}
		return
	}

	switch action {
	case "approve":
		s.handleApprove(repo, repoDir, num, p, extra, autoMerge, polish, bot)
	case "plan":
		s.handlePlan(repo, repoDir, num, p, bot)
	case "followup":
		s.handleFollowUp(repo, repoDir, num, p, bot)
	}
}

// fetchDiscussion fetches an issue or PR body + comments, filtering out bot noise.
func (s *Server) fetchDiscussion(repoDir, repo string, num int, kind string, bot *BotConfig) (string, error) {
	numStr := strconv.Itoa(num)
	token := s.botToken(bot)

	var titleBody string
	var err error
	if kind == "pr" {
		titleBody, err = runCmdWithToken(repoDir, gitTimeout, token, "gh", "pr", "view", numStr,
			"--repo", repo, "--json", "title,body",
			"--jq", `"# " + .title + "\n\n" + (.body // "")`)
	} else {
		titleBody, err = runCmdWithToken(repoDir, gitTimeout, token, "gh", "issue", "view", numStr,
			"--repo", repo, "--json", "title,body",
			"--jq", `"# " + .title + "\n\n" + (.body // "")`)
	}
	if err != nil {
		return "", fmt.Errorf("fetch %s title/body: %w", kind, err)
	}

	commentsRaw, err := runCmdWithToken(repoDir, gitTimeout, token, "gh", "api",
		fmt.Sprintf("repos/%s/issues/%d/comments", repo, num),
		"--paginate")
	if err != nil {
		s.log.Warn("fetchDiscussion API fallback", "repo", repo, "issue", num, "error", err)
		if kind == "pr" {
			return runCmdWithToken(repoDir, gitTimeout, token, "gh", "pr", "view", numStr, "--repo", repo, "--comments")
		}
		return runCmdWithToken(repoDir, gitTimeout, token, "gh", "issue", "view", numStr, "--repo", repo, "--comments")
	}

	var comments []ghComment
	if err := json.Unmarshal([]byte(commentsRaw), &comments); err != nil {
		s.log.Warn("fetchDiscussion JSON parse fallback", "repo", repo, "issue", num, "error", err)
		if kind == "pr" {
			return runCmdWithToken(repoDir, gitTimeout, token, "gh", "pr", "view", numStr, "--repo", repo, "--comments")
		}
		return runCmdWithToken(repoDir, gitTimeout, token, "gh", "issue", "view", numStr, "--repo", repo, "--comments")
	}

	// Determine bot username for noise filtering
	botUsername := s.config.BotUsername
	if bot != nil && bot.Username != "" {
		botUsername = bot.Username
	}

	var filtered []string
	for _, c := range comments {
		if botUsername != "" && strings.EqualFold(c.User.Login, botUsername) && isBotNoise(c.Body) {
			continue
		}
		filtered = append(filtered, fmt.Sprintf("### Comment by %s (%s)\n\n%s", c.User.Login, c.CreatedAt, c.Body))
	}

	result := strings.TrimSpace(titleBody)
	if len(filtered) > 0 {
		result += "\n\n---\n\n## Comments\n\n" + strings.Join(filtered, "\n\n---\n\n")
	}
	return result, nil
}

// handleFollowUp handles follow-up questions on issues.
func (s *Server) handleFollowUp(repo, repoDir string, num int, p webhookPayload, bot *BotConfig) {
	s.log.Info("follow-up on issue", "repo", repo, "issue", num)

	token := s.botToken(bot)
	updateComment, _ := s.postProgressComment(repo, repoDir, num, fmt.Sprintf("🤖 Thinking…\n\n%s", spinnerImg), token)

	discussion, err := s.fetchDiscussion(repoDir, repo, num, "issue", bot)
	if err != nil {
		updateComment(formatError("Failed to read issue discussion", err))
		return
	}

	prompt := fmt.Sprintf("## Task: Respond to Follow-Up\n\nRead the full GitHub issue discussion below (original issue + all comments). The latest comment is a follow-up question or request directed at you.\n\nRespond concisely and helpfully. If the question asks about code, reference specific files and line numbers. If it asks for changes, explain what you would do.\n\n### Discussion\n%s", discussion)
	taskID := fmt.Sprintf("%s#%d", repo, num)
	s.log.Info("agent started", "task", taskID, "action", "follow-up")
	result, err := s.runAgent(repoDir, followUpTimeout, prompt, taskID, true, bot)
	if err != nil {
		updateComment(formatError("Claude follow-up failed", err))
		return
	}

	updateComment(result.Output + formatMetadataFooter(result))
}

// retryIfNoChanges checks git status and retries the agent once if no changes were made.
func (s *Server) retryIfNoChanges(repo string, num int, worktreeDir, prompt string, firstResult *agent.Result, onUpdate func(string), bot *BotConfig) (string, error) {
	status, err := runCmd(worktreeDir, gitTimeout, "git", "status", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("git status: %w", err)
	}
	if strings.TrimSpace(status) != "" {
		return status, nil
	}

	s.log.Warn("no changes after first attempt, retrying", "repo", repo, "issue", num)

	var retryPrompt strings.Builder
	retryPrompt.WriteString("## CRITICAL: Your previous attempt produced ZERO file changes.\n\n")
	retryPrompt.WriteString("### What went wrong\n")
	retryPrompt.WriteString("You likely described changes in text instead of using Edit/Write tools to modify files on disk.\n\n")
	retryPrompt.WriteString("### What you must do NOW\n")
	retryPrompt.WriteString("1. Use the Edit tool to modify existing files, or Write tool to create new files.\n")
	retryPrompt.WriteString("2. Do NOT explain or describe — just make the changes.\n")
	retryPrompt.WriteString("3. After editing, run `git diff` to confirm your changes are on disk.\n\n")

	if firstResult != nil && firstResult.Output != "" {
		text := firstResult.Output
		if len(text) > 2000 {
			text = text[:2000] + "\n...(truncated)"
		}
		retryPrompt.WriteString("### Your previous analysis (reuse this — do NOT re-analyze)\n```\n")
		retryPrompt.WriteString(text)
		retryPrompt.WriteString("\n```\n\n")
	}

	retryPrompt.WriteString("### Original task\n")
	retryPrompt.WriteString(prompt)

	onUpdate(progressBody("Retrying (no changes detected)", ""))
	retryResult, err := s.runAgent(worktreeDir, implementTimeout, retryPrompt.String(), fmt.Sprintf("%s#%d", repo, num), false, bot)
	if err != nil {
		return "", fmt.Errorf("claude retry: %w", err)
	}
	_ = retryResult

	status, err = runCmd(worktreeDir, gitTimeout, "git", "status", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("git status after retry: %w", err)
	}
	return status, nil
}

// handleApprove handles approve commands on issues.
func (s *Server) handleApprove(repo, repoDir string, num int, p webhookPayload, extraGuidance string, autoMerge bool, polish bool, bot *BotConfig) {
	s.log.Info("implementing issue", "repo", repo, "issue", num)

	branch := fmt.Sprintf("issue-%d", num)
	worktreeDir := filepath.Join(repoDir, "worktrees", branch)

	if branchExists(repoDir, branch) {
		s.log.Info("branch already exists, skipping duplicate approve", "branch", branch)
		return
	}

	token := s.botToken(bot)
	s.setIssueLabel(repo, repoDir, num, "implementing", token)
	updateComment, deleteSpinner := s.postProgressComment(repo, repoDir, num, fmt.Sprintf("🤖 Implementing…\n\n%s", spinnerImg), token)

	if _, err := runCmd(repoDir, gitTimeout, "git", "fetch", "origin", "main"); err != nil {
		updateComment(formatError("Failed to fetch origin/main", err))
		return
	}

	if _, err := runCmd(repoDir, gitTimeout, "git", "worktree", "add", worktreeDir, "-b", branch, "origin/main"); err != nil {
		updateComment(formatError("Failed to create worktree", err))
		return
	}

	success := false
	defer func() {
		if !success {
			cleanupWorktree(repoDir, worktreeDir, branch)
		}
	}()

	discussion, err := s.fetchDiscussion(repoDir, repo, num, "issue", bot)
	if err != nil {
		updateComment(formatError("Failed to read issue discussion", err))
		return
	}

	prompt := fmt.Sprintf("## Task: Implement GitHub Issue\n\nRead the full discussion below carefully (issue + all comments), then implement ALL necessary code changes.\n\nRequirements:\n- Read existing code before modifying it\n- Follow the project's existing code style and conventions\n- Handle edge cases mentioned in the discussion\n- Make the minimal set of changes needed to fully resolve the issue\n- Ensure the code compiles/runs correctly\n\n### Discussion\n%s", discussion)
	if extraGuidance != "" {
		prompt += fmt.Sprintf("\n\n## Additional Guidance from Approver (HIGH PRIORITY)\n\nThe following instruction takes priority over general discussion. Follow it precisely:\n\n%s", extraGuidance)
	}
	taskID := fmt.Sprintf("%s#%d", repo, num)
	s.log.Info("agent started", "task", taskID, "action", "implementing")
	result, err := s.runAgent(worktreeDir, implementTimeout, prompt, taskID, false, bot)
	if err != nil {
		updateComment(formatError("Claude implementation failed", err))
		return
	}

	status, err := s.retryIfNoChanges(repo, num, worktreeDir, prompt, result, func(str string) { updateComment(str) }, bot)
	if err != nil {
		updateComment(formatError("Implementation failed", err))
		return
	}
	if strings.TrimSpace(status) == "" {
		updateComment("No changes were made by Claude after 2 attempts. Nothing to commit.")
		return
	}

	if polish {
		s.runPolish(repo, num, worktreeDir, func(str string) { updateComment(str) }, bot)
	}

	title := p.Issue.Title
	commitMsg := fmt.Sprintf("Implement #%d: %s", num, title)

	filesToAdd := filterSafeFiles(status)
	if len(filesToAdd) == 0 {
		updateComment("All changed files were filtered out by security policy. Nothing to commit.")
		return
	}
	addArgs := append([]string{"add", "--"}, filesToAdd...)
	if _, err := runCmd(worktreeDir, gitTimeout, "git", addArgs...); err != nil {
		updateComment(formatError("Failed to stage changes", err))
		return
	}
	if _, err := runCmdWithGitConfig(worktreeDir, gitTimeout, s.botGitName(bot), s.botGitEmail(bot), "git", "commit", "-m", commitMsg); err != nil {
		updateComment(formatError("Failed to commit", err))
		return
	}
	if _, err := runCmd(worktreeDir, gitTimeout, "git", "push", "-u", "origin", branch); err != nil {
		updateComment(formatError("Failed to push branch", err))
		return
	}

	prTitle := fmt.Sprintf("Fix #%d: %s", num, title)
	prBody := fmt.Sprintf("Closes #%d\n\nImplemented automatically by Claude.", num)
	prURL, err := runCmdWithToken(worktreeDir, gitTimeout, token, "gh", "pr", "create", "--title", prTitle, "--body", prBody, "--repo", repo)
	if err != nil {
		updateComment(formatError("Failed to create PR", err))
		return
	}

	prURL = strings.TrimSpace(prURL)
	deleteSpinner()
	s.setIssueLabel(repo, repoDir, num, "review", token)

	if autoMerge {
		if _, err := runCmdWithToken(worktreeDir, gitTimeout, token, "gh", "pr", "merge", "--squash", "--repo", repo, branch); err == nil {
			s.log.Info("PR merged directly", "repo", repo, "issue", num, "pr_url", prURL)
			s.setIssueLabel(repo, repoDir, num, "done", token)
			s.postIssueComment(repo, repoDir, num, fmt.Sprintf("PR created and merged: %s", prURL), token)
		} else if _, err := runCmdWithToken(worktreeDir, gitTimeout, token, "gh", "pr", "merge", "--auto", "--squash", "--repo", repo, branch); err == nil {
			s.log.Info("auto-merge enabled", "repo", repo, "issue", num, "pr_url", prURL)
			s.postIssueComment(repo, repoDir, num, fmt.Sprintf("PR created: %s\n\n✅ Auto-merge enabled (will merge when CI passes)", prURL), token)
		} else {
			s.log.Error("auto-merge failed", "repo", repo, "issue", num, "error", err)
			s.postIssueComment(repo, repoDir, num, fmt.Sprintf("PR created: %s\n\n⚠️ Auto-merge failed — please merge manually", prURL), token)
		}
	} else {
		s.postIssueComment(repo, repoDir, num, fmt.Sprintf("PR created: %s", prURL), token)
	}
	success = true

	s.log.Info("PR created", "repo", repo, "issue", num, "pr_url", prURL)
}

// runPolish runs the two-agent review-refine loop on the current diff.
func (s *Server) runPolish(repo string, num int, worktreeDir string, onUpdate func(string), bot *BotConfig) {
	s.log.Info("starting polish: review phase", "repo", repo, "issue", num)

	reviewText, err := s.runReview(repo, num, worktreeDir, onUpdate, bot)
	if err != nil {
		s.log.Warn("polish review failed (non-fatal)", "repo", repo, "issue", num, "error", err)
		return
	}

	if isLGTM(reviewText) {
		s.log.Info("polish review: LGTM, skipping refine", "repo", repo, "issue", num)
		return
	}

	s.log.Info("starting polish: refine phase", "repo", repo, "issue", num)
	if err := s.runRefine(repo, num, worktreeDir, reviewText, onUpdate, bot); err != nil {
		s.log.Warn("polish refine failed (non-fatal)", "repo", repo, "issue", num, "error", err)
	}
}

// runReview runs an agent call that reviews the current git diff.
func (s *Server) runReview(repo string, num int, worktreeDir string, onUpdate func(string), bot *BotConfig) (string, error) {
	diff, err := runCmd(worktreeDir, gitTimeout, "git", "diff", "HEAD")
	if err != nil {
		diff, err = runCmd(worktreeDir, gitTimeout, "git", "diff")
		if err != nil {
			return "", fmt.Errorf("git diff: %w", err)
		}
	}

	diff = strings.TrimSpace(diff)
	if diff == "" {
		diff, _ = runCmd(worktreeDir, gitTimeout, "git", "diff", "--cached")
		diff = strings.TrimSpace(diff)
	}
	if diff == "" {
		return "LGTM", nil
	}

	if len(diff) > maxDiffLen {
		diff = diff[:maxDiffLen] + "\n... (truncated)"
	}

	prompt := fmt.Sprintf(`## Task: Code Review (Review Only — Do NOT Modify Files)

You are a senior code reviewer (Gunshi/strategist). Review the following git diff for:
- Bugs, logic errors, or edge cases
- Security issues
- Style inconsistencies with the surrounding codebase
- Missing error handling
- Performance concerns

### Rules
- Do NOT use Edit, Write, or any file-modifying tools. You are a REVIEWER only.
- Output your review as plain text.
- If the code looks good and you have no substantive feedback, respond with exactly: LGTM
- Be concise — focus on actionable issues, not nitpicks.

### Git Diff
%s`, "```diff\n"+diff+"\n```")

	taskID := fmt.Sprintf("%s#%d", repo, num)
	onUpdate(progressBody("Polishing (reviewing)", ""))
	result, err := s.runAgent(worktreeDir, polishTimeout, prompt, taskID, true, bot)
	if err != nil {
		return "", fmt.Errorf("agent review: %w", err)
	}

	s.log.Info("polish review complete", "repo", repo, "issue", num, "cost", totalCostUSD(result.Usage))
	return result.Output, nil
}

// runRefine runs an agent call that applies the review findings as code fixes.
func (s *Server) runRefine(repo string, num int, worktreeDir string, reviewText string, onUpdate func(string), bot *BotConfig) error {
	if len(reviewText) > 5000 {
		reviewText = reviewText[:5000] + "\n... (truncated)"
	}

	prompt := fmt.Sprintf(`## Task: Apply Code Review Feedback

A senior reviewer has examined your implementation and found issues. Apply their feedback by making the necessary code changes.

### Rules
- Use Edit tool to fix the issues identified below.
- Only fix what the review calls out — do not make unrelated changes.
- After fixing, run "git diff" to verify your changes are on disk.

### Review Feedback
%s`, reviewText)

	taskID := fmt.Sprintf("%s#%d", repo, num)
	onUpdate(progressBody("Polishing (refining)", ""))
	result, err := s.runAgent(worktreeDir, polishTimeout, prompt, taskID, true, bot)
	if err != nil {
		return fmt.Errorf("agent refine: %w", err)
	}

	s.log.Info("polish refine complete", "repo", repo, "issue", num, "cost", totalCostUSD(result.Usage))
	return nil
}

// isLGTM returns true if the review text indicates no issues were found.
func isLGTM(reviewText string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(reviewText))
	if trimmed == "lgtm" || trimmed == "lgtm." || trimmed == "lgtm!" {
		return true
	}
	if strings.HasPrefix(trimmed, "lgtm") && len(trimmed) < 100 {
		return true
	}
	return false
}

// handlePRComment handles comments on pull requests.
func (s *Server) handlePRComment(repo, repoDir string, num int, p webhookPayload, extraGuidance string, bot *BotConfig) {
	s.log.Info("handling PR comment", "repo", repo, "issue", num)

	token := s.botToken(bot)
	updateComment, deleteSpinner := s.postProgressComment(repo, repoDir, num, fmt.Sprintf("🤖 Implementing…\n\n%s", spinnerImg), token)
	branch, err := runCmdWithToken(repoDir, gitTimeout, token, "gh", "pr", "view", strconv.Itoa(num),
		"--repo", repo, "--json", "headRefName", "--jq", ".headRefName")
	if err != nil {
		updateComment(formatError("Failed to get PR branch name", err))
		return
	}
	branch = strings.TrimSpace(branch)

	worktreeDir := filepath.Join(repoDir, "worktrees", fmt.Sprintf("pr-%d", num))

	if _, err := runCmd(repoDir, gitTimeout, "git", "fetch", "origin", branch); err != nil {
		updateComment(formatError("Failed to fetch PR branch", err))
		return
	}
	if _, err := runCmd(repoDir, gitTimeout, "git", "worktree", "add", worktreeDir, "origin/"+branch); err != nil {
		updateComment(formatError("Failed to create worktree for PR branch", err))
		return
	}
	if _, err := runCmd(worktreeDir, gitTimeout, "git", "checkout", "-B", branch, "origin/"+branch); err != nil {
		updateComment(formatError("Failed to checkout PR branch", err))
		cleanupWorktree(repoDir, worktreeDir, "")
		return
	}

	defer func() {
		s.log.Info("cleaning up PR worktree", "dir", worktreeDir)
		runCmd(repoDir, gitTimeout, "git", "worktree", "remove", "--force", worktreeDir)
	}()

	discussion, err := s.fetchDiscussion(repoDir, repo, num, "pr", bot)
	if err != nil {
		updateComment(formatError("Failed to read PR discussion", err))
		return
	}

	prompt := fmt.Sprintf("## Task: Implement PR Changes\n\nRead the full PR discussion below (description + all comments). The latest comment is a request directed at you.\n\nRequirements:\n- Read existing code before modifying it\n- Follow the project's existing code style and conventions\n- Make only the changes requested in the latest comment\n- Ensure the code compiles/runs correctly\n\n### PR Discussion\n%s", discussion)
	if extraGuidance != "" {
		prompt += fmt.Sprintf("\n\n## Additional Guidance (HIGH PRIORITY)\n\nThe following instruction takes priority. Follow it precisely:\n\n%s", extraGuidance)
	}

	taskID := fmt.Sprintf("%s#%d", repo, num)
	s.log.Info("agent started", "task", taskID, "action", "pr-implementation")
	result, err := s.runAgent(worktreeDir, implementTimeout, prompt, taskID, false, bot)
	if err != nil {
		updateComment(formatError("Claude implementation failed", err))
		return
	}

	status, err := s.retryIfNoChanges(repo, num, worktreeDir, prompt, result, func(str string) { updateComment(str) }, bot)
	if err != nil {
		updateComment(formatError("Implementation failed", err))
		return
	}
	if strings.TrimSpace(status) == "" {
		updateComment("No changes were made by Claude after 2 attempts. Nothing to commit.")
		return
	}

	commitMsg := fmt.Sprintf("PR #%d: implement requested changes", num)

	filesToAdd := filterSafeFiles(status)
	if len(filesToAdd) == 0 {
		updateComment("All changed files were filtered out by security policy. Nothing to commit.")
		return
	}
	addArgs := append([]string{"add", "--"}, filesToAdd...)
	if _, err := runCmd(worktreeDir, gitTimeout, "git", addArgs...); err != nil {
		updateComment(formatError("Failed to stage changes", err))
		return
	}
	if _, err := runCmdWithGitConfig(worktreeDir, gitTimeout, s.botGitName(bot), s.botGitEmail(bot), "git", "commit", "-m", commitMsg); err != nil {
		updateComment(formatError("Failed to commit", err))
		return
	}
	if _, err := runCmd(worktreeDir, gitTimeout, "git", "push", "origin", branch); err != nil {
		updateComment(formatError("Failed to push changes", err))
		return
	}

	deleteSpinner()
	s.postIssueComment(repo, repoDir, num, fmt.Sprintf("Changes pushed to `%s`.", branch), token)
	s.log.Info("pushed PR changes", "repo", repo, "issue", num, "branch", branch)
}
