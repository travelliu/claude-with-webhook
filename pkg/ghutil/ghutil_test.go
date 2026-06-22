package ghutil

import (
	"encoding/json"
	"testing"
)

func TestParseAuthStatusJSON(t *testing.T) {
	raw := []byte(`{
		"hosts": {
			"github.com": [
				{"state": "success", "active": true, "host": "github.com", "login": "travelliu", "scopes": "admin:repo_hook, repo"},
				{"state": "success", "active": false, "host": "github.com", "login": "bliu-coder", "scopes": "admin:repo_hook, repo"}
			]
		}
	}`)

	accounts, err := parseAuthStatusJSON(raw)
	if err != nil {
		t.Fatalf("parseAuthStatusJSON: %v", err)
	}

	if len(accounts) != 2 {
		t.Fatalf("got %d accounts, want 2", len(accounts))
	}

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
	raw := []byte(`{"hosts": {}}`)

	accounts, err := parseAuthStatusJSON(raw)
	if err != nil {
		t.Fatalf("parseAuthStatusJSON: %v", err)
	}
	if len(accounts) != 0 {
		t.Errorf("got %d accounts, want 0", len(accounts))
	}
}

func TestParseAuthStatusText(t *testing.T) {
	output := `github.com
  ✓ Logged in to github.com account travelliu (keyring)
  - Active account: true
  - Git operations protocol: ssh
  - Token: gho_****
  - Token scopes: 'admin:repo_hook', 'repo', 'workflow'
`

	accounts, err := parseAuthStatusText(output)
	if err != nil {
		t.Fatalf("parseAuthStatusText: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("got %d accounts, want 1", len(accounts))
	}

	a := accounts[0]
	if a.Username != "travelliu" {
		t.Errorf("username = %q, want %q", a.Username, "travelliu")
	}
	if a.Instance != "github.com" {
		t.Errorf("instance = %q, want %q", a.Instance, "github.com")
	}
	if !a.Active {
		t.Error("expected active to be true")
	}
	if a.Scopes != "admin:repo_hook,repo,workflow" {
		t.Errorf("scopes = %q, want %q", a.Scopes, "admin:repo_hook,repo,workflow")
	}
}

func TestParseAuthStatusTextMultipleAccounts(t *testing.T) {
	output := `github.com
  ✓ Logged in to github.com account travelliu (keyring)
  - Active account: true
  - Git operations protocol: ssh
  - Token: gho_****
  - Token scopes: 'admin:repo_hook', 'repo'
github.com
  ✓ Logged in to github.com account bliu-coder (keyring)
  - Active account: false
  - Git operations protocol: ssh
  - Token: gho_****
  - Token scopes: 'repo'
`

	accounts, err := parseAuthStatusText(output)
	if err != nil {
		t.Fatalf("parseAuthStatusText: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("got %d accounts, want 2", len(accounts))
	}

	if accounts[0].Username != "travelliu" {
		t.Errorf("account[0].username = %q, want %q", accounts[0].Username, "travelliu")
	}
	if !accounts[0].Active {
		t.Error("expected account[0] to be active")
	}
	if accounts[1].Username != "bliu-coder" {
		t.Errorf("account[1].username = %q, want %q", accounts[1].Username, "bliu-coder")
	}
	if accounts[1].Active {
		t.Error("expected account[1] to be inactive")
	}
}

func TestParseAuthStatusTextNoAccounts(t *testing.T) {
	output := `You are not logged into any GitHub hosts. Run gh auth login to authenticate.`

	_, err := parseAuthStatusText(output)
	if err == nil {
		t.Fatal("expected error for no accounts")
	}
}
