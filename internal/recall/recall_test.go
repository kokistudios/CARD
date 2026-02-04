package recall

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kokistudios/card/internal/capsule"
	"github.com/kokistudios/card/internal/session"
	"github.com/kokistudios/card/internal/store"
)

// setupTestStore creates a temp CARD_HOME with seeded data for recall tests.
func setupTestStore(t *testing.T) *store.Store {
	t.Helper()
	home := filepath.Join(t.TempDir(), "card")
	if err := store.Init(home, false); err != nil {
		t.Fatal(err)
	}
	st, err := store.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func seedSession(t *testing.T, st *store.Store, id, description string, repos []string, status session.SessionStatus) {
	t.Helper()
	dir := st.Path("sessions", id)
	os.MkdirAll(dir, 0755)
	os.MkdirAll(filepath.Join(dir, "capsules"), 0755)

	sess := session.Session{
		ID:          id,
		Description: description,
		Status:      status,
		Repos:       repos,
		CreatedAt:   time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
	}
	data, _ := yaml.Marshal(sess)
	os.WriteFile(filepath.Join(dir, "session.yaml"), data, 0644)
}

func seedCapsule(t *testing.T, st *store.Store, c capsule.Capsule) {
	t.Helper()
	if err := capsule.Store(st, c); err != nil {
		t.Fatal(err)
	}
}

func TestByFiles_ExactMatch(t *testing.T) {
	st := setupTestStore(t)
	seedSession(t, st, "sess-1", "auth feature", []string{"repo-a"}, session.StatusCompleted)
	seedCapsule(t, st, capsule.Capsule{
		ID: "cap-1", SessionID: "sess-1", RepoIDs: []string{"repo-a"}, Phase: "investigate",
		Question: "Which auth?", Choice: "JWT", Rationale: "simpler",
		Tags: []string{"src/auth.ts", "security"},
		CreatedAt: time.Now(),
	})

	result, err := ByFiles(st, "repo-a", []string{"src/auth.ts"}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Capsules) != 1 {
		t.Fatalf("expected 1 capsule, got %d", len(result.Capsules))
	}
	if result.Capsules[0].ID != "cap-1" {
		t.Fatalf("expected cap-1, got %s", result.Capsules[0].ID)
	}
}

func TestByFiles_DirectoryMatch(t *testing.T) {
	st := setupTestStore(t)
	seedSession(t, st, "sess-1", "auth feature", []string{"repo-a"}, session.StatusCompleted)
	seedCapsule(t, st, capsule.Capsule{
		ID: "cap-1", SessionID: "sess-1", RepoIDs: []string{"repo-a"}, Phase: "investigate",
		Question: "Which auth?", Choice: "JWT", Rationale: "simpler",
		Tags: []string{"src/auth/login.ts"},
		CreatedAt: time.Now(),
	})

	// Query for directory should match file inside it
	result, err := ByFiles(st, "repo-a", []string{"src/auth"}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Capsules) != 1 {
		t.Fatalf("expected 1 capsule for directory match, got %d", len(result.Capsules))
	}
}

func TestByFiles_NoMatch(t *testing.T) {
	st := setupTestStore(t)
	seedSession(t, st, "sess-1", "auth feature", []string{"repo-a"}, session.StatusCompleted)
	seedCapsule(t, st, capsule.Capsule{
		ID: "cap-1", SessionID: "sess-1", RepoIDs: []string{"repo-a"}, Phase: "investigate",
		Question: "Which auth?", Choice: "JWT", Rationale: "simpler",
		Tags: []string{"src/auth.ts"},
		CreatedAt: time.Now(),
	})

	result, err := ByFiles(st, "repo-a", []string{"src/database.ts"}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Capsules) != 0 {
		t.Fatalf("expected 0 capsules, got %d", len(result.Capsules))
	}
}

func TestByRepo(t *testing.T) {
	st := setupTestStore(t)
	seedSession(t, st, "sess-1", "auth feature", []string{"repo-a"}, session.StatusCompleted)
	seedSession(t, st, "sess-2", "db migration", []string{"repo-b"}, session.StatusCompleted)
	seedCapsule(t, st, capsule.Capsule{
		ID: "cap-1", SessionID: "sess-1", RepoIDs: []string{"repo-a"}, Phase: "investigate",
		Question: "Which auth?", Choice: "JWT", Rationale: "simpler",
		CreatedAt: time.Now(),
	})
	seedCapsule(t, st, capsule.Capsule{
		ID: "cap-2", SessionID: "sess-2", RepoIDs: []string{"repo-b"}, Phase: "investigate",
		Question: "Which DB?", Choice: "Postgres", Rationale: "JSON support",
		CreatedAt: time.Now(),
	})

	result, err := ByRepo(st, "repo-a", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(result.Sessions))
	}
	if len(result.Capsules) != 1 {
		t.Fatalf("expected 1 capsule, got %d", len(result.Capsules))
	}
}

