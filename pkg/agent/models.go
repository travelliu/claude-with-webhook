package agent

// ProviderRegistry maps provider names to their Backend implementations.
type ProviderRegistry struct {
	providers map[string]Backend
}

func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{
		providers: make(map[string]Backend),
	}
}

func (r *ProviderRegistry) Register(b Backend) {
	r.providers[b.Name()] = b
}

func (r *ProviderRegistry) Get(name string) (Backend, bool) {
	b, ok := r.providers[name]
	return b, ok
}

func (r *ProviderRegistry) List() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}

// ProbeResult holds the probed info for a single provider CLI.
type ProbeResult struct {
	Path    string
	Name    string
	Version string
}

// Probe scans for available provider CLIs.
func (r *ProviderRegistry) Probe() map[string]ProbeResult {
	result := make(map[string]ProbeResult)
	for _, b := range r.providers {
		path, ok := b.CLIPath()
		if !ok {
			continue
		}
		if cp, ok := b.(interface{ CLIInfo() (string, string) }); ok {
			name, version := cp.CLIInfo()
			if name == "" {
				name = b.Name()
			}
			result[b.Name()] = ProbeResult{Path: path, Name: name, Version: version}
		} else {
			result[b.Name()] = ProbeResult{Path: path, Name: b.Name()}
		}
	}
	return result
}

// DefaultRegistry creates a registry with all built-in providers.
func DefaultRegistry() *ProviderRegistry {
	r := NewProviderRegistry()
	r.Register(NewClaudeProvider())
	return r
}
