package phase

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kokistudios/card/internal/artifact"
	"github.com/kokistudios/card/internal/capsule"
	"github.com/kokistudios/card/internal/change"
	"github.com/kokistudios/card/internal/claude"
	"github.com/kokistudios/card/internal/recall"
	"github.com/kokistudios/card/internal/repo"
	"github.com/kokistudios/card/internal/session"
	reposignal "github.com/kokistudios/card/internal/signal"
	"github.com/kokistudios/card/internal/store"
	"github.com/kokistudios/card/internal/ui"
)

// phaseIndex returns the 1-based index of a phase in the given sequence.
func phaseIndex(p Phase, phases []Phase) int {
	for i, ph := range phases {
		if ph == p {
			return i + 1
		}
	}
	return 0
}

// RunSession drives the full phase pipeline for a session.
func RunSession(s *store.Store, sess *session.Session) error {
	return RunSessionFromPhase(s, sess, "")
}

// RunSessionFromPhase drives the phase pipeline starting from a specific phase.
// If startPhase is empty, starts from the beginning.
func RunSessionFromPhase(s *store.Store, sess *session.Session, startPhase Phase) error {
	phases := SequenceFor(sess.Mode)
	totalPhases := len(phases)

	// Display CARD logo at session start (only when starting from beginning)
	if startPhase == "" {
		ui.LogoWithTagline("session mode")
	}

	// Set up signal handling for graceful Ctrl+C
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	// Skip phases before startPhase
	skipping := startPhase != ""

	for _, p := range phases {
		if skipping {
			if p == startPhase {
				skipping = false
			} else {
				continue
			}
		}

		// Execute+Verify run as a loop — skip verify in the main sequence,
		// it's handled inside the execute block.
		if p == PhaseVerify {
			continue
		}

		// Execute phase runs in a loop with verify
		if p == PhaseExecute {
			if err := runExecuteVerifyLoop(s, sess, sigCh, totalPhases); err != nil {
				return err
			}
			// Reload session after loop
			var err error
			sess, err = session.Get(s, sess.ID)
			if err != nil {
				return err
			}
			continue
		}

		// Transition session to this phase's status (skip if already there from resume)
		targetStatus := SessionStatus(p)

		if sess.Status != targetStatus {
			if err := session.Transition(s, sess.ID, targetStatus); err != nil {
				return fmt.Errorf("failed to transition to %s: %w", targetStatus, err)
			}
		}

		// Reload session after transition
		var err error
		sess, err = session.Get(s, sess.ID)
		if err != nil {
			return err
		}

		// Check for interrupt
		select {
		case <-sigCh:
			ui.Warning("Interrupted. Pausing session...")
			_ = session.Pause(s, sess.ID)
			ui.Info(fmt.Sprintf("Session %s paused. Resume with 'card session resume'.", sess.ID))
			return nil
		default:
		}

		if err := runSessionWidePhase(s, sess, p, totalPhases); err != nil {
			return err
		}

		// Reload session in case repos were added via signals
		sess, _ = session.Get(s, sess.ID)

		// Check if approval is needed
		needsApproval := NeedsApproval(p)
		if !needsApproval && p == PhaseSimplify && !s.Config.Session.AutoContinueSimplify {
			needsApproval = true
		}
		if !needsApproval && p == PhaseRecord && !s.Config.Session.AutoContinueRecord {
			needsApproval = true
		}
		if needsApproval {
			proceed, err := promptApproval(p)
			if err != nil {
				return err
			}
			if !proceed {
				ui.Info(fmt.Sprintf("Session paused after %s phase. Use 'card session resume' to continue.", p))
				return session.Pause(s, sess.ID)
			}
		}

		// Notify after each phase
		ui.Notify("CARD", fmt.Sprintf("%s phase complete", p))
	}

	// Clean up intermediate artifacts — only execution log and milestone ledger persist
	cleanupIntermediateArtifacts(s, sess)

	// Final transition to completed
	if err := session.Transition(s, sess.ID, session.StatusCompleted); err != nil {
		return fmt.Errorf("failed to mark session completed: %w", err)
	}

	// Reload for final output
	sess, _ = session.Get(s, sess.ID)
	ui.SessionComplete(sess.ID)
	ui.Notify("CARD", fmt.Sprintf("Session %s completed!", sess.ID))
	return nil
}

