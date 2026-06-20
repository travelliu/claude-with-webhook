package cmd

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the webhook server",
	Long:  `Stop the running webhook server daemon.`,
	RunE:  runStop,
}

func init() {
	addStopCommand()
}

func addStopCommand() {
	rootCmd.AddCommand(stopCmd)
}

func runStop(cmd *cobra.Command, args []string) error {
	profile := "default" // TODO: support profiles
	pidPath := pidPathForProfile(profile)

	// Check if server is running
	pid, err := readPIDFile(pidPath)
	if err != nil {
		return fmt.Errorf("server is not running (no PID file found)")
	}

	// Verify process exists
	process, err := os.FindProcess(pid)
	if err != nil {
		// PID file is stale, clean it up
		os.Remove(pidPath)
		return fmt.Errorf("server is not running (stale PID file)")
	}

	// Send SIGTERM for graceful shutdown
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send shutdown signal: %w", err)
	}

	// Wait for process to exit (up to 10 seconds)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stopped := false
	for {
		select {
		case <-ctx.Done():
			// Timeout: force kill
			if err := process.Kill(); err != nil {
				return fmt.Errorf("failed to force kill process: %w", err)
			}
			fmt.Fprintf(os.Stderr, "Server did not shut down gracefully, forced kill\n")
			stopped = true
		default:
			// Check if process still exists
			if err := process.Signal(syscall.Signal(0)); err != nil {
				// Process has exited
				stopped = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if stopped {
			break
		}
	}

	// Clean up PID file
	os.Remove(pidPath)

	fmt.Fprintf(os.Stderr, "Server stopped\n")
	return nil
}
