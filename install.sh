#!/usr/bin/env bash
set -euo pipefail

# Local installer: run from the claude-with-webhook repo checkout.
# Builds server and installs binary to ~/.local/bin/.

SERVER_DIR="$(cd "$(dirname "$0")" && pwd)"
BIN_DIR="$HOME/.local/bin"
WORK_DIR="$HOME/.claude-webhook"

echo "=== Claude Webhook Server Installer ==="
echo

# Check prerequisites.
missing=()
for cmd in go gh claude git jq openssl; do
  if ! command -v "$cmd" &>/dev/null; then
    missing+=("$cmd")
  fi
done

if [ ${#missing[@]} -gt 0 ]; then
  echo "ERROR: Missing required tools: ${missing[*]}"
  exit 1
fi

echo "All prerequisites found."

# Build.
echo "Building server..."
VERSION=$(git -C "$SERVER_DIR" describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS="-X main.version=$VERSION -X main.buildTime=$BUILD_TIME"
go build -C "$SERVER_DIR" -ldflags "$LDFLAGS" -o "$SERVER_DIR/claude-webhook-server" .

# Install binary.
mkdir -p "$BIN_DIR"
cp "$SERVER_DIR/claude-webhook-server" "$BIN_DIR/claude-webhook-server"
chmod +x "$BIN_DIR/claude-webhook-server"
echo "Installed: $BIN_DIR/claude-webhook-server"

# Create working directory.
mkdir -p "$WORK_DIR"
echo "$SERVER_DIR" > "$WORK_DIR/source-repo"

# Generate .env if not present.
if [ -f "$WORK_DIR/.env" ]; then
  echo ".env already exists, reusing."
else
  WEBHOOK_SECRET=$(openssl rand -hex 20)
  GH_USER=$(gh api user --jq '.login')

  cat > "$WORK_DIR/.env" <<EOF
GITHUB_WEBHOOK_SECRET=$WEBHOOK_SECRET
ALLOWED_USERS=$GH_USER
PORT=8080
EOF
  echo "Generated .env (user=$GH_USER)"
fi

echo
echo "=== Installation Complete ==="
echo
echo "  Binary:    $BIN_DIR/claude-webhook-server"
echo "  Work dir:  $WORK_DIR/"
echo
echo "Next steps:"
echo "  1. claude-webhook-server bot add               # Add a bot"
echo "  2. claude-webhook-server repo add /path/to/repo # Register a repo"
echo "  3. claude-webhook-server daemon start           # Start the server"
echo
