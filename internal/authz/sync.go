package authz

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
)

// SyncCatalogue upserts catalogue permissions and system roles. It is idempotent:
//   - every catalogue key exists in `permissions`;
//   - every system role exists; default grants are applied only when the role is created;
//   - roles with AllPerms always hold every catalogue permission.
//
// Keys removed from code are left in the table (harmless; ignored by Service) so that
// historical assignments are not silently destroyed.
func SyncCatalogue(ctx context.Context, tx *sql.Tx, log *slog.Logger) error {
	for _, p := range Catalogue {
		if _, err := tx.ExecContext(ctx, `INSERT INTO permissions (permission_key, module, description)
			VALUES (?, ?, ?) AS new ON DUPLICATE KEY UPDATE module = new.module, description = new.description`,
			p.Key, p.Module, p.Description); err != nil {
			return fmt.Errorf("sync permission %s: %w", p.Key, err)
		}
	}
	for _, r := range SystemRoles {
		res, err := tx.ExecContext(ctx, `INSERT IGNORE INTO roles (role_key, name, description, is_system, is_protected)
			VALUES (?, ?, ?, 1, ?)`, r.Key, r.Name, r.Description, r.Protected)
		if err != nil {
			return fmt.Errorf("sync role %s: %w", r.Key, err)
		}
		created, _ := res.RowsAffected()
		switch {
		case r.AllPerms:
			if _, err := tx.ExecContext(ctx, `INSERT IGNORE INTO role_permissions (role_id, permission_id)
				SELECT r.id, p.id FROM roles r CROSS JOIN permissions p WHERE r.role_key = ?`, r.Key); err != nil {
				return err
			}
		case created == 1:
			for _, key := range r.Permissions {
				if !known[key] {
					return fmt.Errorf("role %s grants unknown permission %s", r.Key, key)
				}
				if _, err := tx.ExecContext(ctx, `INSERT IGNORE INTO role_permissions (role_id, permission_id)
					SELECT r.id, p.id FROM roles r JOIN permissions p ON p.permission_key = ? WHERE r.role_key = ?`,
					key, r.Key); err != nil {
					return err
				}
			}
			log.Info("role created with default grants", "role", r.Key, "permissions", len(r.Permissions))
		}
	}
	return nil
}
