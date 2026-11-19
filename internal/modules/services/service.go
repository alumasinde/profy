// Package services owns the service catalogue and search synonyms (plan §8.1, §10.4).
package services

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alumasinde/profy/internal/authz"
	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/modules/audit"
	"github.com/alumasinde/profy/internal/modules/categories"
	"github.com/alumasinde/profy/internal/modules/seo"
	"github.com/alumasinde/profy/internal/shared/ids"
	"github.com/alumasinde/profy/internal/shared/lifecycle"
	"github.com/alumasinde/profy/internal/shared/paging"
	"github.com/alumasinde/profy/internal/shared/reqctx"
	"github.com/alumasinde/profy/internal/shared/text"
)

// MaxSynonyms per service.
const MaxSynonyms = 30

// Item is the admin view of a service.
type Item struct {
	ID              string     `json:"id"`
	CategoryID      string     `json:"category_id"`
	CategoryName    string     `json:"category_name"`
	Slug            string     `json:"slug"`
	Path            string     `json:"path"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	RequiresLicence bool       `json:"requires_licence"`
	SortOrder       int        `json:"sort_order"`
	Status          string     `json:"status"`
	MetaTitle       *string    `json:"meta_title"`
	MetaDescription *string    `json:"meta_description"`
	PublishedAt     *time.Time `json:"published_at"`
	Synonyms        []string   `json:"synonyms"`
	internalID      uint64
	categoryInt     uint64
}

// Service implements service administration.
type Service struct {
	db         *sql.DB
	authz      *authz.Service
	categories *categories.Service
}

// New builds the service.
func New(db *sql.DB, az *authz.Service, cats *categories.Service) *Service {
	return &Service{db: db, authz: az, categories: cats}
}

const cols = `s.id, s.public_id, c.public_id, c.id, c.name, s.slug, s.name, COALESCE(s.description, ''), s.requires_licence,
	s.sort_order, s.status, s.meta_title, s.meta_description, s.published_at`
const from = ` FROM services s JOIN categories c ON c.id = s.category_id`

func scan(row interface{ Scan(...any) error }) (*Item, error) {
	var it Item
	var pub, cpub ids.UUID
	var mt, md sql.NullString
	var pa sql.NullTime
	err := row.Scan(&it.internalID, &pub, &cpub, &it.categoryInt, &it.CategoryName, &it.Slug, &it.Name, &it.Description,
		&it.RequiresLicence, &it.SortOrder, &it.Status, &mt, &md, &pa)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, response.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	it.ID, it.CategoryID, it.Path = pub.String(), cpub.String(), seo.ServicePath(it.Slug)
	if mt.Valid {
		it.MetaTitle = &mt.String
	}
	if md.Valid {
		it.MetaDescription = &md.String
	}
	if pa.Valid {
		it.PublishedAt = &pa.Time
	}
	return &it, nil
}

// ListInput filters the list.
type ListInput struct {
	CategoryID, Status, Query, Cursor string
	Limit                             int
}

// List returns services ordered by name with keyset pagination on (name, id).
func (s *Service) List(ctx context.Context, in ListInput) ([]Item, string, error) {
	after, ok := paging.Decode(in.Cursor)
	if !ok {
		return nil, "", response.Validation(map[string]string{"cursor": "invalid"})
	}
	limit := paging.Limit(in.Limit, 50, 200)
	q, args := `SELECT `+cols+from+` WHERE 1=1`, []any{}
	if after > 0 {
		q += ` AND (s.name, s.id) > (SELECT name, id FROM services WHERE id = ?)`
		args = append(args, after)
	}
	if in.CategoryID != "" {
		ref, err := s.categories.Lookup(ctx, s.db, in.CategoryID)
		if err != nil {
			return nil, "", response.Validation(map[string]string{"category_id": "invalid"})
		}
		q += ` AND s.category_id = ?`
		args = append(args, ref.InternalID)
	}
	if in.Status != "" {
		q += ` AND s.status = ?`
		args = append(args, in.Status)
	}
	if t := strings.TrimSpace(in.Query); t != "" {
		q += ` AND (s.name LIKE ? OR s.slug LIKE ? OR EXISTS (SELECT 1 FROM service_synonyms y WHERE y.service_id = s.id AND y.synonym_normalized LIKE ?))`
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(t) + "%"
		args = append(args, like, like, "%"+text.NormalizeTerm(t)+"%")
	}
	q += ` ORDER BY s.name, s.id LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		it, err := scan(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, *it)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = paging.Encode(out[limit-1].internalID)
	}
	return out, next, nil
}

