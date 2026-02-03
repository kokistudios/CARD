package capsule

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kokistudios/card/internal/artifact"
	"github.com/kokistudios/card/internal/store"
)

// CapsuleStatus represents the verification state of a capsule.
type CapsuleStatus string

const (
	StatusHypothesis  CapsuleStatus = "hypothesis"  // Unverified conclusion
	StatusVerified    CapsuleStatus = "verified"    // Confirmed by verification phase
	StatusInvalidated CapsuleStatus = "invalidated" // Proven wrong or outdated
)

// CapsuleType distinguishes decisions from findings.
type CapsuleType string

const (
	TypeDecision CapsuleType = "decision" // Question → Choice → Rationale with alternatives
	TypeFinding  CapsuleType = "finding"  // Observation or conclusion (no alternatives)
)

// Significance represents the impact tier of a decision.
type Significance string

const (
	SignificanceArchitectural   Significance = "architectural"   // Trade-offs, multiple viable alternatives, shapes future work
	SignificanceImplementation  Significance = "implementation"  // Pattern-following, obvious choices, easily reversible
	SignificanceContext         Significance = "context"         // Facts discovered, constraints identified, not really decisions
)

// Confirmation indicates how the decision was confirmed.
type Confirmation string

const (
	ConfirmationExplicit Confirmation = "explicit" // Human explicitly confirmed via card_decision_confirm
	ConfirmationImplicit Confirmation = "implicit" // Stored immediately without human confirmation
)

// Challenge records when a capsule's validity was questioned.
type Challenge struct {
	Timestamp  time.Time `yaml:"timestamp"`
	SessionID  string    `yaml:"session_id"`
	Reason     string    `yaml:"reason"`
	Learned    string    `yaml:"learned,omitempty"`  // What was learned from invalidation (distinct from reason)
	Resolution string    `yaml:"resolution"`         // "verified", "invalidated", "superseded", "pending"
}

// Capsule represents a single decision or finding captured during a CARD phase.
type Capsule struct {
	ID           string    `yaml:"id"`
	SessionID    string    `yaml:"session"`
	RepoIDs      []string  `yaml:"repos,omitempty"`
	Phase        string    `yaml:"phase"`
	Timestamp    time.Time `yaml:"timestamp"`
	Question     string    `yaml:"question"`
	Choice       string    `yaml:"choice"`
	Alternatives []string  `yaml:"alternatives,omitempty"`
	Rationale    string    `yaml:"rationale"`
	Source       string    `yaml:"source,omitempty"` // "human" or "agent" (legacy, use Origin)
	Tags         []string  `yaml:"tags,omitempty"`
	Commits      []string  `yaml:"commits,omitempty"`

	// Status and type (PENSIEVE enhancement)
	Status CapsuleStatus `yaml:"status,omitempty"`
	Type   CapsuleType   `yaml:"type,omitempty"`

	// Decision system redesign fields
	Significance  Significance `yaml:"significance,omitempty"`  // architectural, implementation, context
	PatternID     string       `yaml:"pattern_id,omitempty"`    // Links to pattern this established/follows
	Origin        string       `yaml:"origin,omitempty"`        // "human" or "agent" (replaces Source)
	Confirmation  Confirmation `yaml:"confirmation,omitempty"`  // explicit, implicit
	CreatedAt     time.Time    `yaml:"created_at,omitempty"`    // When capsule was created (for temporal queries)
	InvalidatedAt *time.Time   `yaml:"invalidated_at,omitempty"` // When capsule was invalidated

	// Dependency graph
	Enables    []string `yaml:"enables,omitempty"`    // Capsule IDs this decision enables
	EnabledBy  string   `yaml:"enabled_by,omitempty"` // Capsule ID that enabled this decision
	Constrains []string `yaml:"constrains,omitempty"` // Capsule IDs whose choices this limits

	// Supersession relationships
	SupersededBy string   `yaml:"superseded_by,omitempty"` // Capsule ID that replaces this
	Supersedes   []string `yaml:"supersedes,omitempty"`    // Capsule IDs this replaces

	// Invalidation metadata
	InvalidationReason string `yaml:"invalidation_reason,omitempty"` // Why this decision was invalidated
	Learned            string `yaml:"learned,omitempty"`             // Insight gained from invalidation

	// Challenge history
	Challenges []Challenge `yaml:"challenges,omitempty"`
}

