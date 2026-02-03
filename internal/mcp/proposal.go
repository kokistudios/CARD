package mcp

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/kokistudios/card/internal/capsule"
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

// ProposalStore manages pending decision proposals in memory.
type ProposalStore struct {
	proposals map[string]*Proposal
	mu        sync.RWMutex
	ttl       time.Duration
}

// NewProposalStore creates a new proposal store with the default TTL.
func NewProposalStore() *ProposalStore {
	return &ProposalStore{
		proposals: make(map[string]*Proposal),
		ttl:       DefaultProposalTTL,
	}
}

// NewProposalStoreWithTTL creates a new proposal store with a custom TTL.
func NewProposalStoreWithTTL(ttl time.Duration) *ProposalStore {
	return &ProposalStore{
		proposals: make(map[string]*Proposal),
		ttl:       ttl,
	}
}

// generateProposalID creates a unique proposal ID using crypto/rand.
func generateProposalID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
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

	ps.proposals[p.ID] = p
	return p.ID
}

// Get retrieves a proposal by ID. Returns error if not found or expired.
func (ps *ProposalStore) Get(id string) (*Proposal, error) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	p, ok := ps.proposals[id]
	if !ok {
		return nil, fmt.Errorf("proposal not found: %s", id)
	}

	if p.IsExpired() {
		return nil, fmt.Errorf("proposal expired: %s", id)
	}

	return p, nil
}

// Delete removes a proposal from the store.
func (ps *ProposalStore) Delete(id string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	delete(ps.proposals, id)
}

// Cleanup removes all expired proposals from the store.
// Returns the number of proposals cleaned up.
func (ps *ProposalStore) Cleanup() int {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	count := 0
	now := time.Now()
	for id, p := range ps.proposals {
		if now.After(p.ExpiresAt) {
			delete(ps.proposals, id)
			count++
		}
	}
	return count
}

// Count returns the number of active (non-expired) proposals.
func (ps *ProposalStore) Count() int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	count := 0
	now := time.Now()
	for _, p := range ps.proposals {
		if !now.After(p.ExpiresAt) {
			count++
		}
	}
	return count
}

// ListBySession returns all active proposals for a given session.
func (ps *ProposalStore) ListBySession(sessionID string) []*Proposal {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	var result []*Proposal
	now := time.Now()
	for _, p := range ps.proposals {
		if p.SessionID == sessionID && !now.After(p.ExpiresAt) {
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
