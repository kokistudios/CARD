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

	"github.com/kokistudios/card/internal/store"
)

type CapsuleStatus string

const (
	StatusInvalidated CapsuleStatus = "invalidated" // Superseded or proven wrong
)

type CapsuleType string

const (
	TypeDecision CapsuleType = "decision" // Question → Choice → Rationale with alternatives
	TypeFinding  CapsuleType = "finding"  // Observation or conclusion (no alternatives)
)


type Confirmation string

const (
	ConfirmationExplicit Confirmation = "explicit" // Human explicitly confirmed via card_decision_confirm
	ConfirmationImplicit Confirmation = "implicit" // Stored immediately without human confirmation
)

type Challenge struct {
	Timestamp  time.Time `yaml:"timestamp"`
	SessionID  string    `yaml:"session_id"`
	Reason     string    `yaml:"reason"`
	Learned    string    `yaml:"learned,omitempty"`  // What was learned from invalidation (distinct from reason)
	Resolution string    `yaml:"resolution"`         // "verified", "invalidated", "superseded", "pending"
}

type Capsule struct {
	ID           string   `yaml:"id"`
	SessionID    string   `yaml:"session"`
	RepoIDs      []string `yaml:"repos,omitempty"`
	Phase        string   `yaml:"phase"`
	Question     string   `yaml:"question"`
	Choice       string   `yaml:"choice"`
	Alternatives []string `yaml:"alternatives,omitempty"`
	Rationale    string   `yaml:"rationale"`
	Tags         []string `yaml:"tags,omitempty"`
	Commits      []string `yaml:"commits,omitempty"`

	Status CapsuleStatus `yaml:"status,omitempty"`
	Type   CapsuleType   `yaml:"type,omitempty"`

	PatternID     string       `yaml:"pattern_id,omitempty"`
	Origin        string       `yaml:"origin,omitempty"`         // "human" or "agent"
	Confirmation  Confirmation `yaml:"confirmation,omitempty"`   // explicit, implicit
	CreatedAt     time.Time    `yaml:"created_at,omitempty"`     // When capsule was created
	InvalidatedAt *time.Time   `yaml:"invalidated_at,omitempty"` // When capsule was invalidated

	Enables    []string `yaml:"enables,omitempty"`
	EnabledBy  string   `yaml:"enabled_by,omitempty"`
	Constrains []string `yaml:"constrains,omitempty"`

	SupersededBy string   `yaml:"superseded_by,omitempty"`
	Supersedes   []string `yaml:"supersedes,omitempty"`

	InvalidationReason string `yaml:"invalidation_reason,omitempty"`
	Learned            string `yaml:"learned,omitempty"`

	Challenges []Challenge `yaml:"challenges,omitempty"`
}

type Filter struct {
	SessionID          *string
	RepoID             *string       // matches against any entry in RepoIDs
	Phase              *string
	FilePath           *string       // match against Tags
	Tag                *string       // match against Tags
	Status             *CapsuleStatus // filter by status (only 'invalidated' is meaningful; empty = active)
	Type               *CapsuleType   // filter by type (decision, finding)
	IncludeInvalidated bool           // if true, include invalidated capsules (default: exclude)
	ShowEvolution      bool          // if false (default), deduplicate to latest phase per question
}

func GenerateID(sessionID, phase, question string) string {
	h := sha256.Sum256([]byte(question))
	shortHash := fmt.Sprintf("%x", h[:4])
	return fmt.Sprintf("%s-%s-%s", sessionID, phase, shortHash)
}

func extractField(block, label string) string {
	re := regexp.MustCompile(`(?mi)[-*]\s*\*\*` + regexp.QuoteMeta(label) + `:\*\*\s*(.+)$`)
	m := re.FindStringSubmatch(block)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, "`")
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func capsulesFilePath(st *store.Store, sessionID string) string {
	return st.Path("sessions", sessionID, "capsules.md")
}

