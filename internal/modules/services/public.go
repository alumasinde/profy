package services

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/shared/ids"
	"github.com/alumasinde/profy/internal/shared/text"
)

// Ref is a compact service reference used by other modules.
type Ref struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Slug            string `json:"slug"`
	CategoryName    string `json:"category_name"`
	RequiresLicence bool   `json:"requires_licence"`
	Status          string `json:"-"`
	CategoryStatus  string `json:"-"`
	InternalID      uint64 `json:"-"`
}

// Live reports whether the service and its category are published.
func (r *Ref) Live() bool { return r.Status == "published" && r.CategoryStatus == "published" }

const refCols = `s.id, s.public_id, s.name, s.slug, c.name, s.requires_licence, s.status, c.status FROM services s JOIN categories c ON c.id = s.category_id`

func scanRef(row interface{ Scan(...any) error }) (*Ref, error) {
	var r Ref
	var pub ids.UUID
	err := row.Scan(&r.InternalID, &pub, &r.Name, &r.Slug, &r.CategoryName, &r.RequiresLicence, &r.Status, &r.CategoryStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, response.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.ID = pub.String()
	return &r, nil
}

// Resolve looks up a service by public ID.
func (s *Service) Resolve(ctx context.Context, ex database.Execer, publicID string) (*Ref, error) {
	pub, err := ids.Parse(publicID)
	if err != nil {
		return nil, response.ErrNotFound
	}
	return scanRef(ex.QueryRowContext(ctx, `SELECT `+refCols+` WHERE s.public_id = ?`, pub))
}

// ByInternal returns references for internal IDs.
func (s *Service) ByInternal(ctx context.Context, ex database.Execer, idList []uint64) (map[uint64]*Ref, error) {
	out := map[uint64]*Ref{}
	if len(idList) == 0 {
		return out, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(idList)), ",")
	args := make([]any, len(idList))
	for i, v := range idList {
		args[i] = v
	}
	rows, err := ex.QueryContext(ctx, `SELECT `+refCols+` WHERE s.id IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanRef(rows)
		if err != nil {
			return nil, err
		}
		out[r.InternalID] = r
	}
	return out, rows.Err()
}

// PublicHandler serves GET /api/v1/services: published services in published categories,
// optionally filtered by name or synonym (§15.3).
type PublicHandler struct{ svc *Service }

// NewPublicHandler builds the handler.
func NewPublicHandler(svc *Service) *PublicHandler { return &PublicHandler{svc: svc} }

// Register mounts routes.
func (h *PublicHandler) Register(mux *http.ServeMux, prefix string) {
	mux.HandleFunc("GET "+prefix+"/services", h.list)
}

func (h *PublicHandler) list(w http.ResponseWriter, r *http.Request) {
	q := `SELECT ` + refCols + ` WHERE s.status = 'published' AND c.status = 'published'`
	args := []any{}
	if t := text.NormalizeTerm(r.URL.Query().Get("q")); t != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(t) + "%"
		q += ` AND (s.name LIKE ? OR c.name LIKE ? OR EXISTS (SELECT 1 FROM service_synonyms y WHERE y.service_id = s.id AND y.synonym_normalized LIKE ?))`
		args = append(args, like, like, like)
	}
	rows, err := h.svc.db.QueryContext(r.Context(), q+` ORDER BY c.name, s.sort_order, s.name LIMIT 500`, args...)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	defer rows.Close()
	out := []Ref{}
	for rows.Next() {
		ref, err := scanRef(rows)
		if err != nil {
			response.Fail(w, r, err)
			return
		}
		out = append(out, *ref)
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	response.OK(w, out)
}
