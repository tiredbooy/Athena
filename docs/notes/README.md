# Notes and vault

## Ownership

`internal/notes` keeps markdown files, SQLite note rows, and embeddings
consistent. `internal/utils/file.go` owns safe low-level path and filesystem
operations.

## Note lifecycle

```text
Create/update request
  → validate vault-relative path
  → write or move markdown file
  → save/update SQLite note row
  → chunk body and request embeddings
  → store chunks and embedding blobs
```

Creating a note never silently overwrites an existing file. Updating a note
replaces its chunks and re-embeds the body. An embedding failure is reported
after the note is saved so user content is not lost.

Moving a note never overwrites one either. `MoveNote`, `RenameNote` and the
trash/archive transitions all relocate through `utils.MoveFile`, which refuses
an existing destination. Checking SQLite for a note at the target is not enough:
a Markdown file can exist with no row — Obsidian wrote it, or a partial write
left it — and a bare `os.Rename` would destroy content Athena never indexed
(S-07).

### Filenames and title collisions

A note's filename is its slugified title, so titles that differ only in
punctuation or case share one file: `Go Slices` and `Go: Slices` both want
`go-slices.md`.

- **Same title again** → the existing note is returned, `created=false`, no
  error. Weak models re-emit `create_note` when the user only asked to list,
  and that must stay harmless.
- **Different title, same filename** → error naming *both* titles. Returning
  the older note would report success while the new content was never written.
- **Books are the exception.** A catalog title (`Thinking, Fast and Slow`)
  legitimately differs from what the user typed (`Thinking Fast And Slow`), so
  `create_book` treats the shared filename as the same book and upgrades the
  existing note in place instead of erroring.

### The Markdown describes itself (V-06)

The SQLite row is Athena's index, not its record. A vault opened in Obsidian, or
a database rebuilt from scratch, has only the files — so a note's identity is
written into its YAML frontmatter as well as its row:

- `title:` — the note's title.
- `kind:` — its type, one of `note`, `task`, `book`, matching `note_type` in the
  database.
- `done:` — a task's completion state, a plain YAML `true`.

Every write path restates them, so they cannot drift:

| Path | What it writes |
| --- | --- |
| `createNote` (behind `CreateNote`, `CreateTask`, `CreateBook`) | title and kind in the first write, so a task is a task on disk immediately |
| `UpdateNote` | restates title and kind, carrying every other key — `done:` included — through untouched |
| `RenameNote` | rewrites `title:` after the row is saved (see below) |
| `MarkDone` | rewrites `done:` after the row is saved |
| `DuplicateNote` | copies the **source file's** whole YAML block, so a duplicated book keeps its authors, ISBN and `started_at`, and a copy of a finished task starts finished |

**`done:` is absent, never `false`.** An open task has no `done:` line at all.
Writing `done: false` would mean every note, book and folder index note in the
vault grew a line about a field only tasks use, the first time Athena touched
it. So absent and `false` mean the same thing, and un-ticking a task removes the
key again.

`done:` is also the one key Athena writes for a single note type. `MarkDone`
refuses anything that is not a task, and a rewrite of a note or a book leaves
whatever `done:` the user's own YAML had — the row's flag is meaningless for
those types, so restating it would silently strip a key Athena did not put there.

`RenameNote` rewrites the YAML last, after the row update has succeeded. Until
then the file move can still be undone, and a file put back at its old path
carrying its new title would contradict the record it was just restored to
match. If only that final rewrite fails, the note is renamed everywhere except
one YAML line and the error says exactly that.

**Still one gap:** `ReconcileVault` does not yet read `done:` back. A task
already in the database keeps its state — the row is never overwritten from disk
— but a task file that has *no* row is indexed as open even when its YAML says
`done: true`, so a database rebuilt from scratch still reopens finished tasks.
The fix is one field in the `models.Note` literal in `internal/notes/reconcile.go`.

Portability is a hard rule: these are ordinary YAML keys in an ordinary
Markdown file, and the body is never touched by a frontmatter rewrite.

**Known limitation.** `parser.RenderMarkdown` serialises the `Frontmatter`
struct, so YAML keys Athena does not model — an Obsidian `aliases:` or
`cssclasses:` — are dropped the next time Athena writes that note.

## Folder safety rules

- Paths are vault-relative, slash-separated, and cannot contain `..`.
- **Creating a note, task, or book does NOT create its destination folder.**
  If the folder is missing, the write fails and names it. Athena refuses to
  turn a weak model's guessed path into directories (the same rule
  `internal/chat` enforces before it runs folder actions); a note filed into an
  invented folder is worse than a rejected request, because the user never
  learns the model guessed.
