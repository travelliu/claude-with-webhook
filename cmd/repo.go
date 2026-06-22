package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"

	"claude-with-webhook/pkg/ghutil"
	"claude-with-webhook/pkg/logger"
	"claude-with-webhook/pkg/server"

	"github.com/spf13/cobra"
)

var repoCmd = &cobra.Command{
	Use:   "repo",
	Short: "Manage registered repositories",
}

var repoListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered repositories",
	RunE:  runRepoList,
}

var repoRegisterCmd = &cobra.Command{
	Use:   "add",
	Short: "Register a repository with the webhook server",
	Long: `Register the current git repository with the claude-webhook server.
Detects the repo, selects a bot, sets up tunnel, and configures the GitHub webhook.`,
	RunE: runRepoRegister,
}

var repoRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Unregister a repository",
	RunE:  runRepoRemove,
}

func init() {
	repoCmd.AddCommand(repoListCmd)
	repoCmd.AddCommand(repoRegisterCmd)
	repoCmd.AddCommand(repoRemoveCmd)
	rootCmd.AddCommand(repoCmd)

	repoCmd.PersistentFlags().String("base-dir", filepath.Join(os.Getenv("HOME"), ".claude-webhook"), "Base directory")

	// repo add flags
	repoRegisterCmd.Flags().String("dir", "", "Repository directory (defaults to current directory)")
	repoRegisterCmd.Flags().BoolP("force", "f", false, "Force replace existing webhooks")
	repoRegisterCmd.Flags().Bool("skip-webhook", false, "Skip webhook configuration")
	repoRegisterCmd.Flags().String("bot", "", "Bot name to use (interactive selection if omitted)")
	repoRegisterCmd.Flags().String("webhook-user", "", "GitHub username with admin:repo_hook scope (auto-detected if omitted)")
	repoRegisterCmd.Flags().StringSlice("allow", nil, "Additional GitHub usernames allowed to trigger the bot")

	// repo remove flags
	repoRemoveCmd.Flags().String("name", "", "Repository to unregister (e.g. owner/repo)")
}

// --- repo list ---

