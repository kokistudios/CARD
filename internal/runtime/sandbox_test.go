package runtime

import "testing"

func TestMapToolsToSandbox(t *testing.T) {
	tests := []struct {
		name     string
		tools    []string
		expected string
	}{
		{"nil tools", nil, "danger-full-access"},
		{"read only", []string{"Read", "Glob"}, "read-only"},
		{"write", []string{"Read", "Write"}, "workspace-write"},
		{"bash", []string{"Bash"}, "workspace-write"},
	}

	for _, tt := range tests {
		if got := MapToolsToSandbox(tt.tools); got != tt.expected {
			t.Errorf("%s: expected %s, got %s", tt.name, tt.expected, got)
		}
	}
}
