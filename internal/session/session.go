package session

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kokistudios/card/internal/repo"
	"github.com/kokistudios/card/internal/store"
)

type SessionStatus string

type SessionMode string

const (
	ModeStandard SessionMode = "standard" // Full 7-phase pipeline
	ModeAsk      SessionMode = "ask"      // Conversational, no phases (can be promoted)
)

type ExecutionAttemptRecord struct {
	Attempt int       `yaml:"attempt"`
	Started time.Time `yaml:"started"`
	Outcome string    `yaml:"outcome"` // "completed", "failed_verification", "interrupted"
	Reason  string    `yaml:"reason,omitempty"`
}

const (
	StatusStarted       SessionStatus = "started"
	StatusInvestigating SessionStatus = "investigating"
	StatusPlanning      SessionStatus = "planning"
	StatusReviewing     SessionStatus = "reviewing"
	StatusApproved      SessionStatus = "approved"
	StatusExecuting     SessionStatus = "executing"
	StatusVerifying     SessionStatus = "verifying"
	StatusSimplifying   SessionStatus = "simplifying"
	StatusRecording     SessionStatus = "recording"
	StatusConcluding    SessionStatus = "concluding" // Optional ad-hoc phase for post-session review
	StatusCompleted     SessionStatus = "completed"
	StatusPaused        SessionStatus = "paused"
	StatusAbandoned     SessionStatus = "abandoned"
)

type Session struct {
	ID             string        `yaml:"id"`
	Description    string        `yaml:"description"`
	Context        string        `yaml:"context"`        // operator-provided context (from --context flag, required)
	Mode           SessionMode   `yaml:"mode,omitempty"` // session mode: standard, ask, or research
	Status         SessionStatus `yaml:"status"`
	PreviousStatus SessionStatus `yaml:"previous_status,omitempty"` // stored when paused
	Repos          []string      `yaml:"repos"`
	CreatedAt      time.Time     `yaml:"created_at"`
	UpdatedAt      time.Time     `yaml:"updated_at"`
	PausedAt       *time.Time    `yaml:"paused_at,omitempty"`
	CompletedAt    *time.Time    `yaml:"completed_at,omitempty"`

	Author       string     `yaml:"author,omitempty"`
	Imported     bool       `yaml:"imported,omitempty"`
	ImportedFrom string     `yaml:"imported_from,omitempty"`
	ImportedAt   *time.Time `yaml:"imported_at,omitempty"`

	Supersedes []string `yaml:"supersedes,omitempty"`
	BuildsOn   []string `yaml:"builds_on,omitempty"`

	ExecutionHistory []ExecutionAttemptRecord `yaml:"execution_history,omitempty"`
}

func (s *Session) AddExecutionAttempt(outcome, reason string) {
	attempt := ExecutionAttemptRecord{
		Attempt: len(s.ExecutionHistory) + 1,
		Started: time.Now().UTC(),
		Outcome: outcome,
		Reason:  reason,
	}
	s.ExecutionHistory = append(s.ExecutionHistory, attempt)
}

func (s *Session) UpdateLastExecutionOutcome(outcome, reason string) {
	if len(s.ExecutionHistory) > 0 {
		s.ExecutionHistory[len(s.ExecutionHistory)-1].Outcome = outcome
		s.ExecutionHistory[len(s.ExecutionHistory)-1].Reason = reason
	}
}

