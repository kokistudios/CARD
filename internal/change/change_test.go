package change

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

func createFakeRepo(t *testing.T, s *store.Store, id string) {
	t.Helper()
	name := "testrepo-" + id
	content := fmt.Sprintf("---\nid: %s\nname: %s\nlocal_path: /tmp/nonexistent\n---\n\n# %s\n", id, name, strings.ToUpper(name))
	p := s.Path("repos", "REPO_"+strings.ToUpper(name)+".md")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("create fake repo: %v", err)
	}
}

func createSessionDir(t *testing.T, s *store.Store, sessionID string) {
	t.Helper()
	dir := s.Path("sessions", sessionID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("create session dir: %v", err)
	}
}

func TestCreateChange(t *testing.T) {
	s := setupStore(t)
	createFakeRepo(t, s, "repo1")
	createSessionDir(t, s, "test-session")

	c, err := Create(s, "test-session", "repo1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.SessionID != "test-session" {
		t.Errorf("SessionID = %q, want %q", c.SessionID, "test-session")
	}
	if c.RepoID != "repo1" {
		t.Errorf("RepoID = %q, want %q", c.RepoID, "repo1")
	}
	if c.Status != "started" {
		t.Errorf("Status = %q, want %q", c.Status, "started")
	}

	// Verify directory was created
	dir := s.Path("sessions", "test-session", "changes", "repo1")
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("change directory not created: %v", err)
	}

	// Verify we can read it back
	loaded, err := Get(s, "test-session", "repo1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if loaded.RepoID != "repo1" {
		t.Errorf("loaded RepoID = %q, want %q", loaded.RepoID, "repo1")
	}
}

func TestCreateChange_InvalidRepo(t *testing.T) {
	s := setupStore(t)
	createSessionDir(t, s, "test-session")

	_, err := Create(s, "test-session", "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent repo")
	}
}

func TestListForSession(t *testing.T) {
	s := setupStore(t)
	createFakeRepo(t, s, "repo1")
	createFakeRepo(t, s, "repo2")
	createSessionDir(t, s, "test-session")

	Create(s, "test-session", "repo1")
	Create(s, "test-session", "repo2")

	changes, err := ListForSession(s, "test-session")
	if err != nil {
		t.Fatalf("ListForSession: %v", err)
	}
	if len(changes) != 2 {
		t.Errorf("got %d changes, want 2", len(changes))
	}
}

func TestListForSession_NoChanges(t *testing.T) {
	s := setupStore(t)
	changes, err := ListForSession(s, "nonexistent")
	if err != nil {
		t.Fatalf("ListForSession: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("got %d changes, want 0", len(changes))
	}
}

func TestGet_NotFound(t *testing.T) {
	s := setupStore(t)
	_, err := Get(s, "no-session", "no-repo")
	if err == nil {
		t.Fatal("expected error for nonexistent change")
	}
}