func Store(st *store.Store, c Capsule) error {
	p := capsulesFilePath(st, c.SessionID)

	existing, _ := loadConsolidatedFile(p)

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

func writeConsolidatedFile(path, sessionID string, capsules []Capsule) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}

	phaseOrder := []string{"ask", "investigate", "investigating", "plan", "planning", "review", "reviewing", "execute", "executing", "verify", "verifying", "simplify", "simplifying", "record", "recording", "conclude"}
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
			if c.Origin != "" {
				fmt.Fprintf(&buf, "- **Origin:** %s\n", c.Origin)
			}
			if c.Status != "" {
				fmt.Fprintf(&buf, "- **Status:** %s\n", c.Status)
			}
			if c.Type != "" {
				fmt.Fprintf(&buf, "- **Type:** %s\n", c.Type)
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
			if c.EnabledBy != "" {
				fmt.Fprintf(&buf, "- **EnabledBy:** %s\n", c.EnabledBy)
			}
			if len(c.Enables) > 0 {
				fmt.Fprintf(&buf, "- **Enables:** %s\n", strings.Join(c.Enables, ", "))
			}
			if len(c.Constrains) > 0 {
				fmt.Fprintf(&buf, "- **Constrains:** %s\n", strings.Join(c.Constrains, ", "))
			}
			if c.SupersededBy != "" {
				fmt.Fprintf(&buf, "- **SupersededBy:** %s\n", c.SupersededBy)
			}
			if len(c.Supersedes) > 0 {
				fmt.Fprintf(&buf, "- **Supersedes:** %s\n", strings.Join(c.Supersedes, ", "))
			}
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

func loadConsolidatedFile(path string) ([]Capsule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseConsolidatedCapsules(string(data))
}

func parseConsolidatedCapsules(content string) ([]Capsule, error) {
	var capsules []Capsule

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

	phaseRe := regexp.MustCompile(`(?m)^## (\w+)\s*$`)
	phaseMatches := phaseRe.FindAllStringSubmatchIndex(content, -1)

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
				Origin:    extractField(decBlock, "Origin"),
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

			statusStr := extractField(decBlock, "Status")
			if statusStr != "" {
				c.Status = CapsuleStatus(strings.ToLower(statusStr))
			}

			typeStr := extractField(decBlock, "Type")
			if typeStr != "" {
				c.Type = CapsuleType(strings.ToLower(typeStr))
			} else if len(c.Alternatives) > 0 {
				c.Type = TypeDecision
			} else {
				c.Type = TypeFinding
			}

			confStr := extractField(decBlock, "Confirmation")
			if confStr != "" {
				c.Confirmation = Confirmation(strings.ToLower(confStr))
			}

			c.PatternID = extractField(decBlock, "PatternID")

			createdAtStr := extractField(decBlock, "CreatedAt")
			if createdAtStr != "" {
				if t, err := time.Parse(time.RFC3339, createdAtStr); err == nil {
					c.CreatedAt = t
				}
			}

			invalidatedAtStr := extractField(decBlock, "InvalidatedAt")
			if invalidatedAtStr != "" {
				if t, err := time.Parse(time.RFC3339, invalidatedAtStr); err == nil {
					c.InvalidatedAt = &t
				}
			}

			c.EnabledBy = extractField(decBlock, "EnabledBy")
			enables := extractField(decBlock, "Enables")
			if enables != "" {
				c.Enables = splitCSV(enables)
			}
			constrains := extractField(decBlock, "Constrains")
			if constrains != "" {
				c.Constrains = splitCSV(constrains)
			}

			c.SupersededBy = extractField(decBlock, "SupersededBy")
			supersedes := extractField(decBlock, "Supersedes")
			if supersedes != "" {
				c.Supersedes = splitCSV(supersedes)
			}

			c.InvalidationReason = extractField(decBlock, "InvalidationReason")
			c.Learned = extractField(decBlock, "Learned")

			if c.ID == "" {
				c.ID = GenerateID(sessionID, phase, question)
			}

			capsules = append(capsules, c)
		}
	}

	return capsules, nil
}

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

