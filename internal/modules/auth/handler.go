package auth

import (
	"net/http"
	"sort"
	"time"

	"github.com/alumasinde/profy/internal/authz"
	"github.com/alumasinde/profy/internal/http/middleware"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/modules/users"
	"github.com/alumasinde/profy/internal/security"
	"github.com/alumasinde/profy/internal/shared/reqctx"
)

// Handler exposes the auth API under /api/v1/auth and /api/v1/customer/profile.
type Handler struct {
	svc *Service
}

// NewHandler builds the handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts routes on mux. prefix is "/api/v1".
func (h *Handler) Register(mux *http.ServeMux, prefix string) {
	mux.HandleFunc("GET "+prefix+"/auth/session", h.session)
	mux.HandleFunc("POST "+prefix+"/auth/register", h.register)
	mux.HandleFunc("POST "+prefix+"/auth/otp/send", h.sendOTP)
	mux.HandleFunc("POST "+prefix+"/auth/otp/verify", h.verifyOTP)
	mux.Handle("POST "+prefix+"/auth/pin/setup", authz.RequireAuth(http.HandlerFunc(h.setupPIN)))
	mux.HandleFunc("POST "+prefix+"/auth/pin/login", h.pinLogin)
	mux.HandleFunc("POST "+prefix+"/auth/logout", h.logout)
	mux.HandleFunc("POST "+prefix+"/auth/recovery/start", h.recoveryStart)
	mux.HandleFunc("POST "+prefix+"/auth/recovery/complete", h.recoveryComplete)
	mux.Handle("GET "+prefix+"/auth/sessions", authz.RequireAuth(http.HandlerFunc(h.listSessions)))
	mux.Handle("DELETE "+prefix+"/auth/sessions", authz.RequireAuth(http.HandlerFunc(h.revokeOthers)))
	mux.Handle("DELETE "+prefix+"/auth/sessions/{id}", authz.RequireAuth(http.HandlerFunc(h.revokeSession)))
	mux.Handle("GET "+prefix+"/customer/profile", authz.RequireAuth(http.HandlerFunc(h.getProfile)))
	mux.Handle("PATCH "+prefix+"/customer/profile", authz.RequireAuth(http.HandlerFunc(h.patchProfile)))
}

// startSession sets the session and device cookies and rotates the CSRF token.
func (h *Handler) startSession(w http.ResponseWriter, res *LoginResult) {
	c := h.svc.cookies
	c.set(w, c.SessionName(), res.Session.token, res.Session.expiresAt)
	c.set(w, c.DeviceName(), res.DeviceToken, time.Now().Add(DeviceCookieLife))
	middleware.SetCSRFCookie(w, c.Secure, security.RandomToken(32))
}

// UserView is the signed-in user's own data.
type UserView struct {
	PublicID        string   `json:"public_id"`
	FirstName       string   `json:"first_name"`
	MiddleName      *string  `json:"middle_name"`
	LastName        string   `json:"last_name"`
	DisplayName     string   `json:"display_name"`
	Email           *string  `json:"email"`
	Phone           *string  `json:"phone"`
	EmailVerified   bool     `json:"email_verified"`
	PhoneVerified   bool     `json:"phone_verified"`
	PreferredLocale string   `json:"preferred_locale"`
	Roles           []string `json:"roles"`
}

func toView(u *users.User, roles []string) UserView {
	return UserView{
		PublicID: u.PublicID.String(), FirstName: u.Names.First, MiddleName: u.Names.Middle, LastName: u.Names.Last,
		DisplayName: u.Names.Display(), Email: u.Email, Phone: u.PhoneE164,
		EmailVerified: u.EmailVerifiedAt != nil, PhoneVerified: u.PhoneVerifiedAt != nil,
		PreferredLocale: u.PreferredLocale, Roles: roles,
	}
}

