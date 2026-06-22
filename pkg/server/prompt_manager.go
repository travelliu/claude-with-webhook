package server

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// PromptManager loads and renders prompt templates with a fallback chain:
// repo-specific → global → built-in.
type PromptManager struct {
	baseDir string // ~/.claude-webhook
}

// NewPromptManager creates a PromptManager for the given base directory.
func NewPromptManager(baseDir string) *PromptManager {
	return &PromptManager{baseDir: baseDir}
}

// LoadTaskPrompt loads a task prompt template, renders it with the given data,
// and returns the result. Fallback chain:
//
//	{baseDir}/prompts/{repo}/{action}.tmpl → {baseDir}/prompts/{action}.tmpl → built-in
func (pm *PromptManager) LoadTaskPrompt(repo, action string, data any) (string, error) {
	tmplStr, source := pm.loadTemplate(repo, action+".tmpl")
	if source == "file" {
		slog.Info("loaded task prompt", "repo", repo, "action", action, "source", "file")
	}

	tmpl, err := template.New(action).Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parse prompt template %s: %w", action, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute prompt template %s: %w", action, err)
	}
	return buf.String(), nil
}

// LoadSystemPrompt loads the system prompt (plain text, no template rendering).
// Fallback chain:
//
//	{baseDir}/prompts/{repo}/system.md → {baseDir}/prompts/system.md → built-in
func (pm *PromptManager) LoadSystemPrompt(repo string) string {
	content, _ := pm.loadTemplate(repo, "system.md")
	return content
}

// loadTemplate reads a template file from the prompts directory with fallback.
// Returns the content and the source ("file" or "builtin").
func (pm *PromptManager) loadTemplate(repo, filename string) (string, string) {
	promptsDir := filepath.Join(pm.baseDir, "prompts")

	// Try repo-specific
	if repo != "" {
		if content := readFile(filepath.Join(promptsDir, repo, filename)); content != "" {
			return content, "file"
		}
	}

	// Try global
	if content := readFile(filepath.Join(promptsDir, filename)); content != "" {
		return content, "file"
	}

	// Fallback to built-in
	if content, ok := builtinTemplates[filename]; ok {
		return content, "builtin"
	}
	if filename == "system.md" {
		return builtinSystemPrompt, "builtin"
	}
	return "", "builtin"
}

// readFile reads a file and returns trimmed content, or empty string on error.
func readFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return ""
	}
	return content
}

// EnsureDefaultPrompts copies default templates to a repo-specific directory.
// If no global templates exist, writes built-in defaults to both global and repo dirs.
func (pm *PromptManager) EnsureDefaultPrompts(repo string) error {
	promptsDir := filepath.Join(pm.baseDir, "prompts")
	repoDir := filepath.Join(promptsDir, repo)

	// Create repo dir
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		return fmt.Errorf("create prompts dir: %w", err)
	}

	// Check if global templates exist
	globalHasTemplates := false
	entries, err := os.ReadDir(promptsDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() && (strings.HasSuffix(e.Name(), ".tmpl") || e.Name() == "system.md") {
				globalHasTemplates = true
				break
			}
		}
	}

	if globalHasTemplates {
		// Copy global templates to repo dir (only if not already present)
		return copyTemplates(promptsDir, repoDir)
	}

	// No global templates — write built-in defaults to both global and repo
	if err := writeBuiltinTemplates(promptsDir); err != nil {
		slog.Warn("failed to write global default prompts", "error", err)
	}
	return writeBuiltinTemplates(repoDir)
}

// writeBuiltinTemplates writes all built-in templates to a directory.
func writeBuiltinTemplates(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, content := range builtinTemplates {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}

// copyTemplates copies *.tmpl and system.md from src to dst (skip existing).
func copyTemplates(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".tmpl") && name != "system.md" {
			continue
		}
		dstPath := filepath.Join(dst, name)
		if _, err := os.Stat(dstPath); err == nil {
			continue // already exists
		}
		data, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			continue
		}
		if err := os.WriteFile(dstPath, data, 0o644); err != nil {
			return fmt.Errorf("copy %s: %w", name, err)
		}
	}
	return nil
}