// Filter defines query parameters for listing capsules.
type Filter struct {
	SessionID          *string
	RepoID             *string       // matches against any entry in RepoIDs
	Phase              *string
	FilePath           *string       // match against Tags
	Tag                *string       // match against Tags
	Status             *CapsuleStatus // filter by status (verified, hypothesis, invalidated)
	Type               *CapsuleType   // filter by type (decision, finding)
	Significance       *Significance  // filter by significance tier
	IncludeInvalidated bool          // if true, include invalidated capsules (default: exclude)
	ShowEvolution      bool          // if false (default), deduplicate to latest phase per question
}

// GenerateID creates a deterministic capsule ID from session, phase, and question.
func GenerateID(sessionID, phase, question string) string {
	h := sha256.Sum256([]byte(question))
	shortHash := fmt.Sprintf("%x", h[:4])
	return fmt.Sprintf("%s-%s-%s", sessionID, phase, shortHash)
}

// ExtractFromArtifact parses decision capsules from an artifact's markdown body.
//
// DEPRECATED: This function implements the legacy "document → extract" model.
// New decisions should be captured using the card_decision MCP tool, which stores
// capsules immediately without requiring post-phase extraction.
// This function is retained for backward compatibility with older sessions that
// used the "### Decision:" markdown format in artifacts.
func ExtractFromArtifact(art *artifact.Artifact) ([]Capsule, error) {
	if art == nil {
		return nil, fmt.Errorf("artifact is nil")
	}

	body := art.Body
	var capsules []Capsule

	// Match "### Decision: <question>" headers
	decisionRe := regexp.MustCompile(`(?mi)^###\s+Decision:\s*(.+)$`)
	matches := decisionRe.FindAllStringSubmatchIndex(body, -1)

	for i, match := range matches {
		question := strings.TrimSpace(body[match[2]:match[3]])

		// Extract the block between this header and the next ### or ## or EOF
		blockStart := match[1]
		var blockEnd int
		if i+1 < len(matches) {
			blockEnd = matches[i+1][0]
		} else {
			nextHeader := regexp.MustCompile(`(?m)^##[^#]`)
			loc := nextHeader.FindStringIndex(body[blockStart:])
			if loc != nil {
				blockEnd = blockStart + loc[0]
			} else {
				blockEnd = len(body)
			}
		}

		block := body[blockStart:blockEnd]

		c := Capsule{
			SessionID: art.Frontmatter.Session,
			RepoIDs:   art.Frontmatter.Repos,
			Phase:     art.Frontmatter.Phase,
			Timestamp: art.Frontmatter.Timestamp,
			Question:  question,
			Choice:    extractField(block, "Choice"),
			Rationale: extractField(block, "Rationale"),
			Source:    extractField(block, "Source"),
			Tags:      splitCSV(extractField(block, "Tags")),
		}

		alts := extractField(block, "Alternatives")
		if alts != "" {
			c.Alternatives = splitCSV(alts)
		}

		if c.Source == "" {
			c.Source = "agent"
		}

		// Parse status (default to hypothesis for new capsules)
		statusStr := extractField(block, "Status")
		if statusStr != "" {
			c.Status = CapsuleStatus(strings.ToLower(statusStr))
		} else {
			c.Status = StatusHypothesis
		}

		// Parse type (infer from alternatives: has alternatives = decision, no alternatives = finding)
		typeStr := extractField(block, "Type")
		if typeStr != "" {
			c.Type = CapsuleType(strings.ToLower(typeStr))
		} else if len(c.Alternatives) > 0 {
			c.Type = TypeDecision
		} else {
			c.Type = TypeFinding
		}

		c.ID = GenerateID(c.SessionID, c.Phase, c.Question)
		capsules = append(capsules, c)
	}

	return capsules, nil
}