func runRepoList(cmd *cobra.Command, _ []string) error {
	baseDir, _ := cmd.Flags().GetString("base-dir")

	repos, err := server.LoadRepos(baseDir)
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		fmt.Println("No repositories registered. Use 'repo register' to add one.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "REPO\tDIR\tALLOWED_USERS\tWEBHOOK_TOKEN")
	for name, rc := range repos {
		users := "-"
		if len(rc.AllowedUsers) > 0 {
			users = joinTruncated(rc.AllowedUsers, 40)
		}
		token := "-"
		if rc.WebhookToken != "" {
			token = "configured"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", name, rc.Dir, users, token)
	}
	w.Flush()
	return nil
}

// --- repo register ---

func runRepoRegister(cmd *cobra.Command, args []string) error {
	logger.Init("info")

	force, _ := cmd.Flags().GetBool("force")
	skipWebhook, _ := cmd.Flags().GetBool("skip-webhook")
	botName, _ := cmd.Flags().GetString("bot")
	webhookUser, _ := cmd.Flags().GetString("webhook-user")
	allowedUsers, _ := cmd.Flags().GetStringSlice("allow")
	repoDir, _ := cmd.Flags().GetString("dir")

	// Support positional arg: `repo add /path/to/repo`
	if repoDir == "" && len(args) > 0 {
		repoDir = args[0]
	}

	baseDir, _ := cmd.Flags().GetString("base-dir")

	// Step 0: Verify gh auth
	accounts, err := ghutil.AuthStatus()
	if err != nil || len(accounts) == 0 {
		return fmt.Errorf("not logged in to GitHub; run 'gh auth login' first")
	}
	fmt.Printf("GitHub account: %s\n", accounts[0].Username)

	// Step 1: Detect git repo
	if repoDir == "" {
		repoDir, err = detectGitRoot()
		if err != nil {
			return fmt.Errorf("not in a git repository; use --dir to specify path: %w", err)
		}
	}
	absDir, err := filepath.Abs(repoDir)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	repoDir = absDir

	// Verify it's a git repo
	if _, err := exec.Command("git", "-C", repoDir, "rev-parse", "--git-dir").Output(); err != nil {
		return fmt.Errorf("not a git repository: %s", repoDir)
	}
	fmt.Printf("Repository: %s\n", repoDir)

	// Change to repo dir for gh operations
	origDir, _ := os.Getwd()
	os.Chdir(repoDir)
	defer os.Chdir(origDir)

	// Step 2: Select bot
	bot, err := selectBot(baseDir, botName)
	if err != nil {
		return fmt.Errorf("select bot: %w", err)
	}
	fmt.Printf("Using bot: %s (user=%s)\n", bot.Name, bot.Username)

	// Step 3: Detect GitHub repo (try with bot's token for access)
	repoInfo, err := ghutil.GetCurrentRepo()
	if err != nil {
		if bot.Token != "" {
			repoInfo, err = ghutil.GetCurrentRepoWithToken(bot.Token)
		}
		if err != nil {
			// Show actual gh error for debugging
			out, _ := exec.Command("gh", "repo", "view", "--json", "nameWithOwner").CombinedOutput()
			return fmt.Errorf("detect GitHub repo: %s", strings.TrimSpace(string(out)))
		}
	}
	fmt.Printf("GitHub repo: %s\n", repoInfo.NameWithOwner)

	// Step 4: Resolve webhook token
	var webhookToken string
	if !skipWebhook {
		webhookToken, err = resolveWebhookToken(accounts, bot, webhookUser)
		if err != nil {
			return fmt.Errorf("resolve webhook token: %w", err)
		}
	}

	// Step 5: Register repo in repos.yaml
	if err := saveRepo(baseDir, repoInfo.NameWithOwner, repoDir, allowedUsers, webhookToken); err != nil {
		return fmt.Errorf("register repo: %w", err)
	}

	// Step 5b: Ensure default prompt templates for this repo
	pm := server.NewPromptManager(baseDir)
	if err := pm.EnsureDefaultPrompts(repoInfo.NameWithOwner); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to setup prompt templates: %v\n", err)
	} else {
		fmt.Println("Prompt templates configured.")
	}

	// Step 6: Configure webhook (requires PUBLIC_URL or tunnel running)
	publicURL := os.Getenv("PUBLIC_URL")
	if !skipWebhook && publicURL != "" {
		webhookURL := fmt.Sprintf("%s/%s/webhook", strings.TrimRight(publicURL, "/"), repoInfo.NameWithOwner)
		secret := os.Getenv("GITHUB_WEBHOOK_SECRET")
		if secret == "" {
			return fmt.Errorf("GITHUB_WEBHOOK_SECRET not set; configure .env first")
		}

		fmt.Printf("Webhook URL: %s\n", webhookURL)

		if err := ghutil.EnsureWebhook(repoInfo.NameWithOwner, ghutil.WebhookConfig{
			URL:    webhookURL,
			Secret: secret,
			Token:  webhookToken,
		}); err != nil {
			if !force {
				return fmt.Errorf("configure webhook: %w", err)
			}
			fmt.Fprintf(os.Stderr, "Warning: webhook setup failed (force mode): %v\n", err)
		} else {
			fmt.Println("Webhook configured.")
		}
	}

	// Step 7: Signal server reload
	if err := signalServerReload(); err != nil {
		fmt.Fprintf(os.Stderr, "Note: %v (server may not be running)\n", err)
	}

	os.MkdirAll(filepath.Join(repoDir, "worktrees"), 0o755)

	fmt.Println()
	fmt.Println("=== Registration Complete ===")
	fmt.Printf("  Repo:    %s\n", repoInfo.NameWithOwner)
	fmt.Printf("  Local:   %s\n", repoDir)
	fmt.Printf("  Bot:     %s (%s)\n", bot.Name, bot.Prefix)
	if len(allowedUsers) > 0 {
		fmt.Printf("  Allowed: %s\n", strings.Join(allowedUsers, ", "))
	}
	if webhookToken != "" {
		fmt.Println("  Webhook token: configured")
	}
	if !skipWebhook && publicURL == "" {
		fmt.Println("  Note: Set PUBLIC_URL env and re-run to configure webhook, or use 'daemon start' to auto-detect tunnel.")
	}
	return nil
}

// --- repo remove ---

func runRepoRemove(cmd *cobra.Command, _ []string) error {
	baseDir, _ := cmd.Flags().GetString("base-dir")

	name, _ := cmd.Flags().GetString("name")
	if name == "" {
		return fmt.Errorf("--name is required (e.g. owner/repo)")
	}

	repos, err := server.LoadRepos(baseDir)
	if err != nil {
		return err
	}
	if _, ok := repos[name]; !ok {
		return fmt.Errorf("repo %q not found", name)
	}

	delete(repos, name)
	if err := server.SaveRepos(baseDir, repos); err != nil {
		return err
	}
	fmt.Printf("Repo %q unregistered\n", name)
	return nil
}

// --- helpers ---

func saveRepo(baseDir, repo, dir string, allowedUsers []string, webhookToken string) error {
	repos, _ := server.LoadRepos(baseDir)
	if repos == nil {
		repos = make(map[string]server.RepoConfig)
	}

	rc := repos[repo]
	rc.Dir = dir
	if len(allowedUsers) > 0 {
		rc.AllowedUsers = allowedUsers
	}
	if webhookToken != "" {
		rc.WebhookToken = webhookToken
	}
	repos[repo] = rc

	if err := server.SaveRepos(baseDir, repos); err != nil {
		return err
	}
	fmt.Printf("Registered: %s → %s\n", repo, dir)
	return nil
}

func selectBot(baseDir, botName string) (*server.BotConfig, error) {
	bots, err := server.LoadBots(baseDir)
	if err != nil {
		return nil, err
	}
	if len(bots.Bots) == 0 {
		return nil, fmt.Errorf("no bots configured; run 'bot add' first")
	}

	if botName != "" {
		for i := range bots.Bots {
			if bots.Bots[i].Name == botName {
				return &bots.Bots[i], nil
			}
		}
		return nil, fmt.Errorf("bot %q not found", botName)
	}

	if len(bots.Bots) == 1 {
		return &bots.Bots[0], nil
	}

	fmt.Println("Multiple bots configured:")
	for i, b := range bots.Bots {
		fmt.Printf("  %d. %s (user=%s, prefix=%s, agent=%s)\n", i+1, b.Name, b.Username, b.Prefix, b.Agent)
	}
	fmt.Print("Select bot (number): ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	choice, err := strconv.Atoi(input)
	if err != nil || choice < 1 || choice > len(bots.Bots) {
		return nil, fmt.Errorf("invalid selection: %q", input)
	}
	return &bots.Bots[choice-1], nil
}

func resolveWebhookToken(accounts []ghutil.Account, bot *server.BotConfig, webhookUser string) (string, error) {
	// Explicit --webhook-user
	if webhookUser != "" {
		token, err := ghutil.GetToken(webhookUser)
		if err != nil {
			return "", fmt.Errorf("get token for %s: %w", webhookUser, err)
		}
		fmt.Printf("Using webhook token from: %s\n", webhookUser)
		return token, nil
	}

	// Bot's own token if it has the scope
	if bot != nil && bot.Token != "" {
		for _, a := range accounts {
			if a.Username == bot.Username && strings.Contains(a.Scopes, "admin:repo_hook") {
				return bot.Token, nil
			}
		}
	}

	// Auto-detect: find any gh account with admin:repo_hook
	for _, a := range accounts {
		if strings.Contains(a.Scopes, "admin:repo_hook") {
			token, err := ghutil.GetToken(a.Username)
			if err == nil {
				fmt.Printf("Using webhook token from: %s\n", a.Username)
				return token, nil
			}
		}
	}

	return "", fmt.Errorf("no account with admin:repo_hook scope found; run 'gh auth refresh -h github.com -s admin:repo_hook' or use --webhook-user")
}

func detectGitRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func signalServerReload() error {
	profile := "default"
	pidPath := pidPathForProfile(profile)

	pid, err := readPIDFile(pidPath)
	if err != nil {
		return fmt.Errorf("server is not running (no PID file)")
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("server process not found (pid %d)", pid)
	}

	if err := process.Signal(syscall.SIGHUP); err != nil {
		return fmt.Errorf("send SIGHUP: %w", err)
	}

	fmt.Println("Signaled server to reload config.")
	return nil
}

func joinTruncated(items []string, maxLen int) string {
	result := ""
	for i, s := range items {
		if i > 0 {
			result += ", "
		}
		result += s
		if len(result) > maxLen {
			return result[:maxLen] + "..."
		}
	}
	return result
}
