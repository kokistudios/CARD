package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"

	"github.com/kokistudios/card/internal/artifact"
	"github.com/kokistudios/card/internal/capsule"
	"github.com/kokistudios/card/internal/change"
	"github.com/kokistudios/card/internal/recall"
	"github.com/kokistudios/card/internal/repo"
	"github.com/kokistudios/card/internal/session"
	"github.com/kokistudios/card/internal/store"
)

// Server wraps the MCP server with CARD's store.
type Server struct {
	store     *store.Store
	server    *mcp.Server
	proposals *ProposalStore // Stores pending decision proposals awaiting confirmation
}

// NewServer creates a new CARD MCP server.
func NewServer(st *store.Store, version string) *Server {
	s := &Server{
		store:     st,
		proposals: NewProposalStore(),
	}

	impl := &mcp.Implementation{
		Name:    "card",
		Version: version,
	}

	s.server = mcp.NewServer(impl, nil)
	s.registerTools()

	// Start proposal cleanup routine (clean expired proposals every 5 minutes)
	s.proposals.StartCleanupRoutine(5 * time.Minute)

	return s
}

// Run starts the MCP server on stdio.
func (s *Server) Run(ctx context.Context) error {
	return s.server.Run(ctx, &mcp.StdioTransport{})
}

// registerTools adds all CARD tools to the MCP server.
func (s *Server) registerTools() {
	// card_quickfix_start - promote ask discovery to recorded session
	mcp.AddTool(s.server, &mcp.Tool{
		Name: "card_quickfix_start",
		Description: "Create a quickfix session from a card ask discovery. " +
			"WHEN TO OFFER: Proactively suggest this when you discover something during card ask that needs " +
			"immediate action — a bug, security vulnerability, or fix that emerged from exploring prior decisions. " +
			"Don't wait for the user to ask; if you find something fixable, offer to create a quickfix session. " +
			"BEFORE CALLING: You MUST (1) explain that a quickfix session will record this fix with decision capture, " +
			"skipping investigation/planning since you've already done that discovery together, " +
			"(2) ask for explicit permission, (3) only then call with user_confirmed=true. " +
			"When the tool succeeds, display the next_steps field exactly as returned.",
	}, s.handleQuickfixStart)

	// card_record - record a decision or finding mid-phase
	mcp.AddTool(s.server, &mcp.Tool{
		Name: "card_record",
		Description: "Record a decision or finding immediately without waiting for artifact extraction. " +
			"USE THIS when you make a significant decision during any phase - the decision survives even if " +
			"the session crashes or re-executes. Recorded capsules start as 'hypothesis' status until verified. " +
			"If no session_id provided, finds the most recent active session.",
	}, s.handleRecord)

	// card_agent_guidance - get proactive usage instructions
	mcp.AddTool(s.server, &mcp.Tool{
		Name: "card_agent_guidance",
		Description: "Get guidance on proactive CARD usage. Call this once at the start of a session to " +
			"understand how to use CARD tools effectively. Returns best practices for surfacing context " +
			"and capturing decisions.",
	}, s.handleAgentGuidance)

	// card_write_artifact - deterministically write phase artifacts
	mcp.AddTool(s.server, &mcp.Tool{
		Name: "card_write_artifact",
		Description: "Write a CARD phase artifact to the correct location. USE THIS instead of the Write tool " +
			"when producing phase artifacts (investigation_summary, implementation_guide, execution_log, etc.). " +
			"The tool handles path resolution — you provide the content, CARD handles where it goes. " +
			"Content MUST include valid YAML frontmatter with session, phase, timestamp, and status fields.",
	}, s.handleWriteArtifact)

	// card_decision - unified decision recording with significance tiers
	mcp.AddTool(s.server, &mcp.Tool{
		Name: "card_decision",
		Description: "Record a decision with significance tier and optional human confirmation. " +
			"SIGNIFICANCE TIERS:\n" +
			"- 'architectural': Trade-off decisions, multiple viable alternatives, shapes future work. " +
			"Use require_confirmation=true for human review.\n" +
			"- 'implementation': Pattern-following, obvious choices, easily reversible. " +
			"Use require_confirmation=false for immediate storage.\n" +
			"- 'context': Facts discovered, constraints identified, not really decisions. " +
			"Use require_confirmation=false.\n\n" +
			"FLOW:\n" +
			"- require_confirmation=false: Stores immediately, returns capsule_id\n" +
			"- require_confirmation=true: Returns proposal_id with similar/contradicting decisions. " +
			"Present to human, then call card_decision_confirm to finalize.\n\n" +
			"This replaces manual '### Decision:' blocks in artifacts. " +
			"Decisions captured here are immediately queryable via card_query.",
	}, s.handleDecision)

	// card_decision_confirm - confirm a proposed architectural decision
	mcp.AddTool(s.server, &mcp.Tool{
		Name: "card_decision_confirm",
		Description: "Confirm a proposed decision after human approval. " +
			"Only needed when card_decision was called with require_confirmation=true. " +
			"ACTIONS:\n" +
			"- 'create': Store new capsule with explicit confirmation\n" +
			"- 'supersede': Store new capsule and invalidate the specified prior decisions\n" +
			"- 'skip': Discard proposal, nothing stored\n" +
			"- 'merge_into:<id>': Update existing capsule with enriched rationale/tags\n\n" +
			"When action='supersede', provide invalidate_ids with the IDs to invalidate, " +
			"invalidation_reason, and optionally learned (insight gained).",
	}, s.handleDecisionConfirm)

	// card_context - unified pre-work context
	mcp.AddTool(s.server, &mcp.Tool{
		Name: "card_context",
		Description: "Get unified context before working on files.\n\n" +
			"MODES:\n" +
			"- 'starting_task': Recent decisions + patterns + hotspots for general task awareness\n" +
			"- 'before_edit': File-specific decisions + applicable patterns before editing files\n" +
			"- 'reviewing_pr': Commit-based decisions with coverage analysis for PR reviews\n\n" +
			"PROACTIVE USE: Call this BEFORE any implementation work. Don't wait for users to ask.",
	}, s.handleContext)

	// card_query - unified search
	mcp.AddTool(s.server, &mcp.Tool{
		Name: "card_query",
		Description: "Unified search across CARD's memory.\n\n" +
			"TARGETS:\n" +
			"- 'decisions': Search decision capsules by files, tags, or text\n" +
			"- 'sessions': List sessions for a repository\n" +
			"- 'patterns': Get implementation patterns from sessions\n" +
			"- 'learnings': Query invalidated decisions with learned insights\n" +
			"- 'tags': List all tags from decision capsules\n" +
			"- 'hotspots': Find files with most decisions\n\n" +
			"SMART DEFAULT: Call with target='decisions' and no params for the 15 most recent decisions.",
	}, s.handleQuery)

	// card_session_ops - unified session operations
	mcp.AddTool(s.server, &mcp.Tool{
		Name: "card_session_ops",
		Description: "Unified session operations.\n\n" +
			"OPERATIONS:\n" +
			"- 'summary': Lightweight session overview (description, status, decision list)\n" +
			"- 'artifacts': Full artifacts (milestone_ledger, execution_log, verification_notes)\n" +
			"- 'history': All versioned execution logs across re-execution attempts\n" +
			"- 'review': All decisions with duplicate analysis\n" +
			"- 'dedupe': Merge semantic duplicates (use dedupe_dry_run for preview)",
	}, s.handleSessionOps)

	// card_capsule_ops - unified capsule operations
	mcp.AddTool(s.server, &mcp.Tool{
		Name: "card_capsule_ops",
		Description: "Unified capsule operations.\n\n" +
			"OPERATIONS:\n" +
			"- 'show': Full details of a decision capsule\n" +
			"- 'chain': Navigate supersession relationships\n" +
			"- 'invalidate': Mark a decision as invalidated (requires user_confirmed=true)\n" +
			"- 'graph': Show dependency graph (enables/constrains relationships)\n\n" +
			"For invalidate: You MUST (1) review with operation='show', (2) explain why, (3) get permission.",
	}, s.handleCapsuleOps)

	// card_snapshot - temporal queries for point-in-time decision state
	mcp.AddTool(s.server, &mcp.Tool{
		Name: "card_snapshot",
		Description: "Query CARD's decision state at a point in time. Use this for archaeological queries like " +
			"'what decisions existed before this commit?' or 'what changed in the last 2 weeks?'\n\n" +
			"AS_OF formats:\n" +
			"- ISO8601: '2026-01-15T10:00:00Z'\n" +
			"- Relative: '2 weeks ago', '3 days ago', 'yesterday'\n" +
			"- Commit: 'before:<commit-sha>' (resolves to commit timestamp)\n\n" +
			"Set compare_to_now=true to see what changed since that point.",
	}, s.handleSnapshot)

	// card_phase_complete - signal that a session phase is complete
	mcp.AddTool(s.server, &mcp.Tool{
		Name: "card_phase_complete",
		Description: "Signal that the current phase is complete. Call AFTER writing the artifact via " +
			"card_write_artifact. This allows CARD to gracefully advance to the next phase.\n\n" +
			"STATUS VALUES:\n" +
			"- 'complete': Phase finished successfully; advance to next phase\n" +
			"- 'blocked': Phase cannot proceed; session will pause with your summary as the reason\n" +
			"- 'needs_input': Waiting for human input before completing (use sparingly)\n\n" +
			"After calling this, CARD will terminate the Claude process and transition to the next phase.",
	}, s.handlePhaseComplete)
}

// RecallArgs defines the input for card_recall.
type RecallArgs struct {
	Files              []string `json:"files,omitempty" jsonschema:"File paths to search for related decisions (e.g. src/auth.ts)"`
	Tags               []string `json:"tags,omitempty" jsonschema:"Tags or keywords to search (e.g. authentication, database, api)"`
	Query              string   `json:"query,omitempty" jsonschema:"Search capsule content: the question asked, choice made, and rationale given. Example: 'TypeORM' finds 'Why TypeORM over raw SQL?' Use tags param for concept search like 'authentication'."`
	Repo               string   `json:"repo,omitempty" jsonschema:"Repository ID to scope the search (optional - searches all repos if not specified)"`
	IncludeEvolution   bool     `json:"include_evolution,omitempty" jsonschema:"If true, show all phases of each decision instead of just the latest (default: false)"`
	Status             string   `json:"status,omitempty" jsonschema:"Filter by capsule status: 'verified', 'hypothesis', or 'invalidated' (optional - returns all if not specified)"`
	Format             string   `json:"format,omitempty" jsonschema:"Output format: 'full' (default) or 'compact' (IDs and choices only)"`
	Significance       string   `json:"significance,omitempty" jsonschema:"Filter by significance tier: 'architectural', 'implementation', 'context', or 'all' (default: 'all')"`
	IncludeInvalidated bool     `json:"include_invalidated,omitempty" jsonschema:"If true, include invalidated decisions (default: false - excludes invalidated)"`
}

// RecallResult is the output of card_recall.
type RecallResult struct {
	Capsules []CapsuleSummary `json:"capsules"`
	Sessions []SessionSummary `json:"sessions"`
	Message  string           `json:"message,omitempty"`
}

// CapsuleSummary is a lightweight view of a capsule for recall results.
type CapsuleSummary struct {
	ID           string   `json:"id"`
	SessionID    string   `json:"session_id"`
	Phase        string   `json:"phase"`
	Question     string   `json:"question"`
	Choice       string   `json:"choice"`
	Rationale    string   `json:"rationale,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	MatchTier    string   `json:"match_tier,omitempty"`
	Status       string   `json:"status"`                      // verified, hypothesis, invalidated
	Type         string   `json:"type,omitempty"`              // decision or finding
	Significance string   `json:"significance,omitempty"`      // architectural, implementation, context
	SupersededBy string   `json:"superseded_by,omitempty"`     // If invalidated, what replaced it
	Recency      string   `json:"recency,omitempty"`           // Human-readable time (e.g., "2 days ago")
	PensieveLink string   `json:"pensieve_link,omitempty"`     // Path to milestone_ledger
}

// SessionSummary is a lightweight view of a session.
type SessionSummary struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

func (s *Server) handleRecall(ctx context.Context, req *mcp.CallToolRequest, args RecallArgs) (*mcp.CallToolResult, any, error) {
	q := recall.RecallQuery{
		Files:            args.Files,
		Tags:             args.Tags,
		Query:            args.Query,
		RepoID:           args.Repo,
		IncludeEvolution: args.IncludeEvolution,
	}

	// If repo specified, get the local path for git correlation
	if q.RepoID != "" {
		// Note: we don't have direct repo access here, but recall.Query handles missing RepoPath gracefully
	}

	result, err := recall.Query(s.store, q)
	if err != nil {
		return nil, nil, fmt.Errorf("recall query failed: %w", err)
	}

	// Convert to our output format
	out := RecallResult{}

	if len(result.Capsules) == 0 && len(result.Sessions) == 0 {
		out.Message = "No prior CARD context found for this query. This might be new territory, or try broadening your search with different tags or file paths."
		return nil, out, nil
	}

	// Filter by status if specified
	var statusFilter *capsule.CapsuleStatus
	if args.Status != "" {
		status := capsule.CapsuleStatus(args.Status)
		statusFilter = &status
	}

	// Filter by significance if specified
	var significanceFilter *capsule.Significance
	if args.Significance != "" && args.Significance != "all" {
		sig := capsule.Significance(strings.ToLower(args.Significance))
		significanceFilter = &sig
	}

	isCompact := args.Format == "compact"

	for _, sc := range result.Capsules {
		// Apply status filter
		if statusFilter != nil && sc.Status != *statusFilter {
			continue
		}

		// Exclude invalidated unless explicitly requested
		if !args.IncludeInvalidated && sc.Status == capsule.StatusInvalidated {
			continue
		}

		// Apply significance filter
		if significanceFilter != nil && sc.Significance != *significanceFilter {
			continue
		}

		summary := CapsuleSummary{
			ID:           sc.ID,
			SessionID:    sc.SessionID,
			Phase:        sc.Phase,
			Question:     sc.Question,
			Choice:       sc.Choice,
			Status:       string(sc.Status),
			Type:         string(sc.Type),
			Significance: string(sc.Significance),
			SupersededBy: sc.SupersededBy,
			Recency:      formatRelativeTime(sc.Timestamp),
			PensieveLink: fmt.Sprintf("~/.card/sessions/%s/milestone_ledger.md", sc.SessionID),
		}

		// Include full details unless compact mode
		if !isCompact {
			summary.Rationale = sc.Rationale
			summary.Tags = sc.Tags
			summary.MatchTier = sc.Tier.TierLabel()
		}

		out.Capsules = append(out.Capsules, summary)
	}

	for _, sess := range result.Sessions {
		out.Sessions = append(out.Sessions, SessionSummary{
			ID:          sess.ID,
			Description: sess.Description,
			Status:      sess.Status,
			CreatedAt:   sess.CreatedAt.Format("2006-01-02"),
		})
	}

	// Add hint about quickfix when results are found
	if len(out.Capsules) > 0 {
		out.Message = "DEPRECATED: card_recall will be removed in v2.0. Use card_query with target='decisions' instead. Tip: If you discover something that needs fixing, use card_quickfix_start to create a recorded quickfix session."
	} else if out.Message == "" {
		out.Message = "DEPRECATED: card_recall will be removed in v2.0. Use card_query with target='decisions' instead."
	} else {
		out.Message = "DEPRECATED: card_recall will be removed in v2.0. Use card_query with target='decisions' instead. " + out.Message
	}

	return nil, out, nil
}

// CapsuleShowArgs defines input for card_capsule_show.
type CapsuleShowArgs struct {
	ID string `json:"id" jsonschema:"The capsule ID to retrieve (e.g. 20260128-auth-fix-inv-abc123)"`
}

// CapsuleDetail is the full capsule output.
type CapsuleDetail struct {
	ID           string   `json:"id"`
	SessionID    string   `json:"session_id"`
	RepoIDs      []string `json:"repo_ids"`
	Phase        string   `json:"phase"`
	Question     string   `json:"question"`
	Choice       string   `json:"choice"`
	Alternatives []string `json:"alternatives,omitempty"`
	Rationale    string   `json:"rationale"`
	Tags         []string `json:"tags,omitempty"`
	Commits      []string `json:"commits,omitempty"`
	Timestamp    string   `json:"timestamp"`
}

func (s *Server) handleCapsuleShow(ctx context.Context, req *mcp.CallToolRequest, args CapsuleShowArgs) (*mcp.CallToolResult, any, error) {
	if args.ID == "" {
		return nil, nil, fmt.Errorf("capsule ID is required")
	}

	c, err := capsule.Get(s.store, args.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("capsule not found: %w", err)
	}

	out := CapsuleDetail{
		ID:           c.ID,
		SessionID:    c.SessionID,
		RepoIDs:      c.RepoIDs,
		Phase:        c.Phase,
		Question:     c.Question,
		Choice:       c.Choice,
		Alternatives: c.Alternatives,
		Rationale:    c.Rationale,
		Tags:         c.Tags,
		Commits:      c.Commits,
		Timestamp:    c.Timestamp.Format("2006-01-02 15:04:05"),
	}

	return nil, out, nil
}

// SessionsArgs defines input for card_sessions.
type SessionsArgs struct {
	Repo   string `json:"repo,omitempty" jsonschema:"Repository ID to filter by (optional)"`
	Status string `json:"status,omitempty" jsonschema:"Filter by status: completed, started, paused, abandoned (optional)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum number of sessions to return (default 20)"`
}

// SessionsResult is the output of card_sessions.
type SessionsResult struct {
	Sessions []SessionDetail `json:"sessions"`
	Message  string          `json:"message,omitempty"`
}

// SessionDetail provides more info than SessionSummary.
type SessionDetail struct {
	ID            string   `json:"id"`
	Description   string   `json:"description"`
	Status        string   `json:"status"`
	Repos         []string `json:"repos"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
	DecisionCount int      `json:"decision_count"`
	Summary       string   `json:"summary,omitempty"` // First line of description or context
}

