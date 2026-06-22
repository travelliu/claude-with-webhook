package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"claude-with-webhook/pkg/agent"
	"claude-with-webhook/pkg/github"
	pkglog "claude-with-webhook/pkg/logger"
	"claude-with-webhook/pkg/tunnel"

	"gopkg.in/yaml.v3"
)

// Server represents the webhook server
type Server struct {
	config          *Config
	promptManager   *PromptManager
	httpServer      *http.Server
	githubClient    *github.Client
	tunnelManager   *tunnel.Manager
	agentRegistry   *agent.ProviderRegistry
	bots            []BotConfig
	semaphore       chan struct{}
	deliveryCache   sync.Map // X-GitHub-Delivery UUID -> time.Time (dedup)
	issueMu         sync.Map // per-issue mutex keyed by "repo#number"
	log             *slog.Logger
}

// Config holds server configuration
// RepoConfig holds per-repo configuration.
type RepoConfig struct {
	Dir          string   `yaml:"dir"`
	DefaultBot   string   `yaml:"default_bot,omitempty"`    // bot name when no @mention
	AllowedUsers []string `yaml:"allowed_users,omitempty"`
	WebhookToken string   `yaml:"webhook_token,omitempty"` // token with admin:repo_hook scope
	AutoPlan     *bool    `yaml:"auto_plan,omitempty"`      // auto-trigger plan on issue open (nil/false=disabled, true=enabled)
}

type Config struct {
	WebhookSecret  string
	AllowedUsers   map[string]bool // global fallback
	BotUsername    string          // legacy single-bot env var
	BotGitHubToken string          // legacy single-bot env var
	BotGitName     string          // legacy single-bot env var
	BotGitEmail    string          // legacy single-bot env var
	CommandPrefix  string          // legacy single-bot env var
	Port           string
	BaseDir        string
	PublicURL      string // User-provided public URL (skip tunnel)

	reposMu sync.RWMutex
	repos   map[string]RepoConfig
}

// New creates a new server instance
func New(cfg *Config) *Server {
	s := &Server{
		config:        cfg,
		promptManager: NewPromptManager(cfg.BaseDir),
		log:           pkglog.New("server"),
		githubClient:  github.NewClient(),
		tunnelManager: tunnel.NewManager(cfg.BaseDir, cfg.Port),
		agentRegistry: agent.DefaultRegistry(),
		semaphore:     make(chan struct{}, 3),
	}
	s.loadBots()
	return s
}

// loadBots loads bots from bots.yaml, falling back to env vars for backward compat.
func (s *Server) loadBots() {
	botsFile, err := LoadBots(s.config.BaseDir)
	if err != nil {
		s.log.Warn("failed to load bots.yaml", "error", err)
	}
	if len(botsFile.Bots) > 0 {
		s.bots = botsFile.Bots
		s.log.Info("loaded bots from bots.yaml", "count", len(s.bots))
		return
	}

	// Backward compat: create default bot from env vars
	if s.config.BotUsername != "" && s.config.BotGitHubToken != "" {
		prefix := s.config.CommandPrefix
		if prefix == "" {
			prefix = "@claude"
		}
		s.bots = []BotConfig{
			{
				Name:     "default",
				Username: s.config.BotUsername,
				Token:    s.config.BotGitHubToken,
				GitName:  s.config.BotGitName,
				GitEmail: s.config.BotGitEmail,
				Prefix:   prefix,
				Agent:    "claude",
			},
		}
		s.log.Info("created default bot from env vars", "prefix", prefix)
	}
}

// Start starts the webhook server
func (s *Server) Start() error {
	s.log.Info("starting webhook server", "port", s.config.Port)

	// Setup HTTP routes
	mux := http.NewServeMux()

	// Health check endpoint
	mux.HandleFunc("/health", s.healthCheckHandler)

	// Root endpoint (catch-all for now, will be refined)
	mux.HandleFunc("/", s.webhookCatchAllHandler)

	// Create HTTP server
	s.httpServer = &http.Server{
		Addr:    ":" + s.config.Port,
		Handler: mux,
	}

	// Start background tasks
	s.startBackgroundTasks()

	// Ensure tunnel is running
	if err := s.ensureTunnel(); err != nil {
		s.log.Warn("tunnel check failed", "error", err)
	}

	// Check and update webhooks if needed
	if err := s.checkAndUpdateWebhooks(); err != nil {
		s.log.Warn("webhook check failed", "error", err)
	}

	// Start server
	go func() {
		s.log.Info("server listening", "addr", s.httpServer.Addr)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.log.Error("server error", "error", err)
		}
	}()

	// Wait for shutdown signal
	s.waitForShutdown()

	return nil
}

