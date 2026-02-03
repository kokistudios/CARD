# CARD — Context Artifact Relay Development

Engineering memory for code changes. CARD captures decisions, surfaces prior context, and preserves institutional knowledge across your codebase.

## What CARD Does

When you work on code, you make decisions: *why* this approach over that one, *what* alternatives you considered, *how* requirements shaped the implementation. Those decisions live in your head, maybe in a PR description, then vanish.

CARD captures them. Every coding session produces **decision capsules**: structured records of what was decided, what was considered, and why. When you return to the same code later, CARD recalls the relevant decisions automatically.

```
$ card ask
> What decisions have been made about the auth system?

Found 4 verified decisions for authentication:
1. Use guards at controller level, not service level (security boundary clarity)
2. JWT tokens expire after 1 hour (balance between security and UX)
3. Refresh tokens stored in httpOnly cookies (XSS protection)
4. Rate limit login attempts to 5/minute per IP (brute force protection)

Session: 20260115-auth-refactor | Full context: ~/.card/sessions/.../milestone_ledger.md
```

## Installation

### Homebrew (macOS/Linux)

```bash
brew install kokistudios/tap/card
```

### Scoop (Windows)

```powershell
scoop bucket add kokistudios https://github.com/kokistudios/scoop-bucket
scoop install card
```

### From Source

Requires Go 1.21+

```bash
git clone https://github.com/kokistudios/card.git
cd card
go build -o card-dev ./cmd/card
./card-dev --version
```

This builds the development binary as `card-dev` to avoid conflicts with the released version. See [CONTRIBUTING.md](CONTRIBUTING.md) for the full development setup.

### Requirements

Install at least one supported runtime:
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) (`claude`)
- Codex CLI (`codex`)

## Quick Start

```bash
# Initialize CARD
card init
card repo add /path/to/your/repo

# Start using CARD
card ask                          # Interactive conversation with memory
card session start "feature X"    # Full artifact relay session
```

CARD automatically configures the selected runtime's MCP integration on first run. No manual setup required.

## Two Ways to Use CARD

### 1. Ask Mode — Conversational Memory

`card ask` starts your configured runtime with full access to CARD's engineering memory. The assistant proactively surfaces relevant decisions as you work:

```bash
card ask
```

No setup, no specifying files upfront. Just start working and the assistant pulls relevant context automatically via CARD's MCP server. This is the fastest way to benefit from CARD. Decisions from past sessions inform your current work.

**Recording decisions:** During `card ask`, the assistant can record decisions as you discuss them. This creates a lightweight "ask session" automatically — no manual setup needed. If the conversation evolves into work that needs tracked implementation, the assistant can promote the ask session to a full session with all decisions preserved.

### 2. Session Mode — Full Artifact Relay

For structured, multi-phase work, CARD runs a 7-phase pipeline:

```
INVESTIGATE → PLAN → REVIEW → EXECUTE → VERIFY → SIMPLIFY → RECORD
```

Each phase is a **separate runtime session** with scoped context. Artifacts from one phase feed the next. This keeps context bounded and agents fresh.

```bash
card session start "implement feature X" --repo /path/to/repo
```

**Phases:**
1. **Investigate** — Deep dialogue to understand scope, edge cases, trade-offs
2. **Plan** — Automated generation of implementation guide
3. **Review** — Walk through the plan, amend as needed
4. **Execute** — Implement with mandatory deviation disclosure
5. **Verify** — Independent verification against the plan
6. **Simplify** — Refine code for clarity
7. **Record** — Produce milestone ledger summarizing the work

At each interactive phase, the assistant signals completion via the `card_phase_complete` MCP tool, and CARD automatically advances to the next phase.

After **Verify**, CARD asks: *Accept / Re-execute / Pause?* If verification found issues, choose re-execute to loop back to the Execute phase with the verification feedback. Execution logs are versioned (v1, v2, ...) so you can see what changed between attempts.

## Commands

| Command | Description |
|---------|-------------|
| **Core** | |
| `card init` | Initialize CARD (`~/.card/`) |
| `card doctor [--fix]` | Check and repair system health |
| `card ask [--repo /path]` | Start conversational session with memory |
| **Sessions** | |
| `card session start "desc" --repo /path` | Start artifact relay session |
| `card session list [--all]` | List sessions |
| `card session status [id]` | Show session details |
| `card session pause [id]` | Pause a session |
| `card session resume [id]` | Resume a paused session |
| `card session end [id]` | Complete a session |
| `card session abandon [id]` | Abandon a session |
| `card session retry [id]` | Retry the current phase |
| **Memory** | |
| `card recall --files src/auth.ts` | Recall decisions by file |
| `card recall --tag auth` | Search decisions by tag |
| `card capsule list` | List decision capsules |
| `card capsule show <id>` | Show capsule details |
| **Repos** | |
| `card repo add /path` | Register a repo |
| `card repo list` | List registered repos |
| `card repo remove <id>` | Unregister a repo |
| **Sharing** | |
| `card export <session-id>` | Export session to portable bundle |
| `card import <bundle.card>` | Import session from bundle |
| `card comcap [id]` | Capture git commits for a session |
| **Config** | |
| `card config show` | Show configuration |
| `card config set <key> <value>` | Set a config value |
| `card runtime use <claude\|codex>` | Switch active runtime and set default path |
| `card clean [--all] [--dry-run]` | Remove old session data |

