// Package locations administers the County → Town → Area hierarchy, aliases, centroids and
// merges (plan §10.1, §10.3, §10.7). Counties are official reference data: their names, codes
// and slugs come from seeds; administrators control flags, metadata and centroids.
package locations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alumasinde/profy/internal/authz"
	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/modules/audit"
	"github.com/alumasinde/profy/internal/modules/seo"
	"github.com/alumasinde/profy/internal/shared/ids"
	"github.com/alumasinde/profy/internal/shared/paging"
	"github.com/alumasinde/profy/internal/shared/reqctx"
	"github.com/alumasinde/profy/internal/shared/text"
)

// Location types.
const (
	County = "county"
	Town   = "town"
	Area   = "area"
)

// Kenya's bounding box, with a small margin (§10.3: optionally validate points lie in Kenya).
const (
	MinLat, MaxLat = -4.9, 5.1
	MinLng, MaxLng = 33.8, 42.0
	MaxAliases     = 50
)

type kind struct {
	typ, table, parentCol, parentTable, childTable, childCol string
}

var kinds = map[string]kind{
	County: {County, "counties", "", "", "towns", "county_id"},
	Town:   {Town, "towns", "county_id", "counties", "areas", "town_id"},
	Area:   {Area, "areas", "town_id", "towns", "", ""},
}

// TypeFromPlural maps an API path segment ("counties") to a type.
func TypeFromPlural(p string) (string, bool) {
	switch p {
	case "counties":
		return County, true
	case "towns":
		return Town, true
	case "areas":
		return Area, true
	}
	return "", false
}

// Point is a WGS84 coordinate.
type Point struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// Location is the admin view of a county, town or area.
type Location struct {
	ID              string   `json:"id"`
	Type            string   `json:"type"`
	Name            string   `json:"name"`
	Slug            string   `json:"slug"`
	Path            string   `json:"path"`
	Code            string   `json:"code,omitempty"`
	ParentID        *string  `json:"parent_id"`
	ParentName      string   `json:"parent_name,omitempty"`
	IsSelectable    bool     `json:"is_selectable"`
	IsIndexable     bool     `json:"is_indexable"`
	Status          string   `json:"status"`
	MergedIntoID    *string  `json:"merged_into_id,omitempty"`
	MetaTitle       *string  `json:"meta_title"`
	MetaDescription *string  `json:"meta_description"`
	ChildCount      int      `json:"child_count"`
	Centroid        *Point   `json:"centroid"`
	Aliases         []string `json:"aliases,omitempty"`
	internalID      uint64
	parentInternal  uint64
}

// Service implements location administration.
type Service struct {
	db         *sql.DB
	authz      *authz.Service
	mergeHooks []MergeHook
}

// New builds the service.
func New(db *sql.DB, az *authz.Service) *Service { return &Service{db: db, authz: az} }

// selectSQL builds the shared SELECT for a type.
func selectSQL(k kind) string {
	parentSel, parentJoin := "NULL, '', 0", ""
	code := "''"
	if k.typ == County {
		code = "x.code"
	} else {
		parentSel = "p.public_id, p.name, p.id"
		parentJoin = " JOIN " + k.parentTable + " p ON p.id = x." + k.parentCol
	}
	merged, mergedJoin := "NULL", ""
	if k.typ != County {
		merged, mergedJoin = "mg.public_id", " LEFT JOIN "+k.table+" mg ON mg.id = x.merged_into_id"
	}
	children := "0"
	if k.childTable != "" {
		children = "(SELECT COUNT(*) FROM " + k.childTable + " c WHERE c." + k.childCol + " = x.id AND c.status = 'active')"
	}
	return `SELECT x.id, x.public_id, x.name, x.slug, ` + code + `, ` + parentSel + `, x.is_selectable, x.is_indexable, x.status, ` +
		merged + `, x.meta_title, x.meta_description, ` + children + `,
		ST_Latitude(g.centroid), ST_Longitude(g.centroid)
		FROM ` + k.table + ` x` + parentJoin + mergedJoin + `
		LEFT JOIN location_geo g ON g.location_type = '` + k.typ + `' AND g.location_id = x.id`
}