// --- Built-in templates ---

const builtinSystemPrompt = `## System Instructions (NON-INTERACTIVE MODE)

You are running in a fully non-interactive CI/CD pipeline via "claude -p --dangerously-skip-permissions".
There is NO human to answer questions. You MUST follow these rules:

### Critical: You MUST make actual file changes
- Use the Edit tool or Write tool to ACTUALLY MODIFY FILES on disk.
- Do NOT just describe or explain what changes should be made — MAKE the changes.
- If the task requires code changes, there MUST be modified files when you are done.

### Workflow: Read → Modify → Verify
Follow this exact workflow for every implementation task:
1. READ: Use Read, Glob, and Grep tools to understand the codebase structure and relevant files.
2. MODIFY: Use Edit tool (for existing files) or Write tool (for new files) to make all changes.
3. VERIFY: Run "git diff" via the Bash tool to confirm your changes are on disk. If git diff shows NO output, your edits were NOT saved — you must try again.

### Behavioral rules
1. NEVER ask clarifying questions — make your best judgment and proceed.
2. NEVER pause or wait for input — complete the task fully in one pass.
3. NEVER suggest manual steps — do everything yourself.
4. If something is ambiguous, choose the most reasonable interpretation and proceed.
5. If you encounter an error, try to fix it yourself. If you truly cannot proceed, explain why clearly.
6. Keep your final text response concise and focused on what you did.

### Git rules (the caller handles git operations)
7. You are working inside an isolated git worktree — freely create and modify files.
8. Do NOT run "git commit", "git push", or create PRs — the caller handles all git operations after you finish.
9. Do NOT run "git add" — just edit the files, the caller stages and commits them.

### Quality
10. Read existing code before modifying it — understand context first.
11. Write clean, production-quality code following existing project conventions.

### Planning tasks: Output a structured summary
When the task is to PLAN (not implement), end your response with this summary format:

**## Plan Summary**

- **Goal:** One sentence describing what this builds
- **Approach:** 2-3 sentences on the technical approach
- **Tasks:** Numbered list of implementation tasks, each with:
  - Task name
  - Files to create/modify (with paths)
  - Key changes (1-2 sentences)
- **Key decisions:** Non-obvious choices and why
- **Risks:** Potential blockers or concerns

Keep the summary concise but actionable — it should be enough for someone to understand the full plan without reading the detailed analysis above it.
`