// Get loads a service with its synonyms.
func (s *Service) Get(ctx context.Context, ex database.Execer, publicID string, lock bool) (*Item, error) {
	pub, err := ids.Parse(publicID)
	if err != nil {
		return nil, response.ErrNotFound
	}
	q := `SELECT ` + cols + from + ` WHERE s.public_id = ?`
	if lock {
		q += ` FOR UPDATE OF s`
	}
	it, err := scan(ex.QueryRowContext(ctx, q, pub))
	if err != nil {
		return nil, err
	}
	it.Synonyms, err = s.synonyms(ctx, ex, it.internalID)
	return it, err
}

func (s *Service) synonyms(ctx context.Context, ex database.Execer, id uint64) ([]string, error) {
	rows, err := ex.QueryContext(ctx, `SELECT synonym FROM service_synonyms WHERE service_id = ? ORDER BY synonym`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// StatsForCategory implements categories.Stats.
func (s *Service) StatsForCategory(ctx context.Context, ex database.Execer, categoryID uint64) (int, int, error) {
	var active, published int
	err := ex.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(status = 'published'), 0) FROM services
		WHERE category_id = ? AND status <> 'archived'`, categoryID).Scan(&active, &published)
	return active, published, err
}

// CountsByCategory returns active services per category (for the category list).
func (s *Service) CountsByCategory(ctx context.Context) (map[uint64]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT category_id, COUNT(*) FROM services WHERE status <> 'archived' GROUP BY category_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uint64]int{}
	for rows.Next() {
		var id uint64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// Input is a create/update request; nil fields are unchanged on update.
type Input struct {
	CategoryID      *string `json:"category_id"`
	Slug            *string `json:"slug"`
	Name            *string `json:"name"`
	Description     *string `json:"description"`
	RequiresLicence *bool   `json:"requires_licence"`
	SortOrder       *int    `json:"sort_order"`
	Status          *string `json:"status"`
	MetaTitle       *string `json:"meta_title"`
	MetaDescription *string `json:"meta_description"`
}

func optional(s *string) any {
	if s == nil {
		return nil
	}
	if v := strings.TrimSpace(*s); v != "" {
		return v
	}
	return nil
}

func validateMeta(errs map[string]string, in Input) {
	if in.MetaTitle != nil {
		if v, ok := text.CleanLine(*in.MetaTitle, 160); !ok && v != "" {
			errs["meta_title"] = "invalid"
		}
	}
	if in.MetaDescription != nil {
		if v, ok := text.CleanLine(*in.MetaDescription, 320); !ok && v != "" {
			errs["meta_description"] = "invalid"
		}
	}
	if in.Description != nil {
		if _, ok := text.CleanText(*in.Description, 2000); !ok {
			errs["description"] = "too_long"
		}
	}
}

// Create adds a draft service (services.create).
func (s *Service) Create(ctx context.Context, in Input) (*Item, error) {
	errs := map[string]string{}
	name := ""
	if in.Name == nil {
		errs["name"] = "required"
	} else if n, ok := text.CleanLine(*in.Name, 120); !ok {
		errs["name"] = "required"
	} else {
		name = n
	}
	slug := text.Slugify(name)
	if in.Slug != nil && strings.TrimSpace(*in.Slug) != "" {
		slug = strings.TrimSpace(*in.Slug)
	}
	if name != "" && !text.ValidSlug(slug) {
		errs["slug"] = "invalid"
	}
	if in.Status != nil && *in.Status != lifecycle.Draft {
		errs["status"] = "invalid"
	}
	validateMeta(errs, in)
	var cat *categories.Ref
	if in.CategoryID == nil {
		errs["category_id"] = "required"
	} else if c, err := s.categories.Lookup(ctx, s.db, *in.CategoryID); err != nil || c.Status == lifecycle.Archived {
		errs["category_id"] = "invalid"
	} else {
		cat = c
	}
	if len(errs) > 0 {
		return nil, response.Validation(errs)
	}
	sort, licence, desc := 0, false, ""
	if in.SortOrder != nil {
		sort = *in.SortOrder
	}
	if in.RequiresLicence != nil {
		licence = *in.RequiresLicence
	}
	if in.Description != nil {
		desc, _ = text.CleanText(*in.Description, 2000)
	}
	pub := ids.New()
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO services (public_id, category_id, slug, name, description, requires_licence, sort_order,
			status, meta_title, meta_description) VALUES (?, ?, ?, ?, ?, ?, ?, 'draft', ?, ?)`,
			pub, cat.InternalID, slug, name, desc, licence, sort, optional(in.MetaTitle), optional(in.MetaDescription))
		if database.IsDuplicate(err) {
			return response.Validation(map[string]string{"slug": "taken"})
		}
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "services.created", EntityType: "service", EntityID: pub.String(),
			NewValues: map[string]any{"name": name, "slug": slug, "category": cat.Name}})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, s.db, pub.String(), false)
}

