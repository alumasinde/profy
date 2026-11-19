// Package categories owns the two-level service category tree (plan §2.1, §8.1).
package categories

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/alumasinde/profy/internal/authz"
	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/modules/audit"
	"github.com/alumasinde/profy/internal/modules/seo"
	"github.com/alumasinde/profy/internal/shared/ids"
	"github.com/alumasinde/profy/internal/shared/lifecycle"
	"github.com/alumasinde/profy/internal/shared/reqctx"
	"github.com/alumasinde/profy/internal/shared/text"
)

// Category is the admin view of a category.
type Category struct {
	ID              string     `json:"id"`
	ParentID        *string    `json:"parent_id"`
	Slug            string     `json:"slug"`
	Path            string     `json:"path"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	SortOrder       int        `json:"sort_order"`
	Status          string     `json:"status"`
	MetaTitle       *string    `json:"meta_title"`
	MetaDescription *string    `json:"meta_description"`
	PublishedAt     *time.Time `json:"published_at"`
	ServiceCount    int        `json:"service_count"`
	internalID      uint64
	parentInternal  *uint64
}

// Stats reports services in a category: total non-archived and published.
type Stats func(ctx context.Context, ex database.Execer, categoryID uint64) (active, published int, err error)

// Service implements category administration.
type Service struct {
	db    *sql.DB
	authz *authz.Service
	// ServiceStats is injected by the app (services module) to enforce lifecycle rules.
	ServiceStats Stats
	// ServiceCounts is injected by the app: active (non-archived) services per category.
	ServiceCounts func(ctx context.Context) (map[uint64]int, error)
}

// New builds the service.
func New(db *sql.DB, az *authz.Service) *Service { return &Service{db: db, authz: az} }

const cols = `c.id, c.public_id, p.public_id, c.parent_id, c.slug, c.name, COALESCE(c.description, ''), c.sort_order, c.status,
	c.meta_title, c.meta_description, c.published_at`

const from = ` FROM categories c LEFT JOIN categories p ON p.id = c.parent_id`

func scan(row interface{ Scan(...any) error }) (*Category, error) {
	var c Category
	var pub ids.UUID
	var parentPub []byte
	var parentID sql.NullInt64
	var mt, md sql.NullString
	var pa sql.NullTime
	err := row.Scan(&c.internalID, &pub, &parentPub, &parentID, &c.Slug, &c.Name, &c.Description, &c.SortOrder, &c.Status,
		&mt, &md, &pa)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, response.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.ID, c.Path = pub.String(), seo.CategoryPath(c.Slug)
	if len(parentPub) == 16 {
		var u ids.UUID
		copy(u[:], parentPub)
		s := u.String()
		c.ParentID = &s
		pid := uint64(parentID.Int64)
		c.parentInternal = &pid
	}
	if mt.Valid {
		c.MetaTitle = &mt.String
	}
	if md.Valid {
		c.MetaDescription = &md.String
	}
	if pa.Valid {
		c.PublishedAt = &pa.Time
	}
	return &c, nil
}

// List returns all categories ordered for a tree (parents first, then sort order).
func (s *Service) List(ctx context.Context, status string) ([]Category, error) {
	q, args := `SELECT `+cols+from, []any{}
	if status != "" {
		q += ` WHERE c.status = ?`
		args = append(args, status)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY COALESCE(p.sort_order, c.sort_order), COALESCE(p.name, c.name), c.parent_id IS NOT NULL, c.sort_order, c.name LIMIT 1000`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Category{}
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if s.ServiceCounts != nil {
		counts, err := s.ServiceCounts(ctx)
		if err != nil {
			return nil, err
		}
		for i := range out {
			out[i].ServiceCount = counts[out[i].internalID]
		}
	}
	return out, nil
}

// Get loads one category by public ID.
func (s *Service) Get(ctx context.Context, ex database.Execer, publicID string, lock bool) (*Category, error) {
	pub, err := ids.Parse(publicID)
	if err != nil {
		return nil, response.ErrNotFound
	}
	q := `SELECT ` + cols + from + ` WHERE c.public_id = ?`
	if lock {
		q += ` FOR UPDATE OF c`
	}
	return scan(ex.QueryRowContext(ctx, q, pub))
}

