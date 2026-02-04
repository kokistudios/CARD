package store

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type ClaudeConfig struct {
	Path         string   `yaml:"path"`
	DefaultFlags []string `yaml:"default_flags,omitempty"`
}

type RuntimeConfig struct {
	Type string `yaml:"type"`
	Path string `yaml:"path,omitempty"`
}

type SessionConfig struct {
	AutoContinueSimplify bool `yaml:"auto_continue_simplify"`
	AutoContinueRecord   bool `yaml:"auto_continue_record"`
}

type RecallConfig struct {
	MaxContextBlocks int `yaml:"max_context_blocks"`
	MaxContextChars  int `yaml:"max_context_chars"`
	MaxContextTokens int `yaml:"max_context_tokens"`
}

type Config struct {
	Version string        `yaml:"version"`
	Runtime RuntimeConfig `yaml:"runtime,omitempty"`
	Claude  ClaudeConfig  `yaml:"claude,omitempty"`
	Session SessionConfig `yaml:"session,omitempty"`
	Recall  RecallConfig  `yaml:"recall,omitempty"`
}

func DefaultConfig() Config {
	return Config{
		Version: "1",
		Runtime: RuntimeConfig{
			Type: "claude",
		},
		Claude: ClaudeConfig{
			Path: "claude",
		},
		Session: SessionConfig{
			AutoContinueSimplify: true,
			AutoContinueRecord:   true,
		},
		Recall: RecallConfig{
			MaxContextBlocks: 10,
			MaxContextChars:  8000,
			MaxContextTokens: 5000,
		},
	}
}

type Store struct {
	Home   string
	Config Config
}

type Issue struct {
	Severity string // "warning" or "error"
	Message  string
}

func Home() string {
	if h := os.Getenv("CARD_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".card")
	}
	return filepath.Join(home, ".card")
}

func Init(home string, force bool) error {
	if _, err := os.Stat(home); err == nil && !force {
		return fmt.Errorf("CARD_HOME already exists at %s (use --force to reinitialize)", home)
	}

	dirs := []string{
		home,
		filepath.Join(home, "repos"),
		filepath.Join(home, "sessions"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("failed to create %s: %w", d, err)
		}
	}

	cfg := DefaultConfig()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	cfgPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	return nil
}

func Load(home string) (*Store, error) {
	cfgPath := filepath.Join(home, "config.yaml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read CARD_HOME config at %s: %w", cfgPath, err)
	}
	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config.yaml: %w", err)
	}
	if cfg.Runtime.Type == "" {
		cfg.Runtime.Type = "claude"
	}
	if cfg.Runtime.Type == "claude" && cfg.Runtime.Path == "" && cfg.Claude.Path != "" {
		cfg.Runtime.Path = cfg.Claude.Path
	}
	return &Store{Home: home, Config: cfg}, nil
}

