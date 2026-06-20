package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	// Build-time variables
	Version   = "dev"
	BuildTime = "unknown"
)

var rootCmd = &cobra.Command{
	Use:   "claude-webhook-server",
	Short: "Claude webhook automation server",
	Long: `Claude webhook server that listens for GitHub issue/webhook events
and automates code planning, implementation, and PR creation.`,
	Version: Version,
}

// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Get default base directory
	baseDir := getDefaultBaseDir()

	// Persistent flags (available to all subcommands)
	rootCmd.PersistentFlags().StringP("config", "c", filepath.Join(baseDir, ".env"), "Config file path")
	rootCmd.PersistentFlags().StringP("base-dir", "b", baseDir, "Base directory for server files")
}

func getDefaultBaseDir() string {
	// Check if CLAUDE_WEBHOOK_DIR env var is set
	if dir := os.Getenv("CLAUDE_WEBHOOK_DIR"); dir != "" {
		return dir
	}
	// Default to ~/.claude-webhook
	homeDir := os.Getenv("HOME")
	if homeDir == "" {
		homeDir = os.Getenv("USERPROFILE")
	}
	return filepath.Join(homeDir, ".claude-webhook")
}

// GetBaseDir returns the base directory from flags or env
func GetBaseDir() string {
	dir, _ := rootCmd.PersistentFlags().GetString("base-dir")
	return dir
}

// GetConfigFile returns the config file path from flags
func GetConfigFile() string {
	config, _ := rootCmd.PersistentFlags().GetString("config")
	return config
}

// checkError is a helper for error handling
func checkError(err error, message string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %s: %v\n", message, err)
		os.Exit(1)
	}
}
