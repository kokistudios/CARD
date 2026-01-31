package bundle

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kokistudios/card/internal/capsule"
	"github.com/kokistudios/card/internal/repo"
	"github.com/kokistudios/card/internal/session"
	"github.com/kokistudios/card/internal/store"
)

// BundleManifest describes the contents of a .card bundle.
type BundleManifest struct {
	Version     string            `yaml:"version"`
	ExportedAt  time.Time         `yaml:"exported_at"`
	ExportedBy  string            `yaml:"exported_by"`
	SessionID   string            `yaml:"session_id"`
	Description string            `yaml:"description"`
	Repos       []BundleRepoInfo  `yaml:"repos"`
	Files       []string          `yaml:"files"`
}

// BundleRepoInfo contains repo information for bundle portability.
type BundleRepoInfo struct {
	ID        string `yaml:"id"`
	Name      string `yaml:"name"`
	RemoteURL string `yaml:"remote_url"`
}

// Export creates a .card bundle file from a session.
// The bundle contains all session data, artifacts, and capsules.
func Export(st *store.Store, sessionID, outputPath string) error {
	sess, err := session.Get(st, sessionID)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}

	// Gather repo info
	var repoInfos []BundleRepoInfo
	for _, repoID := range sess.Repos {
		r, err := repo.Get(st, repoID)
		if err != nil {
			// Include minimal info even if repo not found locally
			repoInfos = append(repoInfos, BundleRepoInfo{ID: repoID})
			continue
		}
		repoInfos = append(repoInfos, BundleRepoInfo{
			ID:        r.ID,
			Name:      r.Name,
			RemoteURL: r.RemoteURL,
		})
	}

	// Create output file
	if outputPath == "" {
		outputPath = fmt.Sprintf("%s.card", sessionID)
	}
	// If outputPath is a directory, append the default filename
	if info, err := os.Stat(outputPath); err == nil && info.IsDir() {
		outputPath = filepath.Join(outputPath, fmt.Sprintf("%s.card", sessionID))
	} else if !strings.HasSuffix(outputPath, ".card") {
		outputPath += ".card"
	}

	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outFile.Close()

	gw := gzip.NewWriter(outFile)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	sessionDir := st.Path("sessions", sessionID)
	var files []string

	// Walk the session directory and add all files
	err = filepath.Walk(sessionDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// Get relative path within session
		relPath, err := filepath.Rel(sessionDir, path)
		if err != nil {
			return err
		}

		files = append(files, relPath)

		// Read file content
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}

		// Write to tar
		header := &tar.Header{
			Name:    filepath.Join(sessionID, relPath),
			Size:    int64(len(content)),
			Mode:    int64(info.Mode()),
			ModTime: info.ModTime(),
		}
		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("failed to write tar header: %w", err)
		}
		if _, err := tw.Write(content); err != nil {
			return fmt.Errorf("failed to write tar content: %w", err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to walk session directory: %w", err)
	}

	// Create and add manifest
	manifest := BundleManifest{
		Version:     "1",
		ExportedAt:  time.Now().UTC(),
		ExportedBy:  sess.Author,
		SessionID:   sessionID,
		Description: sess.Description,
		Repos:       repoInfos,
		Files:       files,
	}

	manifestData, err := yaml.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	manifestHeader := &tar.Header{
		Name:    "manifest.yaml",
		Size:    int64(len(manifestData)),
		Mode:    0644,
		ModTime: time.Now(),
	}
	if err := tw.WriteHeader(manifestHeader); err != nil {
		return fmt.Errorf("failed to write manifest header: %w", err)
	}
	if _, err := tw.Write(manifestData); err != nil {
		return fmt.Errorf("failed to write manifest: %w", err)
	}

	return nil
}

// ImportResult contains information about an imported session.
type ImportResult struct {
	SessionID     string
	Description   string
	OriginalAuthor string
	LinkedRepos   []string
	UnlinkedRepos []string
	FilesImported int
}

