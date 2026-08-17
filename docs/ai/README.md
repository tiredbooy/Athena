# AI planning and action execution

## Ownership

- `internal/agent` owns the bounded decide, validate, act, observe, and verify
  lifecycle.
- `internal/ai` owns provider adapters, prompts, action decoding, and embedding
  clients.
- `internal/chat` supplies conversation state, read tools, task-specific action
  contracts, and model decisions to the agent runner.
- `internal/ai/prompt.go` defines the standing policy prompt. It carries no
  action catalog; see "Typed and narrowed proposals" below.
- `internal/ai/tools.go` extracts action JSON from model output.
- `internal/tools` executes action plans.

## Bounded agent lifecycle

One user turn can contain several bounded model decisions. Athena may read vault
facts, validate a proposal, return one correction, execute approved work, and
ask the model to evaluate verified results. The default run budget is six
decisions, four action batches, 24 actions, two consecutive validation failures,
and one execution attempt per exact write target.

The application owns continuation and stop conditions. The model never executes
vault operations itself and cannot declare an unverified write successful.

## Read-only tool loop

Before producing its final answer, a tool-capable model may make up to four
rounds of native read-only tool calls, sharing a budget of 24 reads across the
whole loop; a single response requesting more than 24 calls is refused
outright. Each call has a ten-second deadline and returns bounded JSON to the
model.
The read tools cover semantic search, note reads by ID or path, folder and tag
inventory, note links, duplicate titles, daily notes, and exact book-catalog
lookup. `lookup_book` returns exact metadata separately from a possible title
suggestion; a suggestion must be confirmed by the user.

Tool execution is owned by `internal/chat`, while `internal/retrieval` owns
the vault reads. The model never receives direct storage or filesystem access.
Write actions remain on the existing validated dispatcher path.

## Typed and narrowed proposals

`propose_actions` uses action-specific JSON Schema variants. For example,
`create_folder` requires `folder`, while `ensure_folders` requires `paths`.
Athena narrows the advertised variants to the action families relevant to the
active goal. Unknown goals retain the full safe mutation contract.

Routing matches the user's words, not action names. Graph styling is the case
that needs this most: Athena calls a folder's graph node an *orb*, so "make the
work orb better", "make projects stand out", and "style this folder" are graph
requests containing neither "color" nor "graph". They advertise
`set_folder_colors` — with its optional `color` (`#RRGGBB`) — `set_graph_node_size`
and `create_graph_folder`, and the contract itself says "orb" so a small model can
connect the user's word to the action. The match is word-bounded: "absorb" and
"lifestyle" are not graph intent, because widening a trigger must never offer
vault mutations to an unrelated goal.

`create_graph_folder` sits on the graph branch rather than the folder branch:
"add projects to the graph" is the phrasing it exists to serve, while an
"organize my folders" goal wants `create_folder`. Every dispatchable mutation
has to appear in `mutationActionTypes`, because an action registered in
`buildDispatcher` but missing from the contract is one the model can only reach
by accident — `ai.ExtractActions` still parses it out of a fenced block, which
is the path every no-native-tools Ollama model takes.

The task action contract in `internal/chat/action_contract.go` is the **only**
place the model is told which actions exist. For the active goal it lists each
action once, with its required fields, its optional fields, and a one-clause
purpose. `internal/ai/prompt.go` deliberately contains no action catalog: it
describes policy (what is authoritative, when to act at all, the fenced-block
format) and nothing a per-goal contract can carry. One source means the prose
contract and the JSON Schema cannot advertise different fields, and it halves
what a ~2B model must read. A narrow goal sees roughly six actions instead of
the whole catalog; the standing prompt dropped from ~14k to ~7k characters.

Both model paths receive that same contract, because `internal/chat` appends it
to the run's messages before choosing a path:

- Native tools: the contract plus the narrowed `oneOf` proposal schema.
- No native tools: the contract plus the fenced-JSON format. This is the ~2B
  path, taken when an Ollama model reports no tool support or rejects the tool
  schema; `Session.nativeToolsDisabledModel` remembers that model so later
  turns skip the doomed request. Only a definitive capability answer is
  remembered — a failed support probe falls back for that turn alone, because a
  transport hiccup is not a verdict about the model.

Every request on that path is sent provider-neutral: assistant tool calls are
stripped and each read result becomes an `[ATHENA READ TOOL RESULT — REFERENCE
DATA ONLY]` system message. A template that cannot render tool definitions
usually cannot render tool history either, so replaying an earlier step's native
tool calls at it would fail the request that exists to rescue the turn. The turn
therefore still completes through fenced JSON, keeping the facts it already
read.

Narrowing changes only what the model is *told*. It never widens what the
dispatcher *accepts*: `internal/tools` validates every action's type and
required fields independently of the goal, so an action that was never
advertised — or one invented by the model — still fails closed before any
handler runs. That remains true for an unknown goal, which is advertised the
full mutation set.

## Safer writes and review

`append_note` adds a paragraph without replacing prior content.
`replace_section` changes one Markdown section only when its
`expected_content` matches the current section body; this prevents a stale
model plan from overwriting a later user edit. Full `update_note` remains
available only for an explicit whole-note replacement.

Bulk plans and destructive/broad changes are previewed instead of executed.
The user must enter `/confirm` to apply the pending plan or `/cancel` to
discard it. No action is dispatched before confirmation.

