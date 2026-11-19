package database

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	"github.com/pressly/goose/v3"

	"github.com/alumasinde/profy/migrations"
	"github.com/alumasinde/profy/seeds"
)

func provider(db *sql.DB) (*goose.Provider, error) {
	return goose.NewProvider(goose.DialectMySQL, db, migrations.FS)
}

// MigrateUp applies all pending migrations.
func MigrateUp(ctx context.Context, db *sql.DB, log *slog.Logger) error {
	p, err := provider(db)
	if err != nil {
		return err
	}
	results, err := p.Up(ctx)
	for _, r := range results {
		log.Info("migration applied", "version", r.Source.Version, "file", r.Source.Path, "duration", r.Duration)
	}
	return err
}

// MigrateDown rolls back the most recent migration.
func MigrateDown(ctx context.Context, db *sql.DB, log *slog.Logger) error {
	p, err := provider(db)
	if err != nil {
		return err
	}
	r, err := p.Down(ctx)
	if r != nil {
		log.Info("migration rolled back", "version", r.Source.Version, "file", r.Source.Path)
	}
	return err
}

// MigrateReset rolls back every migration (tests and local only).
func MigrateReset(ctx context.Context, db *sql.DB) error {
	p, err := provider(db)
	if err != nil {
		return err
	}
	_, err = p.DownTo(ctx, 0)
	return err
}

// MigrationStatus returns a printable status per migration.
func MigrationStatus(ctx context.Context, db *sql.DB) ([]string, error) {
	p, err := provider(db)
	if err != nil {
		return nil, err
	}
	st, err := p.Status(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(st))
	for _, s := range st {
		out = append(out, fmt.Sprintf("%-8s %05d %s", s.State, s.Source.Version, s.Source.Path))
	}
	return out, nil
}

// MigrationsCurrent reports whether no migrations are pending (used by /readyz).
func MigrationsCurrent(ctx context.Context, db *sql.DB) (bool, error) {
	p, err := provider(db)
	if err != nil {
		return false, err
	}
	pending, err := p.HasPending(ctx)
	return !pending, err
}

// ApplySeeds runs every embedded seed file in name order. Seeds are idempotent.
func ApplySeeds(ctx context.Context, db *sql.DB, log *slog.Logger) error {
	names, err := fs.Glob(seeds.FS, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		b, err := seeds.FS.ReadFile(name)
		if err != nil {
			return err
		}
		stmts := splitStatements(string(b))
		err = WithTx(ctx, db, func(tx *sql.Tx) error {
			for _, s := range stmts {
				if _, err := tx.ExecContext(ctx, s); err != nil {
					return fmt.Errorf("seed %s: %w", name, err)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		log.Info("seed applied", "file", name, "statements", len(stmts))
	}
	return nil
}

// splitStatements splits a seed file on statement-terminating semicolons at line end.
// Seed files must not contain a semicolon at the end of a line inside a string literal.
func splitStatements(src string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") || (trimmed == "" && cur.Len() == 0) {
			continue
		}
		cur.WriteString(line)
		cur.WriteString("\n")
		if strings.HasSuffix(trimmed, ";") {
			s := strings.TrimSuffix(strings.TrimSpace(cur.String()), ";")
			if s != "" {
				out = append(out, s)
			}
			cur.Reset()
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}