func (s *Server) handleSessions(ctx context.Context, req *mcp.CallToolRequest, args SessionsArgs) (*mcp.CallToolResult, any, error) {
	sessions, err := session.List(s.store)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list sessions: %w", err)
	}

	out := SessionsResult{}
	limit := args.Limit
	if limit <= 0 {
		limit = 20
	}

	for _, sess := range sessions {
		// Filter by repo if specified
		if args.Repo != "" {
			found := false
			for _, r := range sess.Repos {
				if r == args.Repo {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Filter by status if specified
		if args.Status != "" && string(sess.Status) != args.Status {
			continue
		}

		// Count decisions for this session
		capsules, _ := capsule.List(s.store, capsule.Filter{SessionID: &sess.ID})
		decisionCount := len(capsules)

		// Generate summary from description or context
		summary := sess.Description
		if len(summary) > 100 {
			summary = summary[:100] + "..."
		}

		out.Sessions = append(out.Sessions, SessionDetail{
			ID:            sess.ID,
			Description:   sess.Description,
			Status:        string(sess.Status),
			Repos:         sess.Repos,
			CreatedAt:     sess.CreatedAt.Format("2006-01-02 15:04"),
			UpdatedAt:     sess.UpdatedAt.Format("2006-01-02 15:04"),
			DecisionCount: decisionCount,
			Summary:       summary,
		})

		if len(out.Sessions) >= limit {
			break
		}
	}

	if len(out.Sessions) == 0 {
		out.Message = "No sessions found matching the criteria."
	}

	return nil, out, nil
}

// SessionArtifactsArgs defines input for card_session_artifacts.
type SessionArtifactsArgs struct {
	SessionID string `json:"session_id" jsonschema:"The session ID to get artifacts from"`
}

// SessionArtifactsResult contains the session's artifacts.
type SessionArtifactsResult struct {
	SessionID         string `json:"session_id"`
	ExecutionAttempts int    `json:"execution_attempts"` // Number of execution attempts
	MilestoneLedger   string `json:"milestone_ledger,omitempty"`
	ExecutionLog      string `json:"execution_log,omitempty"`      // Latest execution log
	VerificationNotes string `json:"verification_notes,omitempty"` // Latest verification notes
	Message           string `json:"message,omitempty"`
}

func (s *Server) handleSessionArtifacts(ctx context.Context, req *mcp.CallToolRequest, args SessionArtifactsArgs) (*mcp.CallToolResult, any, error) {
	if args.SessionID == "" {
		return nil, nil, fmt.Errorf("session_id is required")
	}

	sess, err := session.Get(s.store, args.SessionID)
	if err != nil {
		return nil, nil, fmt.Errorf("session not found: %w", err)
	}

	out := SessionArtifactsResult{
		SessionID:         sess.ID,
		ExecutionAttempts: len(sess.ExecutionHistory),
	}

	// Read artifacts from session directory
	sessionDir := s.store.Path("sessions", sess.ID)

	if content := readArtifactFile(sessionDir, "milestone_ledger.md"); content != "" {
		out.MilestoneLedger = content
	}
	if content := readArtifactFile(sessionDir, "execution_log.md"); content != "" {
		out.ExecutionLog = content
	}
	if content := readArtifactFile(sessionDir, "verification_notes.md"); content != "" {
		out.VerificationNotes = content
	}

	if out.MilestoneLedger == "" && out.ExecutionLog == "" && out.VerificationNotes == "" {
		out.Message = "No artifacts found for this session. The session may not have progressed through those phases yet."
	} else if out.MilestoneLedger != "" && out.ExecutionLog == "" && sess.Status == session.StatusCompleted {
		out.Message = "Session completed. Execution logs are ephemeral and cleaned up after completion. The milestone_ledger contains the file manifest, patterns, and iteration summary. Use card_recall for queryable decisions."
	}

	return nil, out, nil
}

// readArtifactFile reads a file from the session directory, returning empty string on error.
func readArtifactFile(dir, filename string) string {
	content, err := os.ReadFile(filepath.Join(dir, filename))
	if err != nil {
		return ""
	}
	return string(content)
}

// SessionExecutionHistoryArgs defines input for card_session_execution_history.
type SessionExecutionHistoryArgs struct {
	SessionID string `json:"session_id" jsonschema:"The session ID to get execution history from"`
}

// ExecutionAttempt represents a single execution attempt with its artifacts.
type ExecutionAttempt struct {
	AttemptNumber     int    `json:"attempt_number"`
	ExecutionLog      string `json:"execution_log"`
	VerificationNotes string `json:"verification_notes,omitempty"`
}

// SessionExecutionHistoryResult contains all versioned execution artifacts.
type SessionExecutionHistoryResult struct {
	SessionID    string             `json:"session_id"`
	TotalAttempts int               `json:"total_attempts"`
	Attempts     []ExecutionAttempt `json:"attempts"`
	Message      string             `json:"message,omitempty"`
}

func (s *Server) handleSessionExecutionHistory(ctx context.Context, req *mcp.CallToolRequest, args SessionExecutionHistoryArgs) (*mcp.CallToolResult, any, error) {
	if args.SessionID == "" {
		return nil, nil, fmt.Errorf("session_id is required")
	}

	sess, err := session.Get(s.store, args.SessionID)
	if err != nil {
		return nil, nil, fmt.Errorf("session not found: %w", err)
	}

	sessionDir := s.store.Path("sessions", sess.ID)

	out := SessionExecutionHistoryResult{
		SessionID:     sess.ID,
		TotalAttempts: len(sess.ExecutionHistory),
		Attempts:      make([]ExecutionAttempt, 0),
	}

	// Load all versioned execution logs
	for i := 1; i <= len(sess.ExecutionHistory); i++ {
		attempt := ExecutionAttempt{
			AttemptNumber: i,
		}

		// Load execution log for this attempt
		execFile := fmt.Sprintf("execution_log_v%d.md", i)
		if content := readArtifactFile(sessionDir, execFile); content != "" {
			attempt.ExecutionLog = content
		}

		// Load verification notes for this attempt (may not exist for last attempt if still in progress)
		verifyFile := fmt.Sprintf("verification_notes_v%d.md", i)
		if content := readArtifactFile(sessionDir, verifyFile); content != "" {
			attempt.VerificationNotes = content
		}

		// Only include attempts that have at least an execution log
		if attempt.ExecutionLog != "" {
			out.Attempts = append(out.Attempts, attempt)
		}
	}

	if len(out.Attempts) == 0 {
		if sess.Status == session.StatusCompleted {
			out.Message = "Session completed. Execution logs are ephemeral and cleaned up after completion. Iteration history is preserved in session.yaml (ExecutionHistory field) and summarized in milestone_ledger.md. Use card_recall for queryable decisions."
		} else {
			out.Message = "No execution history found. The session may not have reached the execution phase yet."
		}
	}

	return nil, out, nil
}

// TagsListArgs defines input for card_tags_list (no arguments needed).
type TagsListArgs struct{}

// TagsListResult contains the list of tags.
type TagsListResult struct {
	Tags    []string `json:"tags"`
	Count   int      `json:"count"`
	Message string   `json:"message,omitempty"`
}

func (s *Server) handleTagsList(ctx context.Context, req *mcp.CallToolRequest, args TagsListArgs) (*mcp.CallToolResult, any, error) {
	tags, err := capsule.ListTags(s.store)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list tags: %w", err)
	}

	out := TagsListResult{
		Tags:  tags,
		Count: len(tags),
	}
	if len(tags) == 0 {
		out.Message = "No tags found. Tags are extracted from decision capsules - run some CARD sessions first."
	}

	return nil, out, nil
}

// QuickfixStartArgs defines input for card_quickfix_start.
type QuickfixStartArgs struct {
	UserConfirmed bool               `json:"user_confirmed" jsonschema:"REQUIRED. Set true ONLY after explicitly asking the user and receiving approval. You MUST explain what a quickfix session is before asking."`
	Description   string             `json:"description" jsonschema:"Brief description of the quickfix work (e.g., 'Fix IDOR in notification endpoints')"`
	Repos         []string           `json:"repos" jsonschema:"Repository IDs involved in this fix"`
	Context       string             `json:"context" jsonschema:"Discovery context - how this issue was found, why it matters, relevant prior decisions"`
	Decisions     []QuickfixDecision `json:"decisions,omitempty" jsonschema:"Pre-identified decisions to seed the session capsules"`
}

// QuickfixDecision represents a decision to seed into the quickfix session.
type QuickfixDecision struct {
	Question     string   `json:"question" jsonschema:"What was being decided"`
	Choice       string   `json:"choice" jsonschema:"What was chosen"`
	Alternatives []string `json:"alternatives,omitempty" jsonschema:"Options considered"`
	Rationale    string   `json:"rationale" jsonschema:"Why this choice"`
	Tags         []string `json:"tags,omitempty" jsonschema:"File paths, concepts, domains"`
	Source       string   `json:"source,omitempty" jsonschema:"'human' or 'agent' (default: agent)"`
}

// QuickfixStartResult is the output of card_quickfix_start.
type QuickfixStartResult struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	Message   string `json:"message"`
	NextSteps string `json:"next_steps"`
}

func (s *Server) handleQuickfixStart(ctx context.Context, req *mcp.CallToolRequest, args QuickfixStartArgs) (*mcp.CallToolResult, any, error) {
	if !args.UserConfirmed {
		return nil, nil, fmt.Errorf("user_confirmed must be true - you must ask the user for permission before creating a quickfix session")
	}
	if args.Description == "" {
		return nil, nil, fmt.Errorf("description is required")
	}
	if len(args.Repos) == 0 {
		return nil, nil, fmt.Errorf("at least one repo is required")
	}

	// Validate repos exist
	for _, repoID := range args.Repos {
		if _, err := repo.Get(s.store, repoID); err != nil {
			return nil, nil, fmt.Errorf("repo not found: %s", repoID)
		}
	}

	// Create quickfix session
	sess, err := session.CreateQuickfix(s.store, args.Description, args.Repos, args.Context)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create quickfix session: %w", err)
	}

	// Create change records for each repo (track base commit)
	for _, repoID := range args.Repos {
		if _, err := change.Create(s.store, sess.ID, repoID); err != nil {
			// Log but don't fail - change tracking is secondary
			continue
		}
	}

	// Seed pre-identified decisions as capsules
	if len(args.Decisions) > 0 {
		for _, d := range args.Decisions {
			source := d.Source
			if source == "" {
				source = "agent"
			}
			c := capsule.Capsule{
				SessionID:    sess.ID,
				RepoIDs:      args.Repos,
				Phase:        "quickfix-seed",
				Timestamp:    time.Now().UTC(),
				Question:     d.Question,
				Choice:       d.Choice,
				Alternatives: d.Alternatives,
				Rationale:    d.Rationale,
				Tags:         d.Tags,
				Source:       source,
			}
			c.ID = capsule.GenerateID(sess.ID, c.Phase, c.Question)

			if err := capsule.Store(s.store, c); err != nil {
				// Log but don't fail - seeding is optional
				continue
			}
		}
	}

	out := QuickfixStartResult{
		SessionID: sess.ID,
		Status:    string(sess.Status),
		Message:   fmt.Sprintf("Quickfix session '%s' created and ready for execution.", sess.ID),
		NextSteps: fmt.Sprintf(`
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  QUICKFIX SESSION READY
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  Session: %s

  To execute the fix:

  1. Exit this ask session (Ctrl+C)
  2. Run:  card session resume %s

  The session will start at the Execute phase with your
  discovery context preserved. After implementation,
  it runs Verify → Record to capture the decision.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
`, sess.ID, sess.ID),
	}

	return nil, out, nil
}