var builtinTemplates = map[string]string{
	"plan.tmpl": `## Task: Plan Implementation

Analyze the GitHub issue below and produce a clear, actionable implementation plan.

Do NOT implement — only plan. Do NOT modify any files.

### Output Format

## Plan Summary

- **Goal:** One sentence describing what this builds
- **Approach:** 2-3 sentences on the technical approach

## Tasks

1. **[Task name]**
   - Files: ` + "`path/to/file.go`" + ` (create/modify)
   - Changes: [1-2 sentences describing the change]

2. **[Task name]**
   - Files: ` + "`path/to/file.go`" + ` (create/modify)
   - Changes: [1-2 sentences describing the change]

[Repeat for each task]

## Edge Cases
- [Edge case 1 and how to handle it]
- [Edge case 2 and how to handle it]

## Testing Approach
- [How to verify the changes work correctly]

## Risks
- [Potential blockers or concerns]

### Issue Title
{{.Title}}

### Issue Body
{{.IssueBody}}`,

	"approve.tmpl": `## Task: Implement GitHub Issue

Read the full discussion below carefully (issue + all comments), then implement ALL necessary code changes.

### Workflow
1. **Read** existing code before modifying it — understand context first
2. **Plan** the minimal set of changes needed
3. **Implement** using Edit/Write tools
4. **Verify** with ` + "`git diff`" + ` that changes are on disk

### Requirements
- Follow the project's existing code style and conventions
- Handle edge cases mentioned in the discussion
- Make the minimal set of changes needed to fully resolve the issue
- Ensure the code compiles/runs correctly
- Do NOT run git commit/push/add — the caller handles that

### Discussion
{{.Discussion}}{{if .ExtraGuidance}}

## Additional Guidance from Approver (HIGH PRIORITY)

The following instruction takes priority over general discussion. Follow it precisely:

{{.ExtraGuidance}}{{end}}`,

	"followup.tmpl": `## Task: Respond to Follow-Up

Read the full GitHub issue discussion below (original issue + all comments). The latest comment is a follow-up question or request directed at you.

### How to Respond

**If the comment is a question:**
- Answer concisely with specific references to code (file paths, line numbers)
- Explain the relevant architecture or design decisions

**If the comment requests code changes:**
- Do NOT implement changes — you are in discussion mode only
- Explain what changes would be needed: which files, what modifications
- Outline the approach and any trade-offs
- Suggest using ` + "`@claude approve`" + ` to trigger implementation

**If the comment is ambiguous:**
- Ask for clarification by listing possible interpretations
- Provide your best guess answer alongside

### Rules
- Be concise — aim for clarity over verbosity
- Reference specific files and line numbers when discussing code
- If you reference code, quote the relevant snippet
- Do NOT make up information — if you're unsure, say so

### Discussion
{{.Discussion}}`,

	"review.tmpl": `## Task: Code Review (Review Only — Do NOT Modify Files)

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
` + "```diff\n{{.Diff}}\n```",

	"refine.tmpl": `## Task: Apply Code Review Feedback

A senior reviewer has examined your implementation and found issues. Apply their feedback by making the necessary code changes.

### Rules
- Use Edit tool to fix the issues identified below.
- Only fix what the review calls out — do not make unrelated changes.
- After fixing, run "git diff" to verify your changes are on disk.

### Review Feedback
{{.ReviewText}}`,

	"pr-desc.tmpl": `## Task: Generate Pull Request Description

Write a structured pull request description based on the issue and code changes below.

### Requirements
- Start with "Closes #{{.Num}}" on the first line
- Use the structured format below
- Keep it concise but informative
- Write in English
- Output ONLY the PR description text, nothing else

### Format

Closes #{{.Num}}

## Description
[One-paragraph summary: what changed and why]

## Changes
- [Bullet point for each major change]
- [Group related changes together]

## Type of Change
- [ ] Bug fix (non-breaking fix)
- [ ] New feature (non-breaking addition)
- [ ] Breaking change (fix or feature causing existing functionality to change)
- [ ] Refactor (no functional change)
- [ ] Documentation update

## How to Test
[Steps to verify the changes work correctly, if applicable]

## Related Issues
[Reference any related GitHub issues with #issue_number, if applicable]

### Context

**Issue:** {{.IssueTitle}}

**Changed files (diff stat):**
{{.Stat}}

**Full diff:**
` + "```diff\n{{.Diff}}\n```",

	"pr-implement.tmpl": `## Task: Implement PR Changes

Read the full PR discussion below (description + all comments). The latest comment is a request directed at you.

Requirements:
- Read existing code before modifying it
- Follow the project's existing code style and conventions
- Make only the changes requested in the latest comment
- Ensure the code compiles/runs correctly

### PR Discussion
{{.Discussion}}{{if .ExtraGuidance}}

## Additional Guidance (HIGH PRIORITY)

The following instruction takes priority. Follow it precisely:

{{.ExtraGuidance}}{{end}}`,

	"retry.tmpl": `## CRITICAL: Your previous attempt produced ZERO file changes.

### What went wrong
You likely described changes in text instead of using Edit/Write tools to modify files on disk.

### What you must do NOW
1. Use the Edit tool to modify existing files, or Write tool to create new files.
2. Do NOT explain or describe — just make the changes.
3. After editing, run ` + "`git diff`" + ` to confirm your changes are on disk.

### Your previous analysis (reuse this — do NOT re-analyze)
` + "```" + `
{{.FirstResult}}
` + "```" + `

### Original task
{{.OriginalPrompt}}`,
}
