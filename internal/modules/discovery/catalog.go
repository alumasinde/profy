// Package discovery is the public marketplace read model (plan §6.3, §9, §10.4–10.6): search,
// category/service/location pages, service × location landing pages with indexing rules,
// sitemaps, zero-result handling and demand requests.
//
// It reads published data from the catalogue, locations and providers tables (read-only
// joins, documented in docs/decisions.md) and writes only its own tables
// (seo_landing_pages, demand_requests). Provider cards are rendered by the providers module.
package discovery

import (
	"context"
	"database/sql"
	"sort"

	"sync"
	"time"

	"github.com/alumasinde/profy/internal/modules/seo"
	"github.com/alumasinde/profy/internal/shared/ids"
	"github.com/alumasinde/profy/internal/shared/text"
)

// Location types.
const (
	County = "county"
	Town   = "town"
	Area   = "area"
)

// Category is a published category.
type Category struct {
	ID          uint64
	PublicID    string
	ParentID    uint64
	Slug, Name  string
	Description string
	MetaTitle   string
	MetaDesc    string
	UpdatedAt   time.Time
	Parent      *Category
	Children    []*Category
	Services    []*CatService // services directly in this category
}

// Path is the public URL.
func (c *Category) Path() string { return seo.CategoryPath(c.Slug) }

// AllServices returns this category's services and its subcategories' services.
func (c *Category) AllServices() []*CatService {
	out := append([]*CatService{}, c.Services...)
	for _, ch := range c.Children {
		out = append(out, ch.Services...)
	}
	return out
}

// CatService is a published service in a published category.
type CatService struct {
	ID          uint64
	PublicID    string
	Slug, Name  string
	Description string
	MetaTitle   string
	MetaDesc    string
	UpdatedAt   time.Time
	Category    *Category
	norm        string
	synonyms    []string
}

// Path is the public URL.
func (s *CatService) Path() string { return seo.ServicePath(s.Slug) }

// Location is a county, town or area.
type Location struct {
	Type        string
	ID          uint64
	PublicID    string
	Slug, Name  string
	Label       string // "Parklands, Westlands, Nairobi"
	Selectable  bool   // with all ancestors
	Indexable   bool
	Active      bool
	MergedInto  uint64
	MetaTitle   string
	MetaDesc    string
	UpdatedAt   time.Time
	Parent      *Location
	Children    []*Location
	HasCentroid bool
	Lat, Lng    float64
	norm        string
}

// Key identifies a location across types.
func (l *Location) Key() string { return l.Type + ":" + itoa(l.ID) }

// Path is the public location page URL.
func (l *Location) Path() string { return seo.LocationPath(l.Slug) }

// County returns the county ancestor (or itself).
func (l *Location) County() *Location {
	for c := l; c != nil; c = c.Parent {
		if c.Type == County {
			return c
		}
	}
	return nil
}

// Town returns the town ancestor (or itself) when there is one.
func (l *Location) Town() *Location {
	for c := l; c != nil; c = c.Parent {
		if c.Type == Town {
			return c
		}
	}
	return nil
}

// Contains reports whether other is l or inside l.
func (l *Location) Contains(other *Location) bool {
	for c := other; c != nil; c = c.Parent {
		if c == l {
			return true
		}
	}
	return false
}

// Centroid returns the nearest available centroid (self, then ancestors).
func (l *Location) Centroid() (lat, lng float64, ok bool) {
	for c := l; c != nil; c = c.Parent {
		if c.HasCentroid {
			return c.Lat, c.Lng, true
		}
	}
	return 0, 0, false
}

// Snapshot is an immutable view of published reference data. It is rebuilt whenever the
// underlying tables change (fingerprint check), so admin edits show up immediately while
// public requests avoid repeated catalogue queries (§14.2 "cache static reference data").
type Snapshot struct {
	Categories  []*Category // top level, sorted
	categoryBy  map[string]*Category
	Services    []*CatService // sorted by category then sort order
	serviceBy   map[string]*CatService
	serviceByID map[uint64]*CatService
	Counties    []*Location
	locationBy  map[string]*Location // slug → location (active or merged)
	locationKey map[string]*Location // type:id
	aliases     map[string]*Location // normalized alias → location
	fingerprint string
}