// RecordArgs defines input for card_record.
type RecordArgs struct {
	SessionID    string   `json:"session_id,omitempty" jsonschema:"Session ID to record to (optional - finds active session if not provided)"`
	Type         string   `json:"type" jsonschema:"Type of record: 'decision' (has alternatives) or 'finding' (observation/conclusion)"`
	Question     string   `json:"question" jsonschema:"What was being decided or investigated"`
	Choice       string   `json:"choice" jsonschema:"What was chosen or concluded"`
	Alternatives []string `json:"alternatives,omitempty" jsonschema:"Options considered (for decisions)"`
	Rationale    string   `json:"rationale" jsonschema:"Why this choice was made"`
	Tags         []string `json:"tags,omitempty" jsonschema:"File paths, concepts, domains (e.g. 'src/auth.ts', 'authorization')"`
	Source       string   `json:"source,omitempty" jsonschema:"'human' or 'agent' (default: agent)"`
}

// RecordResult is the output of card_record.
type RecordResult struct {
	CapsuleID string `json:"capsule_id"`
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

func (s *Server) handleRecord(ctx context.Context, req *mcp.CallToolRequest, args RecordArgs) (*mcp.CallToolResult, any, error) {
	if args.Question == "" {
		return nil, nil, fmt.Errorf("question is required")
	}
	if args.Choice == "" {
		return nil, nil, fmt.Errorf("choice is required")
	}

	// Find session
	var sess *session.Session
	var err error

	if args.SessionID != "" {
		sess, err = session.Get(s.store, args.SessionID)
		if err != nil {
			return nil, nil, fmt.Errorf("session not found: %s", args.SessionID)
		}
	} else {
		// Find most recent active session
		active, err := session.GetActive(s.store)
		if err != nil || len(active) == 0 {
			return nil, nil, fmt.Errorf("no active session found - use card_quickfix_start to create one, or provide session_id")
		}
		sess = &active[0]
	}

	// Determine type
	capsuleType := capsule.TypeDecision
	if args.Type == "finding" {
		capsuleType = capsule.TypeFinding
	}

	// Determine source
	source := args.Source
	if source == "" {
		source = "agent"
	}

	// Normalize tags
	tags := capsule.NormalizeTags(args.Tags)

	// Create capsule
	c := capsule.Capsule{
		SessionID:    sess.ID,
		RepoIDs:      sess.Repos,
		Phase:        string(sess.Status), // Use current status as phase
		Timestamp:    time.Now().UTC(),
		Question:     args.Question,
		Choice:       args.Choice,
		Alternatives: args.Alternatives,
		Rationale:    args.Rationale,
		Source:       source,
		Tags:         tags,
		Status:       capsule.StatusHypothesis,
		Type:         capsuleType,
	}
	c.ID = capsule.GenerateID(sess.ID, c.Phase, c.Question)

	if err := capsule.Store(s.store, c); err != nil {
		return nil, nil, fmt.Errorf("failed to store capsule: %w", err)
	}

	out := RecordResult{
		CapsuleID: c.ID,
		SessionID: sess.ID,
		Status:    string(c.Status),
		Message:   fmt.Sprintf("Decision recorded as %s (status: %s). Will be verified when session completes.", c.ID, c.Status),
	}

	return nil, out, nil
}

// FileContextArgs defines input for card_file_context.
type FileContextArgs struct {
	Files        []string `json:"files" jsonschema:"File paths to get context for (e.g. ['src/auth/guard.ts'])"`
	Significance string   `json:"significance,omitempty" jsonschema:"'architectural' (default) or 'all'. Use 'all' for complete history."`
}

// FileDecisions represents decisions related to a specific file.
type FileDecisions struct {
	CapsuleCount  int              `json:"capsule_count"`
	MostRecent    string           `json:"most_recent,omitempty"`
	StatusSummary string           `json:"status_summary"`
	Capsules      []CapsuleSummary `json:"capsules"`
	Sessions      []string         `json:"sessions"`
}

// FileContextResult is the output of card_file_context.
type FileContextResult struct {
	Files        map[string]FileDecisions `json:"files"`
	PensieveLink string                   `json:"pensieve_link,omitempty"`
	Message      string                   `json:"message,omitempty"`
}

func (s *Server) handleFileContext(ctx context.Context, req *mcp.CallToolRequest, args FileContextArgs) (*mcp.CallToolResult, any, error) {
	if len(args.Files) == 0 {
		return nil, nil, fmt.Errorf("at least one file path is required")
	}

	// Default to architectural only (reduces noise)
	filterArchitecturalOnly := args.Significance != "all"

	out := FileContextResult{
		Files: make(map[string]FileDecisions),
	}

	for _, file := range args.Files {
		// Query capsules with this file in tags
		allCapsules, err := capsule.List(s.store, capsule.Filter{})
		if err != nil {
			continue
		}

		var matchingCapsules []capsule.Capsule
		sessionSet := make(map[string]bool)
		matchedIDs := make(map[string]bool)

		for _, c := range allCapsules {
			// Skip invalidated capsules
			if c.Status == capsule.StatusInvalidated {
				continue
			}
			// Filter by significance (default: architectural only)
			if filterArchitecturalOnly && c.Significance != capsule.SignificanceArchitectural {
				continue
			}
			if capsule.MatchesTagQuery(c.Tags, "file:"+file) || capsule.MatchesTagQuery(c.Tags, file) {
				matchingCapsules = append(matchingCapsules, c)
				sessionSet[c.SessionID] = true
				matchedIDs[c.ID] = true
			}
		}

		// Also search milestone_ledger file manifests
		manifestMatches := searchFileManifests(s.store, file)
		for _, c := range manifestMatches {
			if !matchedIDs[c.ID] {
				matchingCapsules = append(matchingCapsules, c)
				sessionSet[c.SessionID] = true
				matchedIDs[c.ID] = true
			}
		}

		if len(matchingCapsules) == 0 {
			out.Files[file] = FileDecisions{
				CapsuleCount:  0,
				StatusSummary: "No prior decisions",
				Capsules:      []CapsuleSummary{},
				Sessions:      []string{},
			}
			continue
		}

		// Build status summary
		verified, hypothesis, invalidated := 0, 0, 0
		var mostRecent time.Time
		for _, c := range matchingCapsules {
			switch c.Status {
			case capsule.StatusVerified:
				verified++
			case capsule.StatusHypothesis:
				hypothesis++
			case capsule.StatusInvalidated:
				invalidated++
			}
			if c.Timestamp.After(mostRecent) {
				mostRecent = c.Timestamp
			}
		}

		statusParts := []string{}
		if verified > 0 {
			statusParts = append(statusParts, fmt.Sprintf("%d verified", verified))
		}
		if hypothesis > 0 {
			statusParts = append(statusParts, fmt.Sprintf("%d hypothesis", hypothesis))
		}
		if invalidated > 0 {
			statusParts = append(statusParts, fmt.Sprintf("%d invalidated", invalidated))
		}

		var capsuleSummaries []CapsuleSummary
		for _, c := range matchingCapsules {
			capsuleSummaries = append(capsuleSummaries, CapsuleSummary{
				ID:           c.ID,
				SessionID:    c.SessionID,
				Phase:        c.Phase,
				Question:     c.Question,
				Choice:       c.Choice,
				Rationale:    c.Rationale,
				Tags:         c.Tags,
				Status:       string(c.Status),
				Significance: string(c.Significance),
			})
		}

		var sessions []string
		for sessID := range sessionSet {
			sessions = append(sessions, sessID)
		}

		mostRecentStr := ""
		if !mostRecent.IsZero() {
			mostRecentStr = formatRelativeTime(mostRecent)
		}

		out.Files[file] = FileDecisions{
			CapsuleCount:  len(matchingCapsules),
			MostRecent:    mostRecentStr,
			StatusSummary: strings.Join(statusParts, ", "),
			Capsules:      capsuleSummaries,
			Sessions:      sessions,
		}

		// Set Pensieve link to most recent session
		if len(sessions) > 0 && out.PensieveLink == "" {
			out.PensieveLink = fmt.Sprintf("~/.card/sessions/%s/milestone_ledger.md", sessions[0])
		}
	}

	if len(out.Files) == 0 {
		out.Message = "No prior decisions found for these files."
	} else if filterArchitecturalOnly {
		out.Message = "Showing architectural decisions only. Use significance: 'all' for complete history."
	}

	return nil, out, nil
}

// CapsuleChainArgs defines input for card_capsule_chain.
type CapsuleChainArgs struct {
	ID string `json:"id" jsonschema:"The capsule ID to get the supersession chain for"`
}

// CapsuleChainResult is the output of card_capsule_chain.
type CapsuleChainResult struct {
	Capsule      CapsuleSummary   `json:"capsule"`
	Supersedes   []CapsuleSummary `json:"supersedes,omitempty"`
	SupersededBy *CapsuleSummary  `json:"superseded_by,omitempty"`
	ChainSummary string           `json:"chain_summary"`
}

func (s *Server) handleCapsuleChain(ctx context.Context, req *mcp.CallToolRequest, args CapsuleChainArgs) (*mcp.CallToolResult, any, error) {
	if args.ID == "" {
		return nil, nil, fmt.Errorf("capsule ID is required")
	}

	chain, err := capsule.GetChain(s.store, args.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get capsule chain: %w", err)
	}

	out := CapsuleChainResult{
		Capsule: CapsuleSummary{
			ID:        chain.Current.ID,
			SessionID: chain.Current.SessionID,
			Phase:     chain.Current.Phase,
			Question:  chain.Current.Question,
			Choice:    chain.Current.Choice,
			Rationale: chain.Current.Rationale,
			Tags:      chain.Current.Tags,
			MatchTier: string(chain.Current.Status),
		},
	}

	// Build supersedes list
	for _, c := range chain.Supersedes {
		out.Supersedes = append(out.Supersedes, CapsuleSummary{
			ID:        c.ID,
			SessionID: c.SessionID,
			Phase:     c.Phase,
			Question:  c.Question,
			Choice:    c.Choice,
			Rationale: c.Rationale,
			Tags:      c.Tags,
			MatchTier: string(c.Status),
		})
	}

	// Set superseded by
	if chain.SupersededBy != nil {
		out.SupersededBy = &CapsuleSummary{
			ID:        chain.SupersededBy.ID,
			SessionID: chain.SupersededBy.SessionID,
			Phase:     chain.SupersededBy.Phase,
			Question:  chain.SupersededBy.Question,
			Choice:    chain.SupersededBy.Choice,
			Rationale: chain.SupersededBy.Rationale,
			Tags:      chain.SupersededBy.Tags,
			MatchTier: string(chain.SupersededBy.Status),
		}
	}

	// Build chain summary
	summary := fmt.Sprintf("Current (%s)", chain.Current.Status)
	if len(chain.Supersedes) > 0 {
		summary += fmt.Sprintf(" ← supersedes %d older decision(s)", len(chain.Supersedes))
	}
	if chain.SupersededBy != nil {
		summary += fmt.Sprintf(" → superseded by %s", chain.SupersededBy.ID)
	}
	out.ChainSummary = summary

	return nil, out, nil
}

// InvalidateArgs defines input for card_invalidate.
type InvalidateArgs struct {
	UserConfirmed bool   `json:"user_confirmed" jsonschema:"REQUIRED. Set true ONLY after explicitly asking the user and receiving approval. You MUST explain why invalidation is warranted before asking."`
	ID            string `json:"id" jsonschema:"The capsule ID to invalidate (required)"`
	Reason        string `json:"reason" jsonschema:"Why this decision is being invalidated (required - must be non-empty)"`
	Learned       string `json:"learned,omitempty" jsonschema:"What was learned from this invalidation (distinct from reason - captures the insight)"`
	SupersededBy  string `json:"superseded_by,omitempty" jsonschema:"ID of the capsule that replaces this decision (if any)"`
}

// InvalidateResult is the output of card_invalidate.
type InvalidateResult struct {
	CapsuleID      string          `json:"capsule_id"`
	PreviousStatus string          `json:"previous_status"`
	NewStatus      string          `json:"new_status"`
	Reason         string          `json:"reason,omitempty"`
	Learned        string          `json:"learned,omitempty"`
	SupersededBy   *CapsuleSummary `json:"superseded_by,omitempty"`
	ChainSummary   string          `json:"chain_summary,omitempty"`
	Message        string          `json:"message"`
}

func (s *Server) handleInvalidate(ctx context.Context, req *mcp.CallToolRequest, args InvalidateArgs) (*mcp.CallToolResult, any, error) {
	// Require explicit user confirmation
	if !args.UserConfirmed {
		return nil, nil, fmt.Errorf("user_confirmed must be true. Before calling this tool, you must: (1) review the decision with card_capsule_show, (2) explain to the user why invalidation is warranted, (3) get explicit permission")
	}

	if args.ID == "" {
		return nil, nil, fmt.Errorf("capsule ID is required")
	}

	// Require non-empty reason
	if strings.TrimSpace(args.Reason) == "" {
		return nil, nil, fmt.Errorf("reason is required and must be non-empty - explain why this decision is being invalidated")
	}

	// Get the capsule first to check current status
	c, err := capsule.Get(s.store, args.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get capsule: %w", err)
	}

	// Check if already invalidated
	if c.Status == capsule.StatusInvalidated {
		return nil, InvalidateResult{
			CapsuleID:      c.ID,
			PreviousStatus: string(c.Status),
			NewStatus:      string(c.Status),
			Message:        fmt.Sprintf("Capsule %s is already invalidated", c.ID),
		}, nil
	}

	previousStatus := string(c.Status)
	reason := strings.TrimSpace(args.Reason)

	// Perform invalidation
	if err := capsule.Invalidate(s.store, args.ID, reason, args.Learned, args.SupersededBy); err != nil {
		return nil, nil, fmt.Errorf("failed to invalidate capsule: %w", err)
	}

	// Build result
	out := InvalidateResult{
		CapsuleID:      args.ID,
		PreviousStatus: previousStatus,
		NewStatus:      string(capsule.StatusInvalidated),
		Reason:         reason,
		Learned:        args.Learned,
		Message:        fmt.Sprintf("Capsule %s marked as invalidated", args.ID),
	}

	// If superseded_by was provided, fetch that capsule for the summary
	if args.SupersededBy != "" {
		if newC, err := capsule.Get(s.store, args.SupersededBy); err == nil {
			out.SupersededBy = &CapsuleSummary{
				ID:        newC.ID,
				SessionID: newC.SessionID,
				Phase:     newC.Phase,
				Question:  newC.Question,
				Choice:    newC.Choice,
				Rationale: newC.Rationale,
				Tags:      newC.Tags,
				Status:    string(newC.Status),
			}
			out.ChainSummary = fmt.Sprintf("%s (invalidated) → superseded by %s (%s)", args.ID, newC.ID, newC.Status)
		}
	}

	return nil, out, nil
}

// formatRelativeTime formats a time as a human-readable relative string.
func formatRelativeTime(t time.Time) string {
	duration := time.Since(t)

	if duration < time.Hour {
		mins := int(duration.Minutes())
		if mins <= 1 {
			return "just now"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	}
	if duration < 24*time.Hour {
		hours := int(duration.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	}
	days := int(duration.Hours() / 24)
	if days == 1 {
		return "1 day ago"
	}
	if days < 30 {
		return fmt.Sprintf("%d days ago", days)
	}
	return t.Format("2006-01-02")
}

// SessionSummaryArgs defines input for card_session_summary.
type SessionSummaryArgs struct {
	SessionID string `json:"session_id" jsonschema:"The session ID to get summary for"`
}

// SessionSummaryResult is a lightweight session summary.
type SessionSummaryResult struct {
	ID          string           `json:"id"`
	Description string           `json:"description"`
	Status      string           `json:"status"`
	Mode        string           `json:"mode,omitempty"`
	Repos       []string         `json:"repos"`
	CreatedAt   string           `json:"created_at"`
	UpdatedAt   string           `json:"updated_at"`
	Decisions   []CapsuleSummary `json:"decisions"`
	Message     string           `json:"message,omitempty"`
}

func (s *Server) handleSessionSummary(ctx context.Context, req *mcp.CallToolRequest, args SessionSummaryArgs) (*mcp.CallToolResult, any, error) {
	if args.SessionID == "" {
		return nil, nil, fmt.Errorf("session_id is required")
	}

	sess, err := session.Get(s.store, args.SessionID)
	if err != nil {
		return nil, nil, fmt.Errorf("session not found: %w", err)
	}

	out := SessionSummaryResult{
		ID:          sess.ID,
		Description: sess.Description,
		Status:      string(sess.Status),
		Mode:        string(sess.Mode),
		Repos:       sess.Repos,
		CreatedAt:   sess.CreatedAt.Format("2006-01-02 15:04"),
		UpdatedAt:   sess.UpdatedAt.Format("2006-01-02 15:04"),
	}

	// Get all capsules for this session
	capsules, err := capsule.List(s.store, capsule.Filter{SessionID: &sess.ID})
	if err == nil {
		for _, c := range capsules {
			out.Decisions = append(out.Decisions, CapsuleSummary{
				ID:        c.ID,
				SessionID: c.SessionID,
				Phase:     c.Phase,
				Question:  c.Question,
				Choice:    c.Choice,
				Rationale: c.Rationale,
				Tags:      c.Tags,
				Status:    string(c.Status),
				Type:      string(c.Type),
				Recency:   formatRelativeTime(c.Timestamp),
			})
		}
	}

	if len(out.Decisions) == 0 {
		out.Message = "No decisions recorded for this session yet."
	}

	return nil, out, nil
}

// HotspotsArgs defines input for card_hotspots.
type HotspotsArgs struct {
	Repo  string `json:"repo,omitempty" jsonschema:"Repository ID to scope the search (optional)"`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum number of hotspots to return (default 10)"`
}

// FileHotspot represents a file with decision count.
type FileHotspot struct {
	Path          string `json:"path"`
	DecisionCount int    `json:"decision_count"`
	LastTouched   string `json:"last_touched"`
}

// ConceptHotspot represents a concept/tag with decision count.
type ConceptHotspot struct {
	Concept       string `json:"concept"`
	DecisionCount int    `json:"decision_count"`
}

// HotspotsResult is the output of card_hotspots.
type HotspotsResult struct {
	FileHotspots    []FileHotspot    `json:"file_hotspots"`
	ConceptHotspots []ConceptHotspot `json:"concept_hotspots"`
	TotalDecisions  int              `json:"total_decisions"`
	Message         string           `json:"message,omitempty"`
}

func (s *Server) handleHotspots(ctx context.Context, req *mcp.CallToolRequest, args HotspotsArgs) (*mcp.CallToolResult, any, error) {
	filter := capsule.Filter{}
	if args.Repo != "" {
		filter.RepoID = &args.Repo
	}

	allCapsules, err := capsule.List(s.store, filter)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list capsules: %w", err)
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}

	// Count by file path
	fileCounts := make(map[string]int)
	fileLastTouched := make(map[string]time.Time)
	conceptCounts := make(map[string]int)

	for _, c := range allCapsules {
		for _, tag := range c.Tags {
			prefix, value := capsule.ParseTag(tag)
			switch prefix {
			case capsule.PrefixFile:
				fileCounts[value]++
				if c.Timestamp.After(fileLastTouched[value]) {
					fileLastTouched[value] = c.Timestamp
				}
			case capsule.PrefixConcept:
				conceptCounts[value]++
			case "": // untyped tags count as concepts
				conceptCounts[tag]++
			}
		}
	}

	// Sort files by count
	type fileCount struct {
		path  string
		count int
		last  time.Time
	}
	var files []fileCount
	for path, count := range fileCounts {
		files = append(files, fileCount{path, count, fileLastTouched[path]})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].count > files[j].count
	})

	// Sort concepts by count
	type conceptCount struct {
		concept string
		count   int
	}
	var concepts []conceptCount
	for concept, count := range conceptCounts {
		concepts = append(concepts, conceptCount{concept, count})
	}
	sort.Slice(concepts, func(i, j int) bool {
		return concepts[i].count > concepts[j].count
	})

	out := HotspotsResult{
		TotalDecisions: len(allCapsules),
	}

	for i, f := range files {
		if i >= limit {
			break
		}
		out.FileHotspots = append(out.FileHotspots, FileHotspot{
			Path:          f.path,
			DecisionCount: f.count,
			LastTouched:   formatRelativeTime(f.last),
		})
	}

	for i, c := range concepts {
		if i >= limit {
			break
		}
		out.ConceptHotspots = append(out.ConceptHotspots, ConceptHotspot{
			Concept:       c.concept,
			DecisionCount: c.count,
		})
	}

	if len(out.FileHotspots) == 0 && len(out.ConceptHotspots) == 0 {
		out.Message = "No hotspots found. Run some CARD sessions to build up decision history."
	}

	return nil, out, nil
}

