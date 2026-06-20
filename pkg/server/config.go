package server

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// BotConfig represents a single bot's configuration.
type BotConfig struct {
	Name     string `yaml:"name"     json:"name"`     // unique identifier
	Username string `yaml:"username" json:"username"`   // GitHub bot username
	Token    string `yaml:"token"    json:"token"`      // GitHub token
	GitName  string `yaml:"git_name" json:"git_name"`   // git commit author name
	GitEmail string `yaml:"git_email" json:"git_email"` // git commit author email
	Prefix   string `yaml:"prefix"   json:"prefix"`     // e.g. "@claude"
	Agent    string `yaml:"agent"    json:"agent"`       // "claude" (future: "kimicode", etc.)
}

// BotsFile is the top-level structure of bots.yaml.
type BotsFile struct {
	Bots []BotConfig `yaml:"bots"`
}

// LoadBots reads bots.yaml from the base directory.
// Returns empty BotsFile if the file doesn't exist.
func LoadBots(baseDir string) (*BotsFile, error) {
	path := filepath.Join(baseDir, "bots.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &BotsFile{}, nil
		}
		return nil, fmt.Errorf("read bots.yaml: %w", err)
	}
	var bots BotsFile
	if err := yaml.Unmarshal(data, &bots); err != nil {
		return nil, fmt.Errorf("parse bots.yaml: %w", err)
	}
	return &bots, nil
}

// SaveBots writes bots.yaml to the base directory.
func SaveBots(baseDir string, bots *BotsFile) error {
	path := filepath.Join(baseDir, "bots.yaml")
	data, err := yaml.Marshal(bots)
	if err != nil {
		return fmt.Errorf("marshal bots.yaml: %w", err)
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return fmt.Errorf("create base dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write bots.yaml: %w", err)
	}
	return nil
}

// loadSystemPrompt loads a system prompt from the prompts directory.
// Lookup order: {baseDir}/prompts/{repo}/{action}.md
//   → {baseDir}/prompts/{repo}/default.md
//   → {baseDir}/prompts/default.md
//   → built-in fallback.
func loadSystemPrompt(baseDir, repo, action string) string {
	promptsDir := filepath.Join(baseDir, "prompts")

	// Try repo/action.md
	if repo != "" && action != "" {
		if content := readPromptFile(promptsDir, repo, action+".md"); content != "" {
			return content
		}
	}

	// Try repo/default.md
	if repo != "" {
		if content := readPromptFile(promptsDir, repo, "default.md"); content != "" {
			return content
		}
	}

	// Try global default.md
	if content := readPromptFile(promptsDir, "", "default.md"); content != "" {
		return content
	}

	// Built-in fallback
	return SystemPrompt
}

// readPromptFile reads a prompt file from the prompts directory.
func readPromptFile(promptsDir, subdir, filename string) string {
	var path string
	if subdir != "" {
		path = filepath.Join(promptsDir, subdir, filename)
	} else {
		path = filepath.Join(promptsDir, filename)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return ""
	}
	slog.Info("loaded prompt", "path", path)
	return content
}
