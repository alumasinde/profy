package jobsqueue

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// ScheduleDaily enqueues each kind once per UTC day (deduplicated, so several instances or
// restarts never double-run it), checking hourly until ctx is cancelled.
func ScheduleDaily(ctx context.Context, db *sql.DB, log *slog.Logger, kinds ...string) {
	enqueue := func() {
		day := time.Now().UTC().Format("2006-01-02")
		for _, k := range kinds {
			if err := Enqueue(ctx, db, k, map[string]string{"day": day},
				Options{DedupeKey: k + ":" + day, MaxAttempts: 3}); err != nil && ctx.Err() == nil {
				log.Error("schedule daily job", "kind", k, "error", err)
			}
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
