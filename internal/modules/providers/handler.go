package providers

import (
	"io"
	"net/http"
	"strconv"

	"github.com/alumasinde/profy/internal/authz"
	"github.com/alumasinde/profy/internal/http/response"
)

// Handler exposes the owner (/provider/...) and staff (/admin/providers/...) APIs. Every
// route is authenticated; the service enforces ownership and *_any permissions.
type Handler struct{ svc *Service }

// NewHandler builds the handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

type targetFn func(r *http.Request) Target

func own(*http.Request) Target               { return Target{} }
func staff(r *http.Request) Target           { return Target{Staff: true, PublicID: r.PathValue("id")} }
func authed(h http.HandlerFunc) http.Handler { return authz.RequireAuth(h) }

// Register mounts routes.
func (h *Handler) Register(mux *http.ServeMux, prefix string) {
	az := h.svc.authz
	staffOnly := func(hf http.HandlerFunc) http.Handler { return az.Require("providers.view_any")(hf) }

	// Owner.
	mux.Handle("GET "+prefix+"/provider/profile", authed(h.get(own)))
	mux.Handle("POST "+prefix+"/provider/profile", authed(h.create))
	mux.Handle("PATCH "+prefix+"/provider/profile", authed(h.update(own)))
	for _, a := range []string{"submit", "pause", "resume", "archive"} {
		mux.Handle("POST "+prefix+"/provider/profile/"+a, authed(h.transition(own, a)))
	}
	mux.Handle("GET "+prefix+"/provider/dashboard", authed(h.dashboard))
	mux.Handle("POST "+prefix+"/provider/services", authed(h.addService(own)))
	mux.Handle("PATCH "+prefix+"/provider/services/{offer}", authed(h.updateService(own)))
	mux.Handle("DELETE "+prefix+"/provider/services/{offer}", authed(h.removeService(own)))
	mux.Handle("PUT "+prefix+"/provider/service-areas", authed(h.areas(own)))
	mux.Handle("PUT "+prefix+"/provider/contacts", authed(h.contacts(own)))
	mux.Handle("POST "+prefix+"/provider/contacts/verify", authed(h.verifyStart))
	mux.Handle("POST "+prefix+"/provider/contacts/verify/confirm", authed(h.verifyConfirm))
	mux.Handle("POST "+prefix+"/provider/media", authed(h.upload(own)))
	mux.Handle("DELETE "+prefix+"/provider/media/{media}", authed(h.deleteMedia(own)))
	mux.Handle("PATCH "+prefix+"/provider/portfolio", authed(h.arrange(own)))
	mux.HandleFunc("POST "+prefix+"/provider/claim/preview", h.claimPreview)
	mux.Handle("POST "+prefix+"/provider/claim", authed(h.claim))

	// Staff.
	mux.Handle("GET "+prefix+"/admin/providers", staffOnly(h.list))
	mux.Handle("POST "+prefix+"/admin/providers", staffOnly(h.createAssisted))
	mux.Handle("GET "+prefix+"/admin/providers/{id}", staffOnly(h.get(staff)))
	mux.Handle("PATCH "+prefix+"/admin/providers/{id}", staffOnly(h.update(staff)))
	mux.Handle("POST "+prefix+"/admin/providers/{id}/status", staffOnly(h.staffTransition))
	mux.Handle("POST "+prefix+"/admin/providers/{id}/invite", staffOnly(h.invite))
	mux.Handle("PUT "+prefix+"/admin/providers/{id}/featured", staffOnly(h.featured))
	mux.Handle("POST "+prefix+"/admin/providers/{id}/services", staffOnly(h.addService(staff)))
	mux.Handle("PATCH "+prefix+"/admin/providers/{id}/services/{offer}", staffOnly(h.updateService(staff)))
	mux.Handle("DELETE "+prefix+"/admin/providers/{id}/services/{offer}", staffOnly(h.removeService(staff)))
	mux.Handle("PUT "+prefix+"/admin/providers/{id}/service-areas", staffOnly(h.areas(staff)))
	mux.Handle("PUT "+prefix+"/admin/providers/{id}/contacts", staffOnly(h.contacts(staff)))
	mux.Handle("POST "+prefix+"/admin/providers/{id}/media", staffOnly(h.upload(staff)))
	mux.Handle("DELETE "+prefix+"/admin/providers/{id}/media/{media}", staffOnly(h.deleteMedia(staff)))
	mux.Handle("PATCH "+prefix+"/admin/providers/{id}/portfolio", staffOnly(h.arrange(staff)))
}

func reply(w http.ResponseWriter, r *http.Request, v any, err error) {
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, v)
}

func (h *Handler) get(t targetFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := h.svc.Get(r.Context(), t(r))
		reply(w, r, v, err)
	}
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in CreateInput
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	v, err := h.svc.Create(r.Context(), in)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, v, nil)
}

func (h *Handler) update(t targetFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in ProfileInput
		if err := response.Decode(w, r, &in); err != nil {
			response.Fail(w, r, err)
			return
		}
		v, err := h.svc.UpdateProfile(r.Context(), t(r), in)
		reply(w, r, v, err)
	}
}

type transitionBody struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
}

// decodeOptional accepts an empty body as the zero value.
func decodeOptional(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.ContentLength == 0 {
		return nil
	}
	return response.Decode(w, r, dst)
}

func (h *Handler) transition(t targetFn, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in transitionBody
		if err := decodeOptional(w, r, &in); err != nil {
			response.Fail(w, r, err)
			return
		}
		v, err := h.svc.Transition(r.Context(), t(r), action, in.Reason)
		reply(w, r, v, err)
	}
}

