package signal

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// PhaseCompleteSignal represents a phase completion signal written by Claude
// via the card_phase_complete MCP tool.
type PhaseCompleteSignal struct {
	SessionID string    `yaml:"session_id"`
	Phase     string    `yaml:"phase"`
	Status    string    `yaml:"status"` // "complete", "blocked", "needs_input"
	Timestamp time.Time `yaml:"timestamp"`
	Summary   string    `yaml:"summary,omitempty"`
}

// ValidPhaseCompleteStatus contains the allowed status values.
var ValidPhaseCompleteStatus = map[string]bool{
	"complete":    true,
	"blocked":     true,
	"needs_input": true,
}

// PhaseCompleteSignalPath returns the path to the phase complete signal file.
func PhaseCompleteSignalPath(workDir string) string {
	return filepath.Join(workDir, "signals", "phase_complete.yaml")
}

// CheckPhaseComplete looks for a phase complete signal file in the work directory.
// Returns nil if no signal file exists.
func CheckPhaseComplete(workDir string) (*PhaseCompleteSignal, error) {
	path := PhaseCompleteSignalPath(workDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read phase complete signal: %w", err)
	}

	var sig PhaseCompleteSignal
	if err := yaml.Unmarshal(data, &sig); err != nil {
		return nil, fmt.Errorf("failed to parse phase complete signal: %w", err)
	}

	// Validate required fields
	if sig.SessionID == "" || sig.Phase == "" || sig.Status == "" {
		return nil, fmt.Errorf("phase complete signal missing required fields")
	}

	return &sig, nil
}

// WritePhaseComplete writes the phase complete signal file.
func WritePhaseComplete(workDir string, sig *PhaseCompleteSignal) error {
	if sig == nil {
		return fmt.Errorf("signal cannot be nil")
	}

	// Validate status
	if !ValidPhaseCompleteStatus[sig.Status] {
		return fmt.Errorf("invalid status %q: must be complete, blocked, or needs_input", sig.Status)
	}

	// Ensure signals directory exists
	signalsDir := filepath.Join(workDir, "signals")
	if err := os.MkdirAll(signalsDir, 0755); err != nil {
		return fmt.Errorf("failed to create signals directory: %w", err)
	}

	data, err := yaml.Marshal(sig)
	if err != nil {
		return fmt.Errorf("failed to marshal phase complete signal: %w", err)
	}

	path := PhaseCompleteSignalPath(workDir)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write phase complete signal: %w", err)
	}

	return nil
}

// ClearPhaseComplete removes the phase complete signal file if it exists.
func ClearPhaseComplete(workDir string) error {
	path := PhaseCompleteSignalPath(workDir)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to clear phase complete signal: %w", err)
	}
	return nil
}
