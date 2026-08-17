# S-08 — Permanent delete and empty trash (design)

**Status:** proposal. Nothing here exists yet. `docs/` describes today; this file
describes a decision the owner has not made.

**Scope:** `internal/notes`, `internal/storage`, `internal/chat`,
`internal/tools`, `internal/ai/prompt.go`, and (later, Grok's queue)
`apps/tui/src/ui/palette.ts`.

**Related rows:** `S-08` (this design), `L-04` (the implementation that follows),
`V-02` (trash chunks), `S-04` (archive + trash cannot stack), `U-13` (commands
allowed while a plan is pending).

---

## 1. What exists today

### Trashing is a move, not a delete

`notes.Service.TrashNote` (`internal/notes/files.go:151`) does four things:

1. refuses outright if the note is archived — archive and trash cannot stack, or
   the recorded origin would itself be a relocation (S-04),
2. computes the note's vault-relative path (`utils.RelVault`),
3. moves the file to `<vault>/.trash/<that relative path>` with
   `utils.MoveFile`, which refuses to clobber an existing destination,
4. sets `TrashedFrom` to the original relative path and writes the row back
   through `saveMovedNote` (`files.go:24`), which moves the file back out of
   `.trash/` if that row update fails (V-04).

`RestoreNote` (`files.go:188`) is the exact reverse: move the file back to
`TrashedFrom`, clear the marker, update the row through the same helper. The
SQLite row and every `chunks` row survive untouched — that is deliberate, and
the comment on `models.Note.TrashedFrom` says so: restore is meant to be a pure
move.

So today Athena has **no delete at all**. It has a move into a hidden folder.

### Trash is excluded from reads, but only at read time

- `NoteStore.All` (`internal/storage/notes.go:88`) filters `trashed_from = ''`,
  and so does its body-free sibling `NoteStore.AllMeta` (`notes.go:106`, V-05).
  Every catalog path runs through `AllMeta`: `retrieval.Service.buildCatalog`
  (`internal/retrieval/context.go:161`) → `Inventory`, `BuildContext`, the
  `list notes` shortcut in `chat.isListingRequest`.
- `ChunkStore.Searchable` (`internal/storage/chunks.go:42`) joins `notes` and
  keeps only `n.trashed_from = ''`. That is V-01. The comment there is honest
  about why the JOIN exists: *trashing does not remove the vectors*, so the
  filter has to happen on every search.
- The read-by-ID paths that neither query covers now share one guard,
  `liveNoteByID` (`internal/retrieval/read_tools.go:67`), which turns a trashed
  row into an error instead of a body. `NoteByID`, `NotesByID`,
  `NoteByRelativePath`, and the subject note of `Links` all route through it, so
  a model still holding a pre-trash `note_id` cannot read the body back through
  any of them. See [Storage and retrieval](../retrieval/README.md#trashed-notes-never-enter-rag).

### The trash is write-only

`NoteStore.Trashed()` (`internal/storage/notes.go:128`) has exactly one caller,
and it is not a listing: `notes.Service.Reindex` (`internal/notes/reindex.go:64`)
appends the trashed rows to the set it re-embeds, deliberately, because a
reindex that skipped them would drop the vectors a restore depends on.
**Nothing shows the trash to anyone** — not a slash command, not a model tool,
not `/doctor`, not the Ink TUI.

The consequence is bigger than "no empty-trash button": **`restore_note` is
effectively unreachable across sessions.** It needs a `note_id`, and after the
conversation that trashed the note ends, there is no surface anywhere in Athena
that will tell you that ID. Search excludes it, inventory excludes it, nothing
lists `.trash`. The note is gone in every practical sense while still costing a
row, a file, and a JOIN on every search.

### What the model can do to trash today

`trash_note` is `ToolDestructive` with `RequiresConfirmation: true`
(`internal/tools/policy.go:110`), so it always stops at a plan card.
`restore_note` is a `ToolWrite`, but it now carries `RequiresConfirmation: true`
as well (`policy.go:102`), so it stops at a plan card on its own rather than only
when batched with another write (`RequiresConfirmation` in `policy.go:160`
reviews any multi-action batch containing a write). Both are registered in
`buildDispatcher` (`cmd/athena/main.go:469`, `:480`) and both have post-write
verifiers that re-read the row (`verifyWrite`, `main.go:532`).

The prompt no longer describes trash as soft or reversible anywhere. What it
says about it is that `.trash` and `archive` are system-managed and must never
be written into (`internal/ai/prompt.go:48`), and that a vague "clean up" is not
authority to batch-trash (`prompt.go:52`).

### The relevant storage facts

- `chunks.note_id` is declared `REFERENCES notes(id) ON DELETE CASCADE`
  (`internal/storage/sqlite.go:24`) **and** `foreign_keys` is forced on for every
  pooled connection through the DSN (`connectionPragmas`, `sqlite.go:90`). So
  deleting a `notes` row already deletes its chunks, atomically, in the same
  SQLite transaction.
- There is no `NoteStore.Delete` of any kind. Nothing in the codebase deletes a
  note row.
- There is no `trashed_at` column. `updated_at` is bumped by the `Update` that
  marks the note trashed, so it is a decent proxy and not a real timestamp.
- The `jobs` table and `storage.JobStore` are no longer unused (V-03).
  `notes.Service.Reindex` opens a job row, tracks progress on it and closes it,
  and `IndexHealth` reads the last *finished* one to compare the embedding model
  that built the index against the configured one
  (`internal/notes/reindex.go:52`, `:105`). `/reindex` runs it and `/doctor`
  reports it (`internal/chat/session.go:626`, `internal/chat/doctor.go:41`).
  Reindex is still the only thing that writes a job.

---

## 2. The problem

Three distinct problems, and they are worth keeping separate because they have
different fixes.

**a. A soft delete with no hard delete is not a delete.** A user who trashes a
note containing a password, a medical note, or someone else's private
information has moved the file inside their own vault. The bytes are on disk in
`.trash/`, the full body is still in the `notes.content` column, and the
embedding of that body is still in `chunks`. Athena currently offers no way to
finish that.

**b. Trash is unbounded and invisible.** Nothing lists it, nothing reports its
size, nothing ever removes anything. Every trashed note permanently adds a row
to the `chunks` JOIN that `Searchable` runs on every single semantic search.
This is small for one note and not small for a year of tidying.

**c. Restore is a promise the product does not keep.** `docs/notes/README.md`
states that a trashed note keeps its row and its vectors precisely so restore is
a pure move, and `restore_note` is a registered action the model can propose. It
is only reversible inside the conversation that did it. The listing is not a warm-up act
for the delete — it is the missing half of a feature that already shipped.

And the reason S-08 is `design-first` rather than `todo`: whatever fixes (a) is
**the only operation in Athena with no undo.** Every other destructive action is
recoverable — `trash_note` restores, `delete_folder` only removes empty folders,
`move_folder` moves back. Even `update_note`, which overwrites a body, leaves
the file where it was. A permanent delete is different in kind, and a ~2B local
model that misreads "clear out my old notes" must not be able to reach it. That
is the whole point of `tasks.md` L-04's phrase: *never hooked to a 2B-model
impulse.*

---

## 3. Constraints

Quoted from `AGENTS.md` and the code, with what each one forces here.

> **Soft-deleted / trashed notes must never enter RAG, semantic search, or
> injected vault context.**

Purging can only *help* this, but the design must not accidentally make a
half-purged note visible. Fail states must land outside search, not inside it.

> **Markdown file and SQLite row stay together. Neither is the single source of
> truth.**

A permanent delete must remove both. Removing one is a bug that produces either
an orphan file or a ghost row.

> **Filesystem and SQLite are not one transaction; new multi-step writes need
> compensating undo or a journal.**

This is the hard one. A delete cannot have compensating undo — you cannot
un-`os.Remove` a file. So the rule points at a journal, and §5 argues a narrower
property that satisfies the rule's stated intent (`docs/notes/README.md`: "rather
than silently accepting partial success"). That argument is decision #4 for the
owner, not something this document gets to wave through.

> **A ~2B local model is a first-class target, not an afterthought.**
> **Prefer application-owned state, narrowed action contracts, validation.**

The guard cannot be "the model is instructed not to". It must be an absence of
capability that a wrong token prediction cannot produce.

> **The engine is the source of truth. The TUI is an event renderer.**
> **The UI stays thin.**

Confirmation state lives in the engine, like `pendingPlan` already does. The
client renders and sends; it does not own the "are you sure" logic.

> **Docs describe behavior that exists now.**

`docs/notes/README.md` and `docs/chat/README.md` change in the same commit as
the code, or the change is not done.

Architectural constraints from the code itself:

- **`chat.Loop` now holds `notes.Service`, and only user commands reach it.**
  `NewLoop` still takes `(chatProvider, providers, oauth, retrievalSvc,
  dispatcher, cfg)`, but `SetNotes` (`internal/chat/chat.go:52`, wired from
  `cmd/athena/main.go:141`) added the handle for V-03, and `/doctor` and
  `/reindex` are its only callers. The comment on that setter states the rule an
  engine-side trash command would inherit: the model's vault writes go through
  the dispatcher, so anything reachable only through `l.notes` is out of the
  model's world by construction.
- **`protocol/athena.v1.schema.json` is enforced.** `TestSchemaListsEveryRequestType`
  and `TestSchemaListsEveryEventType` (`internal/transport/stdio/schema_test.go`)
  fail if Go and the schema disagree. A new request type is a schema change in
  the same commit, by rule.
- **A pending plan swallows every input outside a small allowlist**
  (`session.go:97`, documented at `docs/chat/README.md:58`). U-13 has landed:
  `/confirm` and `/cancel` are handled before the block, and
  `inspectionCommandsWhilePlanPending` (`session.go:541`) admits `/doctor`,
  `/models` and `/compact` because each only reports engine state. The purge
  command must stay outside that allowlist — "inspection commands only" must
  mean it.

---

## 4. Options

The three options differ in **surface** (how the operation is invoked), not in
the delete mechanics — §5 uses the same purge routine either way. Presented
simplest → scalable → production-grade, as `AGENTS.md` requires.

### Option A — user-typed slash command, no action type (simplest)

`/trash` lists the trash. `/trash empty` prints the full list plus a one-time
confirmation phrase. `/trash empty confirm <count>` performs the purge.

**How it works.** `Session.submit` already routes any input starting with `/` to
`Session.command` *before* it touches the model (`session.go:92`). Adding two
cases to that switch gives a code path from the user's keystroke to the purge
with no model in it anywhere. No new action type, no dispatcher registration, no
protocol change, no prompt vocabulary. The Ink client already forwards unknown
slash commands to the engine as `session.submit`, so this works on day one with
zero TUI changes; Grok later adds a palette row.

**Why the model cannot reach it.** Not because a flag says so — because the
capability does not exist in the model's world:

1. No `empty_trash` / `permanent_delete` string is in `mutationActionTypes`
   (`action_contract.go:11`), `typedActionSchema`, `actionPolicies`
   (`policy.go:59`), `buildDispatcher`, or `ai/prompt.go`.
2. If a model invents the name anyway, `Dispatcher.Validate` passes
   `d.handlers[action.Type] != nil` as `known`, and `validateAction` returns
   `unknown action type` (`validateAction`, `policy.go:142`). It fails closed
   before any handler runs, and `RecordRejectedPlan` writes the attempt to
   `action_audit` so you can see that the model tried.
3. The only writer of `Session.Submit`'s `input` is a user interface. The stdio
   server passes `request.Input` from the client; `Loop.Run` passes a scanned
   stdin line. **Model output is never fed back into `Submit`.** That invariant
   is what makes a string channel safe here, and it must be written down, because
   a future "let the agent drive itself" feature would silently break it.

**Cost.** The confirmation is plain text in the transcript rather than a
dedicated destructive UI mode. The list of what will be destroyed is formatted
prose, so Ink cannot style it as a danger surface until Grok does Option B's
rendering.

**How it fails.** A user types the confirm phrase without reading the list.
Mitigated by making the phrase carry the count (`/trash empty confirm 7`) so it
cannot be blind-repeated from muscle memory, and by re-reading the trash at
confirm time and refusing if the count moved. It does not protect against a user
who genuinely wants to destroy the wrong seven notes — nothing does. That is why
this is user-only.

### Option B — dedicated protocol requests and an Ink confirm mode (scalable)

Add `trash.list` and `trash.purge` request types plus `trash.listed` /
`trash.purged` events. The engine issues a single-use purge token with the
listing (the same shape as `PendingPlan.ID` and `takePlan`, `session.go:607`).
Ink renders a dedicated destructive mode: a red-bordered list, a required typed
token, Esc to back out.

**How it works.** Same purge routine underneath. The difference is that the
irreversible operation gets its own wire message, so a client can never produce
it by accident from free text, and the UI can look as dangerous as the operation
is.

**Cost.** Two schema entries and the drift tests, engine handlers, and — the real
cost — **it is not usable until Grok builds the mode**, because the engine would
now expect a request type the current Ink client never sends. Cross-queue work
on a feature that is not blocking anything. It also splits trash handling across
two request types for an operation a user runs a handful of times a year.

**How it fails.** Token handling bugs make purge impossible rather than
dangerous, which is the correct direction to fail. The real failure is schedule:
a half-landed B is worse than a whole A.

### Option C — B plus a journal and an audit trail (production-grade)

Option B, and additionally: each purge writes a row to the unused `jobs` table
(type `trash.purge`, payload = the note IDs and paths) before touching anything,
updated to `done`/`failed` after; and every purged note gets an `action_audit`
row naming its title and original path.

**How it works.** A crash mid-purge leaves a `running` job row. On next start (or
next `/trash`), the engine sees it, re-runs the purge for the listed IDs — every
step is idempotent (§5) — and closes the job. `action_audit` answers "what did I
destroy last Tuesday" months later.

**Cost.** The most machinery for the least common operation. And the audit trail
is not obviously a feature: **a permanent record of the titles and paths of the
notes you permanently deleted is itself a durable trace of the content you asked
to be gone.** For a note deleted for privacy reasons, the audit row is the wrong
outcome. That is not a technical tradeoff; it is the owner's call (decision #5).

**How it fails.** A stale `running` job from a crash that actually completed
causes a harmless re-run. A journal that is written but never read is worse than
no journal, because it looks like safety.

---

## 5. Recommendation

**Take Option A.** Build the purge in `internal/notes`, expose it only through a
typed `/trash` command in `Session.command`, and give the model no name for it at
all. Do not add a protocol type, do not add an action type, do not add a journal.

Why, concretely:

**The strongest guard available is absence, not policy.** Option B's dedicated
request type is not actually a stronger guard than A against the threat L-04
names — the model cannot emit a stdio request either way; the client is what
sends both. What stops the model in both options is the same thing: no action
type, no handler, fail-closed validation. B buys better *rendering*, not better
*safety*, and rendering can be added later without changing the engine's
semantics.

**The delete mechanics are safe by ordering, and idempotent by construction.**
Per note, in `notes.Service.PurgeTrashedNote(id)`:

1. Re-read the row by ID. **Refuse if `TrashedFrom == ""`.** This single check is
   what makes a mis-supplied ID harmless: only something already in the trash can
   ever be purged.
2. **Refuse if the file path is not inside `<vault>/.trash/`.** Compute
   `filepath.Rel(filepath.Join(vaultPath, ".trash"), n.Path)` and reject anything
   starting with `..`. `n.Path` is data from the database being handed to
   `os.Remove`; this is a trust boundary and gets validated like one.
3. `os.Remove(n.Path)`. Treat `os.IsNotExist` as success — the file being already
   gone is the desired end state, not an error.
4. `DELETE FROM notes WHERE id = ? AND trashed_from != ''`. The `AND` clause is
   the concurrency guard: if a restore landed between step 1 and here, zero rows
   match and the purge reports "skipped, note was restored" instead of destroying
   a live note. Chunks disappear with the row through the existing
   `ON DELETE CASCADE` plus the `foreign_keys(1)` DSN pragma — no second
   statement, no second failure mode.

Every step tolerates having already happened, so re-running the purge is always
safe.

**On the journal rule.** `AGENTS.md` requires compensating undo or a journal for
multi-step writes, and the rule's stated purpose (`docs/notes/README.md`) is to
avoid *silently* accepting partial success. This design meets that purpose
without a journal, and it is worth understanding why rather than taking it on
faith:

- Compensating undo is impossible for a delete, so that half of the rule is out.
- The remaining state after any crash **is** the trash list. Whatever was not
  purged is still in `Trashed()` and still shown by `/trash`. The operation's
  progress is readable from the data itself; a journal would be recording what
  the data already says.
- The only inconsistent state a crash can produce is a row whose file is already
  gone (crash between step 3 and step 4). `/trash` flags those entries
  explicitly ("file already removed"), a re-run cleans them, and while they exist
  they are excluded from search and inventory by V-01's existing filters.
- Nothing reports success for a note whose two steps did not both complete.
  `EmptyTrash` returns a per-note result and never aborts the loop on one
  failure.

That is a deliberate deviation from the letter of a standing rule, so it is
decision #4 below rather than a decision this document makes. If the owner wants
the journal, Option C's `jobs`-table version is the way to add it and it does not
change anything else in this design.

**On retention: on-demand only. No age or size expiry.** Automatic expiry means
Athena irreversibly destroys user data with nobody watching, to save kilobytes of
Markdown. The genuine cost of unbounded trash is the `Searchable` JOIN, and the
honest fix for that is V-02 (drop or tombstone chunks at trash time), not a timer
that deletes files. `/doctor` should report trash count and total size so the
user knows when to run `/trash empty`; a scheduled deleter is a different
product, and it needs its own written retention rule first.

**On what the user sees before the point of no return.** The `/trash empty`
preview prints, before any confirmation is possible:

- the exact count and total size on disk,
- every note: title, original path (`trashed_from`), and when it was last
  modified (proxy for trashed-at; see decision #6),
- entries whose file is already missing, flagged,
- untracked files sitting in `.trash/` with no database row, counted separately
  and **never deleted** — Athena does not destroy files it never created a row
  for (decision #3),
- one plain sentence: this cannot be undone, and Athena keeps no backup,
- the exact phrase to type, containing the count.

`/trash empty` alone never deletes anything. The confirm re-reads the trash and
refuses if the count changed since the preview.

### The written rule (this is S-08's acceptance criterion)

| Operation | Who can start it | Guard | Reversible? |
| --- | --- | --- | --- |
| `trash_note` | model, in a plan | plan review — `ToolDestructive`, `RequiresConfirmation` | **yes** — `restore_note` |
| `restore_note` | model, in a plan | reviewed only when batched with other writes | yes |
| `archive_note` / `unarchive_note` | model | as above | yes |
| `delete_folder` | model, in a plan | `ToolDestructive` + confirmation; refuses non-empty folders | destroys nothing |
| `update_note` | model, in a plan | `ToolDestructive` + confirmation | **no** — body is overwritten |
| **permanent delete / empty trash** | **user only — a typed command** | **not an action type at all; two-step typed confirmation carrying the count** | **NO — irreversible** |

Read the second-to-last row honestly: `update_note` already destroys content
with no undo. Permanent delete is not the only lossy path in Athena — it is the
only one that removes the note's existence. V-04 (compensating undo on file+DB
writes) is where the `update_note` gap gets closed.

---

## 6. Decisions needed from the owner

1. **Model access — confirm it is user-only, forever.** The recommendation is
   that no model, local or frontier, can invoke permanent delete under any
   review flow; a model that is asked to "delete forever" answers with "run
   `/trash`". The alternative is an `empty_trash` action gated behind a plan
   card, which means one `Y` keypress in Ink's plan mode stands between a 2B
   model's misreading and irreversible loss. Do you accept user-only as a
   standing product constraint (it goes in `AGENTS.md`, not just this file)?

2. **Retention.** On-demand only, as recommended? Or do you want trash to expire
   after N days? If yes, N is your number, and the doc must then state that
   Athena deletes user files on a timer without asking.

3. **Untracked files in `.trash/`.** The recommendation is that `/trash empty`
   only deletes notes it has database rows for, and merely *reports* other files
   found under `.trash/`. Should it instead delete everything under `.trash/`,
   including files Athena never created? (This is the difference between "empty
   my trash" and "delete a folder in my vault".)

4. **Journal vs. idempotent retry.** §5 argues that a crash-safe, self-describing,
   retryable purge satisfies the intent of the `AGENTS.md` multi-step-write rule
   without a journal. Do you accept that, or do you want the `jobs`-table journal
   (Option C) even though re-running `/trash empty` recovers the same state?

5. **Audit trail — this is a privacy tradeoff, not a logging preference.** Should
   each purged note write an `action_audit` row with its title and original path?
   That gives you "what did I delete and when", and it also means the names of
   the notes you deleted for privacy reasons persist in SQLite indefinitely. Pick
   one: a durable record, a count-only record, or nothing.

6. **`trashed_at` column.** The preview would show "last modified" as a proxy for
   "trashed on". A real `trashed_at` column costs one line in
   `migrateNotesColumns` (`sqlite.go:140`) and makes the preview and any future
   retention rule honest. Worth the migration now, or is `updated_at` good
   enough?

7. **Single-note purge in v1.** Besides `/trash empty`, do you want
   `/trash purge <id>` to destroy one specific note immediately? The privacy case
   is real (a password pasted into a note should not wait for a full empty), and
   it is roughly ten extra lines. Or keep v1 to empty-all only?

---

## 7. Implementation sketch (if approved)

Ordered. Each step is small enough to review on its own; steps 1–5 are one
logical change (L-04) and land with their docs.

1. **`internal/storage/notes.go` — add `NoteStore.PurgeTrashed(id int64) (bool, error)`.**
   `DELETE FROM notes WHERE id = ? AND trashed_from != ''`, returning whether a
   row matched. No new table, no explicit chunk statement: the FK cascade plus
   the `foreign_keys(1)` DSN pragma already remove the vectors inside the same
   transaction. Add a comment saying that out loud, because it is invisible
   otherwise.

2. **New file `internal/notes/trash.go`.** Move `TrashNote` and `RestoreNote`
   here from `files.go` so the whole trash lifecycle is one file (`files.go` is
   211 lines and this would push it past the 300-line guideline), and add:
   - `TrashedNotes() ([]TrashEntry, error)` — wraps `noteStore.Trashed()`,
     `os.Stat`s each file for size and missing-ness, and walks `.trash/` once to
     count files with no row.
   - `PurgeTrashedNote(id int64) error` — the four guarded steps from §5, in that
     order.
   - `EmptyTrash() (purged int, failures []error)` — loop over `TrashedNotes`,
     never abort on one failure, and prune directories under `.trash/` that
     became empty (ignore "not empty" errors).

3. **`internal/chat` — the command.** Add a narrow interface in `chat` (satisfied
   by `*notes.Service`) with just the three trash methods, plus a
   `Loop.SetTrash(...)` setter following the existing `SetBookCatalog` pattern,
   wired in `main.go`. Then in `Session.command` (`session.go:461`):
   - `/trash` → formatted listing, including missing-file and untracked-file
     notes.
   - `/trash empty` → the full preview from §5; store a `pendingPurge{count,
     issuedAt}` under `mu`, single-use, cleared by any other command.
   - `/trash empty confirm <n>` → re-read the trash, refuse unless `n` matches
     both the stored count and the current count, then `EmptyTrash()` and report
     per-note results.

   Do **not** add a protocol request type. Confirm that U-13's "commands allowed
   while a plan is pending" whitelist, when it lands, does not include `/trash
   empty`.

4. **`internal/ai/prompt.go` — one line.** Tell the model it cannot permanently
   delete anything, that `trash_note` is the strongest delete it has, and that a
   request to permanently delete should be answered by pointing at `/trash`. This
   is not a guard (the guard is the missing action type) — it stops a small model
   from *narrating* a deletion it did not perform, which would leave the user
   believing a secret is gone. Keep it to one line; R-06 is trying to shrink this
   prompt.

5. **Tests.** In `internal/notes` unless noted:
   - purge removes file, row, and chunks (assert the chunk count for that note is
     zero afterwards — this is the assertion that proves the cascade is real);
   - purge refuses a note with `TrashedFrom == ""` and leaves its file intact;
   - purge refuses a row whose path is outside `.trash/` (construct one directly
     in the store);
   - purge succeeds when the file is already gone (idempotence);
   - a confirm with a stale count is refused (`internal/chat`);
   - **capability absence** (`internal/tools`): `PolicyFor("empty_trash")` and
     `PolicyFor("permanent_delete")` return `ok == false`, and
     `Dispatcher.Validate` rejects an action of either name. This is the test that
     fails if someone later "helpfully" wires the purge into the dispatcher.

6. **Docs, same change.** `docs/notes/README.md`: the note-states section and a
   "Trash and permanent delete" section carrying the §5 rule table.
   `docs/chat/README.md`: the command list. `tasks.md`: mark S-08 `done`, note
   that L-04 is the implementation.

7. **Ink (Grok's queue, separate change).** Add `/trash` to
   `apps/tui/src/ui/palette.ts` with a description that says *permanent*. Nothing
   else is required — unknown slash commands already reach the engine. A
   dedicated destructive-confirm mode is Option B and can be added later without
   changing engine semantics.

### Interaction with V-02 (trash chunks)

Permanent delete works with either outcome V-02 picks, and neither blocks the
other:

- If V-02 **deletes** chunks at trash time, purge's cascade removes zero chunk
  rows and everything still works. Restore has to re-embed.
- If V-02 **tombstones** them, the cascade removes the tombstoned rows.
- If V-02 never lands, purge is the only thing that ever removes a trashed note's
  vectors — which makes `Searchable`'s JOIN the permanent mechanism rather than a
  stopgap, and means the vectors of a note you trashed last year are still in the
  table until you empty the trash.

Worth knowing when V-02 is designed: the "delete at trash, re-embed at restore"
shape makes this feature strictly simpler, because after it there is exactly one
place that removes vectors.