// runSessionWidePhase runs a phase once for the entire session,
// covering all repos in a single Claude Code invocation.
func runSessionWidePhase(s *store.Store, sess *session.Session, p Phase, totalPhases int) error {
	idx := phaseIndex(p, SequenceFor(sess.Mode))
	phaseName := string(p)

	// Use first repo as the working directory for Claude Code
	// (Claude can access other repos by absolute path)
	var primaryRepo *repo.Repo
	for _, repoID := range sess.Repos {
		r, err := repo.Get(s, repoID)
		if err != nil {
			return fmt.Errorf("repo %s not found: %w", repoID, err)
		}
		if primaryRepo == nil {
			primaryRepo = r
		}
	}
	if primaryRepo == nil {
		return fmt.Errorf("no repos in session")
	}

	ui.PhaseHeader(phaseName, idx, totalPhases, fmt.Sprintf("all repos (%d)", len(sess.Repos)), sess.ID)

	// Assemble recall context across ALL repos (only for investigate)
	recalledContext := ""
	if p == PhaseInvestigate {
		ui.Status("Assembling recall context across all repos...")
		var allRecallParts []string
		for _, repoID := range sess.Repos {
			r, err := repo.Get(s, repoID)
			if err != nil {
				continue
			}
			recallResult, recallErr := recall.Query(s, recall.RecallQuery{
				RepoID:      repoID,
				RepoPath:    r.LocalPath,
				MaxCapsules: s.Config.Recall.MaxContextBlocks,
			})
			if recallErr == nil && len(recallResult.Capsules) > 0 {
				formatted := recall.FormatContext(recallResult, s.Config.Recall.MaxContextTokens)
				allRecallParts = append(allRecallParts, fmt.Sprintf("### %s (%s)\n%s", r.Name, r.ID, formatted))
				ui.Logger.Info("Recall context assembled",
					"repo", r.Name,
					"decisions", len(recallResult.Capsules),
					"sessions", len(recallResult.Sessions))
			}
		}
		if len(allRecallParts) > 0 {
			recalledContext = strings.Join(allRecallParts, "\n\n---\n\n")
		}
	}

	workDir := filepath.Join(os.TempDir(), "card", sess.ID, phaseName)

	// Load prior artifacts for context
	priorArtifacts := loadPriorArtifacts(s, sess.ID, p)

	// For re-execution, also load versioned execution logs and verification notes
	if p == PhaseExecute && len(sess.ExecutionHistory) > 1 {
		priorArtifacts = append(priorArtifacts, loadVersionedExecutionHistory(s, sess.ID, len(sess.ExecutionHistory))...)
	}

	// Determine which template to use
	templatePhase := p
	if p == PhaseExecute && len(sess.ExecutionHistory) > 1 {
		templatePhase = "re-execute" // Use re-execute.md template
	}

	// Render session-wide prompt
	systemPrompt, err := RenderSessionWidePrompt(s, sess, templatePhase, workDir, recalledContext, priorArtifacts)
	if err != nil {
		return fmt.Errorf("failed to render session-wide prompt: %w", err)
	}

	initialMessage := RenderSessionWideInitialMessage(s, sess, p)
	allowedTools := phaseTools(p)

	if err := os.MkdirAll(workDir, 0755); err != nil {
		return fmt.Errorf("failed to create work directory: %w", err)
	}

	// Determine invocation mode
	mode := claude.ModeInteractive
	if p == PhasePlan || p == PhaseSimplify || p == PhaseRecord {
		mode = claude.ModeNonInteractive
	}

	ui.PhaseLaunch(phaseName, mode == claude.ModeInteractive)

	// For interactive mode, spinner shows briefly during startup then stops when Claude UI takes over.
	// For non-interactive mode, spinner runs throughout execution to show progress.
	var spin *ui.Spinner
	var onStart func()
	if mode == claude.ModeInteractive {
		spin = ui.NewSpinner("Starting Claude Code...")
		onStart = func() { spin.Stop() }
	} else {
		spin = ui.NewSpinner(fmt.Sprintf("Running %s phase...", strings.ToUpper(phaseName)))
	}

	err = claude.Invoke(claude.InvokeOptions{
		SystemPrompt:   systemPrompt,
		InitialMessage: initialMessage,
		WorkingDir:     primaryRepo.LocalPath,
		AllowedTools:   allowedTools,
		OutputDir:      workDir,
		ClaudePath:     s.Config.Claude.Path,
		Mode:           mode,
		OnStart:        onStart,
	})
	spin.Stop() // Always stop spinner after invoke completes
	if err != nil {
		if errors.Is(err, claude.ErrInterrupted) {
			ui.Warning("Interrupted. Pausing session...")
			_ = session.Pause(s, sess.ID)
			ui.Info(fmt.Sprintf("Session %s paused at %s. Resume with 'card session resume'.", sess.ID, phaseName))
			return nil
		}
		return fmt.Errorf("claude invocation failed for %s: %w", phaseName, err)
	}

	ui.PhaseComplete(phaseName)

	// Simplify and verify produce no artifact
	if !ProducesArtifact(p) {
		_ = os.RemoveAll(workDir)
		return nil
	}

	ui.Status("Ingesting artifact...")

	// Look for the artifact
	expectedFilename := artifact.PhaseFilename(phaseName)
	a, err := locateArtifact(workDir, primaryRepo.LocalPath, expectedFilename, phaseName)
	if err != nil {
		return err
	}

	// Ensure frontmatter
	if a.Frontmatter.Session == "" {
		a.Frontmatter.Session = sess.ID
	}
	if a.Frontmatter.Phase == "" {
		a.Frontmatter.Phase = phaseName
	}
	if a.Frontmatter.Timestamp.IsZero() {
		a.Frontmatter.Timestamp = time.Now().UTC()
	}
	if a.Frontmatter.Status == "" {
		a.Frontmatter.Status = "final"
	}
	// Set repos from session
	if len(a.Frontmatter.Repos) == 0 {
		a.Frontmatter.Repos = sess.Repos
	}

	// Validate
	if err := artifact.Validate(a, phaseName); err != nil {
		ui.Warning(fmt.Sprintf("Artifact validation: %v", err))
	}

	// Render for non-interactive phases
	if mode == claude.ModeNonInteractive && a.Body != "" {
		fmt.Fprintln(os.Stderr)
		ui.Info(fmt.Sprintf("── %s artifact ──", strings.ToUpper(phaseName)))
		ui.RenderMarkdown(a.Body)
	}

	// Store at session level
	storedPath, err := artifact.StoreSessionLevel(s, sess.ID, a)
	if err != nil {
		return fmt.Errorf("failed to store artifact: %w", err)
	}

	// Version execution logs and verification notes
	if p == PhaseExecute || p == PhaseVerify {
		if err := versionArtifact(s, sess, storedPath, p); err != nil {
			ui.Warning(fmt.Sprintf("Failed to version artifact: %v", err))
		}
	}

	// Extract capsules
	capsules, err := capsule.ExtractFromArtifact(a)
	if err != nil {
		ui.Warning(fmt.Sprintf("Capsule extraction failed: %v", err))
	} else if len(capsules) > 0 {
		for _, c := range capsules {
			if err := capsule.Store(s, c); err != nil {
				ui.Warning(fmt.Sprintf("Failed to store capsule %s: %v", c.ID, err))
			}
		}
		ui.Logger.Info("Decision capsules extracted", "count", len(capsules), "phase", phaseName)
	}

	// Check for repo request signals (investigate and execute)
	if p == PhaseInvestigate || p == PhaseExecute {
		sig, sigErr := reposignal.CheckRepoRequests(workDir)
		if sigErr != nil {
			ui.Warning(fmt.Sprintf("Repo signal check: %v", sigErr))
		} else if sig != nil {
			if _, addErr := reposignal.ProcessRepoRequests(s, sess, sig); addErr != nil {
				ui.Warning(fmt.Sprintf("Repo signal processing: %v", addErr))
			}
		}
	}

	_ = os.RemoveAll(workDir)
	ui.Info(fmt.Sprintf("Artifact stored at %s", storedPath))
	return nil
}

