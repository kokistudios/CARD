package phase

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kokistudios/card/internal/artifact"
	"github.com/kokistudios/card/internal/session"
	"github.com/kokistudios/card/internal/store"
	"github.com/kokistudios/card/internal/ui"
)

type OrphanedArtifact struct {
	SessionID    string
	Phase        string
	TempPath     string
	ArtifactPath string
}

var statusToPhase = map[session.SessionStatus]string{
	session.StatusInvestigating: "investigate",
	session.StatusPlanning:      "plan",
	session.StatusReviewing:     "review",
	session.StatusExecuting:     "execute",
	session.StatusVerifying:     "verify",
	session.StatusSimplifying:   "simplify",
	session.StatusRecording:     "record",
}

func CheckOrphanedArtifacts(s *store.Store) ([]OrphanedArtifact, error) {
	var orphans []OrphanedArtifact

	sessions, err := session.List(s)
	if err != nil {
		return nil, err
	}

	tempBase := filepath.Join(os.TempDir(), "card")

	for _, sess := range sessions {
		phaseName, ok := statusToPhase[sess.Status]
		if !ok {
			continue
		}

		p := Phase(phaseName)
		if !ProducesArtifact(p) {
			continue
		}

		sessionDir := filepath.Join(store.Home(), "sessions", sess.ID)
		expectedFilename := artifact.PhaseFilename(phaseName)
		storedPath := filepath.Join(sessionDir, expectedFilename)
		if _, err := os.Stat(storedPath); err == nil {
			continue
		}

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

func RecoverOrphanedArtifact(s *store.Store, orphan OrphanedArtifact) error {
	sess, err := session.Get(s, orphan.SessionID)
	if err != nil {
		return fmt.Errorf("failed to load session: %w", err)
	}

	a, err := artifact.Load(orphan.ArtifactPath)
	if err != nil {
		return fmt.Errorf("failed to parse artifact: %w", err)
	}

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

	if err := artifact.Validate(a, orphan.Phase); err != nil {
		ui.Warning(fmt.Sprintf("Artifact validation: %v", err))
	}

	storedPath, err := artifact.StoreSessionLevel(s, sess.ID, a)
	if err != nil {
		return fmt.Errorf("failed to store artifact: %w", err)
	}
	ui.Logger.Info("Artifact recovered", "path", storedPath)

	p := Phase(orphan.Phase)
	nextStatus := nextPhaseStatus(p)
	if nextStatus != "" {
		if err := session.Transition(s, sess.ID, nextStatus); err != nil {
			return fmt.Errorf("failed to transition session: %w", err)
		}
		ui.Logger.Info("Session status updated", "from", sess.Status, "to", nextStatus)
	}

	_ = os.RemoveAll(orphan.TempPath)

	return nil
}

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