func scan(k kind, row interface{ Scan(...any) error }) (*Location, error) {
	var l Location
	var pub ids.UUID
	var parentPub, mergedPub []byte
	var parentID sql.NullInt64
	var mt, md sql.NullString
	var lat, lng sql.NullFloat64
	err := row.Scan(&l.internalID, &pub, &l.Name, &l.Slug, &l.Code, &parentPub, &l.ParentName, &parentID,
		&l.IsSelectable, &l.IsIndexable, &l.Status, &mergedPub, &mt, &md, &l.ChildCount, &lat, &lng)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, response.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	l.ID, l.Type, l.Path = pub.String(), k.typ, seo.LocationPath(l.Slug)
	if s := uuidString(parentPub); s != "" {
		l.ParentID, l.parentInternal = &s, uint64(parentID.Int64)
	}
	if s := uuidString(mergedPub); s != "" {
		l.MergedIntoID = &s
	}
	if mt.Valid {
		l.MetaTitle = &mt.String
	}
	if md.Valid {
		l.MetaDescription = &md.String
	}
	if lat.Valid && lng.Valid {
		l.Centroid = &Point{Latitude: lat.Float64, Longitude: lng.Float64}
	}
	return &l, nil
}

func uuidString(b []byte) string {
	if len(b) != 16 {
		return ""
	}
	var u ids.UUID
	copy(u[:], b)
	return u.String()
}

// ListInput filters a list.
type ListInput struct {
	Type, ParentID, Status, Query, Cursor string
	Limit                                 int
}

// List returns locations of a type ordered by name with keyset pagination on (name, id).
func (s *Service) List(ctx context.Context, in ListInput) ([]Location, string, error) {
	k, ok := kinds[in.Type]
	if !ok {
		return nil, "", response.ErrNotFound
	}
	after, ok := paging.Decode(in.Cursor)
	if !ok {
		return nil, "", response.Validation(map[string]string{"cursor": "invalid"})
	}
	limit := paging.Limit(in.Limit, 100, 500)
	q, args := selectSQL(k)+` WHERE 1=1`, []any{}
	if after > 0 {
		q += ` AND (x.name, x.id) > (SELECT name, id FROM ` + k.table + ` WHERE id = ?)`
		args = append(args, after)
	}
	if in.ParentID != "" && k.parentTable != "" {
		pid, err := ids.Parse(in.ParentID)
		if err != nil {
			return nil, "", response.Validation(map[string]string{"parent_id": "invalid"})
		}
		q += ` AND p.public_id = ?`
		args = append(args, pid)
	}
	if in.Status != "" {
		q += ` AND x.status = ?`
		args = append(args, in.Status)
	}
	if t := strings.TrimSpace(in.Query); t != "" {
		q += ` AND (x.name LIKE ? OR EXISTS (SELECT 1 FROM location_aliases a WHERE a.location_type = '` + k.typ +
			`' AND a.location_id = x.id AND a.alias_normalized LIKE ?))`
		args = append(args, "%"+strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(t)+"%", "%"+text.NormalizeTerm(t)+"%")
	}
	q += ` ORDER BY x.name, x.id LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []Location{}
	for rows.Next() {
		l, err := scan(k, rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, *l)
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

// Get loads one location with aliases.
func (s *Service) Get(ctx context.Context, ex database.Execer, typ, publicID string, lock bool) (*Location, error) {
	k, ok := kinds[typ]
	if !ok {
		return nil, response.ErrNotFound
	}
	pub, err := ids.Parse(publicID)
	if err != nil {
		return nil, response.ErrNotFound
	}
	q := selectSQL(k) + ` WHERE x.public_id = ?`
	if lock {
		q += ` FOR UPDATE OF x`
	}
	l, err := scan(k, ex.QueryRowContext(ctx, q, pub))
	if err != nil {
		return nil, err
	}
	l.Aliases, err = s.aliases(ctx, ex, typ, l.internalID)
	return l, err
}

func (s *Service) aliases(ctx context.Context, ex database.Execer, typ string, id uint64) ([]string, error) {
	rows, err := ex.QueryContext(ctx, `SELECT alias FROM location_aliases WHERE location_type = ? AND location_id = ? ORDER BY alias`, typ, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Input is a create/update request; nil fields are unchanged on update.
type Input struct {
	ParentID        *string `json:"parent_id"`
	Name            *string `json:"name"`
	Slug            *string `json:"slug"`
	IsSelectable    *bool   `json:"is_selectable"`
	IsIndexable     *bool   `json:"is_indexable"`
	Status          *string `json:"status"` // active | archived
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
}

// resolveParent returns an active parent's internal ID and slug.
func (s *Service) resolveParent(ctx context.Context, ex database.Execer, typ, parentPublic string) (uint64, string, string) {
	k := kinds[typ]
	pid, err := ids.Parse(parentPublic)
	if err != nil {
		return 0, "", "invalid"
	}
	var id uint64
	var slug, status string
	err = ex.QueryRowContext(ctx, `SELECT id, slug, status FROM `+k.parentTable+` WHERE public_id = ?`, pid).Scan(&id, &slug, &status)
	if err != nil || status != "active" {
		return 0, "", "invalid"
	}
	return id, slug, ""
}

// reserveSlug claims a slug in the shared namespace. With auto set it disambiguates
// ("kahawa" → "kahawa-nairobi" → "kahawa-nairobi-2"), resolving clashes at creation time (§9.1).
func reserveSlug(ctx context.Context, tx *sql.Tx, typ string, id uint64, base string, qualifiers []string, auto bool) (string, error) {
	candidates := []string{base}
	if auto {
		for _, q := range qualifiers {
			if q != "" {
				candidates = append(candidates, text.Slugify(base+"-"+q))
			}
		}
		last := candidates[len(candidates)-1]
		for i := 2; i <= 50; i++ {
			candidates = append(candidates, last+"-"+strconv.Itoa(i))
		}
	}
	for _, c := range candidates {
		if !text.ValidSlug(c) {
			continue
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO location_slugs (slug, location_type, location_id) VALUES (?, ?, ?)`, c, typ, id)
		if err == nil {
			return c, nil
		}
		if !database.IsDuplicate(err) {
			return "", err
		}
	}
	return "", response.Validation(map[string]string{"slug": "taken"})
}