// Update applies a partial update. Publishing requires a published category; slug changes
// after first publication create a 301 redirect.
func (s *Service) Update(ctx context.Context, publicID string, in Input) (*Item, error) {
	p := reqctx.PrincipalFrom(ctx)
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		cur, err := s.Get(ctx, tx, publicID, true)
		if err != nil {
			return err
		}
		errs := map[string]string{}
		validateMeta(errs, in)
		name, slug, status, sort, licence, catID := cur.Name, cur.Slug, cur.Status, cur.SortOrder, cur.RequiresLicence, cur.categoryInt
		catStatus := ""
		content := false
		if in.Name != nil {
			n, ok := text.CleanLine(*in.Name, 120)
			if !ok {
				errs["name"] = "required"
			}
			content, name = content || n != cur.Name, n
		}
		if in.Slug != nil {
			slug = strings.TrimSpace(*in.Slug)
			if !text.ValidSlug(slug) {
				errs["slug"] = "invalid"
			}
			content = content || slug != cur.Slug
		}
		if in.CategoryID != nil {
			c, err := s.categories.Lookup(ctx, tx, *in.CategoryID)
			if err != nil || c.Status == lifecycle.Archived {
				errs["category_id"] = "invalid"
			} else {
				catID, catStatus, content = c.InternalID, c.Status, true
			}
		}
		if in.SortOrder != nil {
			sort, content = *in.SortOrder, true
		}
		if in.RequiresLicence != nil {
			licence, content = *in.RequiresLicence, true
		}
		if in.Description != nil || in.MetaTitle != nil || in.MetaDescription != nil {
			content = true
		}
		if in.Status != nil {
			if !lifecycle.Valid(*in.Status) {
				errs["status"] = "invalid"
			}
			status = *in.Status
		}
		if len(errs) > 0 {
			return response.Validation(errs)
		}
		if content && !s.authz.Can(ctx, p, "services.update") {
			return response.ErrForbidden
		}
		if status != cur.Status && !s.authz.Can(ctx, p, lifecycle.Permission("services", cur.Status, status)) {
			return response.ErrForbidden
		}
		if status == lifecycle.Published {
			if catStatus == "" {
				if catStatus, err = s.categories.StatusByID(ctx, tx, catID); err != nil {
					return err
				}
			}
			if catStatus != lifecycle.Published {
				return response.Conflict(response.CodeParentUnpublished, "Publish the service's category first.")
			}
		}
		desc := any(cur.Description)
		if in.Description != nil {
			desc, _ = text.CleanText(*in.Description, 2000)
		}
		mt, md := any(cur.MetaTitle), any(cur.MetaDescription)
		if in.MetaTitle != nil {
			mt = optional(in.MetaTitle)
		}
		if in.MetaDescription != nil {
			md = optional(in.MetaDescription)
		}
		var publishedAt any = cur.PublishedAt
		if status == lifecycle.Published && cur.PublishedAt == nil {
			publishedAt = time.Now().UTC()
		}
		if _, err := tx.ExecContext(ctx, `UPDATE services SET category_id = ?, slug = ?, name = ?, description = ?, requires_licence = ?,
			sort_order = ?, status = ?, meta_title = ?, meta_description = ?, published_at = ? WHERE id = ?`,
			catID, slug, name, desc, licence, sort, status, mt, md, publishedAt, cur.internalID); err != nil {
			if database.IsDuplicate(err) {
				return response.Validation(map[string]string{"slug": "taken"})
			}
			return err
		}
		if slug != cur.Slug && cur.PublishedAt != nil {
			if err := seo.AddRedirect(ctx, tx, seo.ServicePath(cur.Slug), seo.ServicePath(slug), "auto"); err != nil {
				return err
			}
		}
		old, nw := map[string]any{}, map[string]any{}
		for k, pair := range map[string][2]any{"name": {cur.Name, name}, "slug": {cur.Slug, slug}, "status": {cur.Status, status},
			"requires_licence": {cur.RequiresLicence, licence}, "sort_order": {cur.SortOrder, sort}} {
			if pair[0] != pair[1] {
				old[k], nw[k] = pair[0], pair[1]
			}
		}
		if catID != cur.categoryInt {
			old["category"], nw["category"] = cur.CategoryName, "changed"
		}
		if len(nw) == 0 && !content {
			return nil
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "services.updated", EntityType: "service", EntityID: cur.ID, OldValues: old, NewValues: nw})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, s.db, publicID, false)
}

