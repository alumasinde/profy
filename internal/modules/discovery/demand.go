package discovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/jobsqueue"
	"github.com/alumasinde/profy/internal/modules/analytics"
	"github.com/alumasinde/profy/internal/modules/audit"
	"github.com/alumasinde/profy/internal/security"
	"github.com/alumasinde/profy/internal/shared/ids"
	"github.com/alumasinde/profy/internal/shared/paging"
	"github.com/alumasinde/profy/internal/shared/reqctx"
	"github.com/alumasinde/profy/internal/shared/text"
)

// Demand-request rules (§3.2): a lightweight recruitment signal, not a job.
const (
	JobPurgeDemandContacts = "discovery.purge_demand_contacts"
	DemandContactRetention = 90 * 24 * time.Hour
)

var demandLimit = security.Limit{Max: 5, Window: time.Hour}

// DemandInput is a "request a provider" submission.
type DemandInput struct {
	Service        string `json:"service"` // service slug, optional
	ServiceText    string `json:"service_text"`
	Location       string `json:"location"` // location slug, optional
	LocationText   string `json:"location_text"`
	Notes          string `json:"notes"`
	ContactChannel string `json:"contact_channel"` // sms | email | "" (no contact)
	ContactValue   string `json:"contact_value"`
	ContactConsent bool   `json:"contact_consent"`
}

// CreateDemand stores a request. Contact details are optional and require consent; they are
// cleared after the retention period. Rate-limited per client.
func (s *Service) CreateDemand(ctx context.Context, in DemandInput) (string, error) {
	if ok, retry := s.limiter.Allow("demand:"+reqctx.ClientIP(ctx), demandLimit); !ok {
		return "", response.RateLimited(retry)
	}
	snap, err := s.Snapshot(ctx)
	if err != nil {
		return "", err
	}
	errs := map[string]string{}
	var serviceID any
	svcText := strings.TrimSpace(in.ServiceText)
	if sv := snap.Service(in.Service); sv != nil {
		serviceID, svcText = sv.ID, sv.Name
	}
	var locType, locID any
	locText := strings.TrimSpace(in.LocationText)
	if l := snap.Resolved(snap.Location(in.Location)); l != nil && l.Selectable {
		locType, locID, locText = l.Type, l.ID, l.Label
	}
	if serviceID == nil && svcText == "" {
		errs["service_text"] = "required"
	}
	if len([]rune(svcText)) > 120 {
		errs["service_text"] = "too_long"
	}
	if len([]rune(locText)) > 120 {
		errs["location_text"] = "too_long"
	}
	notes, ok := text.CleanText(in.Notes, 500)
	if !ok {
		errs["notes"] = "too_long"
	}
	var channel, contact any
	if in.ContactChannel != "" || strings.TrimSpace(in.ContactValue) != "" {
		var v string
		var valid bool
		switch in.ContactChannel {
		case "sms":
			v, valid = security.NormalizeKenyanPhone(in.ContactValue)
		case "email":
			v, valid = security.NormalizeEmail(in.ContactValue)
		}
		if !valid {
			errs["contact_value"] = "invalid"
		} else if !in.ContactConsent {
			errs["contact_consent"] = "required"
		}
		channel, contact = in.ContactChannel, v
	}
	if len(errs) > 0 {
		return "", response.Validation(errs)
	}
	var user any
	if p := reqctx.PrincipalFrom(ctx); p != nil {
		user = p.UserID
	}
	pub := ids.New()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO demand_requests (public_id, service_id, service_text, location_type, location_id,
		location_text, notes, contact_channel, contact_value, user_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		pub, serviceID, nullable(svcText), locType, locID, nullable(locText), nullable(notes), channel, contact, user); err != nil {
		return "", err
	}
	props := map[string]string{}
	if sv := snap.Service(in.Service); sv != nil {
		props["service"] = sv.Slug
	}
	if l := snap.Location(in.Location); l != nil {
		props["location"] = l.Slug
	}
	if err := analytics.Record(ctx, s.db, analytics.DemandRequested, 0, props); err != nil {
		s.log.WarnContext(ctx, "analytics", "error", err)
	}
	return pub.String(), nil
}

func nullable(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

// demandForm handles the no-JavaScript form on zero-result pages and redirects back.
func (h *PublicHandler) demandForm(w http.ResponseWriter, r *http.Request) {
	in := DemandInput{Service: r.PostFormValue("service"), ServiceText: r.PostFormValue("service_text"),
		Location: r.PostFormValue("location"), LocationText: r.PostFormValue("location_text"), Notes: r.PostFormValue("notes"),
		ContactChannel: r.PostFormValue("contact_channel"), ContactValue: r.PostFormValue("contact_value"),
		ContactConsent: r.PostFormValue("contact_consent") == "1"}
	if strings.TrimSpace(in.ContactValue) == "" {
		in.ContactChannel = ""
	}
	back := r.PostFormValue("return")
	if !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") || strings.HasPrefix(back, "/\\") {
		back = "/search"
	}
	u, err := url.Parse(back)
	if err != nil {
		u = &url.URL{Path: "/search"}
	}
	q := u.Query()
	q.Del("requested")
	q.Del("request_error")
	if _, err := h.svc.CreateDemand(r.Context(), in); err != nil {
		var e *response.Error
		if !errors.As(err, &e) {
			response.Fail(w, r, err)
			return
		}
		q.Set("request_error", "1")
	} else {
		q.Set("requested", "1")
	}
	u.RawQuery = q.Encode()
	u.Fragment = "request"
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}

