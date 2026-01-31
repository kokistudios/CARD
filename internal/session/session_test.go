package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kokistudios/card/internal/store"
)

func setupStore(t *testing.T) *store.Store {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".card")
	if err := store.Init(dir, false); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	s, err := store.Load(dir)
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	return s
}

// createFakeRepo creates a minimal repo markdown file in the store.
func createFakeRepo(t *testing.T, s *store.Store, id string) {
	t.Helper()
	name := "testrepo-" + id
	content := fmt.Sprintf("---\nid: %s\nname: %s\n---\n\n# %s\n", id, name, strings.ToUpper(name))
	p := s.Path("repos", "REPO_"+strings.ToUpper(name)+".md")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("create fake repo: %v", err)
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{"Add auth feature", "add-auth-feature"},
		{"Fix BUG #123!", "fix-bug-123"},
		{"", "session"},
		{"a b c d e f g h i j k l m n o p q r s t u v w x y z extra long description", "a-b-c-d-e-f-g-h-i-j-k-l-m-n-o-p-q-r-s-t-u-v-w-x-y-z-extra-long-descripti"},
		{"  spaces  everywhere  ", "spaces-everywhere"},
	}
	for _, tc := range cases {
		got := slugify(tc.input)
		if got != tc.want {
			t.Errorf("slugify(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestGenerateID_Format(t *testing.T) {
	id := GenerateID("test description")
	parts := strings.Split(id, "-")
	if len(parts) < 3 {
		t.Fatalf("expected at least 3 parts in ID %q", id)
	}
	// First part should be 8-digit date
	if len(parts[0]) != 8 {
		t.Errorf("date part %q should be 8 chars", parts[0])
	}
	// Last part should be 8 hex chars
	last := parts[len(parts)-1]
	if len(last) != 8 {
		t.Errorf("suffix %q should be 8 chars", last)
	}
}

func TestGenerateID_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := GenerateID("same")
		if seen[id] {
			t.Fatalf("duplicate ID generated: %s", id)
		}
		seen[id] = true
	}
}

