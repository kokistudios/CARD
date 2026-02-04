package capsule

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kokistudios/card/internal/store"
)

func TestGenerateID(t *testing.T) {
	id1 := GenerateID("sess-1", "investigate", "Which database?")
	id2 := GenerateID("sess-1", "investigate", "Which database?")
	id3 := GenerateID("sess-1", "investigate", "Which framework?")

	if id1 != id2 {
		t.Errorf("same inputs should produce same ID: %s != %s", id1, id2)
	}
	if id1 == id3 {
		t.Errorf("different questions should produce different IDs")
	}
	if id1 == "" {
		t.Error("ID should not be empty")
	}
}

func setupStore(t *testing.T) *store.Store {
	t.Helper()
	home := t.TempDir()
	if err := store.Init(home, true); err != nil {
		t.Fatalf("store init: %v", err)
	}
	st, err := store.Load(home)
	if err != nil {
		t.Fatalf("store load: %v", err)
	}
	return st
}

func TestStoreAndGet(t *testing.T) {
	st := setupStore(t)

	// Create session dir
	sessDir := filepath.Join(st.Home, "sessions", "test-sess")
	os.MkdirAll(sessDir, 0755)

	c := Capsule{
		ID:        "test-sess-investigate-abcd1234",
		SessionID: "test-sess",
		RepoIDs:   []string{"repo-1"},
		Phase:     "investigate",
		CreatedAt: time.Now().UTC(),
		Question:  "Which approach?",
		Choice:    "Option B",
		Rationale: "Simpler",
		Origin:    "agent",
		Tags:      []string{"src/main.go"},
	}

	if err := Store(st, c); err != nil {
		t.Fatalf("store capsule: %v", err)
	}

	// Verify consolidated file exists (not individual file)
	consolidatedPath := filepath.Join(st.Home, "sessions", "test-sess", "capsules.md")
	if _, err := os.Stat(consolidatedPath); err != nil {
		t.Fatalf("consolidated capsules.md should exist: %v", err)
	}
	oldDir := filepath.Join(st.Home, "sessions", "test-sess", "capsules")
	if _, err := os.Stat(oldDir); err == nil {
		t.Error("old capsules/ directory should not exist")
	}

	got, err := Get(st, c.ID)
	if err != nil {
		t.Fatalf("get capsule: %v", err)
	}
	if got.Question != "Which approach?" {
		t.Errorf("question = %q", got.Question)
	}
	if got.Choice != "Option B" {
		t.Errorf("choice = %q", got.Choice)
	}
	if got.Origin != "agent" {
		t.Errorf("origin = %q", got.Origin)
	}
}

