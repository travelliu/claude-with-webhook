package cmd

import (
	"github.com/spf13/cobra"
)

var registerCmd = &cobra.Command{
	Use:   "register",
	Short: "Register a repository with the webhook server",
	Long:  `Register the current git repository with the claude-webhook server.
This sets up GitHub webhook and tunnel configuration.`,
	Run:   runRegister,
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

func runRegister(cmd *cobra.Command, args []string) {
	force, _ := cmd.Flags().GetBool("force")
	skipWebhook, _ := cmd.Flags().GetBool("skip-webhook")
	skipTunnel, _ := cmd.Flags().GetBool("skip-tunnel")

	// TODO: Implement register logic from shell script
	// - Check git repo
	// - Load config
	// - Register repo in repos.conf
	// - Setup tunnel (if not skipped)
	// - Configure webhook (if not skipped)

	println("Register command (force=%v, skip-webhook=%v, skip-tunnel=%v)", force, skipWebhook, skipTunnel)
	println("TODO: Implement register logic from shell script")
}

// TODO: Move register logic from shell script:
// - checkGitRepo()
// - loadConfig()
// - registerRepo()
// - setupTunnel()
// - configureWebhook()
// - checkPermissions()
