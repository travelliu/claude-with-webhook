package tunnel

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
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
	baseDir string
	port    string
}

// NewManager creates a new tunnel manager
func NewManager(baseDir, port string) *Manager {
	return &Manager{
		baseDir: baseDir,
		port:    port,
	}
}

// EnsureStarted ensures the tunnel is running, starts it if needed
// If no tunnel is configured, auto-detects and starts the first available one
func (m *Manager) EnsureStarted() (string, error) {
	tunnelType, err := m.detectType()
	if err != nil {
		// No tunnel configured, try to auto-detect
		log.Println("No tunnel configured, auto-detecting...")
		tunnelType, err = DetectType()
		if err != nil {
			return "", fmt.Errorf("auto-detect tunnel: %w", err)
		}

		// Save detected type
		if err := m.SaveType(tunnelType); err != nil {
			log.Printf("Warning: could not save tunnel type: %v", err)
		}

		log.Printf("Auto-detected tunnel: %s", tunnelType)
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

// ensureTailscale ensures Tailscale Funnel is running
func (m *Manager) ensureTailscale() (string, error) {
	// Check if Funnel is already routing to our port
	out, err := exec.Command("tailscale", "funnel", "status").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("check funnel status: %w", err)
	}

	status := string(out)
	portPattern := "127.0.0.1:" + m.port
	localhostPattern := "localhost:" + m.port

	if strings.Contains(status, portPattern) || strings.Contains(status, localhostPattern) {
		log.Println("Tailscale Funnel already running")
		return getTailscaleURL()
	}

	log.Printf("Starting Tailscale Funnel on port %s...", m.port)
	if err := exec.Command("tailscale", "funnel", "--bg", m.port).Run(); err != nil {
		return "", fmt.Errorf("start funnel: %w", err)
	}

	return getTailscaleURL()
}

// ensureNgrok ensures ngrok is running
func (m *Manager) ensureNgrok() (string, error) {
	// Check if ngrok API is available
	if !m.isNgrokRunning() {
		log.Printf("Starting ngrok tunnel on port %s...", m.port)
		// Start ngrok in background
		cmd := exec.Command("ngrok", "http", m.port, "--log=stdout")
		if err := cmd.Start(); err != nil {
			return "", fmt.Errorf("start ngrok: %w", err)
		}

		// Wait for ngrok to be ready
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
		log.Println("Zrok already sharing")
		return url, nil
	}

	log.Printf("Starting zrok public share on port %s...", m.port)
	cmd := exec.Command("zrok", "share", "public",
		fmt.Sprintf("http://localhost:%s", m.port), "--headless")
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start zrok: %w", err)
	}

	// Wait for zrok to be ready
	time.Sleep(2 * time.Second)
	return getZrokURL()
}

// isNgrokRunning checks if ngrok is running
func (m *Manager) isNgrokRunning() bool {
	cmd := exec.Command("curl", "-s", "--max-time", "1",
		"http://127.0.0.1:4040/api/tunnels")
	out, err := cmd.Output()
	return err == nil && len(out) > 0
}

// waitForNgrok waits for ngrok to be ready
func (m *Manager) waitForNgrok() error {
	for i := 0; i < 20; i++ {
		if m.isNgrokRunning() {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("ngrok did not start within timeout")
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
	out, err := exec.Command("curl", "-s", "--max-time", "2",
		"http://127.0.0.1:4040/api/tunnels").Output()
	if err != nil {
		return "", fmt.Errorf("ngrok API not available: %w", err)
	}

	var response struct {
		Tunnels []struct {
			Proto     string `json:"proto"`
			PublicURL string `json:"public_url"`
		} `json:"tunnels"`
	}

	if err := json.Unmarshal(out, &response); err != nil {
		return "", fmt.Errorf("parse ngrok response: %w", err)
	}

	for _, tunnel := range response.Tunnels {
		if tunnel.Proto == "https" && tunnel.PublicURL != "" {
			return tunnel.PublicURL, nil
		}
	}

	return "", fmt.Errorf("no ngrok HTTPS tunnel found")
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
