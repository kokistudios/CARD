# CARD — Context Artifact Relay for Development

## What This Project Is

CARD is a local Go CLI that captures engineering decisions and surfaces prior context during code changes. It implements two modes:

1. **Ask Mode**: Conversational sessions where Claude has MCP tools to query CARD's memory
2. **Session Mode**: A 7-phase artifact relay where each phase is a separate Claude Code invocation

CARD is **not** a chatbot, not an agent framework, not a SaaS product. It is versioned engineering memory.

## Core Concepts

### Decision Capsules
The sole queryable unit of CARD's memory. Each capsule captures:
- Question (what was being decided)
- Choice (what was picked)
- Alternatives (what was considered)
- Rationale (why this choice)
- Tags (file paths, concepts, domains)
- Status (active by default; invalidated when superseded or proven wrong)

### Artifact Relay
In session mode, each phase runs as a **separate Claude Code invocation** with scoped context. Artifacts produced by one phase are the input to the next. This keeps context bounded and agents fresh.

### MCP Server
CARD includes an MCP server (`internal/mcp/server.go`) that exposes tools for Claude to query engineering memory:

**Context & Query:**
- `card_context` — Unified pre-work context (modes: starting_task, before_edit, reviewing_pr)
- `card_query` — Search decisions, sessions, patterns, hotspots, learnings, tags
- `card_snapshot` — Query decision state at a point in time

**Recording:**
- `card_record` — Record a decision immediately (creates ask session automatically if needed)
- `card_decision` — Record decision with significance tier and optional human confirmation
- `card_decision_confirm` — Confirm or supersede a proposed architectural decision

**Operations:**
- `card_session_ops` — Session operations (summary, artifacts, history, review, dedupe)
- `card_capsule_ops` — Capsule operations (show, chain, invalidate, graph)
- `card_promote_to_session` — Promote ask conversation to full session for implementation

**Phase Management:**
- `card_write_artifact` — Write phase artifacts to the correct location
- `card_phase_complete` — Signal phase completion for automatic advancement
- `card_agent_guidance` — Get guidance on proactive CARD usage

## Architecture

### CLI Commands
```
card init                    # Initialize ~/.card/
card doctor [--fix]          # Check/repair health
card ask [--repo /path]      # Start conversational session with MCP memory

card session start "desc" --repo /path [--repo /path ...]
card session list [--all]
card session status [id]
card session pause/resume/end/abandon/retry [id]

card comcap [id] [--force]   # Capture git commits

card repo add/list/remove

card recall --files <path> [--repo <id>] [--tag <tag>]
card capsule list/show

card export <session-id>     # Export to portable bundle
card import <bundle.card>    # Import from bundle

card config show/set
card clean [--all] [--dry-run]
card completion bash|zsh|fish
```

### Phase Pipeline (Session Mode)
```
INVESTIGATE → PLAN → REVIEW → EXECUTE → VERIFY → SIMPLIFY → RECORD
                                 ↑          │
                                 └──────────┘  (re-execute if needed)
```
- **INVESTIGATE**: Interactive — deep dialogue to capture intent
- **PLAN**: Non-interactive — automated generation of implementation guide
- **REVIEW**: Interactive — developer walks through plan, can amend
- **EXECUTE**: Interactive — implementation with deviation disclosure
- **VERIFY**: Interactive — verification against plan; accept/re-execute/pause
- **SIMPLIFY**: Non-interactive — code refinement
- **RECORD**: Non-interactive — produce milestone ledger

### Session State Machine
```
started → investigating → planning → reviewing → approved → executing → verifying → simplifying → recording → completed
                                                               ↑           │
                                                               └───────────┘ (re-execute)
```
Additional states: `paused`, `abandoned`

**Ask Sessions:** `card ask` can create lightweight "ask sessions" (ModeAsk) when decisions are recorded. These can be promoted to standard sessions via `card_promote_to_session`.

### CARD_HOME Layout
```
~/.card/
  config.yaml
  repos/
    <repo-id>.md                    # Registry (Obsidian-compatible)
  sessions/
    <session-id>/
      session.yaml                  # Metadata, status, execution history
      <session-id>.md               # Obsidian summary (hub node)
      capsules.md                   # All decisions for this session
      milestone_ledger.md           # File manifest, patterns, iteration summary
      proposals.json                # Pending decision proposals awaiting confirmation
      # Ephemeral (cleaned up after completion):
      # investigation_summary.md, implementation_guide.md
      # execution_log.md, verification_notes.md
      # execution_log_v*.md, verification_notes_v*.md (versioned on re-execute)
      changes/
        <repo-id>/                  # Per-repo git tracking
```

### Artifact Lifecycle

CARD artifacts have different lifespans by design:

| Artifact | Lifespan | Purpose |
|----------|----------|---------|
| `session.yaml` | Permanent | Metadata, status, execution history timestamps |
| `capsules.md` | Permanent | All decision capsules — the queryable memory |
| `milestone_ledger.md` | Permanent | File manifest, patterns, decisions summary, rollback commands |
| `<session-id>.md` | Permanent | Obsidian hub node linking to related artifacts |
| `investigation_summary.md` | Ephemeral | Investigation notes — cleaned up after completion |
| `implementation_guide.md` | Ephemeral | The plan — cleaned up after completion |
| `execution_log.md` | Ephemeral | Implementation details — cleaned up after completion |
| `verification_notes.md` | Ephemeral | Verification findings — cleaned up after completion |

