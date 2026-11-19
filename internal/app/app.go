// Package app wires configuration, the database and modules into a runnable application.
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/alumasinde/profy/internal/authz"
	"github.com/alumasinde/profy/internal/config"
	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/http/routes"
	"github.com/alumasinde/profy/internal/i18n"
	"github.com/alumasinde/profy/internal/jobsqueue"
	"github.com/alumasinde/profy/internal/modules/auth"
	"github.com/alumasinde/profy/internal/modules/categories"
	"github.com/alumasinde/profy/internal/modules/locations"
	"github.com/alumasinde/profy/internal/modules/notifications"
	"github.com/alumasinde/profy/internal/modules/pages"
	"github.com/alumasinde/profy/internal/modules/public"
	"github.com/alumasinde/profy/internal/modules/roles"
	"github.com/alumasinde/profy/internal/modules/seo"
	"github.com/alumasinde/profy/internal/modules/services"
	"github.com/alumasinde/profy/internal/modules/settings"
	"github.com/alumasinde/profy/internal/security"
	"github.com/alumasinde/profy/web"
)

// App holds constructed components.
type App struct {
	Config   *config.Config
	Log      *slog.Logger
	DB       *sql.DB
	I18n     *i18n.Catalogue
	Settings *settings.Service
	Authz    *authz.Service
	Auth     *auth.Service
	Queue    *jobsqueue.Queue
	Senders  map[string]notifications.Sender
	Handler  http.Handler
}

// NewLogger returns JSON logs outside local development (§19.4).
func NewLogger(cfg *config.Config) *slog.Logger {
	if cfg.IsLocal() {
		return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// Build constructs the application from configuration and an open database.
func Build(cfg *config.Config, log *slog.Logger, db *sql.DB) (*App, error) {
	cat, err := i18n.Load(web.FS, "i18n")
	if err != nil {
		return nil, err
	}
	st := settings.New(db, time.Minute)
	az := authz.New(db, time.Minute)

	senders, err := buildSenders(cfg, log)
	if err != nil {
		return nil, err
	}
	list := make([]notifications.Sender, 0, len(senders))
	for _, s := range senders {
		list = append(list, s)
	}
	authSvc := auth.NewService(auth.Deps{
		DB: db, Config: cfg, Log: log, Settings: st, I18n: cat, Authz: az,
		Limiter: security.NewLimiter(), Senders: list,
	})

	pub, err := public.New(web.FS, cat, st, cfg.SiteURL, log)
	if err != nil {
		return nil, err
	}
	q := jobsqueue.New(db, log)
	auth.RegisterMaintenance(q, db)

	// Phase 3 modules: content, catalogue, locations, redirects.
	redirects := seo.New(db)
	pageSvc := pages.New(db, az)
	catSvc := categories.New(db, az)
	svcSvc := services.New(db, az, catSvc)
	catSvc.ServiceStats, catSvc.ServiceCounts = svcSvc.StatsForCategory, svcSvc.CountsByCategory
	pub.Nav = func(ctx context.Context, menu string) []public.NavLink {
		var out []public.NavLink
		for _, it := range pageSvc.PublicMenu(ctx, menu) {
			out = append(out, public.NavLink{Label: it.Label, URL: it.URL, External: len(it.URL) > 8 && it.URL[:8] == "https://"})
		}
		return out
	}
	pub.Redirect = func(ctx context.Context, path string) string {
		to, _ := redirects.Lookup(ctx, path)
		return to
	}

	h, err := routes.New(routes.Deps{
		DB: db, Log: log, WebFS: web.FS, I18n: cat, Settings: st, Public: pub, Auth: authSvc,
		APIs: []routes.APIModule{
			auth.NewHandler(authSvc),
			roles.NewHandler(roles.New(db, az), az),
			settings.NewAdminHandler(st, az, cat.Has),
			pages.NewAdminHandler(pageSvc, az),
			seo.NewHandler(redirects, az),
			categories.NewHandler(catSvc, az),
			services.NewHandler(svcSvc, az),
			locations.NewHandler(locations.New(db, az), az),
		},
		Pages:      []routes.PublicModule{pages.NewPublicHandler(pageSvc, pub, az)},
		TrustProxy: cfg.TrustProxy, Secure: cfg.SecureCookies(),
	})
	if err != nil {
		return nil, err
	}
	return &App{Config: cfg, Log: log, DB: db, I18n: cat, Settings: st, Authz: az, Auth: authSvc,
		Queue: q, Senders: senders, Handler: h}, nil
}

func buildSenders(cfg *config.Config, log *slog.Logger) (map[string]notifications.Sender, error) {
	out := map[string]notifications.Sender{}
	switch cfg.EmailAdapter {
	case "fake":
		out[auth.ChannelEmail] = notifications.NewFake(auth.ChannelEmail, log)
	case "smtp":
		out[auth.ChannelEmail] = &notifications.SMTPSender{Host: cfg.SMTPHost, Port: cfg.SMTPPort,
			Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, From: cfg.SMTPFrom}
	}
	switch cfg.SMSAdapter {
	case "fake":
		out[auth.ChannelSMS] = notifications.NewFake(auth.ChannelSMS, log)
	case "africastalking":
		out[auth.ChannelSMS] = &notifications.AfricasTalkingSender{BaseURL: cfg.ATBaseURL, Username: cfg.ATUsername,
			APIKey: cfg.ATAPIKey, SenderID: cfg.ATSenderID}
	}
	if cfg.IsProduction() {
		for ch, s := range out {
			if _, fake := s.(*notifications.FakeSender); fake {
				return nil, fmt.Errorf("refusing to start: fake %s adapter in production", ch)
			}
		}
	}
	return out, nil
}

// Serve runs the HTTP server and background worker until ctx is cancelled.
func (a *App) Serve(ctx context.Context) error {
	if ok, err := database.MigrationsCurrent(ctx, a.DB); err != nil {
		return err
	} else if !ok {
		if !a.Config.IsLocal() {
			return errors.New("migrations are pending: run `server migrate up` as a deploy step")
		}
		a.Log.Warn("migrations are pending; run `server migrate up`")
	}
	srv := &http.Server{
		Addr: a.Config.HTTPAddr, Handler: a.Handler,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second,
		MaxHeaderBytes: 64 << 10,
	}
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	workersDone := make(chan struct{})
	go func() {
		defer close(workersDone)
		go auth.ScheduleMaintenance(workerCtx, a.DB, a.Log)
		a.Queue.Run(workerCtx, a.Config.WorkerConcurrency, a.Config.WorkerPollEvery)
	}()

	errCh := make(chan error, 1)
	go func() {
		a.Log.Info("server listening", "addr", a.Config.HTTPAddr, "env", a.Config.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		stopWorkers()
		<-workersDone
		return err
	}
	a.Log.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), a.Config.ShutdownTimeout)
	defer cancel()
	err := srv.Shutdown(shutCtx)
	a.Auth.WaitDeliveries()
	stopWorkers()
	<-workersDone
	return err
}

// WebFS exposes the embedded web filesystem (for tests).
func WebFS() fs.FS { return web.FS }
