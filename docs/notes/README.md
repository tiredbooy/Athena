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

## Folder safety rules

- Paths are vault-relative, slash-separated, and cannot contain `..`.
- Creation creates missing parent directories.
- Deletion works only for empty folders; recursive deletion is intentionally
  not implemented.
- `.trash` is system-managed and cannot be deleted through folder actions.
- Moving/renaming folders repoints affected SQLite note paths.

## Note states

- Normal notes are listed and searchable.
- Trashed notes move below `.trash/` and are hidden from normal listings.
- Archived notes move below `archive/` and preserve their original path.
- Tasks are notes with a type and a `Done` flag.

## Risk to preserve

Filesystem and SQLite changes are not one transaction. For more complex future
operations, use compensating rollback or an operation journal rather than
silently accepting partial success.
