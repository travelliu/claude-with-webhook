package cmd

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the webhook server",
	Long:  `Start the Claude webhook server to listen for GitHub issue/webhook events.`,
	Run:   runStart,
}

func init() {
	startCmd.Flags().StringP("port", "p", "", "Server port (overrides .env PORT)")
	startCmd.Flags().IntP("max-concurrent", "j", 0, "Max concurrent jobs (overrides .env MAX_CONCURRENT)")
}

func addStartCommand() {
	rootCmd.AddCommand(startCmd)
}

func runStart(cmd *cobra.Command, args []string) {
	baseDir := GetBaseDir()
	configFile := GetConfigFile()

	// Load configuration
	cfg := loadServerConfig(baseDir, configFile)

	// Override with command-line flags
	if port, _ := cmd.Flags().GetString("port"); port != "" {
		cfg.Port = port
	}
	if maxConcurrent, _ := cmd.Flags().GetInt("max-concurrent"); maxConcurrent > 0 {
		// Set semaphore size (will be used in server)
		cmd.Flags().Set("max-concurrent", fmt.Sprintf("%d", maxConcurrent))
	}

	// Start the server
	startServer(cfg)
}

// TODO: Move server startup logic from main.go to here
func startServer(cfg interface{}) {
	log.Printf("Claude Webhook Server %s (built %s)", Version, BuildTime)
	log.Printf("Starting server on port %s", cfg.(*Config).Port)
	log.Printf("Base directory: %s", GetBaseDir())

	// Signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// TODO: Start HTTP server here
	// This is a placeholder - the actual server logic from main.go needs to be moved here

	log.Println("Server started. Press Ctrl+C to stop.")

	<-sigChan
	log.Println("Shutting down server...")
}

// Temporary Config struct until we move the full implementation
type Config struct {
	Port    string
	BaseDir string
}

func loadServerConfig(baseDir, configFile string) *Config {
	// Placeholder for config loading
	// TODO: Move loadConfig() from main.go here
	return &Config{
		Port:    "8080",
		BaseDir: baseDir,
	}
}

// TODO: Move all server-related functions from main.go:
// - handleWebhook
// - handleIssueOpened
// - handleIssueComment
// - runPlan
// - handleApprove
// - etc.
