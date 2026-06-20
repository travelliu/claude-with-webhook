# Claude Code Webhook Server

[![Stars](https://img.shields.io/github/stars/htlin222/claude-with-webhook?style=flat)](https://github.com/htlin222/claude-with-webhook/stargazers)
[![Last Commit](https://img.shields.io/github/last-commit/htlin222/claude-with-webhook)](https://github.com/htlin222/claude-with-webhook/commits/main)
[![Go](https://img.shields.io/badge/Go-1.23+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

一个 Go 服务器，通过 GitHub Issue 自动化 Claude Code 的规划和实现。一个服务器可处理多个仓库，按 URL 路径路由。支持多个 bot，每个 bot 使用不同的 AI 后端，通过 `@提及` 触发。

## 工作原理

```
你创建 Issue ──→ GitHub 发送 webhook ──→ 隧道 (Tailscale/ngrok/zrok) ──→ 你的机器
                                                                   │
                    ┌──────────────────────────────────────────────┘
                    ▼
             claude-webhook-server (localhost:8080)
                    │
                    ├─ @bot-a 提及 → Bot A (Claude 后端)
                    ├─ @bot-b 提及 → Bot B (其他后端)
                    └─ 发布规划、实现修改、创建 PR
```

所有处理都在**你的机器上**完成，使用**你本地的 CLI 工具** — 不需要 API key，不需要云端运行器。

## 前置要求

- [Go](https://go.dev/dl/) 1.23+
- [GitHub CLI](https://cli.github.com/) (`gh`) — 通过 `gh auth login` 认证
- [Claude Code](https://docs.anthropic.com/en/docs/claude-code) (`claude`) — 需要有效订阅
- [Tailscale](https://tailscale.com/download) 并启用 [Funnel](https://tailscale.com/kb/1223/funnel)、[ngrok](https://ngrok.com/download) 或 [zrok](https://zrok.io)（任选其一，用于隧道）
- Git、jq、openssl

## 安装

### 从源码编译（需要 Go 1.23+）

```bash
git clone https://github.com/htlin222/claude-with-webhook.git
cd claude-with-webhook
make install
```

### Make 命令

| 命令 | 说明 |
|------|------|
| `make build` | 编译服务器二进制文件 |
| `make install` | 编译并安装到 `~/.local/bin/`，创建工作目录 `~/.claude-webhook/` |
| `make restart` | 编译 + 安装 + 重启服务器 |
| `make uninstall` | 停止服务器并删除二进制文件（工作目录保留） |

## 快速开始

### 0. 登录 GitHub

```bash
gh auth login
```

按提示选择 GitHub.com → 浏览器登录 → 完成设备码验证。如果需要 webhook 管理权限，登录后执行：

```bash
gh auth refresh -h github.com -s admin:repo_hook
```

### 1. 添加 Bot

```bash
# 交互模式 — 自动从 `gh auth status` 检测 GitHub 账户
claude-webhook-server bot add

# 使用显式参数
claude-webhook-server bot add \
  --name claude \
  --username my-bot \
  --token ghp_xxx \
  --prefix @claude \
  --agent claude \
  --git-name "Claude Bot" \
  --git-email "bot@example.com"

# 列出已配置的 bot
claude-webhook-server bot list

# 删除 bot
claude-webhook-server bot remove --name claude
```

当省略 `--username` 和 `--token` 时，`bot add` 会读取 `gh auth status` 并让你交互选择账户。如果只有一个账户，会自动选中。

### 2. 注册仓库

```bash
# 在仓库目录内
cd /path/to/your-repo
claude-webhook-server repo add

# 或指定目录
claude-webhook-server repo add /path/to/your-repo
```

`repo add` 命令会：
1. 检测 git 仓库及其 GitHub remote
2. 提示你选择 bot（如果有多个已配置的 bot）
3. 设置隧道（Tailscale/ngrok/zrok）
4. 创建或更新 GitHub webhook
5. 在 `repos.yaml` 中注册仓库
6. 发送信号通知运行中的服务器重载配置

可用参数：`--dir <path>`、`--force`、`--skip-webhook`、`--skip-tunnel`、`--bot <name>`、`--webhook-user <gh用户名>`、`--allow <用户1,用户2>`。

### 3. 启动服务器

```bash
claude-webhook-server daemon start
```

## 多 Bot 系统

服务器支持多个 bot，每个 bot 有独立的：
- **GitHub 账户** — 用于发布评论和创建 PR
- **AI 后端** — agent 后端（claude 等）
- **命令前缀** — 触发 bot 的前缀（`@claude`、`@helper` 等）
- **Git 身份** — 提交作者的用户名和邮箱

### Bot 配置（`~/.claude-webhook/bots.yaml`）

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

### 路由

当评论首行包含 `@bot-name` 时，匹配的 bot 会处理请求：

```
@claude approve          → 路由到 "claude" bot
@helper can you explain? → 路由到 "helper" bot
```

### 向后兼容

如果 `bots.yaml` 不存在但设置了环境变量（`BOT_USERNAME`、`BOT_GITHUB_TOKEN`），会自动从这些变量创建默认 bot。

## 使用方法

### 命令

所有命令都需要 bot 前缀以防止误触发：

```
@claude approve                       # 开始实现
@claude approve focus on error handling
@claude approve --auto-merge          # PR 创建后自动合并
@claude approve --polish              # PR 前运行代码审查
@claude plan                          # 重新生成规划
@claude <后续问题>                     # 提问任何问题
@claude lgtm                          # 等同于 approve
```

这些命令同时适用于 **Issue** 和 **Pull Request**：

- **在 Issue 上：** `@claude approve` 创建新分支、实现修改并打开 PR。
- **在 PR 上：** `@claude approve` 检出 PR 分支、实现修改并推送。

### Issue 标签

| 标签 | 触发时机 | 含义 |
|------|---------|------|
| `planning` | Issue 创建 / `@claude plan` | 正在生成规划 |
| `planned` | 规划已发布 | 规划待审阅 |
| `implementing` | `@claude approve` | 正在编写代码 |
| `review` | PR 已创建 | 待代码审查 |
| `done` | PR 自动合并 | 已完成 |

## 提示词自定义

可按仓库和操作自定义系统提示词：

```
~/.claude-webhook/prompts/
  default.md              # 全局默认提示词
  owner/repo1/
    default.md            # 仓库级覆盖
    plan.md               # 仓库 + 操作级覆盖
    implement.md
```

查找顺序：`{repo}/{action}.md` → `{repo}/default.md` → `default.md` → 内置默认值。

## 架构

```
~/.local/bin/
└── claude-webhook-server       # 二进制文件（在 PATH 中）

~/.claude-webhook/              # 工作目录
├── bots.yaml                   # Bot 配置
├── repos.yaml                  # 仓库注册表（仓库级配置）
├── prompts/                    # 自定义提示词
│   ├── default.md
│   └── owner/repo/
├── .env                        # 配置（密钥、端口）
└── server.log                  # 服务器日志（守护进程模式时）
```

### Agent 抽象

服务器使用可插拔的 agent 后端系统：

```go
type Backend interface {
    Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error)
    Name() string
    CLIPath() (string, bool)
}
```

目前支持：`claude`（Claude Code CLI）。可扩展支持其他后端。

## 接口

| 方法 | 路径 | 说明 |
|------|------|------|
| `POST` | `/{owner}/{repo}/webhook` | Webhook 接收端点 |
| `GET` | `/health` | 健康检查 |

## 仓库配置（`~/.claude-webhook/repos.yaml`）

每个注册的仓库有独立配置：

```yaml
repos:
  owner/repo1:
    dir: /home/user/projects/repo1
    allowed_users:
      - alice
      - bob
    webhook_token: ghp_xxx  # 具有 admin:repo_hook 权限的 token

  owner/repo2:
    dir: /home/user/projects/repo2
```

| 字段 | 说明 |
|------|------|
| `dir` | 仓库本地路径 |
| `allowed_users` | 允许触发 bot 的 GitHub 用户名（仓库级） |
| `webhook_token` | 具有 `admin:repo_hook` 权限的 GitHub token，用于管理 webhook |

权限检查顺序：仓库 `allowed_users` → 全局 `ALLOWED_USERS` → GitHub 协作者（write+）。

## 环境变量

| 变量 | 说明 |
|------|------|
| `GITHUB_WEBHOOK_SECRET` | Webhook 验证共享密钥 |
| `ALLOWED_USERS` | 逗号分隔的 GitHub 用户名（全局兜底 — 建议用仓库级配置） |
| `PORT` | 服务器端口（默认：`8080`） |
| `PUBLIC_URL` | 公网 URL（跳过隧道自动检测） |

## 安全

- **命令超时** — 规划：30 分钟，实现：60 分钟，git 命令：30 秒
- **并发限制** — 最多 3 个并发任务
- **事件去重** — `X-GitHub-Delivery` UUID 跟踪
- **错误脱敏** — 从错误评论中剥离密钥
- **过滤 git add** — `.env*`、`*.pem`、`*.key` 等文件不会被暂存
- **Worktree 隔离** — 实现在隔离的 git worktree 中运行

## 常见问题

**问：需要 Anthropic API key 吗？**
不需要。服务器调用你本地的 `claude` CLI，使用你现有的订阅。

**问：多人可以共享一个服务器吗？**
可以 — 把所有用户名添加到 `ALLOWED_USERS`。

**问：服务器没运行时创建了 Issue 怎么办？**
评论 `@claude plan` 重新触发规划。

**问：可以用 ngrok 或 zrok 代替 Tailscale 吗？**
可以。自动检测顺序：tailscale → ngrok → zrok。

## 许可证

[MIT](LICENSE)
