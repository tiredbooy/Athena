# Multi-device / synced vault (L-05)

**Status: design proposal. This is planned work, not current behavior.**
Current behavior lives in `docs/`. Nothing in this document is implemented.

## Scope

This design targets **one user, several machines, with the vault directory
synced by something the user already runs** — git, Syncthing, iCloud Drive,
Dropbox. Athena does not become a sync engine. The syncer moves bytes; Athena's
job is to stay correct while somebody else moves its files under it.

Explicitly **not** solved here:

- Multiple users sharing one vault.
- Live collaboration or presence (two editors on one note at once).
- Athena-hosted sync, a server, or an account system.
- Automatic three-way merge of note bodies. Athena will detect a conflict and
  show it. It will not resolve it for you.
- Syncing conversation history, credentials, or OAuth tokens. AGENTS.md keeps
  transcripts in memory and credentials in `~/.config/athena`; that stays true.

---

## 1. What exists today

### 1.1 Paths are absolute, in two places

`internal/config/config.go` stores two absolute paths in
`~/.config/athena/config.yaml`:

```go
type Config struct {
    VaultPath string `yaml:"vault_path"`
    DBPath    string `yaml:"db_path"`
    ...
}
```

`defaultConfig` fills them with `filepath.Join(home, "Athena")` and
`appdirs.DataFile("athena.db")`. `validateDataPaths` refuses to start if either
is empty, and deliberately does **not** repair them to defaults — the comment
explains why, and that instinct is correct and worth preserving. But the file
is loaded verbatim: a `vault_path` of `/home/alice/Athena` stays
`/home/alice/Athena` on a Mac where the home directory is `/Users/alice`.

`Config.EnsureDirs` then calls `os.MkdirAll(c.VaultPath, 0o755)`. A wrong
vault path is not an error. It is a new, empty directory.

### 1.2 The notes table stores an absolute path per note

This is the heart of the problem. `internal/storage/sqlite.go`:

```sql
CREATE TABLE IF NOT EXISTS notes (
    id    INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    path  TEXT NOT NULL UNIQUE,
    ...
);
```

and `internal/models/note.go` documents the column exactly:

```go
Path string // absolute path on disk, inside VaultPath
```

Every write path builds that value from `utils.NotePath(vaultRoot, folder,
title)`, which returns `filepath.Join(vaultRoot, folder, slug+".md")` — an
absolute path baked into a row that outlives the machine it was written on.
`NoteStore.GetByPath` matches on the full absolute string. `internal/notes`
reads and writes files directly from `n.Path` (`update.go:25`, `files.go:50`,
`books.go:55`, and about thirty other call sites).

Interestingly, the schema already contains the *correct* convention twice.
`archived_from` and `trashed_from` hold **vault-relative** paths, produced by
`utils.RelVault`. The codebase already knows how to do this; `notes.path` is
simply the one column that was never converted.

### 1.3 Identity is the slug, and the file carries no ID

`utils.Slugify` lowercases a title and collapses every non-alphanumeric run to
`-`. `utils.NotePath` appends `.md`. So a note's identity on disk is
"the slugified title, in a folder". `Go Slices` and `Go: Slices` are both
`go-slices.md`. S-03 has closed that case on one machine: `createNote`
(`internal/notes/create.go:100`) refuses a create whose slugged path already
belongs to a row with a different title, and the error names both titles. But
the guard is a `GetByPath` lookup in the local database, so it can only see a
collision this machine already recorded — the cross-device version, where each
machine wrote its own note independently, is exactly the one it cannot catch.

`parser.Frontmatter` has fields for `title`, `tags`, `kind`, `done`,
`athena_index`, `linked_folders`, book metadata, and reading dates. **There is
no stable identifier.** Nothing in the Markdown file says "this is the same note the
other machine calls note 7."

The integer `notes.id` is machine-local: it comes from
`INTEGER PRIMARY KEY AUTOINCREMENT`. Every model-facing action
(`update_note`, `move_note`, `trash_note`) carries a `note_id`, so that
integer is Athena's only handle on a note — and it means something different
on each machine.

