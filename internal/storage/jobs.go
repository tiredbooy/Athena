package storage

import (
	"database/sql"
	"fmt"

	"github.com/tiredbooy/internal/models"
)

// Job lifecycle states. Active lists work that has not reached an end state;
// anything outside that set is terminal, which is why the two constants below
// and the Active query have to agree.
const (
	JobRunning = "running"
	JobDone    = "done"
	JobFailed  = "failed"
)

const jobColumns = `id,type,payload,status,progress_current,progress_total,message,error,created_at,updated_at`

type JobStore struct{ db *sql.DB }

func NewJobStore(db *sql.DB) *JobStore { return &JobStore{db: db} }

// Create records queued work. payload is JSON describing the job; it survives
// on the row after the job ends, so a finished job can still answer questions
// about the state it produced.
func (s *JobStore) Create(kind, payload string) (int64, error) {
	r, e := s.db.Exec(`INSERT INTO jobs(type,payload) VALUES (?,?)`, kind, payload)
	if e != nil {
		return 0, fmt.Errorf("create job: %w", e)
	}
	return r.LastInsertId()
}

func (s *JobStore) Update(id int64, status string, current, total int, message, jobErr string) error {
	_, e := s.db.Exec(`UPDATE jobs SET status=?,progress_current=?,progress_total=?,message=?,error=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, status, current, total, message, jobErr, id)
	if e != nil {
		return fmt.Errorf("update job %d: %w", id, e)
	}
	return nil
}

// Finish closes a job out in one write: its terminal status, the payload
// describing what it produced, and the error text if it failed. Progress is
// left where it stopped — for a failed job that is the useful part of the
// record, since it says how far the work got.
func (s *JobStore) Finish(id int64, status, payload, jobErr string) error {
	_, e := s.db.Exec(`UPDATE jobs SET status=?,payload=?,error=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, status, payload, jobErr, id)
	if e != nil {
		return fmt.Errorf("finish job %d: %w", id, e)
	}
	return nil
}

func (s *JobStore) Active() ([]models.Job, error) {
	return s.list(`SELECT ` + jobColumns + ` FROM jobs WHERE status IN ('queued','running','paused') ORDER BY created_at`)
}

// Latest returns the most recent job of a type, or nil when none exists. A
// non-empty status narrows it, and callers need both forms: the last *finished*
// job describes the state that currently exists, while the last job of any
// status is what says whether the most recent attempt failed.
//
// Ordering is by id, not created_at: SQLite's CURRENT_TIMESTAMP has one-second
// resolution, so two jobs in the same second would order arbitrarily.
func (s *JobStore) Latest(kind, status string) (*models.Job, error) {
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE type=?`
	args := []any{kind}
	if status != "" {
		query += ` AND status=?`
		args = append(args, status)
	}
	jobs, err := s.list(query+` ORDER BY id DESC LIMIT 1`, args...)
	if err != nil || len(jobs) == 0 {
		return nil, err
	}
	return &jobs[0], nil
}

func (s *JobStore) list(q string, args ...any) ([]models.Job, error) {
	rows, e := s.db.Query(q, args...)
	if e != nil {
		return nil, fmt.Errorf("query jobs: %w", e)
	}
	defer rows.Close()
	out := []models.Job{}
	for rows.Next() {
		var j models.Job
		if e := rows.Scan(&j.ID, &j.Type, &j.Payload, &j.Status, &j.ProgressCurrent, &j.ProgressTotal, &j.Message, &j.Error, &j.CreatedAt, &j.UpdatedAt); e != nil {
			return nil, fmt.Errorf("scan job: %w", e)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
