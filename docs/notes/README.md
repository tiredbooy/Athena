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

## Obsidian folder graph

Folders are not graph nodes in Obsidian; Markdown files are. Athena therefore
maintains one generated index note per visible folder. A folder index links to
its immediate child folders and notes, creating a clean hierarchy such as
area → category → item without linking every item to every ancestor.

Index notes are marked `athena_index: true` in frontmatter, are not added to
Athena's SQLite catalog or retrieval context, and are refreshed after folder or
note structure changes. Athena refuses to overwrite a user note that occupies
the reserved index path (`folder/path.md` for the folder `folder/path`).

## Note states

- Normal notes are listed and searchable.
- Trashed notes move below `.trash/` and are hidden from normal listings.
- Archived notes move below `archive/` and preserve their original path.
- Tasks are notes with a type and a `Done` flag.

## Risk to preserve

Filesystem and SQLite changes are not one transaction. For more complex future
operations, use compensating rollback or an operation journal rather than
silently accepting partial success.
