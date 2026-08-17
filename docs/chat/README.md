# Chat workflow

## Ownership

`internal/chat` owns conversation history, structured pending tasks, retrieval,
model/read-tool coordination, approval plans, and user-facing outcomes. The
generic bounded lifecycle lives in `internal/agent`; the TUI only renders events
and sends user input or approval decisions.

## Turn flow

```text
user input
  → exact listing shortcut when applicable
  → resume an application-owned pending task when one exists
  → vault catalog + semantic context for the complete active goal
  → bounded model decision with read and decision tools
  → validate a typed, task-narrowed action proposal
  → pause for review or execute allowed work
  → feed verified results back to the model
  → finish, propose remaining work, or ask one clarification
  → save concise history
```

An empty visible reply is explicit: the UI says that the model returned no
visible answer and that no vault changes were made. Action outcomes are always
shown, because a model confirmation does not prove execution succeeded.

Every turn is bounded to five minutes (`chat.TurnTimeout`). Local models can
take minutes to load or answer, especially after a rejected native-tool attempt
falls back to fenced JSON. The parent UI context can still cancel the turn
earlier, for example when the user presses Escape.

Bulk, destructive, and whole-note replacement plans enter a pending state.
`/confirm` executes that exact plan; `/cancel` drops it.

A plan ID is single-use, and it stays single-use if applying it is interrupted.
When the resumed run is cancelled or fails part-way, Athena does not discard the
approval: the actions the execution ledger never verified come back as a **new**
pending plan carrying the same run state, and the returned error names it. The
actions that already succeeded are not in it, so a second `/confirm` retries only
the outstanding work and can never repeat a verified change. `/cancel` discards
what is left. The verified record of the interrupted attempt is published, so a
partly-applied plan is visible rather than hidden behind an error.

`Session.Reset` returns the session to its opening state: history back to the
system prompt, no pending plan, no pending task. It is refused while a turn is
running.

Only the Bubble Tea fallback reaches it today, by calling it directly for
`/reset`. The engine accepts a `session.reset` protocol request (`F-03`), but
the Ink client does not send one yet — its `/clear` remains view-only, so engine
history and any pending plan or task survive it.

Clearing a transcript in the UI is a different thing and must be labeled that
way: it leaves every one of those engine facts intact.

While a plan is pending, review blocks new goals, not inspection. `/confirm`
and `/cancel` decide the plan; `/doctor`, `/models`, and `/compact` still run,
because each only reports engine state — `/compact` included, since a pending
plan carries its own resumable run state and does not read conversation
history. Everything else, including `/model` and any new request, is answered
with the review prompt instead of running. `/model` is excluded on purpose:
swapping the provider under an approved-but-unapplied plan changes who would
execute it, and `/reindex` is excluded because rebuilding the index is work,
not inspection.

## Index health and `/reindex`

A vector is only comparable to vectors from the same embedding model. Change
`embed_model` — by editing the config or by switching provider — and every
search compares a new query against an index the old model built: no error, just
answers that look plausible and are not. Nothing else in Athena can notice this.

`/doctor` reads `notes.Service.IndexHealth()` and prints one **Embedding index**
line:

| State | Line |
| --- | --- |
| the recorded model differs from the configured one | `!` — names both models and points at `/reindex` |
| no rebuild was ever recorded | `✓` — says what built the vectors is unknown |
| the recorded model matches | `✓` — names the model and the vector width |

Unknown is deliberately not a problem. Warning about every vault that has never
been rebuilt would train the user to ignore the one line that means search is
actually broken. The line is omitted when no vault service was wired into the
loop, since nothing could rebuild the index anyway.

`/reindex` rebuilds every vector and reports per-note progress through the same
status callback a turn uses, so a client needs no second progress channel. It is
a **user command and never an action type**: re-embedding a whole vault is the
most expensive thing Athena can do, and a weak local model must not be able to
spend that on a guess.

It respects the request context and, like every turn, runs inside
`chat.TurnTimeout`. A vault too large to rebuild in five minutes needs the
background job that `notes.Service`'s `jobs` row is already shaped for; that
does not exist yet.

The Ink TUI's command palette does not list `/reindex` yet — typing it works.

## The verified execution ledger

Every turn that mutated the vault appends its verified execution record to the
user-visible outcome, and carries the same record structurally as `ledger` on
the terminal protocol event.

