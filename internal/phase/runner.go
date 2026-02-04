package phase

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kokistudios/card/internal/artifact"
)

func hasPartialArtifact(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".md" {
			return true
		}
	}
	return false
}

func findArtifactInDir(dir string) (*artifact.Artifact, error) {
	a, _, err := findArtifactInDirWithPath(dir)
	return a, err
}

func findArtifactInDirWithPath(dir string) (*artifact.Artifact, string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, "", err
	}
	artifactNames := map[string]bool{
		"investigation_summary.md": true,
		"implementation_guide.md":  true,
		"execution_log.md":         true,
		"milestone_ledger.md":      true,
	}
	for _, e := range entries {
		if !e.IsDir() && artifactNames[e.Name()] {
			path := filepath.Join(dir, e.Name())
			a, err := artifact.Load(path)
			if err == nil {
				return a, path, nil
			}
		}
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".md" {
			path := filepath.Join(dir, e.Name())
			a, err := artifact.Load(path)
			if err == nil && a.Frontmatter.Session != "" {
				return a, path, nil
			}
		}
	}
	return nil, "", fmt.Errorf("no artifact files found in %s", dir)
}

// Include both MCP server names for compatibility with card vs card-dev binaries.
func phaseTools(p Phase) []string {
	mcpTools := []string{"mcp__card__*", "mcp__card-dev__*"}

	switch p {
	case PhaseInvestigate:
		return append([]string{"Read", "Glob", "Grep", "Bash(git log:git diff:git show:ls)", "Write"}, mcpTools...)
	case PhasePlan:
		return append([]string{"Read", "Glob", "Grep", "Write"}, mcpTools...)
	case PhaseReview:
		return append([]string{"Read", "Glob", "Grep", "Write"}, mcpTools...)
	case PhaseExecute:
		return nil // All tools allowed
	case PhaseVerify:
		return append([]string{"Read", "Glob", "Grep", "Bash(git log:git diff:git show:go test:go build:npm test:make)", "Write"}, mcpTools...)
	case PhaseSimplify:
		return nil // All tools allowed
	case PhaseRecord:
		return append([]string{"Read", "Glob", "Grep", "Bash(git log:git diff:git show)", "Write"}, mcpTools...)
	default:
		return nil
	}
}
