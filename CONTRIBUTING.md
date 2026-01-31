# Contributing to CARD

Thanks for your interest in contributing to CARD.

## Development Setup

### Prerequisites

- Go 1.21+
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) (for testing)

### Building

```bash
git clone https://github.com/kokistudios/card.git
cd card
go build ./...
go install ./cmd/card
```

### Testing

```bash
go test ./...
go vet ./...
```

### Running Locally

After `go install`, ensure `~/go/bin` is in your PATH:

```bash
export PATH="$HOME/go/bin:$PATH"
card --version
```

## Project Structure

```
cmd/card/          CLI entry point (Cobra commands)
internal/
  store/           CARD_HOME filesystem operations
  repo/            Repository registry, git operations
  session/         Session lifecycle, state machine
  change/          Per-repo change tracking
  phase/           Phase runner and prompt templates
  artifact/        Markdown+frontmatter parsing
  capsule/         Decision capsule storage and querying
  recall/          Context assembly, recall engine
  mcp/             MCP server for Claude Code integration
  claude/          Claude Code CLI wrapper
  ui/              Terminal output, formatting
```

## Making Changes

### Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Keep functions focused and small
- Prefer clarity over cleverness
- No external dependencies without strong justification

### Commit Messages

Write clear commit messages that explain *why*, not just *what*:

```
feat: add export command for portable session bundles

Sessions can now be exported to .card files for sharing with teammates.
Import auto-links repos by matching remote URLs.
```

### Pull Requests

1. Fork the repo and create a feature branch
2. Make your changes with clear commits
3. Ensure tests pass (`go test ./...`)
4. Open a PR with a clear description of the change

## Design Principles

Before making significant changes, read [PHILOSOPHY.md](PHILOSOPHY.md). Key principles:

- **Decisions are first-class** — Everything exists to produce, store, or query decision capsules
- **Local-first** — No servers, no sync, no subscriptions
- **Simple over clever** — Markdown, YAML, single binary
- **Repo-agnostic** — CARD points at repos; repos never depend on CARD

## What We're Looking For

- Bug fixes with test cases
- Documentation improvements
- Performance optimizations
- New recall/query capabilities
- Better phase prompts
- Integration improvements

## What Likely Won't Be Accepted

- Features requiring external services or accounts
- Complex plugin/extension systems
- Changes that make repos depend on CARD
- Significant new dependencies

## Questions?

Open an issue for questions about contributing or the codebase.
