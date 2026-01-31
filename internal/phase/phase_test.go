package phase

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kokistudios/card/internal/artifact"
	"github.com/kokistudios/card/internal/change"
	"github.com/kokistudios/card/internal/session"
	"github.com/kokistudios/card/internal/store"
)

func TestSequence(t *testing.T) {
	seq := Sequence()
	if len(seq) != 7 {
		t.Fatalf("expected 7 phases, got %d", len(seq))
	}
	expected := []Phase{PhaseInvestigate, PhasePlan, PhaseReview, PhaseExecute, PhaseVerify, PhaseSimplify, PhaseRecord}
	for i, p := range seq {
		if p != expected[i] {
			t.Errorf("phase[%d] = %q, want %q", i, p, expected[i])
		}
	}
}

func TestNeedsApproval(t *testing.T) {
	if !NeedsApproval(PhaseInvestigate) {
		t.Error("investigate should need approval")
	}
	if NeedsApproval(PhasePlan) {
		t.Error("plan should not need approval (review phase handles this)")
	}
	if NeedsApproval(PhaseExecute) {
		t.Error("execute should not need approval")
	}
	if NeedsApproval(PhaseSimplify) {
		t.Error("simplify should not need approval")
	}
	if NeedsApproval(PhaseRecord) {
		t.Error("record should not need approval")
	}
}

func TestProducesArtifact(t *testing.T) {
	if !ProducesArtifact(PhaseInvestigate) {
		t.Error("investigate should produce artifact")
	}
	if ProducesArtifact(PhaseSimplify) {
		t.Error("simplify should not produce artifact")
	}
}

func TestSessionStatus(t *testing.T) {
	cases := map[Phase]session.SessionStatus{
		PhaseInvestigate: session.StatusInvestigating,
		PhasePlan:        session.StatusPlanning,
		PhaseReview:      session.StatusReviewing,
		PhaseExecute:     session.StatusExecuting,
		PhaseVerify:      session.StatusVerifying,
		PhaseSimplify:    session.StatusSimplifying,
		PhaseRecord:      session.StatusRecording,
	}
	for p, want := range cases {
		got := SessionStatus(p)
		if got != want {
			t.Errorf("SessionStatus(%q) = %q, want %q", p, got, want)
		}
	}
}

func TestRenderSessionWidePrompt(t *testing.T) {
	tmp := t.TempDir()
	if err := store.Init(tmp, true); err != nil {
		t.Fatal(err)
	}
	s, _ := store.Load(tmp)

	sess := &session.Session{
		ID:          "20260127-test-abc1",
		Description: "Test session",
	}

	for _, p := range Sequence() {
		prompt, err := RenderSessionWidePrompt(s, sess, p, "/tmp/card-work", "", nil)
		if err != nil {
			t.Errorf("RenderSessionWidePrompt(%q) error: %v", p, err)
			continue
		}
		if prompt == "" {
			t.Errorf("RenderSessionWidePrompt(%q) returned empty prompt", p)
		}
		if !contains(prompt, "20260127-test-abc1") {
			t.Errorf("RenderSessionWidePrompt(%q) missing session ID in output", p)
		}
	}
}

func TestRenderSessionWideInitialMessage(t *testing.T) {
	tmp := t.TempDir()
	if err := store.Init(tmp, true); err != nil {
		t.Fatal(err)
	}
	s, _ := store.Load(tmp)

	sess := &session.Session{
		ID:          "test-session",
		Description: "Test",
	}

	for _, p := range Sequence() {
		msg := RenderSessionWideInitialMessage(s, sess, p)
		if msg == "" {
			t.Errorf("RenderSessionWideInitialMessage(%q) returned empty", p)
		}
	}
}

func TestLoadPriorArtifacts(t *testing.T) {
	tmp := t.TempDir()
	if err := store.Init(tmp, true); err != nil {
		t.Fatal(err)
	}
	s, _ := store.Load(tmp)

	sessionID := "test-sess"

	// Store an investigation artifact at session level
	a := &artifact.Artifact{
		Frontmatter: artifact.ArtifactMeta{
			Session:   sessionID,
			Phase:     "investigate",
			Timestamp: time.Now().UTC(),
			Status:    "final",
		},
		Body: "## Executive Summary\n\nTest.",
	}
	_, err := artifact.StoreSessionLevel(s, sessionID, a)
	if err != nil {
		t.Fatal(err)
	}

	// Loading prior artifacts for plan phase should find the investigation artifact
	prior := loadPriorArtifacts(s, sessionID, PhasePlan)
	if len(prior) != 1 {
		t.Fatalf("expected 1 prior artifact, got %d", len(prior))
	}
	if prior[0].Frontmatter.Phase != "investigate" {
		t.Errorf("expected investigate artifact, got %s", prior[0].Frontmatter.Phase)
	}

	// Loading prior artifacts for investigate phase should find none
	prior = loadPriorArtifacts(s, sessionID, PhaseInvestigate)
	if len(prior) != 0 {
		t.Errorf("expected 0 prior artifacts for investigate, got %d", len(prior))
	}
}

func TestPhaseTools(t *testing.T) {
	// Execute and simplify should have nil (all tools)
	if phaseTools(PhaseExecute) != nil {
		t.Error("execute should allow all tools (nil)")
	}
	if phaseTools(PhaseSimplify) != nil {
		t.Error("simplify should allow all tools (nil)")
	}
	// Investigate should have restricted tools
	tools := phaseTools(PhaseInvestigate)
	if tools == nil {
		t.Error("investigate should have restricted tools")
	}
}

func TestSaveChange(t *testing.T) {
	tmp := t.TempDir()
	if err := store.Init(tmp, true); err != nil {
		t.Fatal(err)
	}
	s, _ := store.Load(tmp)

	// Create the directory
	dir := s.Path("sessions", "test-sess", "changes", "abc123")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	ch := &change.Change{
		SessionID: "test-sess",
		RepoID:    "abc123",
		Status:    "started",
		Artifacts: []string{"/tmp/artifact.md"},
	}

	saveChange(s, ch)

	// Verify file was written
	path := filepath.Join(dir, "change.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("change file not written: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