func TestByTags(t *testing.T) {
	st := setupTestStore(t)
	seedSession(t, st, "sess-1", "auth feature", []string{"repo-a"}, session.StatusCompleted)
	seedCapsule(t, st, capsule.Capsule{
		ID: "cap-1", SessionID: "sess-1", RepoIDs: []string{"repo-a"}, Phase: "investigate",
		Question: "Which auth?", Choice: "JWT", Rationale: "simpler",
		Tags: []string{"authentication", "security"},
		CreatedAt: time.Now(),
	})
	seedCapsule(t, st, capsule.Capsule{
		ID: "cap-2", SessionID: "sess-1", RepoIDs: []string{"repo-a"}, Phase: "plan",
		Question: "Token storage?", Choice: "httpOnly cookie", Rationale: "XSS safe",
		Tags: []string{"security", "cookies"},
		CreatedAt: time.Now(),
	})

	// Partial match
	result, err := ByTags(st, []string{"auth"}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Capsules) != 1 {
		t.Fatalf("expected 1 capsule for partial tag match, got %d", len(result.Capsules))
	}

	// Exact match
	result, err = ByTags(st, []string{"security"}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Capsules) != 2 {
		t.Fatalf("expected 2 capsules for security tag, got %d", len(result.Capsules))
	}
}

func TestByTags_CaseInsensitive(t *testing.T) {
	st := setupTestStore(t)
	seedSession(t, st, "sess-1", "test", []string{"repo-a"}, session.StatusCompleted)
	seedCapsule(t, st, capsule.Capsule{
		ID: "cap-1", SessionID: "sess-1", RepoIDs: []string{"repo-a"}, Phase: "investigate",
		Question: "Q?", Choice: "A", Rationale: "R",
		Tags: []string{"Authentication"},
		CreatedAt: time.Now(),
	})

	result, err := ByTags(st, []string{"authentication"}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Capsules) != 1 {
		t.Fatalf("expected case-insensitive match, got %d", len(result.Capsules))
	}
}

