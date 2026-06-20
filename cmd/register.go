package cmd

import (
	"fmt"
	"os"
	"syscall"

	"github.com/spf13/cobra"
)

var registerCmd = &cobra.Command{
	Use:   "register",
	Short: "Register a repository with the webhook server",
	Long:  `Register the current git repository with the claude-webhook server.
This sets up GitHub webhook and tunnel configuration.`,
	RunE:  runRegister,
}

func init() {
	addRegisterCommand()
	registerCmd.Flags().BoolP("force", "f", false, "Force replace existing webhooks")
	registerCmd.Flags().BoolP("skip-webhook", "w", false, "Skip webhook configuration")
	registerCmd.Flags().BoolP("skip-tunnel", "t", false, "Skip tunnel setup")
}

func addRegisterCommand() {
	rootCmd.AddCommand(registerCmd)
}

func runRegister(cmd *cobra.Command, args []string) error {
	force, _ := cmd.Flags().GetBool("force")
	skipWebhook, _ := cmd.Flags().GetBool("skip-webhook")
	skipTunnel, _ := cmd.Flags().GetBool("skip-tunnel")

	fmt.Printf("Register command (force=%v, skip-webhook=%v, skip-tunnel=%v)\n", force, skipWebhook, skipTunnel)

	// TODO: Implement full register logic from shell script
	// For now, just demonstrate the reload signal functionality

	if !skipWebhook {
		// After setting up webhook and updating repos.conf,
		// signal the server to reload
		if err := signalServerReload(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
		}
	}

	fmt.Println("TODO: Implement register logic from shell script")
	return nil
}

// signalServerReload sends SIGHUP to the running server process
func signalServerReload() error {
	profile := "default" // TODO: support profiles
	pidPath := pidPathForProfile(profile)

	pid, err := readPIDFile(pidPath)
	if err != nil {
		return fmt.Errorf("server is not running (no PID file)")
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("server process not found (pid %d)", pid)
	}

	// Send SIGHUP for config reload
	if err := process.Signal(syscall.SIGHUP); err != nil {
		return fmt.Errorf("failed to send SIGHUP: %w", err)
	}

	fmt.Println("Server reloaded repos.conf.")
	return nil
}

// TODO: Move register logic from shell script:
// - checkGitRepo()
// - loadConfig()
// - registerRepo()
// - setupTunnel()
// - configureWebhook()
// - checkPermissions()
