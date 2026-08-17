# Session snapshot and opt-in restore (M-03 design)

**Status: proposal. This is planned work, not current behavior.** Current
behavior lives in `docs/chat/README.md` and `docs/configuration/README.md`.
M-04 implements whatever this document settles.

This is a privacy design first and a storage design second. The chat transcript
is the most sensitive text Athena handles: it contains the user's own words plus
whatever the model quoted back out of their notes. Deciding to write it to disk
is not a performance decision, and it is not reversible for text that has
already been written.

---

## 1. What exists today

### Everything lives in one in-memory `Session`

`chat.Session` (`internal/chat/session.go:32`) owns the whole conversation:

| Field | What it holds |
| --- | --- |
| `history []models.Message` | The durable transcript. Element 0 is the system prompt from `ai.SystemPromptAt(time.Now())`, set in `NewSession` (`session.go:53`). |
| `pendingPlan *PendingPlan` | The one-time approval target (`internal/chat/events.go:43`): `ID`, `Actions`, `CreatedAt`, plus two unexported fields — `run *agent.RunState` and `lead string`. |
| `pendingTask *PendingTask` | The application-owned goal (`internal/chat/task_state.go:15`): `OriginalGoal`, `Question`, `Answers`, `ExpectedAction`, `CreatedAt`. |
| `lastLedger []agent.LedgerRecord` | The verified execution record of the most recent turn. |
| `nextPlanID`, `nextRunID` | Monotonic counters. `previewActions` mints `plan-N`; `startRun` mints `run-N`. |
| `nativeToolsDisabledModel` | An Ollama model that rejected the native tool schema this session. |

There is exactly one `Session` per process. `cmd/athena/main.go:145` builds it
and hands it to `stdio.Serve`. Nothing about it is written to disk, ever. When
the process exits — or when the Ink client respawns a crashed engine via
`EngineClient.reconnect` (E-05) — a brand new `Session` starts with a one-element
history and no pending state.

### The two locks matter for anything that adds I/O

`turnMu` serializes whole turns and is held across model I/O for up to
`TurnTimeout` (5 minutes). `mu` guards the fields above and is deliberately
*never* held across a network call — that split is F-04, and it is why `/models`
and the provider footer stay answerable during a turn. Lock order is `turnMu`
then `mu`.

A snapshot writer must respect this: build the snapshot under `mu`, then release
it and do the file I/O. Holding `mu` across an `fsync` re-creates exactly the
stall F-04 removed.

### Compaction is already deterministic

`compactHistory` (`internal/chat/compact.go:28`) fires automatically once the
history exceeds `historyCompactThreshold` (12,000 characters) or on demand from
`/compact`. It keeps `history[0]` (system prompt) and the last
`historyRecentMessages` (6) messages verbatim, and folds everything older into
one `system` message — `"Conversation memory (older turns):"` — with each item
truncated to `historySummaryItemLimit` (600) by `compactText`. Since M-02 it
also appends a second `system` message built by `retainedStateMessage`
(`compact.go:84`), which restates the application-owned facts — active goal,
pending question, prior answers, last verified actions — as JSON regenerated
from live session state on every compaction, never carried forward as prose.

This matters: Athena already has a bounded, model-free way to shrink a
conversation. A snapshot does not need to invent one, and must not ask a model
to summarise itself (AGENTS.md: a 2B model is a first-class target).

`compactText` is rune-safe: it walks the byte limit back to the nearest rune
boundary before slicing (`compact.go:111`), so a truncated item cannot end in
half a character.

### `Session.Reset` is the only existing "forget"

`Reset` (`session.go:646`) truncates history to the system prompt and nils both
pending fields. Clients reach it through the `session.reset` protocol request;
the stdio server refuses it while a turn runs (`server.go:327`).

### What is already on disk (be honest about the baseline)

