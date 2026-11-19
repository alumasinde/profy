// Package pages owns managed information pages and header/footer navigation (plan §8.1, §8.3).
package pages

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
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

// ReservedSlugs are first path segments used by the application; pages cannot take them.
var ReservedSlugs = []string{
	"services", "categories", "professionals", "locations", "search", "auth", "account", "provider", "admin",
	"api", "static", "_app", "preview", "healthz", "readyz", "robots-txt", "sitemap", "sitemaps", "theme-css",
	"favicon-ico", "login", "register", "logout", "badges", "media",
}

// Limits.
const (
	MaxBody      = 100_000
	MaxMenuItems = 20
)

// Page is a managed page.
type Page struct {
	ID              string     `json:"id"`
	Slug            string     `json:"slug"`
	Path            string     `json:"path"`
	Title           string     `json:"title"`
	Body            string     `json:"body"`
	Status          string     `json:"status"`
	MetaTitle       *string    `json:"meta_title"`
	MetaDescription *string    `json:"meta_description"`
	RobotsIndex     bool       `json:"robots_index"`
	PublishedAt     *time.Time `json:"published_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	internalID      uint64
}

// MenuItem is one navigation link.
type MenuItem struct {
	ID       string `json:"id,omitempty"`
	Label    string `json:"label"`
	URL      string `json:"url"`
	IsActive bool   `json:"is_active"`
}

// Menus that exist (seeded); code renders these two.
var Menus = []string{"header", "footer"}

// Service implements pages and navigation.
type Service struct {
	db    *sql.DB
	authz *authz.Service

	mu    sync.RWMutex
	menus map[string][]MenuItem
	at    time.Time
}

// New builds the service.
func New(db *sql.DB, az *authz.Service) *Service { return &Service{db: db, authz: az} }

const pageCols = `id, public_id, slug, title, body, status, meta_title, meta_description, robots_index, published_at, updated_at`

func scanPage(row interface{ Scan(...any) error }) (*Page, error) {
	var p Page
	var pub ids.UUID
	var mt, md sql.NullString
	var pa sql.NullTime
	if err := row.Scan(&p.internalID, &pub, &p.Slug, &p.Title, &p.Body, &p.Status, &mt, &md, &p.RobotsIndex, &pa, &p.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, response.ErrNotFound
		}
		return nil, err
	}
	p.ID, p.Path = pub.String(), seo.PagePath(p.Slug)
	if mt.Valid {
		p.MetaTitle = &mt.String
	}
	if md.Valid {
		p.MetaDescription = &md.String
	}
	if pa.Valid {
		p.PublishedAt = &pa.Time
	}
	return &p, nil
}

// List returns pages, optionally filtered by status.
func (s *Service) List(ctx context.Context, status string) ([]Page, error) {
	q, args := `SELECT `+pageCols+` FROM managed_pages`, []any{}
	if status != "" {
		q += ` WHERE status = ?`
		args = append(args, status)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY title LIMIT 500`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Page{}
	for rows.Next() {
		p, err := scanPage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// Get loads a page by public ID.
func (s *Service) Get(ctx context.Context, ex database.Execer, publicID string, lock bool) (*Page, error) {
	pub, err := ids.Parse(publicID)
	if err != nil {
		return nil, response.ErrNotFound
	}
	q := `SELECT ` + pageCols + ` FROM managed_pages WHERE public_id = ?`
	if lock {
		q += ` FOR UPDATE`
	}
	return scanPage(ex.QueryRowContext(ctx, q, pub))
}

// Published loads a published page by slug.
func (s *Service) Published(ctx context.Context, slug string) (*Page, error) {
	return scanPage(s.db.QueryRowContext(ctx, `SELECT `+pageCols+` FROM managed_pages WHERE slug = ? AND status = 'published'`, slug))
}

// PageInput is a create/update request; nil fields are unchanged on update.
type PageInput struct {
	Slug            *string `json:"slug"`
	Title           *string `json:"title"`
	Body            *string `json:"body"`
	Status          *string `json:"status"`
	MetaTitle       *string `json:"meta_title"`
	MetaDescription *string `json:"meta_description"`
	RobotsIndex     *bool   `json:"robots_index"`
}

func validSEO(errs map[string]string, title, desc *string) {
	if title != nil {
		if v, ok := text.CleanLine(*title, 160); !ok && v != "" {
			errs["meta_title"] = "invalid"
		}
	}
	if desc != nil {
		if v, ok := text.CleanLine(*desc, 320); !ok && v != "" {
			errs["meta_description"] = "invalid"
		}
	}
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

func (s *Service) checkSlug(ctx context.Context, ex database.Execer, slug string, exceptID uint64) string {
	if !text.ValidSlug(slug) {
		return "invalid"
	}
	if slices.Contains(ReservedSlugs, slug) {
		return "reserved"
	}
	var n int
	_ = ex.QueryRowContext(ctx, `SELECT COUNT(*) FROM managed_pages WHERE slug = ? AND id <> ?`, slug, exceptID).Scan(&n)
	if n > 0 {
		return "taken"
	}
	return ""
}

// Create adds a draft page (pages.create).
func (s *Service) Create(ctx context.Context, in PageInput) (*Page, error) {
	errs := map[string]string{}
	var title, body, slug string
	if in.Title == nil {
		errs["title"] = "required"
	} else if t, ok := text.CleanLine(*in.Title, 160); !ok {
		errs["title"] = "invalid"
		if t == "" {
			errs["title"] = "required"
		}
	} else {
		title = t
	}
	if in.Body != nil {
		b, ok := text.CleanText(*in.Body, MaxBody)
		if !ok {
			errs["body"] = "too_long"
		}
		body = b
	}
	if in.Slug != nil && strings.TrimSpace(*in.Slug) != "" {
		slug = strings.TrimSpace(*in.Slug)
	} else {
		slug = text.Slugify(title)
	}
	validSEO(errs, in.MetaTitle, in.MetaDescription)
	if in.Status != nil && *in.Status != lifecycle.Draft {
		errs["status"] = "invalid" // pages are created as drafts and published explicitly
	}
	if len(errs) == 0 {
		if code := s.checkSlug(ctx, s.db, slug, 0); code != "" {
			errs["slug"] = code
		}
	}
	if len(errs) > 0 {
		return nil, response.Validation(errs)
	}
	robots := true
	if in.RobotsIndex != nil {
		robots = *in.RobotsIndex
	}
	pub := ids.New()
	p := reqctx.PrincipalFrom(ctx)
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO managed_pages (public_id, slug, title, body, status, meta_title, meta_description, robots_index, updated_by)
			VALUES (?, ?, ?, ?, 'draft', ?, ?, ?, ?)`, pub, slug, title, body, optional(in.MetaTitle), optional(in.MetaDescription), robots, p.UserID)
		if database.IsDuplicate(err) {
			return response.Validation(map[string]string{"slug": "taken"})
		}
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "pages.created", EntityType: "page", EntityID: pub.String(),
			NewValues: map[string]any{"slug": slug, "title": title}})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, s.db, pub.String(), false)
}

// Update applies a partial update. Status changes need the lifecycle permission; slug
// changes on a page that has ever been public create a 301 redirect.
func (s *Service) Update(ctx context.Context, publicID string, in PageInput) (*Page, error) {
	p := reqctx.PrincipalFrom(ctx)
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		cur, err := s.Get(ctx, tx, publicID, true)
		if err != nil {
			return err
		}
		next := *cur
		errs := map[string]string{}
		contentChanged := false
		if in.Title != nil {
			t, ok := text.CleanLine(*in.Title, 160)
			if !ok {
				errs["title"] = "required"
			}
			contentChanged = contentChanged || t != cur.Title
			next.Title = t
		}
		if in.Body != nil {
			b, ok := text.CleanText(*in.Body, MaxBody)
			if !ok {
				errs["body"] = "too_long"
			}
			contentChanged = contentChanged || b != cur.Body
			next.Body = b
		}
		if in.Slug != nil {
			next.Slug = strings.TrimSpace(*in.Slug)
			if next.Slug != cur.Slug {
				if code := s.checkSlug(ctx, tx, next.Slug, cur.internalID); code != "" {
					errs["slug"] = code
				}
				contentChanged = true
			}
		}
		validSEO(errs, in.MetaTitle, in.MetaDescription)
		if in.MetaTitle != nil || in.MetaDescription != nil || in.RobotsIndex != nil {
			contentChanged = true
		}
		if in.Status != nil {
			if !lifecycle.Valid(*in.Status) {
				errs["status"] = "invalid"
			}
			next.Status = *in.Status
		}
		if len(errs) > 0 {
			return response.Validation(errs)
		}
		if contentChanged && !s.authz.Can(ctx, p, "pages.update") {
			return response.ErrForbidden
		}
		if next.Status != cur.Status && !s.authz.Can(ctx, p, lifecycle.Permission("pages", cur.Status, next.Status)) {
			return response.ErrForbidden
		}
		if next.Status == lifecycle.Published && strings.TrimSpace(next.Body) == "" {
			return response.Validation(map[string]string{"body": "required"})
		}
		var publishedAt any = cur.PublishedAt
		if next.Status == lifecycle.Published && cur.Status != lifecycle.Published {
			publishedAt = time.Now().UTC()
		}
		robots := cur.RobotsIndex
		if in.RobotsIndex != nil {
			robots = *in.RobotsIndex
		}
		mt, md := any(cur.MetaTitle), any(cur.MetaDescription)
		if in.MetaTitle != nil {
			mt = optional(in.MetaTitle)
		}
		if in.MetaDescription != nil {
			md = optional(in.MetaDescription)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE managed_pages SET slug = ?, title = ?, body = ?, status = ?, meta_title = ?,
			meta_description = ?, robots_index = ?, published_at = ?, updated_by = ? WHERE id = ?`,
			next.Slug, next.Title, next.Body, next.Status, mt, md, robots, publishedAt, p.UserID, cur.internalID); err != nil {
			if database.IsDuplicate(err) {
				return response.Validation(map[string]string{"slug": "taken"})
			}
			return err
		}
		if next.Slug != cur.Slug && cur.PublishedAt != nil {
			if err := seo.AddRedirect(ctx, tx, seo.PagePath(cur.Slug), seo.PagePath(next.Slug), "auto"); err != nil {
				return err
			}
		}
		old, nw := map[string]any{}, map[string]any{}
		diff := func(k string, a, b any) {
			if a != b {
				old[k], nw[k] = a, b
			}
		}
		diff("slug", cur.Slug, next.Slug)
		diff("title", cur.Title, next.Title)
		diff("status", cur.Status, next.Status)
		if cur.Body != next.Body {
			old["body_length"], nw["body_length"] = len(cur.Body), len(next.Body)
		}
		if len(nw) == 0 && !contentChanged {
			return nil
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "pages.updated", EntityType: "page", EntityID: cur.ID, OldValues: old, NewValues: nw})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, s.db, publicID, false)
}

