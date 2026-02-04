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
	"github.com/kokistudios/card/internal/repo"
	"github.com/kokistudios/card/internal/runtime"
	"github.com/kokistudios/card/internal/session"
	reposignal "github.com/kokistudios/card/internal/signal"
	"github.com/kokistudios/card/internal/store"
	"github.com/kokistudios/card/internal/ui"
)

func phaseIndex(p Phase, phases []Phase) int {
	for i, ph := range phases {
		if ph == p {
			return i + 1
		}
	}
	return 0
}

func RunSession(s *store.Store, sess *session.Session) error {
	return RunSessionFromPhase(s, sess, "")
}

func RunSessionFromPhase(s *store.Store, sess *session.Session, startPhase Phase) error {
	phases := SequenceFor(sess.Mode)
	totalPhases := len(phases)

	if startPhase == "" {
		ui.LogoWithTagline("session mode")
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

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

		if p == PhaseExecute {
			if err := runExecuteVerifyLoop(s, sess, sigCh, totalPhases); err != nil {
				return err
			}
			var err error
			sess, err = session.Get(s, sess.ID)
			if err != nil {
				return err
			}
			continue
		}

		targetStatus := SessionStatus(p)

		if sess.Status != targetStatus {
			if err := session.Transition(s, sess.ID, targetStatus); err != nil {
				return fmt.Errorf("failed to transition to %s: %w", targetStatus, err)
			}
		}

		var err error
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

		if err := runSessionWidePhase(s, sess, p, totalPhases); err != nil {
			return err
		}

		sess, _ = session.Get(s, sess.ID)

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

		ui.Notify("CARD", fmt.Sprintf("%s phase complete", p))
	}

	cleanupIntermediateArtifacts(s, sess)

	if err := session.RegenerateSummary(s, sess.ID); err != nil {
		ui.Warning(fmt.Sprintf("Failed to regenerate session summary: %v", err))
	}

	if err := session.Transition(s, sess.ID, session.StatusCompleted); err != nil {
		return fmt.Errorf("failed to mark session completed: %w", err)
	}

	sess, _ = session.Get(s, sess.ID)
	ui.SessionComplete(sess.ID)
	ui.Notify("CARD", fmt.Sprintf("Session %s completed!", sess.ID))
	return nil
}

