package phase

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kokistudios/card/internal/artifact"
)

// hasPartialArtifact checks if the work directory contains any .md files from a prior attempt.
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

// findArtifactInDir looks for any .md file in a directory.
func findArtifactInDir(dir string) (*artifact.Artifact, error) {
	a, _, err := findArtifactInDirWithPath(dir)
	return a, err
}

// findArtifactInDirWithPath looks for any .md file that looks like a CARD artifact in a directory.
// Returns the artifact and the path where it was found.
func findArtifactInDirWithPath(dir string) (*artifact.Artifact, string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, "", err
	}
	// Known artifact filenames to look for
	artifactNames := map[string]bool{
		"investigation_summary.md": true,
		"implementation_guide.md":  true,
		"execution_log.md":         true,
		"milestone_ledger.md":      true,
	}
	// First pass: look for known artifact names
	for _, e := range entries {
		if !e.IsDir() && artifactNames[e.Name()] {
			path := filepath.Join(dir, e.Name())
			a, err := artifact.Load(path)
			if err == nil {
				return a, path, nil
			}
		}
	}
	// Second pass: any .md file with CARD frontmatter
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

// phaseTools returns the allowed Claude Code tools for a phase.
func phaseTools(p Phase) []string {
	switch p {
	case PhaseInvestigate:
		return []string{"Read", "Glob", "Grep", "Bash(git log:git diff:git show:ls)", "Write"}
	case PhasePlan:
		return []string{"Read", "Glob", "Grep", "Write"}
	case PhaseReview:
		return []string{"Read", "Glob", "Grep", "Write"}
	case PhaseExecute:
		return nil
	case PhaseVerify:
		return []string{"Read", "Glob", "Grep", "Bash(git log:git diff:git show:go test:go build:npm test:make)", "Write"}
	case PhaseSimplify:
		return nil
	case PhaseRecord:
		return []string{"Read", "Glob", "Grep", "Bash(git log:git diff:git show)", "Write"}
	default:
		return nil
	}
}
