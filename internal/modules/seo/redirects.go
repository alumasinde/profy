// Package seo owns slug redirects (plan §8.3, §9.4). Sitemaps, metadata defaults and indexing
// rules join it in Phase 5.
package seo

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alumasinde/profy/internal/authz"
	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/modules/audit"
	"github.com/alumasinde/profy/internal/shared/paging"
	"github.com/alumasinde/profy/internal/shared/reqctx"
)

// Public URL builders. Every module uses these so a URL scheme lives in one place (§9.1).
func PagePath(slug string) string     { return "/" + slug }
func CategoryPath(slug string) string { return "/categories/" + slug }
func ServicePath(slug string) string  { return "/services/" + slug }
func LocationPath(slug string) string { return "/locations/" + slug }
func ProviderPath(slug string) string { return "/professionals/" + slug }

// validPath accepts a clean, lowercase, absolute site path.
func validPath(p string) bool {
	if len(p) < 2 || len(p) > 300 || p[0] != '/' || strings.HasPrefix(p, "//") || strings.HasSuffix(p, "/") {
		return false
	}
	for _, r := range p {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '/' || r == '_' || r == '.') {
			return false
		}
	}
	return !strings.Contains(p, "..") && !strings.Contains(p, "//")
}

// AddRedirect records oldPath → newPath inside tx, collapsing chains (anything that pointed
// at oldPath now points at newPath) and removing a redirect that would shadow newPath
// (when a slug is changed back). It is a no-op when the paths are equal.
func AddRedirect(ctx context.Context, tx *sql.Tx, oldPath, newPath, source string) error {
	if oldPath == newPath {
		return nil
	}
	if !validPath(oldPath) || !validPath(newPath) {
		return errors.New("seo: invalid redirect path")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM slug_redirects WHERE old_path = ?`, newPath); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE slug_redirects SET new_path = ? WHERE new_path = ?`, newPath, oldPath); err != nil {
		return err
	}
	var by any
	if p := reqctx.PrincipalFrom(ctx); p != nil {
		by = p.UserID
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO slug_redirects (old_path, new_path, status_code, source, created_by)
		VALUES (?, ?, 301, ?, ?) AS new ON DUPLICATE KEY UPDATE new_path = new.new_path, source = new.source`,
		oldPath, newPath, source, by)
	return err
}

// Service looks up and administers redirects.
type Service struct{ db *sql.DB }

// New builds the service.
func New(db *sql.DB) *Service { return &Service{db: db} }

// Lookup returns the redirect target for a path, or "" when none exists.
func (s *Service) Lookup(ctx context.Context, path string) (string, int) {
	var to string
	var code int
	err := s.db.QueryRowContext(ctx, `SELECT new_path, status_code FROM slug_redirects WHERE old_path = ?`, path).Scan(&to, &code)
	if err != nil {
		return "", 0
	}
	return to, code
}

// Redirect is one entry for administration.
type Redirect struct {
	ID        string    `json:"id"`
	OldPath   string    `json:"old_path"`
	NewPath   string    `json:"new_path"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
}

// Handler exposes /api/v1/admin/redirects (seo.manage_redirects).
type Handler struct {
	svc   *Service
	authz *authz.Service
}

// NewHandler builds the handler.
func NewHandler(svc *Service, az *authz.Service) *Handler { return &Handler{svc: svc, authz: az} }

// Register mounts routes.
func (h *Handler) Register(mux *http.ServeMux, prefix string) {
	g := h.authz.Require("seo.manage_redirects")
	mux.Handle("GET "+prefix+"/admin/redirects", g(http.HandlerFunc(h.list)))
	mux.Handle("POST "+prefix+"/admin/redirects", g(http.HandlerFunc(h.create)))
	mux.Handle("DELETE "+prefix+"/admin/redirects/{id}", g(http.HandlerFunc(h.delete)))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	after, ok := paging.Decode(r.URL.Query().Get("cursor"))
	if !ok {
		response.Fail(w, r, response.Validation(map[string]string{"cursor": "invalid"}))
		return
	}
	limit := paging.Limit(atoi(r.URL.Query().Get("limit")), 50, 200)
	q := `SELECT id, old_path, new_path, source, created_at FROM slug_redirects WHERE 1=1`
	args := []any{}
	if after > 0 {
		q += ` AND id < ?`
		args = append(args, after)
	}
	if t := strings.TrimSpace(r.URL.Query().Get("q")); t != "" {
		q += ` AND (old_path LIKE ? OR new_path LIKE ?)`
		args = append(args, "%"+t+"%", "%"+t+"%")
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := h.svc.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	defer rows.Close()
	out := []Redirect{}
	var idList []uint64
	for rows.Next() {
		var x Redirect
		var id uint64
		if err := rows.Scan(&id, &x.OldPath, &x.NewPath, &x.Source, &x.CreatedAt); err != nil {
			response.Fail(w, r, err)
			return
		}
		x.ID = strconv.FormatUint(id, 10)
		out = append(out, x)
		idList = append(idList, id)
	}
	if err := rows.Err(); err != nil {
		response.Fail(w, r, err)
		return
	}
	var next any
	if len(out) > limit {
		out = out[:limit]
		next = paging.Encode(idList[limit-1])
	}
	response.JSON(w, http.StatusOK, out, map[string]any{"next_cursor": next})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OldPath string `json:"old_path"`
		NewPath string `json:"new_path"`
	}
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	errs := map[string]string{}
	if !validPath(in.OldPath) {
		errs["old_path"] = "invalid"
	}
	if !validPath(in.NewPath) {
		errs["new_path"] = "invalid"
	}
	if len(errs) == 0 && in.OldPath == in.NewPath {
		errs["new_path"] = "invalid"
	}
	if len(errs) > 0 {
		response.Fail(w, r, response.Validation(errs))
		return
	}
	err := database.WithTx(r.Context(), h.svc.db, func(tx *sql.Tx) error {
		if err := AddRedirect(r.Context(), tx, in.OldPath, in.NewPath, "manual"); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, audit.Entry{Action: "seo.redirect.created", EntityType: "redirect",
			EntityID: in.OldPath, NewValues: map[string]any{"new_path": in.NewPath}})
	})
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, map[string]string{"old_path": in.OldPath, "new_path": in.NewPath}, nil)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		response.Fail(w, r, response.ErrNotFound)
		return
	}
	err = database.WithTx(r.Context(), h.svc.db, func(tx *sql.Tx) error {
		var oldP, newP string
		if err := tx.QueryRowContext(r.Context(), `SELECT old_path, new_path FROM slug_redirects WHERE id = ? FOR UPDATE`, id).Scan(&oldP, &newP); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return response.ErrNotFound
			}
			return err
		}
		if _, err := tx.ExecContext(r.Context(), `DELETE FROM slug_redirects WHERE id = ?`, id); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, audit.Entry{Action: "seo.redirect.deleted", EntityType: "redirect",
			EntityID: oldP, OldValues: map[string]any{"new_path": newP}})
	})
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.NoContent(w)
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }
