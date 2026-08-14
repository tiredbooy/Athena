# Chat workflow

## Ownership

`internal/chat` owns conversation history, structured pending tasks, retrieval,
model/read-tool coordination, approval plans, and user-facing outcomes. The
generic bounded lifecycle lives in `internal/agent`; the TUI only renders events
and sends user input or approval decisions.

## Turn flow

```text
user input
  → exact safe shortcut when applicable
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

Every turn is bounded to two minutes. The parent UI context can still cancel it
earlier, for example when the user presses Escape.

Bulk, destructive, and whole-note replacement plans enter a pending state.
`/confirm` executes that exact plan; `/cancel` drops it. Clearing the session
also drops a pending plan.

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

Pending task state is currently session-local and is cleared with the session.
Athena does not write private conversation transcripts to a new JSON file by
default. Durable session recovery should be a separate opt-in design with
retention and privacy rules.

## Recovery and compaction

Transient model transport failures retry once. If Ollama reports that a reply
stopped due to a length/context limit, Athena compacts the active state and
continues once with the original goal and partial answer. Both paths inherit
the UI request context, so `Esc` cancels them immediately.

An input overflow is handled earlier in the Ollama provider: Athena grows the
runtime context once to the smallest safe window and caches it for that model.
This is distinct from output continuation—the former makes the request fit;
the latter resumes a response that already began.

Older history compacts automatically after a bounded size; `/compact` performs
the same deterministic compaction on demand. It keeps the system prompt and
recent turns verbatim, while retaining a short factual memory of older turns.

## Current shortcuts

Exact single-purpose folder creation/deletion can bypass model JSON. Compound
requests stay agentic and go through the planning model.

## UI boundary

The primary Ink TUI and Bubble Tea fallback consume the same `chat.Session`.
The stdio engine emits typed activity, response, plan, error, cancellation, and
completion events. Neither UI owns conversation policy or vault actions.
