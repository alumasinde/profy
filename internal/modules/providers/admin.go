package providers

import (
	"context"
	"strings"
	"time"

	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/modules/locations"
	"github.com/alumasinde/profy/internal/shared/paging"
	"github.com/alumasinde/profy/internal/shared/reqctx"
	"github.com/alumasinde/profy/internal/shared/text"
)

// ListInput filters the staff provider list / review queue.
type ListInput struct {
	Status string
	Q      string
	Cursor string
	Limit  int
}

// ListItem is a row in the staff list.
type ListItem struct {
	ID           string     `json:"id"`
	DisplayName  string     `json:"display_name"`
	Type         string     `json:"provider_type"`
	Slug         string     `json:"slug"`
	Status       string     `json:"status"`
	Completeness int        `json:"completeness"`
	Assisted     bool       `json:"assisted"`
	Claimed      bool       `json:"claimed"`
	BaseArea     *string    `json:"base_area"`
	SubmittedAt  *time.Time `json:"submitted_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// ListPage is a page of providers.
type ListPage struct {
	Items      []ListItem     `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
	Counts     map[string]int `json:"counts"`
}

var listStatuses = []string{StatusDraft, StatusPending, StatusPublished, StatusPaused, StatusSuspended, StatusArchived}

// List returns providers for staff. The pending_review queue is oldest-first (FIFO);
// other views are newest-first.
func (s *Service) List(ctx context.Context, in ListInput) (*ListPage, error) {
	p := reqctx.PrincipalFrom(ctx)
	if p == nil || !s.authz.Can(ctx, p, "providers.view_any") {
		return nil, response.ErrForbidden
	}
	after, ok := paging.Decode(in.Cursor)
	if !ok {
		return nil, response.Validation(map[string]string{"cursor": "invalid"})
	}
	limit := paging.Limit(in.Limit, 25, 100)
	where := []string{"1=1"}
	args := []any{}
	if in.Status != "" {
		valid := false
		for _, st := range listStatuses {
			valid = valid || st == in.Status
		}
		if !valid {
			return nil, response.Validation(map[string]string{"status": "invalid"})
		}
		where = append(where, "listing_status = ?")
		args = append(args, in.Status)
	}
	if q := text.NormalizeTerm(in.Q); q != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q) + "%"
		where = append(where, "(display_name LIKE ? OR slug LIKE ?)")
		args = append(args, like, like)
	}
	fifo := in.Status == StatusPending
	order := "id DESC"
	if after > 0 {
		if fifo {
			where = append(where, "id > ?")
		} else {
			where = append(where, "id < ?")
		}
		args = append(args, after)
	}
	if fifo {
		order = "id ASC"
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `SELECT `+rowCols+` FROM providers WHERE `+strings.Join(where, " AND ")+` ORDER BY `+order+` LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*row
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	page := &ListPage{Items: []ListItem{}, Counts: map[string]int{}}
	if len(list) > limit {
		list = list[:limit]
		page.NextCursor = paging.Encode(list[limit-1].id)
	}
	var areaIDs, pids []uint64
	for _, r := range list {
		pids = append(pids, r.id)
		if r.baseAreaID != nil {
			areaIDs = append(areaIDs, *r.baseAreaID)
		}
	}
	areas, err := s.locations.ByInternal(ctx, s.db, locations.Area, areaIDs)
	if err != nil {
		return nil, err
	}
	claimed := map[uint64]bool{}
	if len(pids) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(pids)), ",")
		cargs := make([]any, len(pids))
		for i, id := range pids {
			cargs[i] = id
		}
		crows, err := s.db.QueryContext(ctx, `SELECT DISTINCT provider_id FROM provider_claims WHERE claimed_at IS NOT NULL AND provider_id IN (`+ph+`)`, cargs...)
		if err != nil {
			return nil, err
		}
		for crows.Next() {
			var id uint64
			if err := crows.Scan(&id); err != nil {
				crows.Close()
				return nil, err
			}
			claimed[id] = true
		}
		crows.Close()
	}
	for _, r := range list {
		it := ListItem{ID: r.publicID.String(), DisplayName: r.displayName, Type: r.providerType, Slug: r.slug, Status: r.status,
			Completeness: r.completeness, Assisted: r.createdBy != nil, Claimed: claimed[r.id], SubmittedAt: r.submittedAt, UpdatedAt: r.updatedAt}
		if r.baseAreaID != nil {
			if a, ok := areas[*r.baseAreaID]; ok {
				it.BaseArea = &a.Label
			}
		}
		page.Items = append(page.Items, it)
	}
	crow, err := s.db.QueryContext(ctx, `SELECT listing_status, COUNT(*) FROM providers GROUP BY listing_status`)
	if err != nil {
		return nil, err
	}
	defer crow.Close()
	for crow.Next() {
		var st string
		var n int
		if err := crow.Scan(&st, &n); err != nil {
			return nil, err
		}
		page.Counts[st] = n
	}
	return page, crow.Err()
}

// Dashboard is the owner's overview.
type Dashboard struct {
	Listing *View          `json:"listing"`
	Views30 int            `json:"profile_views_30d"`
	Reveals map[string]int `json:"contact_reveals_30d"`
}

// OwnerDashboard returns the owner's listing plus 30-day counters.
func (s *Service) OwnerDashboard(ctx context.Context) (*Dashboard, error) {
	v, err := s.Get(ctx, Target{})
	if err != nil {
		return nil, err
	}
	d := &Dashboard{Listing: v, Reveals: map[string]int{}}
	rows, err := s.db.QueryContext(ctx, `SELECT event_type, COUNT(*) FROM analytics_events WHERE provider_id = ? AND created_at >= ? GROUP BY event_type`,
		v.row.id, time.Now().UTC().Add(-30*24*time.Hour))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ev string
		var n int
		if err := rows.Scan(&ev, &n); err != nil {
			return nil, err
		}
		if ev == "provider_profile_view" {
			d.Views30 = n
		} else {
			d.Reveals[strings.TrimPrefix(ev, "provider_")] = n
		}
	}
	return d, rows.Err()
}
