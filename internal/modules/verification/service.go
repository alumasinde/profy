// Package verification owns provider verification badges (plan §12.1): independent,
// evidenced claims — never a single "verified" flag. Contact badges come from OTP; identity,
// business and licence badges come from staff review of private documents.
package verification

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/alumasinde/profy/internal/authz"
	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/jobsqueue"
	"github.com/alumasinde/profy/internal/modules/audit"
	"github.com/alumasinde/profy/internal/modules/media"
	"github.com/alumasinde/profy/internal/modules/moderation"
	"github.com/alumasinde/profy/internal/modules/notifications"
	"github.com/alumasinde/profy/internal/shared/ids"
	"github.com/alumasinde/profy/internal/shared/reqctx"
)

// Badge types (meanings fixed by code; labels are data).
const (
	PhoneVerified      = "phone_verified"
	EmailVerified      = "email_verified"
	IdentityChecked    = "identity_checked"
	BusinessRegistered = "business_registered"
	LicenceChecked     = "licence_checked"
)

// Statuses.
const (
	StatusPending  = "pending"
	StatusVerified = "verified"
	StatusRejected = "rejected"
	StatusExpired  = "expired"
)

// DocumentTypes accepted per document-reviewed badge.
var DocumentTypes = map[string][]string{
	IdentityChecked:    {"national_id", "passport", "alien_id"},
	BusinessRegistered: {"business_registration", "kra_pin"},
	LicenceChecked:     {"licence"},
}

// Jobs and retention.
const (
	JobExpire        = "verification.expire"
	RetainRejected   = 30 * 24 * time.Hour // rejected documents (§16.2 proposed)
	MaxDocsPerSubmit = 3
)

// ProviderRef is what this module needs to know about a provider.
type ProviderRef struct {
	InternalID  uint64
	PublicID    string
	DisplayName string
	Type        string
	OwnerUserID *uint64
	OwnerName   string
}

// LicenceService is a provider-offered service that requires a licence.
type LicenceService struct {
	InternalID uint64 `json:"-"`
	ID         string `json:"id"`
	Name       string `json:"name"`
}

// Providers is implemented by the providers module.
type Providers interface {
	OwnProvider(ctx context.Context) (*ProviderRef, error)
	ProviderRefs(ctx context.Context, ex database.Execer, idList []uint64) (map[uint64]*ProviderRef, error)
	LicenceServices(ctx context.Context, ex database.Execer, providerID uint64) ([]LicenceService, error)
}

// Service implements verification.
type Service struct {
	db        *sql.DB
	authz     *authz.Service
	media     *media.Service
	Providers Providers
}

// New builds the service.
func New(db *sql.DB, az *authz.Service, m *media.Service) *Service {
	return &Service{db: db, authz: az, media: m}
}

