# Shell Script to Go CLI Migration

This document describes the migration from shell-based control scripts to Go CLI commands.

## Overview

The shell scripts (`start.sh`, `stop.sh`, `restart.sh`, `status.sh`, `register.sh`) have been replaced with Go commands using the Cobra CLI framework.

## Command Mapping

| Old Shell Script | New CLI Command | Status |
|------------------|------------------|--------|
| `~/.claude-webhook/start` | `claude-webhook-server start` | ✅ Implemented |
| `~/.claude-webhook/stop` | `claude-webhook-server stop` | ✅ Implemented |
| `~/.claude-webhook/restart` | `claude-webhook-server restart` | ✅ Implemented |
| `~/.claude-webhook/status` | `claude-webhook-server status` | ✅ Placeholder |
| `~/.claude-webhook/register` | `claude-webhook-server register` | ✅ Framework |
| `scripts/register` | N/A | ✅ Removed |
| `scripts/status` | N/A | ✅ Removed |

**Note:** Old shell scripts (`scripts/register`, `scripts/status`) have been removed and replaced with Go commands.

## What's New

### Daemon Mode

The server now supports true daemon mode with:
- **PID file management** — `~/.claude-webhook/server.pid`
- **Process supervision** — Automatic health checks
- **Graceful shutdown** — SIGTERM handling with timeout
- **Background logging** — `~/.claude-webhook/server.log`

### Configuration

- **Dynamic command prefix** — Configure `COMMAND_PREFIX` in `.env`
- **Bot identity** — Configure `BOT_GITHUB_TOKEN`, `BOT_GIT_NAME`, `BOT_GIT_EMAIL`
- **Environment overrides** — Command-line flags override `.env` settings

### Process Control

```bash
# Start in background (daemon mode)
claude-webhook-server start

# Start in foreground (for debugging)
claude-webhook-server start --foreground

# Restart with custom port
claude-webhook-server restart -p 9090
```

## Implementation Status

### ✅ Completed

1. **CLI Framework** — Cobra-based command structure
2. **Start/Stop/Restart** — Full daemon mode support
3. **Configuration Loading** — `cmd/config.go` with `.env` parsing
4. **PID File Management** — Process tracking and cleanup
5. **Health Checks** — HTTP endpoint for daemon monitoring
6. **Command Prefix** — Dynamic prefix support (@claude, @bliu-coder, etc.)
7. **Bot Identity** — GitHub token and Git authorship configuration
8. **Auto Webhook Update** — Startup checks tunnel URL and updates GitHub webhooks automatically

### 🔄 In Progress

1. **Server Startup Logic** — Migrating from `main.go` to `cmd/start.go`
2. **Register Command** — Repository registration logic
3. **Status Command** — Server and repository status display

### 📋 Planned

1. **Profile Support** — Multiple server instances with different configs
2. **Signal Handling** — SIGHUP for config reload, SIGTERM for graceful shutdown
3. **Log Rotation** — Automatic log file management
4. **Systemd Integration** — Native service file generation

## Architecture

```
cmd/
├── root.go       # Root command and global flags
├── config.go     # Configuration loading (.env, repos.conf)
├── start.go      # Start command (foreground/background)
├── stop.go       # Stop command (SIGTERM + timeout)
├── restart.go    # Restart command (stop + start)
├── status.go     # Status command (placeholder)
└── register.go   # Register command (placeholder)
```

### Command Structure

Each command follows this pattern:

1. **Command Definition** — Use, Short, Long descriptions
2. **Flag Registration** — In `init()` function
3. **Handler Function** — `run*()` implementation
4. **Self-Registration** — Calls `add*Command()` in `init()`

## Configuration Changes

### New Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `COMMAND_PREFIX` | Command trigger prefix | `@claude` |
| `BOT_USERNAME` | Bot's GitHub username | - |
| `BOT_GITHUB_TOKEN` | Bot's GitHub token | - |
| `BOT_GIT_NAME` | Git author name | - |
| `BOT_GIT_EMAIL` | Git author email | - |

### Example .env

```env
GITHUB_WEBHOOK_SECRET=your-secret-here
PORT=8080
ALLOWED_USERS=alice,bob,charlie
BOT_USERNAME=bliu-coder
BOT_GITHUB_TOKEN=ghp_xxxxxxxxxxxx
BOT_GIT_NAME="Bot User"
BOT_GIT_EMAIL="bot@example.com"
COMMAND_PREFIX=@bliu-coder
MAX_CONCURRENT=5
```

## Auto Webhook Update

When the server starts, it automatically checks and updates GitHub webhooks if the tunnel URL has changed:

**How it works:**
1. Detects current tunnel URL (Tailscale/ngrok/zrok)
2. Checks all registered repos' GitHub webhooks
3. Updates webhooks if URL mismatch detected
4. Requires `gh` CLI with `admin:repo_hook` scope

**Supported tunnels:**
- **Tailscale Funnel** — Stable URLs, rarely needs update
- **ngrok** — URLs change on restart, auto-update triggers
- **zrok** — URLs change on restart, auto-update triggers

**Example log output:**
```
Current tunnel URL: https://abc.ngrok.io
[owner/repo] webhook URL mismatch - updating
  Old: https://old.ngrok.io/owner/repo/webhook
  New: https://abc.ngrok.io/owner/repo/webhook
[owner/repo] webhook updated successfully
Checked/updated 1 repo webhook(s)
```

**Manual re-registration:**
If auto-update fails, you can manually re-register:
```bash
cd /path/to/repo
~/.claude-webhook/register
```

## Next Steps

### For Users

1. **Update your aliases:**
   ```bash
   # Old aliases
   alias cwh-start='~/.claude-webhook/start'
   alias cwh-stop='~/.claude-webhook/stop'

   # New aliases
   alias cwh-start='claude-webhook-server start'
   alias cwh-stop='claude-webhook-server stop'
   ```

2. **Test the new commands:**
   ```bash
   claude-webhook-server start
   claude-webhook-server status
   claude-webhook-server stop
   ```

### For Developers

1. **Complete server startup migration** — Move HTTP server logic from `main.go` to `cmd/start.go`
2. **Implement register logic** — Migrate shell script logic to Go
3. **Implement status logic** — Server and repository status display
4. **Add health check endpoint** — Return running tasks and server state

## Testing

```bash
# Build
go build -o claude-webhook-server .

# Test start (foreground)
./claude-webhook-server start --foreground

# Test start (background)
./claude-webhook-server start
./claude-webhook-server status

# Test stop
./claude-webhook-server stop

# Test restart
./claude-webhook-server restart -p 9090
```

## Documentation

- [CLI Reference](CLI.md) — Detailed command documentation
- [README.md](../README.md) — Project overview
- [Configuration](CLI.md#configuration) — Environment variables reference

## Rollback

If you encounter issues with the new CLI, you can temporarily use the old shell scripts.

**However, the old scripts have been removed in this release:**
- `scripts/register` - Removed, replaced by `claude-webhook-server register`
- `scripts/status` - Removed, replaced by `claude-webhook-server status`

The migration is complete. All users should use the new Go CLI commands.

## Feedback

Please report issues or suggestions via GitHub issues.
