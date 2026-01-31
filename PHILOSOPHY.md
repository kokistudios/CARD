# CARD Philosophy

The design principles behind CARD.

## The Problem

Engineering knowledge vanishes. You make a decision (why this database schema, why that API pattern, why not the obvious approach) and that reasoning lives in your head, briefly in a PR description, then it's gone.

Six months later, someone (maybe you) looks at the code and wonders: *Why is it like this?* They either:
1. Assume it's wrong and "fix" it, reintroducing the bug the original decision avoided
2. Assume it's sacred and work around it, accumulating cruft
3. Spend hours re-discovering the original constraints

All three waste time. All three happen constantly.

## The Solution

Capture decisions at the moment they're made, in a structured format that's queryable later. Not documentation (nobody reads it). Not comments (they rot). **Decision capsules**: first-class records of what was decided, what was considered, and why.

Then, when anyone touches that code again, surface the relevant decisions automatically. Don't wait for them to ask. Push the context before they make mistakes.

## Core Principles

### 1. Deterministic Over Fuzzy

CARD's recall is structured, not vibes. When you query for decisions about a file, you get decisions about that file. Not "maybe related" suggestions from a similarity search.

Tags are explicit: `file:src/auth.ts`, `concept:authorization`, `domain:payments`. Queries are precise. Results are reproducible.

### 2. Artifacts Over Chat

Chat is ephemeral. Artifacts persist.

Each phase of CARD produces a durable markdown artifact. The investigation produces a summary. The plan produces a guide. The execution produces a log. These artifacts are the baton passed between phases, not conversation history or token counts or context window limits.

Artifacts are readable by humans, versionable by git, linkable from anywhere.

### 3. Local-First

CARD runs on your machine. Your decisions stay in `~/.card/`. No server, no sync service, no subscription.

Think of it like a [Pensieve](https://www.youtube.com/watch?v=KPgZJzQF1Yg): your engineering memories live in your own basin, not in someone else's cloud. When you want to share a memory, you extract it deliberately. `card export` bottles a session into a portable `.card` file that you can hand to a teammate. They `card import` it into their own Pensieve. The memory transfers, but the default is private.

Local-only means you're responsible for your own backups. The `~/.card/` directory is just files. Sync it with Obsidian, version it with git, mirror it to cloud storage, whatever suits your workflow. CARD doesn't care how you protect your memories; it just gives you the tools to extract and share them on your terms.

The only network dependency is Claude Code's API access, and that's Claude's concern, not CARD's.

### 4. Repo-Agnostic

CARD never modifies your repositories. It points at repos; repos never depend on CARD.

You can use CARD on one repo and not another. You can stop using CARD and nothing breaks. There's no `.card/` directory in your repo, no hooks to install, no CI integration required.

### 5. Decisions Are First-Class

Decision capsules are the **sole queryable unit** of CARD's memory. Not sessions, not artifacts, not files. Decisions.

A decision has:
- A **question** (what was being decided)
- A **choice** (what was picked)
- **Alternatives** (what was considered)
- A **rationale** (why this choice)
- **Tags** (files, concepts, domains it relates to)
- A **status** (hypothesis, verified, invalidated)

Everything else in CARD exists to produce, store, or query decisions.

### 6. Push, Don't Pull

The dream: CARD tells you what you need to know **before** you make mistakes. Not "search for context if you remember to." Not "read the docs if they exist."

When you touch `auth.ts`, CARD surfaces the 3 verified decisions about authentication without you asking. When you propose a change that conflicts with a prior decision, CARD flags it before you implement.

Context should flow toward the developer, not wait to be pulled.

### 7. Phases Are Boundaries

Each phase of the artifact relay is a **separate Claude Code session**. This is intentional:

- **Fresh context**: No accumulated confusion from earlier conversation
- **Scoped permissions**: Execute phase can write code; investigate phase cannot
- **Clear contracts**: Each phase has defined inputs (artifacts) and outputs (artifacts)
- **Recoverable failures**: If a phase fails, you retry that phase, not the whole session

Mega-sessions seem convenient but degrade. Bounded phases stay sharp.

### 8. Ephemeral Artifacts, Durable Decisions

Investigation summaries and execution logs are working documents. They're useful during the session, then they're noise.

After a session completes, CARD cleans up the verbose artifacts. What remains:
- **Decisions** (the queryable memory)
- **Milestone ledger** (file manifest, patterns, iteration summary)
- **Session metadata** (for history)

The value gets extracted; the bulk gets discarded.

### 9. Human Over Agent

CARD captures both human decisions and agent recommendations, but labels them differently.

A decision tagged `source:human` means the developer explicitly chose it. A decision tagged `source:agent` means Claude recommended it and the developer accepted it.

When decisions conflict, human decisions take precedence. When recalling context, human decisions get higher weight.

### 10. Simple Over Clever

CARD is written in Go, distributed as a single binary, stores data in markdown and YAML. No database, no ORM, no framework, no clever abstractions.

You can read the session data with `cat`. You can search it with `grep`. You can version it with `git`. You can open it in Obsidian.

Simplicity survives. Cleverness requires maintenance.

## What CARD Is Not

**Not a chatbot.** CARD orchestrates Claude Code; it doesn't replace it.

**Not an agent framework.** CARD doesn't have plugins, tools, or custom actions. It runs Claude Code and captures what comes out.

**Not a SaaS product.** No account, no subscription, no usage limits.

**Not a replacement for documentation.** CARD captures decisions, not explanations. You still need READMEs, ADRs, and architecture diagrams.

**Not magic.** CARD can only recall what was captured. If you skip the investigation phase, you skip the decision capture. Garbage in, garbage out.

## The Long Game

Codebases outlive teams. Teams outlive individuals. Decisions outlive everyone.

The goal is a codebase where any developer (new hire, returning veteran, future maintainer) can understand not just *what* the code does, but *why* it's shaped that way. Where "why did we do it this way?" has an answer that takes seconds to find.

CARD is one tool toward that goal. It won't solve organizational dysfunction, won't replace code review, won't make bad decisions good. But six months from now, when someone asks "why is it like this?" — the answer will be there.
