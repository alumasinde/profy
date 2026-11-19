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
