// Package audit records append-only audit events for administrative and sensitive actions (§13.2, §16.3).
package audit

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/shared/reqctx"
)

// Entry is one audit event.
type Entry struct {
	ActorUserID *uint64
	Action      string
	EntityType  string
	EntityID    string
	OldValues   map[string]any
	NewValues   map[string]any
}

// sensitiveKeys are never written to audit values, at any nesting level.
var sensitiveKeys = []string{"pin", "otp", "code", "password", "token", "secret", "hash", "session", "api_key"}

func isSensitive(k string) bool {
	k = strings.ToLower(k)
	for _, s := range sensitiveKeys {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// Redact returns a copy with sensitive fields replaced by "[redacted]".
func Redact(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if isSensitive(k) {
			out[k] = "[redacted]"
			continue
		}
		if m, ok := v.(map[string]any); ok {
			out[k] = Redact(m)
			continue
		}
		out[k] = v
	}
	return out
}

// Record writes an entry using ex (pass the transaction that made the change).
func Record(ctx context.Context, ex database.Execer, e Entry) error {
	var oldJSON, newJSON []byte
	var err error
	if e.OldValues != nil {
		if oldJSON, err = json.Marshal(Redact(e.OldValues)); err != nil {
			return err
		}
	}
	if e.NewValues != nil {
		if newJSON, err = json.Marshal(Redact(e.NewValues)); err != nil {
			return err
		}
	}
	actor := e.ActorUserID
	if actor == nil {
		if p := reqctx.PrincipalFrom(ctx); p != nil {
			id := p.UserID
			actor = &id
		}
	}
	_, err = ex.ExecContext(ctx, `INSERT INTO audit_logs
		(actor_user_id, action, entity_type, entity_id, old_values, new_values, ip_address, user_agent, request_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		actor, e.Action, e.EntityType, nullStr(e.EntityID), nullJSON(oldJSON), nullJSON(newJSON),
		nullStr(reqctx.ClientIP(ctx)), nullStr(reqctx.UserAgent(ctx)), nullStr(reqctx.RequestID(ctx)), time.Now().UTC())
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}
