package github

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Webhook represents a GitHub webhook
type Webhook struct {
	ID   int    `json:"id"`
	URL  string `json:"url"`
	Active bool  `json:"active"`
}

// Client handles GitHub API operations
type Client struct {
	// Uses gh CLI for authentication
}

// NewClient creates a new GitHub client
func NewClient() *Client {
	return &Client{}
}

// GetWebhooks gets all webhooks for a repository
func (c *Client) GetWebhooks(repo string) ([]Webhook, error) {
	out, err := exec.Command("gh", "api", fmt.Sprintf("repos/%s/hooks", repo),
		"--jq", ".[] | select(.config.url | endswith(\"/webhook\")) | {id: .id, url: .config.url, active: .active}").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("get webhooks: %s", strings.TrimSpace(string(out)))
	}

	if len(out) == 0 {
		return []Webhook{}, nil
	}

	// Parse JSON lines
	var webhooks []Webhook
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		var wh Webhook
		if err := json.Unmarshal([]byte(line), &wh); err != nil {
			continue
		}
		webhooks = append(webhooks, wh)
	}

	return webhooks, nil
}

// UpdateWebhook updates a webhook's URL
func (c *Client) UpdateWebhook(repo string, webhookID int, url, secret string) error {
	cmd := exec.Command("gh", "api", fmt.Sprintf("repos/%s/hooks/%d", repo, webhookID),
		"--method", "PATCH",
		"-f", fmt.Sprintf("config[url]=%s", url),
		"-f", "config[content_type]=json",
		"-f", fmt.Sprintf("config[secret]=%s", secret),
		"-F", "active=true")

	// Use administrator authentication, not bot token
	cmd.Env = cleanEnvForAdminAuth()

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("update webhook: %w, output: %s", err, string(output))
	}

	return nil
}

// CreateWebhook creates a new webhook
func (c *Client) CreateWebhook(repo, url, secret string) error {
	webhookURL := fmt.Sprintf("%s/%s/webhook", url, repo)

	cmd := exec.Command("gh", "api", fmt.Sprintf("repos/%s/hooks", repo),
		"--method", "POST",
		"-f", "name=web",
		"-f", fmt.Sprintf("config[url]=%s", webhookURL),
		"-f", "config[content_type]=json",
		"-f", fmt.Sprintf("config[secret]=%s", secret),
		"-f", "events[]=issues",
		"-f", "events[]=issue_comment",
		"-F", "active=true")

	// Use administrator authentication
	cmd.Env = cleanEnvForAdminAuth()

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create webhook: %w, output: %s", err, string(output))
	}

	return nil
}

// DeleteWebhook deletes a webhook
func (c *Client) DeleteWebhook(repo string, webhookID int) error {
	cmd := exec.Command("gh", "api", fmt.Sprintf("repos/%s/hooks/%d", repo, webhookID),
		"--method", "DELETE")

	// Use administrator authentication
	cmd.Env = cleanEnvForAdminAuth()

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("delete webhook: %w, output: %s", err, string(output))
	}

	return nil
}

// EnsureAdminScope ensures gh has admin:repo_hook scope
func (c *Client) EnsureAdminScope() error {
	scopes, err := c.getAuthScopes()
	if err != nil {
		return err
	}

	if strings.Contains(scopes, "admin:repo_hook") {
		return nil // Already has the scope
	}

	fmt.Println("Requesting admin:repo_hook scope...")
	return exec.Command("gh", "auth", "refresh", "-h", "github.com", "-s", "admin:repo_hook").Run()
}

// getAuthScopes returns current gh authentication scopes
func (c *Client) getAuthScopes() (string, error) {
	out, err := exec.Command("gh", "auth", "status").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("get auth status: %w", err)
	}
	return string(out), nil
}

// CheckPermission checks if a user has write or higher permission on a repo
func (c *Client) CheckPermission(repo, username string) (bool, error) {
	out, err := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/collaborators/%s/permission", repo, username),
		"--jq", ".permission").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("check permission: %s", strings.TrimSpace(string(out)))
	}

	perm := strings.TrimSpace(string(out))
	switch perm {
	case "admin", "maintain", "write":
		return true, nil
	default:
		return false, nil
	}
}

// GetCurrentRepo returns the current GitHub repo name
func (c *Client) GetCurrentRepo(dir string) (string, error) {
	cmd := exec.Command("gh", "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("get current repo: %s", strings.TrimSpace(string(out)))
	}

	repo := strings.TrimSpace(string(out))
	if repo == "" {
		return "", fmt.Errorf("could not detect GitHub repo")
	}

	return repo, nil
}

// cleanEnvForAdminAuth creates environment for admin authentication
// Removes BOT_GITHUB_TOKEN to use default gh auth
func cleanEnvForAdminAuth() []string {
	env := os.Environ()
	cleanEnv := make([]string, 0, len(env))

	for _, e := range env {
		// Skip bot tokens to use administrator's gh authentication
		if !strings.HasPrefix(e, "BOT_GITHUB_TOKEN=") &&
		   !strings.HasPrefix(e, "GITHUB_TOKEN=") {
			cleanEnv = append(cleanEnv, e)
		}
	}

	return cleanEnv
}
