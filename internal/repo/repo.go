package repo

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kokistudios/card/internal/store"
)

type Repo struct {
	ID        string    `yaml:"id"`
	Name      string    `yaml:"name"`
	RemoteURL string    `yaml:"remote_url"`
	LocalPath string    `yaml:"local_path"`
	AddedAt   time.Time `yaml:"added_at"`
}

func DeriveID(remoteURL string) string {
	normalized := NormalizeRemote(remoteURL)
	h := sha256.Sum256([]byte(normalized))
	return fmt.Sprintf("%x", h[:6]) // 12 hex chars
}

func NormalizeRemote(raw string) string {
	s := strings.TrimSpace(raw)

	sshRe := regexp.MustCompile(`^[\w-]+@([\w.\-]+):(.+)$`)
	if m := sshRe.FindStringSubmatch(s); m != nil {
		s = m[1] + "/" + m[2]
	} else {
		s = regexp.MustCompile(`^https?://`).ReplaceAllString(s, "")
		s = regexp.MustCompile(`^ssh://`).ReplaceAllString(s, "")
	}

	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")
	return s
}

func getRemoteURL(repoPath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get git remote for %s: %w", repoPath, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func nameFromRemote(remoteURL string) string {
	normalized := NormalizeRemote(remoteURL)
	parts := strings.Split(normalized, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return normalized
}

func RepoFilename(name string) string {
	return "REPO_" + strings.ToUpper(name) + ".md"
}

func (r *Repo) Filename() string {
	return RepoFilename(r.Name)
}

func Register(s *store.Store, path string) (*Repo, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	info, err := os.Stat(absPath)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("path does not exist or is not a directory: %s", absPath)
	}

	gitDir := filepath.Join(absPath, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		return nil, fmt.Errorf("not a git repository: %s", absPath)
	}

	remoteURL, err := getRemoteURL(absPath)
	if err != nil {
		return nil, err
	}

	id := DeriveID(remoteURL)
	name := nameFromRemote(remoteURL)

	repoFile := filepath.Join(s.Path("repos"), RepoFilename(name))
	if _, err := os.Stat(repoFile); err == nil {
		return nil, fmt.Errorf("repo already registered (ID: %s)", id)
	}

	r := &Repo{
		ID:        id,
		Name:      name,
		RemoteURL: NormalizeRemote(remoteURL),
		LocalPath: absPath,
		AddedAt:   time.Now().UTC(),
	}

	if err := saveRepo(s, r); err != nil {
		return nil, err
	}

	return r, nil
}

func saveRepo(s *store.Store, r *Repo) error {
	var buf bytes.Buffer
	buf.WriteString("---\n")
	fm, err := yaml.Marshal(r)
	if err != nil {
		return fmt.Errorf("failed to marshal repo: %w", err)
	}
	buf.Write(fm)
	buf.WriteString("---\n\n")
	buf.WriteString(fmt.Sprintf("# %s\n\n", strings.ToUpper(r.Name)))
	buf.WriteString(fmt.Sprintf("- **Remote:** %s\n", r.RemoteURL))
	buf.WriteString(fmt.Sprintf("- **Local Path:** `%s`\n", r.LocalPath))
	buf.WriteString(fmt.Sprintf("- **Added:** %s\n", r.AddedAt.Format("2006-01-02")))

	path := filepath.Join(s.Path("repos"), r.Filename())
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write repo file: %w", err)
	}
	return nil
}

func List(s *store.Store) ([]Repo, error) {
	reposDir := s.Path("repos")
	entries, err := os.ReadDir(reposDir)
	if err != nil {
		return nil, fmt.Errorf("cannot read repos directory: %w", err)
	}

	var repos []Repo
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".md") {
			r, err := loadRepoMd(filepath.Join(reposDir, name))
			if err != nil {
				continue
			}
			repos = append(repos, *r)
		}
	}
	return repos, nil
}

func Get(s *store.Store, id string) (*Repo, error) {
	reposDir := s.Path("repos")

	entries, err := os.ReadDir(reposDir)
	if err != nil {
		return nil, fmt.Errorf("cannot read repos directory: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "REPO_") && strings.HasSuffix(name, ".md") {
			r, err := loadRepoMd(filepath.Join(reposDir, name))
			if err == nil && r.ID == id {
				return r, nil
			}
		}
	}

	return nil, fmt.Errorf("repo not found: %s", id)
}

func loadRepoMd(path string) (*Repo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	content := string(data)
	if !strings.HasPrefix(strings.TrimSpace(content), "---") {
		return nil, fmt.Errorf("no frontmatter in repo file")
	}

	rest := strings.TrimSpace(content)[3:]
	rest = strings.TrimLeft(rest, " \t\r\n")
	endIdx := strings.Index(rest, "\n---")
	if endIdx == -1 {
		return nil, fmt.Errorf("unterminated frontmatter")
	}

	fmRaw := rest[:endIdx]
	var r Repo
	if err := yaml.Unmarshal([]byte(fmRaw), &r); err != nil {
		return nil, fmt.Errorf("invalid repo frontmatter: %w", err)
	}
	return &r, nil
}

func Remove(s *store.Store, id string) error {
	r, err := Get(s, id)
	if err != nil {
		return err
	}
	path := filepath.Join(s.Path("repos"), r.Filename())
	return os.Remove(path)
}

func CheckHealth(r Repo) []store.Issue {
	var issues []store.Issue

	info, err := os.Stat(r.LocalPath)
	if err != nil {
		issues = append(issues, store.Issue{Severity: "error", Message: fmt.Sprintf("repo %s: path does not exist: %s", r.ID, r.LocalPath)})
		return issues
	}
	if !info.IsDir() {
		issues = append(issues, store.Issue{Severity: "error", Message: fmt.Sprintf("repo %s: path is not a directory: %s", r.ID, r.LocalPath)})
		return issues
	}

	if _, err := os.Stat(filepath.Join(r.LocalPath, ".git")); err != nil {
		issues = append(issues, store.Issue{Severity: "error", Message: fmt.Sprintf("repo %s: no longer a git repository", r.ID)})
		return issues
	}

	currentRemote, err := getRemoteURL(r.LocalPath)
	if err != nil {
		issues = append(issues, store.Issue{Severity: "warning", Message: fmt.Sprintf("repo %s: cannot read remote URL: %v", r.ID, err)})
		return issues
	}

	if NormalizeRemote(currentRemote) != r.RemoteURL {
		issues = append(issues, store.Issue{Severity: "warning", Message: fmt.Sprintf("repo %s: remote URL changed (registered: %s, current: %s)", r.ID, r.RemoteURL, NormalizeRemote(currentRemote))})
	}

	return issues
}

func CheckAllHealth(s *store.Store) ([]store.Issue, error) {
	repos, err := List(s)
	if err != nil {
		return nil, err
	}
	var issues []store.Issue
	for _, r := range repos {
		issues = append(issues, CheckHealth(r)...)
	}
	return issues, nil
}
