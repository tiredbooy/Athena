# Planned work

This document records approved directions that are not implemented yet.

## Conditional organization recommendations

Goal: append a useful suggestion only when there is evidence, never a generic
recommendation after every answer.

Recommended design:

1. Add an application-level review service that accepts catalog entries and
   returns structured suggestions (`kind`, `note_id`, `reason`, `proposed
   action`).
2. Keep suggestion generation separate from chat text and action execution.
3. Render at most one high-confidence suggestion after a reply.
4. Never apply a recommendation without a new explicit user request.

The first policy rules need to be decided before implementation. A folder being
“wrong” is subjective; the current data model has no universal category for a
note. Good initial rules are explicit conventions such as “book notes must be
under `book/`” or “notes in the vault root should be offered a destination.”

## TypeScript TUI with Go engine

This is a viable direction. Keep Go responsible for vault files, SQLite,
retrieval, Ollama access, and business rules. Let TypeScript own rendering,
keyboard interactions, panels, and client-side UI state.

Before introducing TypeScript, extract the current `chat.Loop` into an engine
API that emits JSON-line events over standard input/output:

```text
TS request: {"type":"chat","id":"...","text":"..."}
Go events:  {"type":"status","message":"Reading ..."}
            {"type":"answer_delta","text":"..."}
            {"type":"action_result","ok":true,"message":"..."}
            {"type":"suggestion","reason":"..."}
            {"type":"complete"}
```

Do not mix terminal escape sequences or debug output into this protocol; write
diagnostics to stderr. Keep the current Go CLI as a thin client while the
TypeScript TUI is built and tested separately.