V-06 has landed, so the two pieces of durable per-note state that used to live
**only** in SQLite are now written to the file as well: `parser.Frontmatter`
carries `kind` (`note` / `task` / `book`) and `done`, `createNote` writes them on
the first save, and `syncNoteFrontmatter` (`internal/notes/update.go:147`)
restates them on rename, update and `MarkDone`. The reading half is not finished:
the reconcile pass builds an adopted note from `title` and `kind` only
(`internal/notes/reconcile.go:145`), so a `done:` on disk is written but never
read back. That matters for this design more than it looks; see §4.

### 1.4 Nothing watches the vault, and the reconcile pass has no caller

`grep -r fsnotify` finds nothing. There is no watcher. V-07's scan does now
exist: `notes.Service.ReconcileVault` (`internal/notes/reconcile.go:67`) walks
the vault, indexes untracked `.md` files, repairs rows whose file moved, and
reports the missing and the conflicting instead of guessing at them. Nothing
outside its own tests calls it — not startup, not a slash command — so in a
running Athena an edit made in Obsidian is still never re-indexed and a file
created in Obsidian is still invisible to search.

The reconcile that runs on its own is `notes.Service.reconcileMissingNotePath`
(`internal/notes/create.go:229`), which `MoveNote`, `RenameNote` and
`DuplicateNote` call when the source file is missing, and which
`ReconcileVault` reuses for the same case. It walks the vault for a file with
the *same basename* **and** the *same frontmatter title*, and:

- zero matches → error naming the missing path
- more than one match → error, refusing to guess
- exactly one → repair the row's path

That refuse-to-guess shape is exactly the right instinct and this design reuses
it rather than inventing a second matcher.

### 1.5 Writes already refuse to clobber — inconsistently

- `utils.WriteNoteFile` returns `os.ErrExist` if the target exists.
- `createNote` stats the target and errors with
  *"file already exists at X (not in database — import or rename)"*.
- `utils.MoveFile` refuses an existing destination.
- `notes.ReplaceSection` compares the current section body against
  `expectedContent` and refuses if it changed, with the comment
  *"prevents a stale model plan from clobbering a user edit."*

But `UpdateNote` and `AppendNote` route through `utils.OverwriteNoteFile`,
which does an unconditional `os.WriteFile`. On one machine that is tolerable —
Athena is usually the only writer. With a syncer it is not.

### 1.6 Embeddings have no model identity

`chunks.embedding` is a raw little-endian `float32` BLOB
(`storage/chunks.go:encodeEmbedding`). No column records which model or
dimension produced it. `storage.SearchSimilar` ranks with `cosineSimilarity`,
which returns `0` when `len(a) != len(b)`.

So an index built with `qwen3-embedding:0.6b` and queried with
`text-embedding-3-small` does not error. Every score is `0.0`, the sort is
arbitrary, and search returns whatever `topK` rows the sort happened to leave
at the front. V-03 has landed and makes that detectable, though not per row:
`notes.Service.Reindex` records the embedding model and the vector dimension in
the `jobs` row for the run, `IndexHealth` (`internal/notes/reindex.go:105`) reads
the last *finished* run back, `/doctor` reports a mismatch as a problem and
points at `/reindex`, and `/reindex` rebuilds. What did not change is the search
path itself: `chunks` still carries no model identity, and `SearchSimilar` still
scores mismatched vectors as `0.0` and returns them rather than refusing.
Multi-device makes that routine rather than a one-time migration hazard.

### 1.7 Concurrency safety today

`storage.Open` sets `foreign_keys(1)`, `busy_timeout(5000)` and
`journal_mode(WAL)` as DSN pragmas rather than one-shot statements, so they hold
on every connection the pool opens (`connectionPragmas`,
`internal/storage/sqlite.go:90` — that is V-08). That is correct for *two
processes on one machine sharing one local file*. It provides nothing at all for two machines each holding their own copy
of a file a syncer is replacing underneath them.

---

## 2. The problem

Put the config file and the database in a synced folder today and here is what
actually happens, in order of how badly it hurts.

**(a) A synced `config.yaml` silently creates an empty vault.**
`vault_path: /home/alice/Athena` on a Mac. `EnsureDirs` creates
`/home/alice/Athena` — Athena has permission, so no error. Athena starts,
reports "vault: /home/alice/Athena", and the real 800 notes at
`/Users/alice/Athena` are invisible. Every `get_note` fails; the model is told
the vault is empty, and behaves accordingly.

