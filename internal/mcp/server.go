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

	"github.com/kokistudios/card/internal/capsule"
	"github.com/kokistudios/card/internal/change"
	"github.com/kokistudios/card/internal/recall"
	"github.com/kokistudios/card/internal/repo"
	"github.com/kokistudios/card/internal/session"
	"github.com/kokistudios/card/internal/store"
)

// Server wraps the MCP server with CARD's store.
type Server struct {
	store  *store.Store
	server *mcp.Server
}

// NewServer creates a new CARD MCP server.
func NewServer(st *store.Store, version string) *Server {
	s := &Server{store: st}

	impl := &mcp.Implementation{
		Name:    "card",
		Version: version,
	}

	s.server = mcp.NewServer(impl, nil)
	s.registerTools()

	return s
}

// Run starts the MCP server on stdio.
func (s *Server) Run(ctx context.Context) error {
	return s.server.Run(ctx, &mcp.StdioTransport{})
}

// registerTools adds all CARD tools to the MCP server.
func (s *Server) registerTools() {
	// card_recall - query CARD's memory for relevant context
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "card_recall",
		Description: "Search CARD's engineering memory for prior decisions. START HERE for discovery - returns compact summaries (id, question, choice, rationale, tags). Use card_capsule_show to drill down on specific decisions, or card_session_artifacts for full session context. Searches across all repos unless repo is specified. SMART DEFAULT: Call with no params to get the 15 most recent decisions. PROACTIVE USE: Call this at the start of any coding task to understand prior context.",
	}, s.handleRecall)

	// card_capsule_show - get full details of a specific capsule
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "card_capsule_show",
		Description: "Get full details of a decision capsule by ID. Use after card_recall to drill down on a specific decision - includes alternatives considered, full rationale, linked commits.",
	}, s.handleCapsuleShow)

	// card_sessions - list sessions for a repo
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "card_sessions",
		Description: "List CARD sessions for a repository. Sessions represent units of work (features, bug fixes, refactors) that may span multiple commits. Use this to understand the history of work on a repo or find sessions to explore further.",
	}, s.handleSessions)

	// card_session_artifacts - get artifacts from a session
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "card_session_artifacts",
		Description: "Get FULL session artifacts (milestone_ledger, execution_log, verification_notes) - can be 500+ lines. Returns latest execution_log (execution_attempts field indicates total attempts). verification_notes shows issues from last verify phase if re-execution was requested. Use this for deep dives into implementation details, SQL queries, verification checklists. For compact decision summaries, use card_recall instead.",
	}, s.handleSessionArtifacts)

	// card_session_execution_history - get all versioned execution logs
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "card_session_execution_history",
		Description: "Get FULL execution history for a session - ALL versioned execution logs and verification notes across re-execution attempts. Use this when you need to understand the evolution of implementation across multiple attempts, what was tried and why it failed, or detailed context on how a feature was built iteratively. Returns array of attempts with execution_log and verification_notes for each. WARNING: Can be very large (1000+ lines) for sessions with many re-executions. NOTE: For completed sessions, execution logs are cleaned up — use card_recall for decisions and milestone_ledger for file manifest and patterns.",
	}, s.handleSessionExecutionHistory)

	// card_tags_list - discover available tags
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "card_tags_list",
		Description: "List all unique tags from CARD's decision capsules. Use this to discover what tags exist before searching with card_recall. Returns file paths, concepts, and domain tags.",
	}, s.handleTagsList)

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

	// card_file_context - get decisions related to specific files
	mcp.AddTool(s.server, &mcp.Tool{
		Name: "card_file_context",
		Description: "Get all CARD decisions related to specific files. This is the single most useful query " +
			"before touching a file - returns capsule count, status summary, and relevant decisions. " +
			"PROACTIVE USE: Call this BEFORE reading or editing any file to surface prior decisions. " +
			"Don't wait for the user to ask - push relevant context proactively.",
	}, s.handleFileContext)

	// card_capsule_chain - navigate supersession relationships
	mcp.AddTool(s.server, &mcp.Tool{
		Name: "card_capsule_chain",
		Description: "Navigate the supersession chain for a capsule. Shows what decisions this capsule " +
			"supersedes (older, invalidated) and what supersedes it (newer, if invalidated). " +
			"Use this to understand the evolution of a decision over time.",
	}, s.handleCapsuleChain)

	// card_invalidate - mark a decision as invalidated with reasoning
	mcp.AddTool(s.server, &mcp.Tool{
		Name: "card_invalidate",
		Description: "Mark a decision capsule as invalidated with full reasoning. " +
			"USE THIS when a prior decision has been proven wrong, requirements changed, or a better approach emerged. " +
			"The 'reason' captures WHY it was invalidated; the 'learned' field captures WHAT was learned " +
			"(e.g., 'synchronous APIs don't scale for our volume'). This creates a learnings database — " +
			"query invalidated decisions later to see what approaches failed and why. " +
			"Use 'superseded_by' to link to the replacement decision if one exists. " +
			"BEFORE CALLING: You MUST (1) use card_capsule_show to review the decision's full context, " +
			"(2) explain to the user why this decision should be invalidated, " +
			"(3) ask for explicit permission, (4) only then call with user_confirmed=true and a non-empty reason.",
	}, s.handleInvalidate)

	// card_session_summary - lightweight session summary
	mcp.AddTool(s.server, &mcp.Tool{
		Name: "card_session_summary",
		Description: "Get a lightweight summary of a session - just the description, status, and decision list. " +
			"Use this for quick 'catch me up' queries. For full artifacts (file manifest, execution logs), " +
			"use card_session_artifacts instead.",
	}, s.handleSessionSummary)

	// card_hotspots - find areas with most decisions
	mcp.AddTool(s.server, &mcp.Tool{
		Name: "card_hotspots",
		Description: "Find files and areas with the most CARD decisions. Use this to understand where " +
			"context is rich vs sparse in the codebase. Returns file hotspots (most decisions) and " +
			"concept hotspots (most discussed topics).",
	}, s.handleHotspots)

	// card_patterns - extract patterns from sessions
	mcp.AddTool(s.server, &mcp.Tool{
		Name: "card_patterns",
		Description: "Get all implementation patterns introduced across CARD sessions. Returns patterns " +
			"with descriptions and the sessions where they were introduced. Use this for quick reference " +
			"on 'how do we do X in this codebase'.",
	}, s.handlePatterns)

	// card_preflight - pre-work briefing
	mcp.AddTool(s.server, &mcp.Tool{
		Name: "card_preflight",
		Description: "Get a pre-flight briefing before working on files. Combines file context, relevant " +
			"decisions, and patterns into actionable guidance. PROACTIVE USE: Call this BEFORE any " +
			"implementation work. Don't wait for users to ask - surface context before they make mistakes. " +
			"Pass your intent (e.g., 'adding rate limiting') for better recommendations.",
	}, s.handlePreflight)

	// card_agent_guidance - get proactive usage instructions
	mcp.AddTool(s.server, &mcp.Tool{
		Name: "card_agent_guidance",
		Description: "Get guidance on proactive CARD usage. Call this once at the start of a session to " +
			"understand how to use CARD tools effectively. Returns best practices for surfacing context " +
			"and capturing decisions.",
	}, s.handleAgentGuidance)
}