// extractField pulls the value from a "- **Label:** value" line.
func extractField(block, label string) string {
	re := regexp.MustCompile(`(?mi)[-*]\s*\*\*` + regexp.QuoteMeta(label) + `:\*\*\s*(.+)$`)
	m := re.FindStringSubmatch(block)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// splitCSV splits a comma-separated string into trimmed parts.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// capsulesFilePath returns the path to the consolidated capsules.md for a session.
func capsulesFilePath(st *store.Store, sessionID string) string {
	return st.Path("sessions", sessionID, "capsules.md")
}

// Store writes a capsule to the consolidated capsules.md file for its session.
// If the file already exists, the capsule is appended (or replaced if same ID).
func Store(st *store.Store, c Capsule) error {
	p := capsulesFilePath(st, c.SessionID)

	// Load existing capsules from the file
	existing, _ := loadConsolidatedFile(p)

	// Replace or append
	found := false
	for i, ec := range existing {
		if ec.ID == c.ID {
			existing[i] = c
			found = true
			break
		}
	}
	if !found {
		existing = append(existing, c)
	}

	return writeConsolidatedFile(p, c.SessionID, existing)
}

// writeConsolidatedFile writes all capsules to a single markdown file grouped by phase.
func writeConsolidatedFile(path, sessionID string, capsules []Capsule) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}

	// Group by phase
	phaseOrder := []string{"quickfix-seed", "investigate", "plan", "execute", "simplify", "record"}
	byPhase := make(map[string][]Capsule)
	for _, c := range capsules {
		byPhase[c.Phase] = append(byPhase[c.Phase], c)
	}

	var buf bytes.Buffer
	buf.WriteString("---\n")
	fmt.Fprintf(&buf, "session: %s\n", sessionID)
	buf.WriteString("type: capsules\n")
	buf.WriteString("---\n\n")
	buf.WriteString("# Decision Capsules\n\n")
	fmt.Fprintf(&buf, "**Session:** [[%s]]\n\n", sessionID)

	for _, phase := range phaseOrder {
		phaseCaps, ok := byPhase[phase]
		if !ok || len(phaseCaps) == 0 {
			continue
		}

		fmt.Fprintf(&buf, "## %s\n\n", phase)

		for _, c := range phaseCaps {
			fmt.Fprintf(&buf, "### Decision: %s\n", c.Question)
			fmt.Fprintf(&buf, "- **ID:** %s\n", c.ID)
			fmt.Fprintf(&buf, "- **Choice:** %s\n", c.Choice)
			if len(c.Alternatives) > 0 {
				fmt.Fprintf(&buf, "- **Alternatives:** %s\n", strings.Join(c.Alternatives, ", "))
			}
			fmt.Fprintf(&buf, "- **Rationale:** %s\n", c.Rationale)
			// Write Origin if set, otherwise fall back to Source for backwards compatibility
			origin := c.Origin
			if origin == "" {
				origin = c.Source
			}
			if origin != "" {
				fmt.Fprintf(&buf, "- **Origin:** %s\n", origin)
			}
			if c.Status != "" {
				fmt.Fprintf(&buf, "- **Status:** %s\n", c.Status)
			}
			if c.Type != "" {
				fmt.Fprintf(&buf, "- **Type:** %s\n", c.Type)
			}
			// New decision system redesign fields
			if c.Significance != "" {
				fmt.Fprintf(&buf, "- **Significance:** %s\n", c.Significance)
			}
			if c.Confirmation != "" {
				fmt.Fprintf(&buf, "- **Confirmation:** %s\n", c.Confirmation)
			}
			if c.PatternID != "" {
				fmt.Fprintf(&buf, "- **PatternID:** %s\n", c.PatternID)
			}
			if len(c.Tags) > 0 {
				fmt.Fprintf(&buf, "- **Tags:** %s\n", strings.Join(c.Tags, ", "))
			}
			if !c.Timestamp.IsZero() {
				fmt.Fprintf(&buf, "- **Timestamp:** %s\n", c.Timestamp.Format(time.RFC3339))
			}
			if !c.CreatedAt.IsZero() {
				fmt.Fprintf(&buf, "- **CreatedAt:** %s\n", c.CreatedAt.Format(time.RFC3339))
			}
			if c.InvalidatedAt != nil {
				fmt.Fprintf(&buf, "- **InvalidatedAt:** %s\n", c.InvalidatedAt.Format(time.RFC3339))
			}
			if len(c.RepoIDs) > 0 {
				fmt.Fprintf(&buf, "- **Repos:** %s\n", strings.Join(c.RepoIDs, ", "))
			}
			if len(c.Commits) > 0 {
				fmt.Fprintf(&buf, "- **Commits:** %s\n", strings.Join(c.Commits, ", "))
			}
			// Dependency graph
			if c.EnabledBy != "" {
				fmt.Fprintf(&buf, "- **EnabledBy:** %s\n", c.EnabledBy)
			}
			if len(c.Enables) > 0 {
				fmt.Fprintf(&buf, "- **Enables:** %s\n", strings.Join(c.Enables, ", "))
			}
			if len(c.Constrains) > 0 {
				fmt.Fprintf(&buf, "- **Constrains:** %s\n", strings.Join(c.Constrains, ", "))
			}
			// Supersession
			if c.SupersededBy != "" {
				fmt.Fprintf(&buf, "- **SupersededBy:** %s\n", c.SupersededBy)
			}
			if len(c.Supersedes) > 0 {
				fmt.Fprintf(&buf, "- **Supersedes:** %s\n", strings.Join(c.Supersedes, ", "))
			}
			// Invalidation metadata
			if c.InvalidationReason != "" {
				fmt.Fprintf(&buf, "- **InvalidationReason:** %s\n", c.InvalidationReason)
			}
			if c.Learned != "" {
				fmt.Fprintf(&buf, "- **Learned:** %s\n", c.Learned)
			}
			buf.WriteString("\n")
		}
	}

	return os.WriteFile(path, buf.Bytes(), 0644)
}