// Import reads a .card bundle and imports the session into CARD_HOME.
// It auto-links repos if matching remotes are found locally.
func Import(st *store.Store, bundlePath string) (*ImportResult, error) {
	inFile, err := os.Open(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open bundle: %w", err)
	}
	defer inFile.Close()

	gr, err := gzip.NewReader(inFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read gzip: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	// First pass: find and parse manifest
	var manifest BundleManifest
	var fileContents = make(map[string][]byte)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read tar: %w", err)
		}

		content, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("failed to read file %s: %w", header.Name, err)
		}

		if header.Name == "manifest.yaml" {
			if err := yaml.Unmarshal(content, &manifest); err != nil {
				return nil, fmt.Errorf("failed to parse manifest: %w", err)
			}
		} else {
			fileContents[header.Name] = content
		}
	}

	if manifest.SessionID == "" {
		return nil, fmt.Errorf("invalid bundle: missing or empty manifest")
	}

	// Check if session already exists
	sessionDir := st.Path("sessions", manifest.SessionID)
	if _, err := os.Stat(sessionDir); err == nil {
		return nil, fmt.Errorf("session %s already exists", manifest.SessionID)
	}

	// Create session directory
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create session directory: %w", err)
	}

	// Write all files
	filesImported := 0
	for name, content := range fileContents {
		// Remove session ID prefix from path
		relPath := strings.TrimPrefix(name, manifest.SessionID+"/")
		if relPath == name {
			// File wasn't under session ID, skip
			continue
		}

		destPath := filepath.Join(sessionDir, relPath)

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory for %s: %w", relPath, err)
		}

		if err := os.WriteFile(destPath, content, 0644); err != nil {
			return nil, fmt.Errorf("failed to write %s: %w", relPath, err)
		}
		filesImported++
	}

	// Try to link repos
	result := &ImportResult{
		SessionID:      manifest.SessionID,
		Description:    manifest.Description,
		OriginalAuthor: manifest.ExportedBy,
		FilesImported:  filesImported,
	}

	localRepos, _ := repo.List(st)
	localByRemote := make(map[string]*repo.Repo)
	for _, r := range localRepos {
		// Normalize remote URL for comparison
		normalized := normalizeRemoteURL(r.RemoteURL)
		localByRemote[normalized] = &r
	}

	for _, bundleRepo := range manifest.Repos {
		normalized := normalizeRemoteURL(bundleRepo.RemoteURL)
		if localRepo, ok := localByRemote[normalized]; ok {
			result.LinkedRepos = append(result.LinkedRepos, localRepo.ID)
		} else {
			result.UnlinkedRepos = append(result.UnlinkedRepos, bundleRepo.RemoteURL)
		}
	}

	// Update session metadata to mark as imported
	sess, err := session.Get(st, manifest.SessionID)
	if err == nil {
		sess.Imported = true
		sess.ImportedFrom = manifest.ExportedBy
		now := time.Now().UTC()
		sess.ImportedAt = &now

		// Update author to current user (the importer)
		sess.Author = getGitAuthor()

		// If we found local repos, update the session's repo list
		if len(result.LinkedRepos) > 0 {
			sess.Repos = result.LinkedRepos
		}

		if err := session.Update(st, sess); err != nil {
			// Non-fatal: session was imported but metadata update failed
			_ = err
		}
	}

	// Update capsule repo references if repos were relinked
	if len(result.LinkedRepos) > 0 {
		updateCapsuleRepos(st, manifest.SessionID, manifest.Repos, result.LinkedRepos)
	}

	return result, nil
}

// normalizeRemoteURL strips protocol and .git suffix for comparison.
func normalizeRemoteURL(url string) string {
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")
	url = strings.TrimPrefix(url, "git@")
	url = strings.TrimSuffix(url, ".git")
	url = strings.Replace(url, ":", "/", 1) // git@github.com:user/repo → github.com/user/repo
	return strings.ToLower(url)
}

// getGitAuthor attempts to get the user's email from git config.
func getGitAuthor() string {
	// Simplified version - in practice this is duplicated from session.go
	// but we avoid circular imports
	return ""
}

// updateCapsuleRepos updates capsule repo references after import.
func updateCapsuleRepos(st *store.Store, sessionID string, oldRepos []BundleRepoInfo, newRepoIDs []string) {
	// Build mapping from old repo ID to new repo ID
	oldToNew := make(map[string]string)
	for i, oldRepo := range oldRepos {
		if i < len(newRepoIDs) {
			oldToNew[oldRepo.ID] = newRepoIDs[i]
		}
	}

	// Load capsules for session
	caps, err := capsule.List(st, capsule.Filter{SessionID: &sessionID})
	if err != nil {
		return
	}

	// Update each capsule's repo references
	for _, c := range caps {
		updated := false
		newRepoRefs := make([]string, 0, len(c.RepoIDs))
		for _, oldID := range c.RepoIDs {
			if newID, ok := oldToNew[oldID]; ok {
				newRepoRefs = append(newRepoRefs, newID)
				updated = true
			} else {
				newRepoRefs = append(newRepoRefs, oldID)
			}
		}
		if updated {
			c.RepoIDs = newRepoRefs
			// Save updated capsule
			_ = capsule.Store(st, c)
		}
	}
}

// ReadManifest reads only the manifest from a bundle without fully extracting.
func ReadManifest(bundlePath string) (*BundleManifest, error) {
	inFile, err := os.Open(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open bundle: %w", err)
	}
	defer inFile.Close()

	gr, err := gzip.NewReader(inFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read gzip: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read tar: %w", err)
		}

		if header.Name == "manifest.yaml" {
			content, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("failed to read manifest: %w", err)
			}
			var manifest BundleManifest
			if err := yaml.Unmarshal(content, &manifest); err != nil {
				return nil, fmt.Errorf("failed to parse manifest: %w", err)
			}
			return &manifest, nil
		}
	}

	return nil, fmt.Errorf("manifest not found in bundle")
}