// Category returns a published category by slug.
func (s *Snapshot) Category(slug string) *Category { return s.categoryBy[slug] }

// Service returns a published service by slug.
func (s *Snapshot) Service(slug string) *CatService { return s.serviceBy[slug] }

// ServiceByID returns a published service by internal ID.
func (s *Snapshot) ServiceByID(id uint64) *CatService { return s.serviceByID[id] }

// Location returns a location by slug (including archived/merged ones).
func (s *Snapshot) Location(slug string) *Location { return s.locationBy[slug] }

// LocationByKey returns a location by type and internal ID.
func (s *Snapshot) LocationByKey(typ string, id uint64) *Location {
	return s.locationKey[typ+":"+itoa(id)]
}

// Resolved follows merges to the surviving location.
func (s *Snapshot) Resolved(l *Location) *Location {
	for i := 0; l != nil && l.MergedInto != 0 && i < 10; i++ {
		l = s.LocationByKey(l.Type, l.MergedInto)
	}
	return l
}

type catalog struct {
	db   *sql.DB
	mu   sync.Mutex
	snap *Snapshot
}

const fingerprintSQL = `SELECT CONCAT_WS('|',
	(SELECT CONCAT(COUNT(*), '/', COALESCE(MAX(updated_at), '')) FROM categories),
	(SELECT CONCAT(COUNT(*), '/', COALESCE(MAX(updated_at), '')) FROM services),
	(SELECT CONCAT(COUNT(*), '/', COALESCE(MAX(id), 0)) FROM service_synonyms),
	(SELECT CONCAT(COUNT(*), '/', COALESCE(MAX(updated_at), '')) FROM counties),
	(SELECT CONCAT(COUNT(*), '/', COALESCE(MAX(updated_at), '')) FROM towns),
	(SELECT CONCAT(COUNT(*), '/', COALESCE(MAX(updated_at), '')) FROM areas),
	(SELECT CONCAT(COUNT(*), '/', COALESCE(MAX(id), 0)) FROM location_aliases),
	(SELECT CONCAT(COUNT(*), '/', COALESCE(MAX(updated_at), '')) FROM location_geo))`

// get returns the current snapshot, rebuilding it when the fingerprint changed.
func (c *catalog) get(ctx context.Context) (*Snapshot, error) {
	var fp string
	if err := c.db.QueryRowContext(ctx, fingerprintSQL).Scan(&fp); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snap != nil && c.snap.fingerprint == fp {
		return c.snap, nil
	}
	s, err := load(ctx, c.db)
	if err != nil {
		return nil, err
	}
	s.fingerprint = fp
	c.snap = s
	return s, nil
}

