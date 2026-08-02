package storage

import (
	"database/sql"
	"fmt"
	"github.com/tiredbooy/internal/models"
)

type JobStore struct{ db *sql.DB }

func NewJobStore(db *sql.DB) *JobStore { return &JobStore{db: db} }
func (s *JobStore) Create(kind, payload string) (int64, error) {
	r, e := s.db.Exec(`INSERT INTO jobs(type,payload) VALUES (?,?)`, kind, payload)
	if e != nil {
		return 0, fmt.Errorf("create job: %w", e)
	}
	return r.LastInsertId()
}
func (s *JobStore) Update(id int64, status string, current, total int, message, jobErr string) error {
	_, e := s.db.Exec(`UPDATE jobs SET status=?,progress_current=?,progress_total=?,message=?,error=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, status, current, total, message, jobErr, id)
	return e
}
func (s *JobStore) Active() ([]models.Job, error) {
	return s.list(`SELECT id,type,payload,status,progress_current,progress_total,message,error,created_at,updated_at FROM jobs WHERE status IN ('queued','running','paused') ORDER BY created_at`)
}
func (s *JobStore) list(q string) ([]models.Job, error) {
	rows, e := s.db.Query(q)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []models.Job{}
	for rows.Next() {
		var j models.Job
		if e := rows.Scan(&j.ID, &j.Type, &j.Payload, &j.Status, &j.ProgressCurrent, &j.ProgressTotal, &j.Message, &j.Error, &j.CreatedAt, &j.UpdatedAt); e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
