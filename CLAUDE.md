# Claude

When the user says **take Claude tasks**, **claude queue**, or **do the Claude work**:

1. Read `tasks.md`.
2. Do **only** tasks whose **Owner** is `Claude`.
3. Ignore every `Grok` row. Do not edit `apps/tui/` except `apps/tui/README.md` if a docs task names it.
4. Start at the first `todo` (or `design-first`) item in **Claude's queue** at the bottom of `tasks.md`.
5. One task (or one tightly related pair like P-01+P-02) per change.
6. Mark the row `done` and update `docs/` in the same change.
7. If you need a Grok-owned file, stop. Write what Grok must do next.

Claude's job in one line: make the engine honest and reliable (docs, trash-out-of-RAG, provider switch without re-login, session lock, structured events, 2B-model path).

Grok's job: the Ink TUI. Do not take it.

Standing rules: `AGENTS.md`.
