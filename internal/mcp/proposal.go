package mcp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kokistudios/card/internal/capsule"
	"github.com/kokistudios/card/internal/store"
)

// DefaultProposalTTL is the default time-to-live for proposals (30 minutes).
const DefaultProposalTTL = 30 * time.Minute

// SimilarDecision represents an existing decision that is semantically similar to a proposal.
type SimilarDecision struct {
	ID               string `json:"id"`
	Question         string `json:"question"`
	Choice           string `json:"choice"`
	Phase            string `json:"phase"`
	SimilarityReason string `json:"similarity_reason"`
}

// ConflictingDecision represents a prior active decision that contradicts a proposal.
type ConflictingDecision struct {
	ID                 string `json:"id"`
	Question           string `json:"question"`
	Choice             string `json:"choice"`
	SessionID          string `json:"session_id"`
	SessionDescription string `json:"session_description"`
	Reason             string `json:"reason,omitempty"` // Why it contradicts
}

// Proposal represents a pending decision awaiting human confirmation.
type Proposal struct {
	ID              string                `json:"id"`
	SessionID       string                `json:"session_id"`
	Capsule         capsule.Capsule       `json:"capsule"`
	SimilarExisting []SimilarDecision     `json:"similar_existing,omitempty"`
	Contradicts     []ConflictingDecision `json:"contradicts,omitempty"`
	SuggestedAction string                `json:"suggested_action"` // "create", "supersedes:<id>", "duplicate_of:<id>"
	CreatedAt       time.Time             `json:"created_at"`
	ExpiresAt       time.Time             `json:"expires_at"`
}

// IsExpired returns true if the proposal has exceeded its TTL.
func (p *Proposal) IsExpired() bool {
	return time.Now().After(p.ExpiresAt)
}

// ProposalStore manages pending decision proposals with disk persistence.
// Proposals are stored per-session in proposals.json files within session directories.
type ProposalStore struct {
	st  *store.Store
	mu  sync.RWMutex
	ttl time.Duration
}

// NewProposalStore creates a new proposal store backed by disk.
func NewProposalStore(st *store.Store) *ProposalStore {
	return &ProposalStore{
		st:  st,
		ttl: DefaultProposalTTL,
	}
}

// NewProposalStoreWithTTL creates a new proposal store with a custom TTL.
func NewProposalStoreWithTTL(st *store.Store, ttl time.Duration) *ProposalStore {
	return &ProposalStore{
		st:  st,
		ttl: ttl,
	}
}

// generateProposalID creates a unique proposal ID using crypto/rand.
func generateProposalID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// proposalsFilePath returns the path to the proposals.json file for a session.
func (ps *ProposalStore) proposalsFilePath(sessionID string) string {
	return ps.st.Path("sessions", sessionID, "proposals.json")
}

// loadProposals reads all proposals for a session from disk.
func (ps *ProposalStore) loadProposals(sessionID string) ([]*Proposal, error) {
	path := ps.proposalsFilePath(sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No proposals file yet
		}
		return nil, err
	}

	var proposals []*Proposal
	if err := json.Unmarshal(data, &proposals); err != nil {
		return nil, fmt.Errorf("invalid proposals.json: %w", err)
	}
	return proposals, nil
}

// saveProposals writes all proposals for a session to disk.
func (ps *ProposalStore) saveProposals(sessionID string, proposals []*Proposal) error {
	path := ps.proposalsFilePath(sessionID)

	// Ensure session directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}

	// Filter out expired proposals before saving
	now := time.Now()
	var active []*Proposal
	for _, p := range proposals {
		if !now.After(p.ExpiresAt) {
			active = append(active, p)
		}
	}

	// If no active proposals, remove the file
	if len(active) == 0 {
		os.Remove(path) // Ignore errors - file may not exist
		return nil
	}

	data, err := json.MarshalIndent(active, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal proposals: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write proposals.json: %w", err)
	}
	return nil
}

// Create stores a new proposal and returns its unique ID.
func (ps *ProposalStore) Create(p *Proposal) string {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	// Generate unique ID if not set
	if p.ID == "" {
		p.ID = generateProposalID()
	}

	// Set timestamps
	p.CreatedAt = time.Now().UTC()
	p.ExpiresAt = p.CreatedAt.Add(ps.ttl)

	// Load existing proposals for this session
	proposals, _ := ps.loadProposals(p.SessionID)
	proposals = append(proposals, p)

	// Save to disk
	if err := ps.saveProposals(p.SessionID, proposals); err != nil {
		// Log error but don't fail - return the ID anyway
		fmt.Fprintf(os.Stderr, "warning: failed to persist proposal: %v\n", err)
	}

	return p.ID
}

// Get retrieves a proposal by ID. Returns error if not found or expired.
func (ps *ProposalStore) Get(id string) (*Proposal, error) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	// We need to search all sessions since we only have the proposal ID
	// In practice, this is okay because there are few sessions with proposals
	sessionsDir := ps.st.Path("sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("proposal not found: %s", id)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		proposals, err := ps.loadProposals(e.Name())
		if err != nil {
			continue
		}
		for _, p := range proposals {
			if p.ID == id {
				if p.IsExpired() {
					return nil, fmt.Errorf("proposal expired: %s", id)
				}
				return p, nil
			}
		}
	}

	return nil, fmt.Errorf("proposal not found: %s", id)
}

// Delete removes a proposal from the store.
func (ps *ProposalStore) Delete(id string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	// Search all sessions for the proposal
	sessionsDir := ps.st.Path("sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		proposals, err := ps.loadProposals(e.Name())
		if err != nil {
			continue
		}

		// Find and remove the proposal
		var updated []*Proposal
		found := false
		for _, p := range proposals {
			if p.ID == id {
				found = true
			} else {
				updated = append(updated, p)
			}
		}

		if found {
			ps.saveProposals(e.Name(), updated)
			return
		}
	}
}

// Cleanup removes all expired proposals from all sessions.
// Returns the number of proposals cleaned up.
func (ps *ProposalStore) Cleanup() int {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	count := 0
	sessionsDir := ps.st.Path("sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return 0
	}

	now := time.Now()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		proposals, err := ps.loadProposals(e.Name())
		if err != nil {
			continue
		}

		var active []*Proposal
		sessionCount := 0
		for _, p := range proposals {
			if now.After(p.ExpiresAt) {
				sessionCount++
			} else {
				active = append(active, p)
			}
		}

		if sessionCount > 0 {
			ps.saveProposals(e.Name(), active)
			count += sessionCount
		}
	}

	return count
}

// Count returns the number of active (non-expired) proposals across all sessions.
func (ps *ProposalStore) Count() int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	count := 0
	sessionsDir := ps.st.Path("sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return 0
	}

	now := time.Now()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		proposals, _ := ps.loadProposals(e.Name())
		for _, p := range proposals {
			if !now.After(p.ExpiresAt) {
				count++
			}
		}
	}

	return count
}

// ListBySession returns all active proposals for a given session.
func (ps *ProposalStore) ListBySession(sessionID string) []*Proposal {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	proposals, err := ps.loadProposals(sessionID)
	if err != nil {
		return nil
	}

	var result []*Proposal
	now := time.Now()
	for _, p := range proposals {
		if !now.After(p.ExpiresAt) {
			result = append(result, p)
		}
	}
	return result
}

// StartCleanupRoutine starts a background goroutine that periodically cleans up
// expired proposals. Returns a channel that can be closed to stop the routine.
func (ps *ProposalStore) StartCleanupRoutine(interval time.Duration) chan struct{} {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				ps.Cleanup()
			case <-stop:
				return
			}
		}
	}()
	return stop
}