func runSessionWidePhase(s *store.Store, sess *session.Session, p Phase, totalPhases int) error {
	idx := phaseIndex(p, SequenceFor(sess.Mode))
	phaseName := string(p)

	// Use first repo as the working directory for the runtime
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

	workDir := filepath.Join(os.TempDir(), "card", sess.ID, phaseName)

	priorArtifacts := loadPriorArtifacts(s, sess.ID, p)

	if p == PhaseExecute && len(sess.ExecutionHistory) > 1 {
		priorArtifacts = append(priorArtifacts, loadVersionedExecutionHistory(s, sess.ID, len(sess.ExecutionHistory))...)
	}

	templatePhase := p
	if p == PhaseExecute && len(sess.ExecutionHistory) > 1 {
		templatePhase = "re-execute" // Use re-execute.md template
	}

	systemPrompt, err := RenderSessionWidePrompt(s, sess, templatePhase, workDir, priorArtifacts)
	if err != nil {
		return fmt.Errorf("failed to render session-wide prompt: %w", err)
	}

	initialMessage := RenderSessionWideInitialMessage(s, sess, p)
	allowedTools := phaseTools(p)

	if err := os.MkdirAll(workDir, 0755); err != nil {
		return fmt.Errorf("failed to create work directory: %w", err)
	}

	_ = reposignal.ClearPhaseComplete(workDir)

	rt, err := runtime.New(s.Config.Runtime.Type, s.Config.Runtime.Path)
	if err != nil {
		return fmt.Errorf("failed to initialize runtime: %w", err)
	}

	mode := runtime.ModeInteractive
	if p == PhasePlan || p == PhaseSimplify || p == PhaseRecord {
		mode = runtime.ModeNonInteractive
	}

	ui.PhaseLaunch(phaseName, rt.Name(), mode == runtime.ModeInteractive)

	var spin *ui.Spinner
	var onStart func()
	if mode == runtime.ModeInteractive {
		spin = ui.NewSpinner(fmt.Sprintf("Starting %s...", strings.ToUpper(rt.Name())))
		onStart = func() { spin.Stop() }
	} else {
		spin = ui.NewSpinner(fmt.Sprintf("Running %s phase...", strings.ToUpper(phaseName)))
	}

	err = rt.Invoke(runtime.InvokeOptions{
		SystemPrompt:   systemPrompt,
		InitialMessage: initialMessage,
		WorkingDir:     primaryRepo.LocalPath,
		AllowedTools:   allowedTools,
		OutputDir:      workDir,
		Mode:           mode,
		OnStart:        onStart,
	})
	spin.Stop()
	if err != nil {
		if errors.Is(err, runtime.ErrPhaseComplete) {
			sig, sigErr := reposignal.CheckPhaseComplete(workDir)
			if sigErr == nil && sig != nil {
				switch sig.Status {
				case "complete":
					ui.PhaseComplete(phaseName)
				case "blocked":
					ui.Warning(fmt.Sprintf("Phase blocked: %s", sig.Summary))
					_ = session.Pause(s, sess.ID)
					ui.Info(fmt.Sprintf("Session %s paused due to blocking issue. Resume with 'card session resume'.", sess.ID))
					return nil
				case "needs_input":
					ui.Warning("Phase needs input - this is unexpected")
				}
			} else {
				ui.PhaseComplete(phaseName)
			}
			_ = reposignal.ClearPhaseComplete(workDir)
		} else if errors.Is(err, runtime.ErrInterrupted) {
			ui.Warning("Interrupted. Pausing session...")
			_ = session.Pause(s, sess.ID)
			ui.Info(fmt.Sprintf("Session %s paused at %s. Resume with 'card session resume'.", sess.ID, phaseName))
			return nil
		} else {
			return fmt.Errorf("%s invocation failed for %s: %w", rt.Name(), phaseName, err)
		}
	} else {
		ui.PhaseComplete(phaseName)
	}

	if !ProducesArtifact(p) {
		_ = os.RemoveAll(workDir)
		return nil
	}

	ui.Status("Ingesting artifact...")

	expectedFilename := artifact.PhaseFilename(phaseName)
	a, err := locateArtifact(s, sess.ID, workDir, primaryRepo.LocalPath, expectedFilename, phaseName)
	if err != nil {
		return err
	}

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
	if len(a.Frontmatter.Repos) == 0 {
		a.Frontmatter.Repos = sess.Repos
	}

	if err := artifact.Validate(a, phaseName); err != nil {
		ui.Warning(fmt.Sprintf("Artifact validation: %v", err))
	}

	if mode == runtime.ModeNonInteractive && a.Body != "" {
		fmt.Fprintln(os.Stderr)
		ui.Info(fmt.Sprintf("── %s artifact ──", strings.ToUpper(phaseName)))
		ui.RenderMarkdown(a.Body)
	}

	storedPath, err := artifact.StoreSessionLevel(s, sess.ID, a)
	if err != nil {
		return fmt.Errorf("failed to store artifact: %w", err)
	}

	if err := session.RegenerateSummary(s, sess.ID); err != nil {
		ui.Warning(fmt.Sprintf("Failed to update session summary: %v", err))
	}

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

func locateArtifact(s *store.Store, sessionID, workDir, repoPath, expectedFilename, phaseName string) (*artifact.Artifact, error) {
	artifactPath := filepath.Join(workDir, expectedFilename)
	if _, err := os.Stat(artifactPath); err == nil {
		a, err := artifact.Load(artifactPath)
		if err != nil {
			return nil, fmt.Errorf("failed to parse artifact: %w", err)
		}
		return a, nil
	}

	repoArtifactPath := filepath.Join(repoPath, expectedFilename)
	if _, err := os.Stat(repoArtifactPath); err == nil {
		a, err := artifact.Load(repoArtifactPath)
		if err != nil {
			return nil, fmt.Errorf("failed to parse artifact: %w", err)
		}
		_ = os.Rename(repoArtifactPath, artifactPath)
		return a, nil
	}

	if a, err := findArtifactInDir(workDir); err == nil {
		return a, nil
	}

	// Claude sometimes ignores output dir
	if a, path, err := findArtifactInDirWithPath(repoPath); err == nil {
		ui.Warning(fmt.Sprintf("Found artifact at %s instead of expected %s — Claude ignored output dir", path, artifactPath))
		_ = os.Rename(path, artifactPath)
		return a, nil
	}

	// MCP tools write artifacts directly to session store
	if s != nil && sessionID != "" {
		sessionArtifactPath := s.Path("sessions", sessionID, expectedFilename)
		if _, err := os.Stat(sessionArtifactPath); err == nil {
			a, err := artifact.Load(sessionArtifactPath)
			if err != nil {
				return nil, fmt.Errorf("failed to parse session artifact: %w", err)
			}
			ui.Warning(fmt.Sprintf("Found artifact in session store at %s — using it for ingestion", sessionArtifactPath))
			return a, nil
		}
	}

	return nil, fmt.Errorf("no artifact found after %s phase — expected %s in %s or %s", phaseName, expectedFilename, workDir, repoPath)
}

func runExecuteVerifyLoop(s *store.Store, sess *session.Session, sigCh chan os.Signal, totalPhases int) error {
	firstIteration := true
	for {
		currentSess, err := session.Get(s, sess.ID)
		if err != nil {
			return err
		}

		if firstIteration {
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
			versionPreviousIteration(s, sess)

			if err := session.Transition(s, sess.ID, session.StatusExecuting); err != nil {
				return fmt.Errorf("failed to transition to executing: %w", err)
			}
		}

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
			sess.UpdateLastExecutionOutcome("completed", "")
			_ = session.Update(s, sess)
			return nil
		case "reexecute":
			addChallengeToExecutionCapsules(s, sess.ID, "Failed verification - re-executing")
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

func loadPriorArtifacts(s *store.Store, sessionID string, currentPhase Phase) []*artifact.Artifact {
	var prior []*artifact.Artifact
	sessionDir := s.Path("sessions", sessionID)
	seen := make(map[string]bool)

	relevantForSimplify := map[Phase]bool{
		PhaseExecute: true,
		PhaseVerify:  true,
	}

	for _, p := range Sequence() {
		if p == currentPhase {
			break
		}
		if !ProducesArtifact(p) {
			continue
		}
		if currentPhase == PhaseSimplify && !relevantForSimplify[p] {
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

func loadVersionedExecutionHistory(s *store.Store, sessionID string, currentAttempt int) []*artifact.Artifact {
	var history []*artifact.Artifact
	sessionDir := s.Path("sessions", sessionID)

	for i := 1; i < currentAttempt; i++ {
		execPath := filepath.Join(sessionDir, fmt.Sprintf("execution_log_v%d.md", i))
		if a, err := artifact.Load(execPath); err == nil {
			history = append(history, a)
		}

		verifyPath := filepath.Join(sessionDir, fmt.Sprintf("verification_notes_v%d.md", i))
		if a, err := artifact.Load(verifyPath); err == nil {
			history = append(history, a)
		}
	}

	return history
}

// versionPreviousIteration versions the current execution_log.md and verification_notes.md
// before a re-execute overwrites them. Called at the START of re-execution, not after each phase.
// This prevents duplication where v1 would otherwise match the unversioned file.
func versionPreviousIteration(s *store.Store, sess *session.Session) {
	sessionDir := s.Path("sessions", sess.ID)
	versionNum := len(sess.ExecutionHistory)

	execPath := filepath.Join(sessionDir, "execution_log.md")
	if data, err := os.ReadFile(execPath); err == nil {
		versionedPath := filepath.Join(sessionDir, fmt.Sprintf("execution_log_v%d.md", versionNum))
		if err := os.WriteFile(versionedPath, data, 0644); err != nil {
			ui.Warning(fmt.Sprintf("Failed to version execution_log: %v", err))
		}
	}

	verifyPath := filepath.Join(sessionDir, "verification_notes.md")
	if data, err := os.ReadFile(verifyPath); err == nil {
		versionedPath := filepath.Join(sessionDir, fmt.Sprintf("verification_notes_v%d.md", versionNum))
		if err := os.WriteFile(versionedPath, data, 0644); err != nil {
			ui.Warning(fmt.Sprintf("Failed to version verification_notes: %v", err))
		}
	}
}

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
	case session.StatusConcluding:
		return PhaseConclude, nil
	default:
		return "", fmt.Errorf("session status %s is not retryable", status)
	}
}

func RunConcludePhase(s *store.Store, sess *session.Session) error {
	if sess.Status != session.StatusCompleted && sess.Status != session.StatusConcluding {
		return fmt.Errorf("conclude can only be run on completed or concluding sessions (current status: %s)", sess.Status)
	}

	ui.LogoWithTagline("conclude mode")
	ui.Info(fmt.Sprintf("Running conclude phase for session: %s", sess.ID))

	if sess.Status != session.StatusConcluding {
		if err := session.Transition(s, sess.ID, session.StatusConcluding); err != nil {
			return fmt.Errorf("failed to transition to concluding: %w", err)
		}
	}

	sess, err := session.Get(s, sess.ID)
	if err != nil {
		return err
	}

	phaseErr := runSessionWidePhase(s, sess, PhaseConclude, 1)

	if err := session.Transition(s, sess.ID, session.StatusCompleted); err != nil {
		return fmt.Errorf("failed to transition back to completed: %w", err)
	}

	if phaseErr != nil && !errors.Is(phaseErr, runtime.ErrPhaseComplete) {
		return phaseErr
	}

	ui.Info(fmt.Sprintf("Session %s conclude phase complete.", sess.ID))
	return nil
}

func saveChange(s *store.Store, ch *change.Change) {
	dir := s.Path("sessions", ch.SessionID, "changes", ch.RepoID)
	path := filepath.Join(dir, "change.yaml")

	data, err := yaml.Marshal(ch)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}

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

	intermediatePhases := []string{"investigate", "plan", "review", "execute", "verify", "conclude"}
	for _, phase := range intermediatePhases {
		path := filepath.Join(sessionDir, artifact.PhaseFilename(phase))
		_ = os.Remove(path)
	}

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
