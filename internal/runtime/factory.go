package runtime

import "fmt"

func New(runtimeType, path string) (Runtime, error) {
	switch runtimeType {
	case "", "claude":
		return &ClaudeRuntime{Path: path}, nil
	case "codex":
		return &CodexRuntime{Path: path}, nil
	default:
		return nil, fmt.Errorf("unknown runtime: %s", runtimeType)
	}
}