// Badge is a currently valid badge for display.
type Badge struct {
	Type        string     `json:"type"`
	Label       string     `json:"label"`
	Description string     `json:"description"`
	ServiceName string     `json:"service_name,omitempty"`
	VerifiedAt  *time.Time `json:"verified_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
}

// SetContactBadge grants or removes an OTP-backed contact badge inside tx.
func (s *Service) SetContactBadge(ctx context.Context, tx *sql.Tx, providerID uint64, badge string, verified bool) error {
	if !verified {
		_, err := tx.ExecContext(ctx, `DELETE FROM provider_verifications WHERE provider_id = ? AND badge_type = ? AND service_id IS NULL`, providerID, badge)
		return err
	}
	now := time.Now().UTC()
	_, err := tx.ExecContext(ctx, `INSERT INTO provider_verifications (public_id, provider_id, badge_type, status, verified_at)
		VALUES (?, ?, ?, 'verified', ?) AS new ON DUPLICATE KEY UPDATE status = 'verified', verified_at = new.verified_at,
		rejection_reason = NULL, expires_at = NULL`, ids.New(), providerID, badge, now)
	return err
}

// Badges returns valid badges per provider (verified and not expired), ordered by type.
func (s *Service) Badges(ctx context.Context, ex database.Execer, providerIDs []uint64) (map[uint64][]Badge, error) {
	out := map[uint64][]Badge{}
	if len(providerIDs) == 0 {
		return out, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(providerIDs)), ",")
	args := []any{time.Now().UTC()}
	for _, id := range providerIDs {
		args = append(args, id)
	}
	rows, err := ex.QueryContext(ctx, `SELECT v.provider_id, v.badge_type, COALESCE(t.label, v.badge_type), COALESCE(t.description, ''),
			COALESCE(s.name, ''), v.verified_at, v.expires_at
		FROM provider_verifications v
		LEFT JOIN verification_badge_types t ON t.badge_type = v.badge_type
		LEFT JOIN services s ON s.id = v.service_id
		WHERE v.status = 'verified' AND (v.expires_at IS NULL OR v.expires_at > ?) AND v.provider_id IN (`+ph+`)
		ORDER BY COALESCE(t.sort_order, 99), s.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pid uint64
		var b Badge
		var va, ea sql.NullTime
		if err := rows.Scan(&pid, &b.Type, &b.Label, &b.Description, &b.ServiceName, &va, &ea); err != nil {
			return nil, err
		}
		if va.Valid {
			b.VerifiedAt = &va.Time
		}
		if ea.Valid {
			b.ExpiresAt = &ea.Time
		}
		out[pid] = append(out[pid], b)
	}
	return out, rows.Err()
}