func TestQuery_Deduplication(t *testing.T) {
	st := setupTestStore(t)
	seedSession(t, st, "sess-1", "auth feature", []string{"repo-a"}, session.StatusCompleted)
	seedCapsule(t, st, capsule.Capsule{
		ID: "cap-1", SessionID: "sess-1", RepoIDs: []string{"repo-a"}, Phase: "investigate",
		Question: "Which auth?", Choice: "JWT", Rationale: "simpler",
		Tags: []string{"src/auth.ts", "authentication"},
		CreatedAt: time.Now(),
	})

	// Query by both files and tags - should only return capsule once
	result, err := Query(st, RecallQuery{
		Files:  []string{"src/auth.ts"},
		RepoID: "repo-a",
		Tags:   []string{"authentication"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Capsules) != 1 {
		t.Fatalf("expected 1 deduplicated capsule, got %d", len(result.Capsules))
	}
}

func TestQuery_EmptyResults(t *testing.T) {
	st := setupTestStore(t)
	result, err := Query(st, RecallQuery{RepoID: "nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Capsules) != 0 {
		t.Fatalf("expected 0 capsules, got %d", len(result.Capsules))
	}
}

func TestQuery_CrossRepoFileSearch(t *testing.T) {
	st := setupTestStore(t)
	seedSession(t, st, "sess-1", "auth feature", []string{"repo-a"}, session.StatusCompleted)
	seedSession(t, st, "sess-2", "other feature", []string{"repo-b"}, session.StatusCompleted)
	seedCapsule(t, st, capsule.Capsule{
		ID: "cap-1", SessionID: "sess-1", RepoIDs: []string{"repo-a"}, Phase: "investigate",
		Question: "Which auth?", Choice: "JWT", Rationale: "simpler",
		Tags: []string{"src/auth.ts"},
		CreatedAt: time.Now(),
	})
	seedCapsule(t, st, capsule.Capsule{
		ID: "cap-2", SessionID: "sess-2", RepoIDs: []string{"repo-b"}, Phase: "investigate",
		Question: "Auth in B?", Choice: "OAuth", Rationale: "third party",
		Tags: []string{"src/auth.ts"},
		CreatedAt: time.Now(),
	})

	// Query by files WITHOUT specifying repo - should find capsules from both repos
	result, err := Query(st, RecallQuery{
		Files:            []string{"src/auth.ts"},
		IncludeEvolution: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Capsules) != 2 {
		t.Fatalf("expected 2 capsules from cross-repo search, got %d", len(result.Capsules))
	}
}

func TestQuery_FullTextSearch(t *testing.T) {
	st := setupTestStore(t)
	seedSession(t, st, "sess-1", "auth feature", []string{"repo-a"}, session.StatusCompleted)
	seedCapsule(t, st, capsule.Capsule{
		ID: "cap-1", SessionID: "sess-1", RepoIDs: []string{"repo-a"}, Phase: "investigate",
		Question: "Which database should we use?", Choice: "PostgreSQL", Rationale: "JSON support and TypeORM compatibility",
		CreatedAt: time.Now(),
	})
	seedCapsule(t, st, capsule.Capsule{
		ID: "cap-2", SessionID: "sess-1", RepoIDs: []string{"repo-a"}, Phase: "investigate",
		Question: "Which auth provider?", Choice: "Okta", Rationale: "Enterprise SSO requirements",
		CreatedAt: time.Now(),
	})

	// Search should find capsule by question text
	result, err := Query(st, RecallQuery{
		Query:            "database",
		IncludeEvolution: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Capsules) != 1 {
		t.Fatalf("expected 1 capsule for 'database' query, got %d", len(result.Capsules))
	}
	if result.Capsules[0].ID != "cap-1" {
		t.Fatalf("expected cap-1, got %s", result.Capsules[0].ID)
	}

	// Search should find by rationale text
	result, err = Query(st, RecallQuery{
		Query:            "typeorm",
		IncludeEvolution: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Capsules) != 1 {
		t.Fatalf("expected 1 capsule for 'typeorm' query, got %d", len(result.Capsules))
	}

	// Case insensitive
	result, err = Query(st, RecallQuery{
		Query:            "POSTGRESQL",
		IncludeEvolution: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Capsules) != 1 {
		t.Fatalf("expected 1 capsule for case-insensitive search, got %d", len(result.Capsules))
	}
}

func TestFormatTerminal_Empty(t *testing.T) {
	result := &RecallResult{}
	out := FormatTerminal(result, false)
	if out != "No prior CARD context found." {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestFormatTerminal_Brief(t *testing.T) {
	result := &RecallResult{
		Capsules: []ScoredCapsule{
			{Capsule: capsule.Capsule{Phase: "investigate", Question: "Which auth?", Choice: "JWT"}, Tier: MatchExactFile},
		},
	}
	out := FormatTerminal(result, false)
	if !contains(out, "JWT") || !contains(out, "Which auth?") || !contains(out, "exact-file") {
		t.Fatalf("brief format missing expected content: %s", out)
	}
}

func TestFormatTerminal_Full(t *testing.T) {
	result := &RecallResult{
		Capsules: []ScoredCapsule{
			{Capsule: capsule.Capsule{Phase: "investigate", Question: "Which auth?", Choice: "JWT", Rationale: "simpler", Tags: []string{"auth"}}, Tier: MatchTag},
		},
	}
	out := FormatTerminal(result, true)
	if !contains(out, "Rationale") || !contains(out, "simpler") || !contains(out, "tag") {
		t.Fatalf("full format missing expected content: %s", out)
	}
}

func TestFormatContext_Empty(t *testing.T) {
	result := &RecallResult{}
	out := FormatContext(result, 0)
	if out != "" {
		t.Fatalf("expected empty string for no capsules, got: %s", out)
	}
}

func TestFormatContext_WithCapsules(t *testing.T) {
	result := &RecallResult{
		Capsules: []ScoredCapsule{
			{Capsule: capsule.Capsule{SessionID: "sess-1", Phase: "investigate", Question: "Which auth?", Choice: "JWT", Rationale: "simpler"}, Tier: MatchExactFile},
		},
	}
	out := FormatContext(result, 0)
	if !contains(out, "Prior CARD Context") || !contains(out, "JWT") {
		t.Fatalf("context format missing expected content: %s", out)
	}
}

func TestFormatContext_StrongVsWeak(t *testing.T) {
	result := &RecallResult{
		Capsules: []ScoredCapsule{
			{Capsule: capsule.Capsule{SessionID: "s1", Phase: "investigate", Question: "Strong?", Choice: "Yes", Rationale: "direct hit"}, Tier: MatchExactFile},
			{Capsule: capsule.Capsule{SessionID: "s1", Phase: "plan", Question: "Weak?", Choice: "Maybe"}, Tier: MatchTag},
		},
	}
	out := FormatContext(result, 0)
	// Strong match should have full markdown heading (with status indicator)
	if !contains(out, "Decision: Strong?") {
		t.Fatalf("expected full heading for strong match: %s", out)
	}
	// Weak match should be a one-liner (with status indicator)
	if !contains(out, "Weak? → Maybe") {
		t.Fatalf("expected one-liner for weak match: %s", out)
	}
}

func TestFormatContext_TokenBudget(t *testing.T) {
	result := &RecallResult{
		Capsules: []ScoredCapsule{
			{Capsule: capsule.Capsule{SessionID: "s1", Phase: "investigate", Question: "First?", Choice: "A", Rationale: "reason"}, Tier: MatchExactFile},
			{Capsule: capsule.Capsule{SessionID: "s1", Phase: "plan", Question: "Second?", Choice: "B", Rationale: "reason2"}, Tier: MatchExactFile},
		},
	}
	// Very tight budget — should only fit header + maybe first entry
	out := FormatContext(result, 50)
	if contains(out, "Second?") {
		t.Fatalf("expected second capsule to be truncated under tight budget: %s", out)
	}
	if !contains(out, "Prior CARD Context") {
		t.Fatalf("expected header to be present: %s", out)
	}
}

func TestMatchesFile(t *testing.T) {
	tests := []struct {
		tags  []string
		file  string
		match bool
	}{
		{[]string{"src/auth.ts"}, "src/auth.ts", true},
		{[]string{"src/auth/login.ts"}, "src/auth", true},     // tag is child of query dir
		{[]string{"src/auth"}, "src/auth/login.ts", true},     // query is child of tag dir
		{[]string{"src/database.ts"}, "src/auth.ts", false},
		{[]string{"SRC/AUTH.TS"}, "src/auth.ts", true},        // case insensitive
		{[]string{}, "src/auth.ts", false},
	}

	for _, tt := range tests {
		got := matchesFile(tt.tags, tt.file)
		if got != tt.match {
			t.Errorf("matchesFile(%v, %q) = %v, want %v", tt.tags, tt.file, got, tt.match)
		}
	}
}

func TestMatchesTag(t *testing.T) {
	tests := []struct {
		tags  []string
		query string
		match bool
	}{
		{[]string{"authentication"}, "auth", true},
		{[]string{"authentication"}, "AUTHENTICATION", true},
		{[]string{"database"}, "auth", false},
		{[]string{}, "auth", false},
	}

	for _, tt := range tests {
		got := matchesTag(tt.tags, tt.query)
		if got != tt.match {
			t.Errorf("matchesTag(%v, %q) = %v, want %v", tt.tags, tt.query, got, tt.match)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && containsStr(s, sub)
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
