// Package routes assembles the HTTP router and middleware stack.
package routes

import (
	"context"
	"database/sql"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/http/middleware"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/i18n"
	"github.com/alumasinde/profy/internal/modules/auth"
	"github.com/alumasinde/profy/internal/modules/public"
	"github.com/alumasinde/profy/internal/modules/settings"
)

// APIModule registers routes under the API prefix.
type APIModule interface {
	Register(mux *http.ServeMux, prefix string)
}

// PublicModule registers server-rendered public routes.
type PublicModule interface {
	Register(mux *http.ServeMux)
}

// Deps are the handlers and services the router needs.
type Deps struct {
	DB         *sql.DB
	Log        *slog.Logger
	WebFS      fs.FS
	I18n       *i18n.Catalogue
	Settings   *settings.Service
	Public     *public.Handler
	Auth       *auth.Service
	APIs       []APIModule
	Pages      []PublicModule
	TrustProxy bool
	Secure     bool
}

const api = "/api/v1"

// New builds the root handler.
func New(d Deps) (http.Handler, error) {
	mux := http.NewServeMux()

	// Health (§19.4).
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "text/plain")
		if err := d.DB.PingContext(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("database unreachable\n"))
			return
		}
		if ok, err := database.MigrationsCurrent(ctx, d.DB); err != nil || !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("migrations pending\n"))
			return
		}
		_, _ = w.Write([]byte("ready\n"))
	})

	// Public, server-rendered pages and static assets.
	d.Public.Register(mux)
	static, err := fs.Sub(d.WebFS, "static")
	if err != nil {
		return nil, err
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServerFS(static))))

	// Platform API.
	mux.HandleFunc("GET "+api+"/i18n/{locale}", func(w http.ResponseWriter, r *http.Request) {
		loc := r.PathValue("locale")
		if !d.I18n.Has(loc) {
			response.Fail(w, r, response.ErrNotFound)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=300")
		response.OK(w, map[string]any{"locale": loc, "messages": d.I18n.Messages(loc), "locales": d.I18n.Locales()})
	})
	mux.HandleFunc("GET "+api+"/settings/public", func(w http.ResponseWriter, r *http.Request) {
		vals, flags, err := d.Settings.Public(r.Context())
		if err != nil {
			response.Fail(w, r, err)
			return
		}
		response.OK(w, map[string]any{"settings": vals, "feature_flags": flags})
	})
	for _, m := range d.APIs {
		m.Register(mux, api)
	}
	for _, m := range d.Pages {
		m.Register(mux)
	}
	mux.HandleFunc(api+"/", func(w http.ResponseWriter, r *http.Request) { response.Fail(w, r, response.ErrNotFound) })

	// Svelte application shell.
	appFS, err := fs.Sub(d.WebFS, "app")
	if err != nil {
		return nil, err
	}
	app := newSPA(appFS)
	mux.HandleFunc("GET /_app/", app.assetHandler)
	for _, p := range AppPrefixes {
		mux.HandleFunc("GET "+p, app.shellHandler)
		mux.HandleFunc("GET "+p+"/", app.shellHandler)
	}

	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Canonical form (§9.1): lowercase, no trailing slash. API and app paths are exempt.
		if r.Method == http.MethodGet && !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/_app/") {
			if target, ok := canonicalPath(r.URL.Path); ok {
				if r.URL.RawQuery != "" {
					target += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, target, http.StatusMovedPermanently)
				return
			}
		}
		// mux.Handler only detects "no route"; mux.ServeHTTP must dispatch so that path
		// wildcards ({id}, {locale}) are populated on the request.
		if _, pattern := mux.Handler(r); pattern == "" && (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
			!strings.HasPrefix(r.URL.Path, "/api/") {
			d.Public.NotFound(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})

	return middleware.Chain(root,
		middleware.RequestContext(d.TrustProxy),
		middleware.Recover(d.Log),
		middleware.SecurityHeaders(d.Secure),
		d.Auth.Middleware,
		d.Public.Maintenance,
		middleware.Logging(d.Log),
		middleware.CSRF(d.Secure, d.Public.SiteOrigin),
	), nil
}

// canonicalPath returns the redirect target when the path is not canonical.
func canonicalPath(p string) (string, bool) {
	c := strings.ToLower(p)
	if len(c) > 1 {
		c = strings.TrimRight(c, "/")
		if c == "" {
			c = "/"
		}
	}
	return c, c != p
}

func cacheStatic(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		h.ServeHTTP(w, r)
	})
}
