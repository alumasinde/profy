# profy — HudumaLink

A Kenya-focused, public-first local services marketplace. **HudumaLink** is a configurable working name stored as data, not code. The full product and engineering plan is in [`plan.md`](plan.md); agent and contributor rules are in [`AGENTS.md`](AGENTS.md); choices made while building are in [`docs/decisions.md`](docs/decisions.md).

## Status

| Phase (plan §21) | State |
|---|---|
| 1 — Foundation | **Done**: single Go binary, config, MySQL + goose migrations, county seeds, request IDs, structured logs, error envelope, CSP/CSRF, i18n catalogues, outbox worker, SvelteKit app embedded, Go-rendered placeholder homepage, health checks, CI, Docker Compose |
| 2 — Identity, auth, authorization | **Done**: three-part names, Kenyan phone normalization, OTP registration/login (HMAC'd, purpose-bound, expiring, attempt- and rate-limited, non-enumerating), device-bound Argon2id PIN with progressive lockout, recovery, sessions and device list, 125-key permission catalogue, six system roles, fail-closed authorization, CLI super-admin bootstrap, audit log, admin UI for users, roles and audit |
| 3 — Dynamic platform and catalogue | **Done**: admin screens and APIs for site settings and branding (typed validation, WCAG contrast check on theme colours), feature flags, maintenance mode (503 for public pages), managed pages (restricted Markdown, preview, publish lifecycle), header/footer navigation, categories (two levels) and services with search synonyms, County → Town → Area administration with aliases, centroids, shared slug namespace and merges, and 301 redirects for every slug change. Every change is permission-checked and audited |
| 4 — Provider management | **Done**: individual and business listings with guided onboarding and a completeness score; offered services with fixed/from/range/on-quote KES pricing; base area, radius, coverage and a private map pin; contacts with per-channel visibility, OTP-proven contact badges and a no-JS reveal; photo pipeline (type sniffing, re-encoding, metadata stripping, size variants) with portfolio moderation; identity, business and licence verification with private, audited document review; listing lifecycle (draft → review → published, pause, suspend, restore, archive) with a moderation queue, owner notifications and full history; assisted onboarding with secure claim links; public profile pages with JSON-LD and correct 404/410/noindex behaviour |
| 5 — Search and SEO pages | Next |
| 6–7 | Not started |

## Stack

Go 1.24 · MySQL 8.0 · goose · `html/template` · SvelteKit 3 / Svelte 5 (static SPA) · Vite 8. One binary embeds the templates, string catalogues and compiled app.

## Run locally

Requirements: Go 1.24+, Node 22+, Docker (or a local MySQL 8.0).

```bash
cp .env.example .env              # then set OTP_HMAC_KEY (openssl rand -base64 48)
docker compose up -d mysql        # MySQL 8.0 with hudumalink and hudumalink_test databases
go mod tidy                       # first run only: writes go.sum
cd frontend && npm ci && npm run build && cd ..   # builds the app into web/app

go run ./cmd/server migrate up
go run ./cmd/server seed
go run ./cmd/server admin create --first Albert --last Masinde --email you@example.com
go run ./cmd/server serve         # http://localhost:8080
```

With `make`: `make db deps migrate seed`, `make admin FIRST=… LAST=… EMAIL=…`, `make run`.

There are no default passwords. Sign in at `/auth/login` with the email or phone you gave `admin create`. With the default `fake` adapters (local only), the one-time code is printed in the server log. Production refuses to start with fake adapters.

On Windows PowerShell, set variables with `$env:DATABASE_DSN="huduma:huduma@tcp(127.0.0.1:3306)/hudumalink"` and so on, or use `make` from Git Bash.

### Frontend development

```bash
go run ./cmd/server serve          # API on :8080
cd frontend && npm run dev         # Vite on :5173, proxies /api to :8080
```

`npm run gen:api` regenerates `src/lib/api/schema.d.ts` from `docs/api/openapi.yaml`.

## Commands

| Command | Purpose |
|---|---|
| `server serve` | HTTP server plus background worker; refuses to start outside local if migrations are pending |
| `server migrate up\|down\|status` | Explicit migration step (§13.4) |
| `server seed` | Idempotent reference data; syncs the permission catalogue and system roles; never overwrites admin edits |
| `server admin create --first --last [--middle] (--email\|--phone)` | Create or promote the first super administrator |

## Tests

```bash
go vet ./...
TEST_DATABASE_DSN='huduma:huduma@tcp(127.0.0.1:3306)/hudumalink_test' go test -race ./...
cd frontend && npm run check
```

Integration tests run against real MySQL and **reset the test database**, so never point `TEST_DATABASE_DSN` at real data. Without it, only unit tests run.

Phase 4 tests cover the exit criteria end to end (create → incomplete submit refused → submit → reject with notice → resubmit → approve → public; pause, suspend, restore, archive → 410), visibility of each state, owner/moderator/customer permissions, pre-moderation of edits and portfolio photos, upload sniffing and variants, private document access and audit, PDF active-content rejection, contact verification and badge loss on change, logged-in-only reveal, assisted claim (wrong person, token reuse, token never stored or logged), location merge re-pointing, and that contact values never reach the audit log.

Phase 3 tests cover: per-key settings permissions, normalization and contrast rejection; data-driven branding and navigation on public pages; maintenance mode; page publish, preview access, slug redirects and chain collapsing; category/service publish ordering and conflict codes; synonym normalization and search; location slug disambiguation across counties, towns and areas; unique aliases; centroid axis order (verified with a real distance); merges; and audit entries for every change.

Phase 1–2 tests cover: registration, OTP one-time use, purpose binding, attempt limits, expiry, resend cooldown and daily cap; non-enumerating responses; CSRF and Origin checks; PIN setup with re-verification, weak-PIN rejection, device binding, progressive delay, lockout and recovery; session listing and cross-user revocation; the authorization matrix (anonymous 401, customer 403, admin limits, protected roles, last super admin); suspension; migrations down and up; seed idempotency; data-driven branding; SEO basics; and the outbox (dedupe, retry, failure).

## Layout

```text
cmd/server            CLI entry point
internal/
  app                 wiring, seed, super-admin bootstrap
  authz               permission catalogue, roles, Can/Require
  config database     environment config; pool, migrations, seeds
  http/               middleware, response envelope, routes, SPA shell
  i18n jobsqueue security shared/
  modules/            auth, users, roles, settings, audit, notifications, public
migrations/ seeds/    goose SQL and idempotent reference data
web/                  templates, i18n catalogues, static CSS, compiled app (web/app)
frontend/             SvelteKit source
docs/api/             OpenAPI contract
tests/integration/    MySQL-backed API tests
```

## Before production

These belong to later phases or to operations: SMS sender-ID registration and email domain authentication (start early, §18), a shared rate-limit store before running more than one instance, backups and restore drills, monitoring, the privacy policy and terms, and the ODPC assessment and DPIA (§16.2). Swahili strings need review by a native speaker.
