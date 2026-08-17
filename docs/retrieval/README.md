# Storage and retrieval

## Ownership

- `internal/storage` persists note metadata and chunk embeddings in SQLite.
- `internal/retrieval` builds model context and performs semantic search.
- `internal/parser` renders/parses markdown and splits text into chunks.

## Data model

`notes` stores the note body, absolute path, type, task state, archive/trash
state, and timestamps. `chunks` stores note text slices and float32 embeddings
as SQLite BLOBs. Migrations add new note columns safely to existing databases.

## Connection pragmas

`storage.Open` passes its pragmas as DSN parameters
(`athena.db?_pragma=foreign_keys(1)&...`), never as a `PRAGMA` statement run
once after opening. `database/sql` hands out a *pool* of connections and opens
new ones on demand, and `foreign_keys` and `busy_timeout` are per-connection
settings in SQLite — a single post-open `Exec` configures whichever connection
happened to serve it and leaves every later one at the defaults. The
`modernc.org/sqlite` driver applies `_pragma=` parameters when it constructs a
connection, so the DSN is what makes them hold everywhere.

| Pragma | Value | Why |
| --- | --- | --- |
| `foreign_keys` | `1` | SQLite defaults them OFF. `chunks.note_id ON DELETE CASCADE` only removes a deleted note's vectors with them ON. |
| `busy_timeout` | `5000` ms | A concurrent writer waits instead of failing immediately with `SQLITE_BUSY` when UI, engine, and indexing overlap. |
| `journal_mode` | `WAL` | Readers and the writer stop blocking each other. WAL is stored in the database file, so this only takes effect once per database. |

`TestOpenAppliesPragmasToEveryPooledConnection` pins this: it holds four
`*sql.Conn` open at once, so the pool must create four distinct connections, and
requires all of them to report the pragmas and to reject a chunk whose
`note_id` does not exist.

## Listing notes without their bodies

`NoteStore.All` selects the full row, content included. The catalog only shows
titles, ids, and paths, so using it there read every note's full text out of
SQLite just to discard it — proportional to vault size on every single turn.

`NoteStore.AllMeta` returns the same set (non-trashed, newest first) as
`[]NoteMeta` — id, title, path, type, done — and never touches the content
column. `Service.buildCatalog`, and therefore `Inventory` and `BuildContext`,
reads through it.

`All` stays for callers that genuinely need bodies: `notes.Reindex` chunks and
embeds content, and `notes.RenameFolder` re-saves whole rows through
`NoteStore.Update`. `notes.OpenTasks`, `notes.ListNotes`, and the folder-graph
sync in `notes.SyncFolderIndexes` only read metadata and should move to
`AllMeta` when that package is next touched.

`TestInventoryDoesNotLoadNoteBodies` pins this by dropping the `content` column
from a temp database and requiring the inventory to still build — a property
check, not an assertion about query text.

## Retrieval flow

