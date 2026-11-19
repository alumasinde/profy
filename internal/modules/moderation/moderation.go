// Package moderation records moderation decisions (plan §12.6). Every decision also gets an
// audit entry, written by the caller in the same transaction.
package moderation

import (
	"context"

	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/shared/reqctx"
)

// Record writes one moderation action (who, what, why, target).
func Record(ctx context.Context, ex database.Execer, targetType, targetID, action, reason string) error {
	var actor any
	if p := reqctx.PrincipalFrom(ctx); p != nil {
		actor = p.UserID
	}
	var r any
	if reason != "" {
		r = reason
	}
	_, err := ex.ExecContext(ctx, `INSERT INTO moderation_actions (actor_user_id, target_type, target_id, action, reason)
		VALUES (?, ?, ?, ?, ?)`, actor, targetType, targetID, action, r)
	return err
}
