# Claude Code Webhook Server

[![Stars](https://img.shields.io/github/stars/htlin222/claude-with-webhook?style=flat)](https://github.com/htlin222/claude-with-webhook/stargazers)
[![Last Commit](https://img.shields.io/github/last-commit/htlin222/claude-with-webhook)](https://github.com/htlin222/claude-with-webhook/commits/main)
[![Go](https://img.shields.io/badge/Go-1.23+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![GitHub issues](https://img.shields.io/github/issues/htlin222/claude-with-webhook)](https://github.com/htlin222/claude-with-webhook/issues)

A Go server that automates Claude Code planning and implementation via GitHub issues. One server handles multiple repos, routed by URL path. Supports multiple bots with different AI backends, each triggered by `@mention`.

## How it works

```
You open an Issue ──→ GitHub sends webhook ──→ Tunnel (Tailscale/ngrok/zrok) ──→ Your machine
                                                                        │
                     ┌──────────────────────────────────────────────────┘
                     ▼
              claude-webhook-server (localhost:8080)
                     │
                     ├─ @bot-a mentions → Bot A (Claude backend)
                     ├─ @bot-b mentions → Bot B (different backend)
                     └─ Posts plan, implements changes, opens PR
```

All processing happens on **your machine** using **your local CLI tools** — no API key, no cloud runners.

## Prerequisites

- [Go](https://go.dev/dl/) 1.23+
- [GitHub CLI](https://cli.github.com/) (`gh`) — authenticated via `gh auth login`
- [Claude Code](https://docs.anthropic.com/en/docs/claude-code) (`claude`) — with an active subscription
- [Tailscale](https://tailscale.com/download) with [Funnel](https://tailscale.com/kb/1223/funnel) enabled, [ngrok](https://ngrok.com/download), or [zrok](https://zrok.io) (any one — for tunneling)
- Git, jq, openssl

## Install

### From source (requires Go 1.23+)

```bash
git clone https://github.com/htlin222/claude-with-webhook.git
cd claude-with-webhook
make install
```

### Make commands

| Command | Description |
|---------|-------------|
| `make build` | Build the server binary |
| `make install` | Build + install binary to `~/.local/bin/`, create work dir `~/.claude-webhook/` |
| `make restart` | Build + install + restart server |
| `make uninstall` | Stop server and remove binary (work dir preserved) |

## Quick Start

### 0. Login to GitHub

```bash
gh auth login
```

Follow the prompts: select GitHub.com → browser login → complete device code verification. For webhook management, also run:

```bash
gh auth refresh -h github.com -s admin:repo_hook
```

### 1. Add a bot

```bash
# Interactive — auto-detects GitHub account from `gh auth status`
claude-webhook-server bot add

# Explicit flags
claude-webhook-server bot add \
  --name claude \
  --username my-bot \
  --token ghp_xxx \
  --prefix @claude \
  --agent claude \
  --git-name "Claude Bot" \
  --git-email "bot@example.com"

# List configured bots
claude-webhook-server bot list

# Remove a bot
claude-webhook-server bot remove --name claude
```

When `--username` and `--token` are omitted, `bot add` reads `gh auth status` and lets you select an account interactively. If only one account is logged in, it's auto-selected.

### 2. Register a repo

```bash
cd /path/to/your-repo
claude-webhook-server repo add
```

The `repo add` command:
1. Detects the current git repo and its GitHub remote
2. Prompts you to select a bot (if multiple are configured)
3. Sets up the tunnel (Tailscale/ngrok/zrok)
4. Creates or updates the GitHub webhook
5. Registers the repo in `repos.yaml`
6. Creates default prompt templates in `~/.claude-webhook/prompts/{owner}/{repo}/`
7. Signals the running server to reload config

Flags: `--dir <path>` (repo directory, defaults to cwd), `--force`, `--skip-webhook`, `--skip-tunnel`, `--bot <name>`, `--webhook-user <gh-username>`, `--allow <user1,user2>`.

### 3. Start the server

```bash
claude-webhook-server daemon start
```

## Multi-Bot System

The server supports multiple bots, each with its own:
- **GitHub account** — for posting comments and creating PRs
- **AI backend** — agent backend (claude, etc.)
- **Command prefix** — triggers the bot (`@claude`, `@helper`, etc.)
- **Git identity** — commit author name/email

### Bots configuration (`~/.claude-webhook/bots.yaml`)

```yaml
bots:
  - name: claude
    username: my-claude-bot
    token: ghp_xxx
    prefix: "@claude"
    agent: claude
    git_name: Claude Bot
    git_email: bot@example.com

  - name: helper
    username: helper-bot
    token: ghp_yyy
    prefix: "@helper"
    agent: claude
    git_name: Helper Bot
    git_email: helper@example.com
```

### Routing

When a comment contains `@bot-name` as the first line, the matching bot handles it:

```
@claude approve          → routed to "claude" bot
@helper can you explain? → routed to "helper" bot
```

### Backward Compatibility

If `bots.yaml` doesn't exist but env vars (`BOT_USERNAME`, `BOT_GITHUB_TOKEN`) are set, a default bot is auto-created from those vars.

## Usage

### Commands

All commands require a bot prefix to prevent accidental triggers:

```
@claude approve                       # start implementation
@claude approve focus on error handling
@claude approve --auto-merge          # auto-merge PR after creation
@claude approve --polish              # run code review before PR
@claude plan                          # re-generate plan
@claude <follow-up question>          # ask anything
@claude lgtm                          # same as approve
```

These work on both **issues** and **pull requests**:

- **On an issue:** `@claude approve` creates a new branch, implements changes, and opens a PR.
- **On a PR:** `@claude approve` checks out the PR branch, implements changes, and pushes.

### Issue Labels

| Label | When | Meaning |
|-------|------|---------|
| `planning` | Issue opened / `@claude plan` | Generating a plan |
| `planned` | Plan posted | Plan ready for review |
| `implementing` | `@claude approve` | Writing code |
| `review` | PR created | Ready for review |
| `done` | PR auto-merged | Fully resolved |

## Prompt Customization

Prompts use Go `text/template` syntax. Each task has a `.tmpl` template with variables like `{{.Title}}`, `{{.Discussion}}`, etc.

### Template Files

```
~/.claude-webhook/prompts/
├── system.md           # System prompt (base behavioral rules)
├── plan.tmpl           # @claude plan
├── approve.tmpl        # @claude approve
├── followup.tmpl       # @claude <question>
├── review.tmpl         # Code review (--polish)
├── refine.tmpl         # Apply review feedback
├── pr-desc.tmpl        # Generate PR description
├── pr-implement.tmpl   # Implement PR changes
├── retry.tmpl          # Retry when no changes detected
└── owner/repo/         # Per-repo overrides (auto-created on repo add)
    ├── system.md
    ├── plan.tmpl
    └── ...
```

### Lookup Order

Repo-specific → global → built-in. For example, `approve.tmpl` for `owner/repo`:

1. `~/.claude-webhook/prompts/owner/repo/approve.tmpl`
2. `~/.claude-webhook/prompts/approve.tmpl`
3. Built-in default

### Template Variables

| Variable | Used In | Description |
|----------|---------|-------------|
| `.Title` | plan | Issue title |
| `.IssueBody` | plan | Issue body |
| `.Discussion` | approve, followup, pr-implement | Full issue/PR discussion |
| `.ExtraGuidance` | approve, pr-implement | Additional guidance from approver |
| `.Diff` | review, pr-desc | Git diff |
| `.Num` | pr-desc | Issue/PR number |
| `.IssueTitle` | pr-desc | Issue title |
| `.Stat` | pr-desc | Diff stat |
| `.ReviewText` | refine | Code review feedback |
| `.FirstResult` | retry | Previous agent output |
| `.OriginalPrompt` | retry | Original task prompt |

### System Prompt vs Task Prompt

- **`system.md`** — Base rules for all tasks (behavioral, git, quality). Sent via `--append-system-prompt`.
- **Task templates** (`.tmpl`) — Task-specific instructions. Sent as user message.

## Architecture

```
~/.local/bin/
└── claude-webhook-server       # Binary (in PATH)

~/.claude-webhook/              # Working directory
├── bots.yaml                   # Bot configurations
├── repos.yaml                  # Repo registry (per-repo config)
├── prompts/                    # Prompt templates (text/template)
│   ├── system.md               # System prompt (base rules)
│   ├── plan.tmpl               # Task templates
│   ├── approve.tmpl
│   ├── followup.tmpl
│   ├── review.tmpl
│   ├── refine.tmpl
│   ├── pr-desc.tmpl
│   ├── pr-implement.tmpl
│   ├── retry.tmpl
│   └── owner/repo/             # Per-repo overrides (auto-created)
├── .env                        # Config (secret, port)
└── server.log                  # Server logs (when running as daemon)
```

### Agent Abstraction

The server uses a pluggable agent backend system:

```go
type Backend interface {
    Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error)
    Name() string
    CLIPath() (string, bool)
}
```

Currently supported: `claude` (Claude Code CLI). Can be extended for other backends.

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/{owner}/{repo}/webhook` | Webhook receiver |
| `GET` | `/health` | Health check |

## Repo Configuration (`~/.claude-webhook/repos.yaml`)

Each registered repo has its own config:

```yaml
repos:
  owner/repo1:
    dir: /home/user/projects/repo1
    allowed_users:
      - alice
      - bob
    webhook_token: ghp_xxx  # auto-filled from gh auth

  owner/repo2:
    dir: /home/user/projects/repo2
```

| Field | Description |
|-------|-------------|
| `dir` | Local path to the repo |
| `allowed_users` | GitHub usernames allowed to trigger the bot (repo-level) |
| `webhook_token` | GitHub token with `admin:repo_hook` scope (auto-detected from gh auth) |

Permission check order: repo `allowed_users` → global `ALLOWED_USERS` → GitHub collaborator (write+).

## Environment Variables

| Variable | Description |
|----------|-------------|
| `GITHUB_WEBHOOK_SECRET` | Shared secret for webhook validation |
| `ALLOWED_USERS` | Comma-separated GitHub usernames (global fallback — prefer repo-level) |
| `PORT` | Server port (default: `8080`) |
| `PUBLIC_URL` | Public URL (skip tunnel auto-detection) |

## Security

- **Command timeouts** — Planning: 30 min, implementation: 60 min, git commands: 30 sec
- **Concurrency limit** — Max 3 concurrent jobs
- **Event deduplication** — `X-GitHub-Delivery` UUID tracking
- **Error sanitization** — Secrets stripped from error comments
- **Filtered git add** — `.env*`, `*.pem`, `*.key`, etc. never staged
- **Worktree isolation** — Implementations run in isolated git worktrees

## FAQ

**Q: Do I need an Anthropic API key?**
No. The server calls your local `claude` CLI, which uses your existing subscription.

**Q: Can multiple people share one server?**
Yes — add all usernames to `ALLOWED_USERS`.

**Q: What if the server is down when an issue is opened?**
Comment `@claude plan` to re-trigger planning.

**Q: Can I use ngrok or zrok instead of Tailscale?**
Yes. Auto-detected in order: tailscale → ngrok → zrok.

## License

[MIT](LICENSE)