// PatternsArgs defines input for card_patterns.
type PatternsArgs struct {
	Repo  string `json:"repo,omitempty" jsonschema:"Repository ID to scope the search (optional)"`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum number of patterns to return (default 20)"`
}

// Pattern represents an implementation pattern.
type Pattern struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Sessions    []string `json:"sessions"`
	Introduced  string   `json:"introduced"` // When first introduced
}

// PatternsResult is the output of card_patterns.
type PatternsResult struct {
	Patterns []Pattern `json:"patterns"`
	Message  string    `json:"message,omitempty"`
}

func (s *Server) handlePatterns(ctx context.Context, req *mcp.CallToolRequest, args PatternsArgs) (*mcp.CallToolResult, any, error) {
	// Get all sessions
	sessions, err := session.List(s.store)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list sessions: %w", err)
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 20
	}

	// Extract patterns from milestone_ledger files
	patternMap := make(map[string]*Pattern)

	for _, sess := range sessions {
		// Filter by repo if specified
		if args.Repo != "" {
			found := false
			for _, r := range sess.Repos {
				if r == args.Repo {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Read milestone_ledger.md
		ledgerPath := s.store.Path("sessions", sess.ID, "milestone_ledger.md")
		content, err := os.ReadFile(ledgerPath)
		if err != nil {
			continue
		}

		// Parse patterns section from milestone_ledger
		patterns := extractPatternsFromLedger(string(content))
		for _, p := range patterns {
			if existing, ok := patternMap[p.Name]; ok {
				// Add session to existing pattern
				existing.Sessions = append(existing.Sessions, sess.ID)
			} else {
				patternMap[p.Name] = &Pattern{
					Name:        p.Name,
					Description: p.Description,
					Sessions:    []string{sess.ID},
					Introduced:  sess.CreatedAt.Format("2006-01-02"),
				}
			}
		}
	}

	out := PatternsResult{}
	for _, p := range patternMap {
		out.Patterns = append(out.Patterns, *p)
		if len(out.Patterns) >= limit {
			break
		}
	}

	// Sort by number of sessions (most used first)
	sort.Slice(out.Patterns, func(i, j int) bool {
		return len(out.Patterns[i].Sessions) > len(out.Patterns[j].Sessions)
	})

	if len(out.Patterns) == 0 {
		out.Message = "No patterns found. Patterns are extracted from milestone_ledger files after sessions complete."
	}

	return nil, out, nil
}

// extractPatternsFromLedger parses patterns from a milestone_ledger markdown file.
func extractPatternsFromLedger(content string) []Pattern {
	var patterns []Pattern

	// Look for "## Implementation Patterns" or "## Patterns" section
	lines := strings.Split(content, "\n")
	inPatternSection := false
	var currentPattern *Pattern

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check for patterns section header
		if strings.HasPrefix(trimmed, "## ") && (strings.Contains(strings.ToLower(trimmed), "pattern") || strings.Contains(strings.ToLower(trimmed), "technique")) {
			inPatternSection = true
			continue
		}

		// Exit patterns section on next ## header
		if strings.HasPrefix(trimmed, "## ") && inPatternSection {
			break
		}

		if !inPatternSection {
			continue
		}

		// Parse pattern entries (### or - ** format)
		if strings.HasPrefix(trimmed, "### ") {
			if currentPattern != nil {
				patterns = append(patterns, *currentPattern)
			}
			currentPattern = &Pattern{
				Name: strings.TrimPrefix(trimmed, "### "),
			}
		} else if strings.HasPrefix(trimmed, "- **") || strings.HasPrefix(trimmed, "* **") {
			// Pattern in list format: - **Pattern Name:** Description
			if idx := strings.Index(trimmed, ":**"); idx != -1 {
				name := strings.TrimPrefix(trimmed[:idx], "- **")
				name = strings.TrimPrefix(name, "* **")
				desc := strings.TrimSpace(trimmed[idx+3:])
				patterns = append(patterns, Pattern{
					Name:        name,
					Description: desc,
				})
			}
		} else if currentPattern != nil && trimmed != "" && !strings.HasPrefix(trimmed, "-") {
			// Description for current pattern
			if currentPattern.Description == "" {
				currentPattern.Description = trimmed
			} else {
				currentPattern.Description += " " + trimmed
			}
		}
	}

	if currentPattern != nil {
		patterns = append(patterns, *currentPattern)
	}

	return patterns
}

// PreflightArgs defines input for card_preflight.
type PreflightArgs struct {
	Files        []string `json:"files" jsonschema:"File paths to get pre-flight briefing for"`
	Intent       string   `json:"intent,omitempty" jsonschema:"What you're planning to do (e.g., 'adding rate limiting', 'refactoring auth')"`
	Significance string   `json:"significance,omitempty" jsonschema:"'architectural' (default) or 'all'. Use 'all' for complete history."`
}

// PreflightResult is the output of card_preflight.
type PreflightResult struct {
	Files             map[string]FileDecisions `json:"files"`
	RelevantDecisions []CapsuleSummary         `json:"relevant_decisions"`
	Patterns          []Pattern                `json:"patterns,omitempty"`
	Recommendations   string                   `json:"recommendations"`
	Message           string                   `json:"message,omitempty"`
}

func (s *Server) handlePreflight(ctx context.Context, req *mcp.CallToolRequest, args PreflightArgs) (*mcp.CallToolResult, any, error) {
	if len(args.Files) == 0 {
		return nil, nil, fmt.Errorf("at least one file path is required")
	}

	// Default to architectural only (reduces noise)
	filterArchitecturalOnly := args.Significance != "all"

	out := PreflightResult{
		Files: make(map[string]FileDecisions),
	}

	// Get file context for each file
	allCapsules, _ := capsule.List(s.store, capsule.Filter{})
	matchedCapsuleIDs := make(map[string]bool)

	for _, file := range args.Files {
		var matchingCapsules []capsule.Capsule
		sessionSet := make(map[string]bool)

		for _, c := range allCapsules {
			// Skip invalidated capsules
			if c.Status == capsule.StatusInvalidated {
				continue
			}
			// Filter by significance (default: architectural only)
			if filterArchitecturalOnly && c.Significance != capsule.SignificanceArchitectural {
				continue
			}
			if capsule.MatchesTagQuery(c.Tags, "file:"+file) || capsule.MatchesTagQuery(c.Tags, file) {
				matchingCapsules = append(matchingCapsules, c)
				sessionSet[c.SessionID] = true
				matchedCapsuleIDs[c.ID] = true
			}
		}

		// Also search milestone_ledger file manifests
		manifestMatches := searchFileManifests(s.store, file)
		for _, c := range manifestMatches {
			if !matchedCapsuleIDs[c.ID] {
				matchingCapsules = append(matchingCapsules, c)
				sessionSet[c.SessionID] = true
				matchedCapsuleIDs[c.ID] = true
			}
		}

		if len(matchingCapsules) == 0 {
			out.Files[file] = FileDecisions{
				CapsuleCount:  0,
				StatusSummary: "No prior decisions",
				Capsules:      []CapsuleSummary{},
				Sessions:      []string{},
			}
			continue
		}

		// Build status summary
		verified, hypothesis, invalidated := 0, 0, 0
		var mostRecent time.Time
		for _, c := range matchingCapsules {
			switch c.Status {
			case capsule.StatusVerified:
				verified++
			case capsule.StatusHypothesis:
				hypothesis++
			case capsule.StatusInvalidated:
				invalidated++
			}
			if c.Timestamp.After(mostRecent) {
				mostRecent = c.Timestamp
			}
		}

		statusParts := []string{}
		if verified > 0 {
			statusParts = append(statusParts, fmt.Sprintf("%d verified", verified))
		}
		if hypothesis > 0 {
			statusParts = append(statusParts, fmt.Sprintf("%d hypothesis", hypothesis))
		}
		if invalidated > 0 {
			statusParts = append(statusParts, fmt.Sprintf("%d invalidated", invalidated))
		}

		var capsuleSummaries []CapsuleSummary
		for _, c := range matchingCapsules {
			capsuleSummaries = append(capsuleSummaries, CapsuleSummary{
				ID:        c.ID,
				SessionID: c.SessionID,
				Phase:     c.Phase,
				Question:  c.Question,
				Choice:    c.Choice,
				Rationale: c.Rationale,
				Tags:      c.Tags,
				Status:    string(c.Status),
			})
		}

		var sessions []string
		for sessID := range sessionSet {
			sessions = append(sessions, sessID)
		}

		mostRecentStr := ""
		if !mostRecent.IsZero() {
			mostRecentStr = formatRelativeTime(mostRecent)
		}

		out.Files[file] = FileDecisions{
			CapsuleCount:  len(matchingCapsules),
			MostRecent:    mostRecentStr,
			StatusSummary: strings.Join(statusParts, ", "),
			Capsules:      capsuleSummaries,
			Sessions:      sessions,
		}
	}

	// Add relevant decisions based on intent if provided
	if args.Intent != "" {
		// Search for capsules matching the intent
		intentCapsules := searchByIntent(allCapsules, args.Intent)
		for _, c := range intentCapsules {
			if !matchedCapsuleIDs[c.ID] {
				out.RelevantDecisions = append(out.RelevantDecisions, CapsuleSummary{
					ID:        c.ID,
					SessionID: c.SessionID,
					Phase:     c.Phase,
					Question:  c.Question,
					Choice:    c.Choice,
					Rationale: c.Rationale,
					Tags:      c.Tags,
					Status:    string(c.Status),
				})
			}
		}
	}

	// Generate recommendations
	out.Recommendations = generateRecommendations(out.Files, out.RelevantDecisions)

	return nil, out, nil
}

// searchFileManifests searches milestone_ledger file manifests for a file path.
func searchFileManifests(st *store.Store, file string) []capsule.Capsule {
	var result []capsule.Capsule

	sessions, _ := session.List(st)
	for _, sess := range sessions {
		ledgerPath := st.Path("sessions", sess.ID, "milestone_ledger.md")
		content, err := os.ReadFile(ledgerPath)
		if err != nil {
			continue
		}

		// Check if file is mentioned in the ledger
		if strings.Contains(string(content), file) {
			// Get capsules from this session
			caps, _ := capsule.List(st, capsule.Filter{SessionID: &sess.ID})
			result = append(result, caps...)
		}
	}

	return result
}

// searchByIntent finds capsules related to an intent string using fuzzy matching.
func searchByIntent(capsules []capsule.Capsule, intent string) []capsule.Capsule {
	var result []capsule.Capsule
	intentLower := strings.ToLower(intent)
	intentWords := strings.Fields(intentLower)

	for _, c := range capsules {
		score := 0
		searchText := strings.ToLower(c.Question + " " + c.Choice + " " + c.Rationale + " " + strings.Join(c.Tags, " "))

		for _, word := range intentWords {
			if strings.Contains(searchText, word) {
				score++
			}
		}

		// Synonym matching
		for _, word := range intentWords {
			synonyms := getSynonyms(word)
			for _, syn := range synonyms {
				if strings.Contains(searchText, syn) {
					score++
				}
			}
		}

		if score >= len(intentWords)/2+1 { // At least half the words match
			result = append(result, c)
		}
	}

	return result
}

// synonymGroups defines related terms for fuzzy matching.
var synonymGroups = [][]string{
	{"auth", "authentication", "login", "signin", "sign-in", "oauth", "jwt", "token"},
	{"authz", "authorization", "permission", "permissions", "access", "access-control", "rbac", "acl"},
	{"db", "database", "sql", "postgres", "postgresql", "mysql", "sqlite", "mongo", "mongodb"},
	{"api", "endpoint", "endpoints", "route", "routes", "handler", "handlers", "controller"},
	{"test", "tests", "testing", "spec", "specs", "unit", "integration", "e2e"},
	{"config", "configuration", "settings", "options", "preferences", "env", "environment"},
	{"cache", "caching", "redis", "memcache", "memoize", "memoization"},
	{"queue", "queues", "job", "jobs", "worker", "workers", "async", "background"},
	{"log", "logging", "logger", "logs", "debug", "trace", "audit"},
	{"error", "errors", "exception", "exceptions", "failure", "failures", "handling"},
	{"security", "secure", "vulnerability", "vulnerabilities", "xss", "csrf", "injection"},
	{"rate", "ratelimit", "rate-limit", "throttle", "throttling", "limit", "limiting"},
	{"validate", "validation", "validator", "validators", "schema", "sanitize"},
	{"user", "users", "account", "accounts", "profile", "profiles", "member"},
	{"notify", "notification", "notifications", "alert", "alerts", "email", "sms"},
}

// getSynonyms returns related terms for a given word.
func getSynonyms(word string) []string {
	wordLower := strings.ToLower(word)
	for _, group := range synonymGroups {
		for _, term := range group {
			if term == wordLower {
				// Return all other terms in the group
				var synonyms []string
				for _, t := range group {
					if t != wordLower {
						synonyms = append(synonyms, t)
					}
				}
				return synonyms
			}
		}
	}
	return nil
}

// AgentGuidanceArgs defines input for card_agent_guidance (no arguments needed).
type AgentGuidanceArgs struct{}

// AgentGuidanceResult contains proactive usage instructions.
type AgentGuidanceResult struct {
	Guidance string `json:"guidance"`
}

func (s *Server) handleAgentGuidance(ctx context.Context, req *mcp.CallToolRequest, args AgentGuidanceArgs) (*mcp.CallToolResult, any, error) {
	guidance := `# CARD Proactive Usage Guide

## The Dream: Push, Don't Pull
CARD should tell you what you need to know BEFORE you make mistakes. Don't wait to be asked - push relevant context proactively.

## Artifact Lifecycle — What Survives vs. What's Ephemeral

Understanding what persists is critical for querying sessions effectively:

**Durable (permanent):**
- milestone_ledger.md — File manifest, patterns, decisions summary, rollback commands
- capsules.md — All decision capsules for the session
- session.yaml — Metadata, status, execution history timestamps

**Ephemeral (cleaned up after session completion):**
- execution_log.md — Detailed implementation log
- verification_notes.md — Verification checklist and findings
- investigation_summary.md — Initial investigation notes
- implementation_guide.md — The plan that was executed

**What this means for queries:**
- For COMPLETED sessions: Use card_recall for decisions, card_session_artifacts for milestone_ledger
- For ACTIVE sessions: card_session_artifacts returns execution_log and verification_notes too
- Don't expect execution logs for old sessions — the decisions and file manifest capture what matters

## When to Call CARD Tools

### Before Reading/Editing Files
- Call card_file_context(files) BEFORE reading or editing any file
- If decisions exist, summarize them proactively to the user
- Example: "I see there are 3 prior decisions about this file, including..."

### At Start of Any Task
- Call card_recall() with no params to see the 15 most recent decisions
- Call card_preflight(files, intent) before implementing anything
- Mention relevant patterns: "This codebase uses X pattern for Y"

### When User Mentions a Topic
- If user says "authentication", call card_recall(tags: ["auth"])
- Synonym matching is built-in: "auth" finds "authentication", "login", etc.

### Before Proposing Changes
- Check if your proposal conflicts with prior decisions
- If verified decisions exist, respect them or explicitly note deviations

## Surfacing Context

### Always Tell Users About Prior Decisions
Bad: [silently read file, make changes]
Good: "Before editing auth.ts, I checked CARD and found 2 verified decisions:
1. Use guards at controller level (not service level)
2. JWT tokens expire after 1 hour
I'll follow these patterns in my implementation."

### Link to Sessions for Deep Dives
- Include session IDs and milestone_ledger paths
- Example: "For full context, see session 20260130-auth-refactor"

## Recording Decisions
- Use card_record() to capture significant decisions mid-task
- Use card_quickfix_start() when you discover something fixable during exploration

## Summary
1. Check context BEFORE touching files
2. Surface relevant decisions PROACTIVELY
3. Follow verified patterns unless explicitly overriding
4. Record new decisions as you make them`

	return nil, AgentGuidanceResult{Guidance: guidance}, nil
}

// generateRecommendations creates actionable guidance based on file context.
func generateRecommendations(files map[string]FileDecisions, relevant []CapsuleSummary) string {
	var parts []string

	// Check for files with prior decisions
	filesWithDecisions := 0
	for _, fd := range files {
		if fd.CapsuleCount > 0 {
			filesWithDecisions++
		}
	}

	if filesWithDecisions > 0 {
		parts = append(parts, fmt.Sprintf("Found prior decisions for %d/%d files.", filesWithDecisions, len(files)))
	}

	// Check for verified decisions to follow
	verifiedCount := 0
	for _, fd := range files {
		for _, c := range fd.Capsules {
			if c.Status == "verified" {
				verifiedCount++
			}
		}
	}
	if verifiedCount > 0 {
		parts = append(parts, fmt.Sprintf("There are %d verified decisions to consider.", verifiedCount))
	}

	// Add relevant intent matches
	if len(relevant) > 0 {
		parts = append(parts, fmt.Sprintf("Found %d related decisions matching your intent.", len(relevant)))
	}

	if len(parts) == 0 {
		return "No prior decisions found for these files. This appears to be new territory."
	}

	return strings.Join(parts, " ")
}

// WriteArtifactArgs defines input for card_write_artifact.
type WriteArtifactArgs struct {
	SessionID string `json:"session_id" jsonschema:"The session ID this artifact belongs to"`
	Phase     string `json:"phase" jsonschema:"The phase producing this artifact: investigate, plan, execute, or record"`
	Content   string `json:"content" jsonschema:"The full artifact content including YAML frontmatter (---\\nsession: ...\\n---)"`
}

// WriteArtifactResult is the output of card_write_artifact.
type WriteArtifactResult struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (s *Server) handleWriteArtifact(ctx context.Context, req *mcp.CallToolRequest, args WriteArtifactArgs) (*mcp.CallToolResult, any, error) {
	if args.SessionID == "" {
		return nil, nil, fmt.Errorf("session_id is required")
	}
	if args.Phase == "" {
		return nil, nil, fmt.Errorf("phase is required")
	}
	if args.Content == "" {
		return nil, nil, fmt.Errorf("content is required")
	}

	// Validate session exists
	sess, err := session.Get(s.store, args.SessionID)
	if err != nil {
		return nil, nil, fmt.Errorf("session not found: %s", args.SessionID)
	}

	// Parse the content as an artifact
	a, err := artifact.Parse([]byte(args.Content))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse artifact content: %w", err)
	}

	// Ensure frontmatter has required fields
	if a.Frontmatter.Session == "" {
		a.Frontmatter.Session = args.SessionID
	}
	if a.Frontmatter.Phase == "" {
		a.Frontmatter.Phase = args.Phase
	}
	if a.Frontmatter.Timestamp.IsZero() {
		a.Frontmatter.Timestamp = time.Now().UTC()
	}
	if a.Frontmatter.Status == "" {
		a.Frontmatter.Status = "final"
	}
	if len(a.Frontmatter.Repos) == 0 {
		a.Frontmatter.Repos = sess.Repos
	}

	// Validate phase
	validPhases := map[string]bool{
		"investigate": true,
		"plan":        true,
		"execute":     true,
		"record":      true,
	}
	if !validPhases[args.Phase] {
		return nil, nil, fmt.Errorf("invalid phase: %s (must be investigate, plan, execute, or record)", args.Phase)
	}

	// Store at session level
	destPath, err := artifact.StoreSessionLevel(s.store, args.SessionID, a)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to store artifact: %w", err)
	}

	return nil, WriteArtifactResult{
		Path:    destPath,
		Message: fmt.Sprintf("Artifact written to %s", destPath),
	}, nil
}