func phaseRank(phase string) int {
	ranks := map[string]int{
		"ask":         0, // Decisions recorded during card ask
		"investigate": 1,
		"plan":        2,
		"review":      3,
		"execute":     4,
		"verify":      5,
		"simplify":    6,
		"record":      7,
	}
	if r, ok := ranks[phase]; ok {
		return r
	}
	return 0
}

func deduplicateToLatestPhase(capsules []Capsule) []Capsule {
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

	if !f.ShowEvolution {
		result = deduplicateToLatestPhase(result)
	}

	return result, nil
}

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

func LinkCommits(st *store.Store, id string, commits []string) error {
	c, err := Get(st, id)
	if err != nil {
		return err
	}
	c.Commits = commits
	return Store(st, *c)
}

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


func Invalidate(st *store.Store, id, reason, learned, supersededBy string) error {
	c, err := Get(st, id)
	if err != nil {
		return err
	}

	c.Status = StatusInvalidated
	c.InvalidationReason = reason
	c.Learned = learned

	if supersededBy != "" {
		c.SupersededBy = supersededBy

		newC, err := Get(st, supersededBy)
		if err == nil {
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

	c.Challenges = append(c.Challenges, Challenge{
		Timestamp:  time.Now().UTC(),
		SessionID:  c.SessionID, // Could be overridden if we know the invalidating session
		Reason:     reason,
		Learned:    learned,
		Resolution: "invalidated",
	})

	return Store(st, *c)
}

func AddChallenge(st *store.Store, id string, challenge Challenge) error {
	c, err := Get(st, id)
	if err != nil {
		return err
	}
	c.Challenges = append(c.Challenges, challenge)
	return Store(st, *c)
}

type ChainResult struct {
	Current      *Capsule
	Supersedes   []Capsule // Capsules this one replaces (older)
	SupersededBy *Capsule  // Capsule that replaced this one (newer)
}

func GetChain(st *store.Store, id string) (*ChainResult, error) {
	c, err := Get(st, id)
	if err != nil {
		return nil, err
	}

	result := &ChainResult{Current: c}

	if c.SupersededBy != "" {
		if newer, err := Get(st, c.SupersededBy); err == nil {
			result.SupersededBy = newer
		}
	}

	for _, oldID := range c.Supersedes {
		if older, err := Get(st, oldID); err == nil {
			result.Supersedes = append(result.Supersedes, *older)
		}
	}

	return result, nil
}

func (s CapsuleStatus) Label() string {
	if s == StatusInvalidated {
		return "[invalidated]"
	}
	return "" // Active capsules have no label
}

func (s CapsuleStatus) IsActive() bool {
	return s != StatusInvalidated
}

func EnrichTagsFromManifest(st *store.Store, sessionID string) (int, error) {
	ledgerPath := st.Path("sessions", sessionID, "milestone_ledger.md")
	content, err := os.ReadFile(ledgerPath)
	if err != nil {
		return 0, nil // No ledger yet, not an error
	}

	filePaths := extractFilePathsFromLedger(string(content))
	if len(filePaths) == 0 {
		return 0, nil
	}

	capsulesPath := capsulesFilePath(st, sessionID)
	capsules, err := loadConsolidatedFile(capsulesPath)
	if err != nil {
		return 0, nil
	}

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

func extractFilePathsFromLedger(content string) []string {
	var paths []string
	lines := strings.Split(content, "\n")
	inManifestSection := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "## ") && strings.Contains(strings.ToLower(trimmed), "file") && strings.Contains(strings.ToLower(trimmed), "manifest") {
			inManifestSection = true
			continue
		}

		if strings.HasPrefix(trimmed, "## ") && inManifestSection {
			break
		}

		if !inManifestSection {
			continue
		}

		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			entry := strings.TrimPrefix(trimmed, "- ")
			entry = strings.TrimPrefix(entry, "* ")
			entry = strings.Trim(entry, "`")

			if idx := strings.Index(entry, ":"); idx > 0 {
				entry = strings.TrimSpace(entry[:idx])
			}
			if idx := strings.Index(entry, " "); idx > 0 {
				entry = strings.TrimSpace(entry[:idx])
			}

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
