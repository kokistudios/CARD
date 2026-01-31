package artifact

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kokistudios/card/internal/store"
)

// ArtifactMeta contains the YAML frontmatter metadata for an artifact.
type ArtifactMeta struct {
	Session   string    `yaml:"session"`
	Repos     []string  `yaml:"repos,omitempty"`
	Phase     string    `yaml:"phase"`
	Timestamp time.Time `yaml:"timestamp"`
	Status    string    `yaml:"status"` // draft, final
}

// Artifact represents a parsed markdown artifact with YAML frontmatter.
type Artifact struct {
	Frontmatter ArtifactMeta
	Body        string
	RawContent  string
	FilePath    string
}

// Parse splits a markdown document into YAML frontmatter and body.
// Frontmatter is delimited by --- lines at the start of the document.
func Parse(raw []byte) (*Artifact, error) {
	content := string(raw)
	trimmed := strings.TrimSpace(content)

	if !strings.HasPrefix(trimmed, "---") {
		// No frontmatter — treat entire content as body with empty metadata
		return &Artifact{
			Body:       content,
			RawContent: content,
		}, nil
	}

	// Find the closing ---
	rest := trimmed[3:] // skip opening ---
	rest = strings.TrimLeft(rest, " \t")
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	} else if len(rest) > 1 && rest[0] == '\r' && rest[1] == '\n' {
		rest = rest[2:]
	}

	endIdx := strings.Index(rest, "\n---")
	if endIdx == -1 {
		return nil, fmt.Errorf("unterminated frontmatter: missing closing ---")
	}

	fmRaw := rest[:endIdx]
	body := rest[endIdx+4:] // skip \n---
	// Trim leading newline from body
	body = strings.TrimLeft(body, "\r\n")

	var meta ArtifactMeta
	if err := yaml.Unmarshal([]byte(fmRaw), &meta); err != nil {
		return nil, fmt.Errorf("invalid frontmatter YAML: %w", err)
	}

	return &Artifact{
		Frontmatter: meta,
		Body:        body,
		RawContent:  content,
	}, nil
}

// Validate checks that an artifact meets phase-specific requirements.
func Validate(a *Artifact, phase string) error {
	if phase == "simplify" {
		return nil
	}
	if a == nil {
		return fmt.Errorf("artifact is nil")
	}

	body := strings.ToLower(a.Body)

	switch phase {
	case "investigate":
		if !strings.Contains(body, "## executive summary") && !strings.Contains(body, "## investigation summary") {
			return fmt.Errorf("investigation artifact missing expected sections (executive summary)")
		}
	case "plan", "review":
		if !strings.Contains(body, "## implementation steps") && !strings.Contains(body, "step") {
			return fmt.Errorf("plan artifact missing implementation steps")
		}
	case "execute":
		if !strings.Contains(body, "## execution") && !strings.Contains(body, "execution log") && !strings.Contains(body, "execution summary") {
			return fmt.Errorf("execution artifact missing execution log sections")
		}
	case "verify":
		if !strings.Contains(body, "verification outcome") && !strings.Contains(body, "issues identified") {
			return fmt.Errorf("verification artifact missing expected sections")
		}
	case "simplify":
		// Simplify produces no artifact — always valid
		return nil
	case "conclude":
		// Research mode conclusions artifact
		if !strings.Contains(body, "## findings") && !strings.Contains(body, "## conclusions") && !strings.Contains(body, "## recommendations") {
			return fmt.Errorf("conclude artifact missing expected sections (findings, conclusions, or recommendations)")
		}
	case "record":
		if !strings.Contains(body, "## summary") && !strings.Contains(body, "## file manifest") {
			return fmt.Errorf("record artifact missing expected ledger sections")
		}
	default:
		return fmt.Errorf("unknown phase: %s", phase)
	}

	return nil
}

// PhaseFilename returns the conventional filename for a phase's artifact.
func PhaseFilename(phase string) string {
	switch phase {
	case "investigate":
		return "investigation_summary.md"
	case "plan":
		return "implementation_guide.md"
	case "review":
		return "implementation_guide.md" // Review overwrites the plan with amendments
	case "execute":
		return "execution_log.md"
	case "verify":
		return "verification_notes.md"
	case "conclude":
		return "research_conclusions.md" // Research mode only
	case "record":
		return "milestone_ledger.md"
	default:
		return phase + ".md"
	}
}

// Store writes an artifact to its canonical location in CARD_HOME.
func Store(st *store.Store, sessionID, repoID string, a *Artifact) (string, error) {
	dir := st.Path("sessions", sessionID, "changes", repoID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create artifact directory: %w", err)
	}

	filename := PhaseFilename(a.Frontmatter.Phase)
	destPath := filepath.Join(dir, filename)

	// Build the file content with frontmatter
	var buf bytes.Buffer
	buf.WriteString("---\n")
	fm, err := yaml.Marshal(a.Frontmatter)
	if err != nil {
		return "", fmt.Errorf("failed to marshal frontmatter: %w", err)
	}
	buf.Write(fm)
	buf.WriteString("---\n\n")
	buf.WriteString(a.Body)

	if err := os.WriteFile(destPath, buf.Bytes(), 0644); err != nil {
		return "", fmt.Errorf("failed to write artifact: %w", err)
	}

	a.FilePath = destPath
	return destPath, nil
}

// StoreSessionLevel writes an artifact to the session directory (not per-repo).
// Used for session-wide phases like investigation.
func StoreSessionLevel(st *store.Store, sessionID string, a *Artifact) (string, error) {
	dir := st.Path("sessions", sessionID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create session directory: %w", err)
	}

	filename := PhaseFilename(a.Frontmatter.Phase)
	destPath := filepath.Join(dir, filename)

	var buf bytes.Buffer
	buf.WriteString("---\n")
	fm, err := yaml.Marshal(a.Frontmatter)
	if err != nil {
		return "", fmt.Errorf("failed to marshal frontmatter: %w", err)
	}
	buf.Write(fm)
	buf.WriteString("---\n\n")
	buf.WriteString(a.Body)

	if err := os.WriteFile(destPath, buf.Bytes(), 0644); err != nil {
		return "", fmt.Errorf("failed to write artifact: %w", err)
	}

	a.FilePath = destPath
	return destPath, nil
}

// Load reads an artifact from a file path.
func Load(path string) (*Artifact, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read artifact: %w", err)
	}
	a, err := Parse(data)
	if err != nil {
		return nil, err
	}
	a.FilePath = path
	return a, nil
}