This is not the model's account of itself. `agent.Outcome.Ledger` comes from the
tool layer's results on every outcome path — normal finish, safe stop, plan
approval, and the error a cancelled or failed run returns — so a terse reply, a
wrong claim of success, a cancellation, or a 2B model that says nothing at all
cannot hide what happened. A read-only turn produces no ledger, and `safeStop`
already writes its own record, so the text is never duplicated.

Each record is the action type, its target, `succeeded` or `failed`, and the
tool's message or error.

A published record always belongs to the turn that produced it. A turn drops the
previous one as it begins, so a turn that reached no execution — a command, the
listing shortcut, a refused input, an approval with nothing pending — reports no
ledger instead of republishing an earlier turn's, which would describe changes
this turn never made.

The ledger is also what decides how much of an interrupted approved plan comes
back for review, and what compaction reports as the last verified actions. A
`succeeded` record is the only proof an action ran, so nothing recorded that way
is ever proposed again. "The same action" is one definition, `agent.ActionSignature`,
used by both the runner and the interrupted-plan check, so they cannot disagree
about what already happened.

That definition hashes only the fields the action's own handler reads — the
target, plus the arguments that decide what lands there. A stray field a weak
model adds to an already-executed action (a `title` on an `append_note`, a
folder spelled as `path`) does not make it new work, which is what stopped the
same paragraph being appended twice. A field the handler does read still
separates two actions: `create_book` passes ISBN to the metadata resolver, so
two ISBNs are two books rather than one already-verified book. Every action
type registered in `buildDispatcher` has a case; a new one needs its own.

## Structured activity

Every real step emits a typed `activity` event rather than a status sentence
the UI has to interpret. A step carries `phase`, `state` (`started` /
`succeeded` / `failed`), and — where it applies — `tool` and `target`:

| Step | Phase | Tool / target |
| --- | --- | --- |
| Vault retrieval | `retrieving` | `vault_context` / the active goal |
| Read tool call | `reading` | the tool name / path, note reference, or query |
| Decision validation | `validating` | — |
| Plan approval pause | `approval` | — |
| Action execution | `executing` | action type / action target |
| Observation and verification | `observing`, `verifying` | — |

`internal/agent` owns the run lifecycle phases; `sessionAgentDriver` reports
read tools; `chat.Session` reports the retrieval step. Read-tool failures are
detected from the tool's own error payload, not from a nil Go error, because
read tools return failures as JSON.