// Create adds a town or area (locations.create). Counties come only from seeds.
func (s *Service) Create(ctx context.Context, typ string, in Input) (*Location, error) {
	k, ok := kinds[typ]
	if !ok || typ == County {
		return nil, response.ErrNotFound
	}
	errs := map[string]string{}
	validateMeta(errs, in)
	name := ""
	if in.Name == nil {
		errs["name"] = "required"
	} else if n, ok := text.CleanLine(*in.Name, 120); !ok {
		errs["name"] = "required"
	} else {
		name = n
	}
	explicit := in.Slug != nil && strings.TrimSpace(*in.Slug) != ""
	slug := text.Slugify(name)
	if explicit {
		slug = strings.TrimSpace(*in.Slug)
		if !text.ValidSlug(slug) {
			errs["slug"] = "invalid"
		}
	}
	if in.ParentID == nil {
		errs["parent_id"] = "required"
	}
	if in.Status != nil && *in.Status != "active" {
		errs["status"] = "invalid"
	}
	if len(errs) > 0 {
		return nil, response.Validation(errs)
	}
	pub := ids.New()
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		parentID, parentSlug, code := s.resolveParent(ctx, tx, typ, *in.ParentID)
		if code != "" {
			return response.Validation(map[string]string{"parent_id": code})
		}
		qualifiers := []string{parentSlug}
		if typ == Area {
			var countySlug string
			if err := tx.QueryRowContext(ctx, `SELECT c.slug FROM towns t JOIN counties c ON c.id = t.county_id WHERE t.id = ?`, parentID).Scan(&countySlug); err != nil {
				return err
			}
			if countySlug != parentSlug {
				qualifiers = append([]string{countySlug}, parentSlug)
			}
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO `+k.table+` (public_id, `+k.parentCol+`, name, slug, is_selectable, is_indexable,
			meta_title, meta_description) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			pub, parentID, name, "pending-"+pub.String(), boolOr(in.IsSelectable, false), boolOr(in.IsIndexable, false),
			optional(in.MetaTitle), optional(in.MetaDescription))
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		final, err := reserveSlug(ctx, tx, typ, uint64(id), slug, qualifiers, !explicit)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE `+k.table+` SET slug = ? WHERE id = ?`, final, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "locations.created", EntityType: typ, EntityID: pub.String(),
			NewValues: map[string]any{"name": name, "slug": final}})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, s.db, typ, pub.String(), false)
}

func boolOr(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

// Update edits a location. Counties accept flags and metadata only. Archiving needs
// locations.archive and is refused while active children remain; slug changes update the
// shared namespace and create a 301 redirect.
func (s *Service) Update(ctx context.Context, typ, publicID string, in Input) (*Location, error) {
	k, ok := kinds[typ]
	if !ok {
		return nil, response.ErrNotFound
	}
	p := reqctx.PrincipalFrom(ctx)
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		cur, err := s.Get(ctx, tx, typ, publicID, true)
		if err != nil {
			return err
		}
		errs := map[string]string{}
		validateMeta(errs, in)
		if typ == County && (in.Name != nil || in.Slug != nil || in.ParentID != nil || in.Status != nil) {
			errs["county"] = "read_only" // official reference data (seeds)
		}
		if cur.Status == "archived" && cur.MergedIntoID != nil {
			return response.Conflict(response.CodeMerged, "This location was merged and cannot be edited.")
		}
		name, slug, status, parent := cur.Name, cur.Slug, cur.Status, cur.parentInternal
		if in.Name != nil {
			n, ok := text.CleanLine(*in.Name, 120)
			if !ok {
				errs["name"] = "required"
			}
			name = n
		}
		if in.Slug != nil {
			slug = strings.TrimSpace(*in.Slug)
			if !text.ValidSlug(slug) {
				errs["slug"] = "invalid"
			}
		}
		if in.Status != nil {
			if *in.Status != "active" && *in.Status != "archived" {
				errs["status"] = "invalid"
			}
			status = *in.Status
		}
		if in.ParentID != nil && typ != County {
			pid, _, code := s.resolveParent(ctx, tx, typ, *in.ParentID)
			if code != "" {
				errs["parent_id"] = code
			}
			parent = pid
		}
		if len(errs) > 0 {
			return response.Validation(errs)
		}
		if status != cur.Status && !s.authz.Can(ctx, p, "locations.archive") {
			return response.ErrForbidden
		}
		edits := in.Name != nil || in.Slug != nil || in.ParentID != nil || in.IsSelectable != nil || in.IsIndexable != nil ||
			in.MetaTitle != nil || in.MetaDescription != nil
		if edits && !s.authz.Can(ctx, p, "locations.update") {
			return response.ErrForbidden
		}
		if status == "archived" && cur.ChildCount > 0 {
			return response.Conflict(response.CodeHasDependents, "Archive or merge this location's active children first.")
		}
		if slug != cur.Slug {
			res, err := tx.ExecContext(ctx, `UPDATE location_slugs SET slug = ? WHERE location_type = ? AND location_id = ?`, slug, typ, cur.internalID)
			if database.IsDuplicate(err) {
				return response.Validation(map[string]string{"slug": "taken"})
			}
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return fmt.Errorf("location slug row missing for %s %d", typ, cur.internalID)
			}
			if err := seo.AddRedirect(ctx, tx, seo.LocationPath(cur.Slug), seo.LocationPath(slug), "auto"); err != nil {
				return err
			}
		}
		mt, md := any(cur.MetaTitle), any(cur.MetaDescription)
		if in.MetaTitle != nil {
			mt = optional(in.MetaTitle)
		}
		if in.MetaDescription != nil {
			md = optional(in.MetaDescription)
		}
		sel, idx := boolOr(in.IsSelectable, cur.IsSelectable), boolOr(in.IsIndexable, cur.IsIndexable)
		q := `UPDATE ` + k.table + ` SET name = ?, slug = ?, is_selectable = ?, is_indexable = ?, meta_title = ?, meta_description = ?`
		args := []any{name, slug, sel, idx, mt, md}
		if typ != County {
			q += `, status = ?, ` + k.parentCol + ` = ?`
			args = append(args, status, parent)
		}
		if _, err := tx.ExecContext(ctx, q+` WHERE id = ?`, append(args, cur.internalID)...); err != nil {
			return err
		}
		old, nw := map[string]any{}, map[string]any{}
		for key, pair := range map[string][2]any{"name": {cur.Name, name}, "slug": {cur.Slug, slug}, "status": {cur.Status, status},
			"is_selectable": {cur.IsSelectable, sel}, "is_indexable": {cur.IsIndexable, idx}} {
			if pair[0] != pair[1] {
				old[key], nw[key] = pair[0], pair[1]
			}
		}
		if parent != cur.parentInternal {
			old["parent"], nw["parent"] = cur.ParentName, "changed"
		}
		if len(nw) == 0 && in.MetaTitle == nil && in.MetaDescription == nil {
			return nil
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "locations.updated", EntityType: typ, EntityID: cur.ID, OldValues: old, NewValues: nw})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, s.db, typ, publicID, false)
}

// SetAliases replaces a location's aliases (locations.manage_aliases). An alias resolves to
// exactly one location, so aliases already used by another location are rejected.
func (s *Service) SetAliases(ctx context.Context, typ, publicID string, list []string) (*Location, error) {
	if len(list) > MaxAliases {
		return nil, response.Validation(map[string]string{"aliases": "too_many"})
	}
	type al struct{ display, norm string }
	var clean []al
	for i, raw := range list {
		v, ok := text.CleanLine(raw, 120)
		n := text.NormalizeTerm(v)
		if !ok || n == "" {
			return nil, response.Validation(map[string]string{"aliases." + strconv.Itoa(i): "invalid"})
		}
		if !slices.ContainsFunc(clean, func(x al) bool { return x.norm == n }) {
			clean = append(clean, al{v, n})
		}
	}
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		cur, err := s.Get(ctx, tx, typ, publicID, true)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM location_aliases WHERE location_type = ? AND location_id = ?`, typ, cur.internalID); err != nil {
			return err
		}
		names := []string{}
		for i, a := range clean {
			_, err := tx.ExecContext(ctx, `INSERT INTO location_aliases (location_type, location_id, alias, alias_normalized) VALUES (?, ?, ?, ?)`,
				typ, cur.internalID, a.display, a.norm)
			if database.IsDuplicate(err) {
				return response.Validation(map[string]string{"aliases." + strconv.Itoa(i): "taken"})
			}
			if err != nil {
				return err
			}
			names = append(names, a.display)
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "locations.aliases.updated", EntityType: typ, EntityID: cur.ID,
			OldValues: map[string]any{"aliases": cur.Aliases}, NewValues: map[string]any{"aliases": names}})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, s.db, typ, publicID, false)
}