// PhaseCompleteArgs defines input for card_phase_complete.
type PhaseCompleteArgs struct {
	SessionID string `json:"session_id" jsonschema:"The session ID that is completing a phase"`
	Phase     string `json:"phase" jsonschema:"The phase completing: investigate, review, execute, verify, simplify, record, or conclude"`
	Status    string `json:"status" jsonschema:"'complete', 'blocked', or 'needs_input'"`
	Summary   string `json:"summary,omitempty" jsonschema:"Brief summary of outcome or blocking reason"`
}

// PhaseCompleteResult is the output of card_phase_complete.
type PhaseCompleteResult struct {
	Message string `json:"message"`
}

func (s *Server) handlePhaseComplete(ctx context.Context, req *mcp.CallToolRequest, args PhaseCompleteArgs) (*mcp.CallToolResult, any, error) {
	if args.SessionID == "" {
		return nil, nil, fmt.Errorf("session_id is required")
	}
	if args.Phase == "" {
		return nil, nil, fmt.Errorf("phase is required")
	}
	if args.Status == "" {
		return nil, nil, fmt.Errorf("status is required")
	}

	// Validate status
	validStatus := map[string]bool{"complete": true, "blocked": true, "needs_input": true}
	if !validStatus[args.Status] {
		return nil, nil, fmt.Errorf("invalid status: %s (must be complete, blocked, or needs_input)", args.Status)
	}

	// Validate phase
	validPhases := map[string]bool{
		"investigate": true,
		"review":      true,
		"execute":     true,
		"verify":      true,
		"simplify":    true,
		"record":      true,
		"conclude":    true,
	}
	if !validPhases[args.Phase] {
		return nil, nil, fmt.Errorf("invalid phase: %s", args.Phase)
	}

	// Validate session exists
	_, err := session.Get(s.store, args.SessionID)
	if err != nil {
		return nil, nil, fmt.Errorf("session not found: %s", args.SessionID)
	}

	// Get output dir from environment (set by orchestrator when invoking Claude)
	outputDir := os.Getenv("CARD_OUTPUT_DIR")
	if outputDir == "" {
		return nil, nil, fmt.Errorf("CARD_OUTPUT_DIR not set - this tool can only be called during a CARD session phase")
	}

	// Import the signal package and write signal file
	sig := &phaseCompleteSignal{
		SessionID: args.SessionID,
		Phase:     args.Phase,
		Status:    args.Status,
		Timestamp: time.Now().UTC(),
		Summary:   args.Summary,
	}

	if err := writePhaseCompleteSignal(outputDir, sig); err != nil {
		return nil, nil, fmt.Errorf("failed to write phase complete signal: %w", err)
	}

	msg := fmt.Sprintf("Phase %s marked as %s.", args.Phase, args.Status)
	if args.Status == "complete" {
		msg += " CARD will advance to the next phase."
	} else if args.Status == "blocked" {
		msg += " Session will pause."
	}

	return nil, PhaseCompleteResult{
		Message: msg,
	}, nil
}

// phaseCompleteSignal mirrors signal.PhaseCompleteSignal to avoid import cycle.
type phaseCompleteSignal struct {
	SessionID string    `yaml:"session_id"`
	Phase     string    `yaml:"phase"`
	Status    string    `yaml:"status"`
	Timestamp time.Time `yaml:"timestamp"`
	Summary   string    `yaml:"summary,omitempty"`
}

func writePhaseCompleteSignal(workDir string, sig *phaseCompleteSignal) error {
	signalsDir := filepath.Join(workDir, "signals")
	if err := os.MkdirAll(signalsDir, 0755); err != nil {
		return fmt.Errorf("failed to create signals directory: %w", err)
	}

	data, err := yaml.Marshal(sig)
	if err != nil {
		return fmt.Errorf("failed to marshal signal: %w", err)
	}

	path := filepath.Join(signalsDir, "phase_complete.yaml")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write signal file: %w", err)
	}

	return nil
}

