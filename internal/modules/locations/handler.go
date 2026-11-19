package locations

import (
	"net/http"
	"strconv"

	"github.com/alumasinde/profy/internal/authz"
	"github.com/alumasinde/profy/internal/http/response"
)

// Handler exposes /api/v1/admin/locations.
type Handler struct {
	svc   *Service
	authz *authz.Service
}

// NewHandler builds the handler.
func NewHandler(svc *Service, az *authz.Service) *Handler { return &Handler{svc: svc, authz: az} }

// Register mounts routes.
func (h *Handler) Register(mux *http.ServeMux, prefix string) {
	req := func(perm string, fn http.HandlerFunc) http.Handler { return h.authz.Require(perm)(fn) }
	base := prefix + "/admin/locations"
	mux.Handle("GET "+base+"/{kind}", req("locations.view", h.list))
	mux.Handle("POST "+base+"/{kind}", req("locations.create", h.create))
	mux.Handle("GET "+base+"/{kind}/{id}", req("locations.view", h.get))
	mux.Handle("PATCH "+base+"/{kind}/{id}", req("locations.view", h.update)) // update/archive checked in service
	mux.Handle("PUT "+base+"/{kind}/{id}/aliases", req("locations.manage_aliases", h.aliases))
	mux.Handle("PUT "+base+"/{kind}/{id}/centroid", req("locations.manage_coordinates", h.setCentroid))
	mux.Handle("DELETE "+base+"/{kind}/{id}/centroid", req("locations.manage_coordinates", h.clearCentroid))
	mux.Handle("POST "+base+"/merge", req("locations.merge", h.merge))
}

func kindOf(w http.ResponseWriter, r *http.Request) (string, bool) {
	t, ok := TypeFromPlural(r.PathValue("kind"))
	if !ok {
		response.Fail(w, r, response.ErrNotFound)
	}
	return t, ok
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	t, ok := kindOf(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, next, err := h.svc.List(r.Context(), ListInput{Type: t, ParentID: q.Get("parent_id"), Status: q.Get("status"),
		Query: q.Get("q"), Cursor: q.Get("cursor"), Limit: limit})
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	var nc any
	if next != "" {
		nc = next
	}
	response.JSON(w, http.StatusOK, list, map[string]any{"next_cursor": nc})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	t, ok := kindOf(w, r)
	if !ok {
		return
	}
	l, err := h.svc.Get(r.Context(), h.svc.db, t, r.PathValue("id"), false)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, l)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	t, ok := kindOf(w, r)
	if !ok {
		return
	}
	var in Input
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	l, err := h.svc.Create(r.Context(), t, in)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, l, nil)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	t, ok := kindOf(w, r)
	if !ok {
		return
	}
	var in Input
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	l, err := h.svc.Update(r.Context(), t, r.PathValue("id"), in)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, l)
}

func (h *Handler) aliases(w http.ResponseWriter, r *http.Request) {
	t, ok := kindOf(w, r)
	if !ok {
		return
	}
	var in struct {
		Aliases []string `json:"aliases"`
	}
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	l, err := h.svc.SetAliases(r.Context(), t, r.PathValue("id"), in.Aliases)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, l)
}

func (h *Handler) setCentroid(w http.ResponseWriter, r *http.Request) {
	t, ok := kindOf(w, r)
	if !ok {
		return
	}
	var in struct {
		Latitude  *float64 `json:"latitude"`
		Longitude *float64 `json:"longitude"`
	}
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	if in.Latitude == nil || in.Longitude == nil {
		response.Fail(w, r, response.Validation(map[string]string{"latitude": "required", "longitude": "required"}))
		return
	}
	l, err := h.svc.SetCentroid(r.Context(), t, r.PathValue("id"), &Point{Latitude: *in.Latitude, Longitude: *in.Longitude})
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, l)
}

func (h *Handler) clearCentroid(w http.ResponseWriter, r *http.Request) {
	t, ok := kindOf(w, r)
	if !ok {
		return
	}
	l, err := h.svc.SetCentroid(r.Context(), t, r.PathValue("id"), nil)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, l)
}

func (h *Handler) merge(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Type     string `json:"type"`
		SourceID string `json:"source_id"`
		TargetID string `json:"target_id"`
	}
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	l, err := h.svc.Merge(r.Context(), in.Type, in.SourceID, in.TargetID)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, l)
}
