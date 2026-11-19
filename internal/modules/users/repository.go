// Package users owns the identity table (plan §11.1, §13.2).
package users

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/shared/ids"
)

// Statuses.
const (
	StatusActive          = "active"
	StatusSuspended       = "suspended"
	StatusPendingDeletion = "pending_deletion"
	StatusDeleted         = "deleted"
)

// User is the internal identity record.
type User struct {
	ID              uint64
	PublicID        ids.UUID
	Names           Names
	Email           *string
	PhoneE164       *string
	EmailVerifiedAt *time.Time
	PhoneVerifiedAt *time.Time
	PreferredLocale string
	Status          string
	LastLoginAt     *time.Time
	CreatedAt       time.Time
}

// ErrNotFound is returned when no user matches.
var ErrNotFound = errors.New("user not found")

const columns = `id, public_id, first_name, middle_name, last_name, email, phone_e164,
	email_verified_at, phone_verified_at, preferred_locale, status, last_login_at, created_at`

func scan(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var middle sql.NullString
	var email, phone sql.NullString
	var ev, pv, ll sql.NullTime
	err := row.Scan(&u.ID, &u.PublicID, &u.Names.First, &middle, &u.Names.Last, &email, &phone,
		&ev, &pv, &u.PreferredLocale, &u.Status, &ll, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if middle.Valid {
		u.Names.Middle = &middle.String
	}
	if email.Valid {
		u.Email = &email.String
	}
	if phone.Valid {
		u.PhoneE164 = &phone.String
	}
	if ev.Valid {
		u.EmailVerifiedAt = &ev.Time
	}
	if pv.Valid {
		u.PhoneVerifiedAt = &pv.Time
	}
	if ll.Valid {
		u.LastLoginAt = &ll.Time
	}
	return &u, nil
}

// Repository persists users.
type Repository struct{}

// ByID loads a user by internal ID.
func (Repository) ByID(ctx context.Context, ex database.Execer, id uint64) (*User, error) {
	return scan(ex.QueryRowContext(ctx, `SELECT `+columns+` FROM users WHERE id = ?`, id))
}

// ByPublicID loads a user by public ID.
func (Repository) ByPublicID(ctx context.Context, ex database.Execer, id ids.UUID) (*User, error) {
	return scan(ex.QueryRowContext(ctx, `SELECT `+columns+` FROM users WHERE public_id = ?`, id))
}

// ByEmail loads a user by normalized email.
func (Repository) ByEmail(ctx context.Context, ex database.Execer, email string) (*User, error) {
	return scan(ex.QueryRowContext(ctx, `SELECT `+columns+` FROM users WHERE email = ?`, email))
}

// ByPhone loads a user by E.164 phone.
func (Repository) ByPhone(ctx context.Context, ex database.Execer, phone string) (*User, error) {
	return scan(ex.QueryRowContext(ctx, `SELECT `+columns+` FROM users WHERE phone_e164 = ?`, phone))
}

// ByDestination loads a user by channel destination.
func (r Repository) ByDestination(ctx context.Context, ex database.Execer, channel, dest string) (*User, error) {
	if channel == "email" {
		return r.ByEmail(ctx, ex, dest)
	}
	return r.ByPhone(ctx, ex, dest)
}

// NewUser holds fields for creation.
type NewUser struct {
	Names         Names
	Email         *string
	PhoneE164     *string
	EmailVerified bool
	PhoneVerified bool
	Locale        string
}

// Create inserts a user and returns its IDs.
func (Repository) Create(ctx context.Context, ex database.Execer, n NewUser) (uint64, ids.UUID, error) {
	pub := ids.New()
	now := time.Now().UTC()
	var ev, pv any
	if n.EmailVerified {
		ev = now
	}
	if n.PhoneVerified {
		pv = now
	}
	locale := n.Locale
	if locale == "" {
		locale = "en"
	}
	res, err := ex.ExecContext(ctx, `INSERT INTO users
		(public_id, first_name, middle_name, last_name, email, phone_e164, email_verified_at, phone_verified_at, preferred_locale, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?)`,
		pub, n.Names.First, n.Names.Middle, n.Names.Last, n.Email, n.PhoneE164, ev, pv, locale, now, now)
	if err != nil {
		return 0, ids.Nil, err
	}
	id, err := res.LastInsertId()
	return uint64(id), pub, err
}

// MarkVerified sets the verified timestamp for a channel.
func (Repository) MarkVerified(ctx context.Context, ex database.Execer, userID uint64, channel string) error {
	col := "phone_verified_at"
	if channel == "email" {
		col = "email_verified_at"
	}
	_, err := ex.ExecContext(ctx, `UPDATE users SET `+col+` = COALESCE(`+col+`, ?) WHERE id = ?`, time.Now().UTC(), userID)
	return err
}

// TouchLogin records the last login time.
func (Repository) TouchLogin(ctx context.Context, ex database.Execer, userID uint64) error {
	_, err := ex.ExecContext(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, time.Now().UTC(), userID)
	return err
}

// UpdateProfile updates names and locale.
func (Repository) UpdateProfile(ctx context.Context, ex database.Execer, userID uint64, n Names, locale string) error {
	_, err := ex.ExecContext(ctx, `UPDATE users SET first_name = ?, middle_name = ?, last_name = ?, preferred_locale = ? WHERE id = ?`,
		n.First, n.Middle, n.Last, locale, userID)
	return err
}

// SetStatus changes account status.
func (Repository) SetStatus(ctx context.Context, ex database.Execer, userID uint64, status string) error {
	_, err := ex.ExecContext(ctx, `UPDATE users SET status = ? WHERE id = ?`, status, userID)
	return err
}

// AssignRole grants a role by key (idempotent).
func (Repository) AssignRole(ctx context.Context, ex database.Execer, userID uint64, roleKey string, by *uint64) error {
	_, err := ex.ExecContext(ctx, `INSERT IGNORE INTO user_roles (user_id, role_id, assigned_by)
		SELECT ?, id, ? FROM roles WHERE role_key = ?`, userID, by, roleKey)
	return err
}

// RoleKeys returns the user's role keys.
func (Repository) RoleKeys(ctx context.Context, ex database.Execer, userID uint64) ([]string, error) {
	rows, err := ex.QueryContext(ctx, `SELECT r.role_key FROM user_roles ur JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = ? ORDER BY r.role_key`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
