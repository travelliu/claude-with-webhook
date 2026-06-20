package cmd

import (
	"path/filepath"
	"os"

	"github.com/spf13/cobra"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the webhook server daemon",
}

func init() {
	daemonCmd.AddCommand(startCmd)
	daemonCmd.AddCommand(stopCmd)
	daemonCmd.AddCommand(statusCmd)
	daemonCmd.AddCommand(restartCmd)
	rootCmd.AddCommand(daemonCmd)

	daemonCmd.PersistentFlags().String("base-dir", filepath.Join(os.Getenv("HOME"), ".claude-webhook"), "Base directory")
}