// ensureTunnel ensures the tunnel is running
// If user provided PUBLIC_URL, skip tunnel and use that instead
func (s *Server) ensureTunnel() error {
	// Check if user provided public URL
	if s.config.PublicURL != "" {
		s.log.Info("using public URL, tunnel skipped", "url", s.config.PublicURL)
		return nil
	}

	// Auto-detect and start tunnel
	url, err := s.tunnelManager.EnsureStarted()
	if err != nil {
		return err
	}
	s.log.Info("tunnel URL", "url", url)
	return nil
}

// checkAndUpdateWebhooks checks and updates webhooks if tunnel URL changed
func (s *Server) checkAndUpdateWebhooks() error {
	// Get current public URL
	var baseURL string
	if s.config.PublicURL != "" {
		baseURL = s.config.PublicURL
		s.log.Info("using configured public URL", "url", baseURL)
	} else {
		tunnelURL, err := s.tunnelManager.GetURL()
		if err != nil {
			return fmt.Errorf("get tunnel URL: %w", err)
		}
		baseURL = tunnelURL
		s.log.Info("current tunnel URL", "url", baseURL)
	}

	repos := s.config.GetAllRepos()
	for repo := range repos {
		if err := s.checkAndUpdateRepoWebhook(repo, baseURL); err != nil {
			s.log.Error("webhook check failed", "repo", repo, "error", err)
		}
	}

	return nil
}

// checkAndUpdateRepoWebhook checks a single repo's webhook
func (s *Server) checkAndUpdateRepoWebhook(repo, tunnelURL string) error {
	webhooks, err := s.githubClient.GetWebhooks(repo)
	if err != nil {
		return err
	}

	if len(webhooks) == 0 {
		s.log.Warn("no webhook found", "repo", repo)
		return nil
	}

	expectedURL := fmt.Sprintf("%s/%s/webhook", tunnelURL, repo)
	wh := webhooks[0] // Take first webhook

	if wh.URL == expectedURL {
		s.log.Info("webhook URL is correct", "repo", repo, "url", wh.URL)
		return nil
	}

	// Update webhook
	s.log.Warn("webhook URL mismatch, updating", "repo", repo, "old", wh.URL, "new", expectedURL)

	if err := s.githubClient.UpdateWebhook(repo, wh.ID, expectedURL, s.config.WebhookSecret); err != nil {
		return err
	}

	s.log.Info("webhook updated successfully", "repo", repo)
	return nil
}

// startBackgroundTasks starts background maintenance tasks
func (s *Server) startBackgroundTasks() {
	// Reload repos.conf on SIGHUP
	s.setupSIGHUP()
}

// setupSIGHUP sets up SIGHUP handler for config reload
func (s *Server) setupSIGHUP() {
	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)
	go func() {
		for range sighup {
			s.log.Info("received SIGHUP, reloading repos.conf")
			s.config.ReloadRepos()
			// Re-check webhooks after reload
			if err := s.checkAndUpdateWebhooks(); err != nil {
				s.log.Warn("webhook check failed after reload", "error", err)
			}
		}
	}()
}

// waitForShutdown waits for shutdown signals
func (s *Server) waitForShutdown() {
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	sig := <-shutdown
	s.log.Info("received signal, shutting down", "signal", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		s.log.Error("server shutdown error", "error", err)
	}

	s.log.Info("server stopped")
}

// GetRepo returns the local path for a repo
func (c *Config) GetRepo(name string) (string, bool) {
	c.reposMu.RLock()
	defer c.reposMu.RUnlock()
	rc, ok := c.repos[name]
	return rc.Dir, ok
}

// GetRepoConfig returns the full config for a repo
func (c *Config) GetRepoConfig(name string) (RepoConfig, bool) {
	c.reposMu.RLock()
	defer c.reposMu.RUnlock()
	rc, ok := c.repos[name]
	return rc, ok
}

// GetAllRepos returns all registered repo dirs
func (c *Config) GetAllRepos() map[string]string {
	c.reposMu.RLock()
	defer c.reposMu.RUnlock()
	result := make(map[string]string, len(c.repos))
	for k, v := range c.repos {
		result[k] = v.Dir
	}
	return result
}

