// Package media stores uploads outside MySQL with metadata and ownership in media_files
// (plan §17): content-type detection, re-encoding with EXIF stripping, responsive sizes,
// private verification documents, moderation and deferred file deletion.
package media

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alumasinde/profy/internal/authz"
	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/jobsqueue"
	"github.com/alumasinde/profy/internal/modules/audit"
	"github.com/alumasinde/profy/internal/modules/moderation"
	"github.com/alumasinde/profy/internal/modules/settings"
	"github.com/alumasinde/profy/internal/shared/ids"
	"github.com/alumasinde/profy/internal/shared/reqctx"
	"github.com/alumasinde/profy/internal/storage"
)

// Purposes.
const (
	PurposeProfile   = "profile"
	PurposeCover     = "cover"
	PurposePortfolio = "portfolio"
	PurposeDocument  = "verification_document"
)

// Statuses.
const (
	StatusActive   = "active"
	StatusPending  = "pending_moderation"
	StatusRejected = "rejected"
	StatusDeleted  = "deleted"
)

// JobPurge deletes files of deleted media in the background.
const JobPurge = "media.purge"

// Media is the API view of a file.
type Media struct {
	ID          string         `json:"id"`
	Purpose     string         `json:"purpose"`
	Status      string         `json:"status"`
	ContentType string         `json:"content_type"`
	Bytes       int64          `json:"bytes"`
	Width       *int           `json:"width,omitempty"`
	Height      *int           `json:"height,omitempty"`
	URL         string         `json:"url,omitempty"`  // largest public variant
	URLs        map[int]string `json:"urls,omitempty"` // width → URL
	Note        *string        `json:"moderation_note,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	InternalID  uint64         `json:"-"`
	ProviderID  *uint64        `json:"-"`
	OwnerUserID *uint64        `json:"-"`
	storageKey  string
	variants    []int
	visibility  string
}

// Service implements media handling.
type Service struct {
	db       *sql.DB
	authz    *authz.Service
	settings *settings.Service
	public   storage.Store
	private  storage.Store
	log      *slog.Logger
}

// New builds the service.
func New(db *sql.DB, az *authz.Service, st *settings.Service, public, private storage.Store, log *slog.Logger) *Service {
	return &Service{db: db, authz: az, settings: st, public: public, private: private, log: log}
}

// RegisterJobs registers background handlers.
func (s *Service) RegisterJobs(q *jobsqueue.Queue) {
	q.Handle(JobPurge, func(ctx context.Context, payload json.RawMessage) error {
		var p struct {
			Keys    []string `json:"keys"`
			Private bool     `json:"private"`
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			return err
		}
		store := s.public
		if p.Private {
			store = s.private
		}
		for _, k := range p.Keys {
			if err := store.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
}

// MaxUploadBytes is the configured limit, never above the code floor.
func (s *Service) MaxUploadBytes(ctx context.Context) int64 {
	mb := s.settings.Int(ctx, settings.KeyUploadMaxMB)
	if mb < 1 {
		mb = 8
	}
	if n := int64(mb) << 20; n < AbsMaxUpload {
		return n
	}
	return AbsMaxUpload
}

func randomPrefix(kind string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	h := hex.EncodeToString(b)
	return kind + "/" + h[:2] + "/" + h
}

// variantKey is the stored key of one width.
func variantKey(prefix string, w int) string { return fmt.Sprintf("%s/w%d.jpg", prefix, w) }

// PublicURL is the URL of a public variant.
func PublicURL(id string, w int) string { return fmt.Sprintf("/media/%s/w%d.jpg", id, w) }

// UploadInput is one upload.
type UploadInput struct {
	OwnerUserID uint64
	ProviderID  uint64
	Purpose     string
	Data        []byte
	Name        string
	Pending     bool // pre-moderation: stored but not public until approved
}

func codeErr(err error) error {
	switch {
	case errors.Is(err, ErrUnsupportedType), errors.Is(err, ErrTooLarge), errors.Is(err, ErrDimensions),
		errors.Is(err, ErrCorrupt), errors.Is(err, ErrUnsafePDF):
		return response.Validation(map[string]string{"file": err.Error()})
	}
	return err
}

// Upload validates, processes and stores a file inside tx, returning its record.
func (s *Service) Upload(ctx context.Context, tx *sql.Tx, in UploadInput) (*Media, error) {
	if int64(len(in.Data)) > s.MaxUploadBytes(ctx) {
		return nil, codeErr(ErrTooLarge)
	}
	if len(in.Data) == 0 {
		return nil, codeErr(ErrCorrupt)
	}
	sum := sha256.Sum256(in.Data)
	var (
		prefix, contentType, visibility string
		width, height                   *int
		variants                        []int
		written                         []string
		store                           storage.Store
	)
	cleanup := func() {
		for _, k := range written {
			_ = store.Delete(k)
		}
	}
	put := func(key string, b []byte) error {
		if err := store.Put(key, b); err != nil {
			return err
		}
		written = append(written, key)
		return nil
	}
	switch in.Purpose {
	case PurposeProfile, PurposeCover, PurposePortfolio:
		store, visibility, contentType = s.public, "public", "image/jpeg"
		p, err := ProcessImage(in.Data)
		if err != nil {
			return nil, codeErr(err)
		}
		prefix = randomPrefix("m")
		for w, b := range p.Variants {
			if err := put(variantKey(prefix, w), b); err != nil {
				cleanup()
				return nil, err
			}
			variants = append(variants, w)
		}
		slices.Sort(variants)
		width, height = &p.Width, &p.Height
	case PurposeDocument:
		store, visibility = s.private, "private"
		prefix = randomPrefix("d")
		if DetectType(in.Data) == "application/pdf" {
			if err := CheckPDF(in.Data); err != nil {
				return nil, codeErr(err)
			}
			contentType = "application/pdf"
			if err := put(prefix+"/doc.pdf", in.Data); err != nil {
				return nil, err
			}
		} else {
			b, w, h, err := ProcessDocumentImage(in.Data)
			if err != nil {
				return nil, codeErr(err)
			}
			contentType, width, height = "image/jpeg", &w, &h
			if err := put(prefix+"/doc.jpg", b); err != nil {
				return nil, err
			}
		}
	default:
		return nil, response.Validation(map[string]string{"purpose": "invalid"})
	}
	status := StatusActive
	if in.Pending {
		status = StatusPending
	}
	var varJSON any
	if len(variants) > 0 {
		b, _ := json.Marshal(variants)
		varJSON = string(b)
	}
	name := in.Name
	if len(name) > 200 {
		name = name[:200]
	}
	var bytesStored int64
	for _, k := range written {
		if f, err := store.Open(k); err == nil {
			if st, err := f.Stat(); err == nil {
				bytesStored += st.Size()
			}
			f.Close()
		}
	}
	pub := ids.New()
	var provider any
	if in.ProviderID > 0 {
		provider = in.ProviderID
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO media_files (public_id, owner_user_id, provider_id, purpose, visibility, storage_key,
		content_type, bytes, width, height, variants, sha256, original_name, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		pub, in.OwnerUserID, provider, in.Purpose, visibility, prefix, contentType, bytesStored, width, height, varJSON, sum[:], nullStr(name), status)
	if err != nil {
		cleanup()
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.byInternal(ctx, tx, uint64(id))
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

const cols = `id, public_id, purpose, status, content_type, bytes, width, height, variants, moderation_note, created_at,
	provider_id, owner_user_id, storage_key, visibility`

func scan(row interface{ Scan(...any) error }) (*Media, error) {
	var m Media
	var pub ids.UUID
	var w, h sql.NullInt64
	var variants []byte
	var note sql.NullString
	var prov, owner sql.NullInt64
	err := row.Scan(&m.InternalID, &pub, &m.Purpose, &m.Status, &m.ContentType, &m.Bytes, &w, &h, &variants, &note, &m.CreatedAt,
		&prov, &owner, &m.storageKey, &m.visibility)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, response.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	m.ID = pub.String()
	if w.Valid {
		x := int(w.Int64)
		m.Width = &x
	}
	if h.Valid {
		x := int(h.Int64)
		m.Height = &x
	}
	if note.Valid {
		m.Note = &note.String
	}
	if prov.Valid {
		x := uint64(prov.Int64)
		m.ProviderID = &x
	}
	if owner.Valid {
		x := uint64(owner.Int64)
		m.OwnerUserID = &x
	}
	if len(variants) > 0 {
		_ = json.Unmarshal(variants, &m.variants)
	}
	if m.visibility == "public" && len(m.variants) > 0 {
		m.URLs = map[int]string{}
		for _, v := range m.variants {
			m.URLs[v] = PublicURL(m.ID, v)
		}
		m.URL = PublicURL(m.ID, m.variants[len(m.variants)-1])
	}
	return &m, nil
}

func (s *Service) byInternal(ctx context.Context, ex database.Execer, id uint64) (*Media, error) {
	return scan(ex.QueryRowContext(ctx, `SELECT `+cols+` FROM media_files WHERE id = ?`, id))
}

// ByPublicID loads a media record.
func (s *Service) ByPublicID(ctx context.Context, ex database.Execer, publicID string) (*Media, error) {
	pub, err := ids.Parse(publicID)
	if err != nil {
		return nil, response.ErrNotFound
	}
	return scan(ex.QueryRowContext(ctx, `SELECT `+cols+` FROM media_files WHERE public_id = ?`, pub))
}

// ByInternalIDs loads several records (no N+1).
func (s *Service) ByInternalIDs(ctx context.Context, ex database.Execer, idsList []uint64) (map[uint64]*Media, error) {
	out := map[uint64]*Media{}
	if len(idsList) == 0 {
		return out, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(idsList)), ",")
	args := make([]any, len(idsList))
	for i, v := range idsList {
		args[i] = v
	}
	rows, err := ex.QueryContext(ctx, `SELECT `+cols+` FROM media_files WHERE id IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		m, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out[m.InternalID] = m
	}
	return out, rows.Err()
}

