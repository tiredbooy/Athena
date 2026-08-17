# Terminal user interface

Project overview, install, and launch flags also live in the
[root README](../../README.md).

## Ownership

Athena has two clients over one engine:

- `apps/tui` is the **primary** TypeScript/Ink client. It talks to the Go
  process over the stdio protocol (`athena engine`).
- `internal/tui` is the **Bubble Tea fallback**. It is frozen: no new features
  go there.

Neither owns notes, model calls, model selection, or action execution. Both
delegate those to `chat.Session`.

## Which client starts

`cmd/athena/main.go` decides:

| Command | Result |
| --- | --- |
| `athena` | Starts the built Ink app. If it is not built, prints a warning and falls back to Bubble Tea. |
| `athena --tui` | Requires the built Ink app; exits with an error instead of falling back. |
| `athena --legacy-tui` | Forces Bubble Tea. |
| `athena engine` | No UI. Speaks the stdio protocol on stdin/stdout. |

"Built" means `apps/tui` has been compiled (`npm install && npm run build`).

## Ink client (primary)

The Ink source is split so new surfaces do not land in one file. `index.tsx`
only mounts the app. `App.tsx` owns engine events, keyboard, and layout.
Presentation lives in `src/ui/`: transcript rows, composer, command palette,
plan card, connect wizard, model picker, and theme picker. Row/selection math
stays in `transcript.ts`. Colors come from `src/theme.ts`.

- Transcript with a source-aware row viewport: Page Up/Page Down and
  mouse-wheel scrolling. Ctrl+↑ / Ctrl+↓ jump to the previous or next user
  turn and pin that prompt at the top of the pane. Drag to select; copy uses
  OSC 52 and the same source rows, so copied text keeps the correct message
  offsets. Assistant replies style markdown headings, lists, fenced code,
  `inline code`, and `**bold**` on those source rows — punctuation stays in
  the copied text. Click a work block or press Ctrl+O to fold or expand the
  activity on the visible turn.
- One-line composer. Enter sends, Shift+Enter inserts a newline, `↑`/`↓` walk
  input history.
- Typing `/` opens a command palette. Arrows move, Tab completes the
  highlighted command, Enter runs it, Esc closes the palette. The on-screen
  hint matches those keys.
- `Esc` cancels the active turn (`session.cancel`). If the command palette is
  open, the first Esc closes the palette instead.
- `/cancel` discards a pending plan or clarification question. It does **not**
  cancel an in-flight turn.
- Commands: `/help`, `/clear`, `/reset`, `/doctor`, `/models`, `/connect`,
  `/theme`, `/compact`, `/cancel`, `/vim-mode`.
- `/vim-mode` is opt-in and stored in `ui.json`. Off (the default), arrows
  and the composer work as before. On, Esc leaves the composer (normal):
  `j`/`k` jump user turns, `g`/`G` go to the top/bottom of the transcript,
  and `i` focuses the composer again. Esc still cancels an in-flight turn
  first. Typed `/cancel` is unchanged.
- `/help` opens a help overlay (shortcuts + every command). Esc closes it.
  Help is not dumped into the transcript.
- Empty and failure panes have copy and a next action: connecting, empty
  session (vault start), no model on `engine.ready`, and engine-down when
  the child process exits. The engine does not report note counts on hello,
  so the vault card is the empty-session start state, not a filesystem
  inventory. Reconnect is automatic (`E-05` landed), so the engine-down card
  asks the user to wait for the retry rather than to restart. `/models` with
  an empty catalog still offers **Connect a new provider**.
- `/clear` is **view-only**. It empties the visible transcript; engine history,
  a pending plan, and a pending clarification task all survive it.
- `/reset` sends `session.reset`. The engine clears history, the pending plan,
  and the pending question, then Ink empties the pane. Reset is refused while
  a turn is running — cancel first.
- `/models` opens the engine-backed model picker, grouped by connected
  provider. The active provider's live catalog is listed first, then each
  other connected provider's saved model, then **Connect a new provider**.
  Enter on a model switches; Enter on that last row opens `/connect`.
- `/connect` walks only the fields the chosen preset still needs; the preset
  alone decides, because `providerPreset` carries no `fields` list on the
  wire. It does not switch on provider type. OAuth and `auth: none` skip the
  form. The first time the chosen preset is remote (not Ollama and not a
  loopback URL), Ink shows that inventory, search, and `get_note` send vault
  text to that provider. Y / Enter continues; Esc / N goes back. The
  acknowledgment is stored in `ui.json` so the warning is not repeated.
- A plan that needs review focuses a card: `Y` or Enter applies it, `N` or
  `R` discards it, Esc returns to the composer without deciding. Tab focuses
  the card again. Typed `yes` in the composer is not approval. `/cancel`
  still discards a pending plan or question. Each action line uses the
  engine's `summary` when that field is present; otherwise it prints
  `type → target` from existing fields and does not switch on action types.
  The engine now sends `summary` on every planned action (Claude `F-01`).
- Verified graph / orb actions stay in the transcript: the plan card and the
  post-approve lines report folder and node size from existing action
  fields (`Graph · work · 1.50x`). A hex color is printed when the engine
  sends `color`, which `set_folder_colors` now carries (Claude `G-02`).
  Graph tools also render as write work blocks. Note that node size is
  graph-wide in Obsidian, not per folder.
- Search, read, and write activity appear as work blocks in the transcript
  while the turn runs and stay in the scrollback after it. Folded blocks are
  one line; click a block or press Ctrl+O to expand the last one. The engine
  already sends `phase` / `tool` / `target` / `path` / `state` when it has
  them, including per-step `started` / `succeeded` / `failed` on retrieve,
  read-tool, validate, and execute (Claude `E-01`). `approval` and
  `verifying` emit only `started`, so a block must not wait for their close.
