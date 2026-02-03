package recall

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/kokistudios/card/internal/capsule"
	"github.com/kokistudios/card/internal/session"
	"github.com/kokistudios/card/internal/store"
)

// RecallQuery defines what to recall.
type RecallQuery struct {
	Files              []string
	RepoID             string
	RepoPath           string // local path, needed for git correlation
	Tags               []string
	Query              string // full-text search across question/choice/rationale
	MaxCapsules        int    // 0 = default (20)
	IncludeEvolution   bool   // if true, show all phases of each decision
	IncludeInvalidated bool   // if true, include invalidated capsules
	RecentOnly         bool   // if true, return most recent decisions (no filters)
}

// SessionSummary is a lightweight view of a session for recall results.
type SessionSummary struct {
	ID          string
	Description string
	Status      string
	CreatedAt   time.Time
}

// MatchTier indicates how a capsule was matched, from most to least relevant.
type MatchTier int

const (
	MatchExactFile MatchTier = iota // highest relevance
	MatchPartialFile
	MatchGitCorrelation
	MatchTag
	MatchText // full-text search on question/choice/rationale
	MatchRepo
)

// TierLabel returns a human-readable label for the match tier.
func (m MatchTier) TierLabel() string {
	switch m {
	case MatchExactFile:
		return "exact-file"
	case MatchPartialFile:
		return "partial-file"
	case MatchGitCorrelation:
		return "git"
	case MatchTag:
		return "tag"
	case MatchText:
		return "text"
	case MatchRepo:
		return "repo"
	default:
		return "unknown"
	}
}

// IsStrong returns true for high-relevance tiers (file and git matches).
func (m MatchTier) IsStrong() bool {
	return m <= MatchGitCorrelation
}

// ScoredCapsule pairs a capsule with its match relevance tier.
type ScoredCapsule struct {
	capsule.Capsule
	Tier MatchTier
}

// RecallResult holds the merged output of recall strategies.
type RecallResult struct {
	Query    RecallQuery
	Capsules []ScoredCapsule
	Sessions []SessionSummary
}

// intermediateResult holds raw capsules before scoring.
type intermediateResult struct {
	Capsules []capsule.Capsule
	Sessions []SessionSummary
}

// ByFiles finds capsules whose tags match the given file paths.
func ByFiles(st *store.Store, repoID string, files []string, includeEvolution, includeInvalidated bool) (*intermediateResult, error) {
	allCapsules, err := capsule.List(st, capsule.Filter{ShowEvolution: includeEvolution, IncludeInvalidated: includeInvalidated})
	if err != nil {
		return nil, err
	}

	result := &intermediateResult{}
	seen := map[string]bool{}

	for _, c := range allCapsules {
		if repoID != "" && !capsuleMatchesRepo(c, repoID) {
			continue
		}
		for _, file := range files {
			if matchesFile(c.Tags, file) && !seen[c.ID] {
				result.Capsules = append(result.Capsules, c)
				seen[c.ID] = true
			}
		}
	}

	result.Sessions = sessionsFromCapsules(st, result.Capsules)
	return result, nil
}

// ByRepo returns all capsules and sessions for a given repo.
func ByRepo(st *store.Store, repoID string, includeEvolution, includeInvalidated bool) (*intermediateResult, error) {
	sessions, err := session.List(st)
	if err != nil {
		return nil, err
	}

	result := &intermediateResult{}
	sessionIDs := map[string]bool{}

	for _, sess := range sessions {
		for _, r := range sess.Repos {
			if r == repoID {
				sessionIDs[sess.ID] = true
				result.Sessions = append(result.Sessions, SessionSummary{
					ID:          sess.ID,
					Description: sess.Description,
					Status:      string(sess.Status),
					CreatedAt:   sess.CreatedAt,
				})
				break
			}
		}
	}

	// Gather capsules from matching sessions
	for sessID := range sessionIDs {
		caps, err := capsule.List(st, capsule.Filter{SessionID: &sessID, RepoID: &repoID, ShowEvolution: includeEvolution, IncludeInvalidated: includeInvalidated})
		if err != nil {
			continue
		}
		result.Capsules = append(result.Capsules, caps...)
	}

	return result, nil
}

