package phase

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kokistudios/card/internal/artifact"
	"github.com/kokistudios/card/internal/capsule"
	"github.com/kokistudios/card/internal/session"
	"github.com/kokistudios/card/internal/store"
	"github.com/kokistudios/card/internal/ui"
)

// OrphanedArtifact represents an artifact found in temp that wasn't ingested.
type OrphanedArtifact struct {
	SessionID    string
	Phase        string
	TempPath     string
	ArtifactPath string
}

// statusToPhase maps session status to the phase that was running when it crashed.
var statusToPhase = map[session.SessionStatus]string{
	session.StatusInvestigating: "investigate",
	session.StatusPlanning:      "plan",
	session.StatusReviewing:     "review",
	session.StatusExecuting:     "execute",
	session.StatusVerifying:     "verify",
	session.StatusSimplifying:   "simplify",
	session.StatusRecording:     "record",
}

// CheckOrphanedArtifacts looks for sessions with orphaned artifacts in temp directories.
func CheckOrphanedArtifacts(s *store.Store) ([]OrphanedArtifact, error) {
	var orphans []OrphanedArtifact

	sessions, err := session.List(s)
	if err != nil {
		return nil, err
	}

	tempBase := filepath.Join(os.TempDir(), "card")

	for _, sess := range sessions {
		// Only check sessions in active phase states
		phaseName, ok := statusToPhase[sess.Status]
		if !ok {
			continue
		}

		// Skip phases that don't produce artifacts
		p := Phase(phaseName)
		if !ProducesArtifact(p) {
			continue
		}

		// Check if artifact already exists in session dir
		sessionDir := filepath.Join(store.Home(), "sessions", sess.ID)
		expectedFilename := artifact.PhaseFilename(phaseName)
		storedPath := filepath.Join(sessionDir, expectedFilename)
		if _, err := os.Stat(storedPath); err == nil {
			// Artifact already stored, not orphaned
			continue
		}

		// Check temp directory for orphaned artifact
		tempDir := filepath.Join(tempBase, sess.ID, phaseName)
		tempArtifactPath := filepath.Join(tempDir, expectedFilename)
		if _, err := os.Stat(tempArtifactPath); err == nil {
			orphans = append(orphans, OrphanedArtifact{
				SessionID:    sess.ID,
				Phase:        phaseName,
				TempPath:     tempDir,
				ArtifactPath: tempArtifactPath,
			})
			continue
		}

		// Also check for any .md file in the temp dir (fallback)
		if entries, err := os.ReadDir(tempDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() && filepath.Ext(e.Name()) == ".md" {
					orphans = append(orphans, OrphanedArtifact{
						SessionID:    sess.ID,
						Phase:        phaseName,
						TempPath:     tempDir,
						ArtifactPath: filepath.Join(tempDir, e.Name()),
					})
					break
				}
			}
		}
	}

	return orphans, nil
}

// RecoverOrphanedArtifact ingests an orphaned artifact and updates session status.
func RecoverOrphanedArtifact(s *store.Store, orphan OrphanedArtifact) error {
	// Load the session
	sess, err := session.Get(s, orphan.SessionID)
	if err != nil {
		return fmt.Errorf("failed to load session: %w", err)
	}

	// Load and parse the artifact
	a, err := artifact.Load(orphan.ArtifactPath)
	if err != nil {
		return fmt.Errorf("failed to parse artifact: %w", err)
	}

	// Ensure frontmatter
	if a.Frontmatter.Session == "" {
		a.Frontmatter.Session = sess.ID
	}
	if a.Frontmatter.Phase == "" {
		a.Frontmatter.Phase = orphan.Phase
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

	// Validate
	if err := artifact.Validate(a, orphan.Phase); err != nil {
		ui.Warning(fmt.Sprintf("Artifact validation: %v", err))
	}

	// Store at session level
	storedPath, err := artifact.StoreSessionLevel(s, sess.ID, a)
	if err != nil {
		return fmt.Errorf("failed to store artifact: %w", err)
	}
	ui.Logger.Info("Artifact recovered", "path", storedPath)

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
		ui.Logger.Info("Decision capsules extracted", "count", len(capsules), "phase", orphan.Phase)
	}

	// Transition session to next phase
	p := Phase(orphan.Phase)
	nextStatus := nextPhaseStatus(p)
	if nextStatus != "" {
		if err := session.Transition(s, sess.ID, nextStatus); err != nil {
			return fmt.Errorf("failed to transition session: %w", err)
		}
		ui.Logger.Info("Session status updated", "from", sess.Status, "to", nextStatus)
	}

	// Clean up temp directory
	_ = os.RemoveAll(orphan.TempPath)

	return nil
}

// nextPhaseStatus returns the session status after completing a phase.
func nextPhaseStatus(p Phase) session.SessionStatus {
	switch p {
	case PhaseInvestigate:
		return session.StatusPlanning
	case PhasePlan:
		return session.StatusReviewing
	case PhaseReview:
		return session.StatusApproved
	case PhaseExecute:
		return session.StatusVerifying
	case PhaseRecord:
		return session.StatusCompleted
	default:
		return ""
	}
}