- The pulsing footer is only for connect/startup and for phases that are not
  work blocks (`provider_wait`). It is not the only place search/read/write
  appear.
- `/theme` opens a live picker for `midnight`, `ocean`, and `system`. Arrows
  preview the whole chrome, Enter saves, Esc reverts. The choice is stored in
  `ui.json` under `XDG_CONFIG_HOME/athena` or `~/.config/athena` and restored
  on the next start, along with the last `providerId` and model. After
  `engine.hello`, if that saved model differs from `engine.ready`, Ink sends
  `model.select` so the chrome matches. That path reuses a saved OAuth
  session (`P-02`); it does not start device-login.
  `midnight` is purple dark, `ocean` is cyan/blue, and
  `system` uses ANSI color slots so the terminal palette wins. When the
  terminal is only 16-color (`TERM` without 256/truecolor, typical tmux/SSH),
  `midnight` and `ocean` fall back to named ANSI slots and do not paint a
  background, so they stay readable instead of dark-on-dark.
- Engine warnings on stderr (Ollama pull noise, OAuth helpers, process
  notes) appear as `engine` transcript lines. They are not parsed as
  protocol JSON and do not mix into stdout.
- If the engine child exits unexpectedly, Ink shows a reconnecting
  state, spawns it again, and sends `engine.hello`. The visible
  transcript stays as view-only history. Pending plans, in-flight turn
  IDs, and engine conversation state are dropped and labeled as gone —
  they are not faked onto the new process. Durable session restore is
  still Claude `M-03` / `M-04`. `Ctrl+C` disposes the client and does
  not reconnect.

### Engine RPC settlement

`EngineClient` keys each request by `requestId`. Progress events reuse that
id (`turn.started`, `activity`, `response`), so the client keeps the promise
open until the first terminal event for that request:

| Request | Settles on |
| --- | --- |
| `session.submit` | `turn.completed`, `plan.ready`, `turn.failed`, `turn.cancelled`, or `error` |
| `plan.approve` | `plan.approved`, `turn.failed`, or `error` |
| `provider.oauth.start` | `provider.connected`, `provider.oauth.cancelled`, or `error` |

Every event is still emitted immediately so the UI can render work in flight.
`error` rejects the promise; the other terminal types resolve with the event.
`provider.oauth.started` and `provider.oauth.progress` are the exception to
the keying above: the engine sends them with a `turnId` and no `requestId`,
so they render as they arrive and only the terminal event settles the call.

### Client tests

`apps/tui/testdata/` holds recorded engine streams as NDJSON (one event per
line). Tests parse those lines with the same `parseEngineLine` /
`readEngineEvents` helpers `EngineClient` uses on stdout, then:

- assert settlement (`firstTerminalEvent`) for `session.submit`,
  `plan.approve`, and `provider.oauth.start`
- fold the same events through `reduceEvent` in `src/engine/session.ts`

The React app does not interpret events itself. It applies that session
state (transcript, status, plan card, pickers). Invalid JSON and
unsupported envelopes become `protocolError`, not guessed UI.

`cd apps/tui && npm test` compiles the package and runs the Node test
files, including these fixtures.

### What "streaming" means today

The engine still emits one `response` with the complete reply. It does
**not** emit `response.delta` yet (`E-04`). Adding it means adding the type
to `protocol/athena.v1.schema.json` in the same change — the schema drift
test now fails otherwise.

If a `response.delta` line does arrive, Ink appends it to a live assistant
row and leaves search/read/write blocks in place. A later `response`
replaces that live text with the final message. `response.delta` is not a
terminal RPC event.

Do not describe today's engine as streaming response text. Tool blocks
plus one final `response` are what it sends.

## Bubble Tea fallback

- Full-screen Bubble Tea application with Bubbles textarea and viewport.
- Compact one-line composer styled as a command bar.
- Spinner and factual engine status below the input. Its streaming plumbing
  receives the reply as a single chunk, for the reason above.
- Conversation entries use role labels and thin themed rails.
- `Esc` cancels an active request.
- `/clear` clears only the visible pane; `/reset` also clears session history.
  `/reset` exists **only here** — it is in-process, not a protocol request.
- Selectable command palette: arrows choose, Tab completes, Escape dismisses.
- `/models` and `/model` query and select models from the active provider.
- `/theme` selects `midnight`, `ocean`, or `system`.

### Bubble Tea themes

- `midnight` is the default purple dark presentation.
- `ocean` changes accent, message, input, and border colors to cyan/blue.
- `system` uses ANSI color slots for hierarchy (cyan accent, blue borders,
  muted text, and red errors). Those slots come from the terminal palette, so
  the appearance follows the user's terminal theme without becoming flat.

Theme styling must be applied to the composer, textarea focus/cursor, spinner,
messages, borders, header, and muted text together. Do not add a fixed color
in `View`, because it would override user theme selection.

Ink ships the same three names. `/theme` previews and persists them there.

### Engine-side contract notes for the client

- **Action display text.** Actions on `plan.ready` carry an engine-written
  `summary` (`Creating note "Quarterly plan"`). Render it rather than switching
  on action types. `plan.approved` carries no actions — the post-approve lines
  reuse the plan the client already has.
- **Execution ledger.** `turn.completed` and `plan.approved` carry `ledger`: the
  verified record of what the vault actually did, as
  `{action, target, status, message?, error?}`. It is present even when the
  model's reply is terse or empty, and absent on read-only turns.
- **Graph orbs.** `set_folder_colors` accepts `color` (`#RRGGBB`) and reports
  the applied color, so the transcript can show the real value.
- **Schema drift.** `protocol/athena.v1.schema.json` is the contract and a Go
  test enforces it in both directions. Any new field or event type has to land
  in the schema in the same change.
