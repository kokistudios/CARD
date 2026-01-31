package capsule

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kokistudios/card/internal/artifact"
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

func TestExtractFromArtifact_SingleCapsule(t *testing.T) {
	art := &artifact.Artifact{
		Frontmatter: artifact.ArtifactMeta{
			Session:   "sess-1",
			Repos:     []string{"repo-abc"},
			Phase:     "investigate",
			Timestamp: time.Now(),
		},
		Body: `## Executive Summary

Some content here.

## Decisions

### Decision: Which database to use
- **Choice:** PostgreSQL
- **Alternatives:** MySQL, SQLite
- **Rationale:** Better JSON support and extensibility
- **Tags:** database, infrastructure
- **Source:** human
`,
	}

	caps, err := ExtractFromArtifact(art)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(caps) != 1 {
		t.Fatalf("expected 1 capsule, got %d", len(caps))
	}

	c := caps[0]
	if c.Question != "Which database to use" {
		t.Errorf("question = %q", c.Question)
	}
	if c.Choice != "PostgreSQL" {
		t.Errorf("choice = %q", c.Choice)
	}
	if len(c.Alternatives) != 2 || c.Alternatives[0] != "MySQL" {
		t.Errorf("alternatives = %v", c.Alternatives)
	}
	if c.Rationale != "Better JSON support and extensibility" {
		t.Errorf("rationale = %q", c.Rationale)
	}
	if c.Source != "human" {
		t.Errorf("source = %q", c.Source)
	}
	if len(c.Tags) != 2 {
		t.Errorf("tags = %v", c.Tags)
	}
	if c.SessionID != "sess-1" || c.Phase != "investigate" {
		t.Errorf("metadata not populated correctly")
	}
	if len(c.RepoIDs) != 1 || c.RepoIDs[0] != "repo-abc" {
		t.Errorf("RepoIDs = %v, want [repo-abc]", c.RepoIDs)
	}
}

func TestExtractFromArtifact_MultipleCapsules(t *testing.T) {
	art := &artifact.Artifact{
		Frontmatter: artifact.ArtifactMeta{
			Session: "sess-2",
			Repos:   []string{"repo-xyz"},
			Phase:   "plan",
		},
		Body: `## Decisions

### Decision: Authentication method
- **Choice:** JWT
- **Alternatives:** Session cookies, OAuth only
- **Rationale:** Stateless and works for API clients

### Decision: API versioning strategy
- **Choice:** URL path versioning
- **Alternatives:** Header versioning, query param
- **Rationale:** Most explicit and debuggable
- **Tags:** api, versioning

## Implementation Steps

Step 1...
`,
	}

	caps, err := ExtractFromArtifact(art)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(caps) != 2 {
		t.Fatalf("expected 2 capsules, got %d", len(caps))
	}

	if caps[0].Question != "Authentication method" {
		t.Errorf("first question = %q", caps[0].Question)
	}
	if caps[1].Question != "API versioning strategy" {
		t.Errorf("second question = %q", caps[1].Question)
	}
	// Source should default to "agent"
	if caps[0].Source != "agent" {
		t.Errorf("default source should be agent, got %q", caps[0].Source)
	}
}

func TestExtractFromArtifact_NoCapsules(t *testing.T) {
	art := &artifact.Artifact{
		Frontmatter: artifact.ArtifactMeta{Session: "s", Phase: "investigate"},
		Body:        "## Executive Summary\n\nJust a summary, no decisions.",
	}

	caps, err := ExtractFromArtifact(art)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(caps) != 0 {
		t.Errorf("expected 0 capsules, got %d", len(caps))
	}
}

func TestExtractFromArtifact_MissingFields(t *testing.T) {
	art := &artifact.Artifact{
		Frontmatter: artifact.ArtifactMeta{Session: "s", Phase: "plan"},
		Body: `### Decision: Minimal decision
- **Choice:** Option A
- **Rationale:** Simple
`,
	}

	caps, err := ExtractFromArtifact(art)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(caps) != 1 {
		t.Fatalf("expected 1 capsule, got %d", len(caps))
	}
	if caps[0].Choice != "Option A" {
		t.Errorf("choice = %q", caps[0].Choice)
	}
	if len(caps[0].Alternatives) != 0 {
		t.Errorf("expected no alternatives, got %v", caps[0].Alternatives)
	}
	if len(caps[0].Tags) != 0 {
		t.Errorf("expected no tags, got %v", caps[0].Tags)
	}
}

func TestExtractFromArtifact_Nil(t *testing.T) {
	_, err := ExtractFromArtifact(nil)
	if err == nil {
		t.Error("expected error for nil artifact")
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
		Timestamp: time.Now().UTC(),
		Question:  "Which approach?",
		Choice:    "Option B",
		Rationale: "Simpler",
		Source:    "agent",
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
	if got.Source != "agent" {
		t.Errorf("source = %q", got.Source)
	}
}

func TestStoreMultipleCapsules_GroupedByPhase(t *testing.T) {
	st := setupStore(t)
	sessDir := filepath.Join(st.Home, "sessions", "test-sess")
	os.MkdirAll(sessDir, 0755)

	capsules := []Capsule{
		{ID: "c1", SessionID: "test-sess", Phase: "investigate", Question: "Q1", Choice: "C1", Source: "human"},
		{ID: "c2", SessionID: "test-sess", Phase: "plan", Question: "Q2", Choice: "C2", Source: "agent"},
		{ID: "c3", SessionID: "test-sess", Phase: "investigate", Question: "Q3", Choice: "C3", Source: "agent"},
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