The English `message` field remains for the Bubble Tea fallback. It is not a
contract — a client must not parse it. See
[the protocol README](../../protocol/README.md#activity-events).

`activityEvent` still infers a phase from legacy English status lines that have
not been converted yet. Those lines are informational detail inside a step and
carry no `state`.

## Concurrency

`chat.Session` holds two locks, because it has two jobs:

- `turnMu` serializes whole turns. One submit or plan approval runs at a time,
  and it is held across model I/O for the full turn.
- `mu` guards session state: history, pending plan, pending task, and the ID
  counters. Every access holds it briefly and never across a network call.

Lock order is `turnMu` then `mu`; nothing takes them the other way.

The split exists because a single lock across a five-minute turn made the
engine unresponsive: `/models`, the model/provider footer, plan state, and any
UI callback that read the session all queued behind generation. With the split,
those stay answerable while a turn runs.

The inspection commands allowed while a plan is pending are a different thing:
a pending plan is state *between* turns, and those commands take `turnMu` like
any other input. They do not run concurrently with a turn.

What is *not* allowed during a turn is swapping the active provider. The stdio
server refuses `model.select`, `provider.connect`, `provider.oauth.start`, and
`session.reset` while a turn is running, because each would change state the
running turn is already using. `model.list` is read-only and stays allowed.

## Clarification state

A model question is not treated as a completed task. Athena stores a structured
in-memory pending task containing the original goal, pending question, prior
answers, and whether the goal expects a mutation. The next user response is
attached to that state and shown to the model as application-owned JSON.
Native `request_clarification` calls carry this decision explicitly; Athena does
not infer their state transition from the wording of the question. Plain-chat
fallback questions use a conservative wording detector.

This is conversational, not a keyword rule: `yes`, a genre name, a correction,
or a longer explanation all use the same path. If more information is still
missing, the next question replaces the pending question while retaining the
resolved goal. Once the facts are complete, the model should propose a
reviewable plan immediately; it must not ask for a second prose confirmation.
Folder creation paths are shown in that review. The lexical folder-intent guard
remains a backstop only for a future unreviewed creation policy; it does not
replace the user's approval of the displayed plan.

A pending task only disappears when the goal is genuinely resolved. It is
cleared by `/cancel`, by `Session.Reset`, or by a run that answered the goal or
turned it into a reviewable plan. Every interruption restores it: a runner
error, a cancelled turn, and a turn timeout all put the original goal, question,
and prior answers back, so the user's next reply is still read as an answer.
A cancellation that lands after the run's first executed action arrives as a
safe stop rather than an error, so the restore is driven by the turn context
being done, not by reading the reply text.

A run can also end without answering the goal *and* without returning an error:
the runner converts an exhausted step or action budget, a twice-invalid plan, or
a model that could not evaluate its own verified results into a **safe stop**.
That reply reads like an answer, so `agent.Outcome.SafeStopped` marks it
structurally and the pending task is restored on that signal too. The
alternative would have been matching the `"I stopped safely because"` prose,
which is exactly the English parsing this architecture exists to avoid.

One consequence is deliberate and remains open. A new, unrelated goal typed
while a task is pending is still attached to that task as an answer; Athena does
not guess that a request is unrelated. A pending question is often answered with
a bare noun phrase ("Science Fiction", "the chapter three one") that is
lexically indistinguishable from a short new request, so any similarity
heuristic would sometimes reclassify an answer as a new goal and strand the
user. Doing it properly needs an explicit typed intent — a "new request"
affordance in the client, or a `session.newGoal` protocol request — rather than
prose sniffing. Until then, `/cancel` is the way to abandon a pending task
(`M-01` in `tasks.md`).

Pending task state is currently session-local and is cleared with the session.
Athena does not write private conversation transcripts to a new JSON file by
default. Durable session recovery should be a separate opt-in design with
retention and privacy rules.

## Recovery and compaction

Transient model transport failures retry once. If Ollama reports that a reply
stopped due to a length/context limit, Athena compacts the active state and
continues once. Both paths inherit the UI request context, so `Esc` cancels
them immediately.

That continuation carries more than the goal and the partial answer. The
conversation prose is dropped, but every application-owned `[ATHENA …]` system
block survives — the task action contract, the pending-task JSON, the run
contract, and any verified execution observation — as do the read results this
turn already paid for, each truncated to a fact-sized excerpt and re-sent as
reference data rather than as an orphan tool-protocol turn. Without them the
continuation had no vault facts and, since the action catalog left the system
prompt, no action vocabulary at all.

An input overflow is handled earlier in the Ollama provider: Athena grows the
runtime context once to the smallest safe window and caches it for that model.
This is distinct from output continuation—the former makes the request fit;
the latter resumes a response that already began.

Older history compacts automatically after a bounded size; `/compact` performs
the same deterministic compaction on demand. It keeps the system prompt and
recent turns verbatim, while retaining a short factual memory of older turns.

That memory is lossy on purpose — each older turn is truncated to a byte budget,
always on a character boundary so accented, Persian, CJK and emoji text is cut
short rather than corrupted — so compaction
never depends on it for the facts needed to finish the active goal. Those facts
are application-owned, and compaction restates them verbatim in a single
`[ATHENA RETAINED STATE — APPLICATION DATA]` system message placed between the
memory and the recent turns:

- the original active goal and the question Athena is waiting on
- every clarification answer already given
- the last turn's verified execution records

The block is rebuilt from live session state on every compaction, and the
previous copy is dropped, so it never accumulates or contradicts itself. It is
absent when there is nothing to retain: no pending task and no recorded
actions. A conversation with no pending task has no application-owned goal —
each turn is self-contained and its request is still in the verbatim recent
window.

## Current shortcuts

Vault **listing** is the only request that bypasses the model. `isListingRequest`
matches a whole-string listing phrase — case-folded, trailing `.?!` and stray
inner whitespace removed — and answers straight from the retrieval inventory.
Everything else — including folder creation and deletion — goes through the
planning model. Earlier drafts of this document described single-purpose folder
shortcuts; they do not exist.

Matching is whole-string, never prefix or keyword. `list my notes and delete the
old ones` reaches the model, because the shortcut answers only the listing half
and would silently drop the rest. A shortcut is added only when it is read-only,
unambiguous, and complete on its own: `what's in my vault` is deliberately not
one, since the reply lists notes only and would read as a complete vault answer.

## UI boundary

The primary Ink TUI and Bubble Tea fallback consume the same `chat.Session`.
The stdio engine emits typed `activity`, `response`, `plan.ready`, `error`,
`cancelled`, and `completed` events. There is no token-delta event: `response`
carries the whole reply once. Neither UI owns conversation policy or vault
actions.