func (h *Handler) session(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := reqctx.PrincipalFrom(ctx)
	if p == nil {
		response.OK(w, map[string]any{"authenticated": false})
		return
	}
	u, err := h.svc.users.ByID(ctx, h.svc.db, p.UserID)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	roles, err := h.svc.users.RoleKeys(ctx, h.svc.db, p.UserID)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	perms, err := h.svc.authz.Permissions(ctx, p.UserID)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	keys := make([]string, 0, len(perms))
	for k := range perms {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ok, until, err := h.svc.recentlyReauthenticated(ctx, p.SessionID)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	var reauthUntil *time.Time
	if ok {
		reauthUntil = &until
	}
	var hasPIN bool
	_ = h.svc.db.QueryRowContext(ctx, `SELECT COUNT(*) > 0 FROM user_credentials WHERE user_id = ? AND credential_type = 'pin'`, p.UserID).Scan(&hasPIN)
	response.OK(w, map[string]any{
		"authenticated":         true,
		"user":                  toView(u, roles),
		"permissions":           keys, // display only; the backend enforces every check
		"has_pin":               hasPIN,
		"reauthenticated_until": reauthUntil,
	})
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var in RegisterInput
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	c, err := h.svc.Register(r.Context(), in)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.JSON(w, http.StatusAccepted, c, nil)
}

func (h *Handler) sendOTP(w http.ResponseWriter, r *http.Request) {
	var in SendOTPInput
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	c, err := h.svc.SendOTP(r.Context(), in)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.JSON(w, http.StatusAccepted, c, nil)
}

func (h *Handler) verifyOTP(w http.ResponseWriter, r *http.Request) {
	var in VerifyInput
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	res, err := h.svc.VerifyOTP(r.Context(), r, in)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	if res == nil { // sensitive_change re-verification
		response.OK(w, map[string]any{"reauthenticated": true})
		return
	}
	h.startSession(w, res)
	response.OK(w, map[string]any{"authenticated": true, "new_user": res.NewUser})
}

func (h *Handler) setupPIN(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PIN string `json:"pin"`
	}
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	if err := h.svc.SetupPIN(r.Context(), in.PIN); err != nil {
		response.Fail(w, r, err)
		return
	}
	response.NoContent(w)
}

func (h *Handler) pinLogin(w http.ResponseWriter, r *http.Request) {
	var in PINLoginInput
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	res, err := h.svc.LoginWithPIN(r.Context(), r, in)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	h.startSession(w, res)
	response.OK(w, map[string]any{"authenticated": true})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Logout(r.Context()); err != nil {
		response.Fail(w, r, err)
		return
	}
	h.svc.cookies.clear(w, h.svc.cookies.SessionName())
	middleware.SetCSRFCookie(w, h.svc.cookies.Secure, security.RandomToken(32))
	response.NoContent(w)
}

func (h *Handler) recoveryStart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Destination string `json:"destination"`
	}
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	c, err := h.svc.RecoveryStart(r.Context(), in.Destination)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.JSON(w, http.StatusAccepted, c, nil)
}

func (h *Handler) recoveryComplete(w http.ResponseWriter, r *http.Request) {
	var in RecoveryCompleteInput
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	res, err := h.svc.RecoveryComplete(r.Context(), r, in)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	h.startSession(w, res)
	response.OK(w, map[string]any{"authenticated": true})
}

func (h *Handler) listSessions(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.ListSessions(r.Context())
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, list)
}

func (h *Handler) revokeSession(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.RevokeSession(r.Context(), r.PathValue("id")); err != nil {
		response.Fail(w, r, err)
		return
	}
	response.NoContent(w)
}

func (h *Handler) revokeOthers(w http.ResponseWriter, r *http.Request) {
	n, err := h.svc.RevokeOtherSessions(r.Context())
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, map[string]any{"revoked": n})
}

func (h *Handler) getProfile(w http.ResponseWriter, r *http.Request) {
	p := reqctx.PrincipalFrom(r.Context())
	u, err := h.svc.users.ByID(r.Context(), h.svc.db, p.UserID)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	roles, err := h.svc.users.RoleKeys(r.Context(), h.svc.db, p.UserID)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, toView(u, roles))
}

func (h *Handler) patchProfile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FirstName       string  `json:"first_name"`
		MiddleName      *string `json:"middle_name"`
		LastName        string  `json:"last_name"`
		PreferredLocale string  `json:"preferred_locale"`
	}
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	names, errs := users.NormalizeNames(in.FirstName, in.MiddleName, in.LastName)
	if in.PreferredLocale != "" && !h.svc.i18n.Has(in.PreferredLocale) {
		errs["preferred_locale"] = "invalid"
	}
	if len(errs) > 0 {
		response.Fail(w, r, response.Validation(errs))
		return
	}
	ctx := r.Context()
	p := reqctx.PrincipalFrom(ctx)
	u, err := h.svc.users.ByID(ctx, h.svc.db, p.UserID)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	locale := in.PreferredLocale
	if locale == "" {
		locale = u.PreferredLocale
	}
	if err := h.svc.users.UpdateProfile(ctx, h.svc.db, p.UserID, names, locale); err != nil {
		response.Fail(w, r, err)
		return
	}
	h.getProfile(w, r)
}
