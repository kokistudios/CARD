package phase

import (
	"bytes"
	"embed"
	"fmt"
	"os/exec"
	"strings"
	"text/template"

	"github.com/kokistudios/card/internal/artifact"
	"github.com/kokistudios/card/internal/repo"
	"github.com/kokistudios/card/internal/session"
	"github.com/kokistudios/card/internal/store"
)

//go:embed templates/*.md
var templateFS embed.FS

// repoInfo holds display data for a single repo in multi-repo templates.
type repoInfo struct {
	ID     string
	Name   string
	Path   string
	Remote string
	GitLog string
}

// executionAttemptInfo holds display data for an execution attempt.
type executionAttemptInfo struct {
	Attempt int
	Started string // formatted timestamp
	Outcome string
	Reason  string
}

// templateData holds the data injected into phase templates.
type templateData struct {
	SessionID            string
	Description          string
	OutputDir            string
	ArtifactFilename     string
	OperatorContext      string
	PriorArtifactContent string
	Repos                []repoInfo
	ExecutionAttempts    int
	ExecutionHistory     []executionAttemptInfo
	IsReExecution        bool // true if this is attempt 2+
	PriorAttemptOutcome  string
	PriorAttemptReason   string
}

// RenderSessionWidePrompt renders a phase template with all session repos.
func RenderSessionWidePrompt(s *store.Store, sess *session.Session, p Phase, workDir string, priorArtifacts []*artifact.Artifact) (string, error) {
	filename := fmt.Sprintf("templates/%s.md", string(p))
	raw, err := templateFS.ReadFile(filename)
	if err != nil {
		return "", fmt.Errorf("template not found for %s phase: %w", p, err)
	}

	repos := buildRepoList(s, sess)

	// Build execution history for template
	var execHistory []executionAttemptInfo
	for _, eh := range sess.ExecutionHistory {
		execHistory = append(execHistory, executionAttemptInfo{
			Attempt: eh.Attempt,
			Started: eh.Started.Format("2006-01-02 15:04"),
			Outcome: eh.Outcome,
			Reason:  eh.Reason,
		})
	}

	data := templateData{
		SessionID:         sess.ID,
		Description:       sess.Description,
		OutputDir:         workDir,
		ArtifactFilename:  artifact.PhaseFilename(string(p)),
		OperatorContext:   sess.Context,
		Repos:             repos,
		ExecutionAttempts: len(sess.ExecutionHistory),
		ExecutionHistory:  execHistory,
		IsReExecution:     len(sess.ExecutionHistory) > 1,
	}

	// Set prior attempt info for re-executions
	if len(sess.ExecutionHistory) > 1 {
		lastAttempt := sess.ExecutionHistory[len(sess.ExecutionHistory)-1]
		data.PriorAttemptOutcome = lastAttempt.Outcome
		data.PriorAttemptReason = lastAttempt.Reason
	}

	// Assemble prior artifact content
	if len(priorArtifacts) > 0 {
		var parts []string
		for _, a := range priorArtifacts {
			if a != nil && a.Body != "" {
				header := fmt.Sprintf("### %s\n", artifact.PhaseFilename(a.Frontmatter.Phase))
				parts = append(parts, header+a.Body)
			}
		}
		data.PriorArtifactContent = strings.Join(parts, "\n\n---\n\n")
	}

	tmpl, err := template.New(string(p)).Parse(string(raw))
	if err != nil {
		return "", fmt.Errorf("failed to parse %s template: %w", p, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render %s template: %w", p, err)
	}

	return buf.String(), nil
}

// RenderSessionWideInitialMessage creates the initial message for a session-wide phase.
func RenderSessionWideInitialMessage(s *store.Store, sess *session.Session, p Phase) string {
	phaseLabel := strings.ToUpper(string(p)[:1]) + string(p)[1:]

	msg := fmt.Sprintf("CARD\n\n%s for session: %s\n\nDescription: %s", phaseLabel, sess.ID, sess.Description)

	if sess.Context != "" {
		msg += fmt.Sprintf("\n\n## Operator-Provided Context\n\n%s", sess.Context)
	}

	msg += "\n\n## Repositories in this session\n"
	for _, repoID := range sess.Repos {
		r, err := repo.Get(s, repoID)
		if err != nil {
			msg += fmt.Sprintf("\n- %s (unable to load)\n", repoID)
			continue
		}
		msg += fmt.Sprintf("\n### %s (%s)\n- Path: %s\n- Remote: %s\n- Recent git log:\n%s\n",
			r.Name, r.ID, r.LocalPath, r.RemoteURL, getGitLog(r.LocalPath))
	}

	// For non-interactive phases, add "Go" to trigger the agent to start
	// (templates instruct agents to wait for "Go" before beginning work)
	if p == PhasePlan || p == PhaseSimplify || p == PhaseRecord {
		msg += "\n\nGo"
	}

	return msg
}

// buildRepoList assembles repoInfo for all repos in a session.
func buildRepoList(s *store.Store, sess *session.Session) []repoInfo {
	var repos []repoInfo
	for _, repoID := range sess.Repos {
		r, err := repo.Get(s, repoID)
		if err != nil {
			continue
		}
		repos = append(repos, repoInfo{
			ID:     r.ID,
			Name:   r.Name,
			Path:   r.LocalPath,
			Remote: r.RemoteURL,
			GitLog: getGitLog(r.LocalPath),
		})
	}
	return repos
}

func getGitLog(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "log", "--oneline", "-20")
	out, err := cmd.Output()
	if err != nil {
		return "(unable to read git log)"
	}
	return strings.TrimSpace(string(out))
}
