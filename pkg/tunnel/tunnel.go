package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Type represents the tunnel type
type Type string

const (
	Tailscale Type = "tailscale"
	Ngrok     Type = "ngrok"
	Zrok      Type = "zrok"
)

// Manager handles tunnel operations
type Manager struct {
	baseDir    string
	port       string
	tunnelPort string                  // port ngrok/tailscale forwards to (defaults to port)
	getURLFunc func() (string, error) // overridable for testing
}

// NewManager creates a new tunnel manager
func NewManager(baseDir, port string) *Manager {
	tunnelPort := os.Getenv("TUNNEL_PORT")
	if tunnelPort == "" {
		tunnelPort = port
	}
	return &Manager{
		baseDir:    baseDir,
		port:       port,
		tunnelPort: tunnelPort,
	}
}

// EnsureStarted ensures the tunnel is running, starts it if needed
// If no tunnel is configured, auto-detects and starts the first available one
func (m *Manager) EnsureStarted() (string, error) {
	tunnelType, err := m.detectType()
	if err != nil {
		// No tunnel configured, try to auto-detect
		slog.Info("no tunnel configured, auto-detecting")
		tunnelType, err = DetectType()
		if err != nil {
			return "", fmt.Errorf("auto-detect tunnel: %w", err)
		}

		// Save detected type
		if err := m.SaveType(tunnelType); err != nil {
			slog.Warn("could not save tunnel type", "error", err)
		}

		slog.Info("auto-detected tunnel", "type", tunnelType)
	}

	switch tunnelType {
	case Tailscale:
		return m.ensureTailscale()
	case Ngrok:
		return m.ensureNgrok()
	case Zrok:
		return m.ensureZrok()
	default:
		return "", fmt.Errorf("unknown tunnel type: %s", tunnelType)
	}
}

// detectType detects the configured tunnel type
func (m *Manager) detectType() (Type, error) {
	tunnelFile := fmt.Sprintf("%s/.tunnel", m.baseDir)
	data, err := os.ReadFile(tunnelFile)
	if err != nil {
		return "", fmt.Errorf("read tunnel file: %w", err)
	}

	tType := strings.TrimSpace(string(data))
	switch tType {
	case "tailscale":
		return Tailscale, nil
	case "ngrok":
		return Ngrok, nil
	case "zrok":
		return Zrok, nil
	default:
		return "", fmt.Errorf("unknown tunnel type: %s", tType)
	}
}

// getURL returns the current tunnel URL, using the injectable function if set.
func (m *Manager) getURL() (string, error) {
	if m.getURLFunc != nil {
		return m.getURLFunc()
	}
	return m.GetURL()
}

// GetURL returns the current tunnel URL
func (m *Manager) GetURL() (string, error) {
	tunnelType, err := m.detectType()
	if err != nil {
		return "", err
	}

	switch tunnelType {
	case Tailscale:
		return getTailscaleURL()
	case Ngrok:
		return getNgrokURL()
	case Zrok:
		return getZrokURL()
	default:
		return "", fmt.Errorf("unknown tunnel type: %s", tunnelType)
	}
}

// WatchURL monitors the tunnel URL for changes. It polls GetURL at the given
// interval and sends the new URL on the returned channel whenever the URL
// differs from the previous value. The channel is closed when ctx is canceled.
func (m *Manager) WatchURL(ctx context.Context, interval time.Duration) <-chan string {
	ch := make(chan string, 1)
	go func() {
		defer close(ch)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		// Capture the initial URL so we only report actual changes.
		var lastURL string
		if url, err := m.getURL(); err == nil {
			lastURL = url
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				url, err := m.getURL()
				if err != nil {
					slog.Warn("tunnel URL check failed", "error", err)
					continue
				}
				if url != lastURL {
					slog.Info("tunnel URL changed", "old", lastURL, "new", url)
					lastURL = url
					// Non-blocking send: drop if the consumer is still busy.
					select {
					case ch <- url:
					default:
					}
				}
			}
		}
	}()
	return ch
}

// ensureTailscale ensures Tailscale Funnel is running
func (m *Manager) ensureTailscale() (string, error) {
	// Check if Funnel is already routing to our port
	out, err := exec.Command("tailscale", "funnel", "status").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("check funnel status: %w", err)
	}

	status := string(out)
	portPattern := "127.0.0.1:" + m.tunnelPort
	localhostPattern := "localhost:" + m.tunnelPort

	if strings.Contains(status, portPattern) || strings.Contains(status, localhostPattern) {
		slog.Info("tailscale funnel already running")
		return getTailscaleURL()
	}

	slog.Info("starting tailscale funnel", "tunnel_port", m.tunnelPort, "server_port", m.port)
	if err := exec.Command("tailscale", "funnel", "--bg", m.tunnelPort).Run(); err != nil {
		return "", fmt.Errorf("start funnel: %w", err)
	}

	return getTailscaleURL()
}

// ensureNgrok ensures ngrok is running
func (m *Manager) ensureNgrok() (string, error) {
	// Check if ngrok API is available
	if !m.isNgrokRunning() {
		slog.Info("starting ngrok tunnel", "tunnel_port", m.tunnelPort, "server_port", m.port)
		// Start ngrok detached (no stdout/stderr to avoid blocking terminal)
		cmd := exec.Command("ngrok", "http", m.tunnelPort, "--log=stdout")
		if err := cmd.Start(); err != nil {
			return "", fmt.Errorf("start ngrok: %w", err)
		}

		// Wait for ngrok to be ready (30 seconds)
		if err := m.waitForNgrok(); err != nil {
			return "", fmt.Errorf("wait for ngrok: %w", err)
		}
	}

	return getNgrokURL()
}

