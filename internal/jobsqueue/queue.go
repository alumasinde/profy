// Package jobsqueue is the in-process background worker fed by the MySQL-backed
// background_jobs outbox, so work survives restarts (plan §6.5). No external queue.
package jobsqueue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sync"
	"time"

	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/security"
)

// Handler processes one job payload. Returning an error schedules a retry with backoff.
type Handler func(ctx context.Context, payload json.RawMessage) error

// Queue enqueues and runs jobs.
type Queue struct {
	db       *sql.DB
	log      *slog.Logger
	handlers map[string]Handler
	workerID string
	mu       sync.RWMutex
}

// New creates a queue.
func New(db *sql.DB, log *slog.Logger) *Queue {
	host, _ := os.Hostname()
	return &Queue{db: db, log: log, handlers: map[string]Handler{}, workerID: fmt.Sprintf("%s-%s", host, security.RandomToken(4))}
}

// Handle registers the handler for a job kind.
func (q *Queue) Handle(kind string, h Handler) {
	q.mu.Lock()
	q.handlers[kind] = h
	q.mu.Unlock()
}

// Options for Enqueue.
type Options struct {
	RunAt       time.Time
	MaxAttempts int
	DedupeKey   string // at most one job per key; a duplicate enqueue is a no-op
}

// Enqueue adds a job using ex — pass the business transaction so the job commits atomically
// with the change that caused it (transactional outbox). Never put secrets in payload.
func Enqueue(ctx context.Context, ex database.Execer, kind string, payload any, opt Options) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	runAt := opt.RunAt
	if runAt.IsZero() {
		runAt = time.Now().UTC()
	}
	maxAttempts := opt.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 8
	}
	var dedupe any
	if opt.DedupeKey != "" {
		dedupe = opt.DedupeKey
	}
	_, err = ex.ExecContext(ctx, `INSERT IGNORE INTO background_jobs (kind, payload, run_at, max_attempts, dedupe_key)
		VALUES (?, ?, ?, ?, ?)`, kind, string(b), runAt.UTC(), maxAttempts, dedupe)
	return err
}

type job struct {
	id          uint64
	kind        string
	payload     json.RawMessage
	attempts    int
	maxAttempts int
}

// claim atomically locks one due job using SKIP LOCKED so multiple workers never collide.
func (q *Queue) claim(ctx context.Context) (*job, error) {
	var j *job
	err := database.WithTx(ctx, q.db, func(tx *sql.Tx) error {
		var c job
		var payload []byte
		err := tx.QueryRowContext(ctx, `SELECT id, kind, payload, attempts, max_attempts FROM background_jobs
			WHERE status = 'pending' AND run_at <= ? ORDER BY run_at, id LIMIT 1 FOR UPDATE SKIP LOCKED`,
			time.Now().UTC()).Scan(&c.id, &c.kind, &payload, &c.attempts, &c.maxAttempts)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		c.payload = payload
		if _, err := tx.ExecContext(ctx, `UPDATE background_jobs SET status = 'running', locked_at = ?, locked_by = ?,
			attempts = attempts + 1 WHERE id = ?`, time.Now().UTC(), q.workerID, c.id); err != nil {
			return err
		}
		c.attempts++
		j = &c
		return nil
	})
	return j, err
}

// Backoff returns the retry delay after n attempts: 10s, 20s, 40s … capped at 1h.
func Backoff(n int) time.Duration {
	d := time.Duration(float64(10*time.Second) * math.Pow(2, float64(n-1)))
	if d > time.Hour || d <= 0 {
		d = time.Hour
	}
	return d
}

func (q *Queue) finish(ctx context.Context, j *job, runErr error) {
	var err error
	switch {
	case runErr == nil:
		_, err = q.db.ExecContext(ctx, `UPDATE background_jobs SET status = 'done', locked_at = NULL, last_error = NULL WHERE id = ?`, j.id)
	case j.attempts >= j.maxAttempts:
		_, err = q.db.ExecContext(ctx, `UPDATE background_jobs SET status = 'failed', locked_at = NULL, last_error = ? WHERE id = ?`,
			truncate(runErr.Error(), 1000), j.id)
		q.log.Error("job failed permanently", "id", j.id, "kind", j.kind, "attempts", j.attempts, "error", runErr)
	default:
		_, err = q.db.ExecContext(ctx, `UPDATE background_jobs SET status = 'pending', locked_at = NULL, last_error = ?, run_at = ? WHERE id = ?`,
			truncate(runErr.Error(), 1000), time.Now().UTC().Add(Backoff(j.attempts)), j.id)
		q.log.Warn("job failed; will retry", "id", j.id, "kind", j.kind, "attempts", j.attempts, "error", runErr)
	}
	if err != nil {
		q.log.Error("job status update failed", "id", j.id, "error", err)
	}
}

// RunOnce claims and runs at most one job. It reports whether a job was processed.
func (q *Queue) RunOnce(ctx context.Context) (bool, error) {
	j, err := q.claim(ctx)
	if err != nil || j == nil {
		return false, err
	}
	q.mu.RLock()
	h := q.handlers[j.kind]
	q.mu.RUnlock()
	var runErr error
	if h == nil {
		runErr = fmt.Errorf("no handler for job kind %q", j.kind)
	} else {
		runErr = safeRun(ctx, h, j.payload)
	}
	q.finish(context.WithoutCancel(ctx), j, runErr)
	return true, nil
}

func safeRun(ctx context.Context, h Handler, payload json.RawMessage) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("panic: %v", v)
		}
	}()
	jobCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	return h(jobCtx, payload)
}

// RecoverStale returns jobs left 'running' by a crashed worker to the pending state.
func (q *Queue) RecoverStale(ctx context.Context, olderThan time.Duration) (int64, error) {
	res, err := q.db.ExecContext(ctx, `UPDATE background_jobs SET status = 'pending', locked_at = NULL
		WHERE status = 'running' AND locked_at < ?`, time.Now().UTC().Add(-olderThan))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Run starts n workers polling every interval until ctx is cancelled, then waits for them.
func (q *Queue) Run(ctx context.Context, n int, interval time.Duration) {
	if c, err := q.RecoverStale(ctx, 10*time.Minute); err != nil {
		q.log.Error("recover stale jobs", "error", err)
	} else if c > 0 {
		q.log.Warn("recovered stale jobs", "count", c)
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t := time.NewTicker(interval)
			defer t.Stop()
			for {
				for { // drain due jobs before sleeping
					if ctx.Err() != nil {
						return
					}
					did, err := q.RunOnce(ctx)
					if err != nil && ctx.Err() == nil {
						q.log.Error("job claim failed", "error", err)
					}
					if !did {
						break
					}
				}
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		}()
	}
	wg.Wait()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