// ByTags finds capsules with matching tags (case-insensitive, partial match).
// Also expands tags with synonyms for fuzzy matching (e.g., "auth" matches "authentication").
func ByTags(st *store.Store, tags []string, includeEvolution, includeInvalidated bool) (*intermediateResult, error) {
	allCapsules, err := capsule.List(st, capsule.Filter{ShowEvolution: includeEvolution, IncludeInvalidated: includeInvalidated})
	if err != nil {
		return nil, err
	}

	result := &intermediateResult{}
	seen := map[string]bool{}

	for _, c := range allCapsules {
		for _, queryTag := range tags {
			// Use synonym-aware matching
			if capsule.MatchesTagQueryWithSynonyms(c.Tags, queryTag) && !seen[c.ID] {
				result.Capsules = append(result.Capsules, c)
				seen[c.ID] = true
			}
		}
	}

	result.Sessions = sessionsFromCapsules(st, result.Capsules)
	return result, nil
}

// ByText finds capsules with matching text in question, choice, or rationale.
func ByText(st *store.Store, query string, includeEvolution, includeInvalidated bool) (*intermediateResult, error) {
	allCapsules, err := capsule.List(st, capsule.Filter{ShowEvolution: includeEvolution, IncludeInvalidated: includeInvalidated})
	if err != nil {
		return nil, err
	}

	result := &intermediateResult{}
	q := strings.ToLower(query)

	for _, c := range allCapsules {
		if matchesText(c, q) {
			result.Capsules = append(result.Capsules, c)
		}
	}

	result.Sessions = sessionsFromCapsules(st, result.Capsules)
	return result, nil
}

// ByRecent returns the most recent capsules, sorted by timestamp (newest first).
func ByRecent(st *store.Store, limit int, includeEvolution, includeInvalidated bool) (*RecallResult, error) {
	allCapsules, err := capsule.List(st, capsule.Filter{ShowEvolution: includeEvolution, IncludeInvalidated: includeInvalidated})
	if err != nil {
		return nil, err
	}

	// Sort by timestamp descending (newest first)
	sort.Slice(allCapsules, func(i, j int) bool {
		return allCapsules[i].Timestamp.After(allCapsules[j].Timestamp)
	})

	// Cap by limit
	if limit <= 0 {
		limit = 15
	}
	if len(allCapsules) > limit {
		allCapsules = allCapsules[:limit]
	}

	// Convert to scored capsules with MatchRepo tier (lowest relevance, but they're recent)
	var scored []ScoredCapsule
	for _, c := range allCapsules {
		scored = append(scored, ScoredCapsule{c, MatchRepo})
	}

	result := &RecallResult{
		Capsules: scored,
		Sessions: sessionsFromCapsules(st, allCapsules),
	}

	return result, nil
}

// matchesText checks if any of the capsule's text fields contain the query.
func matchesText(c capsule.Capsule, query string) bool {
	return strings.Contains(strings.ToLower(c.Question), query) ||
		strings.Contains(strings.ToLower(c.Choice), query) ||
		strings.Contains(strings.ToLower(c.Rationale), query)
}

// ByGitHistory finds capsules linked to commits that touched the given files.
func ByGitHistory(st *store.Store, repoID, repoPath string, files []string, includeEvolution, includeInvalidated bool) (*intermediateResult, error) {
	if repoPath == "" || len(files) == 0 {
		return &intermediateResult{}, nil
	}

	// Get commit SHAs from git log for specified files
	commitSHAs := gitLogCommits(repoPath, files)
	if len(commitSHAs) == 0 {
		return &intermediateResult{}, nil
	}

	// Get all capsules and match against commit SHAs
	allCapsules, err := capsule.List(st, capsule.Filter{ShowEvolution: includeEvolution, IncludeInvalidated: includeInvalidated})
	if err != nil {
		return nil, err
	}

	result := &intermediateResult{}
	commitSet := map[string]bool{}
	for _, sha := range commitSHAs {
		commitSet[sha] = true
	}

	seen := map[string]bool{}
	for _, c := range allCapsules {
		if repoID != "" && !capsuleMatchesRepo(c, repoID) {
			continue
		}
		for _, commit := range c.Commits {
			if commitSet[commit] && !seen[c.ID] {
				result.Capsules = append(result.Capsules, c)
				seen[c.ID] = true
			}
		}
	}

	result.Sessions = sessionsFromCapsules(st, result.Capsules)
	return result, nil
}

// Query runs all applicable recall strategies and merges results.
func Query(st *store.Store, q RecallQuery) (*RecallResult, error) {
	// Smart default: if no params specified, return most recent decisions
	if len(q.Files) == 0 && len(q.Tags) == 0 && q.Query == "" && q.RepoID == "" {
		limit := q.MaxCapsules
		if limit <= 0 {
			limit = 15
		}
		return ByRecent(st, limit, q.IncludeEvolution, q.IncludeInvalidated)
	}

	scored := map[string]ScoredCapsule{}
	sessionSet := map[string]SessionSummary{}

	// File-based recall (cross-repo if no repo specified)
	if len(q.Files) > 0 {
		r, err := ByFiles(st, q.RepoID, q.Files, q.IncludeEvolution, q.IncludeInvalidated)
		if err == nil {
			for _, c := range r.Capsules {
				if existing, ok := scored[c.ID]; !ok || MatchExactFile < existing.Tier {
					scored[c.ID] = ScoredCapsule{c, MatchExactFile}
				}
			}
			mergeSessionSummaries(sessionSet, r.Sessions)
		}
	}

	// Git correlation
	if len(q.Files) > 0 && q.RepoPath != "" {
		r, err := ByGitHistory(st, q.RepoID, q.RepoPath, q.Files, q.IncludeEvolution, q.IncludeInvalidated)
		if err == nil {
			for _, c := range r.Capsules {
				if _, ok := scored[c.ID]; !ok {
					scored[c.ID] = ScoredCapsule{c, MatchGitCorrelation}
				}
			}
			mergeSessionSummaries(sessionSet, r.Sessions)
		}
	}

	// Tag-based recall
	if len(q.Tags) > 0 {
		r, err := ByTags(st, q.Tags, q.IncludeEvolution, q.IncludeInvalidated)
		if err == nil {
			for _, c := range r.Capsules {
				if _, ok := scored[c.ID]; !ok {
					scored[c.ID] = ScoredCapsule{c, MatchTag}
				}
			}
			mergeSessionSummaries(sessionSet, r.Sessions)
		}
	}

	// Full-text search on question/choice/rationale
	if q.Query != "" {
		r, err := ByText(st, q.Query, q.IncludeEvolution, q.IncludeInvalidated)
		if err == nil {
			for _, c := range r.Capsules {
				if _, ok := scored[c.ID]; !ok {
					scored[c.ID] = ScoredCapsule{c, MatchText}
				}
			}
			mergeSessionSummaries(sessionSet, r.Sessions)
		}
	}

	// Repo-based recall (only if no files/tags/query specified, to avoid noise)
	if q.RepoID != "" && len(q.Files) == 0 && len(q.Tags) == 0 && q.Query == "" {
		r, err := ByRepo(st, q.RepoID, q.IncludeEvolution, q.IncludeInvalidated)
		if err == nil {
			for _, c := range r.Capsules {
				if _, ok := scored[c.ID]; !ok {
					scored[c.ID] = ScoredCapsule{c, MatchRepo}
				}
			}
			mergeSessionSummaries(sessionSet, r.Sessions)
		}
	}

	// Sort by relevance tier
	var sortedCapsules []ScoredCapsule
	for _, sc := range scored {
		sortedCapsules = append(sortedCapsules, sc)
	}
	sort.Slice(sortedCapsules, func(i, j int) bool {
		if sortedCapsules[i].Tier != sortedCapsules[j].Tier {
			return sortedCapsules[i].Tier < sortedCapsules[j].Tier // lower = more relevant
		}
		return sortedCapsules[i].Timestamp.After(sortedCapsules[j].Timestamp) // newer first
	})

	result := &RecallResult{Query: q}
	// Cap by count
	maxCapsules := q.MaxCapsules
	if maxCapsules <= 0 {
		maxCapsules = 20
	}
	if len(sortedCapsules) > maxCapsules {
		sortedCapsules = sortedCapsules[:maxCapsules]
	}
	result.Capsules = sortedCapsules
	for _, s := range sessionSet {
		result.Sessions = append(result.Sessions, s)
	}

	return result, nil
}