// ---- Navigation -----------------------------------------------------------------------

// Menu returns all items of a menu (admin view, including inactive ones).
func (s *Service) Menu(ctx context.Context, key string) ([]MenuItem, error) {
	if !slices.Contains(Menus, key) {
		return nil, response.ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `SELECT i.public_id, i.label, i.url, i.is_active FROM navigation_items i
		JOIN navigation_menus m ON m.id = i.menu_id WHERE m.menu_key = ? ORDER BY i.position, i.id`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MenuItem{}
	for rows.Next() {
		var it MenuItem
		var pub ids.UUID
		if err := rows.Scan(&pub, &it.Label, &it.URL, &it.IsActive); err != nil {
			return nil, err
		}
		it.ID = pub.String()
		out = append(out, it)
	}
	return out, rows.Err()
}

// ReplaceMenu replaces a menu's items in order (audited).
func (s *Service) ReplaceMenu(ctx context.Context, key string, items []MenuItem) ([]MenuItem, error) {
	if !slices.Contains(Menus, key) {
		return nil, response.ErrNotFound
	}
	if len(items) > MaxMenuItems {
		return nil, response.Validation(map[string]string{"items": "too_many"})
	}
	errs := map[string]string{}
	for i := range items {
		l, ok := text.CleanLine(items[i].Label, 80)
		if !ok {
			errs["items."+strconv.Itoa(i)+".label"] = "required"
		}
		items[i].Label, items[i].URL = l, strings.TrimSpace(items[i].URL)
		if !SafeURL(items[i].URL) {
			errs["items."+strconv.Itoa(i)+".url"] = "invalid"
		}
	}
	if len(errs) > 0 {
		return nil, response.Validation(errs)
	}
	old, err := s.Menu(ctx, key)
	if err != nil {
		return nil, err
	}
	err = database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		var menuID int
		if err := tx.QueryRowContext(ctx, `SELECT id FROM navigation_menus WHERE menu_key = ? FOR UPDATE`, key).Scan(&menuID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM navigation_items WHERE menu_id = ?`, menuID); err != nil {
			return err
		}
		for i, it := range items {
			if _, err := tx.ExecContext(ctx, `INSERT INTO navigation_items (public_id, menu_id, label, url, position, is_active)
				VALUES (?, ?, ?, ?, ?, ?)`, ids.New(), menuID, it.Label, it.URL, i, it.IsActive); err != nil {
				return err
			}
		}
		strip := func(list []MenuItem) []map[string]any {
			out := []map[string]any{}
			for _, x := range list {
				out = append(out, map[string]any{"label": x.Label, "url": x.URL, "active": x.IsActive})
			}
			return out
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "navigation.updated", EntityType: "navigation_menu", EntityID: key,
			OldValues: map[string]any{"items": strip(old)}, NewValues: map[string]any{"items": strip(items)}})
	})
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.menus = nil
	s.mu.Unlock()
	return s.Menu(ctx, key)
}

// PublicMenu returns active items of a menu from an in-memory cache (§14.2).
func (s *Service) PublicMenu(ctx context.Context, key string) []MenuItem {
	s.mu.RLock()
	m, at := s.menus, s.at
	s.mu.RUnlock()
	if m == nil || time.Since(at) > time.Minute {
		fresh := map[string][]MenuItem{}
		for _, k := range Menus {
			all, err := s.Menu(ctx, k)
			if err != nil {
				return nil
			}
			for _, it := range all {
				if it.IsActive {
					fresh[k] = append(fresh[k], MenuItem{Label: it.Label, URL: it.URL, IsActive: true})
				}
			}
		}
		s.mu.Lock()
		s.menus, s.at = fresh, time.Now()
		s.mu.Unlock()
		m = fresh
	}
	return m[key]
}

// ---- HTTP (admin) ---------------------------------------------------------------------

// AdminHandler exposes /api/v1/admin/pages and /admin/navigation.
type AdminHandler struct {
	svc   *Service
	authz *authz.Service
}

// NewAdminHandler builds the handler.
func NewAdminHandler(svc *Service, az *authz.Service) *AdminHandler {
	return &AdminHandler{svc: svc, authz: az}
}

// Register mounts routes.
func (h *AdminHandler) Register(mux *http.ServeMux, prefix string) {
	req := func(perm string, fn http.HandlerFunc) http.Handler { return h.authz.Require(perm)(fn) }
	mux.Handle("GET "+prefix+"/admin/pages", req("pages.view", h.list))
	mux.Handle("POST "+prefix+"/admin/pages", req("pages.create", h.create))
	mux.Handle("GET "+prefix+"/admin/pages/{id}", req("pages.view", h.get))
	mux.Handle("PATCH "+prefix+"/admin/pages/{id}", req("pages.view", h.update)) // field-level permissions in service
	mux.Handle("GET "+prefix+"/admin/navigation/{menu}", req("navigation.view", h.menu))
	mux.Handle("PUT "+prefix+"/admin/navigation/{menu}", req("navigation.update", h.replaceMenu))
}

func (h *AdminHandler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.List(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, list)
}

func (h *AdminHandler) get(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.Get(r.Context(), h.svc.db, r.PathValue("id"), false)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, p)
}

func (h *AdminHandler) create(w http.ResponseWriter, r *http.Request) {
	var in PageInput
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	p, err := h.svc.Create(r.Context(), in)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, p, nil)
}

func (h *AdminHandler) update(w http.ResponseWriter, r *http.Request) {
	var in PageInput
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	p, err := h.svc.Update(r.Context(), r.PathValue("id"), in)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, p)
}

func (h *AdminHandler) menu(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.Menu(r.Context(), r.PathValue("menu"))
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, items)
}

func (h *AdminHandler) replaceMenu(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Items []MenuItem `json:"items"`
	}
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	items, err := h.svc.ReplaceMenu(r.Context(), r.PathValue("menu"), in.Items)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, items)
}

// Indexable returns published pages that allow indexing (for the sitemap).
func (s *Service) Indexable(ctx context.Context) ([]Page, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+pageCols+` FROM managed_pages WHERE status = 'published' AND robots_index = 1 ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Page
	for rows.Next() {
		p, err := scanPage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}
