# Claude Webhook Server CLI

## Overview

The Claude Webhook Server is a CLI application that manages GitHub webhook automation. It provides daemon mode for background operation and foreground mode for debugging.

## Installation

```bash
go build -o claude-webhook-server .
```

## Commands

### start - Start the webhook server

Start the webhook server in background (default) or foreground mode.

```bash
# Start in background (daemon mode)
claude-webhook-server start

# Start in foreground (for debugging)
claude-webhook-server start --foreground

# Override port
claude-webhook-server start -p 9090

# Set max concurrent jobs
claude-webhook-server start -j 5
```

**Flags:**
- `--foreground` - Run in the foreground instead of background
- `-p, --port string` - Server port (overrides .env PORT)
- `-j, --max-concurrent int` - Max concurrent jobs (overrides .env MAX_CONCURRENT)
- `-c, --config string` - Config file path (default ~/.claude-webhook/.env)
- `-b, --base-dir string` - Base directory for server files (default ~/.claude-webhook)

### stop - Stop the webhook server

Stop the running webhook server daemon gracefully.

```bash
claude-webhook-server stop
```

The command sends SIGTERM for graceful shutdown. If the server doesn't exit within 10 seconds, it forces a kill.

### restart - Restart the webhook server

Restart the running webhook server daemon.

```bash
claude-webhook-server restart

# Restart with custom port
claude-webhook-server restart -p 9090
```

**Flags:**
- `-p, --port string` - Server port (overrides .env PORT)
- `-j, --max-concurrent int` - Max concurrent jobs (overrides .env MAX_CONCURRENT)

### status - Show server and repository status

Display the status of the webhook server, tunnel configuration, and registered repositories.

```bash
claude-webhook-server status

# Verbose mode
claude-webhook-server status -v

# JSON output
claude-webhook-server status -j
```

**Flags:**
- `-v, --verbose` - Show detailed status
- `-j, --json` - Output in JSON format

### register - Register a repository

Register the current git repository with the webhook server.

```bash
claude-webhook-server register

# Force replace existing webhooks
claude-webhook-server register -f

# Skip webhook configuration
claude-webhook-server register -w

# Skip tunnel setup
claude-webhook-server register -t
```

**Flags:**
- `-f, --force` - Force replace existing webhooks
- `-w, --skip-webhook` - Skip webhook configuration
- `-t, --skip-tunnel` - Skip tunnel setup

## Configuration

The server reads configuration from a `.env` file in the base directory.

**Environment Variables:**

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `GITHUB_WEBHOOK_SECRET` | Yes | - | GitHub webhook secret for HMAC verification |
| `PORT` | No | 8080 | Server port |
| `ALLOWED_USERS` | No | - | Comma-separated list of allowed GitHub usernames |
| `BOT_USERNAME` | No | - | Bot's GitHub username (ignores own comments) |
| `BOT_GITHUB_TOKEN` | No | - | GitHub token for gh CLI operations |
| `BOT_GIT_NAME` | No | - | Git author name for commits |
| `BOT_GIT_EMAIL` | No | - | Git author email for commits |
| `COMMAND_PREFIX` | No | @claude | Command prefix for triggering bot actions |
| `MAX_CONCURRENT` | No | 3 | Maximum concurrent automation jobs |

**Example `.env` file:**

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

## Daemon Mode

When started without `--foreground`, the server runs as a daemon:

**Process Management:**
- PID file: `~/.claude-webhook/server.pid`
- Log file: `~/.claude-webhook/server.log`
- Health check: `http://127.0.0.1:8081/health` (PORT+1)

**Graceful Shutdown:**
- SIGTERM: Graceful shutdown (waits for in-flight jobs)
- SIGINT: Immediate shutdown
- SIGHUP: Reload `repos.conf` configuration

**Health Check:**

```bash
curl http://127.0.0.1:8081/health
```

Response:
```json
{"status":"ok"}
```

## Repository Registration

Repositories are registered in `~/.claude-webhook/repos.conf`:

```conf
# Format: owner/repo = /path/to/local/repo
travelliu/moclaw = /root/code/github/travelliu/moclaw
```

The server reloads this file on SIGHUP or via the `register` command.

## Examples

**Basic workflow:**

```bash
# 1. Register a repository
cd /path/to/repo
claude-webhook-server register

# 2. Start the server
claude-webhook-server start

# 3. Check status
claude-webhook-server status

# 4. View logs
tail -f ~/.claude-webhook/server.log

# 5. Stop the server
claude-webhook-server stop
```

**Development workflow (foreground mode):**

```bash
# Run in foreground for debugging
claude-webhook-server start --foreground

# In another terminal, test webhook
curl -X POST http://localhost:8080/webhook \
  -H "Content-Type: application/json" \
  -H "X-Hub-Signature-256: ..." \
  -d '{...}'
```

## Migration from Shell Scripts

The CLI replaces the previous shell script-based workflow:

| Old Command | New Command |
|-------------|-------------|
| `./start.sh` | `claude-webhook-server start` |
| `./stop.sh` | `claude-webhook-server stop` |
| `./restart.sh` | `claude-webhook-server restart` |
| `./status.sh` | `claude-webhook-server status` |
| `./register.sh` | `claude-webhook-server register` |

## Architecture

```
cmd/
├── root.go       # Root command and global flags
├── start.go      # Start/stop/restart logic
├── stop.go       # Stop command implementation
├── restart.go    # Restart command implementation
├── status.go     # Status command implementation
├── register.go   # Register command implementation
└── config.go     # Configuration loading
```

Each command file:
1. Defines the command with `Use`, `Short`, `Long` descriptions
2. Registers flags in `init()`
3. Implements `run*` function for the command logic
4. Calls `add*Command()` in `init()` to register with root command

## Next Steps

- [ ] Migrate server startup logic from `main.go` to `cmd/start.go`
- [ ] Implement health check endpoint in server
- [ ] Add graceful shutdown handling
- [ ] Implement profile support (multiple instances)
- [ ] Complete register command implementation
- [ ] Complete status command implementation