- The vault: plaintext markdown under `vault_path` (default `~/Athena`).
- SQLite (`~/.local/share/athena/athena.db`): `notes.content` in full, `chunks`
  with embeddings, and — this one is easy to miss — `action_audit.action_json`,
  which is the *entire* serialized action, including `content` for every
  `create_note` / `update_note`, and including plans that were merely *rejected*
  (`internal/tools/tools.go:94`, `:285`). Note text is therefore already
  duplicated in the database.
- Credentials: `provider-credentials.json` (0600, plaintext key), Codex and xAI
  OAuth token files.
- `~/.config/athena/ui.json`: theme, last provider/model, warning flags (M-05,
  Ink side).

What is **not** on disk today, and only what is not: the user's typed prose and
the assistant's replies — the conversation itself.

### How a pending plan works today

`previewActions` (`session.go:342`) increments `nextPlanID`, builds
`PendingPlan{ID: "plan-N", Actions, run: state}` and returns the review text.
`takePlan` (`session.go:742`) claims it under `mu` and nils it, so a plan ID is
**single-use**: the second approval finds nothing pending. `approvePlan` then
calls `runner.ResumeApproved(ctx, plan.run, actions, ...)`, which executes the
batch and continues the same agent run (`internal/agent/runner.go:130`) so the
model can observe results and finish.

The unexported `plan.run` is the load-bearing part. `RunState`
(`internal/agent/state.go:45`) carries `Messages` — the full prompt snapshot,
*including the retrieved vault context that `session.go:149` inlines into the
user message* — plus `Completed []ai.ActionResult`, `ActionAttempts` and
`Succeeded`. If `plan.run` is nil, `approvePlan` falls back to
`loop.runActionsWithStatus` (`internal/chat/chat.go:105`), a legacy compat path
that dispatches the actions and returns text with **no ledger and no agent
verification step**.

---

## 2. The problem

Restart forgets everything, and restarts are not rare:

- The user answers a clarification question over two or three turns, the
  terminal dies, and the goal, the question, and every answer are gone. Athena
  will re-embed and re-search the vault for a goal it already had. (The process
  no longer expires on its own: F-06 landed, and `processContext`
  (`cmd/athena/main.go:216`) is now a bare `signal.NotifyContext` with no
  deadline. Only a single turn is bounded, by `TurnTimeout`.)
- A plan card with eight actions is on screen when the engine crashes. Ink
  respawns the engine (E-05) and correctly drops the stale plan ID, so approving
  now yields *"There is no pending change to confirm."* The user's review work is
  lost, and on a 2B model regenerating that plan is expensive and not guaranteed
  to produce the same actions.
- Anything that restarts the engine — switching providers in a way that needs a
  fresh process, an update, a laptop suspend that kills the child — costs the
  whole thread.

But "remember everything" is the wrong repair, and this is where the design has
to be careful rather than convenient:

1. The transcript is private prose. Not "metadata": the user's actual questions
   about their actual notes, plus model replies that quote note bodies verbatim.
2. Naively persisting the *plan* is worse than losing it. A restored plan needs
   either the `RunState` (the largest and most sensitive blob in the engine — it
   contains inlined vault text) or the `plan.run == nil` fallback, which
   regresses E-03's invariant that a mutation always carries a verified ledger.
3. A restored plan ID is a correctness bug, not only a privacy one. Plan IDs are
   single-use *because they live in memory and `takePlan` nils them*. Persist a
   pending plan and that invariant breaks in two ways: (a) approve → crash
   mid-execution → the snapshot still says "pending" → restore → approve again →
   actions that are not idempotent (`append_note`, `move_note`) run twice;
   (b) a restored `plan-3` collides with the `plan-3` a fresh process will mint
   on its third plan, so a stale approval from a reconnected client can approve
   the wrong actions. Note that `ResumeApproved` does **not** consult
   `state.Succeeded` for the approved batch — `executeAndObserve`
   (`runner.go:146`) dispatches unconditionally — so nothing downstream catches a
   double-apply.

---

## 3. Constraints

From AGENTS.md, quoted because they bind this design:

- **Memory:** *"Conversation transcripts stay in-memory by default. Durable
  session recovery is opt-in and needs explicit retention/privacy rules."* The
  default must remain "no private transcript file", and the retention/wipe rules
  are part of the deliverable, not a follow-up.
- **Memory:** *"The application owns the active goal, pending question, and
  pending plan. Never reconstruct those from a short reply such as 'yes'."*
  Restoring must rehydrate typed state, never re-derive it from prose.
- **Memory:** *"Compaction must keep the facts needed to finish the current
  goal."* Whatever the snapshot drops, it may not drop the active goal.
- **Event-driven UI:** *"The engine is the source of truth. The TUI is an event
  renderer."* Restore is an engine decision announced through a typed event. The
  Ink client must not read a session file; `ui.json` must not grow session state.
- **Small-model reliability:** deterministic, application-owned state only. No
  model-generated session summary.
- **Retrieval and vault:** *"Filesystem and SQLite are not one transaction; new
  multi-step writes need compensating undo or a journal."* A snapshot write must
  not be able to leave a half-written file that then poisons startup.
- **Documentation:** `docs/` describes what exists now; this file is a plan.
  Shipping M-04 must update `docs/chat`, `docs/configuration`, and
  `protocol/README.md` in the same change.
- **File organization:** keep files under ~300 lines; `session.go` is already
  626. Snapshot code belongs in new files, not appended to `session.go`.

Architectural constraints from the code:

- `mu` is never held across I/O. Build under the lock, write outside it.
- `PendingPlan.run` is unexported and `agent.RunState` is not serializable as
  written (`ai.ActionResult.Err` is `json:"-"`, so a round-trip silently loses
  failure detail).
- One process = one session. The protocol has no session identifier, so
  multi-session support is a protocol change, not a storage change.
- `F-01`: `protocol/athena.v1.schema.json` is the contract and a Go test fails on
  drift. Any new event lands in the schema in the same change.

---

## 4. Options

### Option A — Resume the task, not the conversation (simplest)

Persist only the application-owned `PendingTask`: `OriginalGoal`, `Question`,
`Answers`, `ExpectedAction`, `CreatedAt`. No transcript, no plan, no vault text.
One small JSON file. On startup, if enabled and fresh, the engine restores
`pendingTask` and says: *"You were in the middle of: <goal>. Athena asked:
<question>. Answer to continue, or /cancel to drop it."*

- **How it fails:** the user says "remember our chat" and gets a goal, not a
  chat. *"What did I ask you earlier?"* is still unanswerable. Turns that were
  never paused for a question (the majority) restore nothing at all, because
  `pendingTask` is only set when `outcome.AwaitingUser` is true
  (`session.go:299`).
- **Cost:** very small. One struct that is already JSON-tagged, one file, one
  config flag.
- **Privacy:** smallest surface, but not zero — `OriginalGoal` and `Answers` are
  the user's own words, and `Question` is model prose that can quote note
  content.

### Option B — Bounded compacted session file, opt-in (recommended)

One file, one slot, deterministic content:

```
~/.local/share/athena/session.json      mode 0600, parent dir 0700
```

Contents (schema v1):

| Field | Type | Why |
| --- | --- | --- |
| `version` | int | Refuse and delete anything that is not 1. |
| `saved_at` | RFC3339 | Drives the TTL check. |
| `provider`, `model` | string | So the restore banner can say what was running. Not authoritative — the live config wins. |
| `goal` | `PendingTask` or null | The application-owned state. Restores verbatim. |
| `messages` | `[{role, content}]` | Compacted transcript, capped. **Excludes** `history[0]`. |
| `interrupted` | bool | True when the file was written at turn start and no completion write followed. |
| `discarded_plan` | `{actions: int, created_at}` or null | A **count**, never action bodies, never an ID. |

Explicitly absent, and this is the design: no plan ID, no action bodies, no
`RunState`, no inlined vault context block, no system prompt. The system prompt
is regenerated at startup by `ai.SystemPromptAt(time.Now())` — restoring a stale
one would hand the model yesterday's date.

