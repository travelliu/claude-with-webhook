package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the webhook server",
	Long:  `Restart the running webhook server daemon.`,
	RunE:  runRestart,
}

func init() {
	addRestartCommand()
	restartCmd.Flags().StringP("port", "p", "", "Server port (overrides .env PORT)")
	restartCmd.Flags().IntP("max-concurrent", "j", 0, "Max concurrent jobs (overrides .env MAX_CONCURRENT)")
}

func addRestartCommand() {
	rootCmd.AddCommand(restartCmd)
}

func runRestart(cmd *cobra.Command, args []string) error {
	fmt.Println("Restarting server...")

	// First, stop the server if running
	profile := "default" // TODO: support profiles
	pidPath := pidPathForProfile(profile)

	if _, err := readPIDFile(pidPath); err == nil {
		// Server is running, stop it
		if err := runStop(cmd, args); err != nil {
			// Server wasn't running or stop failed, continue with start
			fmt.Fprintf(os.Stderr, "Note: %v\n", err)
		}
	}

	// Then start it again
	if err := runStartBackground(cmd); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	return nil
}
