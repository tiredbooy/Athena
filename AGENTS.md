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