func load(ctx context.Context, db *sql.DB) (*Snapshot, error) {
	s := &Snapshot{categoryBy: map[string]*Category{}, serviceBy: map[string]*CatService{}, serviceByID: map[uint64]*CatService{},
		locationBy: map[string]*Location{}, locationKey: map[string]*Location{}, aliases: map[string]*Location{}}

	// Categories (published, with published parent).
	rows, err := db.QueryContext(ctx, `SELECT id, public_id, COALESCE(parent_id, 0), slug, name, COALESCE(description, ''),
		COALESCE(meta_title, ''), COALESCE(meta_description, ''), updated_at FROM categories WHERE status = 'published'
		ORDER BY sort_order, name`)
	if err != nil {
		return nil, err
	}
	byID := map[uint64]*Category{}
	var all []*Category
	for rows.Next() {
		c := &Category{}
		var pub ids.UUID
		if err := rows.Scan(&c.ID, &pub, &c.ParentID, &c.Slug, &c.Name, &c.Description, &c.MetaTitle, &c.MetaDesc, &c.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		c.PublicID = pub.String()
		byID[c.ID] = c
		all = append(all, c)
	}
	rows.Close()
	for _, c := range all {
		if c.ParentID == 0 {
			s.Categories = append(s.Categories, c)
			s.categoryBy[c.Slug] = c
		} else if p := byID[c.ParentID]; p != nil {
			c.Parent = p
			p.Children = append(p.Children, c)
			s.categoryBy[c.Slug] = c
		}
	}

	// Services in live categories.
	rows, err = db.QueryContext(ctx, `SELECT id, public_id, category_id, slug, name, COALESCE(description, ''),
		COALESCE(meta_title, ''), COALESCE(meta_description, ''), updated_at FROM services WHERE status = 'published'
		ORDER BY sort_order, name`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		sv := &CatService{}
		var pub ids.UUID
		var cat uint64
		if err := rows.Scan(&sv.ID, &pub, &cat, &sv.Slug, &sv.Name, &sv.Description, &sv.MetaTitle, &sv.MetaDesc, &sv.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		c := byID[cat]
		if c == nil || s.categoryBy[c.Slug] != c {
			continue
		}
		sv.PublicID, sv.Category, sv.norm = pub.String(), c, text.NormalizeTerm(sv.Name)
		c.Services = append(c.Services, sv)
		s.Services = append(s.Services, sv)
		s.serviceBy[sv.Slug] = sv
		s.serviceByID[sv.ID] = sv
	}
	rows.Close()
	rows, err = db.QueryContext(ctx, `SELECT service_id, synonym_normalized FROM service_synonyms`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id uint64
		var syn string
		if err := rows.Scan(&id, &syn); err != nil {
			rows.Close()
			return nil, err
		}
		if sv := s.serviceByID[id]; sv != nil {
			sv.synonyms = append(sv.synonyms, syn)
		}
	}
	rows.Close()

	// Locations: every county/town/area (archived ones are kept for merge redirects).
	type spec struct{ typ, q string }
	specs := []spec{
		{County, `SELECT id, public_id, 0, slug, name, is_selectable, is_indexable, status, 0, COALESCE(meta_title,''), COALESCE(meta_description,''), updated_at FROM counties ORDER BY name`},
		{Town, `SELECT id, public_id, county_id, slug, name, is_selectable, is_indexable, status, COALESCE(merged_into_id,0), COALESCE(meta_title,''), COALESCE(meta_description,''), updated_at FROM towns ORDER BY name`},
		{Area, `SELECT id, public_id, town_id, slug, name, is_selectable, is_indexable, status, COALESCE(merged_into_id,0), COALESCE(meta_title,''), COALESCE(meta_description,''), updated_at FROM areas ORDER BY name`},
	}
	parentType := map[string]string{Town: County, Area: Town}
	for _, sp := range specs {
		rows, err := db.QueryContext(ctx, sp.q)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			l := &Location{Type: sp.typ}
			var pub ids.UUID
			var parent uint64
			var status string
			if err := rows.Scan(&l.ID, &pub, &parent, &l.Slug, &l.Name, &l.Selectable, &l.Indexable, &status, &l.MergedInto,
				&l.MetaTitle, &l.MetaDesc, &l.UpdatedAt); err != nil {
				rows.Close()
				return nil, err
			}
			l.PublicID, l.Active, l.norm = pub.String(), status == "active", text.NormalizeTerm(l.Name)
			if sp.typ != County {
				l.Parent = s.locationKey[parentType[sp.typ]+":"+itoa(parent)]
				if l.Parent == nil {
					continue
				}
				l.Selectable = l.Selectable && l.Parent.Selectable
				l.Label = l.Name + ", " + l.Parent.Label
				if l.Active {
					l.Parent.Children = append(l.Parent.Children, l)
				}
			} else {
				l.Label = l.Name
				s.Counties = append(s.Counties, l)
			}
			l.Selectable = l.Selectable && l.Active
			s.locationKey[l.Key()] = l
			s.locationBy[l.Slug] = l
		}
		rows.Close()
	}
	rows, err = db.QueryContext(ctx, `SELECT location_type, location_id, ST_Latitude(centroid), ST_Longitude(centroid) FROM location_geo`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var typ string
		var id uint64
		var lat, lng float64
		if err := rows.Scan(&typ, &id, &lat, &lng); err != nil {
			rows.Close()
			return nil, err
		}
		if l := s.locationKey[typ+":"+itoa(id)]; l != nil {
			l.HasCentroid, l.Lat, l.Lng = true, lat, lng
		}
	}
	rows.Close()
	rows, err = db.QueryContext(ctx, `SELECT location_type, location_id, alias_normalized FROM location_aliases`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var typ, alias string
		var id uint64
		if err := rows.Scan(&typ, &id, &alias); err != nil {
			rows.Close()
			return nil, err
		}
		if l := s.locationKey[typ+":"+itoa(id)]; l != nil {
			s.aliases[alias] = l
		}
	}
	rows.Close()
	return s, nil
}

// ---- Text matching ---------------------------------------------------------------------

// Relevance levels for free-text service matching (§10.4).
const (
	relExact    = 1.0
	relSynonym  = 0.95
	relContains = 0.8
	relCategory = 0.7
)

// MatchServices maps free text to services with a relevance in 0..1: service names and
// slugs, database-managed synonyms ("fundi wa maji" → plumbing) and category names.
func (s *Snapshot) MatchServices(q string) map[uint64]float64 {
	n := text.NormalizeTerm(q)
	out := map[uint64]float64{}
	if len([]rune(n)) < 2 {
		return out
	}
	set := func(id uint64, rel float64) {
		if rel > out[id] {
			out[id] = rel
		}
	}
	for _, sv := range s.Services {
		switch {
		case sv.norm == n || sv.Slug == text.Slugify(q):
			set(sv.ID, relExact)
		case containsWord(sv.norm, n) || (len(n) >= 4 && containsWord(n, sv.norm)):
			set(sv.ID, relContains)
		}
		for _, syn := range sv.synonyms {
			switch {
			case syn == n:
				set(sv.ID, relSynonym)
			case containsWord(syn, n) || (len(n) >= 4 && containsWord(n, syn)):
				set(sv.ID, relContains-0.05)
			}
		}
	}
	for _, c := range s.categoryBy {
		cn := text.NormalizeTerm(c.Name)
		if cn == n || containsWord(cn, n) {
			for _, sv := range c.AllServices() {
				set(sv.ID, relCategory)
			}
		}
	}
	return out
}

// containsWord reports whether needle occurs in hay at word boundaries ("plumb" matches
// "plumbing repairs" as a prefix of a word too).
func containsWord(hay, needle string) bool {
	if needle == "" {
		return false
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle && (i == 0 || hay[i-1] == ' ') {
			return true
		}
	}
	return false
}

// MatchLocations resolves free text to selectable locations: exact names and aliases first
// (towns, then areas, then counties), then prefix matches. The first entry is the best.
func (s *Snapshot) MatchLocations(q string) []*Location {
	n := text.NormalizeTerm(q)
	if len([]rune(n)) < 2 {
		return nil
	}
	rank := map[string]int{Town: 0, Area: 1, County: 2}
	var exact, prefix []*Location
	seen := map[*Location]bool{}
	add := func(list *[]*Location, l *Location) {
		l = s.Resolved(l)
		if l != nil && l.Selectable && !seen[l] {
			seen[l] = true
			*list = append(*list, l)
		}
	}
	if l := s.aliases[n]; l != nil {
		add(&exact, l)
	}
	for _, l := range s.locationKey {
		if l.norm == n {
			add(&exact, l)
		}
	}
	for _, l := range s.locationKey {
		if containsWord(l.norm, n) {
			add(&prefix, l)
		}
	}
	for a, l := range s.aliases {
		if containsWord(a, n) {
			add(&prefix, l)
		}
	}
	order := func(list []*Location) {
		sort.SliceStable(list, func(i, j int) bool {
			if rank[list[i].Type] != rank[list[j].Type] {
				return rank[list[i].Type] < rank[list[j].Type]
			}
			return list[i].Label < list[j].Label
		})
	}
	order(exact)
	order(prefix)
	out := append(exact, prefix...)
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