func (s *Store) SaveConfig() error {
	data, err := yaml.Marshal(s.Config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	cfgPath := filepath.Join(s.Home, "config.yaml")
	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	return nil
}

func (s *Store) SetConfigValue(key, value string) error {
	switch key {
	case "runtime.type":
		s.Config.Runtime.Type = value
	case "runtime.path":
		s.Config.Runtime.Path = value
	case "claude.path":
		s.Config.Claude.Path = value
		if s.Config.Runtime.Type == "" || s.Config.Runtime.Type == "claude" {
			s.Config.Runtime.Path = value
		}
	case "session.auto_continue_simplify":
		s.Config.Session.AutoContinueSimplify = value == "true"
	case "session.auto_continue_record":
		s.Config.Session.AutoContinueRecord = value == "true"
	case "recall.max_context_blocks":
		var n int
		if _, err := fmt.Sscanf(value, "%d", &n); err != nil || n < 1 {
			return fmt.Errorf("recall.max_context_blocks must be a positive integer")
		}
		s.Config.Recall.MaxContextBlocks = n
	case "recall.max_context_chars":
		var n int
		if _, err := fmt.Sscanf(value, "%d", &n); err != nil || n < 1 {
			return fmt.Errorf("recall.max_context_chars must be a positive integer")
		}
		s.Config.Recall.MaxContextChars = n
	case "recall.max_context_tokens":
		var n int
		if _, err := fmt.Sscanf(value, "%d", &n); err != nil || n < 100 {
			return fmt.Errorf("recall.max_context_tokens must be an integer >= 100")
		}
		s.Config.Recall.MaxContextTokens = n
	default:
		return fmt.Errorf("unknown config key: %s\nValid keys: runtime.type, runtime.path, claude.path, session.auto_continue_simplify, session.auto_continue_record, recall.max_context_blocks, recall.max_context_chars, recall.max_context_tokens", key)
	}
	return s.SaveConfig()
}

func (s *Store) Path(parts ...string) string {
	all := append([]string{s.Home}, parts...)
	return filepath.Join(all...)
}

func CheckHealth(home string) []Issue {
	var issues []Issue

	for _, dir := range []string{"repos", "sessions"} {
		p := filepath.Join(home, dir)
		info, err := os.Stat(p)
		if err != nil {
			issues = append(issues, Issue{"error", fmt.Sprintf("missing directory: %s", p)})
		} else if !info.IsDir() {
			issues = append(issues, Issue{"error", fmt.Sprintf("expected directory but found file: %s", p)})
		}
	}

	cfgPath := filepath.Join(home, "config.yaml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		issues = append(issues, Issue{"error", fmt.Sprintf("cannot read config.yaml: %v", err)})
	} else {
		var cfg Config
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			issues = append(issues, Issue{"error", fmt.Sprintf("config.yaml is not valid YAML: %v", err)})
		}
	}

	return issues
}

func CheckSessionIntegrity(home string) []Issue {
	var issues []Issue
	sessionsDir := filepath.Join(home, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return issues
	}

	registeredRepos := make(map[string]bool)
	reposDir := filepath.Join(home, "repos")
	repoFiles, _ := os.ReadDir(reposDir)
	for _, rf := range repoFiles {
		if rf.IsDir() || filepath.Ext(rf.Name()) != ".md" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(reposDir, rf.Name()))
		if err != nil {
			continue
		}
		var raw map[string]interface{}
		content := string(data)
		if len(content) > 4 && content[:4] == "---\n" {
			if end := findFrontmatterEnd(content[4:]); end > 0 {
				if err := yaml.Unmarshal([]byte(content[4:4+end]), &raw); err == nil {
					if id, ok := raw["id"].(string); ok && id != "" {
						registeredRepos[id] = true
					}
				}
			}
		}
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sessFile := filepath.Join(sessionsDir, e.Name(), "session.yaml")
		data, err := os.ReadFile(sessFile)
		if err != nil {
			issues = append(issues, Issue{"error", fmt.Sprintf("session %s: missing session.yaml", e.Name())})
			continue
		}

		var raw map[string]interface{}
		if err := yaml.Unmarshal(data, &raw); err != nil {
			issues = append(issues, Issue{"error", fmt.Sprintf("session %s: invalid YAML: %v", e.Name(), err)})
			continue
		}

		if repos, ok := raw["repos"].([]interface{}); ok {
			for _, r := range repos {
				repoID, _ := r.(string)
				if repoID == "" {
					continue
				}
				if !registeredRepos[repoID] {
					issues = append(issues, Issue{"warning", fmt.Sprintf("session %s: references unregistered repo %s", e.Name(), repoID)})
				}
			}
		}
	}

	return issues
}

func findFrontmatterEnd(content string) int {
	for i := 0; i < len(content)-3; i++ {
		if content[i] == '-' && content[i+1] == '-' && content[i+2] == '-' {
			if i == 0 || content[i-1] == '\n' {
				return i
			}
		}
	}
	return -1
}

