package locations

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

// Ref is a compact location reference used by other modules.
type Ref struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	Slug       string `json:"slug"`
	Label      string `json:"label"` // "Ruaka, Kiambu"
	Status     string `json:"-"`
	Selectable bool   `json:"-"`
	InternalID uint64 `json:"-"`
	CountyID   uint64 `json:"-"`
	TownID     uint64 `json:"-"`
}

// refSQL selects a location with its ancestors' names, selectability and status.
func refSQL(typ string) string {
	switch typ {
	case County:
		return `SELECT x.id, x.public_id, x.name, x.slug, x.status, x.is_selectable, x.name, x.id, 0 FROM counties x`
	case Town:
		return `SELECT x.id, x.public_id, x.name, x.slug, x.status, x.is_selectable AND c.is_selectable, CONCAT(x.name, ', ', c.name), c.id, x.id
			FROM towns x JOIN counties c ON c.id = x.county_id`
	default:
		return `SELECT x.id, x.public_id, x.name, x.slug, x.status, x.is_selectable AND t.is_selectable AND c.is_selectable,
				CONCAT(x.name, ', ', t.name, ', ', c.name), c.id, t.id
			FROM areas x JOIN towns t ON t.id = x.town_id JOIN counties c ON c.id = t.county_id`
	}
}

func scanRef(typ string, row interface{ Scan(...any) error }) (*Ref, error) {
	r := Ref{Type: typ}
	var pub ids.UUID
	err := row.Scan(&r.InternalID, &pub, &r.Name, &r.Slug, &r.Status, &r.Selectable, &r.Label, &r.CountyID, &r.TownID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, response.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.ID = pub.String()
	return &r, nil
}

// Resolve looks up a location by type and public ID (any status; callers decide).
func (s *Service) Resolve(ctx context.Context, ex database.Execer, typ, publicID string) (*Ref, error) {
	if _, ok := kinds[typ]; !ok {
		return nil, response.ErrNotFound
	}
	pub, err := ids.Parse(publicID)
	if err != nil {
		return nil, response.ErrNotFound
	}
	return scanRef(typ, ex.QueryRowContext(ctx, refSQL(typ)+` WHERE x.public_id = ?`, pub))
}

// ByInternal returns references for internal IDs of one type.
func (s *Service) ByInternal(ctx context.Context, ex database.Execer, typ string, idList []uint64) (map[uint64]*Ref, error) {
	out := map[uint64]*Ref{}
	if len(idList) == 0 {
		return out, nil
	}
	if _, ok := kinds[typ]; !ok {
		return nil, response.ErrNotFound
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(idList)), ",")
	args := make([]any, len(idList))
	for i, v := range idList {
		args[i] = v
	}
	rows, err := ex.QueryContext(ctx, refSQL(typ)+` WHERE x.id IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanRef(typ, rows)
		if err != nil {
			return nil, err
		}
		out[r.InternalID] = r
	}
	return out, rows.Err()
}

// MergeHook re-points other modules' references when a town or area is merged (called
// inside the merge transaction).
type MergeHook func(ctx context.Context, tx *sql.Tx, typ string, sourceID, targetID uint64) error

// OnMerge registers a hook.
func (s *Service) OnMerge(h MergeHook) { s.mergeHooks = append(s.mergeHooks, h) }

// ---- Public discovery API (§15.3) -----------------------------------------------------

// PublicHandler serves /api/v1/locations for pickers. Only active, selectable locations
// (whose ancestors are also selectable) are listed.
type PublicHandler struct{ svc *Service }

// NewPublicHandler builds the handler.
func NewPublicHandler(svc *Service) *PublicHandler { return &PublicHandler{svc: svc} }

// Register mounts routes.
func (h *PublicHandler) Register(mux *http.ServeMux, prefix string) {
	mux.HandleFunc("GET "+prefix+"/locations/counties", h.counties)
	mux.HandleFunc("GET "+prefix+"/locations/towns", h.towns)
	mux.HandleFunc("GET "+prefix+"/locations/areas", h.areas)
	mux.HandleFunc("GET "+prefix+"/locations/search", h.search)
}

func (h *PublicHandler) list(w http.ResponseWriter, r *http.Request, typ, where string, args ...any) {
	rows, err := h.svc.db.QueryContext(r.Context(), refSQL(typ)+` WHERE x.status = 'active' AND `+where+` ORDER BY x.name LIMIT 1000`, args...)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	defer rows.Close()
	out := []Ref{}
	for rows.Next() {
		ref, err := scanRef(typ, rows)
		if err != nil {
			response.Fail(w, r, err)
			return
		}
		if ref.Selectable {
			out = append(out, *ref)
		}
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	response.OK(w, out)
}

func (h *PublicHandler) counties(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, County, "x.is_selectable = 1")
}

func (h *PublicHandler) towns(w http.ResponseWriter, r *http.Request) {
	pub, err := ids.Parse(r.URL.Query().Get("county"))
	if err != nil {
		response.Fail(w, r, response.Validation(map[string]string{"county": "required"}))
		return
	}
	h.list(w, r, Town, "c.public_id = ?", pub)
}

func (h *PublicHandler) areas(w http.ResponseWriter, r *http.Request) {
	pub, err := ids.Parse(r.URL.Query().Get("town"))
	if err != nil {
		response.Fail(w, r, response.Validation(map[string]string{"town": "required"}))
		return
	}
	h.list(w, r, Area, "t.public_id = ?", pub)
}

// search is alias-aware autocomplete across counties, towns and areas.
func (h *PublicHandler) search(w http.ResponseWriter, r *http.Request) {
	q := text.NormalizeTerm(r.URL.Query().Get("q"))
	if len([]rune(q)) < 2 {
		response.OK(w, []Ref{})
		return
	}
	like := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
	out := []Ref{}
	seen := map[string]bool{}
	for _, typ := range []string{County, Town, Area} {
		rows, err := h.svc.db.QueryContext(r.Context(), refSQL(typ)+` WHERE x.status = 'active' AND (x.name LIKE ? OR x.id IN
			(SELECT location_id FROM location_aliases WHERE location_type = ? AND alias_normalized LIKE ?)) ORDER BY x.name LIMIT 20`,
			like, typ, like)
		if err != nil {
			response.Fail(w, r, err)
			return
		}
		for rows.Next() {
			ref, err := scanRef(typ, rows)
			if err != nil {
				rows.Close()
				response.Fail(w, r, err)
				return
			}
			if ref.Selectable && !seen[ref.ID] {
				seen[ref.ID] = true
				out = append(out, *ref)
			}
		}
		rows.Close()
	}
	if len(out) > 20 {
		out = out[:20]
	}
	response.OK(w, out)
}
