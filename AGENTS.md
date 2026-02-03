# AGENTS.md

This file captures the key project context and operational notes for agentic tools.

## Overview

CARD is a local-first CLI that captures engineering intent in structured artifacts and makes them queryable via MCP. It runs entirely on your machine and stores data in `~/.card/`.

## Runtime Selection

CARD can run against Claude Code or Codex via the runtime interface. The active runtime is configured in `~/.card/config.yaml`:

```yaml
runtime:
  type: claude            # "claude" (default) or "codex"
  path: /usr/local/bin/claude  # Optional override
```

Use the helper command to switch both values at once:

```bash
card runtime use claude
card runtime use codex
```

## MCP Auto-Configuration

CARD automatically configures MCP for the selected runtime on each invocation. The MCP server name matches the binary name (e.g., `card` vs `card-dev`) to avoid confusion.

## Key Commands

```bash
card ask
card session start "feature X" --repo /path/to/repo
card doctor --fix
```

## Tests

```bash
go test ./...
```

## Project Structure (high level)

```
cmd/card/          CLI entry point
internal/runtime/  Runtime interface + Claude/Codex implementations
internal/phase/    Phase runner and orchestrator
internal/store/    CARD_HOME config and storage
internal/mcp/      MCP server implementation
```
