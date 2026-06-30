package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no trailing comma",
			input: `{"bots": [{"name": "test"}]}`,
			want:  `{"bots": [{"name": "test"}]}`,
		},
		{
			name:  "trailing comma in array",
			input: `{"bots": [{"name": "test"},]}`,
			want:  `{"bots": [{"name": "test"}]}`,
		},
		{
			name:  "trailing comma in object",
			input: `{"name": "test",}`,
			want:  `{"name": "test"}`,
		},
		{
			name: "trailing comma with newline",
			input: `{
  "bots": [
    {
      "name": "test"
    },
  ]
}`,
			want: `{
  "bots": [
    {
      "name": "test"
    }
  ]
}`,
		},
		{
			name:  "multiple trailing commas",
			input: `{"a": [1,], "b": [2,],}`,
			want:  `{"a": [1], "b": [2]}`,
		},
		{
			name:  "nested trailing commas",
			input: `{"a": {"b": [1,],},}`,
			want:  `{"a": {"b": [1]}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(sanitizeJSON([]byte(tt.input)))
			if got != tt.want {
				t.Errorf("sanitizeJSON() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoadBotsWithTrailingComma(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "config-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Write a bots.yaml with trailing comma
	content := `{
  "bots": [
    {
      "name": "mybot",
      "username": "mybot-user",
      "token": "test-token",
      "prefix": "@mybot"
    },
  ]
}`
	if err := os.WriteFile(filepath.Join(tmpDir, "bots.yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// LoadBots should succeed by sanitizing the trailing comma
	bots, err := LoadBots(tmpDir)
	if err != nil {
		t.Fatalf("LoadBots() error = %v", err)
	}
	if len(bots.Bots) != 1 {
		t.Fatalf("expected 1 bot, got %d", len(bots.Bots))
	}
	if bots.Bots[0].Name != "mybot" {
		t.Errorf("expected bot name 'mybot', got %q", bots.Bots[0].Name)
	}
}

func TestLoadBotsWithValidYAML(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "config-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Write a valid bots.yaml (YAML format)
	content := `bots:
  - name: mybot
    username: mybot-user
    token: test-token
    prefix: "@mybot"
`
	if err := os.WriteFile(filepath.Join(tmpDir, "bots.yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// LoadBots should succeed
	bots, err := LoadBots(tmpDir)
	if err != nil {
		t.Fatalf("LoadBots() error = %v", err)
	}
	if len(bots.Bots) != 1 {
		t.Fatalf("expected 1 bot, got %d", len(bots.Bots))
	}
	if bots.Bots[0].Name != "mybot" {
		t.Errorf("expected bot name 'mybot', got %q", bots.Bots[0].Name)
	}
}

func TestLoadBotsNoFile(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "config-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// LoadBots should return empty BotsFile
	bots, err := LoadBots(tmpDir)
	if err != nil {
		t.Fatalf("LoadBots() error = %v", err)
	}
	if len(bots.Bots) != 0 {
		t.Errorf("expected 0 bots, got %d", len(bots.Bots))
	}
}

func TestLoadBotsInvalidContent(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "config-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Write an invalid bots.yaml (not valid YAML even after sanitization)
	content := `this is not valid yaml: {[}]`
	if err := os.WriteFile(filepath.Join(tmpDir, "bots.yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// LoadBots should return an error
	_, err = LoadBots(tmpDir)
	if err == nil {
		t.Fatal("expected error for invalid content, got nil")
	}
}