1. Build the complete active-note catalog.
2. Embed the user query with the configured embedding provider (local Ollama
   by default; see [Configuration](../configuration/README.md#embeddings)).
3. Rank stored embeddings with cosine similarity.
4. Keep at most four hits above similarity `0.35`.
5. Include the best chunk from each selected note in model context.

`TestBuildContextDropsHitsBelowMinSimilarity` pins step 4: a chunk that is the
nearest one stored but still unrelated (cosine 0) is dropped rather than
presented to the model as a relevant memory.

An empty vault stops after step 1. `BuildContext` returns before embedding the
question, because there is nothing to rank and a cold local embedding model
costs real seconds. `TestBuildContextSkipsEmbeddingForEmptyVault` counts the
provider's `Embed` calls and requires zero.

## Why there is no vector index (measured, not assumed)

Search is brute-force cosine over every searchable chunk. That was profiled
before considering an index, and the numbers say not to build one.

`BenchmarkSearchNotes` in `internal/retrieval/search_bench_test.go` runs one
full interactive search — stub embedder, brute-force cosine over the whole
corpus, then load the winning notes — against a temp SQLite database of
1024-dimensional vectors (`qwen3-embedding:0.6b` width).
`BenchmarkSearchableLoad` isolates the part an approximate index would *not*
remove: reading every chunk row and decoding its BLOB back to `[]float32`.

    go test ./internal/retrieval/ -run '^$' -bench Benchmark -benchtime 20x

On an AMD Ryzen 7 7700X (Go 1.26, `-benchtime 20x`):

| Chunks | ≈ notes | Full search | Chunk load+decode | Load share | Alloc/search |
| --- | --- | --- | --- | --- | --- |
| 1,000 | ~170 | 6.9 ms | 5.3 ms | 77% | 12 MB |
| 10,000 | ~1,700 | 63 ms | 53 ms | 85% | 125 MB |
| 50,000 | ~8,300 | 289 ms | 244 ms | 85% | 628 MB |

Note counts assume the chunker's 200-word window with 40-word overlap, so
roughly six chunks per thousand-word note.

Cost is linear in chunk count, as brute force should be. The load-bearing
result is the split: **85% of a search is SQLite I/O and BLOB decoding, not
cosine arithmetic.** An approximate nearest-neighbour index attacks the other
15%. It would cost exact results, a simple mental model, a dependency, and an
interaction with reindex (V-03) — to remove at most a sixth of the time.

Against a chat turn that already spends tens to hundreds of milliseconds
embedding the question and seconds generating tokens on a local model, 7 ms at
1k chunks and 63 ms at 10k are invisible.

**Recommendation: do not build a vector index.** Revisit only if a real vault
passes ~50,000 chunks (~8,000 notes) *and* search is measured to be a felt
delay. Even then the first fix is not an index: it is to stop materialising and
re-decoding the entire `chunks` table on every query — 628 MB allocated per
search is the actual smell — by scanning rows without building the full slice,
or by keeping decoded vectors in memory. Only if *that* is not enough does an
index earn its complexity.

## Trashed notes never enter RAG

Soft deletion is a note-level flag (`trashed_from`), and trashing a note does
not remove its vectors from the `chunks` table. The exclusion therefore happens
at read time, in the queries and guards every path routes through:

- `NoteStore.All` and `NoteStore.AllMeta` return only notes with an empty
  `trashed_from`, so the catalog and `BuildContext` never list a trashed note.
  `TestAllMetaExcludesTrashedNotes` holds the metadata listing to that rule.
- `ChunkStore.Searchable` joins chunks to notes and drops any chunk whose note
  is trashed. `SearchSimilar` — and therefore `Search`, `SearchNotes`, and
  `BuildContext` — reads through it.
- `retrieval.Service.liveNoteByID` guards the direct read-by-ID path that
  neither query covers. `NoteStore.GetByID` has no trashed filter on purpose —
  `internal/notes` must still load a trashed row to restore it — so retrieval
  applies the filter itself.

  It is the **single** definition of "a note the model may read", and every
  model-facing read in `internal/retrieval` routes through it: `NoteByID`
  (`get_note`), `NotesByID` (`get_notes`), `NoteByRelativePath`
  (`get_note_by_path`, `get_daily_note`), and the subject note of `Links`
  (`get_note_links`, whose answer is parsed out of that note's body). A second
  definition is what let `get_notes` serve trashed bodies after `get_note` was
  fixed. The reads that only walk the catalog — `Links`' per-entry pass,
  `Tags`, `DuplicateTitles`, `FindNotesByTitle` — need no guard, because
  `AllMeta` already returns it trash-free.

### Reading a trashed note by ID is an error, not a miss

`NoteByID` returns an error naming the note as trashed rather than a silent
nil. The distinction matters because the model reaches this path holding a
`note_id` it captured *before* the note was trashed, often in the same run: a
nil would read as "wrong ID, try again", while the error tells the model the
note is gone and to stop asking. A genuinely unknown ID still returns
`(nil, nil)` so callers can report "not found". The error text carries only the
ID — no title and no body — because the error string reaches the model too.

`NotesByID` fails the **whole** batch on one trashed ID instead of quietly
returning the other notes. Dropping it silently would leave the model holding a
dead ID with nothing to learn from, and `get_notes` can hand back either the
error or the results, never both. The error names the offending ID, so the
model can retry without it. An unknown ID inside a batch stays a silent skip,
matching `NoteByID`'s nil miss.

`TestNoteByIDRefusesTrashedNote` and `TestNotesByIDAndLinksRefuseTrashedNote`
pin these cases.

`ChunkStore.All` still returns every chunk. It means "every stored vector" and
exists for index maintenance, not for search. Do not use it to answer a query.

`TestSearchSimilarExcludesTrashedNotes` pins this: it stores a trashed chunk
that is a *better* vector match than the live one and requires that search
return only the live note.

`TestBuildContextExcludesTrashedNotes` pins the same rule one layer up, where
the model actually sees it: with a trashed note whose vector is the better
match, neither the catalog, the semantic hits, nor the rendered prompt text may
mention it.

## Progress reporting

`BuildContextWithProgress` reports inventory reads, semantic search, and each
selected note read. It does not depend on terminal code, so any UI can render
the events.