// loadConsolidatedFile parses all capsules from a consolidated capsules.md file.
func loadConsolidatedFile(path string) ([]Capsule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseConsolidatedCapsules(string(data))
}

// parseConsolidatedCapsules extracts capsules from the consolidated markdown format.
func parseConsolidatedCapsules(content string) ([]Capsule, error) {
	var capsules []Capsule

	// Extract session ID from frontmatter
	sessionID := ""
	if strings.HasPrefix(strings.TrimSpace(content), "---") {
		rest := strings.TrimSpace(content)[3:]
		rest = strings.TrimLeft(rest, " \t\r\n")
		endIdx := strings.Index(rest, "\n---")
		if endIdx != -1 {
			fmRaw := rest[:endIdx]
			var fm struct {
				Session string `yaml:"session"`
			}
			yaml.Unmarshal([]byte(fmRaw), &fm)
			sessionID = fm.Session
		}
	}

	// Find phase headers (## phase)
	phaseRe := regexp.MustCompile(`(?m)^## (\w+)\s*$`)
	phaseMatches := phaseRe.FindAllStringSubmatchIndex(content, -1)

	// Find decision headers (### Decision: question)
	decisionRe := regexp.MustCompile(`(?mi)^### Decision:\s*(.+)$`)

	for pi, pm := range phaseMatches {
		phase := content[pm[2]:pm[3]]
		blockStart := pm[1]
		var blockEnd int
		if pi+1 < len(phaseMatches) {
			blockEnd = phaseMatches[pi+1][0]
		} else {
			blockEnd = len(content)
		}
		phaseBlock := content[blockStart:blockEnd]

		decMatches := decisionRe.FindAllStringSubmatchIndex(phaseBlock, -1)
		for di, dm := range decMatches {
			question := strings.TrimSpace(phaseBlock[dm[2]:dm[3]])
			decStart := dm[1]
			var decEnd int
			if di+1 < len(decMatches) {
				decEnd = decMatches[di+1][0]
			} else {
				decEnd = len(phaseBlock)
			}
			decBlock := phaseBlock[decStart:decEnd]

			c := Capsule{
				SessionID: sessionID,
				Phase:     phase,
				Question:  question,
				ID:        extractField(decBlock, "ID"),
				Choice:    extractField(decBlock, "Choice"),
				Rationale: extractField(decBlock, "Rationale"),
				Source:    extractField(decBlock, "Source"),
				RepoIDs:   splitCSV(extractField(decBlock, "Repos")),
				Tags:      splitCSV(extractField(decBlock, "Tags")),
			}

			alts := extractField(decBlock, "Alternatives")
			if alts != "" {
				c.Alternatives = splitCSV(alts)
			}

			commits := extractField(decBlock, "Commits")
			if commits != "" {
				c.Commits = splitCSV(commits)
			}

			ts := extractField(decBlock, "Timestamp")
			if ts != "" {
				if t, err := time.Parse(time.RFC3339, ts); err == nil {
					c.Timestamp = t
				}
			}

			// Parse status
			statusStr := extractField(decBlock, "Status")
			if statusStr != "" {
				c.Status = CapsuleStatus(strings.ToLower(statusStr))
			} else {
				c.Status = StatusHypothesis
			}

			// Parse type
			typeStr := extractField(decBlock, "Type")
			if typeStr != "" {
				c.Type = CapsuleType(strings.ToLower(typeStr))
			} else if len(c.Alternatives) > 0 {
				c.Type = TypeDecision
			} else {
				c.Type = TypeFinding
			}

			// Parse new decision system redesign fields
			// Origin (replaces Source, but read both for backwards compatibility)
			c.Origin = extractField(decBlock, "Origin")
			if c.Origin == "" && c.Source != "" {
				c.Origin = c.Source // Migrate Source to Origin
			}

			// Significance (default to implementation for backwards compatibility)
			sigStr := extractField(decBlock, "Significance")
			if sigStr != "" {
				c.Significance = Significance(strings.ToLower(sigStr))
			} else {
				c.Significance = SignificanceImplementation // Default for legacy capsules
			}

			// Confirmation (default to implicit for backwards compatibility)
			confStr := extractField(decBlock, "Confirmation")
			if confStr != "" {
				c.Confirmation = Confirmation(strings.ToLower(confStr))
			} else {
				c.Confirmation = ConfirmationImplicit // Default for legacy capsules
			}

			c.PatternID = extractField(decBlock, "PatternID")

			// Parse CreatedAt (fall back to Timestamp if not set)
			createdAtStr := extractField(decBlock, "CreatedAt")
			if createdAtStr != "" {
				if t, err := time.Parse(time.RFC3339, createdAtStr); err == nil {
					c.CreatedAt = t
				}
			} else if !c.Timestamp.IsZero() {
				c.CreatedAt = c.Timestamp // Migrate Timestamp to CreatedAt
			}

			// Parse InvalidatedAt
			invalidatedAtStr := extractField(decBlock, "InvalidatedAt")
			if invalidatedAtStr != "" {
				if t, err := time.Parse(time.RFC3339, invalidatedAtStr); err == nil {
					c.InvalidatedAt = &t
				}
			}

			// Dependency graph
			c.EnabledBy = extractField(decBlock, "EnabledBy")
			enables := extractField(decBlock, "Enables")
			if enables != "" {
				c.Enables = splitCSV(enables)
			}
			constrains := extractField(decBlock, "Constrains")
			if constrains != "" {
				c.Constrains = splitCSV(constrains)
			}

			// Parse supersession relationships
			c.SupersededBy = extractField(decBlock, "SupersededBy")
			supersedes := extractField(decBlock, "Supersedes")
			if supersedes != "" {
				c.Supersedes = splitCSV(supersedes)
			}

			// Invalidation metadata
			c.InvalidationReason = extractField(decBlock, "InvalidationReason")
			c.Learned = extractField(decBlock, "Learned")

			if c.ID == "" {
				c.ID = GenerateID(sessionID, phase, question)
			}
			if c.Source == "" {
				c.Source = "agent"
			}
			if c.Origin == "" {
				c.Origin = "agent"
			}

			capsules = append(capsules, c)
		}
	}

	return capsules, nil
}