The snapshot is produced by running the existing compaction rules over a *copy*:
`history[0]` dropped, older turns folded into the one deterministic memory line,
last 6 messages verbatim, whole file capped (64 KiB) by dropping oldest verbatim
messages first. Restore = new system prompt, then the restored messages.

- **How it fails:** a private transcript file now exists on disk when the flag is
  on. A stale snapshot can restore a goal about a note the user has since
  deleted — self-correcting, because the next turn re-reads the vault, but
  briefly confusing. A corrupt file must be treated as "start fresh and say so",
  never as a startup failure.
- **Cost:** one new config field, ~150 lines of snapshot/store code in two new
  files, one protocol event, one docs pass. No schema migration, no new
  dependency.

### Option C — Sessions in SQLite with `/sessions` (production-grade)

New tables — `sessions(id, started_at, updated_at, title, provider, model)`,
`session_messages(session_id, seq, role, content, created_at)`,
`session_state(session_id, pending_task_json, next_plan_id)` — plus protocol
requests `session.list` / `session.resume` / `session.delete`, an Ink picker, and
a retention job to delete sessions older than N days.

- **How it fails — and this is the option that can lose or expose user data:**
  the complete, indefinitely retained record of everything the user has ever said
  now lives in `athena.db`, the same file they back up, copy between machines,
  and (per L-05) may one day sync. Deleting rows in SQLite does **not** erase the
  bytes: they stay in free pages until `VACUUM`, and the WAL keeps a second copy
  until checkpoint. A "delete my history" button that leaves the text recoverable
  in a file the user hands to a sync service is a promise Athena would be
  breaking. A retention-job bug in the other direction silently deletes
  conversations the user wanted.
- **Cost:** migrations, retention, `/sessions` UI (Grok's queue — a cross-owner
  dependency M-04 would have to wait on), and concurrency with the indexer on the
  same database.
- It is the right shape *eventually*, if the product decides conversation history
  is a feature rather than a recovery mechanism. It is the wrong first step.

---

## 5. Recommendation

**Ship Option B, with Option A's plan rule: a pending plan never survives a
restart.** Default `session_restore: off`.

Why B over A: A only helps the minority of turns that ended in a question. B
restores the thing users actually mean by "remember" — the thread — while
staying bounded and deterministic, and it reuses `compactHistory`'s existing
rules instead of inventing a second summarisation policy.

Why B over C: the wipe story. A single file means "delete this file and it is
gone" is *true*, and `rm ~/.local/share/athena/session.json` is a wipe the user
can perform and verify without trusting Athena's code. Inside SQLite, honest
erasure requires `VACUUM` and a WAL checkpoint, and the private prose ends up in
the artifact users copy and sync. One file also keeps conversation text out of
the database that vault backups target. There is no operational benefit to a
table here: this is one small blob, read once at startup, written once per turn.

Why plans never restore, stated as an invariant worth a test:

> No plan ID minted by a previous process may exist in a new process's state.

Restoring plan *actions* would require either persisting `RunState` — the
largest and most sensitive object in the engine, containing inlined vault text —
or executing through `approvePlan`'s `plan.run == nil` fallback, which skips the
agent's observe/verify step and returns no ledger, regressing E-03. Neither is
acceptable as a default. The user loses one plan regeneration; they keep the
guarantee that an approved action cannot silently run twice. Instead, the
snapshot records that a plan of N actions was discarded, and the restore banner
says so in those words, so the user knows to re-ask rather than assuming it
applied.

Two writes per turn, both tiny, both outside `mu`:

1. **At turn start**, right after `startRun` appends the user message, with
   `interrupted: true`. This means the user's typed request survives a crash
   *during* the turn, and it is how Athena knows the turn was interrupted.
2. **At turn end** (after `finishAgentOutcome`, and after approve/reject), with
   `interrupted: false`.