// BadgeType is a badge definition with its public wording.
type BadgeType struct {
	Type        string `json:"type"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// Types returns badge definitions (for "What these badges mean").
func (s *Service) Types(ctx context.Context) ([]BadgeType, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT badge_type, label, description FROM verification_badge_types ORDER BY sort_order`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BadgeType{}
	for rows.Next() {
		var b BadgeType
		if err := rows.Scan(&b.Type, &b.Label, &b.Description); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Record is one verification row (owner and staff views).
type Record struct {
	ID              string       `json:"id"`
	BadgeType       string       `json:"badge_type"`
	Label           string       `json:"label"`
	ServiceID       *string      `json:"service_id"`
	ServiceName     string       `json:"service_name,omitempty"`
	Status          string       `json:"status"`
	SubmittedAt     *time.Time   `json:"submitted_at"`
	VerifiedAt      *time.Time   `json:"verified_at"`
	ExpiresAt       *time.Time   `json:"expires_at"`
	RejectionReason *string      `json:"rejection_reason"`
	Documents       []Document   `json:"documents,omitempty"`
	Provider        *ProviderRow `json:"provider,omitempty"`
	internalID      uint64
	providerID      uint64
}

// Document is a submitted private file.
type Document struct {
	ID           string `json:"id"`
	DocumentType string `json:"document_type"`
	MediaID      string `json:"media_id"`
	ContentType  string `json:"content_type"`
	DownloadURL  string `json:"download_url,omitempty"` // staff only
}

// ProviderRow is the provider summary in staff views.
type ProviderRow struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Type        string `json:"provider_type"`
	OwnerName   string `json:"owner_name"`
}

const recCols = `v.id, v.public_id, v.provider_id, v.badge_type, COALESCE(t.label, v.badge_type), s.public_id, COALESCE(s.name, ''),
	v.status, v.submitted_at, v.verified_at, v.expires_at, v.rejection_reason
	FROM provider_verifications v
	LEFT JOIN verification_badge_types t ON t.badge_type = v.badge_type
	LEFT JOIN services s ON s.id = v.service_id`

func scanRecord(row interface{ Scan(...any) error }) (*Record, error) {
	var r Record
	var pub ids.UUID
	var spub []byte
	var sa, va, ea sql.NullTime
	var reason sql.NullString
	err := row.Scan(&r.internalID, &pub, &r.providerID, &r.BadgeType, &r.Label, &spub, &r.ServiceName, &r.Status, &sa, &va, &ea, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, response.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.ID = pub.String()
	if len(spub) == 16 {
		var u ids.UUID
		copy(u[:], spub)
		x := u.String()
		r.ServiceID = &x
	}
	if sa.Valid {
		r.SubmittedAt = &sa.Time
	}
	if va.Valid {
		r.VerifiedAt = &va.Time
	}
	if ea.Valid {
		r.ExpiresAt = &ea.Time
		if r.Status == StatusVerified && ea.Time.Before(time.Now()) {
			r.Status = StatusExpired
		}
	}
	if reason.Valid {
		r.RejectionReason = &reason.String
	}
	return &r, nil
}

func (s *Service) documents(ctx context.Context, ex database.Execer, verificationID uint64, staff bool) ([]Document, error) {
	rows, err := ex.QueryContext(ctx, `SELECT d.public_id, d.document_type, m.public_id, m.content_type FROM verification_documents d
		JOIN media_files m ON m.id = d.media_id WHERE d.verification_id = ? AND m.status <> 'deleted' ORDER BY d.id`, verificationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Document{}
	for rows.Next() {
		var d Document
		var pub, mpub ids.UUID
		if err := rows.Scan(&pub, &d.DocumentType, &mpub, &d.ContentType); err != nil {
			return nil, err
		}
		d.ID, d.MediaID = pub.String(), mpub.String()
		if staff {
			d.DownloadURL = "/api/v1/admin/media/" + d.MediaID + "/download"
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// OwnerOverview lists a provider's badges (all states) plus what can still be requested.
type OwnerOverview struct {
	Records         []Record            `json:"records"`
	Requestable     []string            `json:"requestable"`
	LicenceServices []LicenceService    `json:"licence_services"`
	DocumentTypes   map[string][]string `json:"document_types"`
}

// ForOwner builds the owner's verification overview.
func (s *Service) ForOwner(ctx context.Context) (*OwnerOverview, error) {
	prov, err := s.Providers.OwnProvider(ctx)
	if err != nil {
		return nil, err
	}
	return s.overview(ctx, prov)
}

func (s *Service) overview(ctx context.Context, prov *ProviderRef) (*OwnerOverview, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+recCols+` WHERE v.provider_id = ? ORDER BY v.badge_type, s.name`, prov.InternalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ov := &OwnerOverview{Records: []Record{}, DocumentTypes: DocumentTypes}
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		ov.Records = append(ov.Records, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range ov.Records {
		if ov.Records[i].Documents, err = s.documents(ctx, s.db, ov.Records[i].internalID, false); err != nil {
			return nil, err
		}
	}
	ov.Requestable = []string{IdentityChecked}
	if prov.Type == "business" {
		ov.Requestable = append(ov.Requestable, BusinessRegistered)
	}
	if ov.LicenceServices, err = s.Providers.LicenceServices(ctx, s.db, prov.InternalID); err != nil {
		return nil, err
	}
	if len(ov.LicenceServices) > 0 {
		ov.Requestable = append(ov.Requestable, LicenceChecked)
	}
	return ov, nil
}

// SubmitFile is one uploaded document.
type SubmitFile struct {
	DocumentType string
	Name         string
	Data         []byte
}

// Submit requests a document-reviewed badge for the caller's provider.
func (s *Service) Submit(ctx context.Context, badge, servicePublicID string, files []SubmitFile) (*OwnerOverview, error) {
	prov, err := s.Providers.OwnProvider(ctx)
	if err != nil {
		return nil, err
	}
	allowedDocs, ok := DocumentTypes[badge]
	errs := map[string]string{}
	if !ok {
		errs["badge_type"] = "invalid"
	} else if badge == BusinessRegistered && prov.Type != "business" {
		errs["badge_type"] = "invalid"
	}
	if len(files) == 0 || len(files) > MaxDocsPerSubmit {
		errs["files"] = "required"
	}
	for _, f := range files {
		if !slices.Contains(allowedDocs, f.DocumentType) {
			errs["document_type"] = "invalid"
		}
	}
	var serviceID *uint64
	if badge == LicenceChecked {
		lic, err := s.Providers.LicenceServices(ctx, s.db, prov.InternalID)
		if err != nil {
			return nil, err
		}
		i := slices.IndexFunc(lic, func(l LicenceService) bool { return l.ID == servicePublicID })
		if i < 0 {
			errs["service_id"] = "invalid"
		} else {
			serviceID = &lic[i].InternalID
		}
	}
	if len(errs) > 0 {
		return nil, response.Validation(errs)
	}
	p := reqctx.PrincipalFrom(ctx)
	err = database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		var vid uint64
		var status string
		var expires sql.NullTime
		err := tx.QueryRowContext(ctx, `SELECT id, status, expires_at FROM provider_verifications
			WHERE provider_id = ? AND badge_type = ? AND service_key = COALESCE(?, 0) FOR UPDATE`, prov.InternalID, badge, serviceID).
			Scan(&vid, &status, &expires)
		now := time.Now().UTC()
		switch {
		case errors.Is(err, sql.ErrNoRows):
			res, err := tx.ExecContext(ctx, `INSERT INTO provider_verifications (public_id, provider_id, badge_type, service_id, status,
				submitted_by, submitted_at) VALUES (?, ?, ?, ?, 'pending', ?, ?)`, ids.New(), prov.InternalID, badge, serviceID, p.UserID, now)
			if err != nil {
				return err
			}
			id, _ := res.LastInsertId()
			vid = uint64(id)
		case err != nil:
			return err
		case status == StatusPending:
			return response.Conflict(response.CodeConflict, "This badge is already waiting for review.")
		case status == StatusVerified && (!expires.Valid || expires.Time.After(now.Add(30*24*time.Hour))):
			return response.Conflict(response.CodeConflict, "This badge is already verified.")
		default: // rejected, expired or expiring soon: resubmit and discard old documents
			if err := s.discardDocuments(ctx, tx, vid); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE provider_verifications SET status = 'pending', submitted_by = ?, submitted_at = ?,
				rejection_reason = NULL WHERE id = ?`, p.UserID, now, vid); err != nil {
				return err
			}
		}
		for _, f := range files {
			m, err := s.media.Upload(ctx, tx, media.UploadInput{OwnerUserID: p.UserID, ProviderID: prov.InternalID,
				Purpose: media.PurposeDocument, Data: f.Data, Name: f.Name})
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO verification_documents (public_id, verification_id, media_id, document_type)
				VALUES (?, ?, ?, ?)`, ids.New(), vid, m.InternalID, f.DocumentType); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "verification.submitted", EntityType: "provider", EntityID: prov.PublicID,
			NewValues: map[string]any{"badge": badge, "documents": len(files)}})
	})
	if err != nil {
		return nil, err
	}
	return s.overview(ctx, prov)
}

// discardDocuments soft-deletes a verification's documents (files purged in the background).
func (s *Service) discardDocuments(ctx context.Context, tx *sql.Tx, verificationID uint64) error {
	rows, err := tx.QueryContext(ctx, `SELECT media_id FROM verification_documents WHERE verification_id = ?`, verificationID)
	if err != nil {
		return err
	}
	var mids []uint64
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		mids = append(mids, id)
	}
	rows.Close()
	for _, id := range mids {
		if err := s.media.MarkDeleted(ctx, tx, id); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM verification_documents WHERE verification_id = ?`, verificationID)
	return err
}

// ---- Staff review ---------------------------------------------------------------------

// Queue lists document-reviewed verifications by status (default pending), oldest first.
func (s *Service) Queue(ctx context.Context, status string) ([]Record, error) {
	if status == "" {
		status = StatusPending
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+recCols+` WHERE v.status = ? AND v.badge_type IN ('identity_checked','business_registered','licence_checked')
		ORDER BY v.submitted_at LIMIT 200`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	var pids []uint64
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
		pids = append(pids, r.providerID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	refs, err := s.Providers.ProviderRefs(ctx, s.db, pids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if ref := refs[out[i].providerID]; ref != nil {
			out[i].Provider = &ProviderRow{ID: ref.PublicID, DisplayName: ref.DisplayName, Type: ref.Type, OwnerName: ref.OwnerName}
		}
	}
	return out, nil
}

// Get loads one verification with documents and provider (staff view).
func (s *Service) Get(ctx context.Context, ex database.Execer, publicID string, lock bool) (*Record, error) {
	pub, err := ids.Parse(publicID)
	if err != nil {
		return nil, response.ErrNotFound
	}
	q := `SELECT ` + recCols + ` WHERE v.public_id = ?`
	if lock {
		q += ` FOR UPDATE OF v`
	}
	r, err := scanRecord(ex.QueryRowContext(ctx, q, pub))
	if err != nil {
		return nil, err
	}
	if r.Documents, err = s.documents(ctx, ex, r.internalID, true); err != nil {
		return nil, err
	}
	refs, err := s.Providers.ProviderRefs(ctx, ex, []uint64{r.providerID})
	if err != nil {
		return nil, err
	}
	if ref := refs[r.providerID]; ref != nil {
		r.Provider = &ProviderRow{ID: ref.PublicID, DisplayName: ref.DisplayName, Type: ref.Type, OwnerName: ref.OwnerName}
	}
	return r, nil
}

// DecisionInput is a staff decision.
type DecisionInput struct {
	Decision  string     `json:"decision"` // approve | reject
	Reason    string     `json:"reason"`
	ExpiresAt *time.Time `json:"expires_at"` // required for licences
}

// Decide approves (providers.verify) or rejects (providers.reject_verification) a request.
func (s *Service) Decide(ctx context.Context, publicID string, in DecisionInput) (*Record, error) {
	p := reqctx.PrincipalFrom(ctx)
	perm := map[string]string{"approve": "providers.verify", "reject": "providers.reject_verification"}[in.Decision]
	if perm == "" {
		return nil, response.Validation(map[string]string{"decision": "invalid"})
	}
	if !s.authz.Can(ctx, p, perm) {
		return nil, response.ErrForbidden
	}
	reason := strings.TrimSpace(in.Reason)
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		r, err := s.Get(ctx, tx, publicID, true)
		if err != nil {
			return err
		}
		if r.Status != StatusPending {
			return response.Conflict(response.CodeConflict, "Only pending requests can be decided.")
		}
		now := time.Now().UTC()
		errs := map[string]string{}
		if in.Decision == "reject" && (reason == "" || len(reason) > 500) {
			errs["reason"] = "required"
		}
		if in.Decision == "approve" && r.BadgeType == LicenceChecked && (in.ExpiresAt == nil || !in.ExpiresAt.After(now)) {
			errs["expires_at"] = "required"
		}
		if in.ExpiresAt != nil && !in.ExpiresAt.After(now) {
			errs["expires_at"] = "invalid"
		}
		if len(errs) > 0 {
			return response.Validation(errs)
		}
		if in.Decision == "approve" {
			_, err = tx.ExecContext(ctx, `UPDATE provider_verifications SET status = 'verified', verified_by = ?, verified_at = ?,
				expires_at = ?, rejection_reason = NULL WHERE id = ?`, p.UserID, now, in.ExpiresAt, r.internalID)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE provider_verifications SET status = 'rejected', verified_by = ?, verified_at = NULL,
				rejection_reason = ? WHERE id = ?`, p.UserID, reason, r.internalID)
		}
		if err != nil {
			return err
		}
		if err := moderation.Record(ctx, tx, "verification", r.ID, "verification."+in.Decision, reason); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Entry{Action: "verification." + in.Decision + "d", EntityType: "verification", EntityID: r.ID,
			NewValues: map[string]any{"badge": r.BadgeType, "reason": reason, "provider": r.Provider.ID}}); err != nil {
			return err
		}
		return jobsqueue.Enqueue(ctx, tx, notifications.JobProviderNotify, notifications.ProviderNotice{
			ProviderID: r.providerID, Event: "verification_" + in.Decision + "d", Detail: r.Label, Reason: reason,
		}, jobsqueue.Options{})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, s.db, publicID, false)
}

