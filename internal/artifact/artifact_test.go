package artifact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kokistudios/card/internal/store"
)

func TestParse_WithFrontmatter(t *testing.T) {
	raw := []byte(`---
session: test-session
repos:
  - abc123
phase: investigate
status: final
---

## Executive Summary

This is the body.
`)
	a, err := Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Frontmatter.Session != "test-session" {
		t.Errorf("session = %q, want %q", a.Frontmatter.Session, "test-session")
	}
	if a.Frontmatter.Phase != "investigate" {
		t.Errorf("phase = %q, want %q", a.Frontmatter.Phase, "investigate")
	}
	if !strings.Contains(a.Body, "## Executive Summary") {
		t.Error("body missing expected content")
	}
}

func TestParse_NoFrontmatter(t *testing.T) {
	raw := []byte("# Just a markdown file\n\nSome content.")
	a, err := Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Frontmatter.Session != "" {
		t.Errorf("expected empty session, got %q", a.Frontmatter.Session)
	}
	if !strings.Contains(a.Body, "# Just a markdown file") {
		t.Error("body missing expected content")
	}
}

func TestParse_UnterminatedFrontmatter(t *testing.T) {
	raw := []byte("---\nsession: test\nno closing delimiter")
	_, err := Parse(raw)
	if err == nil {
		t.Fatal("expected error for unterminated frontmatter")
	}
}

func TestValidate_Investigation(t *testing.T) {
	a := &Artifact{Body: "## Executive Summary\n\nFindings here."}
	if err := Validate(a, "investigate"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	bad := &Artifact{Body: "Nothing relevant here."}
	if err := Validate(bad, "investigate"); err == nil {
		t.Error("expected validation error for missing sections")
	}
}

func TestValidate_Plan(t *testing.T) {
	a := &Artifact{Body: "## Implementation Steps\n\nStep 1: do stuff"}
	if err := Validate(a, "plan"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_Simplify(t *testing.T) {
	// Simplify always passes (no artifact)
	if err := Validate(nil, "simplify"); err != nil {
		t.Errorf("simplify should always pass, got: %v", err)
	}
}

func TestValidate_UnknownPhase(t *testing.T) {
	a := &Artifact{Body: "stuff"}
	if err := Validate(a, "nonexistent"); err == nil {
		t.Error("expected error for unknown phase")
	}
}

func TestPhaseFilename(t *testing.T) {
	cases := map[string]string{
		"investigate": "investigation_summary.md",
		"plan":        "implementation_guide.md",
		"execute":     "execution_log.md",
		"record":      "milestone_ledger.md",
		"unknown":     "unknown.md",
	}
	for phase, want := range cases {
		got := PhaseFilename(phase)
		if got != want {
			t.Errorf("PhaseFilename(%q) = %q, want %q", phase, got, want)
		}
	}
}

func TestStoreAndLoad(t *testing.T) {
	tmp := t.TempDir()
	if err := store.Init(tmp, true); err != nil {
		t.Fatalf("store init: %v", err)
	}
	s, err := store.Load(tmp)
	if err != nil {
		t.Fatalf("store load: %v", err)
	}

	a := &Artifact{
		Frontmatter: ArtifactMeta{
			Session:   "test-sess",
			Repos:     []string{"abc123"},
			Phase:     "investigate",
			Timestamp: time.Now().UTC(),
			Status:    "final",
		},
		Body: "## Executive Summary\n\nTest content.",
	}

	path, err := Store(s, "test-sess", "abc123", a)
	if err != nil {
		t.Fatalf("store artifact: %v", err)
	}

	expected := filepath.Join(tmp, "sessions", "test-sess", "changes", "abc123", "investigation_summary.md")
	if path != expected {
		t.Errorf("path = %q, want %q", path, expected)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load artifact: %v", err)
	}
	if loaded.Frontmatter.Session != "test-sess" {
		t.Errorf("loaded session = %q, want %q", loaded.Frontmatter.Session, "test-sess")
	}
	if !strings.Contains(loaded.Body, "## Executive Summary") {
		t.Error("loaded body missing expected content")
	}
}

func TestStore_CreatesDirectories(t *testing.T) {
	tmp := t.TempDir()
	if err := store.Init(tmp, true); err != nil {
		t.Fatal(err)
	}
	s, _ := store.Load(tmp)

	a := &Artifact{
		Frontmatter: ArtifactMeta{Phase: "plan"},
		Body:        "## Implementation Steps\n\nStep 1",
	}

	path, err := Store(s, "new-session", "new-repo", a)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("artifact file not created: %v", err)
	}
}