// FormatTerminal renders recall results for human reading.
func FormatTerminal(r *RecallResult, full bool) string {
	if len(r.Capsules) == 0 && len(r.Sessions) == 0 {
		return "No prior CARD context found."
	}

	var b strings.Builder

	if len(r.Sessions) > 0 {
		b.WriteString(fmt.Sprintf("Found %d prior session(s):\n", len(r.Sessions)))
		for _, s := range r.Sessions {
			b.WriteString(fmt.Sprintf("  - %s: %s [%s] (%s)\n", s.ID, s.Description, s.Status, s.CreatedAt.Format("2006-01-02")))
			b.WriteString(fmt.Sprintf("    📖 ~/.card/sessions/%s/milestone_ledger.md\n", s.ID))
		}
		b.WriteString("\n")
	}

	if len(r.Capsules) > 0 {
		b.WriteString(fmt.Sprintf("Found %d prior decision(s):\n", len(r.Capsules)))
		for _, sc := range r.Capsules {
			statusLabel := sc.Status.Label()
			tierLabel := sc.Tier.TierLabel()
			if full {
				b.WriteString(fmt.Sprintf("\n  %s [%s] %s\n", statusLabel, tierLabel, sc.Question))
				b.WriteString(fmt.Sprintf("    Choice:    %s\n", sc.Choice))
				if len(sc.Alternatives) > 0 {
					b.WriteString(fmt.Sprintf("    Alts:      %s\n", strings.Join(sc.Alternatives, ", ")))
				}
				b.WriteString(fmt.Sprintf("    Rationale: %s\n", sc.Rationale))
				if len(sc.Tags) > 0 {
					b.WriteString(fmt.Sprintf("    Tags:      %s\n", strings.Join(sc.Tags, ", ")))
				}
				if sc.SupersededBy != "" {
					b.WriteString(fmt.Sprintf("    → Superseded by: %s\n", sc.SupersededBy))
				}
				b.WriteString(fmt.Sprintf("    📖 ~/.card/sessions/%s/milestone_ledger.md\n", sc.SessionID))
			} else {
				b.WriteString(fmt.Sprintf("  - %s [%s] %s → %s\n", statusLabel, tierLabel, sc.Question, sc.Choice))
			}
		}
	}

	return b.String()
}

// estimateTokens gives a rough token count from character length (1 token ≈ 4 chars).
func estimateTokens(s string) int {
	return (len(s) + 3) / 4
}