// ensureZrok ensures zrok is running
func (m *Manager) ensureZrok() (string, error) {
	// Check if zrok is already sharing
	url, err := getZrokURL()
	if err == nil && url != "" {
		slog.Info("zrok already sharing")
		return url, nil
	}

	slog.Info("starting zrok public share", "tunnel_port", m.tunnelPort, "server_port", m.port)
	cmd := exec.Command("zrok", "share", "public",
		fmt.Sprintf("http://localhost:%s", m.tunnelPort), "--headless")
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start zrok: %w", err)
	}

	// Wait for zrok to be ready
	time.Sleep(2 * time.Second)
	return getZrokURL()
}

// ngrokDefaultAPI is the default ngrok API address
const ngrokDefaultAPI = "http://127.0.0.1:4040/api/tunnels"

// ngrokAPIURL returns the ngrok API URL by reading its config file
func ngrokAPIURL() string {
	home, _ := os.UserHomeDir()
	configPaths := []string{
		filepath.Join(home, ".config", "ngrok", "ngrok.yml"),
		filepath.Join(home, ".ngrok2", "ngrok.yml"),
	}

	for _, path := range configPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		// Parse web_addr from YAML (simple extraction, no YAML dependency)
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "web_addr:") {
				addr := strings.TrimSpace(strings.TrimPrefix(line, "web_addr:"))
				addr = strings.Trim(addr, "\"'")
				if addr != "" {
					if !strings.HasPrefix(addr, "http") {
						addr = "http://" + addr
					}
					return addr + "/api/tunnels"
				}
			}
		}
	}

	return ngrokDefaultAPI
}

// isNgrokRunning checks if ngrok is running
func (m *Manager) isNgrokRunning() bool {
	cmd := exec.Command("curl", "-s", "--max-time", "1", ngrokAPIURL())
	out, err := cmd.Output()
	return err == nil && len(out) > 0
}

// waitForNgrok waits for ngrok to be ready (up to 30 seconds)
func (m *Manager) waitForNgrok() error {
	for i := 0; i < 60; i++ {
		if m.isNgrokRunning() {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("ngrok did not start within 30s")
}

// getTailscaleURL returns Tailscale Funnel URL
func getTailscaleURL() (string, error) {
	out, err := exec.Command("tailscale", "status", "--json").Output()
	if err != nil {
		return "", fmt.Errorf("get tailscale status: %w", err)
	}

	var ts struct {
		Self struct {
			DNSName string `json:"DNSName"`
		} `json:"Self"`
	}

	if err := json.Unmarshal(out, &ts); err != nil {
		return "", fmt.Errorf("parse tailscale status: %w", err)
	}

	hostname := strings.TrimSuffix(ts.Self.DNSName, ".")
	if hostname == "" {
		return "", fmt.Errorf("no tailscale hostname found")
	}

	return fmt.Sprintf("https://%s", hostname), nil
}

// getNgrokURL returns ngrok tunnel URL
func getNgrokURL() (string, error) {
	apiURL := ngrokAPIURL()
	out, err := exec.Command("curl", "-s", "--max-time", "2", apiURL).Output()
	if err != nil {
		return "", fmt.Errorf("ngrok API not available at %s: %w", apiURL, err)
	}

	slog.Debug("ngrok API response", "response", string(out))

	var response struct {
		Tunnels []struct {
			Proto     string `json:"proto"`
			PublicURL string `json:"public_url"`
		} `json:"tunnels"`
	}

	if err := json.Unmarshal(out, &response); err != nil {
		return "", fmt.Errorf("parse ngrok response: %w", err)
	}

	// Prefer HTTPS, fall back to HTTP
	var httpURL string
	for _, tunnel := range response.Tunnels {
		if tunnel.Proto == "https" && tunnel.PublicURL != "" {
			return tunnel.PublicURL, nil
		}
		if tunnel.Proto == "http" && tunnel.PublicURL != "" {
			httpURL = tunnel.PublicURL
		}
	}

	if httpURL != "" {
		slog.Warn("ngrok: no HTTPS tunnel, using HTTP", "url", httpURL)
		return httpURL, nil
	}

	return "", fmt.Errorf("no ngrok tunnel found (checked %s)", apiURL)
}

// getZrokURL returns zrok share URL
func getZrokURL() (string, error) {
	out, err := exec.Command("zrok", "overview", "--json").Output()
	if err != nil {
		return "", fmt.Errorf("get zrok overview: %w", err)
	}

	var shares []struct {
		ShareMode        string `json:"share_mode"`
		FrontendEndpoint string `json:"frontend_endpoint"`
	}

	if err := json.Unmarshal(out, &shares); err != nil {
		return "", fmt.Errorf("parse zrok overview: %w", err)
	}

	for _, share := range shares {
		if share.ShareMode == "public" && share.FrontendEndpoint != "" {
			return share.FrontendEndpoint, nil
		}
	}

	return "", fmt.Errorf("no zrok public share found")
}

// DetectType detects available tunnel tools
func DetectType() (Type, error) {
	// Check in order: tailscale -> ngrok -> zrok
	if commandExists("tailscale") {
		out, err := exec.Command("tailscale", "status", "--json").Output()
		if err == nil && len(out) > 0 {
			return Tailscale, nil
		}
	}

	if commandExists("ngrok") {
		return Ngrok, nil
	}

	if commandExists("zrok") {
		return Zrok, nil
	}

	return "", fmt.Errorf("no tunnel tool found (install tailscale, ngrok, or zrok)")
}

// SaveType saves the tunnel type to file
func (m *Manager) SaveType(tType Type) error {
	tunnelFile := fmt.Sprintf("%s/.tunnel", m.baseDir)
	return os.WriteFile(tunnelFile, []byte(string(tType)), 0644)
}

// commandExists checks if a command exists
func commandExists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}
