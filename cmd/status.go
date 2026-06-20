package cmd

import (
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show server and repository status",
	Long:  `Display the status of the claude-webhook server, tunnel configuration,
and registered repositories.`,
	Run:   runStatus,
}

func init() {
	statusCmd.Flags().BoolP("verbose", "v", false, "Show detailed status")
	statusCmd.Flags().BoolP("json", "j", false, "Output in JSON format")
}

func addStatusCommand() {
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) {
	verbose, _ := cmd.Flags().GetBool("verbose")
	json, _ := cmd.Flags().GetBool("json")

	// TODO: Implement status logic from shell script
	// - Check server status
	// - Check tunnel status
	// - Show configuration
	// - List registered repos

	if json {
		println("{}") // TODO: Implement JSON output
	} else {
		printlnStatus(verbose)
	}
}

func printlnStatus(verbose bool) {
	println("Claude Webhook Server Status")
	println("────────────────────────────")
	println("TODO: Implement status display")
	if verbose {
		println("Verbose mode: showing detailed information")
	}
}

// TODO: Move status logic from shell script:
// - checkServerStatus()
// - checkTunnelStatus()
// - showConfig()
// - listRegisteredRepos()
// - formatStatusOutput()
