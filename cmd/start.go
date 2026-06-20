package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"claude-with-webhook/pkg/server"

	"github.com/spf13/cobra"
)

const (
	flagForeground = "foreground"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the webhook server",
	Long:  `Start the Claude webhook server to listen for GitHub issue/webhook events.
Runs in the background by default. Use --foreground to run in the current terminal.`,
	RunE:  runStart,
}

func init() {
	startCmd.Flags().Bool(flagForeground, false, "Run in the foreground instead of background")
	startCmd.Flags().StringP("port", "p", "", "Server port (overrides .env PORT)")
	startCmd.Flags().IntP("max-concurrent", "j", 0, "Max concurrent jobs (overrides .env MAX_CONCURRENT)")
	addStartCommand()
}

func addStartCommand() {
	rootCmd.AddCommand(startCmd)
}

func runStart(cmd *cobra.Command, args []string) error {
	foreground, _ := cmd.Flags().GetBool(flagForeground)
	if foreground {
		return runStartForeground(cmd)
	}
	return runStartBackground(cmd)
}

// runStartForeground runs the server in the foreground
func runStartForeground(cmd *cobra.Command) error {
	baseDir := GetBaseDir()

	// Override with command-line flags
	if port, _ := cmd.Flags().GetString("port"); port != "" {
		os.Setenv("PORT", port)
	}
	if maxConcurrent, _ := cmd.Flags().GetInt("max-concurrent"); maxConcurrent > 0 {
		os.Setenv("MAX_CONCURRENT", strconv.Itoa(maxConcurrent))
	}

	// Start server in foreground
	fmt.Println("Starting server in foreground mode...")

	// Create server configuration
	cfg, err := server.NewConfig(baseDir)
	if err != nil {
		return fmt.Errorf("create config: %w", err)
	}

	// Create and start server
	srv := server.New(cfg)
	return srv.Start()
}

// runStartBackground runs the server as a daemon
func runStartBackground(cmd *cobra.Command) error {
	profile := "default" // TODO: support profiles like moclaw
	pidPath := pidPathForProfile(profile)
	healthPort := healthPortForProfile(profile)

	// Check if already running
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if isServerRunning(ctx, healthPort) {
		pid, err := readPIDFile(pidPath)
		if err == nil && pid > 0 {
			return fmt.Errorf("server is already running (pid %d) — use 'restart' to restart it", pid)
		}
	}

	// Get executable path
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	// Build args for background process
	args := buildStartArgs(cmd)

	// Create daemon directory
	daemonDir := daemonDirForProfile(profile)
	if err := os.MkdirAll(daemonDir, 0755); err != nil {
		return fmt.Errorf("create daemon directory: %w", err)
	}

	// Setup log file
	logPath := logPathForProfile(profile)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", logPath, err)
	}

	// Start background process
	child := exec.Command(exePath, args...)
	child.Stdout = logFile
	child.Stderr = logFile
	child.SysProcAttr = syscallProcAttr(true)

	if err := child.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("start server: %w", err)
	}
	logFile.Close()

	pid := child.Process.Pid
	child.Process.Release()

	// Write PID file
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not write PID file: %v\n", err)
	}

	// Check and update webhooks before waiting for server ready
	configFile := GetConfigFile()
	if cfg, err := LoadConfig(configFile); err == nil {
		if err := CheckAndUpdateWebhooks(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: webhook check failed: %v\n", err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "Warning: could not load config for webhook check: %v\n", err)
	}

	// Wait for server to be ready (up to 15 seconds)
	deadline := time.Now().Add(15 * time.Second)
	started := false
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		running := isServerRunning(ctx, healthPort)
		cancel()
		if running {
			started = true
			break
		}
	}

	if !started {
		fmt.Fprintf(os.Stderr, "Server may not have started. Check logs: %s\n", logPath)
		return nil
	}

	fmt.Fprintf(os.Stderr, "Server started (pid %d)\n", pid)
	fmt.Fprintf(os.Stderr, "Logs: %s\n", logPath)
	return nil
}

// buildStartArgs constructs args for the background child process
func buildStartArgs(cmd *cobra.Command) []string {
	args := []string{"start", "--foreground"}

	// Forward port and max-concurrent flags
	if port, _ := cmd.Flags().GetString("port"); port != "" {
		args = append(args, "--port", port)
	}
	if maxConcurrent, _ := cmd.Flags().GetInt("max-concurrent"); maxConcurrent > 0 {
		args = append(args, "--max-concurrent", strconv.Itoa(maxConcurrent))
	}

	return args
}


// --- daemon helpers ---

func daemonDirForProfile(profile string) string {
	baseDir := GetBaseDir()
	if profile == "" || profile == "default" {
		return baseDir
	}
	return fmt.Sprintf("%s/profiles/%s", baseDir, profile)
}

func pidPathForProfile(profile string) string {
	return fmt.Sprintf("%s/server.pid", daemonDirForProfile(profile))
}

func logPathForProfile(profile string) string {
	return fmt.Sprintf("%s/server.log", daemonDirForProfile(profile))
}

func healthPortForProfile(profile string) int {
	// TODO: implement profile-specific ports like moclaw
	port, _ := strconv.Atoi(getConfigEnv("PORT", "8080"))
	return port + 1 // Use PORT+1 for health endpoint to avoid conflicts
}

func isServerRunning(ctx context.Context, port int) bool {
	addr := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

func readPIDFile(pidPath string) (int, error) {
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, err
	}
	return pid, nil
}

func syscallProcAttr(createNewSession bool) *syscall.SysProcAttr {
	if createNewSession {
		return &syscall.SysProcAttr{
			Setsid: true, // Create new session
		}
	}
	return &syscall.SysProcAttr{}
}

func getConfigEnv(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}