## Decision Capsules

Every decision gets captured as a **capsule**:

```yaml
id: 20260115-auth-guard-abc123
session: 20260115-auth-refactor
question: Where should authorization checks live?
choice: Controller-level guards, not service-level
alternatives:
  - Service-level checks
  - Middleware for all routes
rationale: Controllers are the security boundary; services are reusable across contexts
tags:
  - file:src/auth/guard.ts
  - authentication
  - architecture
```

Capsules are **active by default**. When a decision is superseded or proven wrong, it becomes **invalidated** (with recorded learning about what changed).

## Automatic Recall

When you start working, CARD searches for prior decisions touching the same files and surfaces them automatically. This is the core value: past intent informs future work without re-discovery.

In `card ask` mode, Claude has MCP tools to query and update CARD's memory:

**Context tools:**
- `card_context` — Get unified context before working (modes: starting_task, before_edit, reviewing_pr)
- `card_query` — Unified search across decisions, sessions, patterns, hotspots, and learnings
- `card_snapshot` — Query decision state at a point in time (for archaeology queries)

**Recording tools:**
- `card_record` — Capture a decision immediately (creates ask session automatically)
- `card_decision` — Record decision with significance tier and optional human confirmation
- `card_decision_confirm` — Confirm or supersede a proposed architectural decision

**Operations:**
- `card_session_ops` — Session operations (summary, artifacts, history, review, dedupe)
- `card_capsule_ops` — Capsule operations (show, chain, invalidate, graph)
- `card_promote_to_session` — Promote ask conversation to full session for implementation

## Multi-Repo Sessions

Work that spans multiple repositories (API + frontend, infra + service) runs in a single session:

```bash
card session start "add feature X" \
  --repo /path/to/api \
  --repo /path/to/frontend
```

All phases see every repo. Decision capsules reference all involved repos, creating cross-repo links in the knowledge graph.

## Sharing Sessions

Export a session to share with teammates:

```bash
card export 20260130-auth-refactor
# Creates: 20260130-auth-refactor.card
```

Import on another machine:

```bash
card import auth-session.card
# Automatically links to matching repos by remote URL
```

## Data Storage

All data lives in `CARD_HOME` (default `~/.card/`). CARD never modifies your repos.

```
~/.card/
  config.yaml
  repos/<repo-id>.md
  sessions/<session-id>/
    session.yaml              # Metadata, status, execution history
    capsules.md               # All decisions for this session
    milestone_ledger.md       # File manifest, patterns, summary
    changes/<repo-id>/        # Git tracking (base/final commits)
```

The directory is Obsidian-compatible — open it as a vault to see the graph of repos → sessions → decisions.

### Artifact Lifecycle

Some artifacts are **ephemeral** and cleaned up after session completion:
- `execution_log.md`, `verification_notes.md` — process artifacts
- `investigation_summary.md`, `implementation_guide.md` — planning artifacts

These serve their purpose during the session, but only **outcomes** persist:
- `capsules.md` — decisions (the queryable memory)
- `milestone_ledger.md` — file manifest, patterns, rollback commands

This keeps `~/.card/` bounded while preserving what matters for future recall.

## Configuration

```yaml
# ~/.card/config.yaml
runtime:
  type: claude            # "claude" (default) or "codex"
  path: /usr/local/bin/claude  # Optional override
claude:
  path: claude              # Path to Claude CLI
session:
  auto_continue_simplify: true
  auto_continue_record: true
recall:
  max_context_blocks: 10
  max_context_chars: 8000
```

Override values: `card config set recall.max_context_blocks 20`
Switch runtime quickly: `card runtime use codex`

### Runtime Selection

CARD can run against either Claude Code or Codex. Use the helper command to switch both `runtime.type` and `runtime.path` in one step:

```bash
card runtime use claude
card runtime use codex
```

You can override the binary location with `--path` or skip auto-detection with `--auto=false`.

## Project Structure

```
cmd/card/          CLI entry point
internal/
  store/           CARD_HOME filesystem, config
  repo/            Repo registry, git operations
  session/         Session lifecycle, state machine
  change/          Per-repo change tracking
  phase/           Phase runner, orchestrator, templates
  artifact/        Markdown+frontmatter parsing
  capsule/         Decision capsule extraction, storage
  recall/          Context assembly, recall engine
  mcp/             MCP server for Claude Code integration
  runtime/         Runtime interface + Claude/Codex implementations
  ui/              Terminal output, colors, prompts
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and guidelines.

## Philosophy

See [PHILOSOPHY.md](PHILOSOPHY.md) for the design principles behind CARD.

## License

MIT — see [LICENSE](LICENSE)
