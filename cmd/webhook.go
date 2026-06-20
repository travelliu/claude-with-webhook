package cmd

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

// getCurrentTunnelURL detects the current tunnel URL based on tunnel type
func getCurrentTunnelURL(baseDir string) (string, error) {
	tunnelFile := fmt.Sprintf("%s/.tunnel", baseDir)

	tunnelType, err := os.ReadFile(tunnelFile)
	if err != nil {
		return "", fmt.Errorf("no tunnel configured")
	}

	switch strings.TrimSpace(string(tunnelType)) {
	case "tailscale":
		return getTailscaleURL()
	case "ngrok":
		return getNgrokURL()
	case "zrok":
		return getZrokURL()
	default:
		return "", fmt.Errorf("unknown tunnel type: %s", tunnelType)
	}
}

// getTailscaleURL returns the current Tailscale Funnel URL
func getTailscaleURL() (string, error) {
	out, err := exec.Command("tailscale", "status", "--json").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get tailscale status: %w", err)
	}

	var ts struct {
		Self struct {
			DNSName string `json:"DNSName"`
		} `json:"Self"`
	}

	if err := json.Unmarshal(out, &ts); err != nil {
		return "", fmt.Errorf("failed to parse tailscale status: %w", err)
	}

	hostname := strings.TrimSuffix(ts.Self.DNSName, ".")
	if hostname == "" {
		return "", fmt.Errorf("no tailscale hostname found")
	}

	return fmt.Sprintf("https://%s", hostname), nil
}

// getNgrokURL returns the current ngrok tunnel URL
func getNgrokURL() (string, error) {
	// Try to get ngrok tunnels API
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
		return "", fmt.Errorf("failed to parse ngrok response: %w", err)
	}

	// Find HTTPS tunnel
	for _, tunnel := range response.Tunnels {
		if tunnel.Proto == "https" && tunnel.PublicURL != "" {
			return tunnel.PublicURL, nil
		}
	}

	return "", fmt.Errorf("no ngrok HTTPS tunnel found")
}

// getZrokURL returns the current zrok share URL
func getZrokURL() (string, error) {
	out, err := exec.Command("zrok", "overview", "--json").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get zrok overview: %w", err)
	}

	var shares []struct {
		ShareMode        string `json:"share_mode"`
		FrontendEndpoint string `json:"frontend_endpoint"`
	}

	if err := json.Unmarshal(out, &shares); err != nil {
		return "", fmt.Errorf("failed to parse zrok overview: %w", err)
	}

	// Find public share
	for _, share := range shares {
		if share.ShareMode == "public" && share.FrontendEndpoint != "" {
			return share.FrontendEndpoint, nil
		}
	}

	return "", fmt.Errorf("no zrok public share found")
}

// CheckAndUpdateWebhooks checks all registered repos and updates webhooks if URL changed
func CheckAndUpdateWebhooks(cfg *Config) error {
	currentURL, err := getCurrentTunnelURL(cfg.BaseDir)
	if err != nil {
		log.Printf("Warning: could not detect tunnel URL: %v", err)
		return nil // Don't fail startup, just log warning
	}

	log.Printf("Current tunnel URL: %s", currentURL)

	repos := cfg.AllRepos()
	if len(repos) == 0 {
		log.Println("No registered repos to check")
		return nil
	}

	updatedCount := 0
	for repo := range repos {
		if err := checkAndUpdateRepoWebhook(repo, currentURL, cfg.WebhookSecret); err != nil {
			log.Printf("[%s] webhook check failed: %v", repo, err)
		} else {
			updatedCount++
		}
	}

	if updatedCount > 0 {
		log.Printf("Checked/updated %d repo webhook(s)", updatedCount)
	}

	return nil
}

// checkAndUpdateRepoWebhook checks a single repo's webhook and updates if needed
func checkAndUpdateRepoWebhook(repo, currentURL, secret string) error {
	// Get all webhooks for the repo
	out, err := exec.Command("gh", "api", fmt.Sprintf("repos/%s/hooks", repo),
		"--jq", ".[] | select(.config.url | endswith(\"/webhook\")) | {id: .id, url: .config.url}").Output()
	if err != nil {
		return fmt.Errorf("failed to get webhooks: %w", err)
	}

	if len(out) == 0 {
		return fmt.Errorf("no webhook found")
	}

	// Parse webhook info
	var existingWebhook struct {
		ID  int    `json:"id"`
		URL string `json:"url"`
	}

	if err := json.Unmarshal(out, &existingWebhook); err != nil {
		return fmt.Errorf("failed to parse webhook: %w", err)
	}

	expectedURL := fmt.Sprintf("%s/%s/webhook", currentURL, repo)

	// Check if URL matches
	if existingWebhook.URL == expectedURL {
		log.Printf("[%s] webhook URL is correct: %s", repo, existingWebhook.URL)
		return nil
	}

	// URL mismatch, need to update
	log.Printf("[%s] webhook URL mismatch - updating", repo)
	log.Printf("  Old: %s", existingWebhook.URL)
	log.Printf("  New: %s", expectedURL)

	// Update webhook
	updateCmd := exec.Command("gh", "api", fmt.Sprintf("repos/%s/hooks/%d", repo, existingWebhook.ID),
		"--method", "PATCH",
		"-f", fmt.Sprintf("config[url]=%s", expectedURL),
		"-f", "config[content_type]=json",
		"-f", fmt.Sprintf("config[secret]=%s", secret),
		"-F", "active=true")

	if output, err := updateCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to update webhook: %w, output: %s", err, string(output))
	}

	log.Printf("[%s] webhook updated successfully", repo)
	return nil
}