// locateArtifact finds and loads an artifact from the work directory or repo root.
func locateArtifact(workDir, repoPath, expectedFilename, phaseName string) (*artifact.Artifact, error) {
	// 1. Check workDir for exact filename
	artifactPath := filepath.Join(workDir, expectedFilename)
	if _, err := os.Stat(artifactPath); err == nil {
		a, err := artifact.Load(artifactPath)
		if err != nil {
			return nil, fmt.Errorf("failed to parse artifact: %w", err)
		}
		return a, nil
	}

	// 2. Check repo root for exact filename
	repoArtifactPath := filepath.Join(repoPath, expectedFilename)
	if _, err := os.Stat(repoArtifactPath); err == nil {
		a, err := artifact.Load(repoArtifactPath)
		if err != nil {
			return nil, fmt.Errorf("failed to parse artifact: %w", err)
		}
		_ = os.Rename(repoArtifactPath, artifactPath)
		return a, nil
	}

	// 3. Check workDir for any .md file
	if a, err := findArtifactInDir(workDir); err == nil {
		return a, nil
	}

	// 4. Check repo root for any .md artifact file (Claude sometimes ignores output dir)
	if a, path, err := findArtifactInDirWithPath(repoPath); err == nil {
		ui.Warning(fmt.Sprintf("Found artifact at %s instead of expected %s — Claude ignored output dir", path, artifactPath))
		_ = os.Rename(path, artifactPath)
		return a, nil
	}

	return nil, fmt.Errorf("no artifact found after %s phase — expected %s in %s or %s", phaseName, expectedFilename, workDir, repoPath)
}