// DecisionArgs defines input for card_decision.
type DecisionArgs struct {
	Question            string   `json:"question" jsonschema:"What is being decided"`
	Choice              string   `json:"choice" jsonschema:"What was chosen"`
	Alternatives        []string `json:"alternatives,omitempty" jsonschema:"Options considered (for decisions)"`
	Rationale           string   `json:"rationale" jsonschema:"Why this choice was made"`
	Tags                []string `json:"tags,omitempty" jsonschema:"File paths, concepts, domains (e.g. 'src/auth.ts', 'authorization')"`
	Origin              string   `json:"origin,omitempty" jsonschema:"'human' or 'agent' (default: agent)"`
	Significance        string   `json:"significance" jsonschema:"'architectural', 'implementation', or 'context'"`
	RequireConfirmation bool     `json:"require_confirmation" jsonschema:"If true, returns proposal for human review; if false, stores immediately"`
	SessionID           string   `json:"session_id,omitempty" jsonschema:"Session ID (optional - finds active session if not provided)"`
	EnabledBy           string   `json:"enabled_by,omitempty" jsonschema:"Capsule ID that enabled this decision (for dependency graph)"`
	Type                string   `json:"type,omitempty" jsonschema:"'decision' (has alternatives) or 'finding' (observation). Default: inferred from alternatives"`
}

// DecisionResult is the output of card_decision.
type DecisionResult struct {
	// For immediate storage (require_confirmation=false)
	CapsuleID     string `json:"capsule_id,omitempty"`
	SimilarWarning string `json:"similar_warning,omitempty"`

	// For proposal flow (require_confirmation=true)
	ProposalID          string              `json:"proposal_id,omitempty"`
	SimilarExisting     []SimilarDecision   `json:"similar_existing,omitempty"`
	Contradicts         []ConflictingDecision `json:"contradicts,omitempty"`
	SuggestedAction     string              `json:"suggested_action,omitempty"`
	AwaitingConfirmation bool               `json:"awaiting_confirmation,omitempty"`

	Message string `json:"message"`
}

func (s *Server) handleDecision(ctx context.Context, req *mcp.CallToolRequest, args DecisionArgs) (*mcp.CallToolResult, any, error) {
	// Validate required fields
	if args.Question == "" {
		return nil, nil, fmt.Errorf("question is required")
	}
	if args.Choice == "" {
		return nil, nil, fmt.Errorf("choice is required")
	}
	if args.Rationale == "" {
		return nil, nil, fmt.Errorf("rationale is required")
	}
	if args.Significance == "" {
		return nil, nil, fmt.Errorf("significance is required (architectural, implementation, or context)")
	}

	// Validate significance
	sig := capsule.Significance(strings.ToLower(args.Significance))
	if sig != capsule.SignificanceArchitectural && sig != capsule.SignificanceImplementation && sig != capsule.SignificanceContext {
		return nil, nil, fmt.Errorf("invalid significance: %s (must be architectural, implementation, or context)", args.Significance)
	}

	// Find session
	sessionID := args.SessionID
	if sessionID == "" {
		// Find most recent active session
		activeSessions, err := session.GetActive(s.store)
		if err != nil || len(activeSessions) == 0 {
			return nil, nil, fmt.Errorf("no active session found; provide session_id or start a session first")
		}
		sessionID = activeSessions[0].ID // Use most recent active session
	}

	// Verify session exists
	sess, err := session.Get(s.store, sessionID)
	if err != nil {
		return nil, nil, fmt.Errorf("session not found: %s", sessionID)
	}

	// Determine origin
	origin := args.Origin
	if origin == "" {
		origin = "agent"
	}

	// Determine type
	capsuleType := capsule.TypeDecision
	if args.Type != "" {
		capsuleType = capsule.CapsuleType(strings.ToLower(args.Type))
	} else if len(args.Alternatives) == 0 {
		capsuleType = capsule.TypeFinding
	}

	// Normalize tags
	normalizedTags := capsule.NormalizeTags(args.Tags)

	// Build the capsule
	now := time.Now().UTC()
	c := capsule.Capsule{
		SessionID:    sessionID,
		RepoIDs:      sess.Repos,
		Phase:        string(sess.Status), // Use current session status as phase
		Timestamp:    now,
		CreatedAt:    now,
		Question:     args.Question,
		Choice:       args.Choice,
		Alternatives: args.Alternatives,
		Rationale:    args.Rationale,
		Origin:       origin,
		Source:       origin, // Keep Source for backwards compatibility
		Tags:         normalizedTags,
		Status:       capsule.StatusHypothesis,
		Type:         capsuleType,
		Significance: sig,
		EnabledBy:    args.EnabledBy,
	}
	c.ID = capsule.GenerateID(sessionID, c.Phase, args.Question)

	// Load existing capsules for similarity check
	existingCapsules, _ := capsule.List(s.store, capsule.Filter{
		SessionID:          &sessionID,
		IncludeInvalidated: false,
	})

	if args.RequireConfirmation {
		// Full path: check for similar and contradicting decisions
		similarResult := capsule.FastSimilarityCheck(existingCapsules, c)

		// Also check for contradictions across all active decisions
		allActive, _ := capsule.List(s.store, capsule.Filter{
			IncludeInvalidated: false,
		})
		contradictResult := capsule.FastContradictionCheck(allActive, c)

		// Merge results
		mergedResult := capsule.MergeSimilarityResults(similarResult, contradictResult)

		// Create proposal
		proposal := &Proposal{
			SessionID:       sessionID,
			Capsule:         c,
			SuggestedAction: "create",
		}

		if mergedResult != nil {
			// Convert similarity results to proposal format
			for _, sim := range mergedResult.Similar {
				proposal.SimilarExisting = append(proposal.SimilarExisting, SimilarDecision{
					ID:               sim.CapsuleID,
					Question:         sim.Question,
					Choice:           sim.Choice,
					Phase:            sim.Phase,
					SimilarityReason: sim.SimilarityReason,
				})
			}
			for _, con := range mergedResult.Contradicts {
				proposal.Contradicts = append(proposal.Contradicts, ConflictingDecision{
					ID:        con.CapsuleID,
					Question:  con.Question,
					Choice:    con.Choice,
					SessionID: con.SessionID,
					Reason:    con.Reason,
				})
			}
			proposal.SuggestedAction = mergedResult.SuggestedAction
		}

		// Store proposal
		proposalID := s.proposals.Create(proposal)

		return nil, DecisionResult{
			ProposalID:           proposalID,
			SimilarExisting:      proposal.SimilarExisting,
			Contradicts:          proposal.Contradicts,
			SuggestedAction:      proposal.SuggestedAction,
			AwaitingConfirmation: true,
			Message:              "Decision proposal created. Present similar/contradicting decisions to user and call card_decision_confirm with their choice.",
		}, nil
	}

	// Fast path: store immediately
	c.Confirmation = capsule.ConfirmationImplicit

	// Quick similarity check (non-blocking warning)
	var similarWarning string
	if similarResult := capsule.FastSimilarityCheck(existingCapsules, c); similarResult != nil && len(similarResult.Similar) > 0 {
		similarWarning = fmt.Sprintf("Note: Similar decision already exists: %s", similarResult.Similar[0].CapsuleID)
	}

	// Store the capsule
	if err := capsule.Store(s.store, c); err != nil {
		return nil, nil, fmt.Errorf("failed to store decision: %w", err)
	}

	return nil, DecisionResult{
		CapsuleID:      c.ID,
		SimilarWarning: similarWarning,
		Message:        fmt.Sprintf("Decision stored: %s", c.ID),
	}, nil
}

// DecisionConfirmArgs defines input for card_decision_confirm.
type DecisionConfirmArgs struct {
	ProposalID         string   `json:"proposal_id" jsonschema:"The proposal ID from card_decision"`
	Action             string   `json:"action" jsonschema:"'create', 'supersede', 'skip', or 'merge_into:<capsule_id>'"`
	InvalidateIDs      []string `json:"invalidate_ids,omitempty" jsonschema:"Capsule IDs to invalidate (for supersede action)"`
	InvalidationReason string   `json:"invalidation_reason,omitempty" jsonschema:"Why the old decisions are being invalidated"`
	Learned            string   `json:"learned,omitempty" jsonschema:"What was learned from the invalidation (insight gained)"`
}

// DecisionConfirmResult is the output of card_decision_confirm.
type DecisionConfirmResult struct {
	CapsuleID   string   `json:"capsule_id,omitempty"`
	Invalidated []string `json:"invalidated,omitempty"`
	Message     string   `json:"message"`
}

func (s *Server) handleDecisionConfirm(ctx context.Context, req *mcp.CallToolRequest, args DecisionConfirmArgs) (*mcp.CallToolResult, any, error) {
	if args.ProposalID == "" {
		return nil, nil, fmt.Errorf("proposal_id is required")
	}
	if args.Action == "" {
		return nil, nil, fmt.Errorf("action is required (create, supersede, skip, or merge_into:<id>)")
	}

	// Get the proposal
	proposal, err := s.proposals.Get(args.ProposalID)
	if err != nil {
		return nil, nil, fmt.Errorf("proposal not found or expired: %w", err)
	}

	// Handle actions
	switch {
	case args.Action == "skip":
		// Discard proposal
		s.proposals.Delete(args.ProposalID)
		return nil, DecisionConfirmResult{
			Message: "Decision proposal discarded. No capsule stored.",
		}, nil

	case args.Action == "create":
		// Store with explicit confirmation
		c := proposal.Capsule
		c.Confirmation = capsule.ConfirmationExplicit

		if err := capsule.Store(s.store, c); err != nil {
			return nil, nil, fmt.Errorf("failed to store decision: %w", err)
		}

		s.proposals.Delete(args.ProposalID)
		return nil, DecisionConfirmResult{
			CapsuleID: c.ID,
			Message:   fmt.Sprintf("Decision stored with explicit confirmation: %s", c.ID),
		}, nil

	case args.Action == "supersede":
		// Store new capsule and invalidate old ones
		c := proposal.Capsule
		c.Confirmation = capsule.ConfirmationExplicit

		// Link to superseded decisions
		c.Supersedes = args.InvalidateIDs

		if err := capsule.Store(s.store, c); err != nil {
			return nil, nil, fmt.Errorf("failed to store decision: %w", err)
		}

		// Invalidate the old decisions
		var invalidated []string
		for _, oldID := range args.InvalidateIDs {
			if err := capsule.Invalidate(s.store, oldID, args.InvalidationReason, args.Learned, c.ID); err == nil {
				invalidated = append(invalidated, oldID)
			}
		}

		s.proposals.Delete(args.ProposalID)
		return nil, DecisionConfirmResult{
			CapsuleID:   c.ID,
			Invalidated: invalidated,
			Message:     fmt.Sprintf("Decision stored: %s. Superseded %d prior decisions.", c.ID, len(invalidated)),
		}, nil

	case strings.HasPrefix(args.Action, "merge_into:"):
		// Merge into existing capsule
		targetID := strings.TrimPrefix(args.Action, "merge_into:")
		target, err := capsule.Get(s.store, targetID)
		if err != nil {
			return nil, nil, fmt.Errorf("target capsule not found: %s", targetID)
		}

		// Enrich the target capsule
		if proposal.Capsule.Rationale != "" && !strings.Contains(target.Rationale, proposal.Capsule.Rationale) {
			target.Rationale = target.Rationale + "\n\nAdditional context: " + proposal.Capsule.Rationale
		}
		// Merge tags
		tagSet := make(map[string]bool)
		for _, t := range target.Tags {
			tagSet[t] = true
		}
		for _, t := range proposal.Capsule.Tags {
			if !tagSet[t] {
				target.Tags = append(target.Tags, t)
			}
		}
		// Use higher significance
		if proposal.Capsule.Significance == capsule.SignificanceArchitectural {
			target.Significance = capsule.SignificanceArchitectural
		}

		if err := capsule.Store(s.store, *target); err != nil {
			return nil, nil, fmt.Errorf("failed to update capsule: %w", err)
		}

		s.proposals.Delete(args.ProposalID)
		return nil, DecisionConfirmResult{
			CapsuleID: target.ID,
			Message:   fmt.Sprintf("Merged into existing capsule: %s", target.ID),
		}, nil

	default:
		return nil, nil, fmt.Errorf("invalid action: %s", args.Action)
	}
}

// ========== CONSOLIDATED TOOL HANDLERS (Phase 3) ==========

// ContextArgs defines input for card_context.
type ContextArgs struct {
	Mode         string   `json:"mode" jsonschema:"'starting_task', 'before_edit', or 'reviewing_pr'"`
	Files        []string `json:"files,omitempty" jsonschema:"File paths (required for before_edit mode)"`
	Intent       string   `json:"intent,omitempty" jsonschema:"What you're planning to do (e.g., 'adding rate limiting')"`
	Significance string   `json:"significance,omitempty" jsonschema:"'architectural' (default) or 'all'"`
	Limit        int      `json:"limit,omitempty" jsonschema:"Maximum results to return (default 15)"`
}

// ContextResult is the output of card_context.
type ContextResult struct {
	Mode              string                   `json:"mode"`
	Files             map[string]FileDecisions `json:"files,omitempty"`
	RecentDecisions   []CapsuleSummary         `json:"recent_decisions,omitempty"`
	RelevantDecisions []CapsuleSummary         `json:"relevant_decisions,omitempty"`
	Patterns          []Pattern                `json:"patterns,omitempty"`
	Hotspots          []FileHotspot            `json:"hotspots,omitempty"`
	Recommendations   string                   `json:"recommendations,omitempty"`
	Message           string                   `json:"message,omitempty"`
}

