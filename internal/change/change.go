package change

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kokistudios/card/internal/repo"
	"github.com/kokistudios/card/internal/store"
)

// Change tracks per-repo state within a session.
type Change struct {
	SessionID   string    `yaml:"session_id"`
	RepoID      string    `yaml:"repo_id"`
	Status      string    `yaml:"status"`
	BaseCommit  string    `yaml:"base_commit"`
	FinalCommit string    `yaml:"final_commit,omitempty"`
	Artifacts   []string  `yaml:"artifacts,omitempty"`
	CreatedAt   time.Time `yaml:"created_at"`
	UpdatedAt   time.Time `yaml:"updated_at"`
}

// Create creates a change record for a repo in a session.
func Create(s *store.Store, sessionID, repoID string) (*Change, error) {
	// Look up repo to get local path for base commit
	r, err := repo.Get(s, repoID)
	if err != nil {
		return nil, err
	}

	baseCommit := getHeadCommit(r.LocalPath)

	now := time.Now().UTC()
	c := &Change{
		SessionID:  sessionID,
		RepoID:     repoID,
		Status:     "started",
		BaseCommit: baseCommit,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	// Create directory structure
	dir := changeDir(s, sessionID, repoID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create change directory: %w", err)
	}

	if err := save(s, c); err != nil {
		return nil, err
	}

	return c, nil
}

// Get returns a change record.
func Get(s *store.Store, sessionID, repoID string) (*Change, error) {
	p := filepath.Join(changeDir(s, sessionID, repoID), "change.yaml")
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("change not found: session=%s repo=%s", sessionID, repoID)
	}
	var c Change
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("invalid change file: %w", err)
	}
	return &c, nil
}

// ListForSession returns all changes for a session.
func ListForSession(s *store.Store, sessionID string) ([]Change, error) {
	changesDir := s.Path("sessions", sessionID, "changes")
	entries, err := os.ReadDir(changesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read changes directory: %w", err)
	}

	var changes []Change
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		c, err := Get(s, sessionID, e.Name())
		if err != nil {
			continue
		}
		changes = append(changes, *c)
	}
	return changes, nil
}

func changeDir(s *store.Store, sessionID, repoID string) string {
	return s.Path("sessions", sessionID, "changes", repoID)
}

func save(s *store.Store, c *Change) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal change: %w", err)
	}
	p := filepath.Join(changeDir(s, c.SessionID, c.RepoID), "change.yaml")
	if err := os.WriteFile(p, data, 0644); err != nil {
		return fmt.Errorf("failed to write change file: %w", err)
	}
	return nil
}

// Save persists a change record to disk.
func Save(s *store.Store, c *Change) error {
	return save(s, c)
}

// GetHeadCommit returns the HEAD commit SHA for a repo at the given path.
func GetHeadCommit(repoPath string) string {
	return getHeadCommit(repoPath)
}

func getHeadCommit(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