// runExecuteVerifyLoop runs execute and verify phases in a loop until the user accepts.
func runExecuteVerifyLoop(s *store.Store, sess *session.Session, sigCh chan os.Signal, totalPhases int) error {
	firstIteration := true
	for {
		// Reload current status to handle resume from interrupted execute
		currentSess, err := session.Get(s, sess.ID)
		if err != nil {
			return err
		}

		if firstIteration {
			// If already executing (resumed from interrupt), skip transitions
			if currentSess.Status != session.StatusExecuting {
				if err := session.Transition(s, sess.ID, session.StatusApproved); err != nil {
					return fmt.Errorf("failed to transition to approved: %w", err)
				}
				if err := session.Transition(s, sess.ID, session.StatusExecuting); err != nil {
					return fmt.Errorf("failed to transition to executing: %w", err)
				}
			}
			firstIteration = false
		} else {
			if err := session.Transition(s, sess.ID, session.StatusExecuting); err != nil {
				return fmt.Errorf("failed to transition to executing: %w", err)
			}
		}

		// Record execution attempt
		sess, err = session.Get(s, sess.ID)
		if err != nil {
			return err
		}
		sess.AddExecutionAttempt("in_progress", "")
		if err := session.Update(s, sess); err != nil {
			return fmt.Errorf("failed to update execution attempts: %w", err)
		}

		sess, err = session.Get(s, sess.ID)
		if err != nil {
			return err
		}

		select {
		case <-sigCh:
			ui.Warning("Interrupted. Pausing session...")
			_ = session.Pause(s, sess.ID)
			ui.Info(fmt.Sprintf("Session %s paused. Resume with 'card session resume'.", sess.ID))
			return nil
		default:
		}

		if err := runSessionWidePhase(s, sess, PhaseExecute, totalPhases); err != nil {
			return err
		}
		// Reload in case repos were added via signals
		sess, _ = session.Get(s, sess.ID)

		if err := session.Transition(s, sess.ID, session.StatusVerifying); err != nil {
			return fmt.Errorf("failed to transition to verifying: %w", err)
		}
		sess, err = session.Get(s, sess.ID)
		if err != nil {
			return err
		}

		if err := runSessionWidePhase(s, sess, PhaseVerify, totalPhases); err != nil {
			return err
		}

		ui.Notify("CARD", "Verification complete — awaiting decision")

		decision, err := ui.VerifyDecision()
		if err != nil {
			return err
		}

		switch decision {
		case "accept":
			// Mark execution-phase capsules as verified
			count, err := capsule.VerifySessionCapsules(s, sess.ID, "execute")
			if err != nil {
				ui.Warning(fmt.Sprintf("Failed to verify capsules: %v", err))
			} else if count > 0 {
				ui.Logger.Info("Capsules verified", "count", count)
			}
			// Update execution outcome
			sess.UpdateLastExecutionOutcome("completed", "")
			_ = session.Update(s, sess)
			return nil
		case "reexecute":
			// Add challenge to execution-phase capsules
			addChallengeToExecutionCapsules(s, sess.ID, "Failed verification - re-executing")
			// Update execution outcome
			sess.UpdateLastExecutionOutcome("failed_verification", "Re-execution requested")
			_ = session.Update(s, sess)
			ui.Info("Re-executing with feedback incorporated...")
			continue
		case "pause":
			ui.Info("Session paused after verify phase. Use 'card session resume' to continue.")
			return session.Pause(s, sess.ID)
		}
	}
}

