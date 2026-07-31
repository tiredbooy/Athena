# Storage and retrieval domain

## Ownership

- `internal/storage` persists note metadata and chunk embeddings in SQLite.
- `internal/retrieval` builds the model context and performs semantic search.
- `internal/parser` renders/parses markdown and splits text into chunks.

## Data model

The `notes` table stores title, absolute path, body, type, task state, archive
state, trash state, and timestamps. The `chunks` table stores a note ID, chunk
text, chunk position, and a float32 embedding encoded as a SQLite BLOB.

SQLite foreign keys are explicitly enabled on each open because they are a
connection setting. Column migrations for archive and trash state are safe to
run repeatedly.

## Retrieval flow

1. Build the complete active-note catalog. It is always included in model
   context so exact listing questions can be answered reliably.
2. Embed the user query with Ollama.
3. Load all stored embeddings and rank them with cosine similarity.
4. Keep up to four note hits above the minimum similarity threshold (`0.35`).
5. Add one best chunk per note to the context.

This is intentionally brute-force search. It is appropriate for a personal
vault; introduce a vector index only after profiling shows it is needed.

## Progress reporting

`BuildContextWithProgress` accepts a callback and reports actual retrieval
steps: inventory read, semantic search, and each selected note read. Retrieval
does not import the terminal UI; the caller decides how to render progress.