// Get retrieves a single capsule by ID, searching across all sessions.
func Get(st *store.Store, id string) (*Capsule, error) {
	sessionsDir := st.Path("sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read sessions: %w", err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Try consolidated file
		p := capsulesFilePath(st, e.Name())
		caps, err := loadConsolidatedFile(p)
		if err == nil {
			for _, c := range caps {
				if c.ID == id {
					return &c, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("capsule not found: %s", id)
}

// phaseRank returns a numeric rank for phase ordering (higher = later in pipeline).
func phaseRank(phase string) int {
	ranks := map[string]int{
		"quickfix-seed": 0,
		"investigate":   1,
		"plan":          2,
		"review":        3,
		"execute":       4,
		"verify":        5,
		"simplify":      6,
		"record":        7,
	}
	if r, ok := ranks[phase]; ok {
		return r
	}
	return 0
}

// deduplicateToLatestPhase keeps only the latest-phase capsule per question within each session.
func deduplicateToLatestPhase(capsules []Capsule) []Capsule {
	// Key: sessionID + question
	type key struct {
		session  string
		question string
	}
	best := make(map[key]Capsule)

	for _, c := range capsules {
		k := key{session: c.SessionID, question: c.Question}
		existing, found := best[k]
		if !found || phaseRank(c.Phase) > phaseRank(existing.Phase) {
			best[k] = c
		}
	}

	result := make([]Capsule, 0, len(best))
	for _, c := range best {
		result = append(result, c)
	}
	return result
}

// List returns capsules matching the given filter.
func List(st *store.Store, f Filter) ([]Capsule, error) {
	sessionsDir := st.Path("sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read sessions: %w", err)
	}

	var result []Capsule
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		if f.SessionID != nil && e.Name() != *f.SessionID {
			continue
		}

		// Load from consolidated file
		p := capsulesFilePath(st, e.Name())
		caps, err := loadConsolidatedFile(p)
		if err != nil {
			continue
		}

		for _, c := range caps {
			if matchesFilter(&c, f) {
				result = append(result, c)
			}
		}
	}

	// Deduplicate to latest phase unless ShowEvolution is true
	if !f.ShowEvolution {
		result = deduplicateToLatestPhase(result)
	}

	return result, nil
}

// ListTags returns all unique tags from all capsules, sorted alphabetically.
func ListTags(st *store.Store) ([]string, error) {
	caps, err := List(st, Filter{ShowEvolution: true})
	if err != nil {
		return nil, err
	}

	tagSet := make(map[string]bool)
	for _, c := range caps {
		for _, tag := range c.Tags {
			tagSet[tag] = true
		}
	}

	tags := make([]string, 0, len(tagSet))
	for tag := range tagSet {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags, nil
}

// LinkCommits updates a capsule with commit SHAs.
func LinkCommits(st *store.Store, id string, commits []string) error {
	c, err := Get(st, id)
	if err != nil {
		return err
	}
	c.Commits = commits
	return Store(st, *c)
}

// LinkCommitsForSession bulk-links commits to all capsules in a session.
// Returns the count of capsules updated.
func LinkCommitsForSession(st *store.Store, sessionID string, commits []string) (int, error) {
	p := capsulesFilePath(st, sessionID)
	capsules, err := loadConsolidatedFile(p)
	if err != nil {
		return 0, fmt.Errorf("no capsules found for session %s: %w", sessionID, err)
	}
	if len(capsules) == 0 {
		return 0, nil
	}
	for i := range capsules {
		capsules[i].Commits = commits
	}
	if err := writeConsolidatedFile(p, sessionID, capsules); err != nil {
		return 0, err
	}
	return len(capsules), nil
}

func matchesFilter(c *Capsule, f Filter) bool {
	if f.SessionID != nil && c.SessionID != *f.SessionID {
		return false
	}
	if f.RepoID != nil {
		found := false
		for _, r := range c.RepoIDs {
			if r == *f.RepoID {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if f.Phase != nil && c.Phase != *f.Phase {
		return false
	}
	if f.Status != nil && c.Status != *f.Status {
		return false
	}
	if f.Type != nil && c.Type != *f.Type {
		return false
	}
	// Filter by significance tier
	if f.Significance != nil && c.Significance != *f.Significance {
		return false
	}
	// Exclude invalidated capsules unless explicitly requested
	if !f.IncludeInvalidated && c.Status == StatusInvalidated {
		return false
	}
	if f.Tag != nil {
		found := false
		for _, t := range c.Tags {
			if strings.EqualFold(t, *f.Tag) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if f.FilePath != nil {
		found := false
		for _, t := range c.Tags {
			if strings.Contains(t, *f.FilePath) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Verify marks a capsule as verified.
func Verify(st *store.Store, id string) error {
	c, err := Get(st, id)
	if err != nil {
		return err
	}
	c.Status = StatusVerified
	return Store(st, *c)
}

// VerifySessionCapsules marks all capsules from a specific phase in a session as verified.
func VerifySessionCapsules(st *store.Store, sessionID, phase string) (int, error) {
	p := capsulesFilePath(st, sessionID)
	capsules, err := loadConsolidatedFile(p)
	if err != nil {
		return 0, fmt.Errorf("no capsules found for session %s: %w", sessionID, err)
	}

	count := 0
	for i := range capsules {
		if capsules[i].Phase == phase && capsules[i].Status != StatusVerified {
			capsules[i].Status = StatusVerified
			count++
		}
	}

	if count > 0 {
		if err := writeConsolidatedFile(p, sessionID, capsules); err != nil {
			return 0, err
		}
	}
	return count, nil
}

// Invalidate marks a capsule as invalidated and optionally links to its replacement.
// The learned parameter captures what was learned from the invalidation (distinct from reason).
func Invalidate(st *store.Store, id, reason, learned, supersededBy string) error {
	c, err := Get(st, id)
	if err != nil {
		return err
	}

	c.Status = StatusInvalidated
	if supersededBy != "" {
		c.SupersededBy = supersededBy

		// Also update the superseding capsule to reference this one
		newC, err := Get(st, supersededBy)
		if err == nil {
			// Append to Supersedes if not already present
			found := false
			for _, s := range newC.Supersedes {
				if s == id {
					found = true
					break
				}
			}
			if !found {
				newC.Supersedes = append(newC.Supersedes, id)
				_ = Store(st, *newC) // Best effort
			}
		}
	}

	// Add a challenge record
	c.Challenges = append(c.Challenges, Challenge{
		Timestamp:  time.Now().UTC(),
		SessionID:  c.SessionID, // Could be overridden if we know the invalidating session
		Reason:     reason,
		Learned:    learned,
		Resolution: "invalidated",
	})

	return Store(st, *c)
}

// AddChallenge adds a challenge record to a capsule without changing its status.
func AddChallenge(st *store.Store, id string, challenge Challenge) error {
	c, err := Get(st, id)
	if err != nil {
		return err
	}
	c.Challenges = append(c.Challenges, challenge)
	return Store(st, *c)
}

// ChainResult represents the supersession chain for a capsule.
type ChainResult struct {
	Current      *Capsule
	Supersedes   []Capsule // Capsules this one replaces (older)
	SupersededBy *Capsule  // Capsule that replaced this one (newer)
}

// GetChain returns the supersession chain for a capsule.
func GetChain(st *store.Store, id string) (*ChainResult, error) {
	c, err := Get(st, id)
	if err != nil {
		return nil, err
	}

	result := &ChainResult{Current: c}

	// Follow SupersededBy chain upward (to newer)
	if c.SupersededBy != "" {
		if newer, err := Get(st, c.SupersededBy); err == nil {
			result.SupersededBy = newer
		}
	}

	// Follow Supersedes chain downward (to older)
	for _, oldID := range c.Supersedes {
		if older, err := Get(st, oldID); err == nil {
			result.Supersedes = append(result.Supersedes, *older)
		}
	}

	return result, nil
}

// StatusLabel returns a human-readable label for display.
func (s CapsuleStatus) Label() string {
	switch s {
	case StatusVerified:
		return "[verified]"
	case StatusHypothesis:
		return "[hypothesis]"
	case StatusInvalidated:
		return "[invalidated]"
	default:
		return "[unknown]"
	}
}

// IsValid returns true if this is a trustworthy status (verified or hypothesis).
func (s CapsuleStatus) IsValid() bool {
	return s == StatusVerified || s == StatusHypothesis
}

// EnrichTagsFromManifest adds file: tags from a milestone_ledger file manifest.
// This ensures capsules are discoverable by file path even if not explicitly tagged.
func EnrichTagsFromManifest(st *store.Store, sessionID string) (int, error) {
	// Read milestone_ledger.md
	ledgerPath := st.Path("sessions", sessionID, "milestone_ledger.md")
	content, err := os.ReadFile(ledgerPath)
	if err != nil {
		return 0, nil // No ledger yet, not an error
	}

	// Extract file paths from the manifest section
	filePaths := extractFilePathsFromLedger(string(content))
	if len(filePaths) == 0 {
		return 0, nil
	}

	// Load capsules for this session
	capsulesPath := capsulesFilePath(st, sessionID)
	capsules, err := loadConsolidatedFile(capsulesPath)
	if err != nil {
		return 0, nil
	}

	// Add file tags to each capsule
	updated := 0
	for i := range capsules {
		existingTags := make(map[string]bool)
		for _, t := range capsules[i].Tags {
			existingTags[t] = true
		}

		added := false
		for _, fp := range filePaths {
			fileTag := NormalizeTag(fp)
			if !existingTags[fileTag] {
				capsules[i].Tags = append(capsules[i].Tags, fileTag)
				existingTags[fileTag] = true
				added = true
			}
		}
		if added {
			updated++
		}
	}

	if updated > 0 {
		if err := writeConsolidatedFile(capsulesPath, sessionID, capsules); err != nil {
			return 0, err
		}
	}

	return updated, nil
}

// extractFilePathsFromLedger parses file paths from the File Manifest section of a milestone_ledger.
func extractFilePathsFromLedger(content string) []string {
	var paths []string
	lines := strings.Split(content, "\n")
	inManifestSection := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check for file manifest section header
		if strings.HasPrefix(trimmed, "## ") && strings.Contains(strings.ToLower(trimmed), "file") && strings.Contains(strings.ToLower(trimmed), "manifest") {
			inManifestSection = true
			continue
		}

		// Exit manifest section on next ## header
		if strings.HasPrefix(trimmed, "## ") && inManifestSection {
			break
		}

		if !inManifestSection {
			continue
		}

		// Parse file entries (- path/to/file or - `path/to/file`)
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			entry := strings.TrimPrefix(trimmed, "- ")
			entry = strings.TrimPrefix(entry, "* ")
			entry = strings.Trim(entry, "`")

			// Extract just the path (may have annotations like ": description")
			if idx := strings.Index(entry, ":"); idx > 0 {
				entry = strings.TrimSpace(entry[:idx])
			}
			if idx := strings.Index(entry, " "); idx > 0 {
				entry = strings.TrimSpace(entry[:idx])
			}

			// Validate it looks like a file path
			if strings.Contains(entry, "/") || strings.Contains(entry, ".") {
				entry = strings.Trim(entry, "`")
				if entry != "" {
					paths = append(paths, entry)
				}
			}
		}
	}

	return paths
}