func CheckCapsuleIntegrity(home string) []Issue {
	var issues []Issue
	sessionsDir := filepath.Join(home, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return issues
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		capsulesDir := filepath.Join(sessionsDir, e.Name(), "capsules")
		capEntries, err := os.ReadDir(capsulesDir)
		if err != nil {
			continue
		}
		for _, ce := range capEntries {
			if ce.IsDir() {
				continue
			}
			ext := filepath.Ext(ce.Name())
			if ext != ".yaml" && ext != ".md" {
				continue
			}
			capFile := filepath.Join(capsulesDir, ce.Name())
			data, err := os.ReadFile(capFile)
			if err != nil {
				issues = append(issues, Issue{"error", fmt.Sprintf("capsule %s: cannot read file", capFile)})
				continue
			}
			if ext == ".yaml" {
				var raw map[string]interface{}
				if err := yaml.Unmarshal(data, &raw); err != nil {
					issues = append(issues, Issue{"error", fmt.Sprintf("capsule %s: invalid YAML", capFile)})
				}
			}
		}
	}

	return issues
}

func FixIssues(home string) []string {
	var fixed []string

	for _, dir := range []string{"repos", "sessions"} {
		p := filepath.Join(home, dir)
		if _, err := os.Stat(p); err != nil {
			if err := os.MkdirAll(p, 0755); err == nil {
				fixed = append(fixed, fmt.Sprintf("recreated missing directory: %s", dir))
			}
		}
	}

	cfgPath := filepath.Join(home, "config.yaml")
	if _, err := os.Stat(cfgPath); err != nil {
		cfg := DefaultConfig()
		data, _ := yaml.Marshal(cfg)
		if os.WriteFile(cfgPath, data, 0644) == nil {
			fixed = append(fixed, "recreated missing config.yaml with defaults")
		}
	}

	return fixed
}

type EphemeralArtifact struct {
	SessionID string
	Filename  string
	Path      string
}

var ephemeralArtifactPatterns = []string{
	"investigation_summary.md",
	"implementation_guide.md",
	"execution_log.md",
	"verification_notes.md",
	"research_conclusions.md",
}

func CheckEphemeralArtifacts(home string) []EphemeralArtifact {
	var stale []EphemeralArtifact
	sessionsDir := filepath.Join(home, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return stale
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sessionID := e.Name()
		sessionDir := filepath.Join(sessionsDir, sessionID)

		// Check if session is completed
		sessionYAML := filepath.Join(sessionDir, "session.yaml")
		data, err := os.ReadFile(sessionYAML)
		if err != nil {
			continue
		}
		var sess struct {
			Status string `yaml:"status"`
		}
		if err := yaml.Unmarshal(data, &sess); err != nil {
			continue
		}
		if sess.Status != "completed" {
			continue
		}

		for _, pattern := range ephemeralArtifactPatterns {
			path := filepath.Join(sessionDir, pattern)
			if _, err := os.Stat(path); err == nil {
				stale = append(stale, EphemeralArtifact{
					SessionID: sessionID,
					Filename:  pattern,
					Path:      path,
				})
			}
		}

		files, err := os.ReadDir(sessionDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			name := f.Name()
			if (len(name) > 16 && name[:14] == "execution_log_" && name[14] == 'v' && filepath.Ext(name) == ".md") ||
				(len(name) > 21 && name[:19] == "verification_notes_" && name[19] == 'v' && filepath.Ext(name) == ".md") {
				stale = append(stale, EphemeralArtifact{
					SessionID: sessionID,
					Filename:  name,
					Path:      filepath.Join(sessionDir, name),
				})
			}
		}
	}

	return stale
}

type CleanupResult struct {
	Messages           []string // Human-readable messages for each cleaned file
	AffectedSessionIDs []string // Unique session IDs that had artifacts removed
}

func CleanEphemeralArtifacts(home string) CleanupResult {
	result := CleanupResult{
		Messages:           []string{},
		AffectedSessionIDs: []string{},
	}
	stale := CheckEphemeralArtifacts(home)
	affectedSet := make(map[string]bool)

	for _, artifact := range stale {
		if err := os.Remove(artifact.Path); err == nil {
			result.Messages = append(result.Messages, fmt.Sprintf("session %s: removed %s", artifact.SessionID, artifact.Filename))
			affectedSet[artifact.SessionID] = true
		}
	}

	for sessionID := range affectedSet {
		result.AffectedSessionIDs = append(result.AffectedSessionIDs, sessionID)
	}

	return result
}