func (s *Server) handleContext(ctx context.Context, req *mcp.CallToolRequest, args ContextArgs) (*mcp.CallToolResult, any, error) {
	if args.Mode == "" {
		args.Mode = "starting_task"
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 15
	}

	filterArchitecturalOnly := args.Significance != "all"

	out := ContextResult{
		Mode:  args.Mode,
		Files: make(map[string]FileDecisions),
	}

	switch args.Mode {
	case "starting_task":
		// Recent decisions + patterns + hotspots
		allCapsules, _ := capsule.List(s.store, capsule.Filter{IncludeInvalidated: false})

		// Get recent decisions
		sort.Slice(allCapsules, func(i, j int) bool {
			return allCapsules[i].Timestamp.After(allCapsules[j].Timestamp)
		})

		for i, c := range allCapsules {
			if i >= limit {
				break
			}
			if filterArchitecturalOnly && c.Significance != capsule.SignificanceArchitectural {
				continue
			}
			out.RecentDecisions = append(out.RecentDecisions, CapsuleSummary{
				ID:           c.ID,
				SessionID:    c.SessionID,
				Phase:        c.Phase,
				Question:     c.Question,
				Choice:       c.Choice,
				Rationale:    c.Rationale,
				Tags:         c.Tags,
				Status:       string(c.Status),
				Significance: string(c.Significance),
				Recency:      formatRelativeTime(c.Timestamp),
			})
		}

		// Get patterns
		_, patternsResult, _ := s.handlePatterns(ctx, req, PatternsArgs{Limit: 5})
		if pr, ok := patternsResult.(PatternsResult); ok {
			out.Patterns = pr.Patterns
		}

		// Get hotspots
		_, hotspotsResult, _ := s.handleHotspots(ctx, req, HotspotsArgs{Limit: 5})
		if hr, ok := hotspotsResult.(HotspotsResult); ok {
			out.Hotspots = hr.FileHotspots
		}

		out.Message = fmt.Sprintf("Starting task context: %d recent decisions, %d patterns, %d file hotspots",
			len(out.RecentDecisions), len(out.Patterns), len(out.Hotspots))

	case "before_edit":
		if len(args.Files) == 0 {
			return nil, nil, fmt.Errorf("files are required for before_edit mode")
		}

		// Delegate to existing file context logic
		fileCtxArgs := FileContextArgs{Files: args.Files, Significance: args.Significance}
		_, fileCtxRes, err := s.handleFileContext(ctx, req, fileCtxArgs)
		if err != nil {
			return nil, nil, err
		}
		if fcr, ok := fileCtxRes.(FileContextResult); ok {
			out.Files = fcr.Files
		}

		// Add intent-based relevant decisions
		if args.Intent != "" {
			allCapsules, _ := capsule.List(s.store, capsule.Filter{IncludeInvalidated: false})
			intentCapsules := searchByIntent(allCapsules, args.Intent)
			for _, c := range intentCapsules {
				out.RelevantDecisions = append(out.RelevantDecisions, CapsuleSummary{
					ID:           c.ID,
					SessionID:    c.SessionID,
					Phase:        c.Phase,
					Question:     c.Question,
					Choice:       c.Choice,
					Rationale:    c.Rationale,
					Tags:         c.Tags,
					Status:       string(c.Status),
					Significance: string(c.Significance),
				})
			}
		}

		out.Recommendations = generateRecommendations(out.Files, out.RelevantDecisions)

	case "reviewing_pr":
		// For PR review, show decisions related to the files
		if len(args.Files) == 0 {
			return nil, nil, fmt.Errorf("files are required for reviewing_pr mode")
		}

		fileCtxArgs := FileContextArgs{Files: args.Files, Significance: args.Significance}
		_, fileCtxRes, err := s.handleFileContext(ctx, req, fileCtxArgs)
		if err != nil {
			return nil, nil, err
		}
		if fcr, ok := fileCtxRes.(FileContextResult); ok {
			out.Files = fcr.Files
		}

		// Count coverage
		filesWithDecisions := 0
		totalDecisions := 0
		for _, fd := range out.Files {
			if fd.CapsuleCount > 0 {
				filesWithDecisions++
				totalDecisions += fd.CapsuleCount
			}
		}

		out.Message = fmt.Sprintf("PR coverage: %d/%d files have prior decisions (%d total decisions)",
			filesWithDecisions, len(args.Files), totalDecisions)

	default:
		return nil, nil, fmt.Errorf("invalid mode: %s (must be starting_task, before_edit, or reviewing_pr)", args.Mode)
	}

	return nil, out, nil
}

// QueryArgs defines input for card_query.
type QueryArgs struct {
	Target             string   `json:"target" jsonschema:"'decisions', 'sessions', 'patterns', 'learnings', 'tags', or 'hotspots'"`
	Query              string   `json:"query,omitempty" jsonschema:"Search query text"`
	Tags               []string `json:"tags,omitempty" jsonschema:"Tags to filter by"`
	Files              []string `json:"files,omitempty" jsonschema:"File paths to filter by"`
	Repo               string   `json:"repo,omitempty" jsonschema:"Repository ID to scope the search"`
	Significance       string   `json:"significance,omitempty" jsonschema:"'architectural', 'implementation', 'context', or 'all'"`
	Status             string   `json:"status,omitempty" jsonschema:"Filter by status: 'verified', 'hypothesis', 'invalidated'"`
	IncludeInvalidated bool     `json:"include_invalidated,omitempty" jsonschema:"Include invalidated decisions (default: false)"`
	IncludeEvolution   bool     `json:"include_evolution,omitempty" jsonschema:"Show all phases of each decision"`
	Limit              int      `json:"limit,omitempty" jsonschema:"Maximum results to return"`
}

// QueryResult is the output of card_query.
type QueryResult struct {
	Target   string           `json:"target"`
	Capsules []CapsuleSummary `json:"capsules,omitempty"`
	Sessions []SessionDetail  `json:"sessions,omitempty"`
	Patterns []Pattern        `json:"patterns,omitempty"`
	Tags     []string         `json:"tags,omitempty"`
	Hotspots *HotspotsResult  `json:"hotspots,omitempty"`
	Message  string           `json:"message,omitempty"`
}

func (s *Server) handleQuery(ctx context.Context, req *mcp.CallToolRequest, args QueryArgs) (*mcp.CallToolResult, any, error) {
	if args.Target == "" {
		args.Target = "decisions"
	}

	out := QueryResult{Target: args.Target}

	switch args.Target {
	case "decisions":
		// Delegate to existing recall logic
		recallArgs := RecallArgs{
			Files:              args.Files,
			Tags:               args.Tags,
			Query:              args.Query,
			Repo:               args.Repo,
			IncludeEvolution:   args.IncludeEvolution,
			Status:             args.Status,
			Significance:       args.Significance,
			IncludeInvalidated: args.IncludeInvalidated,
		}
		_, recallRes, err := s.handleRecall(ctx, req, recallArgs)
		if err != nil {
			return nil, nil, err
		}
		if rr, ok := recallRes.(RecallResult); ok {
			out.Capsules = rr.Capsules
			out.Message = rr.Message
		}

	case "sessions":
		sessArgs := SessionsArgs{
			Repo:   args.Repo,
			Status: args.Status,
			Limit:  args.Limit,
		}
		_, sessRes, err := s.handleSessions(ctx, req, sessArgs)
		if err != nil {
			return nil, nil, err
		}
		if sr, ok := sessRes.(SessionsResult); ok {
			out.Sessions = sr.Sessions
			out.Message = sr.Message
		}

	case "patterns":
		patArgs := PatternsArgs{
			Repo:  args.Repo,
			Limit: args.Limit,
		}
		_, patRes, err := s.handlePatterns(ctx, req, patArgs)
		if err != nil {
			return nil, nil, err
		}
		if pr, ok := patRes.(PatternsResult); ok {
			out.Patterns = pr.Patterns
			out.Message = pr.Message
		}

	case "learnings":
		// Query invalidated decisions with learned insights
		allCapsules, err := capsule.List(s.store, capsule.Filter{IncludeInvalidated: true})
		if err != nil {
			return nil, nil, err
		}

		for _, c := range allCapsules {
			if c.Status != capsule.StatusInvalidated {
				continue
			}
			// Only include if there's a learned insight
			if c.Learned == "" && c.InvalidationReason == "" {
				continue
			}

			out.Capsules = append(out.Capsules, CapsuleSummary{
				ID:           c.ID,
				SessionID:    c.SessionID,
				Phase:        c.Phase,
				Question:     c.Question,
				Choice:       c.Choice,
				Rationale:    fmt.Sprintf("INVALIDATED: %s\nLEARNED: %s", c.InvalidationReason, c.Learned),
				Tags:         c.Tags,
				Status:       string(c.Status),
				Significance: string(c.Significance),
				SupersededBy: c.SupersededBy,
				Recency:      formatRelativeTime(c.Timestamp),
			})
		}

		if len(out.Capsules) == 0 {
			out.Message = "No learnings found. Learnings are captured when decisions are invalidated with the 'learned' field."
		} else {
			out.Message = fmt.Sprintf("Found %d invalidated decisions with learnings.", len(out.Capsules))
		}

	case "tags":
		_, tagRes, err := s.handleTagsList(ctx, req, TagsListArgs{})
		if err != nil {
			return nil, nil, err
		}
		if tr, ok := tagRes.(TagsListResult); ok {
			out.Tags = tr.Tags
			out.Message = tr.Message
		}

	case "hotspots":
		hotArgs := HotspotsArgs{
			Repo:  args.Repo,
			Limit: args.Limit,
		}
		_, hotRes, err := s.handleHotspots(ctx, req, hotArgs)
		if err != nil {
			return nil, nil, err
		}
		if hr, ok := hotRes.(HotspotsResult); ok {
			out.Hotspots = &hr
			out.Message = hr.Message
		}

	default:
		return nil, nil, fmt.Errorf("invalid target: %s (must be decisions, sessions, patterns, learnings, tags, or hotspots)", args.Target)
	}

	return nil, out, nil
}

// SessionOpsArgs defines input for card_session_ops.
type SessionOpsArgs struct {
	SessionID    string `json:"session_id" jsonschema:"The session ID to operate on"`
	Operation    string `json:"operation" jsonschema:"'summary', 'artifacts', 'history', 'review', or 'dedupe'"`
	DedupeDryRun bool   `json:"dedupe_dry_run,omitempty" jsonschema:"For dedupe: preview only without merging"`
}

// SessionOpsResult is the output of card_session_ops.
type SessionOpsResult struct {
	SessionID string `json:"session_id"`
	Operation string `json:"operation"`

	// For summary
	Description string           `json:"description,omitempty"`
	Status      string           `json:"status,omitempty"`
	Mode        string           `json:"mode,omitempty"`
	Repos       []string         `json:"repos,omitempty"`
	CreatedAt   string           `json:"created_at,omitempty"`
	UpdatedAt   string           `json:"updated_at,omitempty"`
	Decisions   []CapsuleSummary `json:"decisions,omitempty"`

	// For artifacts
	ExecutionAttempts int    `json:"execution_attempts,omitempty"`
	MilestoneLedger   string `json:"milestone_ledger,omitempty"`
	ExecutionLog      string `json:"execution_log,omitempty"`
	VerificationNotes string `json:"verification_notes,omitempty"`

	// For history
	Attempts []ExecutionAttempt `json:"attempts,omitempty"`

	// For review/dedupe
	DuplicateGroups []DuplicateGroup `json:"duplicate_groups,omitempty"`

	Message string `json:"message,omitempty"`
}

// DuplicateGroup represents a group of potentially duplicate decisions.
type DuplicateGroup struct {
	Capsules         []CapsuleSummary `json:"capsules"`
	SimilarityReason string           `json:"similarity_reason"`
	SuggestedMerge   string           `json:"suggested_merge,omitempty"`
}

func (s *Server) handleSessionOps(ctx context.Context, req *mcp.CallToolRequest, args SessionOpsArgs) (*mcp.CallToolResult, any, error) {
	if args.SessionID == "" {
		return nil, nil, fmt.Errorf("session_id is required")
	}
	if args.Operation == "" {
		return nil, nil, fmt.Errorf("operation is required (summary, artifacts, history, review, or dedupe)")
	}

	sess, err := session.Get(s.store, args.SessionID)
	if err != nil {
		return nil, nil, fmt.Errorf("session not found: %w", err)
	}

	out := SessionOpsResult{
		SessionID: sess.ID,
		Operation: args.Operation,
	}

	switch args.Operation {
	case "summary":
		_, summRes, err := s.handleSessionSummary(ctx, req, SessionSummaryArgs{SessionID: args.SessionID})
		if err != nil {
			return nil, nil, err
		}
		if sr, ok := summRes.(SessionSummaryResult); ok {
			out.Description = sr.Description
			out.Status = sr.Status
			out.Mode = sr.Mode
			out.Repos = sr.Repos
			out.CreatedAt = sr.CreatedAt
			out.UpdatedAt = sr.UpdatedAt
			out.Decisions = sr.Decisions
			out.Message = sr.Message
		}

	case "artifacts":
		_, artRes, err := s.handleSessionArtifacts(ctx, req, SessionArtifactsArgs{SessionID: args.SessionID})
		if err != nil {
			return nil, nil, err
		}
		if ar, ok := artRes.(SessionArtifactsResult); ok {
			out.ExecutionAttempts = ar.ExecutionAttempts
			out.MilestoneLedger = ar.MilestoneLedger
			out.ExecutionLog = ar.ExecutionLog
			out.VerificationNotes = ar.VerificationNotes
			out.Message = ar.Message
		}

	case "history":
		_, histRes, err := s.handleSessionExecutionHistory(ctx, req, SessionExecutionHistoryArgs{SessionID: args.SessionID})
		if err != nil {
			return nil, nil, err
		}
		if hr, ok := histRes.(SessionExecutionHistoryResult); ok {
			out.ExecutionAttempts = hr.TotalAttempts
			out.Attempts = hr.Attempts
			out.Message = hr.Message
		}

	case "review", "dedupe":
		// Get all capsules for this session
		capsules, err := capsule.List(s.store, capsule.Filter{SessionID: &sess.ID})
		if err != nil {
			return nil, nil, err
		}

		// Find duplicates using similarity check
		seen := make(map[string]bool)
		for i, c1 := range capsules {
			if seen[c1.ID] {
				continue
			}
			var group []capsule.Capsule
			group = append(group, c1)

			for j := i + 1; j < len(capsules); j++ {
				c2 := capsules[j]
				if seen[c2.ID] {
					continue
				}

				// Check similarity
				if simResult := capsule.FastSimilarityCheck([]capsule.Capsule{c1}, c2); simResult != nil && len(simResult.Similar) > 0 {
					group = append(group, c2)
					seen[c2.ID] = true
				}
			}

			if len(group) > 1 {
				var capsuleSummaries []CapsuleSummary
				for _, c := range group {
					capsuleSummaries = append(capsuleSummaries, CapsuleSummary{
						ID:           c.ID,
						SessionID:    c.SessionID,
						Phase:        c.Phase,
						Question:     c.Question,
						Choice:       c.Choice,
						Rationale:    c.Rationale,
						Tags:         c.Tags,
						Status:       string(c.Status),
						Significance: string(c.Significance),
					})
				}
				out.DuplicateGroups = append(out.DuplicateGroups, DuplicateGroup{
					Capsules:         capsuleSummaries,
					SimilarityReason: "Similar question text",
					SuggestedMerge:   fmt.Sprintf("Consider merging into %s", group[0].ID),
				})
				seen[c1.ID] = true
			}
		}

		if args.Operation == "review" {
			// For review, also include all decisions
			for _, c := range capsules {
				out.Decisions = append(out.Decisions, CapsuleSummary{
					ID:           c.ID,
					SessionID:    c.SessionID,
					Phase:        c.Phase,
					Question:     c.Question,
					Choice:       c.Choice,
					Rationale:    c.Rationale,
					Tags:         c.Tags,
					Status:       string(c.Status),
					Significance: string(c.Significance),
				})
			}
			out.Message = fmt.Sprintf("Session has %d decisions. Found %d potential duplicate groups.", len(capsules), len(out.DuplicateGroups))
		} else {
			// For dedupe
			if args.DedupeDryRun {
				out.Message = fmt.Sprintf("DRY RUN: Found %d duplicate groups. No changes made.", len(out.DuplicateGroups))
			} else if len(out.DuplicateGroups) == 0 {
				out.Message = "No duplicates found to merge."
			} else {
				out.Message = fmt.Sprintf("Found %d duplicate groups. Use card_decision_confirm with action='merge_into:<id>' to merge specific duplicates.", len(out.DuplicateGroups))
			}
		}

	default:
		return nil, nil, fmt.Errorf("invalid operation: %s (must be summary, artifacts, history, review, or dedupe)", args.Operation)
	}

	return nil, out, nil
}

