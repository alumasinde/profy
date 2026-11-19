package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/alumasinde/profy/internal/authz"
	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/modules/audit"
	"github.com/alumasinde/profy/internal/modules/users"
	"github.com/alumasinde/profy/internal/security"
)

// AdminInput describes a super administrator created from the CLI.
type AdminInput struct {
	FirstName, MiddleName, LastName string
	Email, Phone                    string
}

// Seed applies reference-data seeds and synchronizes the permission catalogue and roles.
func Seed(ctx context.Context, db *sql.DB, log *slog.Logger) error {
	if err := database.ApplySeeds(ctx, db, log); err != nil {
		return err
	}
	return database.WithTx(ctx, db, func(tx *sql.Tx) error { return authz.SyncCatalogue(ctx, tx, log) })
}

// CreateSuperAdmin creates (or promotes) a super administrator (§5.1). There is no default
// password: the person signs in with an OTP sent to the given email or phone, which is
// marked verified because an operator with server access vouches for it.
func CreateSuperAdmin(ctx context.Context, db *sql.DB, in AdminInput) (string, bool, error) {
	var middle *string
	if in.MiddleName != "" {
		middle = &in.MiddleName
	}
	names, errs := users.NormalizeNames(in.FirstName, middle, in.LastName)
	if len(errs) > 0 {
		return "", false, fmt.Errorf("invalid names: %v", errs)
	}
	var email, phone *string
	if in.Email != "" {
		e, ok := security.NormalizeEmail(in.Email)
		if !ok {
			return "", false, errors.New("invalid email")
		}
		email = &e
	}
	if in.Phone != "" {
		p, ok := security.NormalizeKenyanPhone(in.Phone)
		if !ok {
			return "", false, errors.New("invalid Kenyan mobile number")
		}
		phone = &p
	}
	if email == nil && phone == nil {
		return "", false, errors.New("an email or phone is required to sign in")
	}
	var repo users.Repository
	var publicID string
	created := false
	err := database.WithTx(ctx, db, func(tx *sql.Tx) error {
		var u *users.User
		var err error
		if email != nil {
			u, err = repo.ByEmail(ctx, tx, *email)
		}
		if u == nil && phone != nil {
			u, err = repo.ByPhone(ctx, tx, *phone)
		}
		if err != nil && !errors.Is(err, users.ErrNotFound) {
			return err
		}
		var id uint64
		if u == nil {
			newID, pub, err := repo.Create(ctx, tx, users.NewUser{Names: names, Email: email, PhoneE164: phone,
				EmailVerified: email != nil, PhoneVerified: phone != nil})
			if err != nil {
				return err
			}
			id, publicID, created = newID, pub.String(), true
			if err := repo.AssignRole(ctx, tx, id, authz.RoleCustomer, nil); err != nil {
				return err
			}
		} else {
			id, publicID = u.ID, u.PublicID.String()
			if u.Status != users.StatusActive {
				return fmt.Errorf("existing account is %s", u.Status)
			}
		}
		var roleExists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM roles WHERE role_key = ?`, authz.RoleSuperAdmin).Scan(&roleExists); err != nil {
			return err
		}
		if roleExists == 0 {
			return errors.New("roles are not seeded: run `server seed` first")
		}
		if err := repo.AssignRole(ctx, tx, id, authz.RoleSuperAdmin, nil); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "users.super_admin.bootstrap", EntityType: "user", EntityID: publicID,
			NewValues: map[string]any{"via": "cli", "created": created}})
	})
	return publicID, created, err
}
