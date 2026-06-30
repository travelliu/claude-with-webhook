package server

import (
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

// BotsFile is the top-level structure of bots.yaml.
type BotsFile struct {
	Bots []BotConfig `yaml:"bots"`
}

// trailingCommaPattern matches trailing commas before closing brackets/braces.
// Handles optional whitespace and newlines between the comma and the closing character.
var trailingCommaPattern = regexp.MustCompile(`,(\s*[}\]])`)

// sanitizeJSON removes trailing commas from JSON-like content.
// This is a common mistake when editing JSON config files, and YAML
// (which is a superset of JSON) also rejects trailing commas.
func sanitizeJSON(data []byte) []byte {
	return trailingCommaPattern.ReplaceAll(data, []byte("$1"))
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
		// Try again after stripping trailing commas (common JSON editing mistake)
		sanitized := sanitizeJSON(data)
		if sanitizeErr := yaml.Unmarshal(sanitized, &bots); sanitizeErr == nil {
			return &bots, nil
		}
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
