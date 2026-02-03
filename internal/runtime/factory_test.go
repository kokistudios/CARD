package runtime

import "testing"

func TestNewRuntime(t *testing.T) {
	tests := []struct {
		name        string
		runtimeType string
		path        string
		wantName    string
		wantErr     bool
	}{
		{"default", "", "", "claude", false},
		{"claude", "claude", "/bin/claude", "claude", false},
		{"codex", "codex", "/bin/codex", "codex", false},
		{"unknown", "wat", "", "", true},
	}

	for _, tt := range tests {
		rt, err := New(tt.runtimeType, tt.path)
		if tt.wantErr {
			if err == nil {
				t.Errorf("%s: expected error, got nil", tt.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tt.name, err)
			continue
		}
		if rt.Name() != tt.wantName {
			t.Errorf("%s: expected runtime %s, got %s", tt.name, tt.wantName, rt.Name())
		}
	}
}