// MarkDeleted soft-deletes media and schedules file removal in the same transaction.
func (s *Service) MarkDeleted(ctx context.Context, tx *sql.Tx, id uint64) error {
	m, err := s.byInternal(ctx, tx, id)
	if err != nil {
		return err
	}
	if m.Status == StatusDeleted {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE media_files SET status = 'deleted' WHERE id = ?`, id); err != nil {
		return err
	}
	return jobsqueue.Enqueue(ctx, tx, JobPurge, map[string]any{"keys": m.keys(), "private": m.visibility == "private"}, jobsqueue.Options{})
}

func (m *Media) keys() []string {
	if m.visibility == "private" {
		if m.ContentType == "application/pdf" {
			return []string{m.storageKey + "/doc.pdf"}
		}
		return []string{m.storageKey + "/doc.jpg"}
	}
	out := []string{}
	for _, w := range m.variants {
		out = append(out, variantKey(m.storageKey, w))
	}
	return out
}

// ---- HTTP: public files ---------------------------------------------------------------

var fileRe = regexp.MustCompile(`^w([0-9]{2,4})\.jpg$`)

// PublicHandler serves /media/{id}/w{width}.jpg.
type PublicHandler struct{ svc *Service }

// NewPublicHandler builds the handler.
func NewPublicHandler(svc *Service) *PublicHandler { return &PublicHandler{svc: svc} }

// Register mounts the route.
func (h *PublicHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /media/{id}/{file}", h.serve)
}

func (h *PublicHandler) serve(w http.ResponseWriter, r *http.Request) {
	m := fileRe.FindStringSubmatch(r.PathValue("file"))
	if m == nil {
		http.NotFound(w, r)
		return
	}
	width, _ := strconv.Atoi(m[1])
	med, err := h.svc.ByPublicID(r.Context(), h.svc.db, r.PathValue("id"))
	if err != nil || med.visibility != "public" || med.Status == StatusDeleted || !slices.Contains(med.variants, width) {
		http.NotFound(w, r)
		return
	}
	cache := "public, max-age=31536000, immutable"
	if med.Status != StatusActive {
		// Pending or rejected images are visible only to their owner and moderators.
		p := reqctx.PrincipalFrom(r.Context())
		if p == nil || ((med.OwnerUserID == nil || *med.OwnerUserID != p.UserID) && !h.svc.authz.Can(r.Context(), p, "media.moderate")) {
			http.NotFound(w, r)
			return
		}
		cache = "private, no-store"
	}
	f, err := h.svc.public.Open(variantKey(med.storageKey, width))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	hd := w.Header()
	hd.Set("Content-Type", "image/jpeg")
	hd.Set("Cache-Control", cache)
	hd.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	hd.Set("Content-Disposition", "inline")
	hd.Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", med.CreatedAt, f)
}

// ---- HTTP: admin ----------------------------------------------------------------------

// AdminHandler exposes private downloads and the media moderation queue.
type AdminHandler struct{ svc *Service }

// NewAdminHandler builds the handler.
func NewAdminHandler(svc *Service) *AdminHandler { return &AdminHandler{svc: svc} }

// Register mounts routes.
func (h *AdminHandler) Register(mux *http.ServeMux, prefix string) {
	az := h.svc.authz
	mux.Handle("GET "+prefix+"/admin/media/{id}/download", az.Require("media.view_private")(http.HandlerFunc(h.download)))
	mux.Handle("GET "+prefix+"/admin/media", az.Require("media.moderate")(http.HandlerFunc(h.queue)))
	mux.Handle("PATCH "+prefix+"/admin/media/{id}", az.Require("media.moderate")(http.HandlerFunc(h.moderate)))
}

// download streams a private document; every view is audited (§16.1).
func (h *AdminHandler) download(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, err := h.svc.ByPublicID(ctx, h.svc.db, r.PathValue("id"))
	if err != nil || m.Status == StatusDeleted {
		response.Fail(w, r, response.ErrNotFound)
		return
	}
	var f io.ReadSeekCloser
	name := "document"
	if m.visibility == "private" {
		key := m.keys()[0]
		file, err := h.svc.private.Open(key)
		if err != nil {
			response.Fail(w, r, response.ErrNotFound)
			return
		}
		f = file
		if m.ContentType == "application/pdf" {
			name += ".pdf"
		} else {
			name += ".jpg"
		}
	} else {
		file, err := h.svc.public.Open(variantKey(m.storageKey, m.variants[len(m.variants)-1]))
		if err != nil {
			response.Fail(w, r, response.ErrNotFound)
			return
		}
		f, name = file, "image.jpg"
	}
	defer f.Close()
	if err := audit.Record(ctx, h.svc.db, audit.Entry{Action: "media.private.viewed", EntityType: "media", EntityID: m.ID}); err != nil {
		response.Fail(w, r, err)
		return
	}
	disp := "attachment"
	if r.URL.Query().Get("inline") == "1" && m.ContentType == "image/jpeg" {
		disp = "inline"
	}
	hd := w.Header()
	hd.Set("Content-Type", m.ContentType)
	hd.Set("Content-Disposition", disp+`; filename="`+name+`"`)
	hd.Set("Cache-Control", "private, no-store")
	hd.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	hd.Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", m.CreatedAt, f)
}

// QueueItem is a media file awaiting moderation.
type QueueItem struct {
	Media
	ProviderName string `json:"provider_name"`
	ProviderPID  string `json:"provider_id"`
}

func (h *AdminHandler) queue(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = StatusPending
	}
	rows, err := h.svc.db.QueryContext(r.Context(), `SELECT m.id, m.public_id, m.purpose, m.status, m.content_type, m.bytes, m.width,
			m.height, m.variants, m.moderation_note, m.created_at, m.provider_id, m.owner_user_id, m.storage_key, m.visibility,
			COALESCE(p.display_name, ''), p.public_id
		FROM media_files m LEFT JOIN providers p ON p.id = m.provider_id
		WHERE m.status = ? AND m.visibility = 'public' ORDER BY m.created_at LIMIT 200`, status)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	defer rows.Close()
	out := []QueueItem{}
	for rows.Next() {
		var q QueueItem
		var ppub []byte
		var pub ids.UUID
		var wd, ht sql.NullInt64
		var variants []byte
		var note sql.NullString
		var prov, owner sql.NullInt64
		if err := rows.Scan(&q.InternalID, &pub, &q.Purpose, &q.Status, &q.ContentType, &q.Bytes, &wd, &ht, &variants, &note,
			&q.CreatedAt, &prov, &owner, &q.storageKey, &q.visibility, &q.ProviderName, &ppub); err != nil {
			response.Fail(w, r, err)
			return
		}
		q.ID = pub.String()
		_ = json.Unmarshal(variants, &q.variants)
		if len(q.variants) > 0 {
			q.URL = PublicURL(q.ID, q.variants[len(q.variants)-1])
		}
		if len(ppub) == 16 {
			var u ids.UUID
			copy(u[:], ppub)
			q.ProviderPID = u.String()
		}
		out = append(out, q)
	}
	response.OK(w, out)
}

func (h *AdminHandler) moderate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Decision string `json:"decision"` // approve | reject
		Note     string `json:"note"`
	}
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	if in.Decision != "approve" && in.Decision != "reject" {
		response.Fail(w, r, response.Validation(map[string]string{"decision": "invalid"}))
		return
	}
	note := strings.TrimSpace(in.Note)
	if in.Decision == "reject" && (note == "" || len(note) > 500) {
		response.Fail(w, r, response.Validation(map[string]string{"note": "required"}))
		return
	}
	ctx := r.Context()
	err := database.WithTx(ctx, h.svc.db, func(tx *sql.Tx) error {
		m, err := h.svc.ByPublicID(ctx, tx, r.PathValue("id"))
		if err != nil || m.Status == StatusDeleted || m.visibility != "public" {
			return response.ErrNotFound
		}
		status := StatusActive
		if in.Decision == "reject" {
			status = StatusRejected
		}
		p := reqctx.PrincipalFrom(ctx)
		if _, err := tx.ExecContext(ctx, `UPDATE media_files SET status = ?, moderated_by = ?, moderated_at = ?, moderation_note = ? WHERE id = ?`,
			status, p.UserID, time.Now().UTC(), nullStr(note), m.InternalID); err != nil {
			return err
		}
		if err := moderation.Record(ctx, tx, "media", m.ID, "media."+in.Decision, note); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "media.moderated", EntityType: "media", EntityID: m.ID,
			OldValues: map[string]any{"status": m.Status}, NewValues: map[string]any{"status": status, "note": note}})
	})
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	m, err := h.svc.ByPublicID(ctx, h.svc.db, r.PathValue("id"))
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, m)
}
