# Athena TypeScript TUI Migration Plan

## Decision

Move Athena's terminal user interface from Go/Bubble Tea to TypeScript using
[Ink](https://www.npmjs.com/package/ink), a React renderer for command-line
applications. Keep Go as the trusted local engine for providers, vault files,
SQLite, embeddings, jobs, and tool execution.

This is a **UI migration**, not a Go rewrite.

## Why

The current Go engine already owns the difficult and safety-sensitive work:

- Markdown vault and filesystem operations
- SQLite index and embeddings
- Provider adapters, retries, cancellation, and credentials
- Read tools, write-action validation, confirmation policy, and audit trail

The Bubble Tea layer has become responsible for interactions that need faster
iteration: multiline composing, copy/selection behavior, scrolling, approval
controls, command palettes, model selection, and live activity. React/Ink is a
better fit for composing and testing those interactions.

## Goals

1. Preserve every vault and provider safety boundary in Go.
2. Make the UI responsive while the engine is running a slow local model.
3. Stream trustworthy activity events from the engine: reading a relative path,
   searching, waiting for a provider, tool calls, action progress, and errors.
4. Replace typed `/confirm` / `/cancel` with explicit approval controls.
5. Allow the new TUI and the existing Bubble Tea UI to coexist during migration.
6. Keep the first release local-first and offline except for providers the user
   explicitly configured.

## Non-goals

- Rewriting the vault, SQLite, provider, or embedding layers in TypeScript.
- Sending the vault to a remote service merely for UI rendering.
- Depending on a browser or web server for the first TUI release.
- Pretending the UI can show private model reasoning. It will show only real
  engine operations and provider wait states.

## Target architecture

```text
┌──────────────────────────────────────────────┐
│ apps/tui (TypeScript + React + Ink)           │
│ composer · chat · activity · approvals        │
└───────────────────────┬──────────────────────┘
                        │ versioned JSON-RPC over stdio
┌───────────────────────▼──────────────────────┐
│ cmd/athena engine (Go)                        │
│ session API · event stream · cancellation     │
├──────────────────────────────────────────────┤
│ internal/chat · tools · notes · retrieval     │
│ ai/providers · storage · jobs                 │
└──────────────────────────────────────────────┘
```

The TypeScript process starts the Go engine as a child process. The engine has
no terminal-rendering dependency and can later serve another UI without changing
business logic.

## Proposed repository layout

```text
apps/
  tui/                         # TypeScript package: React Ink UI
    src/
      app/
      components/              # Composer, ChatTranscript, Activity, Approval
      hooks/                   # engine events, keyboard, viewport behavior
      protocol/                # generated/shared protocol client and types
      test/
    package.json
    tsconfig.json

cmd/
  athena/                      # Go executable entry points

internal/
  api/                         # NEW: session-facing request handlers
  transport/stdio/             # NEW: JSON-RPC framing, handshake, events
  chat/                        # conversation orchestration; no terminal code
  ai/ notes/ retrieval/ tools/ storage/ ...

protocol/
  athena.v1.schema.json        # language-neutral request/event contract
  README.md

docs/
  tui/                         # interaction, protocol, and release docs
```

`internal/tui` remains temporarily while `apps/tui` reaches feature parity.
Only then is Bubble Tea removed. Do not place TypeScript inside `internal/`:
that directory is Go engine implementation detail, while the TUI is a separate
application.

## Protocol design

Use newline-delimited JSON messages on standard input/output. Logs always go to
stderr so they cannot corrupt the protocol stream.

Every message includes `version: 1`, `requestId`, and `type`.

### UI → engine requests

```json
{"version":1,"requestId":"r1","type":"session.submit","input":"organize my books"}
{"version":1,"requestId":"r2","type":"plan.approve","planId":"p42"}
{"version":1,"requestId":"r3","type":"plan.reject","planId":"p42"}
{"version":1,"requestId":"r4","type":"session.cancel","turnId":"t17"}
{"version":1,"requestId":"r5","type":"models.list"}
{"version":1,"requestId":"r6","type":"doctor.run"}
```

### Engine → UI events

```json
{"version":1,"type":"turn.started","turnId":"t17","provider":"Ollama","model":"Qwen"}
{"version":1,"type":"activity","turnId":"t17","phase":"reading","path":"books/finished/foundation.md"}
{"version":1,"type":"activity","turnId":"t17","phase":"provider_wait","provider":"Ollama","model":"Qwen"}
{"version":1,"type":"plan.ready","planId":"p42","summary":"Move 2 notes","actions":[...]}
{"version":1,"type":"turn.completed","turnId":"t17","message":"..."}
```

Rules:

- Paths are always vault-relative; never send host absolute paths to the UI.
- Engine events are factual. A model cannot emit an `activity` event itself.
- Cancellation is acknowledged immediately, then followed by `turn.cancelled`.
- Unknown request/event versions fail clearly instead of being guessed.
- Approval IDs are single-use and expire when the session changes.

## Go refactor phases

### Phase 0 — Stabilize the current contract

1. Extract terminal-dependent callbacks from `chat.Session` into a small
   application-facing session API.
2. Represent status as structured `ActivityEvent` values instead of display
   strings.
3. Make pending plans explicit objects with an ID, actions, expiry, and state.
4. Add tests covering cancellation, one-time approval, tool-limit completion,
   and stale event rejection.

Exit condition: the Bubble Tea UI can render the API through an adapter without
owning chat or tool policy.

### Phase 1 — Add the Go stdio engine

1. Add `athena engine` mode; keep `athena` behavior unchanged initially.
2. Implement the version handshake and JSON-lines framing.
3. Map submit, cancel, approval, model, provider, and `/doctor` operations to
   the existing application services.
4. Emit structured activity, response, plan, error, and completion events.
5. Add protocol integration tests using pipes; malformed messages must not
   crash the engine.

Exit condition: a tiny non-Ink test client can complete a conversation safely.

### Phase 2 — Create the Ink shell

Use TypeScript, React, Ink, and Ink's testing tools. Start with Node LTS for
compatibility; evaluate Bun compilation only as a packaging optimization after
the UI works reliably.

Build these components:

- `EngineClient`: child-process lifecycle, request IDs, reconnection errors.
- `ChatTranscript`: virtualized/scrollable transcript and copy-friendly output.
- `Composer`: Enter submits, Shift+Enter creates a newline, draft is preserved
  while a turn runs, and cancelled turns never overwrite a new draft.
- `ActivityLine`: exactly one current factual operation, not a debug log.
- `ApprovalCard`: clear Yes/No keyboard choices with action summaries.
- `CommandPalette`, `ModelPicker`, `ProviderConnect`, and `DoctorPanel`.

Exit condition: TypeScript can submit a normal chat turn, cancel it, approve a
plan, and render provider/model state against the Go engine.

### Phase 3 — Feature parity

Port, test, and compare:

1. Commands: models, connect, compact, doctor, theme, reset, help.
2. Provider/model selection and OAuth browser handoff.
3. Streaming response/activity states and errors.
4. Pending-plan review and safe confirmation.
5. Keyboard navigation, scrolling, copy/selection behavior, and accessibility.
6. Local-model fault handling: retries, tool-schema fallback, tool-round limit,
   cancellation, and clear errors.

Keep Bubble Tea as `athena --legacy-tui` until this phase is complete.

### Phase 4 — Packaging and cutover

1. Package the TypeScript TUI with its runtime or compile it into a companion
   executable per platform.
2. Let `athena` launch the new UI by default and `athena engine` remain an
   internal protocol command.
3. Ship a release-candidate period with legacy fallback.
4. Remove `internal/tui` only after parity, migration documentation, and smoke
   tests on Linux/macOS/Windows.

## UI interaction decisions

| Interaction | Decision |
| --- | --- |
| Send | Enter |
| Newline | Shift+Enter |
| Cancel | Esc; UI changes immediately after engine acknowledgement |
| Approval | Y/Enter approve; N/Esc reject; visible card, never typed command |
| Activity | One live factual line, e.g. `Reading work/rumera/plan.md` |
| Provider wait | `Ollama · Qwen is generating a response` |
| Copy | Preserve normal terminal selection; do not capture mouse unnecessarily |
| Scroll | Keyboard always works; mouse support must not break selection |

## Risks and mitigations

| Risk | Mitigation |
| --- | --- |
| Two runtimes complicate packaging | Keep a strict stdio protocol and test the packaged launcher on every platform. |
| UI leaks engine policy | The engine owns plans, permissions, and vault writes; UI sends intent only. |
| Stale events after cancellation | Turn IDs and request IDs; UI ignores non-current events. |
| Terminal limitations remain | Ink improves composition, but a local web/Tauri UI is the future option for browser-quality selection and mouse behavior. |
| Protocol churn | Version the schema from day one and add compatibility tests. |

## Acceptance criteria

- No TypeScript code writes vault files or accesses SQLite directly.
- A slow/cancelled provider request cannot freeze the composer.
- Every plan is approved or rejected through an engine-issued plan ID.
- Activity never claims hidden model reasoning.
- The new TUI passes component tests and protocol integration tests.
- `athena --legacy-tui` remains functional until cutover.

## Recommended first implementation slice

Do not begin with visual components. First extract `chat.Session` into a
structured event API and build `athena engine` with only `session.submit`,
`session.cancel`, and `plan.approve/reject`. Once the boundary is reliable, the
Ink UI can be built quickly without duplicating Go logic.
