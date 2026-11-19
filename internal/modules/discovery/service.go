package discovery

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/alumasinde/profy/internal/authz"
	"github.com/alumasinde/profy/internal/i18n"
	"github.com/alumasinde/profy/internal/modules/providers"
	"github.com/alumasinde/profy/internal/modules/settings"
	"github.com/alumasinde/profy/internal/security"
)

// PageSource lists indexable managed pages for the sitemap.
type PageSource func(ctx context.Context) ([]SitemapPage, error)

// SitemapPage is one managed page URL.
type SitemapPage struct {
	Path      string
	UpdatedAt time.Time
}

// Service is the discovery read model.
type Service struct {
	db        *sql.DB
	log       *slog.Logger
	settings  *settings.Service
	authz     *authz.Service
	i18n      *i18n.Catalogue
	providers *providers.Service
	pages     PageSource
	cat       *catalog
	limiter   *security.Limiter
}

// Deps are the dependencies.
type Deps struct {
	DB        *sql.DB
	Log       *slog.Logger
	Settings  *settings.Service
	Authz     *authz.Service
	I18n      *i18n.Catalogue
	Providers *providers.Service
	Pages     PageSource
}

// New builds the service.
func New(d Deps) *Service {
	return &Service{db: d.DB, log: d.Log, settings: d.Settings, authz: d.Authz, i18n: d.I18n, providers: d.Providers,
		pages: d.Pages, cat: &catalog{db: d.DB}, limiter: security.NewLimiter()}
}

// Snapshot returns current reference data.
func (s *Service) Snapshot(ctx context.Context) (*Snapshot, error) { return s.cat.get(ctx) }
