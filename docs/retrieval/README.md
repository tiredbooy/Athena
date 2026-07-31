# Storage and retrieval

## Ownership

- `internal/storage` persists note metadata and chunk embeddings in SQLite.
- `internal/retrieval` builds model context and performs semantic search.
- `internal/parser` renders/parses markdown and splits text into chunks.

## Data model

`notes` stores the note body, absolute path, type, task state, archive/trash
state, and timestamps. `chunks` stores note text slices and float32 embeddings
as SQLite BLOBs. Foreign keys are enabled on each connection; migrations add
new note columns safely to existing databases.

## Retrieval flow

1. Build the complete active-note catalog.
2. Embed the user query with Ollama.
3. Rank stored embeddings with cosine similarity.
4. Keep at most four hits above similarity `0.35`.
5. Include the best chunk from each selected note in model context.

Search is deliberately brute-force and appropriate for a personal vault.
Profile before introducing a vector index.

## Progress reporting

`BuildContextWithProgress` reports inventory reads, semantic search, and each
selected note read. It does not depend on terminal code, so any UI can render
the events.
