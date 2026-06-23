package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"claude-with-webhook/pkg/ghutil"
	"claude-with-webhook/pkg/server"

	"github.com/spf13/cobra"
)

var botCmd = &cobra.Command{
	Use:   "bot",
	Short: "Manage webhook bots",
}

var botAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new bot",
	Long: `Add a new bot configuration. If --username and --token are omitted,
the command reads gh auth status and lets you select an account interactively.`,
	RunE: runBotAdd,
}

var botListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured bots",
	RunE: func(cmd *cobra.Command, args []string) error {
		baseDir, _ := cmd.Flags().GetString("base-dir")

		bots, err := server.LoadBots(baseDir)
		if err != nil {
			return err
		}
		if len(bots.Bots) == 0 {
			fmt.Println("No bots configured. Use 'bot add' to add one.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tUSERNAME\tPREFIX\tAGENT\tGIT_NAME\tGIT_EMAIL")
		for _, b := range bots.Bots {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", b.Name, b.Username, b.Prefix, b.Agent, b.GitName, b.GitEmail)
		}
		w.Flush()
		return nil
	},
}

var botRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove a bot",
	RunE: func(cmd *cobra.Command, args []string) error {
		baseDir, _ := cmd.Flags().GetString("base-dir")

		name, _ := cmd.Flags().GetString("name")
		if name == "" {
			return fmt.Errorf("--name is required")
		}

		bots, err := server.LoadBots(baseDir)
		if err != nil {
			return err
		}

		found := false
		var filtered []server.BotConfig
		for _, b := range bots.Bots {
			if b.Name == name {
				found = true
				continue
			}
			filtered = append(filtered, b)
		}
		if !found {
			return fmt.Errorf("bot %q not found", name)
		}

		bots.Bots = filtered
		if err := server.SaveBots(baseDir, bots); err != nil {
			return err
		}
		fmt.Printf("Bot %q removed\n", name)
		return nil
	},
}

func runBotAdd(cmd *cobra.Command, _ []string) error {
	baseDir, _ := cmd.Flags().GetString("base-dir")

	name, _ := cmd.Flags().GetString("name")
	username, _ := cmd.Flags().GetString("username")
	token, _ := cmd.Flags().GetString("token")
	prefix, _ := cmd.Flags().GetString("prefix")
	agentName, _ := cmd.Flags().GetString("agent")
	gitName, _ := cmd.Flags().GetString("git-name")
	gitEmail, _ := cmd.Flags().GetString("git-email")

	// Interactive account selection when username/token not provided
	if username == "" || token == "" {
		selected, err := selectGhAccount()
		if err != nil {
			return fmt.Errorf("select GitHub account: %w", err)
		}
		if username == "" {
			username = selected.Username
		}
		if token == "" {
			t, err := ghutil.GetToken(selected.Username)
			if err != nil {
				return fmt.Errorf("get token for %s: %w", selected.Username, err)
			}
			token = t
		}
	}

	if name == "" {
		name = username
	}
	if prefix == "" {
		prefix = "@" + name
	}
	if agentName == "" {
		agentName = "claude"
	}

	// Prompt for git identity if not provided via flags
	reader := bufio.NewReader(os.Stdin)
	if gitName == "" {
		fmt.Printf("Git commit author name (default: %s): ", username)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "" {
			gitName = username
		} else {
			gitName = input
		}
	}
	if gitEmail == "" {
		fmt.Print("Git commit author email (required for git commit): ")
		input, _ := reader.ReadString('\n')
		gitEmail = strings.TrimSpace(input)
		if gitEmail == "" {
			return fmt.Errorf("git-email is required — git commit will fail without it")
		}
	}

	bots, err := server.LoadBots(baseDir)
	if err != nil {
		return err
	}

	for _, b := range bots.Bots {
		if b.Name == name {
			return fmt.Errorf("bot %q already exists", name)
		}
	}

	bots.Bots = append(bots.Bots, server.BotConfig{
		Name:     name,
		Username: username,
		Token:    token,
		GitName:  gitName,
		GitEmail: gitEmail,
		Prefix:   prefix,
		Agent:    agentName,
	})

	if err := server.SaveBots(baseDir, bots); err != nil {
		return err
	}
	fmt.Printf("Bot %q added (user=%s, prefix=%s, agent=%s)\n", name, username, prefix, agentName)
	return nil
}

// selectGhAccount reads gh auth status and returns a user-selected account.
func selectGhAccount() (ghutil.Account, error) {
	accounts, err := ghutil.AuthStatus()
	if err != nil {
		return ghutil.Account{}, err
	}
	if len(accounts) == 0 {
		return ghutil.Account{}, fmt.Errorf("no GitHub accounts found; run 'gh auth login' first")
	}

	if len(accounts) == 1 {
		fmt.Printf("Using GitHub account: %s\n", accounts[0].Username)
		return accounts[0], nil
	}

	// Prefer the active account
	for _, a := range accounts {
		if a.Active {
			fmt.Printf("Using active GitHub account: %s\n", a.Username)
			return a, nil
		}
	}

	// Interactive selection
	fmt.Println("Multiple GitHub accounts found:")
	for i, a := range accounts {
		marker := " "
		if a.Active {
			marker = "*"
		}
		fmt.Printf("  [%s] %d. %s (%s)\n", marker, i+1, a.Username, a.Instance)
	}
	fmt.Print("Select account (number): ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	choice, err := strconv.Atoi(input)
	if err != nil || choice < 1 || choice > len(accounts) {
		return ghutil.Account{}, fmt.Errorf("invalid selection: %q", input)
	}
	selected := accounts[choice-1]
	fmt.Printf("Selected: %s\n", selected.Username)
	return selected, nil
}

func init() {
	botCmd.AddCommand(botAddCmd)
	botCmd.AddCommand(botListCmd)
	botCmd.AddCommand(botRemoveCmd)
	rootCmd.AddCommand(botCmd)

	botCmd.PersistentFlags().String("base-dir", filepath.Join(os.Getenv("HOME"), ".claude-webhook"), "Base directory")

	botAddCmd.Flags().String("name", "", "Bot name (defaults to GitHub username)")
	botAddCmd.Flags().String("username", "", "GitHub bot username (auto-detected from gh if omitted)")
	botAddCmd.Flags().String("token", "", "GitHub token (auto-detected from gh if omitted)")
	botAddCmd.Flags().String("prefix", "", "Command prefix (defaults to @name)")
	botAddCmd.Flags().String("agent", "claude", "Agent backend (claude)")
	botAddCmd.Flags().String("git-name", "", "Git commit author name")
	botAddCmd.Flags().String("git-email", "", "Git commit author email")

	botRemoveCmd.Flags().String("name", "", "Bot name to remove")
}
