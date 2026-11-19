# HudumaLink — Product & Engineering Plan

> **Working name:** HudumaLink
> **Name policy:** This is a configurable working name, not a permanent product identity. The public name, logo, colors, tagline, domain, contact details and SEO defaults must be changeable without code changes or database migrations.
> **Product:** A Kenya-focused local services discovery marketplace connecting customers with individual professionals and service businesses.
> **Document status:** v2 — consolidated product and technical blueprint (revised 2026-11-19).
> **Decision status:** Items marked **(Proposed)** are recommended defaults that need owner confirmation. Open items are tracked in [§25 Open decisions](#25-open-decisions).

### Revision summary (v1 → v2)

- Merged the late "mandatory" sections (name fields, granular permissions) into the main body so there is one source of truth; removed contradictions (`full_name`, coarse permission examples).
- Reordered the roadmap: authentication now precedes the admin features that depend on it; jobs moved after the MVP launch, matching the scope in §2.
- Fixed technical defects: MySQL spatial indexes cannot be built on a nullable column; spatial axis order depends on the constructor function; Kenyan numbers include the `+2541…` range.
- Added missing product rules: provider verification levels, listing lifecycle, enquiry lifecycle, review eligibility, contact-reveal policy and moderation.
- Added launch strategy and success metrics, non-functional targets, operations, language (English/Swahili) readiness, API conventions, and an open-decisions log.
- Moved AI coding-agent instructions to an appendix and merged the duplicate build-order list into the roadmap.
- Normalized formatting (lists and tables).

------------------------------------------------------------------------

## Contents

1. Product vision
2. Product scope
3. Launch strategy and success metrics
4. Brand and configuration strategy
5. Users, roles and permissions
6. Technical architecture
7. Design and user experience
8. Dynamic content and no-hardcoding policy
9. SEO architecture
10. Location system and geographic search
11. Identity, authentication and account security
12. Marketplace rules and lifecycles
13. Database architecture
14. Indexing and query performance
15. API design
16. Security, privacy and compliance
17. Media and image handling
18. Notifications
19. Non-functional requirements and operations
20. Testing and quality strategy
21. Implementation roadmap
22. MVP acceptance criteria
23. Key architectural decisions to preserve
24. Terminology
25. Open decisions
- Appendix A — AI coding-agent instructions

------------------------------------------------------------------------

## 1. Product vision

Build a public-first marketplace where people can discover local professionals and businesses by service and location, inspect public profiles, compare available information and contact providers directly.

The product is more than a business directory and must not be designed as a marketing website that hides the marketplace behind registration. The homepage itself is the entry point to the marketplace.

### Product promise

**Find trusted professionals near you.**

Customers can discover providers without an account. Registration is required only for identity-dependent features: submitting enquiries, saving providers, writing reviews and tracking enquiries or jobs.

### Product principles

1. **Public-first discovery:** Search, categories, provider profiles and useful location pages are accessible without login.
2. **Dynamic by design:** Categories, services, locations, provider content, branding, navigation, SEO metadata, feature flags and operational settings are database-managed.
3. **One source of truth:** Go owns business rules and validation. Svelte and the future Kotlin app consume the same API.
4. **Modular monolith:** One deployable Go application with clear internal module boundaries. No premature microservices.
5. **Mobile-first:** Design and test phone layouts first (including low-end Android on mobile data), then tablet and desktop.
6. **Privacy and safety:** Collect only necessary personal data; protect contact details and private documents; provide moderation and reporting.
7. **No marketplace payments initially:** The platform connects customers and providers. It does not collect payments or manage payouts.
8. **SEO is a product capability:** Public pages are crawlable, server-rendered and have configurable metadata.
9. **Honest trust signals:** Never fabricate providers, reviews, ratings, coordinates or verification. Every badge means exactly what its label says.

------------------------------------------------------------------------

## 2. Product scope

### 2.1 MVP — first public release

#### Public marketplace

- Responsive homepage with service and location search.
- Dynamic service categories and subcategories.
- Provider search and filtering.
- Public provider profiles with services, service areas and portfolio.
- Public contact actions controlled by provider privacy settings (§12.5).
- Location pages only where there is enough real provider inventory to be useful (§9.6).
- How-it-works, about, contact, privacy and terms pages.
- Sitemap, robots rules, canonical URLs and structured data.
- Search-engine-friendly server-rendered HTML for public pages.

#### Customer account

- Registration and login (§11).
- Email and/or SMS verification according to configured channels.
- Customer profile and account security (sessions, devices).
- Submit enquiries to providers.
- Save/favourite providers.
- View enquiry history.
- Submit reviews subject to moderation and eligibility rules (§12.4).
- Report a provider, listing or review.
- Request account data export or deletion.

#### Provider account

- Register as an individual or business.
- Create and edit public profile.
- Add services and descriptions.
- Select base location and service areas; set service radius where applicable.
- Upload profile, cover and portfolio media.
- Configure phone, WhatsApp and email visibility.
- Submit verification documents.
- Manage enquiries.
- Submit for review, publish or pause listing, subject to moderation rules (§12.2).
- View basic profile and contact-action analytics.

#### Administration

- Dashboard and operational metrics.
- Manage users, roles and permissions.
- Manage providers, verification and listing status.
- Manage categories, services and locations.
- Moderate reviews, reports and media.
- Manage site identity, branding, pages and navigation.
- Manage SEO defaults, page-level metadata and redirects.
- Manage notification templates and enabled delivery channels.
- View audit logs.
- Configure feature flags and platform settings.

> **Note:** "My Jobs" / "Jobs" navigation items belong to the follow-up release. In the MVP they are hidden behind the `jobs` feature flag.

### 2.2 Follow-up release

- Lightweight job records created from accepted enquiries.
- Job status tracking: `pending`, `accepted`, `in_progress`, `completed`, `cancelled`.
- Job status history and operational reporting.
- Review eligibility upgraded to prefer completed jobs.
- Provider replies to reviews.
- Provider availability information.
- More detailed provider analytics.
- Customer notifications and provider reminders.
- Better search ranking controls and moderation tooling.

Jobs are operational records only. They do not represent payments, invoices, escrow or financial settlement.

### 2.3 Later releases

- Kotlin Android application using the same API.
- Android biometric unlock using `BiometricPrompt` and Android Keystore.
- Push notifications.
- Optional GPS-based search in the app.
- Saved customer locations.
- Organization accounts (multiple team members managing one business profile).
- Full Swahili translation of UI and content.
- More advanced search and provider insights.

### 2.4 Explicitly out of scope initially

- Payment gateway, M-Pesa collection, wallet, escrow and provider payouts.
- Paid promotion / sponsored listings and subscription billing.
- Complex appointment calendars.
- Real-time provider tracking.
- In-platform chat.
- AI recommendations.
- Microservices and distributed infrastructure.
- Mandatory customer registration before browsing.

------------------------------------------------------------------------

## 3. Launch strategy and success metrics

A marketplace with no providers is useless to customers, and one with no customers is useless to providers. The engineering plan must support a deliberate cold-start strategy.

### 3.1 Launch geography (Proposed)

- Launch in a **limited pilot region** (for example, Nairobi and Kiambu counties) and a **limited set of high-demand categories** (for example, plumbing, electrical, cleaning, beauty, moving, appliance repair).
- Seed the full Kenya location hierarchy (all 47 counties), but mark only pilot locations as `selectable` and potentially `indexable` at launch.
- Recruit and onboard providers **before** public launch so search results are not empty. Support assisted onboarding: a support/admin user creates a draft profile that the provider then claims and confirms (with consent and verification).
- Never publish fabricated or scraped provider listings.

### 3.2 Empty-state behaviour

- Searches with no results must show nearby areas, broader coverage, related services and a "notify me / request a provider" option rather than a dead end. (The "request" capture is a lightweight demand signal for recruitment; it is not a job.)
- Log zero-result searches (service + location) to guide provider recruitment.

### 3.3 Success metrics

| Area | Metric |
|---|---|
| Supply | Published providers per pilot category × area; % profiles meeting completeness threshold; % verified |
| Demand | Unique searches; search-to-profile-view rate; zero-result search rate |
| Liquidity | Profile view → contact action rate; enquiries per published provider; % enquiries responded within 24h |
| Quality | Review volume and average; report rate per 1,000 profile views; median provider response time |
| SEO | Indexed eligible pages; organic sessions; Core Web Vitals pass rate |
| Retention | Returning customers (30-day); providers active in last 30 days |

Metrics are computed from `analytics_events` and operational tables, not from audit logs.

------------------------------------------------------------------------

## 4. Brand and configuration strategy

The application name must never be embedded in templates, Svelte components, API responses or business logic. The repository and Go module names are internal and need not change when the public brand changes.

Use a central settings service with typed settings and an admin interface.

### 4.1 Configurable brand settings

| Key | Notes |
|---|---|
| `site_name`, `site_short_name` | Public names |
| `site_tagline`, `site_description` | Used for homepage and default metadata |
| `site_url` | Canonical origin; validated |
| `support_email`, `support_phone`, `whatsapp_number` | Public contact |
| `social_links` | Structured list |
| `logo_light`, `logo_dark`, `favicon`, `default_og_image` | Media references |
| `primary_color`, `secondary_color`, `accent_color` | Validated against contrast rules |
| `default_locale`, `default_timezone` (`Africa/Nairobi`), `default_country` (`KE`), `default_currency` (`KES`) | Regional defaults |

The working name HudumaLink is stored as configuration and may be replaced without changing application code.

### 4.2 Configuration rules

- Store operational settings in MySQL, with safe defaults in application configuration for bootstrap and recovery.
- Use typed keys, validation and documented allowed values.
- Cache settings in memory with explicit invalidation after admin updates (single instance: in-process; multiple instances: version counter polled from the database).
- Do not read the database for every setting on every request.
- Keep secrets (SMTP credentials, SMS API keys, signing/HMAC secrets, database passwords) in environment variables or a secret manager, never in settings tables.
- Record administrative setting changes in the audit log.
- Separate public settings (safe to expose to the frontend) from private operational settings.
- Support maintenance mode and feature flags.

------------------------------------------------------------------------

## 5. Users, roles and permissions

A user is an identity. Roles determine what that identity may do. A user may hold more than one role (for example, a provider who is also a customer).

### 5.1 Initial roles

| Role | Intended access |
|---|---|
| Customer | Own profile, favourites, enquiries, eligible reviews and (later) own jobs |
| Provider | Own provider profile, services, service areas, portfolio, enquiries, jobs and own analytics |
| Support | Limited user and enquiry lookup; no verification, role management or sensitive document access by default |
| Moderator | Assigned provider/review/report/media moderation; no platform settings or role management |
| Administrator | Only the explicit administrative permissions assigned to that role |
| Super administrator | Restricted break-glass access to high-impact platform and authorization controls; held by very few accounts |

Do not assume every provider is a separate user account. A provider profile belongs to a user in the MVP and, later, to an organization with multiple authorized members (`provider_members`).

The first super administrator is created with a server CLI command (for example `server admin create`) — never through a default seeded password.

### 5.2 Authorization principles

- Permissions are explicit, granular, documented and enforced by the Go backend. Roles are collections of permissions; code checks **permissions**, not role names.
- Hiding a button in Svelte is not authorization.
- Support global permissions, resource ownership checks and scoped permissions where needed (for example, a moderator assigned to a subset of content).
- Every check **fails closed**: missing role, missing permission, unavailable authorization data or invalid scope denies access.
- Use least privilege. Do not grant every permission to the `administrator` role. Seed a documented initial permission set.
- Protect high-privilege role and permission changes; record every change in the audit log.

### 5.3 Permission naming convention

Use `resource.action`, with `_own` / `_any` suffixes where ownership matters. Use distinct permissions for viewing, creating, editing, publishing, verifying, moderating, exporting, deleting and managing settings. Do not create broad permissions such as `admin.access`.

### 5.4 Initial permission catalogue

**Dashboard and platform**
`dashboard.view`, `dashboard.view_admin_metrics`, `platform.settings.view`, `platform.settings.update`, `platform.feature_flags.view`, `platform.feature_flags.update`, `platform.maintenance.enable`, `platform.maintenance.disable`

**Users and accounts**
`users.view`, `users.view_any`, `users.view_sensitive`, `users.create`, `users.update`, `users.suspend`, `users.restore`, `users.delete_request.review`, `users.export`, `users.roles.assign`, `users.sessions.revoke`

**Roles and permissions**
`roles.view`, `roles.create`, `roles.update`, `roles.delete`, `permissions.view`, `roles.permissions.assign`

**Providers**
`providers.view`, `providers.view_any`, `providers.create`, `providers.update_own`, `providers.update_any`, `providers.publish_own`, `providers.unpublish_own`, `providers.publish_any`, `providers.suspend`, `providers.restore`, `providers.verify`, `providers.reject_verification`, `providers.assign_featured`, `providers.remove_featured`, `providers.export`

**Provider services and coverage**
`provider_services.view_own`, `provider_services.create_own`, `provider_services.update_own`, `provider_services.delete_own`, `provider_services.manage_any`, `provider_areas.view_own`, `provider_areas.manage_own`, `provider_areas.manage_any`

**Categories and service catalogue**
`categories.view`, `categories.create`, `categories.update`, `categories.publish`, `categories.archive`, `services.view`, `services.create`, `services.update`, `services.publish`, `services.archive`

**Locations**
`locations.view`, `locations.create`, `locations.update`, `locations.archive`, `locations.manage_aliases`, `locations.merge`, `locations.manage_coordinates`

**Enquiries and jobs**
`inquiries.view_own`, `inquiries.view_any`, `inquiries.create`, `inquiries.update_own`, `inquiries.update_assigned`, `inquiries.close`, `jobs.view_own`, `jobs.view_any`, `jobs.create`, `jobs.update_own`, `jobs.update_any`, `jobs.cancel`, `jobs.complete`

**Reviews, reports and moderation**
`reviews.view`, `reviews.view_any`, `reviews.create`, `reviews.moderate`, `reviews.hide`, `reviews.restore`, `reports.create`, `reports.view`, `reports.assign`, `reports.resolve`, `reports.dismiss`, `moderation.actions.view`, `moderation.actions.create`

**Media**
`media.view_own`, `media.upload_own`, `media.delete_own`, `media.view_private`, `media.moderate`, `media.delete_any`

**SEO, pages and navigation**
`seo.view`, `seo.update_defaults`, `seo.update_pages`, `seo.manage_redirects`, `seo.manage_sitemap_rules`, `pages.view`, `pages.create`, `pages.update`, `pages.publish`, `pages.archive`, `navigation.view`, `navigation.update`

**Notifications**
`notifications.view_own`, `notifications.view_any`, `notifications.send`, `notification_templates.view`, `notification_templates.create`, `notification_templates.update`, `notification_templates.publish`, `notification_channels.view`, `notification_channels.configure`

**Analytics and audit**
`analytics.view_own`, `analytics.view_provider`, `analytics.view_platform`, `analytics.export`, `audit_logs.view`, `audit_logs.view_sensitive`, `audit_logs.export`

Audit logs are append-only through the application. There is no permission to edit or delete audit records via ordinary CRUD screens.

### 5.5 Ownership and scope checks

A permission such as `providers.update_own` is not sufficient by itself. The service layer must also confirm the provider belongs to the authenticated user, or that the user separately holds `providers.update_any`.

Apply the same rule to own vs. any enquiry, job, media, analytics and service areas.

Use centralized authorization policies in Go (for example `authz.Can(ctx, actor, "providers.update", provider)`); do not repeat ad hoc checks across handlers.

### 5.6 Permission data model

- Tables: `roles`, `permissions`, `user_roles`, `role_permissions`.
- `permissions`: `id`, `permission_key` UNIQUE, `description`, `module`, `created_at`.
- `role_permissions`: UNIQUE(`role_id`, `permission_id`), foreign keys to both parents.
- Permission keys are defined in code (so code and data cannot drift) and synchronized to the table by a migration/seed step; role-to-permission assignments are data.
- Consider `user_permission_overrides` only when a concrete requirement exists. Not in the MVP.
- Cache a user's effective permissions per session; invalidate on role or assignment change.

------------------------------------------------------------------------

## 6. Technical architecture

### 6.1 Stack

| Layer | Technology | Responsibility |
|---|---|---|
| Backend | Go | HTTP server, business logic, authorization, validation, API |
| Database | MySQL 8 (InnoDB) | Transactional and relational data, spatial queries |
| Public HTML | Go `html/template` | Server-rendered SEO pages |
| Web app | Svelte 5 + TypeScript | Dashboards, auth flows, rich forms, progressive enhancement |
| Frontend build | Vite | Compile frontend assets, embedded into the Go binary |
| API | REST `/api/v1` | Shared by web app and future mobile app |
| Media | Filesystem or S3-compatible object storage | Public images and private verification documents |
| Mobile (later) | Kotlin | Android client using the same API |

### 6.2 Library and tooling choices (Proposed)

Keep dependencies few and boring. Prefer the standard library.

| Concern | Proposed choice | Rationale |
|---|---|---|
| Routing | `net/http` `ServeMux` (Go 1.22+ method and path patterns) | No framework needed |
| Logging | `log/slog` (JSON in production) | Standard library, structured |
| MySQL driver | `github.com/go-sql-driver/mysql` with `database/sql` | De-facto standard |
| Query layer | Hand-written SQL, optionally generated with `sqlc` | Parameterized, explicit, type-safe |
| Migrations | `goose` or `golang-migrate` (choose one) | Versioned up/down SQL files |
| Password/PIN hashing | `golang.org/x/crypto/argon2` | Argon2id |
| Image processing | Standard `image` packages first; add a library only if WebP/AVIF output is required | Avoid CGO unless justified |
| Frontend app | SvelteKit with `adapter-static` (SPA fallback), or plain Svelte + small router — see §25 | Static assets only; no Node server in production |
| Frontend tests | Vitest; Playwright for end-to-end | Standard tooling |

### 6.3 Rendering strategy: public pages vs. application

Use a hybrid model.

**Go-rendered public pages**

- Homepage.
- Category and service pages.
- Service + location landing pages.
- Provider profile pages.
- Public location pages.
- Static information pages.

Go returns complete HTML with title, description, canonical URL, Open Graph metadata, structured data and meaningful visible content. Public pages must work without JavaScript; keep the public-page JS budget small (target ≤ 50 KB gzipped).

**Svelte application**

- Interactive search filter enhancement on public pages.
- Registration, login and account recovery.
- Customer dashboard (`/account/*`).
- Provider dashboard (`/provider/*`).
- Admin dashboard (`/admin/*`).

Go serves the Svelte app's `index.html` for these application path prefixes (client-side routing) and returns `noindex` for them. Build static assets with Vite and embed them with `embed.FS`; hashed filenames with long-lived cache headers.

### 6.4 Web vs. mobile API authentication

- **Web:** opaque session cookie (`HttpOnly`, `Secure`, `SameSite=Lax`) plus CSRF protection on state-changing requests.
- **Android (later):** short-lived access token plus rotating refresh token bound to a registered device. CSRF does not apply to bearer-token requests.
- Both resolve to the same authenticated principal in Go middleware, so business code does not care which client called it.

### 6.5 Deployment model

The deliverable is one Go executable containing application code, templates, compiled Svelte assets and static resources.

External dependencies:

- MySQL server.
- Persistent media storage.
- Email provider.
- SMS provider (Kenyan aggregator; see §18).
- Reverse proxy / TLS termination (for example Caddy or Nginx), optional.

A standalone binary does not mean the database or third-party delivery providers are embedded.

Background work (sending notifications, image processing, sitemap regeneration, expiring enquiries, rating recalculation) runs in in-process workers fed by a MySQL-backed job/outbox table so work survives restarts. No external queue initially.

### 6.6 Repository structure

```text
hudumalink/
├── cmd/
│   └── server/
│       └── main.go              # serve, migrate, admin create, seed
├── internal/
│   ├── app/                     # wiring / dependency construction
│   ├── config/
│   ├── database/
│   ├── http/
│   │   ├── middleware/
│   │   ├── routes/
│   │   └── response/
│   ├── security/                # hashing, tokens, CSRF, rate limiting
│   ├── authz/                   # permission policies
│   ├── jobsqueue/               # background worker + outbox
│   ├── modules/
│   │   ├── auth/
│   │   ├── users/
│   │   ├── roles/
│   │   ├── providers/
│   │   ├── verification/
│   │   ├── categories/
│   │   ├── services/
│   │   ├── locations/
│   │   ├── search/
│   │   ├── inquiries/
│   │   ├── jobs/
│   │   ├── reviews/
│   │   ├── favorites/
│   │   ├── reports/
│   │   ├── moderation/
│   │   ├── media/
│   │   ├── notifications/
│   │   ├── pages/
│   │   ├── seo/
│   │   ├── settings/
│   │   ├── analytics/
│   │   ├── audit/
│   │   └── administration/
│   ├── storage/
│   └── shared/
├── web/
│   ├── embed.go
│   ├── templates/
│   │   ├── layouts/
│   │   ├── pages/
│   │   └── partials/
│   ├── i18n/                    # UI string catalogues (en, sw)
│   └── static/
├── frontend/
│   ├── src/
│   │   ├── lib/
│   │   ├── components/
│   │   ├── features/
│   │   ├── routes/
│   │   │   ├── auth/
│   │   │   ├── account/
│   │   │   ├── provider/
│   │   │   └── admin/
│   │   └── app.ts
│   ├── package.json
│   └── vite.config.ts
├── migrations/
├── seeds/                       # reference data: countries, counties, initial catalogue
├── scripts/
├── tests/
├── docs/
├── AGENTS.md
├── go.mod
├── Makefile
└── README.md
```

Each Go module normally separates:

- `handler.go` — HTTP transport.
- `service.go` — business rules and orchestration.
- `repository.go` — persistence.
- `model.go` — internal data structures.
- `dto.go` — request and response structures.
- `validation.go` — input validation.

Keep module interfaces explicit. Modules call each other through services, not by reading each other's tables. Avoid a giant global utility package and keep business logic out of HTTP handlers.

------------------------------------------------------------------------

## 7. Design and user experience

### 7.1 Visual direction

A premium, modern, trustworthy local-services marketplace aesthetic:

- Clear typography and strong visual hierarchy.
- Light neutral backgrounds.
- One configurable primary brand color and one accent color.
- Restrained shadows and borders.
- Rounded but not excessively pill-shaped cards.
- High-quality provider photography.
- Consistent iconography.
- Accessible color contrast (WCAG 2.2 AA).
- Generous spacing without wasting mobile screen space.

Do not hardcode brand colors in components. Use CSS variables populated from the active theme configuration or a generated theme stylesheet. Reject theme colors that fail contrast checks.

### 7.2 Public navigation

**Desktop:** brand logo · Find Services · Categories · How It Works · For Professionals · Login · Join Free.

**Mobile:** compact brand header · search access · menu with public links and account actions. Add bottom navigation only if usability testing shows value.

Navigation items are database-managed (§8).

### 7.3 Homepage layout

The homepage is a working marketplace, not a corporate brochure. Section order:

1. **Header:** logo, discovery links, provider registration and login.
2. **Hero/search:** headline, service search, location selector and search action.
3. **Popular categories:** database-driven category cards.
4. **Providers near selected location:** real inventory, never hardcoded examples.
5. **Popular services:** dynamic service links.
6. **How it works:** find, compare, contact.
7. **Featured providers:** only providers selected by an explicit, documented curation policy.
8. **For professionals:** value of creating a free profile.
9. **Trust and safety:** what verification means, reporting, safe-contact guidance.
10. **Footer:** categories, locations, policies, support and brand details.

The homepage must work with no featured providers and no location permission, with useful fallback content and a manual location selector.

### 7.4 Provider cards

Show only available, public information:

- Profile image or safe placeholder.
- Provider/business display name.
- Main service or category.
- Town/area and relevant coverage ("Based in Ruiru · Serves Ruaka").
- Verification badges with precise wording (§12.1).
- Rating and review count only when the minimum review threshold is met.
- Short description.
- View profile action.
- Call / WhatsApp actions when provider settings permit.

Never imply a provider is "verified" because a phone number is verified.

### 7.5 Provider profile

Sections: name, photo/logo and cover · verification badges and what they mean · description and experience · services (with optional price range) · service areas · portfolio · contact options · working hours if provided · reviews · "Report this listing".

Visitors can view the profile and use public contact actions without an account. Enquiry, favourite and review actions require authentication; the login flow returns the user to the same profile and action.

### 7.6 Account and dashboard UX

**Customer:** Overview · Find Services · Saved Providers · My Enquiries · My Jobs *(flagged)* · My Reviews · Profile · Security.

**Provider:** Overview · Public Profile · Services · Service Areas · Portfolio · Verification · Enquiries · Jobs *(flagged)* · Reviews · Analytics · Account and Security.

**Admin:** Overview · Users and Roles · Providers and Verification · Categories and Services · Counties, Towns and Areas · Enquiries and Jobs · Reviews, Reports and Moderation · Media · SEO Manager · Navigation and Pages · Notifications · Audit Logs · Site Settings.

Admin navigation items are shown only when the user holds the corresponding permission (display only; the backend still enforces).

Provider onboarding should be a guided step-by-step flow with a visible profile-completeness indicator showing what is still required before submission.

### 7.7 Language and localisation

- English is the launch language. The architecture must be **Swahili-ready** from day one.
- All UI strings (Go templates and Svelte) come from string catalogues keyed by message ID; no user-facing literals in components.
- Store `preferred_locale` on users. Format dates, times (`Africa/Nairobi`) and currency (`KES`) through locale-aware helpers.
- Database-managed content (categories, services, pages, SEO text) should be designed so a translation table can be added later without schema rewrites (for example `category_translations`). Full translation is a later release.
- If localized public URLs are introduced later, use `hreflang` and avoid duplicate-content issues.

------------------------------------------------------------------------

## 8. Dynamic content and no-hardcoding policy

The application must not rely on hardcoded business data.

### 8.1 Database-managed data

- Product name and brand identity; logos, favicon and theme colors.
- Tagline, default site description, contact information and social links.
- Header and footer navigation.
- Static page content.
- Categories, hierarchy, services and descriptions.
- Counties, towns, areas and aliases.
- Provider listings and service coverage.
- Featured-provider selections and curation rules.
- SEO defaults and page-level overrides.
- Notification templates.
- OTP delivery channel settings.
- Feature flags.
- Contact visibility options.
- Verification labels and explanatory text.
- Thresholds: indexing eligibility, minimum reviews to show a rating, enquiry expiry, rate-limit values within safe bounds.

### 8.2 Code-defined

- Security-critical rules and their floors (for example, an admin can tighten rate limits but not disable them).
- Data types and validation constraints.
- Permission keys and enforcement.
- Notification adapter interfaces.
- State-transition rules (§12).
- Core ranking algorithm implementation.
- Database schema and migrations.
- Safe fallback defaults.

Business content is data-driven; security and integrity rules are never editable as arbitrary scripts by administrators.

### 8.3 Admin content-management features

- Change application branding.
- Edit default SEO metadata and per-entity overrides.
- Manage navigation links.
- Publish and unpublish content; preview public pages before publishing.
- Configure featured providers.
- Manage redirects when public slugs change (automatic on slug change, editable manually).
- Inspect sitemap eligibility and why a page is or isn't indexable.

Record changes to critical content and settings in the audit log.

------------------------------------------------------------------------

## 9. SEO architecture

SEO is implemented in the first release.

### 9.1 Public URL strategy

Stable, readable, lowercase, hyphen-separated slugs:

```text
/
/categories
/services/plumbing
/services/plumbing/ruaka
/services/cleaning/nairobi
/professionals/john-plumbing-services
/locations/kiambu
/how-it-works
/join-as-professional
/about
/contact
/privacy-policy
/terms
```

- Never use database IDs in public URLs. Use an internal numeric primary key plus a unique slug (public pages) or UUID (API resources).
- Location slugs must be unique within the URL namespace they are used in. Where two areas share a name (for example, the same estate name in two counties), disambiguate the slug (`kahawa-nairobi`). Resolve this at slug-creation time, not at request time.
- Trailing slashes, uppercase and query-string variants redirect (301) or canonicalize to one form.

### 9.2 Metadata management

Global defaults plus per-entity overrides. Fields:

- `meta_title`, `meta_description`
- `canonical_url` (optional override; validate allowed host and URL)
- `robots_index`, `robots_follow`
- `og_title`, `og_description`, `og_image_media_id`
- `seo_status`, `seo_updated_at`

**Decision:** store SEO fields directly on core public entities (providers, categories, services, locations, managed pages) to retain referential integrity and simplify queries. Use a separate `seo_landing_pages` table for service × location combinations that receive manual overrides, keyed by `(service_id, location_type, location_id)` with real foreign keys where possible.

### 9.3 Metadata generation

When no override exists, generate defaults from real data:

- Homepage: configured site name and description.
- Category: category name plus brand.
- Service/location: service name, area name and county.
- Provider: display name, primary service and service area.

Avoid repetitive or misleading descriptions. Administrators may override.

### 9.4 Technical SEO requirements

- Server-render public HTML through Go templates.
- Unique title and meta description for each indexable page.
- One clear H1 per page.
- Canonical URL on every public page.
- Open Graph and social metadata.
- Correct HTTP status codes (200, 301, 404, 410 for permanently removed providers, 503 during maintenance).
- XML sitemap index and dynamic sitemap files (split by type; ≤ 50,000 URLs per file).
- `robots.txt` generated from configuration.
- Breadcrumbs and internal links.
- JSON-LD structured data where accurate.
- Descriptive image alt text.
- 301 redirects for changed slugs; avoid redirect chains.
- `noindex` for dashboards, login, filter/sort combinations and thin/duplicate pages.
- Exclude unpublished, paused, suspended and unapproved content from sitemaps.
- Never expose private provider or customer information in structured data.

### 9.5 Structured data

Use only types supported by visible page content:

- `Organization` and `WebSite` for the platform.
- `BreadcrumbList` for breadcrumbs.
- `ItemList` for genuine provider/category collections.
- `LocalBusiness` (or an accurate subtype) for eligible provider businesses.

Never invent ratings, review counts, addresses, opening hours or prices. Validate JSON-LD with Google's Rich Results Test and the Schema.org Validator.

### 9.6 Indexing policy for location pages

Do not index every service × location combination. A landing page is indexable only when it:

- Has a valid, active service and a selectable location.
- Has at least *N* active, public, relevant providers (configurable; for example N = 3).
- Contains useful, non-duplicated content.
- Has a stable canonical URL.
- Is not a filtered duplicate of another page.

Thin combinations are `noindex` and become indexable automatically when thresholds are met. Pages that drop below the threshold should not flap: apply hysteresis (for example, stay indexed until the count stays below threshold for a configured period).

### 9.7 Search-engine operations

- Configure Google Search Console (and Bing Webmaster Tools).
- Submit the sitemap.
- Monitor indexing and crawl errors.
- Track Core Web Vitals.
- Monitor broken links and redirect chains.
- Keep an SEO change history for important public pages (via audit log).

------------------------------------------------------------------------

## 10. Location system and geographic search

Location is modelled explicitly. Never store locations as comma-separated strings or rely only on free-text addresses.

### 10.1 Location hierarchy

```text
Country
  └── County
       └── Town / City
            └── Area / Estate / Neighbourhood
```

- Kenya is the initial country; don't hardcode Kenya into tables or algorithms. Store country reference data.
- Tables: `countries`, `counties`, `towns`, `areas`, `location_aliases`.
- Seed all 47 counties from an official source in a versioned seed file. Towns and areas are curated by admins, starting with the pilot region.
- Aliases cover spelling variants, common local names and alternate search terms. Each alias resolves to one canonical location record.
- Each location has `is_selectable`, `is_indexable` and `status` (`active`, `archived`).

### 10.2 Base location vs. service coverage

These are different concepts:

1. **Base location:** where the provider is based.
2. **Service areas:** places the provider explicitly agrees to serve.
3. **Service radius:** optional distance from the provider's coordinates.
4. **Customer search location:** where the customer needs the service.

A provider based in Ruiru may serve Ruaka, Kiambu, Nairobi and Thika. Do not exclude them because their base town differs from the search town.

Use a many-to-many `provider_service_areas` table. Coverage can be declared at area level, and optionally at town or county level ("serves all of Nairobi"), expanded at query time via the hierarchy.

### 10.3 Geographic coordinates

- Store coordinates as `POINT NOT NULL SRID 4326` with a `SPATIAL INDEX`.
- **MySQL requires spatial-indexed columns to be `NOT NULL`**, so provider coordinates live in a separate table `provider_geo (provider_id PK, location POINT NOT NULL SRID 4326, accuracy_m, source, updated_at)`. A provider without coordinates simply has no row.
- Areas and towns may also carry a representative centroid (same pattern) used for radius search from a selected area.
- **Axis order depends on the function:** `ST_SRID(POINT(lng, lat), 4326)` takes longitude first, while WKT functions such as `ST_GeomFromText('POINT(…)', 4326)` default to **latitude-longitude** for SRID 4326 unless `'axis-order=long-lat'` is passed. Standardize on one helper in the repository layer, read with `ST_Latitude()` / `ST_Longitude()`, and cover it with a test using a known coordinate.
- Validate latitude (−90..90) and longitude (−180..180), and optionally that the point lies within Kenya's bounding box.
- Store coordinate provenance (`provider_pin`, `geocoded`, `admin`) and accuracy.
- Never use a made-up coordinate as a real provider location. Never show a provider's exact coordinates publicly; show area-level location only.
- Never require GPS permission to browse or search.

### 10.4 Search behaviour

Customers can:

- Search by service.
- Select county, town and area.
- Use a one-time current location if they choose.
- Choose a radius where relevant.
- Search without sharing GPS.
- Change location at any time.

Search inputs: `service`, `county`, `town`, `area`, `latitude`, `longitude`, `radius_km` (bounded, for example 1–50), `page`, `limit` (bounded), `sort`.

Free-text service search should match service names, aliases/synonyms (for example "fundi wa maji" → plumbing) and category names. Service synonyms are database-managed.

### 10.5 Matching and ranking

Candidate providers match through:

1. Explicit service-area coverage (including town/county-level coverage).
2. Radius coverage, if coordinates and radius are available.
3. Any supported broader coverage rule.

Then filter by: published listing status, relevant service association, moderation/verification eligibility, optional availability.

Then rank. Initial tuning weights **(Proposed)**:

| Factor | Weight |
|---|---|
| Service relevance | 35% |
| Location match (explicit coverage > radius > broader) | 30% |
| Profile completeness | 15% |
| Rating quality (Bayesian-smoothed, so 1 review at 5★ does not beat 40 at 4.8★) | 10% |
| Recent activity / responsiveness | 10% |

- Distance alone must not override explicit service coverage.
- Weights are configuration within safe bounds; eligibility rules are not admin-editable.
- Give new providers a fair chance: reserve a small share of result slots for an exploration/rotation mechanism, deterministic per day so pagination stays stable.
- Log ranking inputs for a sample of searches so tuning can be measured.

### 10.6 Spatial query performance

Do not calculate distance across every provider row.

1. Filter providers by published status and requested service.
2. Find explicit service-area matches via indexed relational joins.
3. Use a spatial bounding box (`MBRContains` / `ST_Within` on an envelope) to find nearby candidates using the spatial index.
4. Compute exact distance for candidates only (`ST_Distance_Sphere`).
5. Merge and deduplicate.
6. Rank and paginate.
7. Inspect plans with `EXPLAIN ANALYZE`.

Test with realistic data volumes on the exact MySQL version used in production.

### 10.7 Location administration

Administrators can:

- Add and edit counties, towns and areas; set parent relationships.
- Manage aliases.
- Merge duplicate locations safely (re-point references, create redirects, keep an audit trail).
- Archive obsolete locations without breaking historical records.
- Add validated coordinates/centroids.
- Control whether a location is selectable and indexable.

Use stable IDs and slugs. Never hard-delete locations referenced by enquiries, jobs or providers.

------------------------------------------------------------------------

## 11. Identity, authentication and account security

### 11.1 Identity model

One `users` table for identity. A user authenticates using a verified email or phone, depending on configured policy. At least one verified login channel is required (application rule).

**Phone numbers:** normalize to E.164. Kenyan mobile numbers start with `07…` or `01…` locally, i.e. `+2547XXXXXXXX` or `+2541XXXXXXXX`. Accept `07…`, `01…`, `254…` and `+254…` input; validate server-side; store only the normalized form.

**Email:** trim and compare case-insensitively; store normalized lowercase for uniqueness.

### 11.2 Name-field standard (mandatory)

Every person is represented with separate name fields:

- `first_name` — required.
- `middle_name` — optional; a person may have no middle name.
- `last_name` — required.

Do not use a single `full_name` column as the canonical name, and do not assume everyone has a middle name.

```sql
first_name  VARCHAR(100) NOT NULL,
middle_name VARCHAR(100) NULL,
last_name   VARCHAR(100) NOT NULL
```

Use the three fields in registration/profile forms, account settings, admin user listings and details, audit views, notifications and templates, provider ownership and contact-person records, dashboards and API DTOs.

For display, compose the name in application code from non-empty parts in order: first, middle, last. Do not persist a duplicated `full_name` unless an integration requires a snapshot; if so, document why and how it stays consistent.

Never auto-split an existing full name. When importing legacy records, keep the original value in an import staging field and require review or user confirmation.

Note: a **business provider's display name** (for example "Ruaka Plumbing Services") is a provider field, separate from the owner's personal name.

**Validation**

- Trim leading/trailing whitespace.
- Reject empty `first_name` and `last_name`; allow null `middle_name` (store empty input as `NULL`).
- Support Unicode letters and legitimate name punctuation (apostrophes, hyphens, spaces).
- Do not force title case or change a person's spelling.
- No English-only regular expressions.
- Apply reasonable length limits; reject control characters.
- Always escape names when rendering; never treat them as markup.

### 11.3 Authentication methods (Proposed)

The MVP is **passwordless-first**:

1. **OTP login** (SMS or email) — primary method and the root of account recovery.
2. **PIN login** — optional convenience credential after a verified login on a device.

No passwords in the MVP (see §25). If passwords are added later, hash with Argon2id and add a breached-password check.

### 11.4 OTP delivery

Independent email and SMS adapters:

```go
type OTPDelivery interface {
    Send(ctx context.Context, destination string, code string, purpose Purpose) error
}
```

Select a channel based on platform settings, user preference and verified destinations.

Policy:

- Six-digit cryptographically random code.
- Five-minute expiry.
- Maximum 5 verification attempts per challenge.
- Resend cooldown (for example 60 s, increasing).
- Per-account, per-destination and per-IP rate limits; daily cap per destination (SMS costs money and is an abuse target).
- One-time use.
- Purpose-bound challenges (`registration`, `login`, `recovery`, `sensitive_change`).
- Store an HMAC of the code (server-side secret), never plaintext. Never log OTPs.
- Responses must not reveal whether an account exists for a destination (avoid enumeration).

If a channel is disabled, use another permitted, verified channel. If none is available, fail securely with a clear recovery path. Never silently bypass verification.

### 11.5 PIN login

- Six-digit PIN as a convenience credential only, usable on a device where the user has previously completed OTP login.
- Hash with Argon2id and a unique salt.
- Reject weak/common PINs (`123456`, repeated digits, simple sequences, birth-year patterns where known).
- Strict attempt limits with progressive delay; lock PIN login after repeated failures and require OTP.
- OTP-backed PIN reset.
- Never store or log plaintext PINs.

A six-digit PIN has low entropy; rate limiting and device/session binding are essential.

### 11.6 Web sessions

Opaque server-side sessions:

- High-entropy random token; store only its hash.
- `Secure`, `HttpOnly`, `SameSite=Lax` cookies.
- CSRF protection for state-changing requests.
- Rotate the session on login and privilege change.
- Idle and absolute expiry.
- Logout and server-side revocation.
- Session/device list in account security; "sign out other devices".
- Re-verify (OTP or PIN) before sensitive actions: changing phone/email, setting PIN, deleting account.

### 11.7 Future Android biometrics

Never store fingerprint or face data in MySQL and never send biometric data to the server.

Use `BiometricPrompt` and Android Keystore: authenticate normally, register the device, and protect a device-bound credential or refresh-token operation with a Keystore key. Biometrics unlock the local credential; the server validates normal tokens and device state.

------------------------------------------------------------------------

## 12. Marketplace rules and lifecycles

These rules are code-defined state machines (§8.2) with database-managed labels and thresholds. Every transition is validated in the service layer and recorded in a status-history table.

### 12.1 Provider verification levels

Verification is a set of independent badges, never a single "verified" flag.

| Badge | Meaning | Evidence |
|---|---|---|
| Phone verified | The provider controls this phone number | OTP |
| Email verified | The provider controls this email | OTP/link |
| Identity checked | Staff reviewed a government ID matching the account holder's name | ID document (private) |
| Business registered | Staff reviewed business registration documents | Registration certificate / KRA PIN (private) |
| Licence checked *(per service, where a trade is regulated)* | Staff reviewed a professional licence for that service | Licence (private), with expiry |

- Each badge records `status`, `verified_by`, `verified_at`, `expires_at` and rejection reason.
- Public labels and explanatory text are database-managed; the profile links to a "What these badges mean" page.
- Expired evidence removes the badge automatically.
- Documents are private, access-controlled, and retained only as long as the retention policy allows (§16).

### 12.2 Provider listing lifecycle

```text
draft ──submit──▶ pending_review ──approve──▶ published ◀──▶ paused
  ▲                    │                         │
  └──── reject (reason)┘                         ├──suspend──▶ suspended ──restore──▶ published
                                                 └──archive──▶ archived
```

- Minimum to submit: display name, at least one service, base area, at least one service area, at least one contact channel, and accepted provider terms.
- Material edits to a published profile (name, services, photos) may re-enter moderation per configured policy; text edits can be post-moderated.
- Only `published` listings appear in search, sitemaps and structured data. `paused`/`suspended` profiles return a non-indexable "not available" page (or 410 for archived).
- Suspension requires a reason and creates a moderation action and audit entry; the provider is notified.

### 12.3 Enquiry lifecycle

```text
new ─▶ viewed ─▶ responded ─▶ accepted ─▶ (job created — follow-up release)
  │       │          │
  │       │          └──▶ declined
  └───────┴──▶ withdrawn (by customer)
any open state ──(no activity N days)──▶ expired
accepted/declined/expired/withdrawn ──▶ closed
```

- Fields: service (optional), short description, preferred timing (free text or date), location (area), and optional contact preference.
- The provider sees the customer's contact details only after the customer submits the enquiry (customer consents on submission).
- Anti-spam: per-customer daily enquiry limit, duplicate detection (same customer → same provider within a short window), and the provider can mark an enquiry as spam (feeds moderation).
- Provider response time is tracked and may feed ranking.

### 12.4 Review eligibility and moderation (Proposed defaults)

- A customer may review a provider only if they have an enquiry with that provider in `responded` or `accepted` state, created at least 24 hours earlier. (When jobs launch, a `completed` job becomes the preferred eligibility signal.)
- One review per customer per provider; editable within a configured window; edits re-enter moderation.
- Rating 1–5 plus text with minimum length.
- **Pre-moderation** in the MVP: reviews are `pending` until approved.
- Show rating and count publicly only when approved reviews ≥ a configured minimum (for example 3).
- Providers cannot delete reviews; they can report them. Provider replies come in the follow-up release.
- Ratings are recalculated transactionally on approval/hide/restore and by a periodic reconciliation job.

### 12.5 Contact visibility and reveal

Per channel (phone, WhatsApp, email), the provider chooses:

| Setting | Behaviour |
|---|---|
| `public` | Shown on the profile |
| `reveal_on_click` | Hidden in initial HTML; revealed via a rate-limited request, logged as a contact action |
| `logged_in_only` | Revealed only to authenticated customers |
| `hidden` | Not shown; enquiry only |

- Default: `reveal_on_click` (reduces scraping while keeping no-login contact).
- WhatsApp actions use click-to-chat links with a pre-filled, templated message that mentions the platform.
- Contact numbers are not placed in JSON-LD unless the provider's setting is `public`.
- Show safe-contact guidance near contact actions (never pay upfront to unknown parties, report suspicious behaviour).

### 12.6 Reports and moderation

- Anyone can report a listing; authenticated users can report reviews. Report reasons are database-managed (fraud, offensive content, wrong information, impersonation, other).
- Reports flow into a moderation queue with assignment, status (`open`, `in_review`, `resolved`, `dismissed`) and resolution notes.
- Every moderation decision creates a `moderation_actions` record (who, what, why, target) and an audit entry.
- Repeated or high-severity reports can automatically pause a listing pending review (configurable threshold).

------------------------------------------------------------------------

## 13. Database architecture

MySQL 8 with InnoDB, `utf8mb4` / `utf8mb4_0900_ai_ci`, foreign keys, explicit migrations and UTC timestamps (`DATETIME(3)` stored as UTC). Use transactions for multi-table operations.

Numeric primary keys (`BIGINT UNSIGNED`) for joins; `public_id` (UUID stored as `BINARY(16)`, ideally time-ordered UUIDv7) or slugs for external identifiers.

Status fields use `VARCHAR` with application-validated values (or MySQL `ENUM` if the team prefers; be consistent). Adding a status must not require a table rebuild.

### 13.1 Core tables

**Identity and security**
`users`, `roles`, `permissions`, `user_roles`, `role_permissions`, `otp_challenges`, `user_sessions`, `user_devices`, `user_credentials` (PIN hash, later refresh tokens), `user_consents`, `data_subject_requests`

**Provider and service catalogue**
`providers`, `provider_members` *(later, organizations)*, `provider_contacts`, `provider_verifications`, `verification_documents`, `provider_status_history`, `categories`, `services`, `service_synonyms`, `provider_services`, `provider_hours`

**Locations**
`countries`, `counties`, `towns`, `areas`, `location_aliases`, `provider_service_areas`, `provider_geo`, `location_geo`

**Media and content**
`media_files`, `provider_portfolio`, `managed_pages`, `navigation_menus`, `navigation_items`, `seo_landing_pages`

**Customer interactions**
`favorites`, `inquiries`, `inquiry_status_history`, `provider_reviews`, `reports`, `moderation_actions`, `search_demand_requests`

**Jobs and analytics**
`jobs`, `job_status_history`, `analytics_events`, `provider_analytics_daily`, `search_logs` (sampled)

**Platform operations**
`audit_logs`, `notifications`, `notification_templates`, `background_jobs` (outbox), `site_settings`, `feature_flags`, `slug_redirects`

### 13.2 Table design notes

**users**
`id`, `public_id`, `first_name`, `middle_name` (nullable), `last_name`, `email` (nullable, unique when present), `phone_e164` (nullable, unique when present), `email_verified_at`, `phone_verified_at`, `preferred_locale`, `status` (`active`, `suspended`, `pending_deletion`, `deleted`), `last_login_at`, `created_at`, `updated_at`.

MySQL unique indexes allow multiple `NULL`s, which suits optional email/phone.

**providers**
`id`, `public_id`, `owner_user_id`, `provider_type` (`individual`, `business`), `display_name`, `slug`, `description`, `years_experience` (nullable), `base_area_id`, `service_radius_km` (nullable), `listing_status` (§12.2), `completeness_score`, `rating_average` (denormalized), `rating_count` (denormalized), SEO fields (§9.2), `published_at`, `created_at`, `updated_at`.

Coordinates live in `provider_geo` (§10.3). Verification is in `provider_verifications` (one row per badge type), not a single column. Unique `slug`. If one user may manage multiple provider profiles, model membership separately.

**provider_contacts**
`id`, `provider_id`, `channel` (`phone`, `whatsapp`, `email`), `value`, `visibility` (§12.5), `verified_at`.

**provider_service_areas**
`provider_id`, `location_type` (`area`, `town`, `county`), `location_id`, `created_at`.
Unique (`provider_id`, `location_type`, `location_id`); index beginning with (`location_type`, `location_id`) for reverse lookup. *(If only area-level coverage is supported in the MVP, simplify to `area_id` with real foreign keys.)*

**provider_services**
`id`, `provider_id`, `service_id`, `custom_title`, `description`, `pricing_type` (`fixed`, `from`, `range`, `on_quote`), `price_from`, `price_to` (nullable, KES), `status`, timestamps.
Never store lists of service IDs in a text column.

**inquiries**
`id`, `public_id`, `customer_user_id`, `provider_id`, `service_id` (nullable), `area_id` (nullable), `subject`, `message`, `preferred_timing`, `status` (§12.3), `responded_at`, `closed_at`, timestamps.

**provider_reviews**
`id`, `public_id`, `provider_id`, `customer_user_id`, `inquiry_id` (nullable), `job_id` (nullable), `rating` (1–5), `body`, `moderation_status` (`pending`, `approved`, `hidden`, `rejected`), `moderated_by`, `moderated_at`, timestamps. Unique (`provider_id`, `customer_user_id`).

**jobs** *(follow-up release)*
`id`, `public_id`, `inquiry_id` (nullable), `customer_user_id`, `provider_id`, `status`, `started_at`, `completed_at`, timestamps. Created only when both parties agree to proceed. No payment columns.

**audit_logs**
`id`, `actor_user_id` (nullable for system actions), `action`, `entity_type`, `entity_id`, `old_values` JSON, `new_values` JSON, `ip_address`, `user_agent`, `request_id`, `created_at`.

Never log passwords, PINs, OTPs, access/refresh/session tokens or other authentication secrets. Redact sensitive fields before writing `old_values`/`new_values`.

**analytics_events**
`id`, `event_type`, `provider_id` (nullable), `user_id` (nullable), `session_hash`, `properties` JSON (whitelisted keys), `created_at`. High volume: aggregate daily into `provider_analytics_daily` and prune raw events per retention policy (for example 13 months); consider monthly partitioning once volume warrants it.

### 13.3 Normalization and controlled denormalization

- Normalize users, roles, providers, services, categories and locations.
- Junction tables for many-to-many relationships.
- Keep transactional records normalized.
- Denormalize rating averages/counts and completeness scores only as read optimizations with a reliable recalculation path.
- Keep analytics aggregates separate from raw events and from audit records.
- Media metadata in MySQL; bytes in filesystem/object storage.
- JSON only for genuinely flexible metadata, never as a substitute for relational tables.
- Soft-delete (status/`deleted_at`) for records referenced by history; hard-delete or anonymize personal data per the deletion process (§16.2).

### 13.4 Migrations

- Versioned, reviewed SQL migrations; one logical change per migration.
- Every migration considers compatibility with the currently running binary (expand → migrate → contract for renames/removals).
- Migrations run via `server migrate` as an explicit deploy step, not implicitly on every boot in production.
- Reference data (counties, initial permission keys) via idempotent seeds.

------------------------------------------------------------------------

## 14. Indexing and query performance

Indexes reflect actual query patterns. Do not index every column.

### 14.1 Initial index plan

| Table | Index / constraint | Purpose |
|---|---|---|
| `users` | UNIQUE(`email`) | Login and uniqueness |
| `users` | UNIQUE(`phone_e164`) | Phone login and uniqueness |
| `users` | INDEX(`status`, `created_at`) | Admin filtering |
| `providers` | UNIQUE(`slug`) | Public profile lookup |
| `providers` | INDEX(`listing_status`, `created_at`) | Listing eligibility, admin filters |
| `providers` | INDEX(`base_area_id`, `listing_status`) | Base-area lookup |
| `provider_geo` | SPATIAL INDEX(`location`) | Spatial candidate search |
| `provider_verifications` | UNIQUE(`provider_id`, `badge_type`) | One row per badge |
| `provider_services` | UNIQUE(`provider_id`, `service_id`) | No duplicate assignments |
| `provider_services` | INDEX(`service_id`, `status`, `provider_id`) | Search by service |
| `provider_service_areas` | UNIQUE(`provider_id`, `location_type`, `location_id`) | No duplicate coverage |
| `provider_service_areas` | INDEX(`location_type`, `location_id`, `provider_id`) | Providers serving a location |
| `favorites` | UNIQUE(`customer_user_id`, `provider_id`) | No duplicate favourites |
| `favorites` | INDEX(`customer_user_id`, `created_at`) | Saved list |
| `inquiries` | INDEX(`provider_id`, `status`, `created_at`) | Provider inbox |
| `inquiries` | INDEX(`customer_user_id`, `created_at`) | Customer history |
| `jobs` | INDEX(`provider_id`, `status`, `created_at`) | Provider jobs |
| `jobs` | INDEX(`customer_user_id`, `created_at`) | Customer jobs |
| `provider_reviews` | UNIQUE(`provider_id`, `customer_user_id`) | One review per customer |
| `provider_reviews` | INDEX(`provider_id`, `moderation_status`, `created_at`) | Public reviews, moderation |
| `audit_logs` | INDEX(`actor_user_id`, `created_at`) | Actor history |
| `audit_logs` | INDEX(`entity_type`, `entity_id`, `created_at`) | Entity history |
| `otp_challenges` | INDEX(`destination`, `purpose`, `created_at`) | Lookup and rate control |
| `user_sessions` | UNIQUE(`token_hash`) | Session validation |
| `slug_redirects` | UNIQUE(`old_path`) | Redirect lookup |
| `location_aliases` | INDEX(`alias_normalized`) | Alias resolution |
| `analytics_events` | INDEX(`provider_id`, `event_type`, `created_at`) | Provider analytics rollup |

Review selectivity and query plans before adding indexes. Compound indexes may make single-column ones redundant (MySQL automatically indexes foreign keys if no usable index exists — account for this).

### 14.2 Query standards

- Parameterized SQL only.
- Select only required columns; no `SELECT *` in application queries.
- Avoid N+1 queries.
- Paginate every potentially large list; bounded `limit`.
- Cursor (keyset) pagination for high-volume admin lists and feeds.
- `EXPLAIN` / `EXPLAIN ANALYZE` on critical queries; keep the results in `docs/queries/`.
- Transactions for multi-step writes.
- Connection pool limits, query timeouts via `context`.
- Slow-query log enabled and reviewed.
- Cache static reference data (categories, services, locations, settings) in memory.
- No Redis or Elasticsearch until measurement demonstrates a need.

### 14.3 Search implementation stages

1. MySQL relational filtering + service synonym matching + spatial candidates (MVP).
2. MySQL `FULLTEXT` (ngram parser if needed) for provider/service text search.
3. Dedicated search engine only when data volume, relevance needs and measured latency justify the operational cost.

------------------------------------------------------------------------

## 15. API design

Versioned REST under `/api/v1/`.

### 15.1 Conventions

- JSON, `snake_case` fields, ISO 8601 UTC timestamps.
- Resource identifiers: `public_id` (UUID) or slug; never internal numeric IDs.
- Success: `{ "data": …, "meta": { "next_cursor": …, … } }`.
- Error: `{ "error": { "code": "validation_failed", "message": "…", "fields": { "first_name": "required" }, "request_id": "…" } }` with correct HTTP status (400, 401, 403, 404, 409, 422, 429, 500).
- Stable machine-readable error codes; human messages are localizable.
- `Idempotency-Key` header supported on create endpoints that may be retried by mobile clients (enquiries, reviews).
- `429` responses include `Retry-After`.
- Never expose SQL errors, stack traces or internal secrets.
- Maintain an OpenAPI spec in `docs/api/` as the contract for Svelte and Kotlin; generate TypeScript types from it.

### 15.2 Authentication

```text
POST /auth/register
POST /auth/otp/send
POST /auth/otp/verify
POST /auth/pin/setup
POST /auth/pin/login
POST /auth/logout
POST /auth/recovery/start
POST /auth/recovery/complete
GET  /auth/sessions
DELETE /auth/sessions/{id}
```

### 15.3 Public discovery

```text
GET  /categories
GET  /services
GET  /locations/counties
GET  /locations/towns?county=
GET  /locations/areas?town=
GET  /locations/search?q=          # alias-aware autocomplete
GET  /providers/search
GET  /providers/{slug}
POST /providers/{slug}/contact-reveal   # rate-limited; returns channel value
GET  /services/{serviceSlug}/{locationSlug}
POST /demand-requests                   # "request a provider" on zero results
```

### 15.4 Customer

```text
GET    /customer/profile
PATCH  /customer/profile
GET    /customer/favorites
POST   /customer/favorites
DELETE /customer/favorites/{providerId}
POST   /customer/inquiries
GET    /customer/inquiries
GET    /customer/inquiries/{id}
POST   /customer/inquiries/{id}/withdraw
GET    /customer/jobs                   # flagged
POST   /customer/reviews
PATCH  /customer/reviews/{id}
POST   /reports
POST   /customer/data-export
POST   /customer/account-deletion
```

### 15.5 Provider

```text
POST   /provider/profile
PATCH  /provider/profile
POST   /provider/profile/submit
POST   /provider/profile/pause
POST   /provider/profile/publish
GET    /provider/dashboard
GET    /provider/services
POST   /provider/services
PATCH  /provider/services/{id}
DELETE /provider/services/{id}
GET    /provider/service-areas
PUT    /provider/service-areas          # replace full set
GET    /provider/contacts
PUT    /provider/contacts
POST   /provider/media
DELETE /provider/media/{id}
GET    /provider/verifications
POST   /provider/verifications
GET    /provider/inquiries
PATCH  /provider/inquiries/{id}         # status transitions per §12.3
GET    /provider/jobs                   # flagged
PATCH  /provider/jobs/{id}              # flagged
GET    /provider/analytics
```

### 15.6 Administration

```text
GET    /admin/dashboard
GET    /admin/users
GET    /admin/users/{id}
PATCH  /admin/users/{id}/status
PUT    /admin/users/{id}/roles
GET    /admin/roles
PUT    /admin/roles/{id}/permissions
GET    /admin/providers
PATCH  /admin/providers/{id}/status
GET    /admin/verifications?status=pending
PATCH  /admin/verifications/{id}
GET|POST|PATCH /admin/categories[/{id}]
GET|POST|PATCH /admin/services[/{id}]
GET|POST|PATCH /admin/locations[/{id}]
POST   /admin/locations/merge
GET    /admin/reviews?status=pending
PATCH  /admin/reviews/{id}
GET    /admin/reports
PATCH  /admin/reports/{id}
GET    /admin/audit-logs
GET|PATCH /admin/seo
GET|POST|DELETE /admin/redirects
GET|POST|PATCH /admin/pages[/{id}]
GET|PUT /admin/navigation/{menu}
GET|PATCH /admin/notification-templates[/{id}]
GET|PATCH /admin/settings
GET|PATCH /admin/feature-flags
```

------------------------------------------------------------------------

## 16. Security, privacy and compliance

### 16.1 Security controls

- Validate all inputs on the server.
- Prepared/parameterized SQL.
- CSRF protection for cookie-authenticated state-changing requests.
- Authentication and authorization middleware; permission policies in services.
- Rate-limit login, OTP, PIN, contact-reveal, enquiry, review and report endpoints. Single instance: in-process limiter; multiple instances: shared store (MySQL or Redis when justified).
- Secure cookies and session lifecycle.
- Validate uploaded file type by content, not extension; size limits.
- Re-encode public images and strip EXIF/GPS metadata.
- Verification documents in private storage, access-controlled and audit-logged on view.
- Secrets outside source control; rotate on staff departure or suspected leak.
- TLS everywhere in production; HSTS.
- Security headers and a restrictive Content Security Policy (no inline scripts; nonces where unavoidable).
- Audit logs for administrative and sensitive actions.
- Dependency vulnerability scanning (`govulncheck`, `npm audit`) in CI.
- Bot protection on registration and enquiry forms if abuse appears (start with rate limits and honeypot fields; add a CAPTCHA only if needed).

### 16.2 Privacy and Kenya Data Protection Act, 2019

- Assess obligations under the Data Protection Act, 2019 and its regulations, including whether registration with the Office of the Data Protection Commissioner (ODPC) is required for the business.
- Publish a privacy policy describing data collected, purposes, lawful basis, retention, sharing (SMS/email providers, hosting) and data-subject rights.
- Record consent and the accepted terms/privacy version in `user_consents`. Marketing messages require separate opt-in.
- Data-subject rights: access/export, correction and deletion via `data_subject_requests`, processed within a defined time frame. Deletion anonymizes personal data while preserving non-personal records required for integrity (for example, an anonymized review or audit entry).
- Retention schedule **(Proposed)**: OTP challenges 30 days; sessions 90 days after expiry; raw analytics 13 months; rejected verification documents 30 days; approved verification documents for the duration of the listing plus a defined period; audit logs per legal advice.
- Consider cross-border data-transfer requirements if hosting or processors are outside Kenya.
- Data Protection Impact Assessment for processing of identity documents.
- Breach notification procedure aligned with the Act's timelines.

### 16.3 Audit vs. analytics

Audit logs answer **who changed what, and when**. Analytics answer **how the marketplace is used**.

Example analytics events: `search_performed`, `search_zero_results`, `provider_profile_view`, `provider_phone_reveal`, `provider_whatsapp_click`, `inquiry_created`, `favorite_added`, `review_submitted`.

Never put OTPs, passwords, PINs, tokens or full private enquiry messages into analytics events. Respect cookie/analytics consent requirements for any third-party analytics.

------------------------------------------------------------------------

## 17. Media and image handling

- Keep binaries outside MySQL; store metadata and ownership in `media_files`.
- Random, non-guessable filenames.
- Validate size and actual content type; allow only approved image formats (and PDF for verification documents).
- Re-encode images into approved formats; generate responsive sizes (for example 160, 480, 960, 1600 px wide).
- Strip EXIF metadata, including GPS coordinates.
- Prevent executable or polyglot uploads; serve user content with `Content-Disposition` and correct `Content-Type`, and from a path or domain that cannot execute scripts.
- Verification documents in private storage; access via authorized download handlers or short-lived signed URLs, logged in the audit trail.
- Media moderation: new portfolio images on published profiles may be pre- or post-moderated per configuration.
- Deletion and retention policies applied by background jobs.
- Lazy-load below-the-fold images; explicit width/height to avoid layout shift.
- Per-provider media quotas.

------------------------------------------------------------------------

## 18. Notifications

A provider-independent notification service with adapters.

- **Initial channels:** email and SMS.
- **Future:** Android push notifications.
- **SMS provider:** a Kenyan SMS aggregator (for example, Africa's Talking or similar). **Lead time:** registering an alphanumeric sender ID with mobile networks can take weeks — start in Phase 1. Budget for per-message costs and enforce daily caps.
- **Email:** a transactional email provider with SPF, DKIM and DMARC configured on the sending domain.

Templates are database-managed with safe variable substitution (allow-listed variables per template). Administrators cannot execute arbitrary template code. Templates are per channel and per locale.

Events:

- OTP requested.
- Account verified.
- New enquiry (to provider).
- Enquiry status changed (to customer).
- Listing approved / rejected / suspended.
- Verification approved / rejected / expiring.
- Review moderation result.
- Job status changed *(follow-up)*.
- Security alert (new device login, contact detail changed).

Delivery goes through the outbox/background queue with retries and exponential backoff. Store delivery status, provider message IDs where safe, retry count and timestamps. Never log OTP contents. Respect user notification preferences except for security and transactional messages.

------------------------------------------------------------------------

## 19. Non-functional requirements and operations

### 19.1 Performance targets (Proposed, at launch scale)

| Measure | Target |
|---|---|
| Public page server response (p95) | ≤ 300 ms |
| Search API (p95, 10,000 providers) | ≤ 500 ms |
| Largest Contentful Paint (mid-range Android, 4G) | ≤ 2.5 s |
| Interaction to Next Paint | ≤ 200 ms |
| Cumulative Layout Shift | ≤ 0.1 |
| Public page JavaScript | ≤ 50 KB gzipped |

### 19.2 Availability and recovery (Proposed)

- Availability target: 99.5% monthly for public pages and API.
- Backups: daily full MySQL backup plus binary logs for point-in-time recovery; media storage backed up or versioned.
- RPO ≤ 1 hour; RTO ≤ 4 hours.
- Restore drill at least monthly before launch and quarterly afterwards; documented runbook.
- Backups encrypted and stored off-host.

### 19.3 Environments

- **Local:** Docker Compose for MySQL (same major/minor version as production) and a fake SMS/email adapter that writes to logs/console with OTPs visible only in local mode.
- **Staging:** production-like, anonymized or synthetic data, real but sandboxed SMS/email.
- **Production.**

Configuration differs only by environment variables. Fake adapters must be impossible to enable in production (startup check).

### 19.4 Observability

- Structured JSON logs with request IDs, user public ID (where authenticated) and no secrets/PII beyond necessity.
- Metrics: request rate, latency, error rate by route; DB pool usage; queue depth; OTP send success by channel; SMS spend.
- Health endpoints: `/healthz` (liveness), `/readyz` (DB reachable, migrations current).
- Error tracking and alerting on error-rate spikes, failed backups, SMS delivery failures and queue backlog.
- Uptime monitoring for public pages.

### 19.5 CI/CD

- CI on every change: `go vet`, `staticcheck`, `go test` (including MySQL-backed tests), `govulncheck`, frontend lint/type-check/tests, build of the single binary.
- Deployment: build once, promote the same artifact from staging to production; run migrations as a deliberate step; zero-downtime restart behind the reverse proxy; documented rollback.

------------------------------------------------------------------------

## 20. Testing and quality strategy

### 20.1 Automated tests

- Unit tests for business rules, validation and state machines (§12).
- Repository tests against real MySQL (same version as production).
- API integration tests, including error format and status codes.
- Authentication and authorization tests: every protected endpoint is tested for "no session", "wrong permission" and "not owner".
- OTP expiry, attempt, resend and rate-limit tests.
- Name-field validation tests (Unicode, missing middle name, control characters).
- Phone normalization tests (`07…`, `01…`, `254…`, `+254…`, invalid).
- Location matching, alias resolution and radius tests, including the spatial axis-order test.
- SEO rendering tests: title, description, canonical, robots, JSON-LD.
- Sitemap and indexing-eligibility tests.
- Upload security tests.
- Migration up/down tests.
- End-to-end tests (Playwright) for key flows: search → profile → contact; register → enquire; provider onboarding → submit → approve → appears in search; review → moderation → visible.

### 20.2 Performance tests

Homepage response time; category and location page rendering; provider search by service and area; spatial search with realistic volumes; provider dashboard lists; admin filtering and pagination; sitemap generation at scale.

Use realistic seed data, including providers based outside the searched town but explicitly serving it, and areas with no providers.

### 20.3 Accessibility and responsive testing

Phone, tablet and desktop widths; keyboard navigation; visible focus states; labels; contrast; semantic HTML; screen-reader-friendly forms and error messages. Test on at least one low-end Android device on a throttled connection.

------------------------------------------------------------------------

## 21. Implementation roadmap

Phases are ordered to reduce rework. Each phase ends with its exit criteria met and tests passing.

### Phase 1 — Foundation

- Repository, Go application, `cmd/server` subcommands (`serve`, `migrate`, `admin create`, `seed`).
- Environment-based configuration.
- MySQL, migrations and seeds (countries, counties).
- Module boundaries, logging, request IDs, error handling, response format.
- Vite/Svelte application and Go template rendering; embedded assets.
- i18n string-catalogue mechanism.
- Background job/outbox worker.
- CI pipeline and test structure; Docker Compose for local dev.
- **Start SMS sender ID registration and email domain setup.**

**Exit criteria:** The binary serves a responsive placeholder public page and the Svelte app, connects safely to MySQL, runs migrations and seeds, and passes CI.

### Phase 2 — Identity, authentication and authorization

- Users with the three-part name standard.
- Registration and verification; email and SMS adapters (plus local fakes).
- OTP lifecycle and rate limiting.
- Secure web sessions, device list, revocation.
- PIN setup and login.
- Account recovery.
- Roles, permission catalogue, authorization policies, super-admin bootstrap.
- Audit log infrastructure.

**Exit criteria:** Authentication flows and permission checks pass security and integration tests; role changes are audited.

### Phase 3 — Dynamic platform and catalogue

- Site settings and brand identity; theme variables.
- Managed pages and navigation.
- Feature flags and maintenance mode.
- Categories, services, synonyms.
- Location hierarchy administration, aliases, centroids.
- Admin dashboard shell.

**Exit criteria:** Administrators (with the right permissions) can change brand identity, navigation, pages, categories, services and locations without code edits; all changes are audited.

### Phase 4 — Provider management

- Individual and business provider profiles; guided onboarding with completeness score.
- Service assignment and pricing type.
- Base location, service areas, radius and coordinates.
- Contact channels and visibility.
- Portfolio and media pipeline.
- Verification badges and document review.
- Listing lifecycle and moderation queue.
- Assisted onboarding (admin-created draft, provider claims).

**Exit criteria:** A provider can create, submit and manage a listing; moderators can approve, reject and suspend it; only published listings are publicly visible.

### Phase 5 — Public marketplace and SEO

- Homepage and search (relational + spatial + ranking).
- Category, service, service/location and location pages.
- Provider profile pages and contact reveal.
- Server-rendered metadata, canonicals, robots, sitemaps, structured data, redirects.
- Indexing eligibility rules.
- Empty states and demand requests.
- Mobile performance and accessibility pass.

**Exit criteria:** Public pages return complete, useful HTML with unique metadata, are crawlable without login or JavaScript, and meet §19.1 targets on staging data.

### Phase 6 — Customer engagement

- Favourites.
- Enquiries and enquiry lifecycle (both sides).
- Reviews with eligibility and moderation.
- Reports and moderation actions.
- Notifications for the events in §18.
- Customer dashboard; provider enquiry inbox and basic analytics.
- Data export and deletion requests.

**Exit criteria:** Customers can discover providers publicly and use authenticated features reliably; providers can manage enquiries; moderators can handle reviews and reports.

### Phase 7 — Production hardening and MVP launch

- Security review (including authorization matrix review).
- Load and query testing; `EXPLAIN ANALYZE` review of critical queries.
- Backup and restore validation.
- Monitoring, alerting and runbooks.
- Privacy policy, terms, ODPC assessment, DPIA.
- Provider recruitment in the pilot region to agreed inventory targets.
- Search Console setup and sitemap submission.
- Production deployment and release checklist.

**Exit criteria:** All §22 acceptance criteria are met; pilot categories × areas meet minimum inventory; launch.

### Phase 8 — Jobs and reporting (follow-up release)

- Convert accepted enquiries into job records; job status history.
- Provider and customer job views (enable `jobs` flag).
- Review eligibility from completed jobs; provider replies to reviews.
- Operational reporting and richer provider analytics.

**Exit criteria:** Jobs can be tracked end-to-end without payments.

### Phase 9 — Kotlin Android application

- Reuse API contracts (OpenAPI).
- Token-based auth with device registration.
- Biometric unlock (local).
- Customer and provider experiences.
- Push notifications and optional GPS.

------------------------------------------------------------------------

## 22. MVP acceptance criteria

The MVP is not complete until these conditions are met.

### Brand and dynamic content

- [ ] Product name can be changed from administration without rebuilding.
- [ ] Logo, favicon, colors and tagline are configurable.
- [ ] Header and footer links are database-managed.
- [ ] Categories, services and locations are not hardcoded.
- [ ] Public pages use configured branding.
- [ ] No user-facing string literals outside string catalogues.

### Identity and access

- [ ] Users have separate `first_name`, nullable `middle_name` and `last_name` everywhere.
- [ ] Kenyan `07…` and `01…` numbers normalize correctly to E.164.
- [ ] Every protected endpoint enforces permissions and ownership in Go and fails closed.
- [ ] Administrator role does not implicitly hold all permissions.
- [ ] First super administrator is created via CLI; no default credentials exist.

### Marketplace

- [ ] Visitors can browse and search by service and location without registering.
- [ ] Visitors can view provider profiles.
- [ ] Contact actions respect provider visibility settings; reveals are rate-limited and logged.
- [ ] Enquiry submission requires authentication and follows the enquiry lifecycle.
- [ ] Providers can manage services, service areas and contact settings.
- [ ] Only published listings appear in search, sitemaps and structured data.
- [ ] Verification badges are separate and accurately labelled.
- [ ] Reviews require eligibility and moderation; ratings show only above the minimum count.
- [ ] Zero-result searches show useful alternatives and are logged.

### Location

- [ ] County, town and area hierarchy is normalized.
- [ ] Location aliases resolve to canonical records.
- [ ] A provider can serve multiple areas, and can appear in a town other than their base town when coverage permits.
- [ ] Radius search works with valid coordinates; axis order is covered by a test.
- [ ] Manual location selection works without GPS permission.
- [ ] Spatial and relational queries are reviewed with `EXPLAIN ANALYZE`.

### SEO

- [ ] Public pages return meaningful server-rendered HTML.
- [ ] Titles and descriptions are unique and editable.
- [ ] Canonical URLs are correct.
- [ ] Open Graph metadata is present.
- [ ] Sitemap contains eligible public pages only.
- [ ] Robots rules protect private and thin pages.
- [ ] Slug changes create redirects.
- [ ] Structured data reflects visible, accurate content.

### Security, privacy and reliability

- [ ] OTPs are HMAC-hashed, purpose-bound, expiring, attempt-limited and one-time.
- [ ] PINs use Argon2id, weak-PIN rejection and rate limiting.
- [ ] Sessions are revocable and protected; CSRF is enforced.
- [ ] Uploads are validated; EXIF stripped; private documents protected and access-logged.
- [ ] Sensitive secrets never enter logs, analytics or audit values.
- [ ] Audit events are recorded for sensitive administrative actions.
- [ ] Data export and deletion requests can be fulfilled.
- [ ] Backups can be restored successfully within the RTO.

### Performance and usability

- [ ] Mobile-first layouts work at common screen sizes.
- [ ] Large lists are paginated with bounded limits.
- [ ] Critical queries have reviewed execution plans.
- [ ] Images are optimized and lazy-loaded where appropriate.
- [ ] Core Web Vitals meet §19.1 targets and are monitored.
- [ ] Public discovery works without JavaScript.
- [ ] WCAG 2.2 AA checks pass on key flows.

------------------------------------------------------------------------

## 23. Key architectural decisions to preserve

1. Keep the application a modular Go monolith until there is a measured reason to split it.
2. Use Go-rendered public HTML for SEO and Svelte for interactive application experiences.
3. One API and one business-rule implementation for web and future Kotlin clients.
4. Keep the product name, visual identity, metadata, navigation and marketplace content configurable.
5. Normalize location data and distinguish base location, service areas and radius.
6. Design indexes around real queries and verify them with execution plans.
7. Do not require customer registration for public discovery.
8. Do not add payments to the initial scope.
9. Never trust frontend authorization or validation as a security boundary; permissions fail closed.
10. Build SEO, auditability, privacy and operational configuration into the first release.
11. Represent people with separate first, middle (optional) and last name fields.
12. Never fabricate marketplace content: providers, reviews, ratings, coordinates or verification.

**Final product direction:** Build a configurable, public-first local services marketplace. The public website is the discovery engine; the authenticated application manages customer and provider activity; the Go backend is the single source of business logic and data.

------------------------------------------------------------------------

## 24. Terminology

| Term | Meaning |
|---|---|
| **Provider** | An individual professional or business offering services. "Professional" is used in public copy. |
| **Customer** | A person seeking a service. |
| **Enquiry / inquiry** | A customer's message to a provider. Public UI copy uses Kenyan/British English (**enquiry**, **favourite**); code identifiers, tables and API paths use **inquiry**, **favorite**. Do not mix within a layer. |
| **Listing** | A provider's public profile and its publication state. |
| **Service area** | A location a provider explicitly serves. |
| **Base location** | Where a provider is based. |
| **Badge** | One specific, evidenced verification claim (§12.1). |
| **Job** | An operational record of agreed work (follow-up release); never a payment record. |

------------------------------------------------------------------------

## 25. Open decisions

| # | Decision | Recommendation | Status |
|---|---|---|---|
| 1 | Do we support passwords at all? | No for MVP: OTP + optional PIN; reconsider for admin accounts | Open |
| 2 | Stronger authentication for staff/admin accounts | Require OTP on every new device + short sessions; consider TOTP for super administrators | Open |
| 3 | SvelteKit (`adapter-static`) vs. plain Svelte + router | SvelteKit static — file-based routing matches the planned folder structure | Open |
| 4 | Migration tool | `goose` (SQL files, simple CLI, embeddable) | Open |
| 5 | Service-area granularity in MVP | Area + town + county coverage; simplify to area-only if time-constrained | Open |
| 6 | Pilot region and launch categories | Nairobi + Kiambu; 6–8 high-demand categories | Open |
| 7 | Minimum providers for an indexable service × location page | 3 | Open |
| 8 | Minimum approved reviews before showing a rating | 3 | Open |
| 9 | Default contact visibility | `reveal_on_click` | Open |
| 10 | Review moderation mode | Pre-moderation at launch | Open |
| 11 | SMS and email providers | Kenyan SMS aggregator; transactional email provider with Kenyan-deliverable reputation | Open |
| 12 | Hosting location and data residency | Decide after DPA cross-border assessment | Open |
| 13 | Final brand name and domain | Keep "HudumaLink" as configurable working name | Open |
| 14 | Swahili at launch? | English at launch; Swahili-ready architecture; translate top public pages first | Open |

Update this table as decisions are made; move confirmed decisions into the relevant section and mark them **Decided** with a date.

------------------------------------------------------------------------

## Appendix A — AI coding-agent instructions and token-efficiency policy

AI tools may assist with implementation but must follow this architecture and work only on the requested scope. The repository's root `AGENTS.md` (and/or `CLAUDE.md`) contains a concise version of this policy and links to this plan rather than copying it.

### A.1 What the AI must do

1. Read the relevant project instructions and inspect only the files needed for the task.
2. Follow existing architecture, naming conventions, module boundaries and coding style.
3. Reuse existing services, repositories, validators, middleware, components and utilities.
4. Make the smallest complete change that satisfies the requirement.
5. Preserve backward compatibility unless a breaking change is explicitly requested and planned.
6. Use migrations for schema changes; never silently alter production schema.
7. Add or update focused tests for changed behaviour.
8. Use parameterized SQL and existing database access patterns.
9. Enforce authorization and validation in Go, not only in the frontend.
10. Keep API contracts (OpenAPI) and documentation synchronized.
11. Report files changed, behaviour changed, tests run and remaining risks.
12. Ask a concise clarification question when a requirement affects security, data integrity or architecture and cannot be safely inferred.

### A.2 What the AI must not do

- Re-read or print the entire repository when only a few files are relevant.
- Reproduce large files, logs, dependency trees or generated assets in responses.
- Dump complete schemas or table contents unless specifically required.
- Repeat the full product plan in every task or explain basics unless asked.
- Generate alternative architectures when one is already approved.
- Introduce a framework, library, service, database, cache or microservice without a demonstrated requirement.
- Add abstractions, interfaces, factories, wrappers or configuration layers that don't solve a current requirement.
- Create duplicate helpers or parallel implementations.
- Rename unrelated files, variables, routes or columns; reformat unrelated files; rewrite working modules for style.
- Invent business rules, permissions, fields, endpoints or UI flows not in this specification.
- Hardcode branding, categories, services, locations, SEO copy, contact details, user-facing strings or operational settings.
- Put secrets, credentials, OTPs, PINs or tokens in source code, fixtures, logs or responses.
- Weaken validation, authorization, CSRF protection, rate limits or session security.
- Trust client-supplied user IDs, roles, provider ownership or permission claims.
- Store passwords, PINs, OTPs or raw session tokens in plaintext.
- Expose private verification documents or private customer data.
- Use a single `full_name` field for a person.
- Add speculative features or unnecessary dependencies.
- Change schema without a migration and a compatibility/rollback consideration.
- Claim tests passed when they were not run, or claim work is secure/production-ready without evidence.
- Silently swallow errors or return success on failure.
- Change public URLs without redirects and SEO consideration.
- Create indexable thin pages or fake reviews, listings, ratings, coordinates or business details.

### A.3 Token-efficient workflow

1. **Understand:** read project instructions and the specific related files.
2. **Locate:** search for existing implementations before creating new ones.
3. **Plan briefly:** only when the task is non-trivial.
4. **Implement:** edit only necessary files.
5. **Verify:** run the smallest relevant test/lint/build commands, then broader checks if warranted.
6. **Summarize:** concise result with changed files and verification status.

Avoid repeating the request, reprinting the plan, narrating every command, showing unchanged code, listing irrelevant files, long tutorials and speculative enhancements.

### A.4 Scope control

- Fixing one bug: fix that bug and its directly related tests; don't redesign the module.
- Adding one field: trace it through migration, model, DTO, validation, repository, service, handler and UI; nothing else.
- Implementing a feature: identify the owning module, follow its patterns, add only required DB/API/UI changes, include permission checks and audit events, update focused tests and docs.
- A larger architectural change: explain the impact and request approval first.

### A.5 Output format for AI coding tasks

```text
Implemented
- Brief description of the change.

Files changed
- path/to/file

Verification
- Commands run and their results.

Notes
- Only important limitations, migration steps or follow-up risks.
```

### A.6 Project instruction file

Create a root-level `AGENTS.md` containing: approved stack and architecture; folder/module ownership; naming and database conventions (including the name-field standard and inquiry/enquiry terminology); security requirements; migration and testing requirements; dynamic-content/no-hardcoding rules; and the AI rules and workflow above. Keep it concise and link to `plan.md` for the full specification.