// CapsuleOpsArgs defines input for card_capsule_ops.
type CapsuleOpsArgs struct {
	ID            string `json:"id" jsonschema:"The capsule ID to operate on"`
	Operation     string `json:"operation" jsonschema:"'show', 'chain', 'invalidate', or 'graph'"`
	Reason        string `json:"reason,omitempty" jsonschema:"For invalidate: why this decision is being invalidated"`
	Learned       string `json:"learned,omitempty" jsonschema:"For invalidate: what was learned"`
	SupersededBy  string `json:"superseded_by,omitempty" jsonschema:"For invalidate: ID of replacement decision"`
	UserConfirmed bool   `json:"user_confirmed,omitempty" jsonschema:"For invalidate: REQUIRED - must be true after getting user permission"`
	Depth         int    `json:"depth,omitempty" jsonschema:"For graph: how many levels to traverse (default 2)"`
	Direction     string `json:"direction,omitempty" jsonschema:"For graph: 'up', 'down', or 'both' (default 'both')"`
}

// CapsuleOpsResult is the output of card_capsule_ops.
type CapsuleOpsResult struct {
	Operation string `json:"operation"`

	// For show
	Capsule *CapsuleDetail `json:"capsule,omitempty"`

	// For chain
	Current      *CapsuleSummary  `json:"current,omitempty"`
	Supersedes   []CapsuleSummary `json:"supersedes,omitempty"`
	SupersededBy *CapsuleSummary  `json:"superseded_by,omitempty"`
	ChainSummary string           `json:"chain_summary,omitempty"`

	// For invalidate
	PreviousStatus string `json:"previous_status,omitempty"`
	NewStatus      string `json:"new_status,omitempty"`

	// For graph
	Graph *DependencyGraph `json:"graph,omitempty"`

	Message string `json:"message,omitempty"`
}

// DependencyGraph represents the decision dependency graph.
type DependencyGraph struct {
	Root     CapsuleSummary   `json:"root"`
	Enables  []CapsuleSummary `json:"enables,omitempty"`
	EnabledBy []CapsuleSummary `json:"enabled_by,omitempty"`
	Constrains []CapsuleSummary `json:"constrains,omitempty"`
	ASCII    string           `json:"ascii,omitempty"`
}

func (s *Server) handleCapsuleOps(ctx context.Context, req *mcp.CallToolRequest, args CapsuleOpsArgs) (*mcp.CallToolResult, any, error) {
	if args.ID == "" {
		return nil, nil, fmt.Errorf("capsule ID is required")
	}
	if args.Operation == "" {
		return nil, nil, fmt.Errorf("operation is required (show, chain, invalidate, or graph)")
	}

	out := CapsuleOpsResult{Operation: args.Operation}

	switch args.Operation {
	case "show":
		_, showRes, err := s.handleCapsuleShow(ctx, req, CapsuleShowArgs{ID: args.ID})
		if err != nil {
			return nil, nil, err
		}
		if cd, ok := showRes.(CapsuleDetail); ok {
			out.Capsule = &cd
		}

	case "chain":
		_, chainRes, err := s.handleCapsuleChain(ctx, req, CapsuleChainArgs{ID: args.ID})
		if err != nil {
			return nil, nil, err
		}
		if cr, ok := chainRes.(CapsuleChainResult); ok {
			out.Current = &cr.Capsule
			out.Supersedes = cr.Supersedes
			out.SupersededBy = cr.SupersededBy
			out.ChainSummary = cr.ChainSummary
		}

	case "invalidate":
		_, invRes, err := s.handleInvalidate(ctx, req, InvalidateArgs{
			ID:            args.ID,
			Reason:        args.Reason,
			Learned:       args.Learned,
			SupersededBy:  args.SupersededBy,
			UserConfirmed: args.UserConfirmed,
		})
		if err != nil {
			return nil, nil, err
		}
		if ir, ok := invRes.(InvalidateResult); ok {
			out.PreviousStatus = ir.PreviousStatus
			out.NewStatus = ir.NewStatus
			out.ChainSummary = ir.ChainSummary
			out.Message = ir.Message
		}

	case "graph":
		// Build multi-hop dependency graph using the capsule package
		graphResult, err := capsule.BuildGraph(s.store, args.ID, args.Depth, args.Direction)
		if err != nil {
			return nil, nil, err
		}

		// Convert to MCP result format
		graph := &DependencyGraph{
			Root: CapsuleSummary{
				ID:           graphResult.Root.ID,
				SessionID:    graphResult.Root.SessionID,
				Question:     graphResult.Root.Question,
				Choice:       graphResult.Root.Choice,
				Significance: string(graphResult.Root.Significance),
			},
			ASCII: graphResult.ASCII,
		}

		// Categorize nodes by relationship type
		for _, edge := range graphResult.Edges {
			// Find the target node
			var targetNode *capsule.GraphNode
			for i := range graphResult.Nodes {
				if graphResult.Nodes[i].ID == edge.To {
					targetNode = &graphResult.Nodes[i]
					break
				}
			}
			if targetNode == nil {
				continue
			}

			summary := CapsuleSummary{
				ID:           targetNode.ID,
				Question:     targetNode.Question,
				Choice:       targetNode.Choice,
				Significance: string(targetNode.Significance),
			}

			switch edge.Relationship {
			case "enables":
				if edge.From == args.ID {
					graph.Enables = append(graph.Enables, summary)
				} else {
					graph.EnabledBy = append(graph.EnabledBy, summary)
				}
			case "constrains":
				graph.Constrains = append(graph.Constrains, summary)
			}
		}

		out.Graph = graph
		out.Message = fmt.Sprintf("Dependency graph for %s (depth=%d, direction=%s): %d nodes, %d edges",
			args.ID, graphResult.Depth, graphResult.Direction, len(graphResult.Nodes)+1, len(graphResult.Edges))

	default:
		return nil, nil, fmt.Errorf("invalid operation: %s (must be show, chain, invalidate, or graph)", args.Operation)
	}

	return nil, out, nil
}

// truncateString truncates a string to maxLen with ellipsis.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// SnapshotArgs defines input for card_snapshot.
type SnapshotArgs struct {
	AsOf         string   `json:"as_of" jsonschema:"Point in time: ISO8601, relative ('2 weeks ago'), or 'before:<commit>'"`
	Files        []string `json:"files,omitempty" jsonschema:"Filter to decisions affecting these files"`
	Tags         []string `json:"tags,omitempty" jsonschema:"Filter to decisions with these tags"`
	Significance string   `json:"significance,omitempty" jsonschema:"'architectural', 'implementation', 'context', or 'all'"`
	CompareToNow bool     `json:"compare_to_now,omitempty" jsonschema:"If true, show diff between snapshot and current state"`
}

// SnapshotResult is the output of card_snapshot.
type SnapshotResult struct {
	AsOf            string           `json:"as_of"`
	ResolvedTime    string           `json:"resolved_time"`
	ActiveDecisions []CapsuleSummary `json:"active_decisions"`
	Summary         SnapshotSummary  `json:"summary"`
	Diff            *SnapshotDiff    `json:"diff,omitempty"`
	Message         string           `json:"message,omitempty"`
}

// SnapshotSummary provides counts at the snapshot point.
type SnapshotSummary struct {
	TotalActive       int `json:"total_active"`
	BySignificance    map[string]int `json:"by_significance"`
	ByStatus          map[string]int `json:"by_status"`
}

// SnapshotDiff shows what changed between snapshot and now.
type SnapshotDiff struct {
	CreatedSince     []CapsuleSummary `json:"created_since,omitempty"`
	InvalidatedSince []CapsuleSummary `json:"invalidated_since,omitempty"`
	Summary          string           `json:"summary"`
}

func (s *Server) handleSnapshot(ctx context.Context, req *mcp.CallToolRequest, args SnapshotArgs) (*mcp.CallToolResult, any, error) {
	if args.AsOf == "" {
		return nil, nil, fmt.Errorf("as_of is required (ISO8601, relative like '2 weeks ago', or 'before:<commit>')")
	}

	// Parse the as_of time
	snapshotTime, err := parseAsOf(args.AsOf)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse as_of: %w", err)
	}

	// Get all capsules (including invalidated for historical view)
	allCapsules, err := capsule.List(s.store, capsule.Filter{IncludeInvalidated: true})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list capsules: %w", err)
	}

	// Filter by significance if specified
	var significanceFilter *capsule.Significance
	if args.Significance != "" && args.Significance != "all" {
		sig := capsule.Significance(strings.ToLower(args.Significance))
		significanceFilter = &sig
	}

	out := SnapshotResult{
		AsOf:         args.AsOf,
		ResolvedTime: snapshotTime.Format(time.RFC3339),
		Summary: SnapshotSummary{
			BySignificance: make(map[string]int),
			ByStatus:       make(map[string]int),
		},
	}

	// Determine which capsules were active at the snapshot time
	// Active at time T: created_at <= T AND (invalidated_at IS NULL OR invalidated_at > T)
	for _, c := range allCapsules {
		// Check if capsule existed at snapshot time
		createdAt := c.CreatedAt
		if createdAt.IsZero() {
			createdAt = c.Timestamp // Fallback to timestamp for legacy capsules
		}
		if createdAt.After(snapshotTime) {
			continue // Created after snapshot
		}

		// Check if capsule was still active at snapshot time
		if c.InvalidatedAt != nil && !c.InvalidatedAt.After(snapshotTime) {
			continue // Was invalidated before snapshot
		}

		// Apply file filter
		if len(args.Files) > 0 {
			matched := false
			for _, file := range args.Files {
				if capsule.MatchesTagQuery(c.Tags, "file:"+file) || capsule.MatchesTagQuery(c.Tags, file) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}

		// Apply tag filter
		if len(args.Tags) > 0 {
			matched := false
			for _, tag := range args.Tags {
				if capsule.MatchesTagQuery(c.Tags, tag) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}

		// Apply significance filter
		if significanceFilter != nil && c.Significance != *significanceFilter {
			continue
		}

		// Capsule was active at snapshot time
		out.ActiveDecisions = append(out.ActiveDecisions, CapsuleSummary{
			ID:           c.ID,
			SessionID:    c.SessionID,
			Phase:        c.Phase,
			Question:     c.Question,
			Choice:       c.Choice,
			Rationale:    c.Rationale,
			Tags:         c.Tags,
			Status:       string(c.Status),
			Significance: string(c.Significance),
			Recency:      formatRelativeTime(createdAt),
		})

		// Update summary counts
		out.Summary.TotalActive++
		out.Summary.BySignificance[string(c.Significance)]++
		// For status at snapshot time, we need to determine what status it had then
		statusAtSnapshot := "hypothesis"
		if c.Status == capsule.StatusVerified && (c.InvalidatedAt == nil || c.InvalidatedAt.After(snapshotTime)) {
			statusAtSnapshot = "verified"
		}
		out.Summary.ByStatus[statusAtSnapshot]++
	}

	// Compare to now if requested
	if args.CompareToNow {
		diff := &SnapshotDiff{}
		now := time.Now()

		// Find capsules created since snapshot
		for _, c := range allCapsules {
			createdAt := c.CreatedAt
			if createdAt.IsZero() {
				createdAt = c.Timestamp
			}

			// Created after snapshot
			if createdAt.After(snapshotTime) && c.Status != capsule.StatusInvalidated {
				diff.CreatedSince = append(diff.CreatedSince, CapsuleSummary{
					ID:           c.ID,
					SessionID:    c.SessionID,
					Question:     c.Question,
					Choice:       c.Choice,
					Status:       string(c.Status),
					Significance: string(c.Significance),
					Recency:      formatRelativeTime(createdAt),
				})
			}

			// Invalidated after snapshot
			if c.InvalidatedAt != nil && c.InvalidatedAt.After(snapshotTime) && c.InvalidatedAt.Before(now) {
				diff.InvalidatedSince = append(diff.InvalidatedSince, CapsuleSummary{
					ID:           c.ID,
					SessionID:    c.SessionID,
					Question:     c.Question,
					Choice:       c.Choice,
					Status:       string(c.Status),
					Significance: string(c.Significance),
					Rationale:    fmt.Sprintf("Invalidated: %s", c.InvalidationReason),
				})
			}
		}

		diff.Summary = fmt.Sprintf("Since %s: %d decisions created, %d invalidated",
			snapshotTime.Format("2006-01-02"), len(diff.CreatedSince), len(diff.InvalidatedSince))
		out.Diff = diff
	}

	out.Message = fmt.Sprintf("Snapshot at %s: %d active decisions", snapshotTime.Format("2006-01-02 15:04"), out.Summary.TotalActive)

	return nil, out, nil
}

// parseAsOf parses various time formats for snapshot queries.
func parseAsOf(asOf string) (time.Time, error) {
	// Check for "before:<commit>" format
	if strings.HasPrefix(asOf, "before:") {
		// This would require git access - for now return an error suggesting ISO format
		return time.Time{}, fmt.Errorf("commit-based timestamps not yet implemented; use ISO8601 or relative format")
	}

	// Try ISO8601 first
	if t, err := time.Parse(time.RFC3339, asOf); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02T15:04:05Z", asOf); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", asOf); err == nil {
		return t, nil
	}

	// Parse relative formats
	asOfLower := strings.ToLower(strings.TrimSpace(asOf))

	if asOfLower == "yesterday" {
		return time.Now().AddDate(0, 0, -1), nil
	}

	// Parse "N <unit> ago" format
	var n int
	var unit string
	if _, err := fmt.Sscanf(asOfLower, "%d %s ago", &n, &unit); err == nil {
		unit = strings.TrimSuffix(unit, "s") // normalize: "weeks" -> "week"
		now := time.Now()
		switch unit {
		case "minute":
			return now.Add(-time.Duration(n) * time.Minute), nil
		case "hour":
			return now.Add(-time.Duration(n) * time.Hour), nil
		case "day":
			return now.AddDate(0, 0, -n), nil
		case "week":
			return now.AddDate(0, 0, -n*7), nil
		case "month":
			return now.AddDate(0, -n, 0), nil
		case "year":
			return now.AddDate(-n, 0, 0), nil
		}
	}

	return time.Time{}, fmt.Errorf("unrecognized time format: %s (use ISO8601 like '2026-01-15' or relative like '2 weeks ago')", asOf)
}
