#!/usr/bin/env bash
set -euo pipefail

# Remote installer: curl -sL <url>/remote-install.sh | bash
# Downloads pre-built binary from GitHub Releases (no Go required).
# Run from any git repo root to install the server and register that repo.

GITHUB_REPO="htlin222/claude-with-webhook"
RAW_URL="https://raw.githubusercontent.com/${GITHUB_REPO}/main"
SERVER_DIR="$HOME/.claude-webhook"

echo "=== Claude Webhook Installer ==="
echo

# Must be in a git repo.
if ! git rev-parse --is-inside-work-tree &>/dev/null; then
  echo "ERROR: Not inside a git repository."
  exit 1
fi

REPO_DIR=$(git rev-parse --show-toplevel)

# Check prerequisites (Go is NOT required — we download the binary).
missing=()
for cmd in gh claude git jq openssl; do
  if ! command -v "$cmd" &>/dev/null; then
    missing+=("$cmd")
  fi
done

# Need at least one tunnel tool.
has_tunnel=false
for cmd in tailscale ngrok zrok; do
  if command -v "$cmd" &>/dev/null; then
    has_tunnel=true
    break
  fi
done
if [ "$has_tunnel" = false ]; then
  missing+=("tailscale/ngrok/zrok")
fi

if [ ${#missing[@]} -gt 0 ]; then
  echo "ERROR: Missing required tools: ${missing[*]}"
  exit 1
fi

echo "All prerequisites found."

# --- Detect platform ---
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)       echo "ERROR: Unsupported architecture: $ARCH"; exit 1 ;;
esac

BINARY_NAME="claude-webhook-server-${OS}-${ARCH}"
echo "Platform: ${OS}/${ARCH}"

# --- Download binary from latest release ---
mkdir -p "$SERVER_DIR"

echo "Downloading latest binary..."
gh release download --repo "$GITHUB_REPO" \
  --pattern "$BINARY_NAME" \
  --dir "$SERVER_DIR" \
  --clobber
mv "$SERVER_DIR/$BINARY_NAME" "$SERVER_DIR/claude-webhook-server"
chmod +x "$SERVER_DIR/claude-webhook-server"
echo "Installed: $SERVER_DIR/claude-webhook-server"

# --- Download scripts ---
echo "Downloading scripts..."
for script in register status; do
  curl -sL "$RAW_URL/scripts/$script" -o "$SERVER_DIR/$script"
  chmod +x "$SERVER_DIR/$script"
done

# Create start/stop scripts.
cat > "$SERVER_DIR/start" <<'STARTEOF'
#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$DIR"
exec ./claude-webhook-server "$@"
STARTEOF
chmod +x "$SERVER_DIR/start"

cat > "$SERVER_DIR/stop" <<'STOPEOF'
#!/usr/bin/env bash
pkill -f claude-webhook-server 2>/dev/null && echo "Server stopped." || echo "Server not running."
STOPEOF
chmod +x "$SERVER_DIR/stop"

# --- Generate .env if not present ---
if [ -f "$SERVER_DIR/.env" ]; then
  echo ".env already exists, reusing."
else
  WEBHOOK_SECRET=$(openssl rand -hex 20)
  GH_USER=$(gh api user --jq '.login')
  PORT=8080

  cat > "$SERVER_DIR/.env" <<EOF
GITHUB_WEBHOOK_SECRET=$WEBHOOK_SECRET
ALLOWED_USERS=$GH_USER
PORT=$PORT
EOF
  echo "Generated .env (user=$GH_USER, port=$PORT)"
fi

# Source .env.
set -a
source "$SERVER_DIR/.env"
set +a

# --- Register this repo ---
REPOS_CONF="$SERVER_DIR/repos.conf"
touch "$REPOS_CONF"

GH_REPO_NAME=$(gh repo view --json nameWithOwner --jq '.nameWithOwner' 2>/dev/null || true)
if [ -z "$GH_REPO_NAME" ]; then
  echo "ERROR: Could not detect GitHub repo name."
  exit 1
fi

if grep -q "^${GH_REPO_NAME}=" "$REPOS_CONF" 2>/dev/null; then
  sed -i.bak "s|^${GH_REPO_NAME}=.*|${GH_REPO_NAME}=${REPO_DIR}|" "$REPOS_CONF"
  rm -f "$REPOS_CONF.bak"
  echo "Updated repo: $GH_REPO_NAME → $REPO_DIR"
else
  echo "${GH_REPO_NAME}=${REPO_DIR}" >> "$REPOS_CONF"
  echo "Registered repo: $GH_REPO_NAME → $REPO_DIR"
fi

# Signal running server to reload.
if pkill -HUP -f claude-webhook-server 2>/dev/null; then
  echo "Sent SIGHUP to running server — repos.conf reloaded."
else
  echo "Server not running (will pick up new repo on next start)."
fi

# Create worktrees dir in the repo.
mkdir -p "$REPO_DIR/worktrees"
if ! grep -qx 'worktrees/' "$REPO_DIR/.gitignore" 2>/dev/null; then
  echo 'worktrees/' >> "$REPO_DIR/.gitignore"
  echo "Added worktrees/ to .gitignore"
fi

# --- Ensure gh has admin:repo_hook scope ---
echo "Checking GitHub CLI scopes..."
SCOPES=$(gh auth status 2>&1 || true)
if echo "$SCOPES" | grep -q "admin:repo_hook"; then
  echo "admin:repo_hook scope OK."
else
  echo "Requesting admin:repo_hook scope..."
  gh auth refresh -h github.com -s admin:repo_hook
fi

# --- Tunnel setup (reuse register script logic) ---
"$SERVER_DIR/register"

echo
echo "=== Installation Complete ==="
echo
echo "Repo registered: $GH_REPO_NAME → $REPO_DIR"
echo "Server dir:      $SERVER_DIR"
echo
echo "  Start:    $SERVER_DIR/start"
echo "  Stop:     $SERVER_DIR/stop"
echo "  Status:   $SERVER_DIR/status"
echo "  Register: cd /path/to/repo && $SERVER_DIR/register"
echo
echo "Add more repos:"
echo "  cd /path/to/another-repo && $SERVER_DIR/register"
echo
