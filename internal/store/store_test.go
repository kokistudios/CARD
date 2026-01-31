package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInit(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".card")

	if err := Init(home, false); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Verify structure
	for _, d := range []string{"repos", "sessions"} {
		p := filepath.Join(home, d)
		info, err := os.Stat(p)
		if err != nil {
			t.Errorf("expected directory %s to exist", d)
		} else if !info.IsDir() {
			t.Errorf("expected %s to be a directory", d)
		}
	}

	// config.yaml should exist
	if _, err := os.Stat(filepath.Join(home, "config.yaml")); err != nil {
		t.Error("expected config.yaml to exist")
	}

	// Second init should fail without force
	if err := Init(home, false); err == nil {
		t.Error("expected error on duplicate init")
	}

	// Force should succeed
	if err := Init(home, true); err != nil {
		t.Errorf("expected force init to succeed: %v", err)
	}
}

func TestLoad(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".card")
	Init(home, false)

	s, err := Load(home)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if s.Home != home {
		t.Errorf("expected Home=%s, got %s", home, s.Home)
	}
}

func TestPath(t *testing.T) {
	s := &Store{Home: "/tmp/.card"}
	got := s.Path("repos", "abc.yaml")
	want := filepath.Join("/tmp/.card", "repos", "abc.yaml")
	if got != want {
		t.Errorf("Path() = %s, want %s", got, want)
	}
}

func TestCheckHealth(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".card")
	Init(home, false)

	issues := CheckHealth(home)
	if len(issues) != 0 {
		t.Errorf("expected no issues, got %v", issues)
	}

	// Remove a directory to trigger an issue
	os.RemoveAll(filepath.Join(home, "repos"))
	issues = CheckHealth(home)
	if len(issues) == 0 {
		t.Error("expected issues after removing repos dir")
	}
}

func TestHomeEnvVar(t *testing.T) {
	t.Setenv("CARD_HOME", "/custom/path")
	if got := Home(); got != "/custom/path" {
		t.Errorf("Home() = %s, want /custom/path", got)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Claude.Path != "claude" {
		t.Errorf("expected default claude path 'claude', got %s", cfg.Claude.Path)
	}
	if !cfg.Session.AutoContinueSimplify {
		t.Error("expected auto_continue_simplify true by default")
	}
	if !cfg.Session.AutoContinueRecord {
		t.Error("expected auto_continue_record true by default")
	}
	if cfg.Recall.MaxContextBlocks != 10 {
		t.Errorf("expected max_context_blocks 10, got %d", cfg.Recall.MaxContextBlocks)
	}
	if cfg.Recall.MaxContextChars != 8000 {
		t.Errorf("expected max_context_chars 8000, got %d", cfg.Recall.MaxContextChars)
	}
}

func TestLoadMergesDefaults(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".card")
	Init(home, false)

	// Write a minimal config with only version
	os.WriteFile(filepath.Join(home, "config.yaml"), []byte("version: \"1\"\n"), 0644)

	s, err := Load(home)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	// Default values should be filled in
	if s.Config.Claude.Path != "claude" {
		t.Errorf("expected default claude path, got %s", s.Config.Claude.Path)
	}
	if s.Config.Recall.MaxContextBlocks != 10 {
		t.Errorf("expected default max_context_blocks, got %d", s.Config.Recall.MaxContextBlocks)
	}
}

func TestSetConfigValue(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".card")
	Init(home, false)
	s, _ := Load(home)

	if err := s.SetConfigValue("claude.path", "/usr/local/bin/claude"); err != nil {
		t.Fatal(err)
	}
	if s.Config.Claude.Path != "/usr/local/bin/claude" {
		t.Errorf("expected updated path, got %s", s.Config.Claude.Path)
	}

	// Reload and verify persistence
	s2, _ := Load(home)
	if s2.Config.Claude.Path != "/usr/local/bin/claude" {
		t.Errorf("config not persisted, got %s", s2.Config.Claude.Path)
	}
}

func TestSetConfigValue_InvalidKey(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".card")
	Init(home, false)
	s, _ := Load(home)

	if err := s.SetConfigValue("nonexistent.key", "value"); err == nil {
		t.Error("expected error for unknown key")
	}
}

func TestSetConfigValue_InvalidInt(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".card")
	Init(home, false)
	s, _ := Load(home)

	if err := s.SetConfigValue("recall.max_context_blocks", "notanumber"); err == nil {
		t.Error("expected error for non-integer value")
	}
}

func TestFixIssues(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".card")
	Init(home, false)

	// Remove repos dir
	os.RemoveAll(filepath.Join(home, "repos"))

	fixed := FixIssues(home)
	if len(fixed) == 0 {
		t.Error("expected at least one fix")
	}

	// Verify directory was recreated
	if _, err := os.Stat(filepath.Join(home, "repos")); err != nil {
		t.Error("repos dir not recreated")
	}
}

func TestCheckSessionIntegrity_Valid(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".card")
	Init(home, false)

	issues := CheckSessionIntegrity(home)
	if len(issues) != 0 {
		t.Errorf("expected no issues for empty sessions dir, got %v", issues)
	}
}

func TestCheckCapsuleIntegrity_Valid(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".card")
	Init(home, false)

	issues := CheckCapsuleIntegrity(home)
	if len(issues) != 0 {
		t.Errorf("expected no issues for empty sessions dir, got %v", issues)
	}
}
