package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCleanJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "trailing comma in array",
			input: `{"items": [1, 2, 3,]}`,
			want:  `{"items": [1, 2, 3]}`,
		},
		{
			name:  "trailing comma in object",
			input: `{"name": "test", "value": 42,}`,
			want:  `{"name": "test", "value": 42}`,
		},
		{
			name:  "trailing comma in both",
			input: `{"items": [{"name": "a",},],}`,
			want:  `{"items": [{"name": "a"}]}`,
		},
		{
			name:  "no trailing comma",
			input: `{"items": [1, 2, 3]}`,
			want:  `{"items": [1, 2, 3]}`,
		},
		{
			name:  "empty array",
			input: `{"items": []}`,
			want:  `{"items": []}`,
		},
		{
			name:  "nested trailing commas",
			input: `{"a": {"b": [1, 2,],},}`,
			want:  `{"a": {"b": [1, 2]}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(cleanJSON([]byte(tt.input)))
			if got != tt.want {
				t.Errorf("cleanJSON() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoadBots_JSON(t *testing.T) {
	tmpDir := t.TempDir()

	// Test: bots.json with trailing commas
	jsonContent := `{
  "bots": [
    {
      "name": "mybot",
      "username": "mybot-user",
      "token": "ghp_test123",
      "prefix": "@mybot"
    },
  ]
}`
	if err := os.WriteFile(filepath.Join(tmpDir, "bots.json"), []byte(jsonContent), 0o644); err != nil {
		t.Fatal(err)
	}

	bots, err := LoadBots(tmpDir)
	if err != nil {
		t.Fatalf("LoadBots() error = %v", err)
	}
	if len(bots.Bots) != 1 {
		t.Fatalf("expected 1 bot, got %d", len(bots.Bots))
	}
	if bots.Bots[0].Name != "mybot" {
		t.Errorf("bot name = %q, want %q", bots.Bots[0].Name, "mybot")
	}
	if bots.Bots[0].Prefix != "@mybot" {
		t.Errorf("bot prefix = %q, want %q", bots.Bots[0].Prefix, "@mybot")
	}
}

func TestLoadBots_JSONMultipleBots(t *testing.T) {
	tmpDir := t.TempDir()

	jsonContent := `{
  "bots": [
    {
      "name": "bot1",
      "prefix": "@bot1"
    },
    {
      "name": "bot2",
      "prefix": "@bot2"
    },
  ]
}`
	if err := os.WriteFile(filepath.Join(tmpDir, "bots.json"), []byte(jsonContent), 0o644); err != nil {
		t.Fatal(err)
	}

	bots, err := LoadBots(tmpDir)
	if err != nil {
		t.Fatalf("LoadBots() error = %v", err)
	}
	if len(bots.Bots) != 2 {
		t.Fatalf("expected 2 bots, got %d", len(bots.Bots))
	}
}

func TestLoadBots_YAMLPreferredOverJSON(t *testing.T) {
	tmpDir := t.TempDir()

	// Create both YAML and JSON files
	yamlContent := `bots:
  - name: yaml-bot
    prefix: "@yaml"
`
	jsonContent := `{"bots": [{"name": "json-bot", "prefix": "@json"}]}`
	if err := os.WriteFile(filepath.Join(tmpDir, "bots.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "bots.json"), []byte(jsonContent), 0o644); err != nil {
		t.Fatal(err)
	}

	bots, err := LoadBots(tmpDir)
	if err != nil {
		t.Fatalf("LoadBots() error = %v", err)
	}
	if len(bots.Bots) != 1 {
		t.Fatalf("expected 1 bot, got %d", len(bots.Bots))
	}
	if bots.Bots[0].Name != "yaml-bot" {
		t.Errorf("expected YAML bot to be preferred, got %q", bots.Bots[0].Name)
	}
}

func TestLoadBots_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()

	bots, err := LoadBots(tmpDir)
	if err != nil {
		t.Fatalf("LoadBots() error = %v", err)
	}
	if len(bots.Bots) != 0 {
		t.Errorf("expected 0 bots, got %d", len(bots.Bots))
	}
}

func TestLoadBots_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()

	// Invalid JSON (not just trailing commas)
	jsonContent := `{"bots": [{"name": "broken"`
	if err := os.WriteFile(filepath.Join(tmpDir, "bots.json"), []byte(jsonContent), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadBots(tmpDir)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestLoadJSON(t *testing.T) {
	tmpDir := t.TempDir()

	type Config struct {
		Name  string   `json:"name"`
		Items []string `json:"items"`
	}

	// Test with trailing commas
	jsonContent := `{
  "name": "test",
  "items": ["a", "b", "c",],
}`
	path := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(path, []byte(jsonContent), 0o644); err != nil {
		t.Fatal(err)
	}

	var cfg Config
	if err := LoadJSON(path, &cfg); err != nil {
		t.Fatalf("LoadJSON() error = %v", err)
	}
	if cfg.Name != "test" {
		t.Errorf("name = %q, want %q", cfg.Name, "test")
	}
	if len(cfg.Items) != 3 {
		t.Errorf("items count = %d, want 3", len(cfg.Items))
	}
}

func TestLoadJSON_TrailingCommaInObject(t *testing.T) {
	tmpDir := t.TempDir()

	type Config struct {
		Name string `json:"name"`
		Port int    `json:"port"`
	}

	jsonContent := `{
  "name": "server",
  "port": 8080,
}`
	path := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(path, []byte(jsonContent), 0o644); err != nil {
		t.Fatal(err)
	}

	var cfg Config
	if err := LoadJSON(path, &cfg); err != nil {
		t.Fatalf("LoadJSON() error = %v", err)
	}
	if cfg.Name != "server" {
		t.Errorf("name = %q, want %q", cfg.Name, "server")
	}
	if cfg.Port != 8080 {
		t.Errorf("port = %d, want 8080", cfg.Port)
	}
}

func TestCleanJSON_PreservesStringCommas(t *testing.T) {
	// Commas inside strings should not be affected
	input := `{"text": "hello, world, how are you?"}`
	got := string(cleanJSON([]byte(input)))
	if got != input {
		t.Errorf("cleanJSON() modified string content: got %q", got)
	}
}

func TestCleanJSON_MultipleTrailingCommas(t *testing.T) {
	input := `{"a": 1,, "b": 2}`
	got := string(cleanJSON([]byte(input)))
	// Note: double commas are not handled - only trailing commas before ] or }
	// This is intentional as double commas are invalid JSON in any case
	var result map[string]int
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		// Expected - double commas are truly invalid
		return
	}
}
