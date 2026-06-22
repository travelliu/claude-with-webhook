# CLAUDE.md — Project Rules

## Code Mode Detection

### Discussion Mode (NO code changes)

When the user is asking questions, analyzing, discussing, reviewing, or planning WITHOUT explicitly requesting code changes:

- DO NOT write, edit, or create any files
- DO NOT run build/test commands
- ONLY analyze, explain, discuss, and provide recommendations
- Use Read, grep, find tools to explore the codebase for analysis purposes only

Triggers: questions, analysis requests, planning, discussion, any message that does NOT explicitly ask to write/modify/implement code.

### Code Mode (MUST create worktree)

When the user explicitly requests code changes:

- MUST create a git worktree FIRST before making any changes
- All code work happens in the isolated worktree
- Never modify files in the main working directory directly

Triggers: "implement", "write", "create", "add", "fix", "refactor", "update", "modify", "change", "build", specific file/code instructions.

### Edge Cases

- If unclear, default to Discussion Mode and ask the user to clarify
- "Implement this plan" → Code Mode
- "What would you change?" → Discussion Mode
- "Fix the bug" → Code Mode
- "Show me the bug" → Discussion Mode
