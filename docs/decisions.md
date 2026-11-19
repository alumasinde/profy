# Implementation decisions

The open decisions in [plan §25](../plan.md#25-open-decisions) are still the owner's to confirm. To start building, the plan's **recommended** option was adopted as a working default. Each is cheap to revisit unless noted.

| # | Decision | Adopted for implementation | Where it lives | Cost to change |
|---|---|---|---|---|
| 1 | Passwords? | **No.** OTP + optional device-bound PIN | `internal/modules/auth` | Medium |
| 2 | Staff authentication | Not yet differentiated; same OTP/PIN + session TTLs from env | `SESSION_*_TTL` | Low |
| 3 | SvelteKit vs plain Svelte | **SvelteKit 3** (`adapter-static`, SPA fallback `200.html`) | `frontend/` | High |
| 4 | Migration tool | **goose** v3, embedded | `migrations/`, `internal/database` | High |
| 6 | Pilot region | Nairobi + Kiambu seeded `is_selectable = 1` | `seeds/002_counties.sql` (admin-editable later) | Low |
| 9 | Default contact visibility | Not built yet (Phase 4) | — | — |
| 11 | SMS / email providers | Adapters for **Africa's Talking** and **SMTP**; fakes for local | `internal/modules/notifications` | Low |
| 13 | Brand | "HudumaLink" stored as `site_name` data | `site_settings` | None (data) |
| 14 | Swahili | English complete; `sw.json` partial, falls back per key; **needs native-speaker review** | `web/i18n` | Low |

## Smaller choices made while building Phases 1–2

- **OTP delivery is synchronous to the provider but asynchronous to the HTTP response**, not via the outbox, so codes never touch the database in plaintext and response timing doesn't reveal whether an account exists.
- **Registration takes one login channel** (phone *or* email). Adding a second verified channel belongs with account settings later.
- **Registering with an already-registered destination** returns the same response and the code signs the owner in; names in that request are ignored.
- **Weak PIN rule:** six digits, at least three distinct digits, no ascending/descending run, not two repeated triples, not on a common-PIN list.
- **PIN lockout:** progressive delay after 3 failures (30 s, doubling), locked after 6; any OTP login or recovery unlocks.
- **Re-verification window:** 10 minutes after an OTP (registration, login, recovery or `sensitive_change`). Required for PIN setup.
- **Protected roles** (super administrator) can be granted or removed only by a holder; the last active super administrator cannot be removed; its permissions are fixed (always complete).
- **Administrator role** gets an explicit, documented subset — not roles/permission management, sensitive user/audit data, exports, maintenance or flag changes.
- **OTP message text** comes from the string catalogue until database-managed notification templates arrive (Phase 6).
- **Rate limiter** is in-process (single instance). A shared store is needed before running multiple instances.
- **Proposed retention** (§16.2) runs as a daily outbox job: OTP challenges 30 days, sessions 90 days after expiry/revocation, finished jobs 30 days.

## Choices made while building Phase 3

- **Public URL scheme** lives in one place (`internal/modules/seo`): pages `/{slug}`, categories `/categories/{slug}`, services `/services/{slug}`, locations `/locations/{slug}`. The plan lists `/categories` and `/services/{slug}` but not a category detail URL; `/categories/{slug}` is the working choice for Phase 5.
- **Redirects** are created automatically when the slug changes on anything that has ever been public (pages, categories and services after first publication; locations always). Chains are collapsed on write and changing a slug back removes the shadowing redirect.
- **Location slugs share one namespace** across counties, towns and areas (`location_slugs`), because `/services/{service}/{location}` can't tell them apart. New towns/areas are disambiguated at creation: `ruaka` → `ruaka-nairobi` → `ruaka-nairobi-2`.
- **Counties are official data**: names, codes and slugs come from seeds; admins control selectable/indexable flags, SEO fields, aliases and centroids.
- **Aliases are globally unique** (database constraint) so an alias always resolves to one location.
- **Merging** is for towns and areas: children and aliases move to the target, the source name becomes an alias, the source is archived with `merged_into_id` (never deleted), and its URL redirects. **Phase 4 must extend merge** to re-point provider base areas and service areas.
- **Category tree is two levels** (category → subcategory). Publishing is bottom-up (parent first); unpublishing and archiving are top-down (children and services first), with conflict codes `parent_not_published` and `has_dependents`.
- **Managed pages use restricted Markdown** rendered server-side (headings, lists, bold, links to site paths/https/mailto/tel). No raw HTML, so editors can't inject scripts. Six information pages are seeded as **drafts with placeholder text**; nothing is public until published. Privacy and terms need legal review first.
- **Settings are a code-defined schema**: validation per kind (URL, asset, email, Kenyan phone, colour, locale, timezone, ISO codes, social links). `otp_channels` needs `notification_channels.configure`; `maintenance_mode` has its own endpoint and permissions.
- **Theme contrast rule** (WCAG 2.2 AA, matching how the stylesheets use the colours): primary ≥ 4.5:1 against white and the page background (buttons, links); secondary ≥ 4.5:1 (headings); accent ≥ 3:1 (focus ring).
- **Logos and images are URLs for now** (`https://` or `/static/`). They become media references when the media pipeline arrives in Phase 4.
- **Maintenance mode** returns 503 with `Retry-After` for public pages only; sign-in, account, admin and the API keep working so staff can turn it off.
- **Feature flags** can be toggled, not created, from the admin: code references flags, so seeds create them.

## Choices made while building Phase 4

- **One listing per user.** A user may own one non-archived provider listing; starting one grants the `provider` role. Any signed-in user can start a listing — onboarding is open by design, and nothing is public until a moderator approves it.
- **Listing state machine is code** (`providers.Transitions`): draft → pending_review → published ⇄ paused; staff can suspend (from pending/published/paused) and restore (→ published); reject returns to draft; archive is final (public URL answers 410). Owners can submit, pause, resume and archive only; a suspended owner cannot resume. Reject and suspend require a reason, which is shown to the owner and sent to them.
- **Approval re-checks completeness.** Pending listings stay editable, so the required items (name, a live service, base area, coverage, a contact, accepted provider terms) are checked again on approve, not only on submit.
- **Completeness score** is a fixed weighting in code (required items 60, optional 40: description ≥ 80 characters, profile photo, portfolio, experience).
- **Edits to live listings** follow the `listing_edit_moderation` setting: `post` (default) publishes immediately and relies on after-the-fact moderation; `pre` sends name and profile/cover photo changes back to `pending_review`. Portfolio photos follow `portfolio_moderation` the same way (pre = hidden until approved).
- **Visibility.** Only `published` listings render. Paused and suspended answer 200 with a neutral "not taking enquiries" page and `noindex` (no details, no contacts, no reason); draft and pending are 404 (following any slug redirect); archived is 410. Owners and `providers.view_any` holders get `/preview/professionals/{id}`.
- **Contacts and reveal.** Each channel has its own visibility: public, reveal_on_click (default), logged_in_only, hidden. Reveal works without JavaScript (`?reveal=phone`, `nofollow`, `noindex`, `no-store`) and through `POST /api/v1/providers/{slug}/contact-reveal`; both are rate-limited per IP (30 per 10 minutes) and recorded as analytics events. Contact values never enter audit logs, job payloads or analytics.
- **Contact badges are proof-based.** phone_verified / email_verified exist exactly while a contact value has been proven by OTP — either because it equals the owner's OTP-verified login, or through the new `contact_verification` OTP purpose. Changing a value drops its verification and badge.
- **Document badges** (identity, business registration, licence per licensed service) are reviewed by staff with `providers.verify` / `providers.reject_verification`; moderators see the queue but don't decide. Licences need an expiry date; a daily job expires them. Documents live in a separate private store, are downloadable only with `media.view_private`, every download is audited, and rejected documents are purged after 30 days.
- **Media pipeline.** Type is sniffed from bytes (JPEG, PNG, WebP; PDF for documents). Images are decoded, EXIF-oriented, and re-encoded as JPEG at 160/480/960/1600 px (never upscaled), which strips all metadata including GPS. PDFs with JavaScript, launch actions, embedded files, XFA or auto-open actions are refused. Public images are served from `/media/{id}/w{W}.jpg` with `nosniff` and a sandbox CSP; non-active images only to their owner or media moderators. Files are on local disk under `MEDIA_DIR` (`public/`, `private/`) behind a `storage.Store` interface so object storage can replace it.
- **Assisted onboarding.** Staff create an unowned draft (`created_by` set) and invite the real provider by SMS or email. The invite token (32 random bytes) is stored only as a hash, expires after 14 days, travels in the URL fragment (so it never reaches server logs or Referer), and is never put in the job queue — invites are sent directly. Claiming requires signing in with the exact phone/email invited and accepting the provider terms; an unclaimed listing cannot be submitted. Re-inviting expires earlier tokens.
- **Map pins are private.** The pin is optional, must be inside Kenya's bounding box, is stored as `POINT SRID 4326` for Phase 5 distance search, and is never rendered publicly or written to the audit log. Public pages show the base area label only.
- **Location merges re-point providers.** The locations module exposes a merge hook; the providers module moves base areas and coverage to the merge target (deduplicating) in the same transaction.
- **Notifications to owners** (listing approved/rejected/suspended/restored, verification decisions) go through the outbox as `provider.notify` with IDs only; the worker resolves the owner's verified phone (SMS) or email and sends catalogue text. Database-managed templates arrive in Phase 6.
- **Read-only display joins exception.** Staff list and view queries read `users`, `areas/towns/counties`, `services` and `media_files` through those modules' exported read helpers; the providers module never writes their tables. The one direct read is the owner-name join in status history.
- **Review queue order** is oldest listing first (by internal ID), not by submission time; a resubmitted listing keeps its original place. Good enough at launch volumes; revisit with a `submitted_at` keyset if the queue grows.
- **Profile views** are counted on every public page view except the owner's own. Bots are not filtered yet; the counter is labelled as views, not unique visitors.
- **Public profile page** is server-rendered with `LocalBusiness` JSON-LD (no ratings until reviews exist; phone only when its visibility is public). `/badges` explains each badge and what it does not guarantee.
