package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"claude-with-webhook/pkg/github"
	"claude-with-webhook/pkg/tunnel"
)

// Server represents the webhook server
type Server struct {
	config          *Config
	httpServer      *http.Server
	githubClient    *github.Client
	tunnelManager   *tunnel.Manager
	semaphore       chan struct{}
	deliveryCache   *DeliveryCache
}

// Config holds server configuration
type Config struct {
	WebhookSecret  string
	AllowedUsers   map[string]bool
	BotUsername    string
	BotGitHubToken string
	BotGitName     string
	BotGitEmail    string
	CommandPrefix  string
	Port           string
	BaseDir        string
	PublicURL      string // User-provided public URL (skip tunnel)

	reposMu sync.RWMutex
	repos   map[string]string
}

// DeliveryCache tracks webhook delivery IDs to prevent duplicates
type DeliveryCache struct {
	mu   sync.Mutex
	data map[string]time.Time
}

// New creates a new server instance
func New(cfg *Config) *Server {
	return &Server{
		config: cfg,
		githubClient: github.NewClient(),
		tunnelManager: tunnel.NewManager(cfg.BaseDir, cfg.Port),
		semaphore: make(chan struct{}, 3), // Default max concurrent
		deliveryCache: &DeliveryCache{
			data: make(map[string]time.Time),
		},
	}
}

// Start starts the webhook server
func (s *Server) Start() error {
	log.Printf("Starting webhook server on port %s", s.config.Port)

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
		log.Printf("Warning: tunnel check failed: %v", err)
	}

	// Check and update webhooks if needed
	if err := s.checkAndUpdateWebhooks(); err != nil {
		log.Printf("Warning: webhook check failed: %v", err)
	}

	// Start server
	go func() {
		log.Printf("Server listening on %s", s.httpServer.Addr)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Server error: %v", err)
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
		log.Printf("Using public URL: %s (tunnel skipped)", s.config.PublicURL)
		return nil
	}

	// Auto-detect and start tunnel
	url, err := s.tunnelManager.EnsureStarted()
	if err != nil {
		return err
	}
	log.Printf("Tunnel URL: %s", url)
	return nil
}

// checkAndUpdateWebhooks checks and updates webhooks if tunnel URL changed
func (s *Server) checkAndUpdateWebhooks() error {
	// Get current public URL
	var baseURL string
	if s.config.PublicURL != "" {
		baseURL = s.config.PublicURL
		log.Printf("Using configured public URL: %s", baseURL)
	} else {
		tunnelURL, err := s.tunnelManager.GetURL()
		if err != nil {
			return fmt.Errorf("get tunnel URL: %w", err)
		}
		baseURL = tunnelURL
		log.Printf("Current tunnel URL: %s", baseURL)
	}

	repos := s.config.GetAllRepos()
	for repo := range repos {
		if err := s.checkAndUpdateRepoWebhook(repo, baseURL); err != nil {
			log.Printf("[%s] webhook check failed: %v", repo, err)
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
		log.Printf("[%s] no webhook found", repo)
		return nil
	}

	expectedURL := fmt.Sprintf("%s/%s/webhook", tunnelURL, repo)
	wh := webhooks[0] // Take first webhook

	if wh.URL == expectedURL {
		log.Printf("[%s] webhook URL is correct: %s", repo, wh.URL)
		return nil
	}

	// Update webhook
	log.Printf("[%s] webhook URL mismatch - updating", repo)
	log.Printf("  Old: %s", wh.URL)
	log.Printf("  New: %s", expectedURL)

	if err := s.githubClient.UpdateWebhook(repo, wh.ID, expectedURL, s.config.WebhookSecret); err != nil {
		return err
	}

	log.Printf("[%s] webhook updated successfully", repo)
	return nil
}

// startBackgroundTasks starts background maintenance tasks
func (s *Server) startBackgroundTasks() {
	// Clean up old delivery IDs periodically
	go func() {
		for range time.Tick(10 * time.Minute) {
			s.deliveryCache.Clean(time.Now().Add(-1 * time.Hour))
		}
	}()

	// Reload repos.conf on SIGHUP
	s.setupSIGHUP()
}

// setupSIGHUP sets up SIGHUP handler for config reload
func (s *Server) setupSIGHUP() {
	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)
	go func() {
		for range sighup {
			log.Println("Received SIGHUP, reloading repos.conf...")
			s.config.ReloadRepos()
			// Re-check webhooks after reload
			if err := s.checkAndUpdateWebhooks(); err != nil {
				log.Printf("Warning: webhook check failed after reload: %v", err)
			}
		}
	}()
}

// waitForShutdown waits for shutdown signals
func (s *Server) waitForShutdown() {
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	sig := <-shutdown
	log.Printf("Received %s, shutting down...", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}

	log.Println("Server stopped")
}

// GetRepo returns the local path for a repo
func (c *Config) GetRepo(name string) (string, bool) {
	c.reposMu.RLock()
	defer c.reposMu.RUnlock()
	dir, ok := c.repos[name]
	return dir, ok
}

// GetAllRepos returns all registered repos
func (c *Config) GetAllRepos() map[string]string {
	c.reposMu.RLock()
	defer c.reposMu.RUnlock()
	result := make(map[string]string, len(c.repos))
	for k, v := range c.repos {
		result[k] = v
	}
	return result
}

// ReloadRepos reloads repos.conf
func (c *Config) ReloadRepos() {
	repos := loadRepos(c.BaseDir)
	c.reposMu.Lock()
	c.repos = repos
	c.reposMu.Unlock()
	for repo, dir := range repos {
		log.Printf("  %s → %s", repo, dir)
	}
}

// Clean removes old entries from delivery cache
func (d *DeliveryCache) Clean(cutoff time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for key, timestamp := range d.data {
		if timestamp.Before(cutoff) {
			delete(d.data, key)
		}
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

func (s *Server) webhookHandler(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement webhook handling logic
	// This will be migrated from main.go
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Webhook received (implementation pending)"))
}

// webhookCatchAllHandler handles webhook routes like /{owner}/{repo}/webhook
func (s *Server) webhookCatchAllHandler(w http.ResponseWriter, r *http.Request) {
	// For now, just handle root path
	if r.URL.Path == "/" {
		s.rootHandler(w, r)
		return
	}

	// TODO: Parse /{owner}/{repo}/webhook route
	// TODO: Implement webhook handling logic
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Webhook endpoint (implementation pending)"))
}

// loadRepos loads repos.conf from disk
func loadRepos(baseDir string) map[string]string {
	reposFile := fmt.Sprintf("%s/repos.conf", baseDir)
	repos := make(map[string]string)

	data, err := os.ReadFile(reposFile)
	if err != nil {
		return repos
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			repo := strings.TrimSpace(parts[0])
			path := strings.TrimSpace(parts[1])
			if repo != "" && path != "" {
				repos[repo] = path
			}
		}
	}

	return repos
}

// NewConfig creates a new config from environment variables
func NewConfig(baseDir string) (*Config, error) {
	// Load .env file if exists
	envFile := fmt.Sprintf("%s/.env", baseDir)
	if err := loadEnvFile(envFile); err != nil {
		log.Printf("Warning: could not load .env file: %v", err)
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