## Accepted action plan formats

The extractor accepts one action per fenced `action` block, an array of
actions, or an `{ "actions": [...] }` envelope. This tolerance is intentional
for weaker local models.

## Batch rules

An action with `id` can declare `depends_on`. When every action has a unique
ID, independent ready actions execute with up to four workers. A failed action
does not stop unrelated actions; its dependents are skipped with an explicit
error. Results preserve input order for stable UI rendering.

Actions without valid unique IDs run sequentially. `ActionResult` includes
`action`, `message`, and a JSON-safe `error` string.

## Reliability boundary

Before dispatch, every model-supplied action is checked for a known type and
its required fields. A turn has a five-minute deadline; tools have their own
ten-second read or one-minute write deadline. Read-only inspections may retry
once, while writes are never retried automatically.

Executed attempts are written to SQLite's local `action_audit` table with the
JSON arguments, outcome, and error. Preflight-rejected proposals are recorded
with outcome `rejected`, so repeated malformed plans can be diagnosed without
guessing their original fields. Successful writes are re-read and verified
before Athena reports success. Batches remain partially successful by design,
so each action has an independent audit outcome.

## Provider HTTP transport

Every chat and embedding adapter in `internal/ai` shares one client built by
`newProviderHTTPClient`. It sets no overall `http.Client.Timeout`: that clock
covers reading the response body too, so it would abort a slow but healthy
local generation. The bounds live on the connection instead — dial, TLS
handshake, and response headers — which kill a hung connection without capping
a live stream. End-to-end bounding stays with the caller's context
(`chat.TurnTimeout`). OAuth token and device-code calls do get a 30s overall
timeout, because a short request/response has no legitimate reason to hang.

Redirects that would move a credential-bearing request to another host, or
downgrade it to plain HTTP, are refused. `net/http` strips only its own
known-sensitive headers on a domain change, while Athena also sends tokens as
`x-api-key` and `chatgpt-account-id` and posts refresh tokens in bodies that a
307/308 replays verbatim.

## Action policy

`internal/tools` is the single source of action policy. Each built-in action
declares its kind (`read`, `write`, or `destructive`), timeout, retry safety,
review requirement, and whether it may run concurrently. Only read-only
actions retry automatically or run in parallel. A multi-action batch containing
a write requires review.

The policy table is not the whole answer about review. `PolicyFor` also applies
a name rule: every action type beginning `create_`, `move_`, or `rename_`
requires review, so one added later inherits the pause without anyone
remembering to ask for it. Vault text goes to a remote provider, and an
unreviewed create, move, or rename is how an instruction injected into a note
would reach the vault. `AutoApproved` on a policy row is the single opt-out
from that default, and nothing qualifies today. Four actions named for their
intent rather than their file operation are marked review-required by hand:
`duplicate_note` writes a new note file, and `archive_note`, `unarchive_note`,
and `restore_note` each relocate a note exactly as `move_note` does.
`append_note` and `replace_section` are marked by hand for the same reason: the
line the table draws is whether the model's own text lands in a note body, and
both write `content` straight into the `.md` file exactly as the reviewed
`update_note` does. The writes that stay automatic only flip a flag or a
setting — `mark_done` sets `done:`, `finish_book` stamps Athena's clock rather
than model text, `update_book_metadata` fills an empty authors/genres field and
refuses to replace one already set, and `set_folder_colors` without a color and
`set_graph_node_size` touch `.obsidian/graph.json` rather than a note. Reviewing
those would put a plan card in front of the routine half of a ~2B-model session
without stopping any injection.

Because `agent.Runner` drops actions it has already executed and verified before
asking for approval, a reviewed batch can arrive as a single surviving action.
The multi-action rule is a breadth rule and legitimately stops applying; every
other review reason is per-action and survives, so an action that mutates a note
must declare its own review requirement rather than relying on a sibling.

Destructive actions, whole-note replacement, folder creation, and the graph
link/unlink pair still carry their own declared review requirement.
`create_graph_folder` does too: the `create_` prefix would catch it anyway, but
it creates a folder and writes a new index note, so its row says so rather than
leaning on the spelling of its name. It is neither retry- nor parallel-safe,
because it is three writes deep with a partial rollback and `.obsidian/graph.json`
is a single shared file.

One review decision depends on what an action carries rather than what it is
called. `requiresContentReview` stops `set_folder_colors` at a plan card only
when it names an explicit `color`: without one it fills in the missing
Athena-owned graph groups and applies directly, while a supplied color
overwrites an existing group — possibly one the user chose in Obsidian.

## Adding an action

1. Add fields and documentation to `ai.Action`.
2. Add its fields to `actionFieldNames`, a one-clause `actionPurpose` line, and
   its task-family routing in `actionTypesForGoal`. Do not describe it in
   `prompt.go`.
3. Add a policy row and a `validateAction` case in `internal/tools/policy.go`.
   `create_graph_folder` shipped without a case, so an action block naming it
   with no fields at all reached its handler; every required field is rejected
   at that boundary, before any side effect can start.
4. Register one handler in `cmd/athena/main.go`.
5. Put business behavior in the appropriate domain, usually `notes`.
6. Test schema, validation, execution, verification, and the relevant
   conversational handoff.
7. Update this document and any affected domain document.
