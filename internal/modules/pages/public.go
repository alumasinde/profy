package pages

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/alumasinde/profy/internal/authz"
	"github.com/alumasinde/profy/internal/modules/public"
	"github.com/alumasinde/profy/internal/shared/reqctx"
)

// pageData is the template data for a managed page.
type pageData struct {
	Title   string
	Body    template.HTML
	preview bool
}

// IsPreview lets the layout show the preview banner.
func (p pageData) IsPreview() bool { return p.preview }

// PublicHandler serves published pages at /{slug} and authorized previews (§8.3).
type PublicHandler struct {
	svc   *Service
	pub   *public.Handler
	authz *authz.Service
}

// NewPublicHandler builds the handler.
func NewPublicHandler(svc *Service, pub *public.Handler, az *authz.Service) *PublicHandler {
	return &PublicHandler{svc: svc, pub: pub, authz: az}
}

// Register mounts routes. Literal routes (/theme.css, /robots.txt, /auth …) take precedence.
func (h *PublicHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{slug}", h.page)
	mux.HandleFunc("GET /preview/pages/{id}", h.preview)
}

func (h *PublicHandler) page(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.Published(r.Context(), r.PathValue("slug"))
	if err != nil {
		h.pub.NotFound(w, r)
		return
	}
	origin := h.pub.SiteOrigin(r)
	meta := public.Meta{Title: p.Title + " — " + h.pub.SiteName(r), Canonical: origin + p.Path, Robots: "index,follow"}
	if p.MetaTitle != nil {
		meta.Title = *p.MetaTitle
	}
	if p.MetaDescription != nil {
		meta.Description = *p.MetaDescription
	} else {
		meta.Description = summary(p.Body)
	}
	if !p.RobotsIndex {
		meta.Robots = "noindex,follow"
	}
	h.pub.Render(w, r, http.StatusOK, "page", meta, pageData{Title: p.Title, Body: RenderMarkdown(p.Body)})
}

// preview renders any page (including drafts) for users holding pages.view; never indexed.
func (h *PublicHandler) preview(w http.ResponseWriter, r *http.Request) {
	if !h.authz.Can(r.Context(), reqctx.PrincipalFrom(r.Context()), "pages.view") {
		h.pub.NotFound(w, r)
		return
	}
	p, err := h.svc.Get(r.Context(), h.svc.db, r.PathValue("id"), false)
	if err != nil {
		h.pub.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.pub.Render(w, r, http.StatusOK, "page", public.Meta{Title: p.Title, Robots: "noindex,nofollow"},
		pageData{Title: p.Title, Body: RenderMarkdown(p.Body), preview: true})
}

// summary derives a plain meta description from the page body when none is set (§9.3).
func summary(body string) string {
	var parts []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#-*0123456789. "))
		if line != "" {
			parts = append(parts, line)
		}
	}
	s := strings.NewReplacer("**", "", "[", "", "]", "").Replace(strings.Join(parts, " "))
	if r := []rune(s); len(r) > 155 {
		s = strings.TrimSpace(string(r[:155])) + "…"
	}
	return s
}