func getGitAuthor() string {
	cmd := exec.Command("git", "config", "user.email")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

var validTransitions = map[SessionStatus][]SessionStatus{
	StatusStarted:       {StatusInvestigating},
	StatusInvestigating: {StatusPlanning},
	StatusPlanning:      {StatusReviewing},
	StatusReviewing:     {StatusApproved},
	StatusApproved:      {StatusExecuting},
	StatusExecuting:     {StatusVerifying},
	StatusVerifying:     {StatusSimplifying, StatusExecuting}, // can loop back to execute
	StatusSimplifying:   {StatusRecording},
	StatusRecording:     {StatusCompleted},
	StatusConcluding:    {StatusCompleted}, // conclude returns to completed
}

var terminalStatuses = map[SessionStatus]bool{
	StatusCompleted: true,
	StatusAbandoned: true,
}

func GenerateID(description string) string {
	date := time.Now().Format("20060102")
	slug := slugify(description)
	suffix := randomHex(8)
	return fmt.Sprintf("%s-%s-%s", date, slug, suffix)
}

func slugify(s string) string {
	s = strings.ToLower(s)
	s = regexp.MustCompile(`[^a-z0-9\s-]`).ReplaceAllString(s, "")
	s = regexp.MustCompile(`[\s]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 72 {
		s = s[:72]
		s = strings.TrimRight(s, "-")
	}
	if s == "" {
		s = "session"
	}
	return s
}

func randomHex(n int) string {
	b := make([]byte, (n+1)/2)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)[:n]
}

type CreateOption func(*createOptions)

type createOptions struct {
	context string
	mode    SessionMode
}

func WithContext(ctx string) CreateOption {
	return func(o *createOptions) {
		o.context = ctx
	}
}

func WithMode(mode SessionMode) CreateOption {
	return func(o *createOptions) {
		o.mode = mode
	}
}

func Create(s *store.Store, description string, repoIDs []string, opts ...CreateOption) (*Session, error) {
	if len(repoIDs) == 0 {
		return nil, fmt.Errorf("at least one repo is required")
	}

	for _, id := range repoIDs {
		if _, err := repo.Get(s, id); err != nil {
			return nil, fmt.Errorf("repo not found: %s", id)
		}
	}

	id := GenerateID(description)
	sessDir := s.Path("sessions", id)
	for {
		if _, err := os.Stat(sessDir); err != nil {
			break // doesn't exist, good
		}
		id = GenerateID(description)
		sessDir = s.Path("sessions", id)
	}

	var options createOptions
	for _, o := range opts {
		o(&options)
	}

	now := time.Now().UTC()
	sess := &Session{
		ID:          id,
		Description: description,
		Context:     options.context,
		Mode:        options.mode,
		Status:      StatusStarted,
		Repos:       repoIDs,
		CreatedAt:   now,
		UpdatedAt:   now,
		Author:      getGitAuthor(),
	}

	if err := os.MkdirAll(sessDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create session directory: %w", err)
	}

	if err := save(s, sess); err != nil {
		return nil, err
	}

	return sess, nil
}

func CreateAsk(s *store.Store, description string, repoIDs []string) (*Session, error) {
	return Create(s, description, repoIDs, WithMode(ModeAsk))
}

func PromoteToStandard(s *store.Store, sessionID string, startPhase string) error {
	sess, err := Get(s, sessionID)
	if err != nil {
		return err
	}
	if sess.Mode != ModeAsk {
		return fmt.Errorf("only ask sessions can be promoted (session %s has mode %s)", sessionID, sess.Mode)
	}

	sess.Mode = ModeStandard

	// Set status based on start phase
	switch startPhase {
	case "plan":
		// Start at investigating — orchestrator will transition to planning after loading context
		sess.Status = StatusInvestigating
	case "execute":
		// Skip to approved, ready for execute phase
		sess.Status = StatusApproved
	default: // "investigate" or empty
		sess.Status = StatusStarted
	}

	sess.UpdatedAt = time.Now().UTC()
	return save(s, sess)
}

func UpdateDescription(s *store.Store, sessionID string, description string) error {
	sess, err := Get(s, sessionID)
	if err != nil {
		return err
	}
	sess.Description = description
	sess.UpdatedAt = time.Now().UTC()
	return save(s, sess)
}

func Transition(s *store.Store, id string, newStatus SessionStatus) error {
	sess, err := Get(s, id)
	if err != nil {
		return err
	}

	if sess.Status == StatusCompleted && newStatus == StatusConcluding {
		sess.Status = newStatus
		sess.UpdatedAt = time.Now().UTC()
		return save(s, sess)
	}

	if terminalStatuses[sess.Status] {
		return fmt.Errorf("session %s is %s and cannot transition", id, sess.Status)
	}

	if sess.Status == StatusPaused {
		return fmt.Errorf("session %s is paused — resume it first", id)
	}

	// Allow self-transition (retry same phase)
	if sess.Status == newStatus {
		return nil
	}

	allowed := validTransitions[sess.Status]
	valid := false
	for _, a := range allowed {
		if a == newStatus {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("invalid transition: %s → %s", sess.Status, newStatus)
	}

	sess.Status = newStatus
	sess.UpdatedAt = time.Now().UTC()
	if newStatus == StatusCompleted {
		now := time.Now().UTC()
		sess.CompletedAt = &now
	}
	return save(s, sess)
}

func Pause(s *store.Store, id string) error {
	sess, err := Get(s, id)
	if err != nil {
		return err
	}
	if terminalStatuses[sess.Status] {
		return fmt.Errorf("session %s is %s and cannot be paused", id, sess.Status)
	}
	if sess.Status == StatusPaused {
		return fmt.Errorf("session %s is already paused", id)
	}

	sess.PreviousStatus = sess.Status
	sess.Status = StatusPaused
	now := time.Now().UTC()
	sess.PausedAt = &now
	sess.UpdatedAt = now
	return save(s, sess)
}

// Resume resumes a paused or stuck session.
// Paused sessions restore their previous status.
// Active sessions (stuck due to crash/interrupt) are returned as-is for re-execution.
func Resume(s *store.Store, id string) error {
	sess, err := Get(s, id)
	if err != nil {
		return err
	}
	if terminalStatuses[sess.Status] {
		return fmt.Errorf("session %s is %s and cannot be resumed", id, sess.Status)
	}

	if sess.Status == StatusPaused {
		sess.Status = sess.PreviousStatus
		sess.PreviousStatus = ""
		sess.UpdatedAt = time.Now().UTC()
		return save(s, sess)
	}

	// Active status (stuck from crash/interrupt) — no state change needed,
	// the orchestrator will pick up from the current phase.
	return nil
}

func Abandon(s *store.Store, id string) error {
	sess, err := Get(s, id)
	if err != nil {
		return err
	}
	if terminalStatuses[sess.Status] {
		return fmt.Errorf("session %s is already %s", id, sess.Status)
	}

	sess.Status = StatusAbandoned
	sess.UpdatedAt = time.Now().UTC()
	return save(s, sess)
}

func Complete(s *store.Store, id string) error {
	return Transition(s, id, StatusCompleted)
}

func AddRepos(s *store.Store, id string, newRepoIDs []string) error {
	sess, err := Get(s, id)
	if err != nil {
		return err
	}

	existing := make(map[string]bool)
	for _, r := range sess.Repos {
		existing[r] = true
	}

	for _, r := range newRepoIDs {
		if !existing[r] {
			sess.Repos = append(sess.Repos, r)
			existing[r] = true
		}
	}

	sess.UpdatedAt = time.Now().UTC()
	return save(s, sess)
}

// Update saves changes to an existing session without transition logic.
// Updates the UpdatedAt timestamp automatically.
func Update(s *store.Store, sess *Session) error {
	sess.UpdatedAt = time.Now().UTC()
	return save(s, sess)
}

func Get(s *store.Store, id string) (*Session, error) {
	p := s.Path("sessions", id, "session.yaml")
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	var sess Session
	if err := yaml.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("invalid session file: %w", err)
	}
	return &sess, nil
}

func List(s *store.Store) ([]Session, error) {
	sessionsDir := s.Path("sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("cannot read sessions directory: %w", err)
	}

	var sessions []Session
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sess, err := Get(s, e.Name())
		if err != nil {
			continue
		}
		sessions = append(sessions, *sess)
	}
	return sessions, nil
}

func GetActive(s *store.Store) ([]Session, error) {
	all, err := List(s)
	if err != nil {
		return nil, err
	}
	var active []Session
	for _, sess := range all {
		if !terminalStatuses[sess.Status] {
			active = append(active, sess)
		}
	}
	return active, nil
}

func save(s *store.Store, sess *Session) error {
	data, err := yaml.Marshal(sess)
	if err != nil {
		return fmt.Errorf("failed to marshal session: %w", err)
	}
	p := s.Path("sessions", sess.ID, "session.yaml")
	if err := os.WriteFile(p, data, 0644); err != nil {
		return fmt.Errorf("failed to write session file: %w", err)
	}
	return writeSummaryMd(s, sess)
}

func writeSummaryMd(s *store.Store, sess *Session) error {
	var buf bytes.Buffer
	buf.WriteString("---\n")
	fm, err := yaml.Marshal(sess)
	if err != nil {
		return nil // non-fatal, session.yaml is the source of truth
	}
	buf.Write(fm)
	buf.WriteString("---\n\n")

	buf.WriteString(fmt.Sprintf("# %s\n\n", sess.Description))

	statusTag := strings.ToUpper(string(sess.Status))
	buf.WriteString(fmt.Sprintf("#%s\n\n", statusTag))

	if len(sess.Repos) > 0 {
		buf.WriteString("## Repos\n\n")
		for _, repoID := range sess.Repos {
			r, err := repo.Get(s, repoID)
			if err == nil {
				linkName := strings.TrimSuffix(r.Filename(), ".md")
				buf.WriteString(fmt.Sprintf("- [[%s]]\n", linkName))
			} else {
				buf.WriteString(fmt.Sprintf("- [[%s]]\n", repoID))
			}
		}
		buf.WriteString("\n")
	}

	// Artifacts (session-level)
	// For completed sessions: only persistent artifacts (milestone_ledger, capsules)
	// For active sessions: include intermediate artifacts for Obsidian navigation
	sessionDir := s.Path("sessions", sess.ID)

	// Persistent artifacts (always shown if they exist)
	persistentArtifacts := []string{
		"milestone_ledger.md",
	}

	// Intermediate artifacts (only shown for active sessions, cleaned up on completion)
	intermediateArtifacts := []string{
		"investigation_summary.md",
		"implementation_guide.md",
		"execution_log.md",
		"verification_notes.md",
		"research_conclusions.md",
	}

	var foundArtifacts []string

	for _, af := range persistentArtifacts {
		if _, err := os.Stat(filepath.Join(sessionDir, af)); err == nil {
			foundArtifacts = append(foundArtifacts, strings.TrimSuffix(af, ".md"))
		}
	}

	if sess.Status != StatusCompleted && sess.Status != StatusAbandoned {
		for _, af := range intermediateArtifacts {
			if _, err := os.Stat(filepath.Join(sessionDir, af)); err == nil {
				foundArtifacts = append(foundArtifacts, strings.TrimSuffix(af, ".md"))
			}
		}

		// Add versioned artifacts (execution_log_v*.md, verification_notes_v*.md)
		entries, err := os.ReadDir(sessionDir)
		if err == nil {
			for _, entry := range entries {
				name := entry.Name()
				if (strings.HasPrefix(name, "execution_log_v") || strings.HasPrefix(name, "verification_notes_v")) &&
					strings.HasSuffix(name, ".md") {
					foundArtifacts = append(foundArtifacts, strings.TrimSuffix(name, ".md"))
				}
			}
		}
	}

	if len(foundArtifacts) > 0 {
		buf.WriteString("## Artifacts\n\n")
		for _, name := range foundArtifacts {
			buf.WriteString(fmt.Sprintf("- [[%s]]\n", name))
		}
		buf.WriteString("\n")
	}

	capsulesPath := s.Path("sessions", sess.ID, "capsules.md")
	if _, err := os.Stat(capsulesPath); err == nil {
		buf.WriteString("## Decisions\n\n")
		buf.WriteString("- [[capsules]]\n\n")
	}

	buf.WriteString("## Timeline\n\n")
	buf.WriteString(fmt.Sprintf("- **Created:** %s\n", sess.CreatedAt.Format("2006-01-02 15:04")))
	buf.WriteString(fmt.Sprintf("- **Updated:** %s\n", sess.UpdatedAt.Format("2006-01-02 15:04")))
	if sess.PausedAt != nil {
		buf.WriteString(fmt.Sprintf("- **Paused:** %s\n", sess.PausedAt.Format("2006-01-02 15:04")))
	}
	if sess.CompletedAt != nil {
		buf.WriteString(fmt.Sprintf("- **Completed:** %s\n", sess.CompletedAt.Format("2006-01-02 15:04")))
	}

	p := s.Path("sessions", sess.ID, sess.ID+".md")
	return os.WriteFile(p, buf.Bytes(), 0644)
}

// Call after cleaning up ephemeral artifacts to update the Artifacts section.
func RegenerateSummary(s *store.Store, sessionID string) error {
	sess, err := Get(s, sessionID)
	if err != nil {
		return err
	}
	return writeSummaryMd(s, sess)
}
