# Notes and vault domain

## Ownership

`internal/notes` owns changes that must keep markdown files, SQLite note rows,
and embeddings consistent. `internal/utils/file.go` owns safe low-level path
and filesystem operations.

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
replaces its chunks and re-embeds the new body. An embedding failure is
reported after the note has been saved, so user content is not lost.

## Folder safety rules

- Folder paths are vault-relative, slash-separated, and cannot contain `..`.
- Creating a folder creates missing parents.
- Deleting a folder works only when it is empty; recursive deletion is not
  implemented deliberately.
- `.trash` is system-managed and cannot be deleted through folder actions.
- Moving or renaming a folder repoints every affected note path in SQLite.

## Note states

- Normal notes are listed and searchable.
- Trashed notes move below `.trash/`, retain their row and embeddings, and are
  excluded from normal listings until restored.
- Archived notes move below `archive/` and retain their original path for
  restoration.
- Tasks are notes with a type and a `Done` flag.

## Important maintenance risks

File operations followed by database updates are not transactional across the
filesystem and SQLite. If a future change makes these flows more complex,
consider compensating rollback or an operation journal rather than ignoring a
partial failure.
