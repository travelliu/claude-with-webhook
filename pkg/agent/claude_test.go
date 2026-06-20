package agent

import (
	"testing"
)

func TestBuildClaudeArgs(t *testing.T) {
	tests := []struct {
		name string
		opts ExecOptions
		want []string
	}{
		{
			name: "minimal",
			opts: ExecOptions{},
			want: []string{"-p", "--output-format", "stream-json", "--input-format", "stream-json", "--verbose", "--permission-mode", "bypassPermissions"},
		},
		{
			name: "with model and system prompt",
			opts: ExecOptions{Model: "claude-sonnet-4-6", SystemPrompt: "test prompt"},
			want: []string{"-p", "--output-format", "stream-json", "--input-format", "stream-json", "--verbose", "--permission-mode", "bypassPermissions", "--model", "claude-sonnet-4-6", "--append-system-prompt", "test prompt"},
		},
		{
			name: "with max turns",
			opts: ExecOptions{MaxTurns: 10},
			want: []string{"-p", "--output-format", "stream-json", "--input-format", "stream-json", "--verbose", "--permission-mode", "bypassPermissions", "--max-turns", "10"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildClaudeArgs(tt.opts)
			if len(got) != len(tt.want) {
				t.Errorf("len = %d, want %d", len(got), len(tt.want))
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("arg[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