// promptApproval asks the developer whether to proceed to the next phase.
func promptApproval(completedPhase Phase) (bool, error) {
	nextPhase := ""
	switch completedPhase {
	case PhaseInvestigate:
		nextPhase = "planning"
	case PhasePlan:
		nextPhase = "execution"
	}

	return ui.ApprovalPrompt(string(completedPhase), nextPhase)
}

// loadPriorArtifacts loads artifacts from earlier phases for context.
// All artifacts are stored at session level.
func loadPriorArtifacts(s *store.Store, sessionID string, currentPhase Phase) []*artifact.Artifact {
	var prior []*artifact.Artifact
	sessionDir := s.Path("sessions", sessionID)
	seen := make(map[string]bool)

	for _, p := range Sequence() {
		if p == currentPhase {
			break
		}
		if !ProducesArtifact(p) {
			continue
		}
		filename := artifact.PhaseFilename(string(p))
		if seen[filename] {
			continue
		}
		seen[filename] = true

		sessionPath := filepath.Join(sessionDir, filename)
		if a, err := artifact.Load(sessionPath); err == nil {
			prior = append(prior, a)
		}
	}

	return prior
}

// loadVersionedExecutionHistory loads all versioned execution logs and verification notes
// up to (but not including) the current attempt.
func loadVersionedExecutionHistory(s *store.Store, sessionID string, currentAttempt int) []*artifact.Artifact {
	var history []*artifact.Artifact
	sessionDir := s.Path("sessions", sessionID)

	// Load versioned execution logs (v1, v2, ..., v(currentAttempt-1))
	for i := 1; i < currentAttempt; i++ {
		execPath := filepath.Join(sessionDir, fmt.Sprintf("execution_log_v%d.md", i))
		if a, err := artifact.Load(execPath); err == nil {
			history = append(history, a)
		}

		// Load corresponding verification notes
		verifyPath := filepath.Join(sessionDir, fmt.Sprintf("verification_notes_v%d.md", i))
		if a, err := artifact.Load(verifyPath); err == nil {
			history = append(history, a)
		}
	}

	return history
}

