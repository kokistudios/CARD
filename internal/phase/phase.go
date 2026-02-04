package phase

import (
	"github.com/kokistudios/card/internal/session"
)

type Phase string

const (
	PhaseInvestigate Phase = "investigate"
	PhasePlan        Phase = "plan"
	PhaseReview      Phase = "review"
	PhaseExecute     Phase = "execute"
	PhaseVerify      Phase = "verify"
	PhaseSimplify    Phase = "simplify"
	PhaseRecord      Phase = "record"
	PhaseConclude    Phase = "conclude" // Optional ad-hoc phase, not in standard Sequence()
)

func Sequence() []Phase {
	return []Phase{PhaseInvestigate, PhasePlan, PhaseReview, PhaseExecute, PhaseVerify, PhaseSimplify, PhaseRecord}
}

func AskSequence() []Phase {
	return []Phase{} // No phases — ask is conversational
}

func SequenceFor(mode session.SessionMode) []Phase {
	switch mode {
	case session.ModeAsk:
		return AskSequence()
	default:
		return Sequence()
	}
}

func NeedsApproval(p Phase) bool {
	return p == PhaseInvestigate
}

func ProducesArtifact(p Phase) bool {
	return p != PhaseSimplify
}

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
