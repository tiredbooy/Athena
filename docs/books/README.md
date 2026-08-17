# Book metadata

When the user asks Athena to start or add a book, Athena uses the `create_book`
action rather than asking the language model to invent bibliographic facts.

## Resolution order

1. Look up the normalized title in Athena's local SQLite metadata cache.
2. If it is not cached, do a small exact-title lookup against Open Library.
3. If the catalog returns a strong similar title but no exact title, present it
   as a correction suggestion. Never silently rename the user's book.
4. Cache an exact result locally and write an Obsidian-compatible book note.
5. If there is no exact match or the lookup is unavailable, use authors and
   genres explicitly supplied by the user as fallback facts. Otherwise create
   the note with `metadata_source: unresolved`. Athena never invents the author,
   genre, year, or ISBN.

An ISBN is optional. If supplied, Athena validates its checksum. When it is not
supplied, the catalog result may provide one; otherwise the field is left empty.

## What leaves the machine

Step 2 is the only step that leaves the device, and the title it sends is text
from the user's vault. The lookup goes to `openlibrary.org` and nowhere else:
the resolver's HTTP client refuses to follow a redirect to any other host, so a
redirecting or compromised catalog cannot forward the query — or the `Referer`
header carrying it — to a third party. Nothing else about the note is sent, no
credential is attached, and a cache hit (step 1) makes no request at all.

Book notes default to `books/reading` and contain frontmatter such as:

```yaml
kind: book
authors: [Isaac Asimov]
genres: [Science fiction]
published_year: 1951
isbn: "9780553293357"
metadata_source: open_library
started_at: 2026-08-03T20:15:00+03:30
```

`finish_book` records `finished_at` from Athena's local system clock. The model
can see the current time for conversation, but it cannot author the stored
lifecycle timestamp.

`update_book_metadata` fills authors and/or genres on an existing tracked book
without replacing its reading notes. Empty action fields never erase existing
metadata, and fallback values cannot replace different non-empty catalog facts.
A user fallback records `metadata_source: user`; supplementing a partial catalog
record appends `+user` to its source.

Genre has two distinct representations when the user asks to organize by
genre: structured `genres` frontmatter describes the book, while the physical
folder such as `books/reading/Science Fiction` describes its vault location.

## Storage and optional offline catalog

The normal personal cache starts tiny and grows only with books the user looks
up. It is stored in Athena's existing SQLite database.

Do not download the entire Open Library corpus by default. Its official bulk
dump page currently lists approximately:

| Catalog data | Compressed download |
| --- | ---: |
| authors | 0.5 GB |
| works | 2.9 GB |
| editions (needed for broad ISBN coverage) | 9.2 GB |
| all current record types | 12.4 GB |

An imported, indexed SQLite catalog will require more disk than those compressed
files. A future `/catalog download` should let the user choose a small curated
catalog, works+authors, or the full editions corpus, display the estimated free
space requirement, and run only after explicit consent.

Source: [Open Library bulk data](https://openlibrary.org/developers/dumps).
