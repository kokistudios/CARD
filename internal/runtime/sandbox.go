package runtime

import "strings"

// MapToolsToSandbox maps CARD tool restrictions to Codex sandbox levels.
func MapToolsToSandbox(tools []string) string {
	if tools == nil {
		return "danger-full-access"
	}
	hasWrite := false
	hasBash := false
	for _, t := range tools {
		if t == "Write" {
			hasWrite = true
		}
		if strings.HasPrefix(t, "Bash") {
			hasBash = true
		}
	}
	if hasWrite || hasBash {
		return "workspace-write"
	}
	return "read-only"
}
