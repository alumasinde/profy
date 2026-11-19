package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/alumasinde/profy/internal/jobsqueue"
)

// JobPruneAuth is the daily retention job kind (§16.2 proposed schedule).
const JobPruneAuth = "maintenance.prune_auth"

// Retention periods (Proposed, §16.2).
const (
	RetainOTPChallenges = 30 * 24 * time.Hour
	RetainSessions      = 90 * 24 * time.Hour // after expiry or revocation
	RetainFinishedJobs  = 30 * 24 * time.Hour
)

// RegisterMaintenance registers the retention handler with the queue.
func RegisterMaintenance(q *jobsqueue.Queue, db *sql.DB) {
	q.Handle(JobPruneAuth, func(ctx context.Context, _ json.RawMessage) error {
		now := time.Now().UTC()
		stmts := []struct {
			q   string
			arg time.Time
		}{
			{`DELETE FROM otp_challenges WHERE created_at < ? LIMIT 10000`, now.Add(-RetainOTPChallenges)},
			{`DELETE FROM user_sessions WHERE COALESCE(revoked_at, absolute_expires_at) < ? LIMIT 10000`, now.Add(-RetainSessions)},
			{`DELETE FROM background_jobs WHERE status = 'done' AND updated_at < ? LIMIT 10000`, now.Add(-RetainFinishedJobs)},
		}
		for _, s := range stmts {
			if _, err := db.ExecContext(ctx, s.q, s.arg); err != nil {
				return err
			}
		}
		return nil
	})
}

// ScheduleMaintenance enqueues the retention job once per UTC day (deduplicated across
// restarts and instances) until ctx is cancelled.
func ScheduleMaintenance(ctx context.Context, db *sql.DB, log *slog.Logger) {
	enqueue := func() {
		day := time.Now().UTC().Format("2006-01-02")
		if err := jobsqueue.Enqueue(ctx, db, JobPruneAuth, map[string]string{"day": day},
			jobsqueue.Options{DedupeKey: JobPruneAuth + ":" + day, MaxAttempts: 3}); err != nil && ctx.Err() == nil {
			log.Error("schedule maintenance", "error", err)
		}
	}
	enqueue()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			enqueue()
		}
	}
}
