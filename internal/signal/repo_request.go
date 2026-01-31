package signal

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kokistudios/card/internal/change"
	"github.com/kokistudios/card/internal/repo"
	"github.com/kokistudios/card/internal/session"
	"github.com/kokistudios/card/internal/store"
	"github.com/kokistudios/card/internal/ui"
)

// RepoRequest represents a single repo that Claude has identified as needed.
type RepoRequest struct {
	Path   string `yaml:"path,omitempty"`
	Remote string `yaml:"remote,omitempty"`
	Reason string `yaml:"reason"`
}

// RepoRequestSignal is the top-level structure of a repo request signal file.
type RepoRequestSignal struct {
	Repos []RepoRequest `yaml:"repos"`
}

// CheckRepoRequests looks for a repo request signal file in the work directory.
// Returns nil if no signal file exists.
func CheckRepoRequests(workDir string) (*RepoRequestSignal, error) {
	path := filepath.Join(workDir, "signals", "repo_request.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read repo request signal: %w", err)
	}

	var sig RepoRequestSignal
	if err := yaml.Unmarshal(data, &sig); err != nil {
		return nil, fmt.Errorf("failed to parse repo request signal: %w", err)
	}
	if len(sig.Repos) == 0 {
		return nil, nil
	}
	return &sig, nil
}

// ProcessRepoRequests resolves repo requests and adds them to the session.
// Returns the list of newly added repo IDs.
func ProcessRepoRequests(s *store.Store, sess *session.Session, sig *RepoRequestSignal) ([]string, error) {
	if sig == nil {
		return nil, nil
	}

	existing := make(map[string]bool)
	for _, id := range sess.Repos {
		existing[id] = true
	}

	var added []string
	for _, req := range sig.Repos {
		id, err := resolveRepoRequest(s, req)
		if err != nil {
			ui.Warning(fmt.Sprintf("Skipping repo request (%s): %v", requestLabel(req), err))
			continue
		}
		if existing[id] {
			continue
		}

		// Create change record for the new repo
		if _, err := change.Create(s, sess.ID, id); err != nil {
			ui.Warning(fmt.Sprintf("Failed to create change for repo %s: %v", id, err))
			continue
		}

		added = append(added, id)
		existing[id] = true

		r, _ := repo.Get(s, id)
		name := id
		if r != nil {
			name = r.Name
		}
		reason := ""
		if req.Reason != "" {
			reason = " — " + req.Reason
		}
		ui.Info(fmt.Sprintf("Added repo %s (%s) to session%s", name, id[:8], reason))
	}

	if len(added) > 0 {
		if err := session.AddRepos(s, sess.ID, added); err != nil {
			return nil, fmt.Errorf("failed to add repos to session: %w", err)
		}
	}

	return added, nil
}

// resolveRepoRequest turns a request into a registered repo ID.
func resolveRepoRequest(s *store.Store, req RepoRequest) (string, error) {
	if req.Path != "" {
		// Try to register; if already registered, look up by derived ID
		r, err := repo.Register(s, req.Path)
		if err == nil {
			return r.ID, nil
		}
		if strings.Contains(err.Error(), "already registered") {
			remote, rerr := getRemoteFromPath(req.Path)
			if rerr != nil {
				return "", rerr
			}
			return repo.DeriveID(remote), nil
		}
		return "", err
	}

	if req.Remote != "" {
		id := repo.DeriveID(req.Remote)
		if _, err := repo.Get(s, id); err == nil {
			return id, nil
		}
		return "", fmt.Errorf("repo with remote %q not registered — register it with 'card repo add <path>' first", req.Remote)
	}

	return "", fmt.Errorf("repo request must specify path or remote")
}

// getRemoteFromPath gets the git remote URL from a local repo path.
func getRemoteFromPath(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	cmd := exec.Command("git", "-C", absPath, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get git remote for %s: %w", absPath, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func requestLabel(req RepoRequest) string {
	if req.Path != "" {
		return req.Path
	}
	return req.Remote
}