// SetSynonyms replaces a service's search synonyms (services.update; audited).
func (s *Service) SetSynonyms(ctx context.Context, publicID string, list []string) (*Item, error) {
	if len(list) > MaxSynonyms {
		return nil, response.Validation(map[string]string{"synonyms": "too_many"})
	}
	type syn struct{ display, norm string }
	var clean []syn
	for i, raw := range list {
		v, ok := text.CleanLine(raw, 120)
		n := text.NormalizeTerm(v)
		if !ok || n == "" {
			return nil, response.Validation(map[string]string{"synonyms." + strconv.Itoa(i): "invalid"})
		}
		if !slices.ContainsFunc(clean, func(x syn) bool { return x.norm == n }) {
			clean = append(clean, syn{v, n})
		}
	}
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		cur, err := s.Get(ctx, tx, publicID, true)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM service_synonyms WHERE service_id = ?`, cur.internalID); err != nil {
			return err
		}
		names := []string{}
		for _, x := range clean {
			if _, err := tx.ExecContext(ctx, `INSERT INTO service_synonyms (service_id, synonym, synonym_normalized) VALUES (?, ?, ?)`,
				cur.internalID, x.display, x.norm); err != nil {
				return err
			}
			names = append(names, x.display)
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "services.synonyms.updated", EntityType: "service", EntityID: cur.ID,
			OldValues: map[string]any{"synonyms": cur.Synonyms}, NewValues: map[string]any{"synonyms": names}})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, s.db, publicID, false)
}

// ---- HTTP -----------------------------------------------------------------------------

// Handler exposes /api/v1/admin/services.
type Handler struct {
	svc   *Service
	authz *authz.Service
}

// NewHandler builds the handler.
func NewHandler(svc *Service, az *authz.Service) *Handler { return &Handler{svc: svc, authz: az} }

// Register mounts routes.
func (h *Handler) Register(mux *http.ServeMux, prefix string) {
	req := func(perm string, fn http.HandlerFunc) http.Handler { return h.authz.Require(perm)(fn) }
	mux.Handle("GET "+prefix+"/admin/services", req("services.view", h.list))
	mux.Handle("POST "+prefix+"/admin/services", req("services.create", h.create))
	mux.Handle("GET "+prefix+"/admin/services/{id}", req("services.view", h.get))
	mux.Handle("PATCH "+prefix+"/admin/services/{id}", req("services.view", h.update)) // field-level permissions in service
	mux.Handle("PUT "+prefix+"/admin/services/{id}/synonyms", req("services.update", h.synonyms))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, next, err := h.svc.List(r.Context(), ListInput{CategoryID: q.Get("category_id"), Status: q.Get("status"),
		Query: q.Get("q"), Cursor: q.Get("cursor"), Limit: limit})
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	var nc any
	if next != "" {
		nc = next
	}
	response.JSON(w, http.StatusOK, list, map[string]any{"next_cursor": nc})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	it, err := h.svc.Get(r.Context(), h.svc.db, r.PathValue("id"), false)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, it)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in Input
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	it, err := h.svc.Create(r.Context(), in)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, it, nil)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var in Input
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	it, err := h.svc.Update(r.Context(), r.PathValue("id"), in)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, it)
}

func (h *Handler) synonyms(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Synonyms []string `json:"synonyms"`
	}
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	it, err := h.svc.SetSynonyms(r.Context(), r.PathValue("id"), in.Synonyms)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, it)
}