**(b) A synced database makes every note unwritable.**
Machine B opens a DB whose rows say `/home/alice/Athena/go-slices.md`.
`GetByPath` is called with B's computed path `/Users/alice/Athena/go-slices.md`
and returns `nil`. `createNote` therefore takes the "not in the database"
branch, `os.Stat` finds the file, and it errors: *"file already exists at
go-slices.md (not in database — import or rename)."* Meanwhile `ListNotes`
happily returns all 800 rows, and every read through `n.Path` fails with
`ENOENT`. The vault appears simultaneously full and broken.

**(c) A per-device database forks note identity.**
This is the "correct" configuration today and it still breaks. B has no rows,
so all 800 files are invisible until something indexes them; the first
`create_note` on B inserts `id = 1` for a note that is `id = 412` on A. Note
IDs are the handle in every action payload and in the plan cards the user
approves. They now mean different things per machine. Nothing today persists a
note ID across a restart, so this is latent rather than actively wrong — but it
forecloses ever persisting one.

**(d) Syncing the SQLite file can lose data.**
Say this plainly: **a file syncer copying `athena.db` while WAL mode is on can
lose notes.** WAL means the database is three files — `athena.db`,
`athena.db-wal`, `athena.db-shm` — and a committed transaction may live only in
the `-wal` file until a checkpoint. Dropbox, iCloud, and Syncthing copy files
independently, not as an atomic snapshot, so the copy that lands on the other
machine can be a `.db` from time T with a `-wal` from time T+3s. SQLite may
open it, may refuse it, or may open it and be subtly wrong. Git is no better:
it treats the `.db` as one opaque binary blob, so a concurrent edit is an
unmergeable whole-file conflict and picking a side discards every change on the
other.

The mitigating fact: note *bodies* also exist as Markdown, so text is
recoverable. What is not recoverable is everything that lives only in SQLite —
`note_type`, `done`, `created_at`, and the entire embedding index.

**(e) Slug identity collides across devices.**
A writes "Go: Slices" → `go-slices.md`. B writes "Go Slices" → `go-slices.md`.
Two different notes, one filename, created independently. Git reports a
conflict on a file neither branch's ancestor contains. Syncthing keeps one and
writes `go-slices.sync-conflict-20260817-101500-ABCDEFG.md` beside it. That
sidecar is a `.md` file sitting in the vault — and a naive V-07 startup scan
would cheerfully adopt it as a brand-new note titled "Go Slices", giving the
user two near-identical notes and no explanation.

**(f) Divergent embed models poison search silently.**
Covered in §1.6. With one machine this happens once, when you change a model.
With two machines it is the steady state unless the models are pinned.

**(g) Athena overwrites edits it never saw.**
`UpdateNote` reads the file, re-renders it, and writes it back. If the syncer
delivered a newer version of that file between Athena's last index and this
write, the newer version is gone — no error, no prompt. This is the concrete
violation of the L-05 acceptance line *"no silent overwrite."*

---

## 3. Constraints

From `AGENTS.md`, quoted because they bind the design rather than merely
suggest it:

> **"Markdown file and SQLite row stay together. Neither is the single source
> of truth."**

This is the constraint that decides the whole design, and it deserves a careful
reading rather than a convenient one. The obvious answer to a synced vault is
"Markdown is the truth, the database is a cache, rebuild it." That sentence
forbids stating it that way. §5 proposes a reading that I believe honours it —
Markdown owns *identity and durable per-note facts*, SQLite owns the *derived
index* — but the owner has to confirm it. It is decision 1 in §6.

> **"Filesystem and SQLite are not one transaction; new multi-step writes need
> compensating undo or a journal."**

Reconcile is the largest multi-step write Athena would ever perform. It must be
resumable and must not leave half-adopted state. V-04 has landed for the
single-note writes, so the pattern to follow already exists in the package:
`saveMovedNote` (`internal/notes/files.go:24`) moves a file back when its row
update fails, `createNote` removes the file it just wrote when the insert fails,
and `UpdateNote` rewrites the previous body when the row update fails.