// ReloadRepos reloads repos.yaml
func (c *Config) ReloadRepos() {
	repos := loadRepos(c.BaseDir)
	c.reposMu.Lock()
	c.repos = repos
	c.reposMu.Unlock()
	for repo, rc := range repos {
		slog.Info("repo loaded", "repo", repo, "dir", rc.Dir)
	}
}

// HTTP Handlers
func (s *Server) healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) rootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, "Claude Webhook Server running\n")
	fmt.Fprintf(w, "Port: %s\n", s.config.Port)
}

// webhookCatchAllHandler handles webhook routes like /{owner}/{repo}/webhook
func (s *Server) webhookCatchAllHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		s.rootHandler(w, r)
		return
	}

	// Parse /{owner}/{repo}/webhook route
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) == 3 && parts[2] == "webhook" {
		repo := parts[0] + "/" + parts[1]
		s.handleWebhook(w, r, repo)
		return
	}

	http.NotFound(w, r)
}

// reposFileYAML is the top-level structure of repos.yaml.
type reposFileYAML struct {
	Repos map[string]RepoConfig `yaml:"repos"`
}

// loadRepos loads repo config from repos.yaml, falling back to repos.conf.
func loadRepos(baseDir string) map[string]RepoConfig {
	// Try repos.yaml first
	yamlPath := filepath.Join(baseDir, "repos.yaml")
	if data, err := os.ReadFile(yamlPath); err == nil {
		var rf reposFileYAML
		if err := yaml.Unmarshal(data, &rf); err == nil && len(rf.Repos) > 0 {
			return rf.Repos
		}
	}

	// Fallback to repos.conf (legacy flat format)
	confPath := filepath.Join(baseDir, "repos.conf")
	repos := make(map[string]RepoConfig)
	data, err := os.ReadFile(confPath)
	if err != nil {
		return repos
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			repo := strings.TrimSpace(parts[0])
			dir := strings.TrimSpace(parts[1])
			if repo != "" && dir != "" {
				repos[repo] = RepoConfig{Dir: dir}
			}
		}
	}
	return repos
}

// LoadRepos is the exported version of loadRepos for use by CLI commands.
func LoadRepos(baseDir string) (map[string]RepoConfig, error) {
	return loadRepos(baseDir), nil
}

// SaveRepos writes repos.yaml to the base directory.
func SaveRepos(baseDir string, repos map[string]RepoConfig) error {
	path := filepath.Join(baseDir, "repos.yaml")
	out := struct {
		Repos map[string]RepoConfig `yaml:"repos"`
	}{Repos: repos}
	data, err := yaml.Marshal(out)
	if err != nil {
		return fmt.Errorf("marshal repos.yaml: %w", err)
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return fmt.Errorf("create base dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write repos.yaml: %w", err)
	}
	return nil
}

// NewConfig creates a new config from environment variables
func NewConfig(baseDir string) (*Config, error) {
	// Load .env file if exists
	envFile := fmt.Sprintf("%s/.env", baseDir)
	if err := loadEnvFile(envFile); err != nil {
		slog.Warn("could not load .env file", "error", err)
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

	commandPrefix := os.Getenv("COMMAND_PREFIX")
	if commandPrefix == "" {
		commandPrefix = "@claude"
	}

	publicURL := os.Getenv("PUBLIC_URL") // User-provided public URL

	repos := loadRepos(baseDir)

	return &Config{
		WebhookSecret:   secret,
		AllowedUsers:    allowed,
		BotUsername:     os.Getenv("BOT_USERNAME"),
		BotGitHubToken:  os.Getenv("BOT_GITHUB_TOKEN"),
		BotGitName:      os.Getenv("BOT_GIT_NAME"),
		BotGitEmail:     os.Getenv("BOT_GIT_EMAIL"),
		CommandPrefix:   commandPrefix,
		Port:            port,
		BaseDir:         baseDir,
		PublicURL:       publicURL,
		repos:           repos,
	}, nil
}

// loadEnvFile loads environment variables from .env file
func loadEnvFile(envFile string) error {
	data, err := os.ReadFile(envFile)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			if key != "" && !strings.HasPrefix(key, "#") {
				os.Setenv(key, val)
			}
		}
	}

	return nil
}
