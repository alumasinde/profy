# AGENTS.md — rules for AI coding agents and contributors

The full specification is [`plan.md`](plan.md). This file is the concise working policy (plan Appendix A). Read the relevant plan section before changing a module; do not copy the plan into responses.

## Approved stack and architecture

- **Go 1.24 modular monolith**, one binary: `cmd/server` (`serve`, `migrate up|down|status`, `seed`, `admin create`).
- **MySQL 8 (InnoDB, utf8mb4_0900_ai_ci)**, hand-written parameterized SQL via `database/sql`, **goose** migrations embedded from `migrations/`.
- **Go `html/template`** for public, crawlable pages (`web/templates`). **SvelteKit 3 + Svelte 5 (static SPA)** for `/auth`, `/account`, `/provider`, `/admin`, built into `web/app` and embedded.
- REST API under `/api/v1`, contract in `docs/api/openapi.yaml`. No new frameworks, caches, queues or services without a measured need.

## Module ownership

| Path | Owns |
|---|---|
| `internal/config` | Environment config and startup safety checks |
| `internal/database` | Pool, transactions, migrations, seeds |
| `internal/http/{middleware,response,routes}` | Request IDs, logging, CSP, CSRF, error envelope, router |
| `internal/security` | Tokens, Argon2id, OTP HMAC, PIN rules, phone/email normalization, rate limiter |
| `internal/authz` | Permission catalogue (code), system roles, `Can`/`Require`, ownership checks |
| `internal/jobsqueue` | MySQL outbox worker |
| `internal/i18n` + `web/i18n/*.json` | String catalogues shared by Go and Svelte |
| `internal/shared/{text,paging,lifecycle}` | Slugs and search-term normalization, keyset cursors, draft/published/archived permission rules |
| `internal/modules/<name>` | One business module each (`auth`, `users`, `roles`, `settings`, `audit`, `notifications`, `public`, `pages`, `seo`, `categories`, `services`, `locations`, `media`, `verification`, `providers`, `moderation`, `analytics`, …) |
| `internal/storage` | File store interface (local disk now: `MEDIA_DIR/public`, `MEDIA_DIR/private`) |

Modules talk through services, never by reading each other's tables. Business logic stays out of handlers.

## Conventions

- **Names:** `first_name` (required), `middle_name` (nullable), `last_name` (required). Never a canonical `full_name`; never auto-split names; never change spelling or case. Display = non-empty parts joined.
- **Terminology:** UI copy says *enquiry*/*favourite*; code, tables and API say `inquiry`/`favorite`.
- **IDs:** `BIGINT UNSIGNED` internal keys; `public_id` UUIDv7 `BINARY(16)` or slugs externally. Never expose internal IDs.
- **Time:** UTC `DATETIME(3)` in the DB; format in `Africa/Nairobi` at the edge.
- **Public URLs:** build them only with the helpers in `internal/modules/seo` and call `seo.AddRedirect` in the same transaction whenever a public slug changes.
- **Locations:** points are written with `ST_SRID(POINT(lng, lat), 4326)` and read with `ST_Latitude/ST_Longitude`; county/town/area slugs are reserved through `location_slugs`.
- **Uploads:** never trust names or client MIME types; go through `media.Upload` (sniff, re-encode, variants). Private documents use the private store and every view is audited.
- **Provider visibility:** only `published` listings render publicly; contact values are personal data and never go into audit, analytics or job payloads.
- **Phones:** store E.164 only (`+2547…`, `+2541…`); accept `07…`, `01…`, `254…`, `+254…`.
- **API:** `{ "data", "meta" }` / `{ "error": { code, message, fields, request_id } }`, snake_case, stable error codes, bounded `limit`, cursor pagination for admin lists.
- **No hardcoding:** branding, categories, services, locations, SEO copy, contacts and settings are data; every user-facing string is a catalogue key (add it to `web/i18n/en.json`; `sw.json` is optional per key).

## Security requirements (never weaken)

- Authorization is checked in Go with permissions (not role names) and **fails closed**. Use `authz.Require(perm)` on routes and `CanOwnOrAny` for `_own/_any`. Hiding UI is not authorization.
- New permission keys go in `internal/authz/catalogue.go` only; `server seed` syncs them.
- Cookie-authenticated unsafe requests require the CSRF header; keep `SameSite=Lax`, `HttpOnly`, `Secure` (non-local), session rotation on login.
- Never store or log OTPs, PINs, tokens or secrets in plaintext — not in logs, audit values, analytics, job payloads or fixtures. OTPs are HMAC'd; PINs are Argon2id.
- Auth responses must not reveal whether an account exists.
- Record administrative and sensitive changes with `audit.Record` in the same transaction.
- Fake email/SMS adapters must stay impossible in production (config check).
- CSP has no `unsafe-inline`; framework inline scripts/styles are allowed by hash only.

## Migrations and tests

- Schema changes = a new numbered goose file (`-- +goose Up` / `-- +goose Down`), one logical change, compatible with the running binary (expand → migrate → contract). Never edit an applied migration.
- Reference data = idempotent files in `seeds/` that never overwrite admin edits.
- Add focused tests for changed behaviour. Unit tests sit beside code; HTTP/DB tests live in `tests/integration` and need `TEST_DATABASE_DSN` (real MySQL 8). Every protected endpoint is tested for *no session*, *wrong permission* and *not owner*.
- Run before claiming done: `go vet ./...`, `go test ./...` (with `TEST_DATABASE_DSN`), `npm run check` and `npm run build` in `frontend/`. Never claim tests passed without running them.

## Agent workflow

1. Read this file and the relevant plan section; inspect only the files you need.
2. Search for existing helpers before adding new ones; follow the module's patterns.
3. Make the smallest complete change; no speculative features, abstractions or renames.
4. Do not invent business rules, permissions, fields or endpoints that the plan does not specify — ask when security, data integrity or architecture is unclear.
5. Report in this format:

```text
Implemented
- …
Files changed
- …
Verification
- commands run and results
Notes
- limitations, migration steps, risks
```