// ValidatePoint checks ranges and the Kenya bounding box.
func ValidatePoint(p Point) map[string]string {
	errs := map[string]string{}
	if p.Latitude < -90 || p.Latitude > 90 || p.Latitude < MinLat || p.Latitude > MaxLat {
		errs["latitude"] = "out_of_range"
	}
	if p.Longitude < -180 || p.Longitude > 180 || p.Longitude < MinLng || p.Longitude > MaxLng {
		errs["longitude"] = "out_of_range"
	}
	return errs
}

// SetCentroid sets or clears (p == nil) a location's centroid (locations.manage_coordinates).
// Points are written with ST_SRID(POINT(lng, lat), 4326) — longitude first — the single
// helper for axis order (§10.3).
func (s *Service) SetCentroid(ctx context.Context, typ, publicID string, p *Point) (*Location, error) {
	if p != nil {
		if errs := ValidatePoint(*p); len(errs) > 0 {
			return nil, response.Validation(errs)
		}
	}
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		cur, err := s.Get(ctx, tx, typ, publicID, true)
		if err != nil {
			return err
		}
		if p == nil {
			if _, err := tx.ExecContext(ctx, `DELETE FROM location_geo WHERE location_type = ? AND location_id = ?`, typ, cur.internalID); err != nil {
				return err
			}
		} else if _, err := tx.ExecContext(ctx, `INSERT INTO location_geo (location_type, location_id, centroid, source)
			VALUES (?, ?, ST_SRID(POINT(?, ?), 4326), 'admin') AS new ON DUPLICATE KEY UPDATE centroid = new.centroid, source = new.source`,
			typ, cur.internalID, p.Longitude, p.Latitude); err != nil {
			return err
		}
		var newV any
		if p != nil {
			newV = map[string]any{"latitude": p.Latitude, "longitude": p.Longitude}
		}
		var oldV any
		if cur.Centroid != nil {
			oldV = map[string]any{"latitude": cur.Centroid.Latitude, "longitude": cur.Centroid.Longitude}
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "locations.centroid.updated", EntityType: typ, EntityID: cur.ID,
			OldValues: map[string]any{"centroid": oldV}, NewValues: map[string]any{"centroid": newV}})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, s.db, typ, publicID, false)
}

