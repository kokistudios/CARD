package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kokistudios/card/internal/store"
)

func TestNormalizeRemote(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{"https://github.com/user/repo.git", "github.com/user/repo"},
		{"https://github.com/user/repo", "github.com/user/repo"},
		{"git@github.com:user/repo.git", "github.com/user/repo"},
		{"git@github.com:user/repo", "github.com/user/repo"},
		{"ssh://git@github.com/user/repo.git", "git@github.com/user/repo"},
		{"https://github.com/user/repo/", "github.com/user/repo"},
	}
	for _, tc := range cases {
		got := NormalizeRemote(tc.input)
		if got != tc.want {
			t.Errorf("NormalizeRemote(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestDeriveID_Stable(t *testing.T) {
	id1 := DeriveID("https://github.com/user/repo.git")
	id2 := DeriveID("git@github.com:user/repo.git")
	if id1 != id2 {
		t.Errorf("expected same ID for HTTPS and SSH, got %s vs %s", id1, id2)
	}
	if len(id1) != 12 {
		t.Errorf("expected 12 char ID, got %d: %s", len(id1), id1)
	}
}

// createTestRepo creates a temporary git repo with a remote.
func createTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")
	run("remote", "add", "origin", "https://github.com/test/testrepo.git")
	run("commit", "--allow-empty", "-m", "init")
	return dir
}

func TestRegisterListRemove(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "card-home")
	store.Init(home, false)
	s, _ := store.Load(home)

	repoDir := createTestRepo(t)

	// Register
	r, err := Register(s, repoDir)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if r.Name != "testrepo" {
		t.Errorf("expected name=testrepo, got %s", r.Name)
	}

	// Duplicate
	if _, err := Register(s, repoDir); err == nil {
		t.Error("expected error on duplicate register")
	}

	// List
	repos, err := List(s)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}

	// Get
	got, err := Get(s, r.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.LocalPath != r.LocalPath {
		t.Errorf("Get returned wrong path")
	}

	// Remove
	if err := Remove(s, r.ID); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	repos, _ = List(s)
	if len(repos) != 0 {
		t.Error("expected 0 repos after remove")
	}
}

func TestCheckHealth_MissingPath(t *testing.T) {
	r := Repo{
		ID:        "abc123",
		LocalPath: "/nonexistent/path",
		RemoteURL: "github.com/test/repo",
	}
	issues := CheckHealth(r)
	if len(issues) == 0 {
		t.Error("expected health issues for missing path")
	}
	if issues[0].Severity != "error" {
		t.Errorf("expected error severity, got %s", issues[0].Severity)
	}
}
