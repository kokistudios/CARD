package capsule

import (
	"testing"
)

func TestInferPrefix(t *testing.T) {
	tests := []struct {
		name     string
		tag      string
		expected TagPrefix
	}{
		// File detection
		{"file with path", "src/auth/guard.ts", PrefixFile},
		{"file with extension", "auth.go", PrefixFile},
		{"typescript file", "component.tsx", PrefixFile},
		{"python file", "main.py", PrefixFile},
		{"nested path", "internal/capsule/capsule.go", PrefixFile},

		// Table detection
		{"table with suffix", "workspace_users", PrefixTable},
		{"events table", "audit_events", PrefixTable},
		{"sessions table", "user_sessions", PrefixTable},
		{"snake_case table", "workspace_access_tokens", PrefixTable},

		// Service detection
		{"service suffix", "NotificationService", PrefixService},
		{"controller suffix", "UserController", PrefixService},
		{"handler suffix", "AuthHandler", PrefixService},
		{"repository suffix", "WorkspaceRepository", PrefixService},
		{"guard suffix", "AuthGuard", PrefixService},

		// API detection
		{"GET endpoint", "GET /api/users", PrefixAPI},
		{"POST endpoint", "POST /notifications", PrefixAPI},
		{"path with api", "/api/v1/workspaces", PrefixAPI},

		// Concept (default)
		{"simple concept", "authentication", PrefixConcept},
		{"domain concept", "authorization", PrefixConcept},
		{"feature name", "dark-mode", PrefixConcept},

		// Already prefixed (should return the existing prefix)
		{"already file prefixed", "file:src/auth.ts", PrefixFile},
		{"already concept prefixed", "concept:security", PrefixConcept},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := InferPrefix(tt.tag)
			if result != tt.expected {
				t.Errorf("InferPrefix(%q) = %q, want %q", tt.tag, result, tt.expected)
			}
		})
	}
}

func TestNormalizeTag(t *testing.T) {
	tests := []struct {
		tag      string
		expected string
	}{
		{"src/auth/guard.ts", "file:src/auth/guard.ts"},
		{"workspace_users", "table:workspace_users"},
		{"NotificationService", "service:NotificationService"},
		{"GET /api/users", "api:GET /api/users"},
		{"authentication", "concept:authentication"},
		// Already prefixed should not double-prefix
		{"file:src/auth.ts", "file:src/auth.ts"},
		{"concept:security", "concept:security"},
	}

	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			result := NormalizeTag(tt.tag)
			if result != tt.expected {
				t.Errorf("NormalizeTag(%q) = %q, want %q", tt.tag, result, tt.expected)
			}
		})
	}
}

func TestParseTag(t *testing.T) {
	tests := []struct {
		tag            string
		expectedPrefix TagPrefix
		expectedValue  string
	}{
		{"file:src/auth.ts", PrefixFile, "src/auth.ts"},
		{"table:users", PrefixTable, "users"},
		{"service:AuthService", PrefixService, "AuthService"},
		{"concept:security", PrefixConcept, "security"},
		{"api:GET /users", PrefixAPI, "GET /users"},
		{"unprefixed", "", "unprefixed"},
	}

	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			prefix, value := ParseTag(tt.tag)
			if prefix != tt.expectedPrefix {
				t.Errorf("ParseTag(%q) prefix = %q, want %q", tt.tag, prefix, tt.expectedPrefix)
			}
			if value != tt.expectedValue {
				t.Errorf("ParseTag(%q) value = %q, want %q", tt.tag, value, tt.expectedValue)
			}
		})
	}
}

func TestFilterByPrefix(t *testing.T) {
	tags := []string{
		"file:src/auth.ts",
		"file:src/user.ts",
		"table:users",
		"concept:security",
		"service:AuthService",
	}

	fileTags := FilterByPrefix(tags, PrefixFile)
	if len(fileTags) != 2 {
		t.Errorf("FilterByPrefix(file:) = %d tags, want 2", len(fileTags))
	}

	tableTags := FilterByPrefix(tags, PrefixTable)
	if len(tableTags) != 1 {
		t.Errorf("FilterByPrefix(table:) = %d tags, want 1", len(tableTags))
	}
}

func TestMatchesTagQuery(t *testing.T) {
	tags := []string{
		"file:src/auth/guard.ts",
		"concept:authorization",
		"service:NotificationService",
	}

	tests := []struct {
		query    string
		expected bool
	}{
		// Exact prefix match
		{"file:guard", true},
		{"file:auth", true},
		{"file:missing", false},

		// Prefix mismatch
		{"table:guard", false},

		// No prefix - matches any value
		{"auth", true},
		{"notification", true},
		{"missing", false},

		// Partial match
		{"guard.ts", true},
		{"Service", true},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			result := MatchesTagQuery(tags, tt.query)
			if result != tt.expected {
				t.Errorf("MatchesTagQuery(tags, %q) = %v, want %v", tt.query, result, tt.expected)
			}
		})
	}
}

func TestNormalizeTags(t *testing.T) {
	input := []string{
		"src/auth/guard.ts",
		"workspace_users",
		"authorization",
	}

	result := NormalizeTags(input)

	expected := []string{
		"file:src/auth/guard.ts",
		"table:workspace_users",
		"concept:authorization",
	}

	if len(result) != len(expected) {
		t.Fatalf("NormalizeTags returned %d tags, want %d", len(result), len(expected))
	}

	for i, tag := range result {
		if tag != expected[i] {
			t.Errorf("NormalizeTags[%d] = %q, want %q", i, tag, expected[i])
		}
	}
}