func (h *Handler) staffTransition(w http.ResponseWriter, r *http.Request) {
	var in transitionBody
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	v, err := h.svc.Transition(r.Context(), staff(r), in.Action, in.Reason)
	reply(w, r, v, err)
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	d, err := h.svc.OwnerDashboard(r.Context())
	reply(w, r, d, err)
}

func (h *Handler) addService(t targetFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in OfferInput
		if err := response.Decode(w, r, &in); err != nil {
			response.Fail(w, r, err)
			return
		}
		v, err := h.svc.AddService(r.Context(), t(r), in)
		if err != nil {
			response.Fail(w, r, err)
			return
		}
		response.JSON(w, http.StatusCreated, v, nil)
	}
}

func (h *Handler) updateService(t targetFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in OfferInput
		if err := response.Decode(w, r, &in); err != nil {
			response.Fail(w, r, err)
			return
		}
		v, err := h.svc.UpdateService(r.Context(), t(r), r.PathValue("offer"), in)
		reply(w, r, v, err)
	}
}

func (h *Handler) removeService(t targetFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := h.svc.RemoveService(r.Context(), t(r), r.PathValue("offer"))
		reply(w, r, v, err)
	}
}

func (h *Handler) areas(t targetFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in AreasInput
		if err := response.Decode(w, r, &in); err != nil {
			response.Fail(w, r, err)
			return
		}
		v, err := h.svc.SetAreas(r.Context(), t(r), in)
		reply(w, r, v, err)
	}
}

func (h *Handler) contacts(t targetFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Contacts []ContactInput `json:"contacts"`
		}
		if err := response.Decode(w, r, &in); err != nil {
			response.Fail(w, r, err)
			return
		}
		v, err := h.svc.SetContacts(r.Context(), t(r), in.Contacts)
		reply(w, r, v, err)
	}
}

func (h *Handler) verifyStart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Channel string `json:"channel"`
	}
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	c, err := h.svc.StartContactVerification(r.Context(), in.Channel)
	reply(w, r, c, err)
}

func (h *Handler) verifyConfirm(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ChallengeID string `json:"challenge_id"`
		Code        string `json:"code"`
	}
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	v, err := h.svc.ConfirmContactVerification(r.Context(), in.ChallengeID, in.Code)
	reply(w, r, v, err)
}

// upload accepts multipart: purpose (profile|cover|portfolio), caption, file.
func (h *Handler) upload(t targetFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		max := h.svc.media.MaxUploadBytes(r.Context())
		r.Body = http.MaxBytesReader(w, r.Body, max+1<<20)
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			response.Fail(w, r, response.Validation(map[string]string{"file": "too_large"}))
			return
		}
		defer r.MultipartForm.RemoveAll()
		f, fh, err := r.FormFile("file")
		if err != nil {
			response.Fail(w, r, response.Validation(map[string]string{"file": "required"}))
			return
		}
		defer f.Close()
		if fh.Size > max {
			response.Fail(w, r, response.Validation(map[string]string{"file": "too_large"}))
			return
		}
		data, err := io.ReadAll(io.LimitReader(f, max+1))
		if err != nil || int64(len(data)) > max {
			response.Fail(w, r, response.Validation(map[string]string{"file": "too_large"}))
			return
		}
		v, err := h.svc.UploadPhoto(r.Context(), t(r), r.FormValue("purpose"), r.FormValue("caption"), fh.Filename, data)
		if err != nil {
			response.Fail(w, r, err)
			return
		}
		response.JSON(w, http.StatusCreated, v, nil)
	}
}

func (h *Handler) deleteMedia(t targetFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := h.svc.DeletePhoto(r.Context(), t(r), r.PathValue("media"))
		reply(w, r, v, err)
	}
}

func (h *Handler) arrange(t targetFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Items []PortfolioOrder `json:"items"`
		}
		if err := response.Decode(w, r, &in); err != nil {
			response.Fail(w, r, err)
			return
		}
		if len(in.Items) > 200 {
			response.Fail(w, r, response.Validation(map[string]string{"items": "too_many"}))
			return
		}
		v, err := h.svc.ArrangePortfolio(r.Context(), t(r), in.Items)
		reply(w, r, v, err)
	}
}

func (h *Handler) claimPreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	p, err := h.svc.PreviewClaim(r.Context(), in.Token)
	reply(w, r, p, err)
}

func (h *Handler) claim(w http.ResponseWriter, r *http.Request) {
	var in ClaimInput
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	v, err := h.svc.Claim(r.Context(), in)
	reply(w, r, v, err)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	p, err := h.svc.List(r.Context(), ListInput{Status: q.Get("status"), Q: q.Get("q"), Cursor: q.Get("cursor"), Limit: limit})
	reply(w, r, p, err)
}

func (h *Handler) createAssisted(w http.ResponseWriter, r *http.Request) {
	var in AssistedInput
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	v, err := h.svc.CreateAssisted(r.Context(), in)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, v, nil)
}

func (h *Handler) invite(w http.ResponseWriter, r *http.Request) {
	var in InviteInput
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	v, err := h.svc.Invite(r.Context(), r.PathValue("id"), in)
	reply(w, r, v, err)
}

// featured sets {days} of homepage featuring (0 removes it).
func (h *Handler) featured(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Days int `json:"days"`
	}
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	v, err := h.svc.SetFeatured(r.Context(), r.PathValue("id"), in.Days)
	reply(w, r, v, err)
}
