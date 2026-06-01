// SQLite-backed job log. Stores full markdown content so failed jobs can be
// reprinted from the web UI without the original caller still being present.
package jobs

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type Status string

const (
	StatusQueued  Status = "queued"
	StatusSent    Status = "sent"
	StatusFailed  Status = "failed"
	StatusExpired Status = "expired"
)

// Default job lifetime — after this, queued/failed jobs become "expired".
const DefaultExpiry = time.Hour

type Job struct {
	ID         string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	Source     string // "mcp" | "web" | "apk"
	Status     Status
	Content    string // full markdown
	Error      string
	RetryCount int
	SentAt     *time.Time
}

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS jobs (
    id          TEXT PRIMARY KEY,
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,
    source      TEXT NOT NULL,
    status      TEXT NOT NULL,
    content     TEXT NOT NULL,
    error       TEXT NOT NULL DEFAULT '',
    retry_count INTEGER NOT NULL DEFAULT 0,
    sent_at     INTEGER
);
CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
CREATE INDEX IF NOT EXISTS idx_jobs_created ON jobs(created_at DESC);
`

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Enqueue creates a new queued job and returns its ID.
func (s *Store) Enqueue(source, content string) (*Job, error) {
	now := time.Now()
	j := &Job{
		ID:        uuid.NewString(),
		CreatedAt: now,
		ExpiresAt: now.Add(DefaultExpiry),
		Source:    source,
		Status:    StatusQueued,
		Content:   content,
	}
	_, err := s.db.Exec(
		`INSERT INTO jobs(id, created_at, expires_at, source, status, content) VALUES (?,?,?,?,?,?)`,
		j.ID, j.CreatedAt.UnixNano(), j.ExpiresAt.UnixNano(), j.Source, string(j.Status), j.Content,
	)
	if err != nil {
		return nil, err
	}
	return j, nil
}

func (s *Store) Get(id string) (*Job, error) {
	row := s.db.QueryRow(
		`SELECT id, created_at, expires_at, source, status, content, error, retry_count, sent_at FROM jobs WHERE id = ?`,
		id,
	)
	return scan(row)
}

// NextQueued returns the oldest queued, non-expired job — or nil if none.
func (s *Store) NextQueued() (*Job, error) {
	row := s.db.QueryRow(
		`SELECT id, created_at, expires_at, source, status, content, error, retry_count, sent_at
		 FROM jobs WHERE status = ? AND expires_at > ?
		 ORDER BY created_at ASC LIMIT 1`,
		string(StatusQueued), time.Now().UnixNano(),
	)
	j, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return j, err
}

// List returns up to limit most-recent jobs (any status).
func (s *Store) List(limit int) ([]*Job, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT id, created_at, expires_at, source, status, content, error, retry_count, sent_at
		 FROM jobs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		j, err := scanRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// CountByStatus is for status endpoints / metrics.
func (s *Store) CountByStatus(st Status) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status = ?`, string(st)).Scan(&n)
	return n, err
}

func (s *Store) MarkSent(id string) error {
	now := time.Now().UnixNano()
	_, err := s.db.Exec(`UPDATE jobs SET status = ?, sent_at = ?, error = '' WHERE id = ?`,
		string(StatusSent), now, id)
	return err
}

func (s *Store) MarkFailed(id, errMsg string) error {
	_, err := s.db.Exec(`UPDATE jobs SET status = ?, error = ? WHERE id = ?`,
		string(StatusFailed), errMsg, id)
	return err
}

func (s *Store) BumpRetry(id, lastErr string) (int, error) {
	_, err := s.db.Exec(`UPDATE jobs SET retry_count = retry_count + 1, error = ? WHERE id = ?`,
		lastErr, id)
	if err != nil {
		return 0, err
	}
	var n int
	err = s.db.QueryRow(`SELECT retry_count FROM jobs WHERE id = ?`, id).Scan(&n)
	return n, err
}

// SweepExpired marks all not-yet-sent jobs past their expiry as "expired".
// Returns the number of jobs affected.
func (s *Store) SweepExpired() (int64, error) {
	res, err := s.db.Exec(
		`UPDATE jobs SET status = ?
		 WHERE status IN (?, ?) AND expires_at <= ?`,
		string(StatusExpired), string(StatusQueued), string(StatusFailed),
		time.Now().UnixNano(),
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Requeue copies an existing job's content into a new queued job (reprint).
func (s *Store) Requeue(id, source string) (*Job, error) {
	old, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	return s.Enqueue(source, old.Content)
}

type scanner interface{ Scan(dest ...any) error }

func scan(r scanner) (*Job, error) {
	var j Job
	var createdNs, expiresNs int64
	var sentNs sql.NullInt64
	var status string
	if err := r.Scan(&j.ID, &createdNs, &expiresNs, &j.Source, &status, &j.Content, &j.Error, &j.RetryCount, &sentNs); err != nil {
		return nil, err
	}
	j.CreatedAt = time.Unix(0, createdNs)
	j.ExpiresAt = time.Unix(0, expiresNs)
	j.Status = Status(status)
	if sentNs.Valid {
		t := time.Unix(0, sentNs.Int64)
		j.SentAt = &t
	}
	return &j, nil
}

func scanRows(r *sql.Rows) (*Job, error) { return scan(r) }
