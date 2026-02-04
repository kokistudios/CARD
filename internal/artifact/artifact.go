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

type ArtifactMeta struct {
	Session   string    `yaml:"session"`
	Repos     []string  `yaml:"repos,omitempty"`
	Phase     string    `yaml:"phase"`
	Timestamp time.Time `yaml:"timestamp"`
	Status    string    `yaml:"status"` // draft, final
}

type Artifact struct {
	Frontmatter ArtifactMeta
	Body        string
	RawContent  string
	FilePath    string
}

func Parse(raw []byte) (*Artifact, error) {
	content := string(raw)
	trimmed := strings.TrimSpace(content)

	if !strings.HasPrefix(trimmed, "---") {
		return &Artifact{
			Body:       content,
			RawContent: content,
		}, nil
	}

	rest := trimmed[3:]
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
	body := rest[endIdx+4:]
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
		return nil
	case "conclude":
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

func PhaseFilename(phase string) string {
	switch phase {
	case "investigate":
		return "investigation_summary.md"
	case "plan":
		return "implementation_guide.md"
	case "review":
		return "implementation_guide.md"
	case "execute":
		return "execution_log.md"
	case "verify":
		return "verification_notes.md"
	case "record":
		return "milestone_ledger.md"
	default:
		return phase + ".md"
	}
}

func Store(st *store.Store, sessionID, repoID string, a *Artifact) (string, error) {
	dir := st.Path("sessions", sessionID, "changes", repoID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create artifact directory: %w", err)
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