**Why ephemeral artifacts are cleaned up:**
- Execution logs are *process artifacts* (HOW work was done)
- Decisions and milestone_ledger are *outcome artifacts* (WHAT was decided and WHY)
- Only outcomes need to persist for future recall
- Keeps `~/.card/` from growing unboundedly

**For MCP tool queries:**
- Completed sessions: `card_session_ops` (operation='artifacts') returns only `milestone_ledger`
- Active sessions: `card_session_ops` returns all artifacts including execution logs
- Use `card_query` (target='decisions') for decisions — they're always available

### Decision Capsule Structure
```yaml
id: <generated>
timestamp: <ISO 8601>
session: <session-id>
repos:
  - <repo-id>
phase: <phase that produced this>
question: <what was being decided>
choice: <what was chosen>
alternatives:
  - <option considered>
rationale: <why this choice>
tags:
  - file:<path>
  - concept:<name>
  - <domain>
source: human | agent
status: invalidated            # Only set when invalidated; empty = active
type: decision | finding
supersedes: <capsule-id>      # If this replaces an older decision
superseded_by: <capsule-id>   # If invalidated and replaced
invalidation_reason: <why>    # If invalidated
learned: <insight>            # What was learned from invalidation
commits:
  - <SHA>
```

### Decision Confirmation Flow

For architectural decisions that need human review, CARD uses a two-step confirmation flow:

1. **Propose**: Agent calls `card_decision` with `require_confirmation: true`
   - Creates a **proposal** stored in `proposals.json` in the session directory
   - Returns similar/conflicting decisions for the agent to present to the user
   - Proposal has a 30-minute TTL

2. **Confirm**: Agent presents the decision to the user, then calls `card_decision_confirm`
   - Actions: `create` (store as new), `supersede` (replace old decisions), `skip` (discard), or `merge_into:<id>` (update existing)
   - On confirmation, the capsule is stored in `capsules.md`
   - The proposal is deleted from `proposals.json`

**Why proposals persist to disk:**
- Each phase runs as a separate Claude Code invocation
- In-memory state is lost when MCP server restarts between phases
- Disk persistence allows proposals from one phase to be confirmed in the next
- `card_context` surfaces pending proposals so agents see unconfirmed decisions

**For implementation decisions** (obvious choices, pattern-following), use `require_confirmation: false` to store immediately without the two-step flow.

## Technology

- **Language**: Go
- **Distribution**: Single binary (brew, scoop, direct download)
- **Storage**: Markdown+frontmatter for artifacts, YAML for metadata/config
- **AI Runtime**: Claude Code CLI (`claude`) or Codex CLI (`codex`)
- **MCP**: Model Context Protocol server for runtime integration
- **No external services**: Everything runs locally

## Module Structure
```
cmd/card/               CLI entry point (Cobra commands)
internal/
  store/                CARD_HOME filesystem, config
  repo/                 Repo registry, git operations, health checks
  session/              Session lifecycle, state machine, modes
  change/               Per-repo change tracking (base/final commits)
  phase/                Phase runner, orchestrator, prompt templates
  artifact/             Markdown+frontmatter parsing, validation
  capsule/              Decision capsule extraction, storage, querying
  recall/               Context assembly (file, repo, tag, git correlation)
  mcp/                  MCP server implementation
  runtime/              Runtime interface + Claude/Codex implementations
  ui/                   Terminal output, colors, prompts
```

## Key Implementation Details

### Repo Identification
Repos are identified by a stable hash of the normalized primary remote URL. The registry maps repo IDs to local paths and metadata.

### Recall Engine
Recall is triggered:
- **Automatically** during `card ask`: MCP tools query capsules as conversation evolves
- **Automatically** during investigation: CARD surfaces relevant capsules as context
- **Manually** via `card recall`: standalone queries by file, tag, or keyword

### Artifact Lifecycle
- Created during phases, stored at session level
- Investigation and plan artifacts cleaned up on session completion
- Milestone ledger and decisions persist permanently
- Execution logs versioned on re-execute (v1, v2, ...), cleaned up on completion

### Export/Import
- Export creates a `.card` bundle (gzipped tarball) with all session data
- Import extracts to CARD_HOME, auto-links repos by matching remote URL

## Development

When working on CARD itself:

- **`card`** — The released version installed via Homebrew/Scoop. Use for normal operation.
- **`card-dev`** — The local development binary. Build with `go build -o card-dev ./cmd/card`.

Both binaries share the same `~/.card` data directory. This lets you test changes against real sessions while keeping the released version as your default.

### MCP Server Auto-Configuration

CARD automatically configures the selected runtime's MCP server on every invocation. The binary name determines the MCP server name:

- Running `card-dev` → registers `card-dev` MCP server, removes `card` MCP server
- Running `card` → registers `card` MCP server, removes `card-dev` MCP server

This prevents confusion about which version's tools the assistant is using. The configuration:
- Delegates to the runtime implementation
- Removes alternates for Claude (card vs card-dev)
- Only prints a message when changes are made

Use `card ask --setup-mcp` to force reconfiguration if needed.

## Guiding Principles
- **Deterministic over fuzzy**: structured recall, not similarity search
- **Artifacts over chat**: durable outputs, not ephemeral conversation
- **Local-first**: no server, no sync, no subscription
- **Repo-agnostic**: CARD points at repos; repos never depend on CARD
- **Decisions are first-class**: the sole queryable unit
- **Push, don't pull**: surface context before mistakes happen
- **Simple over clever**: markdown, YAML, single binary