The interrupted flag earns its keep because of a fact the snapshot cannot hide:
a turn that died mid-execution may already have mutated the vault. Athena's
memory will not mention it. The banner must therefore say *"your last request was
interrupted and Athena recorded no reply — an interrupted turn may already have
changed the vault"*, and point at the vault and `action_audit` as the record of
what actually happened. Silently restoring a truncated conversation as if it were
complete would be exactly the kind of dishonesty `docs/chat` was written to
remove.

Known ceiling, deliberately accepted: two engine processes sharing one snapshot
file are last-writer-wins, so one session's memory can overwrite the other's.
Single-user laptop, single engine child — this is fine and gets documented as a
limitation. The upgrade path if it ever matters is an exclusive lock held for the
process lifetime (`O_EXCL` pid file or `flock`), where the second engine skips
both restore and writes and emits a diagnostic.

Not persisted, on purpose: `nextPlanID` / `nextRunID` (fresh counters in a fresh
process; restoring them is only needed if plans restore, and they do not),
`lastLedger` (the durable record is `action_audit` and the vault),
`nativeToolsDisabledModel` (cheap to rediscover, and stale state here would
suppress native tools for a model that was since fixed).

---

## 6. Decisions needed from the owner

These are the questions the code cannot answer. Everything in section 7 waits on
them.

1. **Default and opt-in mechanism.** Confirm `session_restore` ships **off**, and
   decide how a user turns it on: hand-editing `config.yaml` (explicit, a little
   hostile) or a `/remember on` command that writes the flag after showing what
   will be stored and where. The second is friendlier; it also means Athena can
   create the file from inside a chat, which is the moment the privacy decision
   is actually made.
