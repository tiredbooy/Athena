package notes

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/parser"
	"github.com/tiredbooy/internal/storage"
)

// reindexJobType names the rows Reindex writes into the jobs table.
const reindexJobType = "reindex"

// reindexRecord is the JSON payload of a reindex job. It is the only durable
// answer to "which embedding model produced the vectors in the index?" — chunks
// store bare float blobs, and the configured model can be changed at any time
// from the config file or a provider switch. Without this record, swapping
// embedding models leaves search comparing vectors from two different spaces
// and quietly returning nonsense.
type reindexRecord struct {
	EmbedModel string `json:"embed_model"`
	Dimensions int    `json:"dimensions"`
	Notes      int    `json:"notes"`
}

// IndexHealth reports whether the vectors currently in the index were built by
// the embedding model that is configured now, plus how the last reindex ended.
type IndexHealth struct {
	// IndexedWith is the model recorded by the last finished reindex. Empty
	// means no reindex has ever completed, so what built the vectors is unknown.
	IndexedWith  string
	ConfiguredAs string
	Dimensions   int
	Mismatch     bool

	// LastRun describes the most recent attempt, finished or not.
	LastRun     time.Time
	LastStatus  string
	LastError   string
	LastCurrent int
	LastTotal   int
}

// Reindex rebuilds every vector in the index and records the run in the jobs
// table, so a user can see that it happened, how far it got, and whether it
// finished. Vectors are all prepared before ChunkStore.ReplaceAll swaps them in
// one transaction: a half-replaced index would mix embedding dimensions, which
// is worse than the stale index it was meant to fix.
func (s *Service) Reindex(ctx context.Context, progress func(int, int)) error {
	notes, err := s.noteStore.All()
	if err != nil {
		return fmt.Errorf("list notes: %w", err)
	}
	// Trashed notes are re-embedded too. Athena keeps a trashed note's vectors
	// instead of deleting them, because that is what makes RestoreNote instant
	// and offline-safe; ChunkStore.Searchable, not deletion, is what keeps them
	// out of RAG. Skipping them here would drop those vectors for good, and
	// restore never re-embeds — the note would come back silently unsearchable.
	// ponytail: trash is small in a personal vault, so re-embedding it costs
	// less than a restore that needs the embedding provider to be reachable.
	trashed, err := s.noteStore.Trashed()
	if err != nil {
		return fmt.Errorf("list trashed notes: %w", err)
	}
	notes = append(notes, trashed...)

	record := reindexRecord{EmbedModel: s.ai.EmbedModel(), Notes: len(notes)}
	jobID := s.startReindexJob(record, len(notes))

	prepared := make([]*models.Chunk, 0)
	for i, note := range notes {
		texts := parser.ChunkText(note.Content, 200, 40)
		if len(texts) == 0 {
			texts = []string{note.Title}
		}
		for index, text := range texts {
			vector, err := s.ai.Embed(ctx, text)
			if err != nil {
				err = fmt.Errorf("embed %q: %w", note.Title, err)
				s.endReindexJob(jobID, record, err)
				return err
			}
			record.Dimensions = len(vector)
			prepared = append(prepared, &models.Chunk{NoteID: note.ID, Content: text, ChunkIdx: index, Embedding: vector})
		}
		if progress != nil {
			progress(i+1, len(notes))
		}
		s.trackReindexJob(jobID, i+1, len(notes))
	}
	if err := s.chunkStore.ReplaceAll(prepared); err != nil {
		s.endReindexJob(jobID, record, err)
		return err
	}
	s.endReindexJob(jobID, record, nil)
	return nil
}

// IndexHealth compares the model that built the index against the configured
// one. It is a pure database read so a caller can run it before every search
// without paying for an embedding request.
func (s *Service) IndexHealth() (IndexHealth, error) {
	health := IndexHealth{ConfiguredAs: s.ai.EmbedModel()}
	if s.jobs == nil {
		return health, nil
	}

	lastRun, err := s.jobs.Latest(reindexJobType, "")
	if err != nil {
		return health, fmt.Errorf("read last reindex: %w", err)
	}
	if lastRun != nil {
		health.LastRun, health.LastStatus, health.LastError = lastRun.UpdatedAt, lastRun.Status, lastRun.Error
		health.LastCurrent, health.LastTotal = lastRun.ProgressCurrent, lastRun.ProgressTotal
	}

	// The index only ever changes inside ReplaceAll, so only a *finished* job
	// describes the vectors that exist now. Reading the last attempt instead
	// would blame a failed switch to a new model for an index the old one built.
	finished, err := s.jobs.Latest(reindexJobType, storage.JobDone)
	if err != nil {
		return health, fmt.Errorf("read last finished reindex: %w", err)
	}
	if finished == nil {
		return health, nil
	}
	var record reindexRecord
	if err := json.Unmarshal([]byte(finished.Payload), &record); err != nil {
		return health, fmt.Errorf("read reindex record from job %d: %w", finished.ID, err)
	}
	health.IndexedWith, health.Dimensions = record.EmbedModel, record.Dimensions
	// Unknown is not a mismatch. A vault that has never been reindexed has no
	// recorded model, and warning about every one of those would teach the user
	// to ignore the warning that actually means their search is broken.
	health.Mismatch = record.EmbedModel != "" && record.EmbedModel != health.ConfiguredAs
	return health, nil
}

// startReindexJob returns 0 when there is no job store or the row cannot be
// written. Every other job helper ignores 0, so bookkeeping failures never stop
// a reindex the user asked for — losing the record is far cheaper than losing
// the rebuild.
func (s *Service) startReindexJob(record reindexRecord, total int) int64 {
	if s.jobs == nil {
		return 0
	}
	id, err := s.jobs.Create(reindexJobType, encodeReindexRecord(record))
	if err != nil {
		return 0
	}
	s.trackReindexJob(id, 0, total)
	return id
}

// ponytail: one UPDATE per note, unthrottled. A reindex already makes an
// embedding request per chunk, so the write is noise beside it; add batching
// only if a vault ever gets large enough for the job table to be the cost.
func (s *Service) trackReindexJob(id int64, current, total int) {
	if s.jobs == nil || id == 0 {
		return
	}
	// Discarded on purpose: a refused progress write must not abort a rebuild
	// the user asked for, and the next tick overwrites the row anyway.
	_ = s.jobs.Update(id, storage.JobRunning, current, total, "rebuilding vectors", "")
}

func (s *Service) endReindexJob(id int64, record reindexRecord, cause error) {
	if s.jobs == nil || id == 0 {
		return
	}
	status, message := storage.JobDone, ""
	if cause != nil {
		// A failed run must not claim to have produced an index. The payload
		// keeps the model it was attempting, but the status is what IndexHealth
		// filters on before trusting it.
		status, message = storage.JobFailed, cause.Error()
	}
	// Discarded for the same reason as the progress write, and the caller of
	// Reindex already gets the real outcome as a return value.
	_ = s.jobs.Finish(id, status, encodeReindexRecord(record), message)
}

func encodeReindexRecord(record reindexRecord) string {
	encoded, err := json.Marshal(record)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}