// versionArtifact creates a versioned copy of an execution log or verification notes artifact.
func versionArtifact(s *store.Store, sess *session.Session, artifactPath string, p Phase) error {
	sessionDir := s.Path("sessions", sess.ID)

	var versionedFilename string
	if p == PhaseExecute {
		versionedFilename = fmt.Sprintf("execution_log_v%d.md", len(sess.ExecutionHistory))
	} else if p == PhaseVerify {
		versionedFilename = fmt.Sprintf("verification_notes_v%d.md", len(sess.ExecutionHistory))
	} else {
		return nil // No versioning for other phases
	}

	versionedPath := filepath.Join(sessionDir, versionedFilename)

	// Copy the artifact to the versioned filename
	data, err := os.ReadFile(artifactPath)
	if err != nil {
		return fmt.Errorf("failed to read artifact for versioning: %w", err)
	}

	if err := os.WriteFile(versionedPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write versioned artifact: %w", err)
	}

	return nil
}

// CurrentPhase determines the current phase from a session's status.
func CurrentPhase(status session.SessionStatus) (Phase, error) {
	switch status {
	case session.StatusStarted, session.StatusInvestigating:
		return PhaseInvestigate, nil
	case session.StatusPlanning:
		return PhasePlan, nil
	case session.StatusReviewing:
		return PhaseReview, nil
	case session.StatusApproved, session.StatusExecuting:
		return PhaseExecute, nil
	case session.StatusVerifying:
		return PhaseVerify, nil
	case session.StatusSimplifying:
		return PhaseSimplify, nil
	case session.StatusRecording:
		return PhaseRecord, nil
	default:
		return "", fmt.Errorf("session status %s is not retryable", status)
	}
}

// saveChange re-saves a change record directly.
func saveChange(s *store.Store, ch *change.Change) {
	dir := s.Path("sessions", ch.SessionID, "changes", ch.RepoID)
	path := filepath.Join(dir, "change.yaml")

	data, err := yaml.Marshal(ch)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}

// addChallengeToExecutionCapsules adds a challenge to all execute-phase capsules.
func addChallengeToExecutionCapsules(s *store.Store, sessionID, reason string) {
	caps, err := capsule.List(s, capsule.Filter{SessionID: &sessionID})
	if err != nil {
		return
	}

	phase := "execute"
	for _, c := range caps {
		if c.Phase == phase {
			challenge := capsule.Challenge{
				Timestamp:  time.Now().UTC(),
				SessionID:  sessionID,
				Reason:     reason,
				Resolution: "pending",
			}
			_ = capsule.AddChallenge(s, c.ID, challenge)
		}
	}
}

// cleanupIntermediateArtifacts removes intermediate artifacts after session completion.
// Persists: milestone_ledger.md, capsules.md, session.yaml (the queryable engineering memory).
// Removes: ALL other artifacts (investigation, plan, execution logs, verification notes).
// Rationale: Capsules contain the queryable decisions; milestone ledger has file manifest,
// patterns, and iteration summary. Execution logs are verbose working documents with no
// queryable value beyond what's extracted to capsules and the ledger.
func cleanupIntermediateArtifacts(s *store.Store, sess *session.Session) {
	sessionDir := s.Path("sessions", sess.ID)

	// Remove all intermediate phase artifacts (everything except milestone_ledger and capsules)
	intermediatePhases := []string{"investigate", "plan", "review", "execute", "verify", "conclude"}
	for _, phase := range intermediatePhases {
		path := filepath.Join(sessionDir, artifact.PhaseFilename(phase))
		_ = os.Remove(path)
	}

	// Remove all versioned artifacts (execution_log_v*.md, verification_notes_v*.md)
	entries, err := os.ReadDir(sessionDir)
	if err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, "execution_log_v") && strings.HasSuffix(name, ".md") {
				_ = os.Remove(filepath.Join(sessionDir, name))
			}
			if strings.HasPrefix(name, "verification_notes_v") && strings.HasSuffix(name, ".md") {
				_ = os.Remove(filepath.Join(sessionDir, name))
			}
		}
	}
}