// Ref is the minimal category reference used by the services module.
type Ref struct {
	InternalID uint64
	Name       string
	Status     string
}

// Lookup resolves a category public ID for other modules.
func (s *Service) Lookup(ctx context.Context, ex database.Execer, publicID string) (*Ref, error) {
	c, err := s.Get(ctx, ex, publicID, false)
	if err != nil {
		return nil, err
	}
	return &Ref{InternalID: c.internalID, Name: c.Name, Status: c.Status}, nil
}

// StatusByID returns a category's status by internal ID (used by the services module).
func (s *Service) StatusByID(ctx context.Context, ex database.Execer, id uint64) (string, error) {
	var st string
	err := ex.QueryRowContext(ctx, `SELECT status FROM categories WHERE id = ?`, id).Scan(&st)
	return st, err
}

// Input is a create/update request; nil fields are unchanged on update.
type Input struct {
	ParentID        *string `json:"parent_id"` // "" clears the parent
	Slug            *string `json:"slug"`
	Name            *string `json:"name"`
	Description     *string `json:"description"`
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

// resolveParent validates a parent: it must exist, be top-level and not archived.
func (s *Service) resolveParent(ctx context.Context, ex database.Execer, publicID string, self uint64) (*uint64, string) {
	if publicID == "" {
		return nil, ""
	}
	p, err := s.Get(ctx, ex, publicID, false)
	if err != nil {
		return nil, "invalid"
	}
	if p.internalID == self || p.ParentID != nil || p.Status == lifecycle.Archived {
		return nil, "invalid"
	}
	id := p.internalID
	return &id, ""
}

// Create adds a draft category (categories.create).
func (s *Service) Create(ctx context.Context, in Input) (*Category, error) {
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
	var parent *uint64
	if in.ParentID != nil {
		var code string
		if parent, code = s.resolveParent(ctx, s.db, *in.ParentID, 0); code != "" {
			errs["parent_id"] = code
		}
	}
	if len(errs) > 0 {
		return nil, response.Validation(errs)
	}
	sort := 0
	if in.SortOrder != nil {
		sort = *in.SortOrder
	}
	desc := ""
	if in.Description != nil {
		desc, _ = text.CleanText(*in.Description, 2000)
	}
	pub := ids.New()
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO categories (public_id, parent_id, slug, name, description, sort_order, status, meta_title, meta_description)
			VALUES (?, ?, ?, ?, ?, ?, 'draft', ?, ?)`, pub, parent, slug, name, desc, sort, optional(in.MetaTitle), optional(in.MetaDescription))
		if database.IsDuplicate(err) {
			return response.Validation(map[string]string{"slug": "taken"})
		}
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "categories.created", EntityType: "category", EntityID: pub.String(),
			NewValues: map[string]any{"name": name, "slug": slug}})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, s.db, pub.String(), false)
}

// Update applies a partial update with lifecycle rules:
//   - publishing a subcategory requires a published parent;
//   - unpublishing or archiving requires no published (resp. active) subcategories or services;
//   - a category with subcategories cannot itself become a subcategory;
//   - slug changes after first publication create a 301 redirect.
func (s *Service) Update(ctx context.Context, publicID string, in Input) (*Category, error) {
	p := reqctx.PrincipalFrom(ctx)
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		cur, err := s.Get(ctx, tx, publicID, true)
		if err != nil {
			return err
		}
		errs := map[string]string{}
		validateMeta(errs, in)
		name, slug, status, sort := cur.Name, cur.Slug, cur.Status, cur.SortOrder
		parent := cur.parentInternal
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
		if in.ParentID != nil {
			var code string
			if parent, code = s.resolveParent(ctx, tx, *in.ParentID, cur.internalID); code != "" {
				errs["parent_id"] = code
			}
			content = true
		}
		if in.SortOrder != nil {
			sort, content = *in.SortOrder, true
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
		if content && !s.authz.Can(ctx, p, "categories.update") {
			return response.ErrForbidden
		}
		if status != cur.Status && !s.authz.Can(ctx, p, lifecycle.Permission("categories", cur.Status, status)) {
			return response.ErrForbidden
		}
		var children, liveChildren int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(status = 'published'), 0) FROM categories
			WHERE parent_id = ? AND status <> 'archived'`, cur.internalID).Scan(&children, &liveChildren); err != nil {
			return err
		}
		if parent != nil && children > 0 {
			return response.Validation(map[string]string{"parent_id": "has_children"})
		}
		if status == lifecycle.Published && parent != nil {
			var ps string
			if err := tx.QueryRowContext(ctx, `SELECT status FROM categories WHERE id = ?`, *parent).Scan(&ps); err != nil {
				return err
			}
			if ps != lifecycle.Published {
				return response.Conflict(response.CodeParentUnpublished, "Publish the parent category first.")
			}
		}
		if status != cur.Status && status != lifecycle.Published && s.ServiceStats != nil {
			active, published, err := s.ServiceStats(ctx, tx, cur.internalID)
			if err != nil {
				return err
			}
			if liveChildren > 0 || published > 0 {
				return response.Conflict(response.CodeHasDependents, "Unpublish this category's subcategories and services first.")
			}
			if status == lifecycle.Archived && (children > 0 || active > 0) {
				return response.Conflict(response.CodeHasDependents, "Archive this category's subcategories and services first.")
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
		if _, err := tx.ExecContext(ctx, `UPDATE categories SET parent_id = ?, slug = ?, name = ?, description = ?, sort_order = ?,
			status = ?, meta_title = ?, meta_description = ?, published_at = ? WHERE id = ?`,
			parent, slug, name, desc, sort, status, mt, md, publishedAt, cur.internalID); err != nil {
			if database.IsDuplicate(err) {
				return response.Validation(map[string]string{"slug": "taken"})
			}
			return err
		}
		if slug != cur.Slug && cur.PublishedAt != nil {
			if err := seo.AddRedirect(ctx, tx, seo.CategoryPath(cur.Slug), seo.CategoryPath(slug), "auto"); err != nil {
				return err
			}
		}
		old, nw := map[string]any{}, map[string]any{}
		for k, pair := range map[string][2]any{"name": {cur.Name, name}, "slug": {cur.Slug, slug}, "status": {cur.Status, status}, "sort_order": {cur.SortOrder, sort}} {
			if pair[0] != pair[1] {
				old[k], nw[k] = pair[0], pair[1]
			}
		}
		if !ptrEq(cur.parentInternal, parent) {
			old["parent_changed"], nw["parent_changed"] = true, true
		}
		if len(nw) == 0 && !content {
			return nil
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "categories.updated", EntityType: "category", EntityID: cur.ID, OldValues: old, NewValues: nw})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, s.db, publicID, false)
}