func TestCreateSession(t *testing.T) {
	s := setupStore(t)
	createFakeRepo(t, s, "repo1")
	createFakeRepo(t, s, "repo2")

	sess, err := Create(s, "test session", []string{"repo1", "repo2"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sess.Status != StatusStarted {
		t.Errorf("status = %s, want started", sess.Status)
	}
	if len(sess.Repos) != 2 {
		t.Errorf("repos = %d, want 2", len(sess.Repos))
	}

	// Verify persisted
	loaded, err := Get(s, sess.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if loaded.Description != "test session" {
		t.Errorf("description = %q, want %q", loaded.Description, "test session")
	}
}

func TestCreateSession_NoRepos(t *testing.T) {
	s := setupStore(t)
	_, err := Create(s, "test", nil)
	if err == nil {
		t.Fatal("expected error for no repos")
	}
}

func TestCreateSession_InvalidRepo(t *testing.T) {
	s := setupStore(t)
	_, err := Create(s, "test", []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent repo")
	}
}

func TestTransitions_Valid(t *testing.T) {
	s := setupStore(t)
	createFakeRepo(t, s, "repo1")

	sess, err := Create(s, "transition test", []string{"repo1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	steps := []SessionStatus{
		StatusInvestigating,
		StatusPlanning,
		StatusReviewing,
		StatusApproved,
		StatusExecuting,
		StatusVerifying,
		StatusSimplifying,
		StatusRecording,
		StatusCompleted,
	}

	for _, next := range steps {
		if err := Transition(s, sess.ID, next); err != nil {
			t.Fatalf("Transition to %s: %v", next, err)
		}
	}

	final, _ := Get(s, sess.ID)
	if final.Status != StatusCompleted {
		t.Errorf("final status = %s, want completed", final.Status)
	}
	if final.CompletedAt == nil {
		t.Error("CompletedAt should be set")
	}
}

func TestTransitions_Invalid(t *testing.T) {
	s := setupStore(t)
	createFakeRepo(t, s, "repo1")

	sess, _ := Create(s, "invalid test", []string{"repo1"})

	// started -> executing should fail
	if err := Transition(s, sess.ID, StatusExecuting); err == nil {
		t.Error("expected error for invalid transition started → executing")
	}

	// started -> completed should fail
	if err := Transition(s, sess.ID, StatusCompleted); err == nil {
		t.Error("expected error for invalid transition started → completed")
	}
}

func TestPauseResume(t *testing.T) {
	s := setupStore(t)
	createFakeRepo(t, s, "repo1")

	sess, _ := Create(s, "pause test", []string{"repo1"})
	Transition(s, sess.ID, StatusInvestigating)

	// Pause
	if err := Pause(s, sess.ID); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	paused, _ := Get(s, sess.ID)
	if paused.Status != StatusPaused {
		t.Errorf("status = %s, want paused", paused.Status)
	}
	if paused.PausedAt == nil {
		t.Error("PausedAt should be set")
	}

	// Can't transition while paused
	if err := Transition(s, sess.ID, StatusPlanning); err == nil {
		t.Error("expected error transitioning while paused")
	}

	// Resume
	if err := Resume(s, sess.ID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	resumed, _ := Get(s, sess.ID)
	if resumed.Status != StatusInvestigating {
		t.Errorf("status = %s, want investigating", resumed.Status)
	}
}

func TestPause_AlreadyPaused(t *testing.T) {
	s := setupStore(t)
	createFakeRepo(t, s, "repo1")

	sess, _ := Create(s, "double pause", []string{"repo1"})
	Pause(s, sess.ID)
	if err := Pause(s, sess.ID); err == nil {
		t.Error("expected error pausing already paused session")
	}
}

func TestResume_Completed(t *testing.T) {
	s := setupStore(t)
	createFakeRepo(t, s, "repo1")

	sess, _ := Create(s, "completed session", []string{"repo1"})
	// Force to completed status
	sess.Status = StatusCompleted
	save(s, sess)

	if err := Resume(s, sess.ID); err == nil {
		t.Error("expected error resuming completed session")
	}
}

func TestResume_ActiveStatus(t *testing.T) {
	s := setupStore(t)
	createFakeRepo(t, s, "repo1")

	sess, _ := Create(s, "stuck session", []string{"repo1"})
	// Simulate stuck in executing state (from crash/interrupt)
	sess.Status = StatusExecuting
	save(s, sess)

	if err := Resume(s, sess.ID); err != nil {
		t.Fatalf("expected no error resuming stuck active session: %v", err)
	}
	got, _ := Get(s, sess.ID)
	if got.Status != StatusExecuting {
		t.Errorf("status = %s, want executing (unchanged)", got.Status)
	}
}

func TestAbandon(t *testing.T) {
	s := setupStore(t)
	createFakeRepo(t, s, "repo1")

	sess, _ := Create(s, "abandon test", []string{"repo1"})
	if err := Abandon(s, sess.ID); err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	abandoned, _ := Get(s, sess.ID)
	if abandoned.Status != StatusAbandoned {
		t.Errorf("status = %s, want abandoned", abandoned.Status)
	}

	// Can't abandon again
	if err := Abandon(s, sess.ID); err == nil {
		t.Error("expected error abandoning already abandoned session")
	}
}

func TestAbandon_FromPaused(t *testing.T) {
	s := setupStore(t)
	createFakeRepo(t, s, "repo1")

	sess, _ := Create(s, "abandon paused", []string{"repo1"})
	Pause(s, sess.ID)
	if err := Abandon(s, sess.ID); err != nil {
		t.Fatalf("Abandon from paused: %v", err)
	}
}

func TestListAndGetActive(t *testing.T) {
	s := setupStore(t)
	createFakeRepo(t, s, "repo1")

	Create(s, "session one", []string{"repo1"})
	sess2, _ := Create(s, "session two", []string{"repo1"})
	Create(s, "session three", []string{"repo1"})

	// Abandon one
	Abandon(s, sess2.ID)

	all, err := List(s)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("List: got %d, want 3", len(all))
	}

	active, err := GetActive(s)
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}
	if len(active) != 2 {
		t.Errorf("GetActive: got %d, want 2", len(active))
	}
}

func TestTransition_FromCompleted(t *testing.T) {
	s := setupStore(t)
	createFakeRepo(t, s, "repo1")

	sess, _ := Create(s, "completed test", []string{"repo1"})
	// Walk through all states to completed
	for _, st := range []SessionStatus{StatusInvestigating, StatusPlanning, StatusApproved, StatusExecuting, StatusSimplifying, StatusRecording, StatusCompleted} {
		Transition(s, sess.ID, st)
	}

	if err := Transition(s, sess.ID, StatusStarted); err == nil {
		t.Error("expected error transitioning from completed")
	}
}
