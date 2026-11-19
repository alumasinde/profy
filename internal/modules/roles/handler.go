package roles

import (
	"net/http"
	"strconv"

	"github.com/alumasinde/profy/internal/authz"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/shared/reqctx"
)

// Handler exposes /api/v1/admin role, user and audit endpoints.
type Handler struct {
	svc   *Service
	authz *authz.Service
}

// NewHandler builds the handler.
func NewHandler(svc *Service, az *authz.Service) *Handler { return &Handler{svc: svc, authz: az} }

// Register mounts routes; each is guarded by a specific permission (fails closed).
func (h *Handler) Register(mux *http.ServeMux, prefix string) {
	req := func(perm string, fn http.HandlerFunc) http.Handler { return h.authz.Require(perm)(fn) }
	mux.Handle("GET "+prefix+"/admin/roles", req("roles.view", h.listRoles))
	mux.Handle("PUT "+prefix+"/admin/roles/{key}/permissions", req("roles.permissions.assign", h.setRolePermissions))
	mux.Handle("GET "+prefix+"/admin/permissions", req("permissions.view", h.listPermissions))
	mux.Handle("GET "+prefix+"/admin/users", req("users.view_any", h.listUsers))
	mux.Handle("GET "+prefix+"/admin/users/{id}", req("users.view_any", h.getUser))
	mux.Handle("PUT "+prefix+"/admin/users/{id}/roles", req("users.roles.assign", h.setUserRoles))
	mux.Handle("PATCH "+prefix+"/admin/users/{id}/status", authz.RequireAuth(http.HandlerFunc(h.setUserStatus)))
	mux.Handle("GET "+prefix+"/admin/audit-logs", req("audit_logs.view", h.listAudit))
}

func (h *Handler) listRoles(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.ListRoles(r.Context())
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, list)
}

func (h *Handler) setRolePermissions(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Permissions []string `json:"permissions"`
	}
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	v, err := h.svc.SetRolePermissions(r.Context(), r.PathValue("key"), in.Permissions)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, v)
}

func (h *Handler) listPermissions(w http.ResponseWriter, r *http.Request) {
	response.OK(w, h.svc.ListPermissions())
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, next, err := h.svc.ListUsers(r.Context(), ListUsersInput{Status: q.Get("status"), Query: q.Get("q"), Cursor: q.Get("cursor"), Limit: limit})
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, list, map[string]any{"next_cursor": nullIfEmpty(next)})
}

func (h *Handler) getUser(w http.ResponseWriter, r *http.Request) {
	v, err := h.svc.GetUser(r.Context(), r.PathValue("id"))
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, v)
}

func (h *Handler) setUserRoles(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Roles []string `json:"roles"`
	}
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	v, err := h.svc.SetUserRoles(r.Context(), r.PathValue("id"), in.Roles)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, v)
}

// setUserStatus needs users.suspend to suspend and users.restore to restore.
func (h *Handler) setUserStatus(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	perm := "users.restore"
	if in.Status == "suspended" {
		perm = "users.suspend"
	}
	if !h.authz.Can(r.Context(), reqctx.PrincipalFrom(r.Context()), perm) {
		response.Fail(w, r, response.ErrForbidden)
		return
	}
	v, err := h.svc.SetUserStatus(r.Context(), r.PathValue("id"), in.Status, in.Reason)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, v)
}

func (h *Handler) listAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	sensitive := h.authz.Can(r.Context(), reqctx.PrincipalFrom(r.Context()), "audit_logs.view_sensitive")
	list, next, err := h.svc.ListAudit(r.Context(), ListAuditInput{
		EntityType: q.Get("entity_type"), EntityID: q.Get("entity_id"), Action: q.Get("action"),
		Cursor: q.Get("cursor"), Limit: limit, IncludeSensitive: sensitive,
	})
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, list, map[string]any{"next_cursor": nullIfEmpty(next)})
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