func ptrEq(a, b *uint64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// ---- HTTP -----------------------------------------------------------------------------

// Handler exposes /api/v1/admin/categories.
type Handler struct {
	svc   *Service
	authz *authz.Service
}

// NewHandler builds the handler.
func NewHandler(svc *Service, az *authz.Service) *Handler { return &Handler{svc: svc, authz: az} }

// Register mounts routes.
func (h *Handler) Register(mux *http.ServeMux, prefix string) {
	req := func(perm string, fn http.HandlerFunc) http.Handler { return h.authz.Require(perm)(fn) }
	mux.Handle("GET "+prefix+"/admin/categories", req("categories.view", h.list))
	mux.Handle("POST "+prefix+"/admin/categories", req("categories.create", h.create))
	mux.Handle("GET "+prefix+"/admin/categories/{id}", req("categories.view", h.get))
	mux.Handle("PATCH "+prefix+"/admin/categories/{id}", req("categories.view", h.update)) // field-level permissions in service
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.List(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, list)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	c, err := h.svc.Get(r.Context(), h.svc.db, r.PathValue("id"), false)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, c)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in Input
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	c, err := h.svc.Create(r.Context(), in)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, c, nil)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var in Input
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	c, err := h.svc.Update(r.Context(), r.PathValue("id"), in)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, c)
}
