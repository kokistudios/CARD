package phase

import (
	"github.com/kokistudios/card/internal/session"
)

// Phase represents a named phase in the CARD pipeline.
type Phase string

const (
	PhaseInvestigate Phase = "investigate"
	PhasePlan        Phase = "plan"
	PhaseReview      Phase = "review"
	PhaseExecute     Phase = "execute"
	PhaseVerify      Phase = "verify"
	PhaseSimplify    Phase = "simplify"
	PhaseRecord      Phase = "record"
	PhaseConclude    Phase = "conclude" // Research mode only
)

// Sequence returns the ordered list of phases for standard sessions.
func Sequence() []Phase {
	return []Phase{PhaseInvestigate, PhasePlan, PhaseReview, PhaseExecute, PhaseVerify, PhaseSimplify, PhaseRecord}
}

// QuickfixSequence returns the phases for quickfix sessions.
// Skips investigate/plan/review - goes straight to execute.
func QuickfixSequence() []Phase {
	return []Phase{PhaseExecute, PhaseVerify, PhaseSimplify, PhaseRecord}
}

// ResearchSequence returns the phases for research sessions.
// No code execution - just investigate, conclude, and record.
func ResearchSequence() []Phase {
	return []Phase{PhaseInvestigate, PhaseConclude, PhaseRecord}
}

// SequenceFor returns the appropriate phase sequence for a session mode.
func SequenceFor(mode session.SessionMode) []Phase {
	switch mode {
	case session.ModeQuickfix:
		return QuickfixSequence()
	case session.ModeResearch:
		return ResearchSequence()
	default:
		return Sequence()
	}
}

// NeedsApproval returns true if the developer must approve before proceeding past this phase.
// Only investigate needs a simple y/n gate. Review and verify handle their own interactive gates.
func NeedsApproval(p Phase) bool {
	return p == PhaseInvestigate
}

// ProducesArtifact returns true if this phase produces an artifact file.
func ProducesArtifact(p Phase) bool {
	return p != PhaseSimplify
}

// SessionStatus maps a phase to the session status when that phase is active.
func SessionStatus(p Phase) session.SessionStatus {
	switch p {
	case PhaseInvestigate:
		return session.StatusInvestigating
	case PhasePlan:
		return session.StatusPlanning
	case PhaseExecute:
		return session.StatusExecuting
	case PhaseReview:
		return session.StatusReviewing
	case PhaseVerify:
		return session.StatusVerifying
	case PhaseSimplify:
		return session.StatusSimplifying
	case PhaseRecord:
		return session.StatusRecording
	case PhaseConclude:
		return session.StatusConcluding
	default:
		return session.StatusStarted
	}
}
