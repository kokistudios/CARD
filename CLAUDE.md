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
- Status (hypothesis → verified → invalidated)

### Artifact Relay
In session mode, each phase runs as a **separate Claude Code invocation** with scoped context. Artifacts produced by one phase are the input to the next. This keeps context bounded and agents fresh.

### MCP Server
CARD includes an MCP server (`internal/mcp/server.go`) that exposes tools for Claude to query engineering memory:
- `card_recall` — Search decisions by files, tags, keywords
- `card_file_context` — Get decisions related to specific files
- `card_preflight` — Pre-flight briefing before implementation
- `card_record` — Record a decision mid-session
- `card_quickfix_start` — Create a quickfix session from discovery
- `card_invalidate` — Mark a decision as invalidated with reasoning
- Plus: `card_sessions`, `card_capsule_show`, `card_hotspots`, `card_patterns`, etc.

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
Additional states: `paused`, `abandoned`, `quickfix_executing` (for quickfix mode)

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
      # Ephemeral (cleaned up after completion):
      # investigation_summary.md, implementation_guide.md
      # execution_log.md, verification_notes.md
      # execution_log_v*.md, verification_notes_v*.md (versioned on re-execute)
      changes/
        <repo-id>/                  # Per-repo git tracking
```

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
status: hypothesis | verified | invalidated
type: decision | finding
supersedes: <capsule-id>      # If this replaces an older decision
superseded_by: <capsule-id>   # If invalidated and replaced
invalidation_reason: <why>    # If invalidated
learned: <insight>            # What was learned from invalidation
commits:
  - <SHA>
```

## Technology

- **Language**: Go
- **Distribution**: Single binary (brew, scoop, direct download)
- **Storage**: Markdown+frontmatter for artifacts, YAML for metadata/config
- **AI Runtime**: Claude Code CLI (`claude`) — required dependency
- **MCP**: Model Context Protocol server for Claude integration
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
  claude/               Claude Code CLI wrapper
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

## Guiding Principles
- **Deterministic over fuzzy**: structured recall, not similarity search
- **Artifacts over chat**: durable outputs, not ephemeral conversation
- **Local-first**: no server, no sync, no subscription
- **Repo-agnostic**: CARD points at repos; repos never depend on CARD
- **Decisions are first-class**: the sole queryable unit
- **Push, don't pull**: surface context before mistakes happen
- **Simple over clever**: markdown, YAML, single binary
