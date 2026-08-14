# Athena documentation

This folder documents Athena by responsibility. Each domain has its own folder
and `README.md`. Future proposals live outside this folder in `/plans`.

| Domain | Responsibility | Document |
| --- | --- | --- |
| Application startup | Composition root and YAML configuration. | [configuration](configuration/README.md) |
| Model providers | Provider connections, adapter boundaries, and extension guide. | [providers](providers/README.md) |
| Books | Local metadata cache and book note lifecycle. | [books](books/README.md) |
| Notes and vault files | Markdown notes, folders, tasks, archive, and trash. | [notes](notes/README.md) |
| Data and search | SQLite records, embeddings, and retrieval context. | [retrieval](retrieval/README.md) |
| AI planning and execution | Ollama client, action extraction, batches, and dispatching. | [ai](ai/README.md) |
| Chat behavior | Turn orchestration, shortcuts, history, and visible errors. | [chat](chat/README.md) |
| Terminal user interface | Bubble Tea workspace, command palette, streaming, and themes. | [tui](tui/README.md) |

## Future plans

- [Organization recommendations](../plans/organization-recommendations.md)

## Architectural rule

The dependency direction is:

```text
Terminal UI
    ↓
chat / application orchestration
            ↓
 notes, retrieval, AI actions
            ↓
 storage, filesystem, Ollama
```

Business rules belong in `internal/notes` and future application services.
Terminal rendering must not leak into those domains.

Conversation-level task state belongs in `internal/chat`. One bounded agent run
may pause for clarification or approval, but the original goal must remain
explicit application state rather than being reconstructed from a short reply
such as "yes".

## Before changing code

1. Read the matching domain document.
2. Preserve the markdown file and SQLite record together; neither is the
   single source of truth by itself.
3. Add or extend a test for behavior that does not require Ollama.
4. Run `go test ./...`. If the home build cache is read-only, use
   `env GOCACHE=/tmp/athena-go-build go test ./...`.
5. Update every affected domain document in the same change. Documents describe
   current behavior; future designs belong in plan documents.