// RecallArgs defines the input for card_recall.
type RecallArgs struct {
	Files            []string `json:"files,omitempty" jsonschema:"File paths to search for related decisions (e.g. src/auth.ts)"`
	Tags             []string `json:"tags,omitempty" jsonschema:"Tags or keywords to search (e.g. authentication, database, api)"`
	Query            string   `json:"query,omitempty" jsonschema:"Search capsule content: the question asked, choice made, and rationale given. Example: 'TypeORM' finds 'Why TypeORM over raw SQL?' Use tags param for concept search like 'authentication'."`
	Repo             string   `json:"repo,omitempty" jsonschema:"Repository ID to scope the search (optional - searches all repos if not specified)"`
	IncludeEvolution bool     `json:"include_evolution,omitempty" jsonschema:"If true, show all phases of each decision instead of just the latest (default: false)"`
	Status           string   `json:"status,omitempty" jsonschema:"Filter by capsule status: 'verified', 'hypothesis', or 'invalidated' (optional - returns all if not specified)"`
	Format           string   `json:"format,omitempty" jsonschema:"Output format: 'full' (default) or 'compact' (IDs and choices only)"`
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

	isCompact := args.Format == "compact"

	for _, sc := range result.Capsules {
		// Apply status filter
		if statusFilter != nil && sc.Status != *statusFilter {
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
		out.Message = "Tip: If you discover something that needs fixing, use card_quickfix_start to create a recorded quickfix session."
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
	Files []string `json:"files" jsonschema:"File paths to get context for (e.g. ['src/auth/guard.ts'])"`
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
	Files  []string `json:"files" jsonschema:"File paths to get pre-flight briefing for"`
	Intent string   `json:"intent,omitempty" jsonschema:"What you're planning to do (e.g., 'adding rate limiting', 'refactoring auth')"`
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
