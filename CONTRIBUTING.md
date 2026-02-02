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
go build -o ~/bin/card-dev ./cmd/card
```

This builds the development binary as `card-dev` directly to `~/bin/`, making it available system-wide. Make sure `~/bin` is in your PATH:

```bash
mkdir -p ~/bin
echo 'export PATH="$HOME/bin:$PATH"' >> ~/.zshrc  # or ~/.bashrc
source ~/.zshrc
```

This avoids conflicts with the released version installed via Homebrew.

#### Windows

```powershell
git clone https://github.com/kokistudios/card.git
cd card
go build -o $env:USERPROFILE\bin\card-dev.exe ./cmd/card
```

Add `%USERPROFILE%\bin` to your PATH:

```powershell
# Create bin directory if needed
New-Item -ItemType Directory -Force -Path "$env:USERPROFILE\bin"

# Add to PATH permanently (requires new terminal to take effect)
[Environment]::SetEnvironmentVariable("Path", "$env:USERPROFILE\bin;" + [Environment]::GetEnvironmentVariable("Path", "User"), "User")
```

This avoids conflicts with the released version installed via Scoop.

### Testing

```bash
go test ./...
go vet ./...
```

### Running Locally

Use `card-dev` for development and testing:

```bash
card-dev --version
card-dev init
card-dev ask --repo /path/to/your/repo
```

On Windows, use `card-dev.exe` or just `card-dev` (PowerShell resolves it).

If you also have the released version installed (Homebrew on macOS/Linux, Scoop on Windows), it remains available as `card` — this lets you test against both versions.

### MCP Server Auto-Configuration

CARD automatically configures Claude Code's MCP integration on every invocation. Importantly:

- **Running `card-dev`** registers the `card-dev` MCP server and **removes** the `card` MCP server
- **Running `card`** registers the `card` MCP server and **removes** the `card-dev` MCP server

This ensures you're always using the MCP server that matches the binary you're running, preventing confusion about which version's tools Claude is using.

Both binaries share the same `~/.card` data directory, so your sessions, capsules, and repos are accessible from either version. Only the MCP server registration switches.

To force MCP reconfiguration (e.g., if paths changed):

```bash
card-dev ask --setup-mcp
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