// RegisterJobs registers the daily expiry/retention job.
func (s *Service) RegisterJobs(q *jobsqueue.Queue) {
	q.Handle(JobExpire, func(ctx context.Context, _ json.RawMessage) error {
		now := time.Now().UTC()
		return database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
			// Expired evidence removes the badge (§12.1).
			if _, err := tx.ExecContext(ctx, `UPDATE provider_verifications SET status = 'expired'
				WHERE status = 'verified' AND expires_at IS NOT NULL AND expires_at <= ?`, now); err != nil {
				return err
			}
			// Rejected documents are kept 30 days (§16.2 proposed).
			rows, err := tx.QueryContext(ctx, `SELECT id FROM provider_verifications WHERE status = 'rejected' AND updated_at < ?`, now.Add(-RetainRejected))
			if err != nil {
				return err
			}
			var vids []uint64
			for rows.Next() {
				var id uint64
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				vids = append(vids, id)
			}
			rows.Close()
			for _, id := range vids {
				if err := s.discardDocuments(ctx, tx, id); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

// ---- HTTP -----------------------------------------------------------------------------

// Handler exposes provider and staff verification endpoints.
type Handler struct{ svc *Service }

// NewHandler builds the handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts routes.
func (h *Handler) Register(mux *http.ServeMux, prefix string) {
	az := h.svc.authz
	mux.HandleFunc("GET "+prefix+"/verification/badge-types", h.types)
	mux.Handle("GET "+prefix+"/provider/verifications", authz.RequireAuth(http.HandlerFunc(h.own)))
	mux.Handle("POST "+prefix+"/provider/verifications", authz.RequireAuth(http.HandlerFunc(h.submit)))
	mux.Handle("GET "+prefix+"/admin/verifications", az.Require("providers.view_any")(http.HandlerFunc(h.queue)))
	mux.Handle("GET "+prefix+"/admin/verifications/{id}", az.Require("providers.view_any")(http.HandlerFunc(h.get)))
	mux.Handle("PATCH "+prefix+"/admin/verifications/{id}", az.Require("providers.view_any")(http.HandlerFunc(h.decide)))
}

func (h *Handler) types(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Types(r.Context())
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, list)
}

func (h *Handler) own(w http.ResponseWriter, r *http.Request) {
	ov, err := h.svc.ForOwner(r.Context())
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, ov)
}

// submit accepts multipart: badge_type, service_id (licence), document_type, files (1–3).
func (h *Handler) submit(w http.ResponseWriter, r *http.Request) {
	max := h.svc.media.MaxUploadBytes(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, max*MaxDocsPerSubmit+1<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		response.Fail(w, r, response.Validation(map[string]string{"file": "too_large"}))
		return
	}
	defer r.MultipartForm.RemoveAll()
	docType := r.FormValue("document_type")
	var files []SubmitFile
	for _, fh := range r.MultipartForm.File["files"] {
		if fh.Size > max {
			response.Fail(w, r, response.Validation(map[string]string{"file": "too_large"}))
			return
		}
		f, err := fh.Open()
		if err != nil {
			response.Fail(w, r, err)
			return
		}
		data, err := readAll(f, max)
		f.Close()
		if err != nil {
			response.Fail(w, r, response.Validation(map[string]string{"file": "too_large"}))
			return
		}
		files = append(files, SubmitFile{DocumentType: docType, Name: fh.Filename, Data: data})
	}
	ov, err := h.svc.Submit(r.Context(), r.FormValue("badge_type"), r.FormValue("service_id"), files)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, ov, nil)
}

func (h *Handler) queue(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Queue(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, list)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	rec, err := h.svc.Get(r.Context(), h.svc.db, r.PathValue("id"), false)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, rec)
}

func (h *Handler) decide(w http.ResponseWriter, r *http.Request) {
	var in DecisionInput
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	rec, err := h.svc.Decide(r.Context(), r.PathValue("id"), in)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, rec)
}
