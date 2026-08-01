Introduce real tools, but start with read-only ones
The most useful first tools are:
search_notes(query, limit)
get_note(note_id) — retrieve full content when an excerpt is insufficient
list_notes(folder, tag, status)
list_folders()
find_notes_by_title(query)
Athena currently injects the full inventory plus only four semantic search hits into every prompt. These tools let the model ask for exactly the missing information instead of guessing.
I would not add web, shell, or arbitrary file tools. They increase capability quickly but make a personal vault assistant much harder to trust.
Make write tools safer and more specific
Existing create/move/trash actions are a good base. The next useful additions are:
append_to_note — safer than replacing a whole note
patch_note_section — edit a named section, with an expected version/content hash
set_note_tags
preview_changes — show a proposed batch before execution
For destructive or broad operations—trash, delete folder, mass move, overwrite—require confirmation. For narrow reversible actions, execute directly and report exactly what happened.
Add a tool policy layer
Every tool should declare metadata such as:
Property	Example
Kind	read / write / destructive
Retry-safe	search: yes; update note: no
Confirmation	required for bulk trash
Timeout	10s read, 60s write
Parallel-safe	searches: yes; same-note updates: no


That belongs between the model and your existing tools.Dispatcher. The dispatcher remains the executor; the policy layer decides whether a call is valid and allowed.
Test failures, not just success paths
Your current tool parser and batch dispatcher already have useful tests. Next, add scenario tests for:
Ollama disconnecting mid-stream
malformed or unknown tool calls
cancellation during embedding
duplicate create requests
tool dependency failures
partial note update failure
a model selecting the wrong note ID
retrying a read without repeating a write