// Merge folds a duplicate town or area into another of the same type (locations.merge):
// children and aliases move to the target, the source name becomes an alias of the target,
// the source is archived with merged_into set (never deleted), its slug stays reserved and
// redirects to the target. Registered merge hooks re-point other modules' references
// (provider base areas and service areas) in the same transaction.
func (s *Service) Merge(ctx context.Context, typ, sourceID, targetID string) (*Location, error) {
	k, ok := kinds[typ]
	if !ok || typ == County {
		return nil, response.Validation(map[string]string{"type": "invalid"})
	}
	if sourceID == targetID {
		return nil, response.Validation(map[string]string{"target_id": "invalid"})
	}
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		src, err := s.Get(ctx, tx, typ, sourceID, true)
		if err != nil {
			return response.Validation(map[string]string{"source_id": "invalid"})
		}
		dst, err := s.Get(ctx, tx, typ, targetID, true)
		if err != nil {
			return response.Validation(map[string]string{"target_id": "invalid"})
		}
		if src.Status != "active" || dst.Status != "active" {
			return response.New(http.StatusConflict, response.CodeConflict, "Only active locations can be merged.")
		}
		moved := int64(0)
		if k.childTable != "" {
			res, err := tx.ExecContext(ctx, `UPDATE `+k.childTable+` SET `+k.childCol+` = ? WHERE `+k.childCol+` = ?`, dst.internalID, src.internalID)
			if err != nil {
				return err
			}
			moved, _ = res.RowsAffected()
		}
		if _, err := tx.ExecContext(ctx, `UPDATE location_aliases SET location_id = ? WHERE location_type = ? AND location_id = ?`,
			dst.internalID, typ, src.internalID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT IGNORE INTO location_aliases (location_type, location_id, alias, alias_normalized)
			VALUES (?, ?, ?, ?)`, typ, dst.internalID, src.Name, text.NormalizeTerm(src.Name)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM location_geo WHERE location_type = ? AND location_id = ?`, typ, src.internalID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE `+k.table+` SET status = 'archived', is_selectable = 0, is_indexable = 0, merged_into_id = ?
			WHERE id = ?`, dst.internalID, src.internalID); err != nil {
			return err
		}
		if err := seo.AddRedirect(ctx, tx, seo.LocationPath(src.Slug), seo.LocationPath(dst.Slug), "auto"); err != nil {
			return err
		}
		for _, hook := range s.mergeHooks {
			if err := hook(ctx, tx, typ, src.internalID, dst.internalID); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "locations.merged", EntityType: typ, EntityID: src.ID,
			OldValues: map[string]any{"name": src.Name, "slug": src.Slug},
			NewValues: map[string]any{"merged_into": dst.ID, "merged_into_name": dst.Name, "children_moved": moved,
				"at": time.Now().UTC().Format(time.RFC3339)}})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, s.db, typ, targetID, false)
}
