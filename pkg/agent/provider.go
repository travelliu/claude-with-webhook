package agent

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// cliProvider holds provider identity and standard path conventions.
// Embed this in a concrete provider to get Name and CLIPath for free.
type cliProvider struct {
	name       string // provider identifier, e.g. "claude"
	envPathVar string // env var that overrides the CLI binary path
	cmdName    string // default CLI binary name, e.g. "claude"
}

func (p *cliProvider) Name() string { return p.name }

// CLIPath returns the resolved path to the provider CLI binary.
func (p *cliProvider) CLIPath() (string, bool) {
	path, err := p.resolveCLIPath()
	if err != nil {
		return "", false
	}
	return path, true
}

var semverPattern = regexp.MustCompile(`v?(\d+)\.(\d+)\.(\d+)`)

// CLIInfo runs the provider CLI with --version and returns the display name
// and parsed semver version string. Best-effort.
func (p *cliProvider) CLIInfo() (name, version string) {
	path, err := p.resolveCLIPath()
	if err != nil {
		return p.name, ""
	}
	out, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		return p.name, ""
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return p.name, ""
	}
	firstLine, _, _ := strings.Cut(raw, "\n")
	firstLine = strings.TrimSpace(firstLine)
	if m := semverPattern.FindStringSubmatch(firstLine); len(m) == 4 {
		return p.name, fmt.Sprintf("%s.%s.%s", m[1], m[2], m[3])
	}
	return p.name, firstLine
}

func (p *cliProvider) resolveCLIPath() (string, error) {
	cmd := ""
	if p.envPathVar != "" {
		cmd = os.Getenv(p.envPathVar)
	}
	if cmd == "" {
		cmd = p.cmdName
	}
	if cmd == "" {
		cmd = p.name
	}
	path, err := exec.LookPath(cmd)
	if err != nil {
		return "", fmt.Errorf("%s executable not found: %w", p.name, err)
	}
	return path, nil
}
