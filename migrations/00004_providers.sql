-- Phase 4: providers, coverage, contacts, media, verification, moderation (plan §10, §12, §13, §17).

-- +goose Up
CREATE TABLE media_files (
    id               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    public_id        BINARY(16)    NOT NULL,
    owner_user_id    BIGINT UNSIGNED NULL,
    provider_id      BIGINT UNSIGNED NULL,
    purpose          VARCHAR(30)   NOT NULL, -- profile | cover | portfolio | verification_document
    visibility       VARCHAR(10)   NOT NULL, -- public | private
    storage_key      VARCHAR(200)  NOT NULL, -- random, non-guessable prefix
    content_type     VARCHAR(60)   NOT NULL, -- stored (re-encoded) type
    bytes            BIGINT UNSIGNED NOT NULL,
    width            INT UNSIGNED  NULL,
    height           INT UNSIGNED  NULL,
    variants         JSON          NULL,     -- available widths for images
    sha256           BINARY(32)    NOT NULL, -- of the original upload
    original_name    VARCHAR(200)  NULL,
    status           VARCHAR(30)   NOT NULL DEFAULT 'active', -- active | pending_moderation | rejected | deleted
    moderated_by     BIGINT UNSIGNED NULL,
    moderated_at     DATETIME(3)   NULL,
    moderation_note  VARCHAR(500)  NULL,
    created_at       DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at       DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_media_files_public_id (public_id),
    UNIQUE KEY uq_media_files_storage_key (storage_key),
    KEY ix_media_files_provider (provider_id, purpose, status),
    KEY ix_media_files_status (status, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE providers (
    id                  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    public_id           BINARY(16)    NOT NULL,
    owner_user_id       BIGINT UNSIGNED NULL,  -- NULL until an assisted draft is claimed
    provider_type       VARCHAR(20)   NOT NULL, -- individual | business
    display_name        VARCHAR(120)  NOT NULL,
    slug                VARCHAR(140)  NOT NULL,
    description         TEXT          NULL,
    years_experience    TINYINT UNSIGNED NULL,
    base_area_id        INT UNSIGNED  NULL,
    service_radius_km   TINYINT UNSIGNED NULL,
    profile_media_id    BIGINT UNSIGNED NULL,
    cover_media_id      BIGINT UNSIGNED NULL,
    listing_status      VARCHAR(20)   NOT NULL DEFAULT 'draft', -- draft | pending_review | published | paused | suspended | archived
    completeness_score  TINYINT UNSIGNED NOT NULL DEFAULT 0,
    rating_average      DECIMAL(3,2)  NULL,      -- denormalized (Phase 6)
    rating_count        INT UNSIGNED  NOT NULL DEFAULT 0,
    terms_version       VARCHAR(30)   NULL,
    terms_accepted_at   DATETIME(3)   NULL,
    status_reason       VARCHAR(500)  NULL,      -- last rejection/suspension reason shown to the owner
    meta_title          VARCHAR(160)  NULL,
    meta_description    VARCHAR(320)  NULL,
    created_by          BIGINT UNSIGNED NULL,    -- staff member for assisted onboarding
    submitted_at        DATETIME(3)   NULL,
    published_at        DATETIME(3)   NULL,
    created_at          DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at          DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_providers_public_id (public_id),
    UNIQUE KEY uq_providers_slug (slug),
    KEY ix_providers_owner (owner_user_id),
    KEY ix_providers_status (listing_status, created_at),
    KEY ix_providers_base_area (base_area_id, listing_status),
    CONSTRAINT fk_providers_owner FOREIGN KEY (owner_user_id) REFERENCES users (id),
    CONSTRAINT fk_providers_base_area FOREIGN KEY (base_area_id) REFERENCES areas (id),
    CONSTRAINT fk_providers_profile_media FOREIGN KEY (profile_media_id) REFERENCES media_files (id) ON DELETE SET NULL,
    CONSTRAINT fk_providers_cover_media FOREIGN KEY (cover_media_id) REFERENCES media_files (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

ALTER TABLE media_files
    ADD CONSTRAINT fk_media_files_provider FOREIGN KEY (provider_id) REFERENCES providers (id),
    ADD CONSTRAINT fk_media_files_owner FOREIGN KEY (owner_user_id) REFERENCES users (id);

CREATE TABLE provider_status_history (
    id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    provider_id    BIGINT UNSIGNED NOT NULL,
    from_status    VARCHAR(20)   NULL,
    to_status      VARCHAR(20)   NOT NULL,
    reason         VARCHAR(500)  NULL,
    actor_user_id  BIGINT UNSIGNED NULL,
    created_at     DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    KEY ix_provider_status_history (provider_id, created_at),
    CONSTRAINT fk_provider_status_history_provider FOREIGN KEY (provider_id) REFERENCES providers (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE provider_contacts (
    id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    provider_id  BIGINT UNSIGNED NOT NULL,
    channel      VARCHAR(10)   NOT NULL, -- phone | whatsapp | email
    value        VARCHAR(254)  NOT NULL, -- E.164 or normalized email
    visibility   VARCHAR(20)   NOT NULL, -- public | reveal_on_click | logged_in_only | hidden
    verified_at  DATETIME(3)   NULL,
    created_at   DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at   DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_provider_contacts_channel (provider_id, channel),
    CONSTRAINT fk_provider_contacts_provider FOREIGN KEY (provider_id) REFERENCES providers (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE provider_services (
    id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    public_id     BINARY(16)    NOT NULL,
    provider_id   BIGINT UNSIGNED NOT NULL,
    service_id    INT UNSIGNED  NOT NULL,
    custom_title  VARCHAR(120)  NULL,
    description   VARCHAR(1000) NULL,
    pricing_type  VARCHAR(10)   NOT NULL DEFAULT 'on_quote', -- fixed | from | range | on_quote
    price_from    INT UNSIGNED  NULL, -- whole KES
    price_to      INT UNSIGNED  NULL,
    status        VARCHAR(10)   NOT NULL DEFAULT 'active', -- active | hidden
    created_at    DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at    DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_provider_services_public_id (public_id),
    UNIQUE KEY uq_provider_services (provider_id, service_id),
    KEY ix_provider_services_search (service_id, status, provider_id),
    CONSTRAINT fk_provider_services_provider FOREIGN KEY (provider_id) REFERENCES providers (id) ON DELETE CASCADE,
    CONSTRAINT fk_provider_services_service FOREIGN KEY (service_id) REFERENCES services (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Explicit coverage at area, town or county level, expanded via the hierarchy at query time (§10.2).
CREATE TABLE provider_service_areas (
    provider_id    BIGINT UNSIGNED NOT NULL,
    location_type  VARCHAR(10)   NOT NULL, -- area | town | county
    location_id    INT UNSIGNED  NOT NULL,
    created_at     DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (provider_id, location_type, location_id),
    KEY ix_provider_service_areas_reverse (location_type, location_id, provider_id),
    CONSTRAINT fk_provider_service_areas_provider FOREIGN KEY (provider_id) REFERENCES providers (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Spatial-indexed columns must be NOT NULL, so a provider without a pin has no row (§10.3).
-- Exact coordinates are never shown publicly.
CREATE TABLE provider_geo (
    provider_id  BIGINT UNSIGNED NOT NULL,
    location     POINT         NOT NULL SRID 4326,
    accuracy_m   INT UNSIGNED  NULL,
    source       VARCHAR(20)   NOT NULL, -- provider_pin | geocoded | admin
    updated_at   DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (provider_id),
    SPATIAL INDEX sx_provider_geo_location (location),
    CONSTRAINT fk_provider_geo_provider FOREIGN KEY (provider_id) REFERENCES providers (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE provider_portfolio (
    id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    provider_id  BIGINT UNSIGNED NOT NULL,
    media_id     BIGINT UNSIGNED NOT NULL,
    caption      VARCHAR(200)  NULL,
    position     INT UNSIGNED  NOT NULL DEFAULT 0,
    created_at   DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_provider_portfolio_media (media_id),
    KEY ix_provider_portfolio (provider_id, position),
    CONSTRAINT fk_provider_portfolio_provider FOREIGN KEY (provider_id) REFERENCES providers (id) ON DELETE CASCADE,
    CONSTRAINT fk_provider_portfolio_media FOREIGN KEY (media_id) REFERENCES media_files (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Public labels and explanations are data (§12.1); the meanings are fixed by code.
CREATE TABLE verification_badge_types (
    badge_type    VARCHAR(30)   NOT NULL,
    label         VARCHAR(80)   NOT NULL,
    description   VARCHAR(500)  NOT NULL,
    sort_order    INT           NOT NULL DEFAULT 0,
    updated_at    DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (badge_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- One row per badge (licence badges per service).
CREATE TABLE provider_verifications (
    id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    public_id         BINARY(16)    NOT NULL,
    provider_id       BIGINT UNSIGNED NOT NULL,
    badge_type        VARCHAR(30)   NOT NULL, -- phone_verified | email_verified | identity_checked | business_registered | licence_checked
    service_id        INT UNSIGNED  NULL,
    service_key       INT UNSIGNED  AS (COALESCE(service_id, 0)) STORED,
    status            VARCHAR(20)   NOT NULL, -- pending | verified | rejected | expired
    submitted_by      BIGINT UNSIGNED NULL,
    submitted_at      DATETIME(3)   NULL,
    verified_by       BIGINT UNSIGNED NULL,
    verified_at       DATETIME(3)   NULL,
    expires_at        DATETIME(3)   NULL,
    rejection_reason  VARCHAR(500)  NULL,
    created_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_provider_verifications_public_id (public_id),
    UNIQUE KEY uq_provider_verifications_badge (provider_id, badge_type, service_key),
    KEY ix_provider_verifications_queue (status, submitted_at),
    CONSTRAINT fk_provider_verifications_provider FOREIGN KEY (provider_id) REFERENCES providers (id) ON DELETE CASCADE,
    CONSTRAINT fk_provider_verifications_service FOREIGN KEY (service_id) REFERENCES services (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE verification_documents (
    id               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    public_id        BINARY(16)    NOT NULL,
    verification_id  BIGINT UNSIGNED NOT NULL,
    media_id         BIGINT UNSIGNED NOT NULL,
    document_type    VARCHAR(40)   NOT NULL, -- national_id | passport | alien_id | business_registration | kra_pin | licence
    created_at       DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_verification_documents_public_id (public_id),
    KEY ix_verification_documents_verification (verification_id),
    CONSTRAINT fk_verification_documents_verification FOREIGN KEY (verification_id) REFERENCES provider_verifications (id) ON DELETE CASCADE,
    CONSTRAINT fk_verification_documents_media FOREIGN KEY (media_id) REFERENCES media_files (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Assisted onboarding: staff create a draft; the provider claims it by proving control of
-- the invited phone/email and accepting provider terms (§3.1). Only the token hash is stored.
CREATE TABLE provider_claims (
    id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    provider_id    BIGINT UNSIGNED NOT NULL,
    channel        VARCHAR(10)   NOT NULL, -- sms | email
    destination    VARCHAR(254)  NOT NULL,
    token_hash     BINARY(32)    NOT NULL,
    expires_at     DATETIME(3)   NOT NULL,
    claimed_at     DATETIME(3)   NULL,
    claimed_by     BIGINT UNSIGNED NULL,
    created_by     BIGINT UNSIGNED NULL,
    created_at     DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_provider_claims_token (token_hash),
    KEY ix_provider_claims_provider (provider_id, created_at),
    CONSTRAINT fk_provider_claims_provider FOREIGN KEY (provider_id) REFERENCES providers (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Every moderation decision: who, what, why, target (§12.6).
CREATE TABLE moderation_actions (
    id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    actor_user_id  BIGINT UNSIGNED NULL,
    target_type    VARCHAR(30)   NOT NULL,
    target_id      VARCHAR(64)   NOT NULL,
    action         VARCHAR(40)   NOT NULL,
    reason         VARCHAR(500)  NULL,
    created_at     DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    KEY ix_moderation_actions_target (target_type, target_id, created_at),
    KEY ix_moderation_actions_actor (actor_user_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Marketplace usage events (not audit). Aggregation and retention arrive in Phase 6.
CREATE TABLE analytics_events (
    id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    event_type    VARCHAR(40)   NOT NULL,
    provider_id   BIGINT UNSIGNED NULL,
    user_id       BIGINT UNSIGNED NULL,
    session_hash  BINARY(16)    NULL,
    properties    JSON          NULL,
    created_at    DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    KEY ix_analytics_events_provider (provider_id, event_type, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Moderators review listings (§5.1, Phase 4 exit criteria). Fresh installs get this from the
-- code-defined defaults; existing installs get it here once.
INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.permission_key IN ('providers.publish_any', 'providers.suspend', 'providers.restore')
WHERE r.role_key = 'moderator';

-- +goose Down
DELETE rp FROM role_permissions rp JOIN roles r ON r.id = rp.role_id JOIN permissions p ON p.id = rp.permission_id
WHERE r.role_key = 'moderator' AND p.permission_key IN ('providers.publish_any', 'providers.suspend', 'providers.restore');
DROP TABLE analytics_events;
DROP TABLE moderation_actions;
DROP TABLE provider_claims;
DROP TABLE verification_documents;
DROP TABLE provider_verifications;
DROP TABLE verification_badge_types;
DROP TABLE provider_portfolio;
DROP TABLE provider_geo;
DROP TABLE provider_service_areas;
DROP TABLE provider_services;
DROP TABLE provider_contacts;
DROP TABLE provider_status_history;
ALTER TABLE media_files DROP FOREIGN KEY fk_media_files_provider, DROP FOREIGN KEY fk_media_files_owner;
DROP TABLE providers;
DROP TABLE media_files;