> **"Soft-deleted / trashed notes must never enter RAG."**

A file that reappears from `.trash` via sync, or a note whose row says trashed
while the file sits in a normal folder, must resolve to *excluded*. Fail
closed.

> **"The engine is the source of truth. The TUI is an event renderer.
> Loading, tool progress, plans, errors, and completion must come from typed
> protocol events, not from parsing English status strings."**

Reconcile progress and every conflict must be typed protocol events in
`protocol/athena.v1.schema.json`. The Ink client (Grok's territory) renders
them; the engine decides them.

> **"A ~2B local model is a first-class target… Prefer application-owned state,
> narrowed action contracts, validation."**

Conflict resolution is never a model action. No `resolve_conflict` tool. The
model can *report* that a conflict exists; only a human keystroke resolves one.
This mirrors L-04's rule for permanent delete: *"never hooked to a 2B-model
impulse."*

> **"Prefer standard library whenever practical."** / *"Before adding a
> dependency: explain why."*

No `fsnotify`, no CRDT library, no git bindings, no UUID package. `crypto/rand`
plus `encoding/hex` mints an ID in four lines; `crypto/sha256` hashes a file;
`os.ReadDir` and `filepath.WalkDir` walk a vault. Everything here is stdlib.

From the existing architecture:

- `internal/utils/file.go` already owns safe path operations
  (`RelVault`, `CleanFolder`, `NotePath`). New path logic belongs there, not
  scattered through `internal/notes`.
- `internal/appdirs` owns per-user layout via XDG. The database is app data and
  must stay there; it is not a document.
- `config.validateDataPaths`'s established pattern is *fail loudly and name the
  file and field*, never silently substitute. Every new startup check follows
  that pattern.
- `docs/plans/` holds proposals; `docs/` holds what exists. This file is
  correctly in `docs/plans/`.

---

## 4. Options

### Option A — Portable paths, per-device database (simplest)

**How it works.** Exactly one thing is ever synced: the vault directory of
`.md` files. The database is per-machine and never leaves it.

1. `notes.path` becomes **vault-relative**, slash-separated
   (`projects/go-slices.md`), matching the convention `trashed_from` and
   `archived_from` already use. A single unexported helper on `notes.Service`
   turns a row into an absolute path at the filesystem boundary; nothing else
   in the package touches `filepath.Join` with the vault root.
2. `vault_path` and `db_path` gain `~` and `$HOME` expansion at load, so one
   config string resolves correctly on every machine.
3. Startup refuses to continue when the database holds notes but the vault
   directory is empty or absent, instead of `MkdirAll`-ing a decoy
   (failure (a)).
4. Reconcile-on-start (V-07) walks the vault and matches files to rows **by
   relative path**, adopting untracked files and flagging missing ones.

**What it costs.** Note identity is the path, so a rename on machine A is
indistinguishable from *delete + create* on machine B. B drops the old row and
adopts a new one, which means it loses `created_at`, re-embeds the whole note,
and — until V-06 lands — **silently resets `note_type` and `done`**, because
those live only in SQLite. A task you ticked on A comes back unticked on B
after a rename. That is data loss of a small but real kind, and it is why
Option A is not free.

Machine-local integer IDs stay divergent, so no feature may ever persist a
`note_id` outside its own machine.

**How it fails.** Two devices editing one note between syncs produces a
syncer-level conflict that Athena cannot distinguish from an ordinary Obsidian
edit — it sees "the file is not what I indexed" in both cases. Athena would
re-index a file containing git conflict markers as if it were prose, and adopt
a `*.sync-conflict-*.md` sidecar as a new note.

### Option B — Stable note ID in frontmatter (scalable)

Everything in Option A, plus identity that survives the path.

**How it works.**

1. Every note carries `athena_id` in its YAML frontmatter — 16 bytes from
   `crypto/rand`, hex-encoded. Written by the same change that lands V-06's
   frontmatter work, so notes get `title`, `kind`, `done`, and `athena_id` in
   one pass over the vault.
2. `notes` gains `athena_id TEXT` plus a **partial** unique index
   (`CREATE UNIQUE INDEX … WHERE athena_id IS NOT NULL`). Partial, because a
   plain `UNIQUE` column on a table backfilled with empty strings rejects the
   second row. The existing `INTEGER PRIMARY KEY` stays exactly as it is — it
   remains the local handle, so `chunks.note_id`'s foreign key, every action
   payload, and every plan card keep working unchanged. This is the cheap part:
   no rewrite of the action contract.
3. Reconcile matches in a fixed order: `athena_id` → relative path →
   the existing basename-plus-frontmatter-title heuristic. First match wins;
   ambiguity is a conflict, never a guess.
4. Because the file names itself, a rename on A is a *rename* on B: same ID,
   new path, row updated in place. `created_at` survives, `done` survives,
   and unchanged content is not re-embedded.
5. It also makes cross-device collisions *detectable*. When B finds
   `go-slices.md` carrying an `athena_id` that is not the ID its row for that
   path holds, that is unambiguous evidence of two independently created notes
   — the exact case (e) that Option A cannot see.

**What it costs.** A one-time migration that rewrites every file in the vault
to add a frontmatter line. That migration is itself a sync event: 800 modified
files landing on the other machine at once, and it must not run concurrently on
two machines (see decision 3). The IDs are visible forever in Obsidian's
properties panel. And once other devices have synced them, removing them
re-breaks identity — so this is effectively **irreversible**.

**How it fails.** A user duplicates a note file in Finder and now two files
claim one ID. Reconcile must handle that explicitly: keep the ID on the file
whose path matches the existing row, flag the other as a duplicate, and mint a
new ID only when the user says so. Never mint silently — silent minting turns
"I made a backup copy" into two divergent notes with no relationship.

### Option C — Sync the database with a real merge (production-grade)

**How it works.** Athena grows an operation log: a `note_ops` table recording
every create/update/move/trash with a device ID and a logical clock, plus
per-note version vectors, plus a merge function. Replication happens either
through a coordinating server or by exporting the log to per-note text sidecars
that git can merge line-wise. Last-writer-wins on fields, three-way merge on
bodies.

**What it costs.** This is a distributed systems project, not a feature. It
would immediately be the largest and hardest subsystem in the repository — a
clock, a merge, a resolution UI, and a test matrix of interleavings. It also
does not remove the need for Option A and B's work: you still need relative
paths and stable IDs before an op log can reference anything.

The only sane off-the-shelf shapes are `sqlite3_rsync` or Litestream-style
replication, and both require a coordinator the user runs — which contradicts
"synced by something the user already runs."

**How it fails.** Merge bugs in this class of system lose notes quietly and
are discovered weeks later. And it fails the AGENTS.md bar directly: an
unrequested abstraction, invented architecture, and a new dependency, in
exchange for solving collaboration nobody asked for.

---

## 5. Recommendation

**Build Option B, shipped as two independently useful stages, with Option A's
mechanics as stage one.**

The reasoning:

**The database must not be synced.** Not as a compromise — as the design.
Syncing a WAL-mode SQLite file through a general-purpose file syncer is the one
choice here that can actually lose committed data (§2d), and it buys nothing
the Markdown does not already carry. Each machine keeps its own
`~/.local/share/athena/*.db`, and reconcile rebuilds whatever that machine is
missing. Sync exactly one thing, and make that thing plain text.

**This is not "Markdown is the source of truth."** Here is the reading of the
AGENTS.md rule I am proposing, and it is a genuine division rather than a
rebrand: **the Markdown file owns note identity and every fact a human would
mourn losing** — the ID, the title, the type, the done state, the tags, the
body. **SQLite owns the derived index** — chunks, embeddings, and the fast
lookups. Neither can be discarded to satisfy the other, which is what the rule
protects: reconcile *reconciles*, and where the two disagree about something
Markdown does not carry, the answer is a flagged conflict, not an overwrite.
This reading only holds **after V-06** puts type and done in frontmatter. Until
then, rebuilding a database from Markdown genuinely destroys user state, and
L-05 must not ship. **V-06 and V-03 are hard prerequisites.**

**Stable IDs are worth their cost, and stage one alone is not enough.** Option
A is smaller and I would normally stop there. It fails on the specific thing
this task exists to fix: without an ID in the file, a rename on one machine is
a delete-and-create on the other, and two devices creating the same slug is
invisible. Both are ordinary events for one person with a laptop and a desktop,
not edge cases. The ID costs one frontmatter line and one partial index.

**"No silent overwrite" becomes one guard in one place.** All note writes route
through a single `Service` method that compares the file's current SHA-256
against the `content_hash` recorded when Athena last read it. If they differ,
the write is refused before it touches the file:

> **Cannot write "Go nil slices" — the file changed since Athena last read it.**
> `projects/go-nil-slices.md` was modified outside Athena (another device, or
> Obsidian). Your requested change was not applied and nothing was overwritten.
> Run `/reconcile` to re-read the file, then try again — or open the file and
> resolve it yourself.

That is the concrete answer to "what does the user see instead." The refusal is
a typed protocol event with the note ID, the relative path, and both hashes;
the action's outcome is `refused`, not `failed`, and it lands in the existing
`action_audit` table. `ReplaceSection`'s expected-content check is the same idea
one level down, and generalising it is a smaller diff than leaving each caller
to guard itself.

**Athena detects conflicts; it does not resolve them.** The syncer already owns
concurrent-edit detection — git writes markers, Syncthing writes sidecars.
Athena's three jobs are: never overwrite a file it did not write last, notice
and surface what the syncer flagged, and keep the index honest. That is what
makes this "not a Dropbox hack": we are not reimplementing sync, we are making
the engine correct in the presence of one.

---

## 6. Decisions needed from the owner

1. **Does the AGENTS.md rule "Markdown file and SQLite row stay together;
   neither is the single source of truth" permit §5's reading** — Markdown owns
   identity and durable per-note facts, SQLite owns the derived index and may be
   rebuilt per device? If not, L-05 cannot proceed as designed and the standing
   rule needs rewording first.

2. **Is a visible `athena_id:` line in every note's frontmatter acceptable,
   permanently?** It appears in Obsidian's properties panel on every note. Once
   other devices have synced them, removing them re-breaks identity — treat this
   as **irreversible**.

3. **Which syncers are supported?** Git only, or also Syncthing / iCloud /
   Dropbox? This decides whether Athena must recognise git conflict markers and
   `*.sync-conflict-*.md` sidecars, and it decides whether the one-time
   ID-backfill migration can assume a serialising step (a git pull) or must
   defend against running on two machines at once.

4. **May Athena write anything into the vault when it finds a conflict** — for
   example a `projects/go-slices.conflict-desktop.md` copy so nothing is lost —
   or must it stay read-only and report until the user acts?

5. **Is the local database disposable?** If a machine's `.db` is deleted,
   Athena rebuilds it from Markdown, and everything not in frontmatter is gone:
   `created_at` becomes the reconcile time, and every embedding is recomputed
   (minutes of local model time on a large vault). Confirm that is acceptable,
   because it is the behaviour reconcile implies.

6. **Retention and privacy for the conflict log.** The proposed
   `vault_conflicts` table records relative paths and short content excerpts so
   the user can see *what* differs. Excerpts are note content in a table the
   user did not explicitly opt into. Keep them forever, expire after N days, or
   store paths and hashes only? AGENTS.md sets an explicit bar for durable
   private content and this clears or fails it by your answer.

7. **Must two devices agree on the embedding model?** Recommended: yes, pinned
   by V-03's index metadata, and a device configured with a different model
   refuses to search and says so rather than returning zero-similarity noise.
   Confirm you would rather have a hard refusal than degraded results.

8. **Trash across devices.** Machine A trashes a note; before syncing, machine B
   edits it. Proposal: the edit wins, the note is restored from `.trash`, and
   the trash action is reported as reverted — never delete content that was
   edited afterwards. Confirm, because the alternative (deletion wins) is
   unrecoverable once L-04's empty-trash ships.

---

## 7. Implementation sketch

Ordered. Each phase is independently shippable and independently useful; stop
after any of them and the product is not worse than before.

**Phase 0 — prerequisites (not this task).** V-06 (title, type, done into
frontmatter) and V-03 (reindex as a real job, with embedding model and
dimension recorded and checked). L-05 must not start before both land; §5
explains why V-06 in particular is load-bearing.

**Phase 1 — relative paths.** Change the meaning of `notes.path` to
vault-relative, slash-separated. Add one unexported accessor on
`notes.Service` that resolves a row to an absolute path, and route every
filesystem call in `internal/notes`, `internal/retrieval`, and
`cmd/athena/main.go` through it. Migration in `storage.migrateNotesColumns`
style: strip the configured vault prefix from each row; a row that does **not**
start with the current vault path is left untouched and recorded, never
rewritten — it is exactly the "wrong machine" case and must be visible. Tests:
round-trip a note through create → move → rename → trash → restore and assert
the stored path is relative at every step. No user-visible change yet.

**Phase 2 — portable configuration.** Expand a leading `~` and `$HOME` in
`vault_path` and `db_path` at load, keeping `validateDataPaths`'s
refuse-and-name behaviour for empty values. Add one startup guard: if the
database contains notes and the resolved vault directory is missing or empty,
stop and print both paths — do not `MkdirAll` a decoy. Update
`docs/configuration/README.md` in the same change.

**Phase 3 — vault identity.** Write `.athena/vault.json` at the vault root
holding a generated `vault_id`. Default `db_path` becomes
`~/.local/share/athena/<vault_id>.db`, so two different vaults can never share
one database. On startup, compare the database's recorded vault ID against the
vault's; a mismatch stops with both IDs named. Add `.athena` to the directories
already skipped alongside `.trash` and `.obsidian` in `utils.ListFolders` and
in the reconcile walk.

**Phase 4 — stable note IDs.** Add `athena_id` to `parser.Frontmatter`; add the
column and the partial unique index to `notes`; mint IDs with `crypto/rand`.
Backfill as a resumable job in the existing `jobs` table: for each note, write
the ID into frontmatter, then record it on the row, then move on — so an
interrupted backfill resumes rather than restarts. Notes already carrying an ID
are skipped, which makes the job idempotent and safe to run on the second
machine after sync.

**Phase 5 — reconcile on start (completes V-07).** A job, not inline startup
work, so it reports progress and can be cancelled.

*Inputs:* every `.md` under the vault except `.trash`, `.obsidian`, `.athena`,
and generated folder-index notes (`athena_index: true`); every `notes` row.

*Matching order, first match wins:* `athena_id` → relative path → the existing
`reconcileMissingNotePath` basename-plus-title heuristic. Two candidates at any
level is a conflict, never a guess.

*Per-file outcomes:*

- **Unchanged** — size and mtime match `indexed_mtime`; skip without hashing.
- **Changed** — hash differs from `content_hash`; re-read, update the row,
  re-chunk, re-embed. This is the normal "edited in Obsidian" path and is not a
  conflict.
- **Moved** — ID matches, path differs; update the path only. No re-embedding.
- **New** — no match; adopt it, minting an ID if absent.
- **Row without a file** — mark the row `missing`, never delete it and never
  delete its chunks. A missing note is excluded from search and RAG the same way
  a trashed one is, in `ChunkStore.Searchable`, so it fails closed.
- **Conflict** — see below; nothing is written.

*Conflict cases:* ID at this path differs from the row's ID (independent
creation); the same ID appears in two files (duplicate); the file contains git
conflict markers; a `*.sync-conflict-*.md` sidecar exists beside a tracked note;
a row says trashed while the file sits in a normal folder.

**Phase 6 — surfacing.** Add the `vault_conflicts` table: `id`, `kind`,
`note_id` (nullable), `rel_path`, `detail`, `detected_at`, `resolved_at`,
`resolution`. Add typed reconcile-progress and conflict events to
`protocol/athena.v1.schema.json` — Claude owns the schema side, Grok renders
them. Add `/conflicts` to list open rows and `/reconcile` to run the job on
demand. Add the write guard from §5 as the single method every note write routes
through, and make `utils.OverwriteNoteFile` unreachable from `internal/notes`
without passing it.

**Phase 7 — documentation.** `docs/notes/README.md` gets the identity rules and
the write guard; `docs/configuration/README.md` gets the path expansion, the
vault ID, and the explicit statement that the database is per-device and must
not be synced; `docs/retrieval/README.md` gets missing-note exclusion. Mark
L-05 `done` in `tasks.md` in the same change, per AGENTS.md.
