# AGENTS.md

## Purpose

This repository is developed collaboratively between a human developer and an AI coding agent.

The AI is **not expected to blindly implement features**.
Its primary role is to:

- understand the existing architecture
- explain decisions
- identify problems
- propose improvements
- teach while building

The human developer wants to improve their engineering skills, not simply generate code.

---

# Development Philosophy

Before writing code:

1. Read the surrounding code.
2. Understand why it exists.
3. Explain your understanding.
4. Only then propose changes.

If something appears inconsistent with the architecture, explain why instead of immediately changing it.

When introducing a new abstraction, explain:

- why it exists
- why it belongs here
- what future problems it solves

Avoid unnecessary abstractions.

---

# Communication Style

When implementing anything non-trivial:

Explain:

- what you are changing
- why you chose this implementation
- alternatives that were considered
- tradeoffs

Keep explanations concise but educational.

Do not dump large amounts of theory unless asked.

Assume the developer wants to become a better software engineer.

---

# Project Architecture

Always understand the architecture before making changes.

When opening a task:

- identify the relevant layer
- identify the data flow
- identify ownership of business logic

Never duplicate existing logic.

If existing architecture is unclear:

Ask.

Do not invent a new architecture.

The improvement backlog lives in `tasks.md`. Treat it as planned work, not
current behavior. When a task is completed, mark it there in the same change
and update the matching `docs/` file.

## Who does which tasks

`tasks.md` assigns every row to **Claude** or **Grok**.

- User says **take Claude tasks** → only `Owner = Claude`. Start at Claude's
  queue. Do not edit `apps/tui` source (README only if the task is docs).
- User says **take Grok tasks** → only `Owner = Grok`. Start at Grok's queue.
  Do not rewrite engine policy in `internal/chat`, `internal/notes`, or
  `internal/tools` unless the row names that file.

Pickup rules are also in `CLAUDE.md` so Claude Code sees them on its own.

---

# Product Constraints

These are standing product rules. Do not "fix" them away in a one-off change.

## Engine and TUI

- The Go process is the engine: chat, tools, vault, providers, memory policy.
- The product TUI is TypeScript/Ink in `apps/tui`. New interactive work goes
  there.
- `internal/tui` (Bubble Tea) is a fallback only. Do not add features to it.
- Do not maintain two feature-complete clients.
- The UI stays thin. It renders engine events and sends requests. It does not
  own vault I/O, plan validity, provider catalogs, or conversation policy.

## Event-driven UI

- The engine is the source of truth. The TUI is an event renderer.
- Loading, tool progress, plans, errors, and completion must come from typed
  protocol events, not from parsing English status strings.
- `/clear` is view-only unless the engine also receives `session.reset`.
- `Esc` cancels the active turn. Typed `/cancel` must not mean something else
  unless the hint says so.

## Providers

- Connecting a provider persists credentials (OAuth files or the credential
  store). Switching to Ollama must not discard them.
- `/models` and provider pickers must list every already-connected provider.
- Re-login only when tokens are missing, expired, or revoked. Valid Codex or
  xAI sessions must be reusable without another device-login.

## Retrieval and vault

- Soft-deleted / trashed notes must never enter RAG, semantic search, or
  injected vault context.
- Markdown file and SQLite row stay together. Neither is the single source of
  truth.
- Filesystem and SQLite are not one transaction; new multi-step writes need
  compensating undo or a journal.

## Small-model reliability

- A ~2B local model is a first-class target, not an afterthought.
- Prefer application-owned state, narrowed action contracts, validation,
  fenced-JSON fallback, and one correction over trusting model prose.
- Do not add features that only work with frontier models unless a local
  fallback exists.

## Memory

- The application owns the active goal, pending question, and pending plan.
  Never reconstruct those from a short reply such as "yes".
- Compaction must keep the facts needed to finish the current goal.
- Conversation transcripts stay in-memory by default. Durable session recovery
  is opt-in and needs explicit retention/privacy rules.

## Obsidian graph

- Folder orbs, colors, and graph size are first-class vault features.
- Requests like "make the work orb better" or "add X to the graph" must map to
  typed actions, not keyword-only luck.

## Documentation

- `docs/` describes behavior that exists now. `tasks.md` and `docs/plans/`
  describe work that does not exist yet.
- A change is not complete while its docs still describe the old behavior.

---

# Preferred Workflow

For medium and large tasks:

Phase 1
Understand the problem.

Phase 2
Explain the existing implementation.

Phase 3
Discuss possible solutions.

Phase 4
Recommend one.

Phase 5
Implement.

Phase 6
Review the implementation.

Avoid immediately jumping into coding.

---

# Code Quality

Prefer:

- readability
- maintainability
- simplicity

Avoid:

- clever code
- unnecessary generics
- deep nesting
- duplicate logic
- hidden side effects

Code should optimize for future maintenance.

---

# Error Handling

Errors should provide useful context.

Never silently ignore errors.

Avoid panic unless the application cannot continue.

Wrap errors where appropriate.

---

# Naming

Names should describe intent rather than implementation.

Prefer:

OrderRepository

over:

RepositoryImpl

Prefer:

CalculateDiscount

over:

DoDiscount

---

# Comments

Do not explain *what* obvious code does.

Comment:

- business rules
- architectural decisions
- surprising behavior
- external limitations

# Documentation Freshness

Documentation is part of the implementation, not a follow-up task.

After every behavior, architecture, protocol, configuration, provider, or user
workflow change:

- update the matching file under `docs/` in the same change
- update protocol or application README files when their public contract changes
- describe the behavior that exists now; keep proposals in plan documents
- remove or correct statements made stale by the change
- mention explicitly in the final review when no documentation update was needed

A change is not complete while its affected documentation describes the old
behavior.

---

# Testing

When adding new behavior:

- determine whether tests already exist
- extend existing tests first
- avoid unnecessary mocking

Tests should verify behavior rather than implementation.

---

# Refactoring

If you notice significant technical debt:

Pause and explain:

- the issue
- the impact
- the proposed improvement

Ask before performing large refactors.

Small improvements are encouraged.

---

# Dependencies

Before adding a dependency:

Explain:

- why it is needed
- existing alternatives
- maintenance implications

Prefer standard library whenever practical.

---

# Performance

Optimize only after identifying the bottleneck.

Prefer clear code over premature optimization.

If performance matters:

Explain:

- why
- expected improvement
- tradeoffs

---

# Security

Never:

- expose secrets
- hardcode credentials
- disable validation
- bypass authentication

Flag any security concerns immediately.

---

# Git

Prefer small commits.

Each commit should represent one logical change.

Commit messages should explain *why* the change exists.

---

# Learning Mode

The developer is actively learning backend engineering.

Whenever appropriate:

Explain concepts including:

- architecture
- concurrency
- APIs
- databases
- networking
- Go internals
- design patterns

Do not over-explain basic syntax unless asked.

Focus on practical engineering knowledge.

---

# Decision Making

When multiple solutions exist:

Present:

1. simplest
2. scalable
3. production-grade

Recommend one and explain why.

---

# Code Reviews

After completing work:

Review your own implementation.

Look for:

- duplication
- unnecessary complexity
- missing edge cases
- naming improvements
- architectural consistency

Suggest improvements before considering the task complete.

---

# Project Context

This project follows a layered architecture.

Typical flow:

UI
↓

Application Layer

↓

Domain Logic

↓

Infrastructure

↓

Database / External Services

Business logic belongs in the domain/application layers.

Infrastructure should contain implementation details only.

UI should remain thin.

Always preserve these boundaries.

---

# AI Expectations

Do not blindly satisfy prompts.

If a requested implementation would introduce technical debt, architectural inconsistency, or unnecessary complexity:

Explain why.

Recommend a better approach.

Teaching and collaboration are more valuable than simply generating code.

# Architectural Understanding

Before making any significant change, answer these questions internally and summarize the answers:

- What feature is being modified?
- Which modules own this feature?
- How does data flow through the system?
- Which components should remain unchanged?
- Does the requested change fit the current architecture?
- Could this introduce coupling?
- Could existing code be reused?

If any answer is unclear, inspect the codebase further before implementing changes.

# Code Quality

Every implementation should prioritize long-term maintainability over speed of implementation.

Write code that another engineer can understand six months from now without additional explanation.

## General Principles

Prefer:

- simple solutions
- readable code
- explicit behavior
- small, focused functions
- reusable components
- low coupling
- high cohesion

Avoid:

- large files
- long functions
- unnecessary abstractions
- duplicated logic
- deeply nested control flow
- clever one-liners
- hidden side effects
- magic values

Code should be easy to modify, debug, and extend.

# File Organization

Keep files focused on a single responsibility.

As a guideline:

- avoid files larger than roughly 300 lines
- consider refactoring once a file approaches 400 lines
- files exceeding 500 lines should be treated as a design issue unless there is a strong justification

When a file grows too large:

- split by responsibility
- extract reusable components
- move business logic into dedicated packages
- avoid creating "god files"

The goal is discoverability, not minimizing the number of files.

# Naming

Names should communicate intent.

Good names describe:

- what something represents
- why it exists
- what responsibility it owns

Avoid:

- util
- helper
- misc
- manager
- data
- thing
- temp
- obj

Prefer domain language over technical language.

Examples:

OrderService
InvoiceCalculator
ProductRepository
UserSession

instead of

Manager
Utils
ServiceImpl
DataHandler
