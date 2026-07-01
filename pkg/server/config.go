package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

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
	GHBin    string `yaml:"gh_bin"   json:"gh_bin"`     // custom gh binary path (default: "gh")
}

// BotsFile is the top-level structure of bots config files.
type BotsFile struct {
	Bots []BotConfig `yaml:"bots" json:"bots"`
}

// trailingCommaPattern matches trailing commas before closing brackets/braces.
var trailingCommaPattern = regexp.MustCompile(`,(\s*[}\]])`)

// cleanJSON removes trailing commas from JSON to support lenient parsing.
func cleanJSON(data []byte) []byte {
	return trailingCommaPattern.ReplaceAll(data, []byte("$1"))
}

// LoadBots reads bots config from the base directory.
// Tries bots.yaml first, then bots.json (with lenient trailing-comma handling).
// Returns empty BotsFile if no config file exists.
func LoadBots(baseDir string) (*BotsFile, error) {
	yamlPath := filepath.Join(baseDir, "bots.yaml")
	jsonPath := filepath.Join(baseDir, "bots.json")

	// Try bots.yaml first
	if data, err := os.ReadFile(yamlPath); err == nil {
		var bots BotsFile
		if err := yaml.Unmarshal(data, &bots); err != nil {
			return nil, fmt.Errorf("parse bots.yaml: %w\n\nTip: check for syntax errors (indentation, missing colons)", err)
		}
		return &bots, nil
	}

	// Try bots.json with lenient parsing
	if data, err := os.ReadFile(jsonPath); err == nil {
		cleaned := cleanJSON(data)
		var bots BotsFile
		if err := json.Unmarshal(cleaned, &bots); err != nil {
			return nil, fmt.Errorf("parse bots.json: %w\n\nTip: check for syntax errors (missing quotes, invalid values)", err)
		}
		return &bots, nil
	}

	// No config file found
	return &BotsFile{}, nil
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

// LoadJSON reads a JSON config file with lenient trailing-comma handling.
func LoadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	cleaned := cleanJSON(data)
	if err := json.Unmarshal(cleaned, v); err != nil {
		return fmt.Errorf("parse %s: %w\n\nTip: check for syntax errors (missing quotes, invalid values)", filepath.Base(path), err)
	}
	return nil
}
