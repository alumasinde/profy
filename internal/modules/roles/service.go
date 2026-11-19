// Package roles provides administration of roles, permissions, user role assignment,
// user status and the audit-log viewer (plan §5, §15.6).
package roles

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alumasinde/profy/internal/authz"
	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/modules/audit"
	"github.com/alumasinde/profy/internal/modules/auth"
	"github.com/alumasinde/profy/internal/modules/users"
	"github.com/alumasinde/profy/internal/shared/ids"
	"github.com/alumasinde/profy/internal/shared/paging"
	"github.com/alumasinde/profy/internal/shared/reqctx"
)

// Service implements role and user administration.
type Service struct {
	db    *sql.DB
	authz *authz.Service
	users users.Repository
}

// New builds the service.
func New(db *sql.DB, az *authz.Service) *Service { return &Service{db: db, authz: az} }

// RoleView is a role with its permissions.
type RoleView struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	IsSystem    bool     `json:"is_system"`
	IsProtected bool     `json:"is_protected"`
	UserCount   int      `json:"user_count"`
	Permissions []string `json:"permissions"`
}

// ListRoles returns every role with its permission keys.
func (s *Service) ListRoles(ctx context.Context) ([]RoleView, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.id, r.role_key, r.name, r.description, r.is_system, r.is_protected,
			(SELECT COUNT(*) FROM user_roles ur WHERE ur.role_id = r.id)
		FROM roles r ORDER BY r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RoleView
	byID := map[int]int{}
	for rows.Next() {
		var id int
		var v RoleView
		if err := rows.Scan(&id, &v.Key, &v.Name, &v.Description, &v.IsSystem, &v.IsProtected, &v.UserCount); err != nil {
			return nil, err
		}
		v.Permissions = []string{}
		byID[id] = len(out)
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	prow, err := s.db.QueryContext(ctx, `SELECT rp.role_id, p.permission_key FROM role_permissions rp
		JOIN permissions p ON p.id = rp.permission_id ORDER BY p.permission_key`)
	if err != nil {
		return nil, err
	}
	defer prow.Close()
	for prow.Next() {
		var id int
		var k string
		if err := prow.Scan(&id, &k); err != nil {
			return nil, err
		}
		if i, ok := byID[id]; ok && authz.Known(k) {
			out[i].Permissions = append(out[i].Permissions, k)
		}
	}
	return out, prow.Err()
}

// ErrProtectedRole is returned when editing the always-complete super administrator role.
var ErrProtectedRole = response.New(http.StatusConflict, response.CodeConflict, "This role's permissions are fixed and cannot be edited.")

// SetRolePermissions replaces a role's permission set (audited).
func (s *Service) SetRolePermissions(ctx context.Context, roleKey string, keys []string) (*RoleView, error) {
	def, isSystem := authz.RoleDefByKey(roleKey)
	if isSystem && def.AllPerms {
		return nil, ErrProtectedRole
	}
	clean := []string{}
	for _, k := range keys {
		if !authz.Known(k) {
			return nil, response.Validation(map[string]string{"permissions": "unknown:" + k})
		}
		if !slices.Contains(clean, k) {
			clean = append(clean, k)
		}
	}
	sort.Strings(clean)
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		var roleID int
		err := tx.QueryRowContext(ctx, `SELECT id FROM roles WHERE role_key = ? FOR UPDATE`, roleKey).Scan(&roleID)
		if errors.Is(err, sql.ErrNoRows) {
			return response.ErrNotFound
		}
		if err != nil {
			return err
		}
		old, err := stringsQuery(ctx, tx, `SELECT p.permission_key FROM role_permissions rp JOIN permissions p ON p.id = rp.permission_id
			WHERE rp.role_id = ? ORDER BY p.permission_key`, roleID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM role_permissions WHERE role_id = ?`, roleID); err != nil {
			return err
		}
		for _, k := range clean {
			if _, err := tx.ExecContext(ctx, `INSERT INTO role_permissions (role_id, permission_id)
				SELECT ?, id FROM permissions WHERE permission_key = ?`, roleID, k); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "roles.permissions.updated", EntityType: "role", EntityID: roleKey,
			OldValues: map[string]any{"permissions": old}, NewValues: map[string]any{"permissions": clean}})
	})
	if err != nil {
		return nil, err
	}
	s.authz.Invalidate()
	list, err := s.ListRoles(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range list {
		if r.Key == roleKey {
			return &r, nil
		}
	}
	return nil, response.ErrNotFound
}

// PermissionView is a catalogue entry.
type PermissionView struct {
	Key         string `json:"key"`
	Module      string `json:"module"`
	Description string `json:"description"`
}

// ListPermissions returns the code-defined catalogue.
func (s *Service) ListPermissions() []PermissionView {
	out := make([]PermissionView, 0, len(authz.Catalogue))
	for _, p := range authz.Catalogue {
		out = append(out, PermissionView{p.Key, p.Module, p.Description})
	}
	return out
}

// ---- Users ----------------------------------------------------------------------------

// AdminUserView is a user in admin listings.
type AdminUserView struct {
	PublicID      string     `json:"public_id"`
	FirstName     string     `json:"first_name"`
	MiddleName    *string    `json:"middle_name"`
	LastName      string     `json:"last_name"`
	DisplayName   string     `json:"display_name"`
	Email         *string    `json:"email"`
	Phone         *string    `json:"phone"`
	EmailVerified bool       `json:"email_verified"`
	PhoneVerified bool       `json:"phone_verified"`
	Status        string     `json:"status"`
	Roles         []string   `json:"roles"`
	LastLoginAt   *time.Time `json:"last_login_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

// ListUsersInput filters the user list.
type ListUsersInput struct {
	Status string
	Query  string
	Cursor string
	Limit  int
}

// ListUsers returns users newest first with keyset pagination.
func (s *Service) ListUsers(ctx context.Context, in ListUsersInput) ([]AdminUserView, string, error) {
	after, ok := paging.Decode(in.Cursor)
	if !ok {
		return nil, "", response.Validation(map[string]string{"cursor": "invalid"})
	}
	limit := paging.Limit(in.Limit, 25, 100)
	q := `SELECT id, public_id, first_name, middle_name, last_name, email, phone_e164, email_verified_at IS NOT NULL,
			phone_verified_at IS NOT NULL, status, last_login_at, created_at FROM users WHERE 1=1`
	args := []any{}
	if after > 0 {
		q += ` AND id < ?`
		args = append(args, after)
	}
	if in.Status != "" {
		q += ` AND status = ?`
		args = append(args, in.Status)
	}
	if t := strings.TrimSpace(in.Query); t != "" {
		like := escapeLike(strings.ToLower(t)) + "%"
		q += ` AND (first_name LIKE ? OR last_name LIKE ? OR email LIKE ? OR phone_e164 LIKE ?)`
		phone := like
		if p, ok := normalizePhonePrefix(t); ok {
			phone = p + "%"
		}
		args = append(args, like, like, like, phone)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []AdminUserView
	var idList []uint64
	for rows.Next() {
		var id uint64
		var pub ids.UUID
		var v AdminUserView
		var middle, email, phone sql.NullString
		var ll sql.NullTime
		if err := rows.Scan(&id, &pub, &v.FirstName, &middle, &v.LastName, &email, &phone, &v.EmailVerified,
			&v.PhoneVerified, &v.Status, &ll, &v.CreatedAt); err != nil {
			return nil, "", err
		}
		v.PublicID = pub.String()
		if middle.Valid {
			v.MiddleName = &middle.String
		}
		if email.Valid {
			v.Email = &email.String
		}
		if phone.Valid {
			v.Phone = &phone.String
		}
		if ll.Valid {
			v.LastLoginAt = &ll.Time
		}
		v.DisplayName = users.Names{First: v.FirstName, Middle: v.MiddleName, Last: v.LastName}.Display()
		out = append(out, v)
		idList = append(idList, id)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out, idList = out[:limit], idList[:limit]
		next = paging.Encode(idList[limit-1])
	}
	if err := s.attachRoles(ctx, out, idList); err != nil {
		return nil, "", err
	}
	if out == nil {
		out = []AdminUserView{}
	}
	return out, next, nil
}

// attachRoles loads roles for a page of users in one query (no N+1).
func (s *Service) attachRoles(ctx context.Context, list []AdminUserView, idList []uint64) error {
	if len(list) == 0 {
		return nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(idList)), ",")
	args := make([]any, len(idList))
	index := map[uint64]int{}
	for i, id := range idList {
		args[i] = id
		index[id] = i
		list[i].Roles = []string{}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT ur.user_id, r.role_key FROM user_roles ur JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id IN (`+ph+`) ORDER BY r.role_key`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id uint64
		var k string
		if err := rows.Scan(&id, &k); err != nil {
			return err
		}
		list[index[id]].Roles = append(list[index[id]].Roles, k)
	}
	return rows.Err()
}

// GetUser returns one user by public ID.
func (s *Service) GetUser(ctx context.Context, publicID string) (*AdminUserView, error) {
	u, err := s.lookup(ctx, s.db, publicID)
	if err != nil {
		return nil, err
	}
	roles, err := s.users.RoleKeys(ctx, s.db, u.ID)
	if err != nil {
		return nil, err
	}
	v := &AdminUserView{
		PublicID: u.PublicID.String(), FirstName: u.Names.First, MiddleName: u.Names.Middle, LastName: u.Names.Last,
		DisplayName: u.Names.Display(), Email: u.Email, Phone: u.PhoneE164, EmailVerified: u.EmailVerifiedAt != nil,
		PhoneVerified: u.PhoneVerifiedAt != nil, Status: u.Status, Roles: roles, LastLoginAt: u.LastLoginAt, CreatedAt: u.CreatedAt,
	}
	return v, nil
}

func (s *Service) lookup(ctx context.Context, ex database.Execer, publicID string) (*users.User, error) {
	pub, err := ids.Parse(publicID)
	if err != nil {
		return nil, response.ErrNotFound
	}
	u, err := s.users.ByPublicID(ctx, ex, pub)
	if errors.Is(err, users.ErrNotFound) {
		return nil, response.ErrNotFound
	}
	return u, err
}

var errLastSuperAdmin = response.New(http.StatusConflict, response.CodeConflict, "At least one active super administrator must remain.")

// actorHoldsRole reports whether the current principal holds roleKey (used only for the
// protected-role rule: a protected role can be granted or removed only by a holder).
func (s *Service) actorHoldsRole(ctx context.Context, ex database.Execer, roleKey string) (bool, error) {
	p := reqctx.PrincipalFrom(ctx)
	if p == nil {
		return false, nil
	}
	var n int
	err := ex.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_roles ur JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = ? AND r.role_key = ?`, p.UserID, roleKey).Scan(&n)
	return n > 0, err
}

// SetUserRoles replaces a user's roles (audited). Protected roles can be granted or removed
// only by an actor who holds that role; the last active super administrator cannot be removed.
func (s *Service) SetUserRoles(ctx context.Context, publicID string, roleKeys []string) (*AdminUserView, error) {
	clean := []string{}
	for _, k := range roleKeys {
		if !slices.Contains(clean, k) {
			clean = append(clean, k)
		}
	}
	sort.Strings(clean)
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		u, err := s.lookup(ctx, tx, publicID)
		if err != nil {
			return err
		}
		var locked uint64 // serialize concurrent role changes for this user
		if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id = ? FOR UPDATE`, u.ID).Scan(&locked); err != nil {
			return err
		}
		old, err := s.users.RoleKeys(ctx, tx, u.ID)
		if err != nil {
			return err
		}
		protected, err := stringsQuery(ctx, tx, `SELECT role_key FROM roles WHERE is_protected = 1`)
		if err != nil {
			return err
		}
		known, err := stringsQuery(ctx, tx, `SELECT role_key FROM roles`)
		if err != nil {
			return err
		}
		for _, k := range clean {
			if !slices.Contains(known, k) {
				return response.Validation(map[string]string{"roles": "unknown:" + k})
			}
		}
		for _, pk := range protected {
			if slices.Contains(old, pk) == slices.Contains(clean, pk) {
				continue
			}
			holds, err := s.actorHoldsRole(ctx, tx, pk)
			if err != nil {
				return err
			}
			if !holds {
				return response.ErrForbidden
			}
			if pk == authz.RoleSuperAdmin && slices.Contains(old, pk) {
				var remaining int
				if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_roles ur JOIN roles r ON r.id = ur.role_id
					JOIN users x ON x.id = ur.user_id
					WHERE r.role_key = ? AND x.status = 'active' AND ur.user_id <> ? FOR UPDATE`, pk, u.ID).Scan(&remaining); err != nil {
					return err
				}
				if remaining == 0 {
					return errLastSuperAdmin
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_roles WHERE user_id = ?`, u.ID); err != nil {
			return err
		}
		var by *uint64
		if p := reqctx.PrincipalFrom(ctx); p != nil {
			by = &p.UserID
		}
		for _, k := range clean {
			if err := s.users.AssignRole(ctx, tx, u.ID, k, by); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "users.roles.updated", EntityType: "user", EntityID: u.PublicID.String(),
			OldValues: map[string]any{"roles": old}, NewValues: map[string]any{"roles": clean}})
	})
	if err != nil {
		return nil, err
	}
	s.authz.Invalidate()
	return s.GetUser(ctx, publicID)
}

// SetUserStatus suspends or restores a user (audited). Suspension revokes all sessions.
// requiredPerm is checked by the caller's route ("users.suspend" or "users.restore").
func (s *Service) SetUserStatus(ctx context.Context, publicID, status, reason string) (*AdminUserView, error) {
	if status != users.StatusActive && status != users.StatusSuspended {
		return nil, response.Validation(map[string]string{"status": "invalid"})
	}
	reason = strings.TrimSpace(reason)
	if status == users.StatusSuspended && (reason == "" || len(reason) > 500) {
		return nil, response.Validation(map[string]string{"reason": "required"})
	}
	p := reqctx.PrincipalFrom(ctx)
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		u, err := s.lookup(ctx, tx, publicID)
		if err != nil {
			return err
		}
		if p != nil && u.ID == p.UserID {
			return response.New(http.StatusConflict, response.CodeConflict, "You cannot change your own account status.")
		}
		if u.Status == status {
			return nil
		}
		if u.Status != users.StatusActive && u.Status != users.StatusSuspended {
			return response.New(http.StatusConflict, response.CodeConflict, "This account's status cannot be changed here.")
		}
		roles, err := s.users.RoleKeys(ctx, tx, u.ID)
		if err != nil {
			return err
		}
		if slices.Contains(roles, authz.RoleSuperAdmin) {
			holds, err := s.actorHoldsRole(ctx, tx, authz.RoleSuperAdmin)
			if err != nil {
				return err
			}
			if !holds {
				return response.ErrForbidden
			}
		}
		if err := s.users.SetStatus(ctx, tx, u.ID, status); err != nil {
			return err
		}
		if status == users.StatusSuspended {
			if err := auth.RevokeAllForUser(ctx, tx, u.ID, "suspended"); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "users.status.updated", EntityType: "user", EntityID: u.PublicID.String(),
			OldValues: map[string]any{"status": u.Status}, NewValues: map[string]any{"status": status, "reason": reason}})
	})
	if err != nil {
		return nil, err
	}
	s.authz.Invalidate()
	return s.GetUser(ctx, publicID)
}

// ---- Audit logs -----------------------------------------------------------------------

// AuditView is one audit entry.
type AuditView struct {
	ID         string          `json:"id"`
	Actor      *string         `json:"actor_public_id"`
	ActorName  *string         `json:"actor_name"`
	Action     string          `json:"action"`
	EntityType string          `json:"entity_type"`
	EntityID   *string         `json:"entity_id"`
	OldValues  json.RawMessage `json:"old_values,omitempty"`
	NewValues  json.RawMessage `json:"new_values,omitempty"`
	IPAddress  *string         `json:"ip_address,omitempty"`
	RequestID  *string         `json:"request_id"`
	CreatedAt  time.Time       `json:"created_at"`
}

// ListAuditInput filters the audit log.
type ListAuditInput struct {
	EntityType, EntityID, Action, Cursor string
	Limit                                int
	IncludeSensitive                     bool // IP addresses; requires audit_logs.view_sensitive
}

// ListAudit returns audit entries newest first.
func (s *Service) ListAudit(ctx context.Context, in ListAuditInput) ([]AuditView, string, error) {
	after, ok := paging.Decode(in.Cursor)
	if !ok {
		return nil, "", response.Validation(map[string]string{"cursor": "invalid"})
	}
	limit := paging.Limit(in.Limit, 25, 100)
	q := `SELECT a.id, u.public_id, u.first_name, u.middle_name, u.last_name, a.action, a.entity_type, a.entity_id,
			a.old_values, a.new_values, a.ip_address, a.request_id, a.created_at
		FROM audit_logs a LEFT JOIN users u ON u.id = a.actor_user_id WHERE 1=1`
	args := []any{}
	if after > 0 {
		q += ` AND a.id < ?`
		args = append(args, after)
	}
	if in.EntityType != "" {
		q += ` AND a.entity_type = ?`
		args = append(args, in.EntityType)
		if in.EntityID != "" {
			q += ` AND a.entity_id = ?`
			args = append(args, in.EntityID)
		}
	}
	if in.Action != "" {
		q += ` AND a.action = ?`
		args = append(args, in.Action)
	}
	q += ` ORDER BY a.id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []AuditView{}
	var lastIDs []uint64
	for rows.Next() {
		var v AuditView
		var id uint64
		var pub []byte
		var first, middle, last, entityID, ip, reqID sql.NullString
		var oldV, newV []byte
		if err := rows.Scan(&id, &pub, &first, &middle, &last, &v.Action, &v.EntityType, &entityID, &oldV, &newV, &ip, &reqID, &v.CreatedAt); err != nil {
			return nil, "", err
		}
		v.ID = strconv.FormatUint(id, 10)
		if len(pub) == 16 {
			var u ids.UUID
			copy(u[:], pub)
			ps := u.String()
			v.Actor = &ps
			var mid *string
			if middle.Valid {
				mid = &middle.String
			}
			name := users.Names{First: first.String, Middle: mid, Last: last.String}.Display()
			v.ActorName = &name
		}
		if entityID.Valid {
			v.EntityID = &entityID.String
		}
		if reqID.Valid {
			v.RequestID = &reqID.String
		}
		if in.IncludeSensitive && ip.Valid {
			v.IPAddress = &ip.String
		}
		v.OldValues, v.NewValues = oldV, newV
		out = append(out, v)
		lastIDs = append(lastIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = paging.Encode(lastIDs[limit-1])
	}
	return out, next, nil
}

func stringsQuery(ctx context.Context, ex database.Execer, q string, args ...any) ([]string, error) {
	rows, err := ex.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func normalizePhonePrefix(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "07") || strings.HasPrefix(s, "01") {
		return "+254" + escapeLike(s[1:]), true
	}
	return "", false
}