func TestStoreMultipleCapsules_GroupedByPhase(t *testing.T) {
	st := setupStore(t)
	sessDir := filepath.Join(st.Home, "sessions", "test-sess")
	os.MkdirAll(sessDir, 0755)

	capsules := []Capsule{
		{ID: "c1", SessionID: "test-sess", Phase: "investigate", Question: "Q1", Choice: "C1", Origin: "human"},
		{ID: "c2", SessionID: "test-sess", Phase: "plan", Question: "Q2", Choice: "C2", Origin: "agent"},
		{ID: "c3", SessionID: "test-sess", Phase: "investigate", Question: "Q3", Choice: "C3", Origin: "agent"},
	}
	for _, c := range capsules {
		if err := Store(st, c); err != nil {
			t.Fatalf("store capsule %s: %v", c.ID, err)
		}
	}

	// Read the file and verify structure
	data, err := os.ReadFile(filepath.Join(st.Home, "sessions", "test-sess", "capsules.md"))
	if err != nil {
		t.Fatalf("read capsules.md: %v", err)
	}
	content := string(data)

	// Should have phase headers in order
	invIdx := indexOf(content, "## investigate")
	planIdx := indexOf(content, "## plan")
	if invIdx == -1 || planIdx == -1 {
		t.Fatal("missing phase headers")
	}
	if invIdx >= planIdx {
		t.Error("investigate should come before plan")
	}

	// All 3 capsules should be retrievable
	all, _ := List(st, Filter{})
	if len(all) != 3 {
		t.Errorf("expected 3 capsules, got %d", len(all))
	}
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func TestListWithFilters(t *testing.T) {
	st := setupStore(t)

	// Create two sessions
	for _, sess := range []string{"sess-a", "sess-b"} {
		os.MkdirAll(filepath.Join(st.Home, "sessions", sess), 0755)
	}

	capsules := []Capsule{
		{ID: "sess-a-inv-1", SessionID: "sess-a", RepoIDs: []string{"r1"}, Phase: "investigate", Question: "Q1", Choice: "C1", Tags: []string{"auth", "src/auth.go"}},
		{ID: "sess-a-plan-2", SessionID: "sess-a", RepoIDs: []string{"r1"}, Phase: "plan", Question: "Q2", Choice: "C2", Tags: []string{"api"}},
		{ID: "sess-b-inv-3", SessionID: "sess-b", RepoIDs: []string{"r2"}, Phase: "investigate", Question: "Q3", Choice: "C3", Tags: []string{"auth"}},
	}
	for _, c := range capsules {
		Store(st, c)
	}

	// No filter — all capsules
	all, _ := List(st, Filter{})
	if len(all) != 3 {
		t.Errorf("expected 3 capsules, got %d", len(all))
	}

	// Filter by session
	sessA := "sess-a"
	filtered, _ := List(st, Filter{SessionID: &sessA})
	if len(filtered) != 2 {
		t.Errorf("expected 2 capsules for sess-a, got %d", len(filtered))
	}

	// Filter by tag
	authTag := "auth"
	filtered, _ = List(st, Filter{Tag: &authTag})
	if len(filtered) != 2 {
		t.Errorf("expected 2 capsules with auth tag, got %d", len(filtered))
	}

	// Filter by phase
	inv := "investigate"
	filtered, _ = List(st, Filter{Phase: &inv})
	if len(filtered) != 2 {
		t.Errorf("expected 2 investigate capsules, got %d", len(filtered))
	}

	// Filter by file path
	authFile := "auth.go"
	filtered, _ = List(st, Filter{FilePath: &authFile})
	if len(filtered) != 1 {
		t.Errorf("expected 1 capsule matching auth.go, got %d", len(filtered))
	}
}

func TestLinkCommits(t *testing.T) {
	st := setupStore(t)

	os.MkdirAll(filepath.Join(st.Home, "sessions", "s1"), 0755)

	c := Capsule{ID: "s1-inv-abc", SessionID: "s1", Phase: "investigate", Question: "Q", Choice: "C"}
	Store(st, c)

	if err := LinkCommits(st, "s1-inv-abc", []string{"abc123", "def456"}); err != nil {
		t.Fatalf("link commits: %v", err)
	}

	got, _ := Get(st, "s1-inv-abc")
	if len(got.Commits) != 2 || got.Commits[0] != "abc123" {
		t.Errorf("commits = %v", got.Commits)
	}
}

func TestStoreReplacesExisting(t *testing.T) {
	st := setupStore(t)
	os.MkdirAll(filepath.Join(st.Home, "sessions", "s1"), 0755)

	c := Capsule{ID: "c1", SessionID: "s1", Phase: "investigate", Question: "Q", Choice: "Old"}
	Store(st, c)

	c.Choice = "New"
	Store(st, c)

	got, _ := Get(st, "c1")
	if got.Choice != "New" {
		t.Errorf("expected updated choice 'New', got %q", got.Choice)
	}

	// Should still be only 1 capsule
	all, _ := List(st, Filter{})
	if len(all) != 1 {
		t.Errorf("expected 1 capsule after replace, got %d", len(all))
	}
}