// FormatContext renders recall results as a markdown block for Claude Code prompt injection.
// tokenBudget limits the output size; 0 means unlimited.
func FormatContext(r *RecallResult, tokenBudget int) string {
	if len(r.Capsules) == 0 {
		return ""
	}

	header := "## Prior CARD Context\n\nThe following decisions were made in prior sessions touching related files:\n\n"
	var b strings.Builder
	b.WriteString(header)
	usedTokens := estimateTokens(header)

	for _, sc := range r.Capsules {
		var entry string
		statusLabel := sc.Status.Label()

		if sc.Tier.IsStrong() {
			// Full detail for strong matches
			var eb strings.Builder
			eb.WriteString(fmt.Sprintf("### %s Decision: %s\n", statusLabel, sc.Question))
			eb.WriteString(fmt.Sprintf("- **Choice:** %s\n", sc.Choice))
			eb.WriteString(fmt.Sprintf("- **Rationale:** %s\n", sc.Rationale))
			if len(sc.Tags) > 0 {
				eb.WriteString(fmt.Sprintf("- **Tags:** %s\n", strings.Join(sc.Tags, ", ")))
			}
			if sc.SupersededBy != "" {
				eb.WriteString(fmt.Sprintf("- **Superseded by:** %s\n", sc.SupersededBy))
			}
			eb.WriteString(fmt.Sprintf("- **Phase:** %s, **Session:** %s\n", sc.Phase, sc.SessionID))
			eb.WriteString(fmt.Sprintf("- 📖 Step into memory: `~/.card/sessions/%s/milestone_ledger.md`\n\n", sc.SessionID))
			entry = eb.String()
		} else {
			// One-liner for weak matches
			entry = fmt.Sprintf("- %s [%s] %s → %s\n", statusLabel, sc.Phase, sc.Question, sc.Choice)
		}

		entryTokens := estimateTokens(entry)
		if tokenBudget > 0 && usedTokens+entryTokens > tokenBudget {
			break
		}
		b.WriteString(entry)
		usedTokens += entryTokens
	}

	return b.String()
}

// matchesFile checks if any tag matches the given file path.
// Supports exact match, prefix match (directory), and substring match.
func matchesFile(tags []string, file string) bool {
	file = strings.TrimSuffix(file, "/")
	for _, tag := range tags {
		tag = strings.TrimSuffix(tag, "/")
		if strings.EqualFold(tag, file) {
			return true
		}
		// Directory prefix match: file "src/auth/login.ts" matches tag "src/auth"
		if strings.HasPrefix(strings.ToLower(file), strings.ToLower(tag)+"/") {
			return true
		}
		// Tag prefix match: tag "src/auth/login.ts" matches query "src/auth"
		if strings.HasPrefix(strings.ToLower(tag), strings.ToLower(file)+"/") {
			return true
		}
	}
	return false
}

// matchesTag checks if any capsule tag matches the query tag (case-insensitive, partial).
func matchesTag(tags []string, queryTag string) bool {
	q := strings.ToLower(queryTag)
	for _, t := range tags {
		if strings.Contains(strings.ToLower(t), q) {
			return true
		}
	}
	return false
}

// gitLogCommits returns commit SHAs from git log for the given files.
func gitLogCommits(repoPath string, files []string) []string {
	args := []string{"-C", repoPath, "log", "--format=%H", "-50", "--"}
	args = append(args, files...)
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var shas []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			shas = append(shas, line)
		}
	}
	return shas
}

// sessionsFromCapsules extracts unique session summaries from a list of capsules.
func sessionsFromCapsules(st *store.Store, caps []capsule.Capsule) []SessionSummary {
	seen := map[string]bool{}
	var summaries []SessionSummary
	for _, c := range caps {
		if seen[c.SessionID] {
			continue
		}
		seen[c.SessionID] = true
		sess, err := session.Get(st, c.SessionID)
		if err != nil {
			continue
		}
		summaries = append(summaries, SessionSummary{
			ID:          sess.ID,
			Description: sess.Description,
			Status:      string(sess.Status),
			CreatedAt:   sess.CreatedAt,
		})
	}
	return summaries
}

func mergeSessionSummaries(set map[string]SessionSummary, summaries []SessionSummary) {
	for _, s := range summaries {
		set[s.ID] = s
	}
}

// capsuleMatchesRepo checks if a capsule is associated with the given repo ID.
func capsuleMatchesRepo(c capsule.Capsule, repoID string) bool {
	for _, r := range c.RepoIDs {
		if r == repoID {
			return true
		}
	}
	return false
}