2. **How much prose.** Goal only (Option A's content, no transcript file at all),
   or goal plus the compacted last-6-turns transcript (the recommendation), or
   full history up to the byte cap? This is the whole privacy/utility tradeoff in
   one question.
3. **Retention.** How long may an unused snapshot survive: until the next restore
   (one-shot — restore then delete), 7 days, 30 days, or indefinitely? And is it
   one slot (each session overwrites the last) or the last N sessions? The
   recommendation is one slot with a 7-day TTL, because a snapshot older than
   that is a stale conversation, not a recovery.
4. **What "wipe" must cover — the irreversible one.** Deleting `session.json` is
   the easy half. Does `/forget` also purge `action_audit` rows, which contain
   note content in `action_json` including *rejected* proposals? That deletion is
   irreversible and it destroys the diagnostic trail for repeated tool failures.
   Options: (a) session file only, and document that the audit log exists;
   (b) session file plus audit rows older than N days; (c) `/forget --all` which
   also empties `action_audit`, with a second confirmation. Whatever is chosen,
   the docs must state plainly that the vault itself is never touched by a wipe.
5. **Automatic restore or explicit `/resume`.** Auto-restore is what "it
   remembers" means to most users. It also means launching Athena in a shared or
   screen-shared terminal reprints the last private conversation with no consent
   at that moment. Explicit `/resume` costs one command and removes that class of
   surprise.
6. **Confirm the plan rule.** Plans never survive a restart, and the user is told
   a plan of N actions was discarded (recommended). The alternative — re-present
   the same actions as a *new* plan for re-review — is possible, but it must
   re-run validation against current vault state and cannot attach the agent's
   verified ledger. Do you accept losing the plan?
7. **Machine-local or vault-adjacent.** Does the snapshot stay in
   `~/.local/share/athena/` (machine-local, never synced), or is it expected to
   travel with a synced vault? This bears directly on L-05; the recommendation is
   machine-local and explicitly out of the vault.

---

## 7. Implementation sketch (M-04, if approved)

Ordered so each step is independently reviewable. No step edits `apps/tui/`.

1. **Config.** Add `session_restore` (bool, default false) and, if decision 3
   picks a TTL, `session_retention_days` to `config.Config` in
   `internal/config/config.go`. It is a plain YAML field, not a credential.
2. **Snapshot type and Session methods.** New file `internal/chat/snapshot.go`:
   the `Snapshot` struct with JSON tags; `(*Session).Snapshot(interrupted bool)`
   which takes `mu`, copies, applies the compaction rules and the byte cap, and
   returns a value; `(*Session).Restore(Snapshot) error` which takes `mu`,
   refuses if the session is not pristine (history longer than the system prompt,
   or any pending state), rebuilds history as `[new system prompt] + messages`,
   and sets `pendingTask`. It never sets `pendingPlan`. Add a rune-safe cap
   helper next to `compactText` rather than reusing its byte slice.
3. **Store.** New file `internal/chat/snapshot_store.go`: `Load(path)`,
   `Save(path, Snapshot)`, `Delete(path)`. `Save` writes a temp file in the same
   directory with mode 0600 and `os.Rename`s over the target — rename within one
   filesystem is atomic, so a crash mid-write leaves either the old file or the
   new one, never a half-parsed one. This is the journal AGENTS.md asks for on
   multi-step writes, at file granularity. `Load` returns "no snapshot" for a
   missing file, and for a corrupt or wrong-version file returns an error the
   caller turns into a stderr diagnostic plus a delete.
4. **Wire the writes.** An optional store field on `Session`, set via
   `SetSnapshotStore` from `main.go` only when the flag is on (nil store = today's
   behavior exactly, which keeps the default path untouched). Call sites: after
   `startRun` returns (interrupted=true), after `finishAgentOutcome`, and at the
   end of `approvePlan` / `rejectPlan`. Every call builds under `mu` and writes
   after releasing it. A write error never fails the turn: report once as a
   diagnostic and disable further writes for the process, so the user is not
   spammed every turn by a full disk.
5. **Wire the deletes.** `Session.Reset` deletes the snapshot — otherwise
   `session.reset` claims to have cleared state that silently comes back at the
   next launch, which is worse than not having the feature. Update the
   `session.reset` event message accordingly. Add `/forget` to `Session.command`
   (delete + `Reset`, with whatever decision 4 chose), and add it to the command
   list in `chat.go`'s fallback help line.
6. **Startup restore.** In `cmd/athena/main.go`, after `chat.NewSession(loop)`
   and before `stdio.Serve`: when enabled, load, apply the TTL, call `Restore`,
   and keep a small restore summary. Under decision 5's `/resume` variant, hold
   the loaded snapshot and restore on the command instead.
7. **Protocol.** Add a `session.restored` event carrying counts only — restored
   message count, the goal text, `discarded_plan.actions`, `interrupted` — to
   `protocol/athena.v1.schema.json` and the Go `Event` struct in
   `internal/transport/stdio/server.go`, emitted in response to `engine.hello`.
   Update `schema_test.go`'s type list in the same change or the F-01 drift test
   fails, which is the point of it. If decision 5 picks `/resume`, add the
   matching request type too.
8. **Tests**, extending what exists rather than adding a new harness:
   - `session_test.go`: restore rehydrates goal and history; a restored session
     has no pending plan; approving immediately after a restore returns *"There
     is no pending change to confirm."*; the first plan minted after a restore is
     `plan-1` and no pre-restart ID is ever accepted by `takePlan`.
   - `compact_test.go` or `snapshot_test.go`: the snapshot never exceeds the cap,
     never splits a rune, and never contains a vault-context block or a plan ID.
   - `snapshot_test.go`: atomic write leaves 0600; a corrupt file yields a clean
     "start fresh"; TTL expiry deletes rather than restores.
   - `stdio/schema_test.go`: the new event type is in the schema.
9. **Docs in the same change** (AGENTS.md: a change is not complete while its docs
   describe the old behavior): rewrite the *"Pending task state is currently
   session-local"* paragraph in `docs/chat/README.md`; add `session.json` to the
   **Local files** list in `docs/configuration/README.md` with the flag, the
   retention rule, and the exact wipe instructions; document the new event in
   `protocol/README.md`. Mark M-03 `done` and note in M-04 that plans are
   deliberately not restored.

What M-04 must **not** do: persist `agent.RunState`; persist plan IDs or action
bodies; restore a pending plan; put session state in `ui.json`; make the Ink
client read the snapshot file; or ship with the flag defaulted on.
