package ghutil

import (
	"encoding/json"
	"testing"
)

func TestParseAuthStatusJSON(t *testing.T) {
	// Simulate the JSON structure returned by `gh auth status --json hosts`
	raw := `{
		"hosts": {
			"github.com": [
				{"state": "success", "active": true, "host": "github.com", "login": "travelliu", "scopes": "admin:repo_hook, repo"},
				{"state": "success", "active": false, "host": "github.com", "login": "bliu-coder", "scopes": "admin:repo_hook, repo"}
			]
		}
	}`

	var data ghAuthStatusJSON
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var accounts []Account
	for host, entries := range data.Hosts {
		for _, e := range entries {
			if e.State != "success" {
				continue
			}
			accounts = append(accounts, Account{
				Username: e.Login,
				Instance: host,
				Active:   e.Active,
				Scopes:   e.Scopes,
			})
		}
	}

	if len(accounts) != 2 {
		t.Fatalf("got %d accounts, want 2", len(accounts))
	}

	// Find the active account
	var active *Account
	for i := range accounts {
		if accounts[i].Active {
			active = &accounts[i]
		}
	}
	if active == nil {
		t.Fatal("no active account found")
	}
	if active.Username != "travelliu" {
		t.Errorf("active account = %q, want %q", active.Username, "travelliu")
	}
	if active.Instance != "github.com" {
		t.Errorf("active instance = %q, want %q", active.Instance, "github.com")
	}
}

func TestAuthStatusJSONStructure(t *testing.T) {
	// Test with enterprise host
	raw := `{
		"hosts": {
			"github.example.com": [
				{"state": "success", "active": true, "host": "github.example.com", "login": "corp-bot", "scopes": "repo"}
			]
		}
	}`

	var data ghAuthStatusJSON
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	entries, ok := data.Hosts["github.example.com"]
	if !ok {
		t.Fatal("expected github.example.com host")
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Login != "corp-bot" {
		t.Errorf("login = %q, want %q", entries[0].Login, "corp-bot")
	}
	if entries[0].Active != true {
		t.Error("expected active to be true")
	}
}

func TestAuthStatusJSONEmpty(t *testing.T) {
	raw := `{"hosts": {}}`

	var data ghAuthStatusJSON
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var accounts []Account
	for host, entries := range data.Hosts {
		for _, e := range entries {
			accounts = append(accounts, Account{
				Username: e.Login,
				Instance: host,
				Active:   e.Active,
			})
		}
	}
	if len(accounts) != 0 {
		t.Errorf("got %d accounts, want 0", len(accounts))
	}
}
