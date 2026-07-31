# Athena documentation

This folder documents Athena by domain. Read this file first, then open the
domain that matches the change you are making.

| Domain | Responsibility | Document |
| --- | --- | --- |
| Bootstrap and configuration | Starts the application and owns local configuration. | [configuration](domains/configuration.md) |
| Notes and vault files | Markdown notes, folders, tasks, archive, and trash. | [notes-and-vault](domains/notes-and-vault.md) |
| Storage and retrieval | SQLite records, embeddings, and search context. | [storage-and-retrieval](domains/storage-and-retrieval.md) |
| AI and actions | Ollama client, prompt, action JSON, and dispatching. | [ai-and-actions](domains/ai-and-actions.md) |
| Chat and terminal UI | Turn orchestration, status display, and commands. | [chat-and-tui](domains/chat-and-tui.md) |
| Planned work | Recommendation policy and the TypeScript TUI migration. | [planned-work](planned-work.md) |

## Architectural rule

The dependency direction is:

```text
Terminal UI / future TypeScript UI
            ↓
          chat
            ↓
 notes, retrieval, AI actions
            ↓
 storage, filesystem, Ollama
```

Business rules belong in `internal/notes` and future application services.
Terminal rendering must not leak into those domains.

## Before changing code

1. Read the matching domain document.
2. Preserve the markdown file and SQLite record together; neither is the
   single source of truth by itself.
3. Add or extend a test for behavior that does not require Ollama.
4. Run `go test ./...`. If the home build cache is read-only, use
   `env GOCACHE=/tmp/athena-go-build go test ./...`.
