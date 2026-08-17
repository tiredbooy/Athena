# Grok

When the user says **take Grok tasks**, **grok queue**, or **do the Grok work**:

1. Read `tasks.md`.
2. Do **only** tasks whose **Owner** is `Grok`.
3. Ignore every `Claude` row. Do not rewrite `internal/chat`, `internal/notes`,
   `internal/tools`, or `internal/retrieval` policy.
4. Start at the first `todo` item in **Grok's queue** at the bottom of `tasks.md`.
5. One task (or one tightly related pair like U-02+U-03) per change.
6. Mark the row `done` and update `docs/tui` in the same change.
7. If you need a Claude-owned engine event that does not exist yet, render
   what exists and name the blocking Claude ID. Do not invent engine policy.

Grok's job in one line: make the Ink TUI feel like a product (honest RPC,
palette, live work blocks, themes).

Claude's job: the Go engine. Do not take it.

Standing rules: `AGENTS.md`.