- Explicit folder creation (`create_folder` / `ensure_folders`) is the only way
  to add directories, and it does create missing parents.
- One code-owned exception: a book created with no folder goes to
  `books/reading`, which Athena creates if missing. That default lives in
  `notes.CreateBook`, not in a prompt, so it is Athena's choice rather than a
  model guess. Any folder the caller names must already exist.
- Deletion works only for empty folders; recursive deletion is intentionally
  not implemented.
- `.trash` is system-managed and cannot be deleted through folder actions.
- Moving/renaming folders repoints affected SQLite note paths.

## Obsidian folder graph

Folders are not graph nodes in Obsidian; Markdown files are. Athena therefore
maintains one generated index note per visible folder. A folder index links to
its immediate child folders and notes, creating a clean hierarchy such as
area → category → item without linking every item to every ancestor.

Index notes are marked `athena_index: true` in frontmatter, are not added to
Athena's SQLite catalog or retrieval context, and are refreshed after folder or
note structure changes. Athena refuses to overwrite a user note that occupies
the reserved index path (`folder/path.md` for the folder `folder/path`).

### Orb colors

`set_folder_colors` styles a folder's orb. It takes `folder`, optional
`include_children` (direct children only, never recursive), and optional
`color` as `#RRGGBB`.

- **With a color**, that is an explicit user choice and it replaces whatever the
  folder had.
- **Without one**, Athena picks the palette entry whose nearest neighbour among
  that folder's *siblings* is furthest away, so a new orb reads as distinct from
  the ones beside it. Ties keep the earliest palette entry, so the same vault
  always renders the same way.

  An earlier version hashed the folder name. That was stable but blind: two
  adjacent orbs could land on neighbouring colors, which is what "make this
  folder stand out" is asking to avoid.

Without an explicit color, a valid existing color group is never replaced —
including one the user chose in Obsidian. Athena only fills gaps and repairs
malformed entries it wrote itself. That rule holds for a folder's children
during `include_children` too, and both halves of it (kept by default, replaced
on an explicit request) are pinned by tests in `internal/notes/graph_test.go`. The action reports the resulting color per
folder, so the outcome says which color landed rather than just "done".

**Size is graph-wide, not per folder.** Obsidian's core Graph view has a single
`nodeSizeMultiplier` and no per-color-group size, so `set_graph_node_size`
changes every node. Per-folder sizing would need a graph plugin; Athena does not
pretend to offer it.

## Note states

- Normal notes are listed and searchable.
- Trashed notes move below `.trash/` and are hidden from normal listings.
- Archived notes move below `archive/` and preserve their original path.
- Tasks are notes with a type and a `Done` flag. The type is in the file and the
  row; `Done` is in the row only.

### Archive and trash do not stack (S-04)

Both states are the same shape — move the file away, record the vault-relative
path it came from (`ArchivedFrom`, `TrashedFrom`) — so entering one while
already in the other would record an origin that is *itself* a relocation, and
the paths would nest (`.trash/archive/...`). Restore would then put the note
back somewhere it never lived.

`TrashNote` refuses an archived note and `ArchiveNote` refuses a trashed one,
each naming the state the note is already in. The note must be unarchived or
restored first. Repeating the state a note is already in stays a no-op, not an
error: `TrashNote` on a trashed note returns it unchanged, and likewise
`ArchiveNote` — a model re-emitting the same action must stay harmless.

### Trash keeps its vectors (V-02)

Trashing a note keeps its chunks and embeddings in the `chunks` table.
`ChunkStore.Searchable` — the single read path behind semantic search and RAG —
joins on `notes` and drops anything whose note is trashed, so trashed content
cannot reach retrieval even though its vectors still exist.

The alternative was deleting the chunks on trash and re-embedding on restore.
That is cheaper on disk, but it makes restore depend on the embedding provider:
restoring a note offline (or after an Ollama model was removed) would either
fail or, worse, succeed with no vectors and leave the note silently invisible
to search. **A restore must always leave the note searchable again**, so Athena
pays the storage instead. Deleting on trash is only worth revisiting if trash
size ever becomes a real cost — and then restore needs its own re-embed path
with an honest offline failure.

The consequence for anything that rebuilds the index: `notes.Reindex` embeds
trashed notes as well as live ones. Skipping them would delete their vectors
(`ChunkStore.ReplaceAll` clears the table) with no code anywhere to bring them
back. They count towards the progress a reindex reports, too.

