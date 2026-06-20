package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Config holds the server configuration
type Config struct {
	WebhookSecret  string
	AllowedUsers   map[string]bool
	BotUsername    string   // GitHub username the bot posts as; its own comments are ignored
	BotGitHubToken string   // GitHub token for gh CLI operations (optional, defaults to gh auth)
	BotGitName     string   // Git author name for commits (optional)
	BotGitEmail    string   // Git author email for commits (optional)
	CommandPrefix  string   // Command prefix for triggering bot actions (default: @claude)
	Port           string
	BaseDir        string // directory where server lives (~/.claude-webhook)

	reposMu sync.RWMutex
	repos   map[string]string // "owner/repo" → local path
}

// GetRepo returns the local path for a repo, safe for concurrent access.
func (c *Config) GetRepo(name string) (string, bool) {
	c.reposMu.RLock()
	defer c.reposMu.RUnlock()
	dir, ok := c.repos[name]
	return dir, ok
}

// ReloadRepos re-reads repos.conf from disk.
func (c *Config) ReloadRepos() {
	repos := loadRepos(filepath.Join(c.BaseDir, "repos.conf"))
	c.reposMu.Lock()
	c.repos = repos
	c.reposMu.Unlock()
	for repo, dir := range repos {
		fmt.Printf("  %s → %s\n", repo, dir)
	}
}

// LoadConfig loads the server configuration from the given file path
func LoadConfig(configFile string) (*Config, error) {
	// Get base directory from config file path
	baseDir := filepath.Dir(configFile)

	// Load .env file
	if err := loadDotEnv(configFile); err != nil {
		return nil, fmt.Errorf("load .env file: %w", err)
	}

	secret := os.Getenv("GITHUB_WEBHOOK_SECRET")
	if secret == "" {
		return nil, fmt.Errorf("GITHUB_WEBHOOK_SECRET is required")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	allowed := make(map[string]bool)
	for _, u := range strings.Split(os.Getenv("ALLOWED_USERS"), ",") {
		u = strings.TrimSpace(u)
		if u != "" {
			allowed[u] = true
		}
	}

	repos := loadRepos(filepath.Join(baseDir, "repos.conf"))

	// Set default command prefix if not configured
	commandPrefix := os.Getenv("COMMAND_PREFIX")
	if commandPrefix == "" {
		commandPrefix = "@claude"
	}

	return &Config{
		WebhookSecret:   secret,
		AllowedUsers:    allowed,
		BotUsername:     os.Getenv("BOT_USERNAME"),
		BotGitHubToken:  os.Getenv("BOT_GITHUB_TOKEN"),
		BotGitName:      os.Getenv("BOT_GIT_NAME"),
		BotGitEmail:     os.Getenv("BOT_GIT_EMAIL"),
		CommandPrefix:   commandPrefix,
		Port:            port,
		repos:           repos,
		BaseDir:         baseDir,
	}, nil
}

// loadRepos parses repos.conf into a map.
func loadRepos(confPath string) map[string]string {
	repos := make(map[string]string)
	f, err := os.Open(confPath)
	if err != nil {
		return repos
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		repo := strings.TrimSpace(parts[0])
		dir := strings.TrimSpace(parts[1])
		if repo != "" && dir != "" {
			repos[repo] = dir
		}
	}
	return repos
}

// loadDotEnv loads KEY=value pairs from a file into environment variables.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		// .env is optional for now
		return nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			if key != "" {
				os.Setenv(key, val)
			}
		}
	}
	return scanner.Err()
}
