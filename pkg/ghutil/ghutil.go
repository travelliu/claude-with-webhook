package ghutil

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Account represents a logged-in GitHub account from `gh auth status`.
type Account struct {
	Username string
	Instance string // e.g. "github.com"
	Active   bool
	Scopes   string
}

// ghAuthStatusJSON is the JSON structure returned by `gh auth status --json hosts`.
type ghAuthStatusJSON struct {
	Hosts map[string][]ghAccountEntry `json:"hosts"`
}

type ghAccountEntry struct {
	State  string `json:"state"`
	Active bool   `json:"active"`
	Host   string `json:"host"`
	Login  string `json:"login"`
	Scopes string `json:"scopes"`
}

// AuthStatus uses `gh auth status --json` to extract logged-in accounts.
func AuthStatus() ([]Account, error) {
	out, err := exec.Command("gh", "auth", "status", "--json", "hosts").Output()
	if err != nil {
		return nil, fmt.Errorf("gh auth status: %w", err)
	}

	var raw ghAuthStatusJSON
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse gh auth status: %w", err)
	}

	var accounts []Account
	for host, entries := range raw.Hosts {
		for _, e := range entries {
			if e.State != "success" {
				continue
			}
			accounts = append(accounts, Account{
				Username: e.Login,
				Instance: host,
				Active:   e.Active,
				Scopes:   e.Scopes,
			})
		}
	}
	return accounts, nil
}

// GetToken retrieves the auth token for a given user via `gh auth token`.
func GetToken(username string) (string, error) {
	args := []string{"auth", "token"}
	if username != "" {
		args = append(args, "-u", username)
	}
	out, err := exec.Command("gh", args...).Output()
	if err != nil {
		return "", fmt.Errorf("gh auth token: %w", err)
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", fmt.Errorf("no token found for user %q", username)
	}
	return token, nil
}

// RepoInfo holds information about the current GitHub repository.
type RepoInfo struct {
	NameWithOwner string // e.g. "owner/repo"
	CloneURL      string
	DefaultBranch string
}

// GetCurrentRepo returns info about the repo in the current directory.
func GetCurrentRepo() (*RepoInfo, error) {
	return GetCurrentRepoWithToken("")
}

// GetCurrentRepoWithToken returns repo info using a specific token.
func GetCurrentRepoWithToken(token string) (*RepoInfo, error) {
	cmd := ghWithToken(token, "repo", "view", "--json", "nameWithOwner,url,defaultBranchRef")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh repo view: %w", err)
	}
	var raw struct {
		NameWithOwner    string `json:"nameWithOwner"`
		URL              string `json:"url"`
		DefaultBranchRef struct {
			Name string `json:"name"`
		} `json:"defaultBranchRef"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse repo info: %w", err)
	}
	return &RepoInfo{
		NameWithOwner: raw.NameWithOwner,
		CloneURL:      raw.URL,
		DefaultBranch: raw.DefaultBranchRef.Name,
	}, nil
}

// WebhookConfig holds webhook creation/update parameters.
type WebhookConfig struct {
	URL    string
	Secret string
	Token  string // GitHub token for API calls (uses bot's account)
}

// EnsureWebhook creates or updates a GitHub webhook for the given repo.
// Uses the bot's token (via GH_TOKEN env) to authenticate.
func EnsureWebhook(repo string, cfg WebhookConfig) error {
	jqFilter := fmt.Sprintf(`.[] | select(.config.url == "%s") | .id`, cfg.URL)
	listCmd := ghWithToken(cfg.Token, "api", fmt.Sprintf("repos/%s/hooks", repo), "--jq", jqFilter)
	out, err := listCmd.Output()
	if err != nil {
		return fmt.Errorf("list hooks: %w", err)
	}

	existingID := strings.TrimSpace(string(out))

	if existingID != "" {
		return updateWebhook(repo, existingID, cfg)
	}
	return createWebhook(repo, cfg)
}

func createWebhook(repo string, cfg WebhookConfig) error {
	args := []string{
		"api", fmt.Sprintf("repos/%s/hooks", repo), "--method", "POST",
		"-f", "name=web",
		"-f", "config[url]=" + cfg.URL,
		"-f", "config[content_type]=json",
		"-f", "config[secret]=" + cfg.Secret,
		"-f", "events[]=issues",
		"-f", "events[]=issue_comment",
		"-F", "active=true",
	}
	if err := runGHWithToken(cfg.Token, args); err != nil {
		return fmt.Errorf("create webhook: %w", err)
	}
	return nil
}

func updateWebhook(repo, hookID string, cfg WebhookConfig) error {
	args := []string{
		"api", fmt.Sprintf("repos/%s/hooks/%s", repo, hookID), "--method", "PATCH",
		"-f", "config[url]=" + cfg.URL,
		"-f", "config[content_type]=json",
		"-f", "config[secret]=" + cfg.Secret,
		"-f", "events[]=issues",
		"-f", "events[]=issue_comment",
		"-F", "active=true",
	}
	if err := runGHWithToken(cfg.Token, args); err != nil {
		return fmt.Errorf("update webhook: %w", err)
	}
	return nil
}

// CheckScope checks if the active gh account has the admin:repo_hook scope.
func CheckScope() (bool, error) {
	accounts, err := AuthStatus()
	if err != nil {
		return false, err
	}
	for _, a := range accounts {
		if a.Active {
			return strings.Contains(a.Scopes, "admin:repo_hook"), nil
		}
	}
	return false, nil
}

// RefreshScope requests the admin:repo_hook scope via gh auth refresh.
func RefreshScope() error {
	return runGH([]string{"auth", "refresh", "-h", "github.com", "-s", "admin:repo_hook"})
}

func runGH(args []string) error {
	return runGHWithToken("", args)
}

func runGHWithToken(token string, args []string) error {
	cmd := ghWithToken(token, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh %s: %s", strings.Join(args[:2], " "), strings.TrimSpace(string(out)))
	}
	return nil
}

// ghWithToken creates an exec.Cmd for gh with optional GH_TOKEN env.
func ghWithToken(token string, args ...string) *exec.Cmd {
	cmd := exec.Command("gh", args...)
	if token != "" {
		cmd.Env = append(os.Environ(), "GH_TOKEN="+token)
	}
	return cmd
}