## Rebuilding the index (V-03)

`notes.Service.Reindex` re-embeds every note — live and trashed — and swaps the
whole `chunks` table in one `ChunkStore.ReplaceAll` transaction. Preparing all
vectors first is what prevents a half-replaced index mixing two embedding
dimensions, which is worse than the stale index it was meant to fix.

### The index records what built it

Chunks store bare float blobs, so nothing in them says which model produced
them — and the configured embedding model can change at any time. A reindex
therefore writes a row into the `jobs` table (`type = 'reindex'`) whose JSON
payload holds the embedding model, vector width and note count. That row is the
only durable answer to *"which model built the vectors currently in the index?"*.

The row is also the record that the run happened: it goes to `running` with
per-note progress, then to `done` or `failed` with the error text. Progress is
left where a failed run stopped, because how far it got is the useful part.

Recording is best-effort. A refused job write never aborts a rebuild the user
asked for; a `Service` built without `TrackJobsIn` reindexes exactly as before
and simply leaves no record.

### Detecting a poisoned index

`notes.Service.IndexHealth()` compares the recorded model against the configured
one. It is a pure database read, cheap enough to call before a search:

| Field | Meaning |
| --- | --- |
| `IndexedWith` | model recorded by the last **finished** reindex; empty means unknown |
| `ConfiguredAs` | embedding model configured now |
| `Dimensions` | vector width recorded at reindex time |
| `Mismatch` | true only when a recorded model exists and differs |
| `LastRun`, `LastStatus`, `LastError`, `LastCurrent`, `LastTotal` | the most recent attempt, finished or not |

Two rules keep the answer honest:

- **Only a finished job describes the index.** The vectors change only inside
  `ReplaceAll`, so a failed switch to a new model must not be reported as the
  model that built them — that would say "your index is fine" at the exact
  moment the user needs to retry.
- **Unknown is not a mismatch.** A vault that has never been reindexed has no
  recorded model. Warning about every one of those would train the user to
  ignore the warning that means their search is actually broken.

`cmd/athena` calls `TrackJobsIn`, so runs are recorded. `Mismatch` is reported
as a problem line by `/doctor`, and `/reindex` runs `Reindex` as a user command;
both live in `internal/chat` (see [the chat guide](../chat/README.md#index-health-and-reindex)).
Search itself does not yet consult `IndexHealth` before answering.

## Filesystem and SQLite are not one transaction

A file write and its row are two separate steps, so every write path undoes its
filesystem step when the database step fails (V-04):

| Path | Undo on DB failure |
| --- | --- |
| `createNote` | deletes the file it just created |
| `MoveNote`, `RenameNote`, trash/restore, archive/unarchive | moves the file back (`saveMovedNote`) |
| `UpdateNote` | rewrites the file with the exact bytes it overwrote |

Two rules make the undo safe to run: it only ever touches what the same call
created or moved, and `utils.MoveFile` refuses to clobber anything that
reappeared at the old path. Deleting a file Athena did not just create would be
far worse than an orphan. If the undo itself fails, **both** errors are
reported — at that point the vault and the database genuinely disagree and the
user needs both halves to repair it.

`moveFolder` is deliberately not covered. It repoints many rows after one
directory move, so an undo would need more database writes at exactly the
moment the database is refusing them. A stale row left behind is instead
repaired by `reconcileMissingNotePath` the next time that note is moved or
renamed. A folder move that must be all-or-nothing needs an operation journal,
not a compensating step.

## Post-write verification (A-01)

A handler returning `nil` only means the call did not error. `VerifyWrite`
(`internal/notes/verify.go`) re-reads the vault afterwards and confirms the
write actually landed: the folder exists after a create/rename/move, the note
row is present and of the right type after `create_*`, the graph colour groups
and node size match after a graph action, and the folder index links point the
way `link_folders`/`unlink_folders` asked. It refreshes the Obsidian folder
graph first, because index notes are a derived view that has to follow every
structural write.

`VerifiedWriteActions()` lists the action types it checks. Both live here, next
to the writes they guard: `cmd/athena/main.go` registers the callback with the
dispatcher and knows nothing about which action has which invariant. Adding an
invariant is therefore one edit in this package — a new `case` in `VerifyWrite`
plus its action type in that list.

The verifier must not change anything. Its only job is to stop an unverified
success being reported to the user; a verifier that repaired what it found
would hide the very drift it exists to catch.