// ---- Staff ----------------------------------------------------------------------------

// Demand is a stored request for staff.
type Demand struct {
	ID             string    `json:"id"`
	ServiceID      string    `json:"service_id,omitempty"`
	ServiceText    string    `json:"service_text"`
	LocationText   string    `json:"location_text"`
	Notes          string    `json:"notes,omitempty"`
	ContactChannel string    `json:"contact_channel,omitempty"`
	ContactValue   string    `json:"contact_value,omitempty"`
	ContactPurged  bool      `json:"contact_purged"`
	Status         string    `json:"status"`
	StaffNote      string    `json:"staff_note,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	internalID     uint64
}

var demandStatuses = map[string]bool{"new": true, "in_progress": true, "fulfilled": true, "closed": true}

// ListDemand pages requests for staff (demand_requests.view). Viewing contact details is
// audited because they are personal data.
func (s *Service) ListDemand(ctx context.Context, status, cursor string, limit int) ([]Demand, string, error) {
	after, ok := paging.Decode(cursor)
	if !ok {
		return nil, "", response.Validation(map[string]string{"cursor": "invalid"})
	}
	limit = paging.Limit(limit, 25, 100)
	where, args := "1=1", []any{}
	if status != "" {
		if !demandStatuses[status] {
			return nil, "", response.Validation(map[string]string{"status": "invalid"})
		}
		where, args = "d.status = ?", append(args, status)
	}
	if after > 0 {
		where += " AND d.id < ?"
		args = append(args, after)
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `SELECT d.id, d.public_id, s.public_id, COALESCE(d.service_text, ''), COALESCE(d.location_text, ''),
		COALESCE(d.notes, ''), COALESCE(d.contact_channel, ''), COALESCE(d.contact_value, ''), d.contact_purged_at IS NOT NULL,
		d.status, COALESCE(d.staff_note, ''), d.created_at
		FROM demand_requests d LEFT JOIN services s ON s.id = d.service_id WHERE `+where+` ORDER BY d.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []Demand
	contacts := 0
	for rows.Next() {
		var d Demand
		var pub ids.UUID
		var spub []byte
		if err := rows.Scan(&d.internalID, &pub, &spub, &d.ServiceText, &d.LocationText, &d.Notes, &d.ContactChannel,
			&d.ContactValue, &d.ContactPurged, &d.Status, &d.StaffNote, &d.CreatedAt); err != nil {
			return nil, "", err
		}
		d.ID = pub.String()
		if len(spub) == 16 {
			var u ids.UUID
			copy(u[:], spub)
			d.ServiceID = u.String()
		}
		if d.ContactValue != "" {
			contacts++
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = paging.Encode(out[limit-1].internalID)
	}
	if contacts > 0 {
		_ = audit.Record(ctx, s.db, audit.Entry{Action: "demand_requests.contacts.viewed", EntityType: "demand_request",
			EntityID: "list", NewValues: map[string]any{"with_contact": contacts, "status": status}})
	}
	if out == nil {
		out = []Demand{}
	}
	return out, next, nil
}

// UpdateDemand changes status and the staff note (demand_requests.update).
func (s *Service) UpdateDemand(ctx context.Context, publicID, status, note string) error {
	if !demandStatuses[status] {
		return response.Validation(map[string]string{"status": "invalid"})
	}
	n, ok := text.CleanText(note, 500)
	if !ok {
		return response.Validation(map[string]string{"staff_note": "too_long"})
	}
	pub, err := ids.Parse(publicID)
	if err != nil {
		return response.ErrNotFound
	}
	p := reqctx.PrincipalFrom(ctx)
	return database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE demand_requests SET status = ?, staff_note = ?, handled_by = ? WHERE public_id = ?`,
			status, nullable(n), p.UserID, pub)
		if err != nil {
			return err
		}
		if c, _ := res.RowsAffected(); c == 0 {
			var exists int
			_ = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM demand_requests WHERE public_id = ?`, pub).Scan(&exists)
			if exists == 0 {
				return response.ErrNotFound
			}
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "demand_requests.updated", EntityType: "demand_request",
			EntityID: publicID, NewValues: map[string]any{"status": status}})
	})
}

// purgeDemandContacts clears contact details older than the retention period.
func (s *Service) purgeDemandContacts(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE demand_requests SET contact_value = NULL, contact_channel = NULL, contact_purged_at = UTC_TIMESTAMP(3)
		WHERE contact_value IS NOT NULL AND created_at < ?`, time.Now().UTC().Add(-DemandContactRetention))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RegisterDemandJobs registers the daily contact purge.
func (s *Service) RegisterDemandJobs(q *jobsqueue.Queue) {
	q.Handle(JobPurgeDemandContacts, func(ctx context.Context, _ json.RawMessage) error {
		n, err := s.purgeDemandContacts(ctx)
		if err == nil && n > 0 {
			s.log.InfoContext(ctx, "demand contacts purged", "rows", n)
		}
		return err
	})
}
