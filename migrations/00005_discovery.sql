-- Phase 5: public marketplace and SEO (plan §9, §10.4–10.6, §3.2).

-- +goose Up

-- Service × location landing pages (§9.2, §9.6). A row exists once a combination has been
-- evaluated (daily job) or an administrator has overridden it. `indexed` is the computed,
-- hysteresis-smoothed indexing state; robots_mode lets SEO staff force either way.
CREATE TABLE seo_landing_pages (
    id                INT UNSIGNED  NOT NULL AUTO_INCREMENT,
    service_id        INT UNSIGNED  NOT NULL,
    location_type     VARCHAR(10)   NOT NULL, -- county | town | area
    location_id       INT UNSIGNED  NOT NULL,
    meta_title        VARCHAR(160)  NULL,
    meta_description  VARCHAR(320)  NULL,
    intro             TEXT          NULL,     -- restricted Markdown, shown above results
    robots_mode       VARCHAR(10)   NOT NULL DEFAULT 'auto', -- auto | index | noindex
    indexed           TINYINT(1)    NOT NULL DEFAULT 0,
    provider_count    INT UNSIGNED  NOT NULL DEFAULT 0,
    below_since       DATETIME(3)   NULL,     -- first evaluation below the threshold (hysteresis)
    evaluated_at      DATETIME(3)   NULL,
    created_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_seo_landing_pages (service_id, location_type, location_id),
    KEY ix_seo_landing_pages_indexed (indexed, service_id),
    KEY ix_seo_landing_pages_location (location_type, location_id),
    CONSTRAINT fk_seo_landing_pages_service FOREIGN KEY (service_id) REFERENCES services (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- "Request a provider" on zero results (§3.2). A demand signal for recruitment, not a job.
-- Contact details are optional, consented, and cleared after the retention period.
CREATE TABLE demand_requests (
    id               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    public_id        BINARY(16)    NOT NULL,
    service_id       INT UNSIGNED  NULL,
    service_text     VARCHAR(120)  NULL,
    location_type    VARCHAR(10)   NULL,
    location_id      INT UNSIGNED  NULL,
    location_text    VARCHAR(120)  NULL,
    notes            VARCHAR(500)  NULL,
    contact_channel  VARCHAR(10)   NULL, -- sms | email
    contact_value    VARCHAR(254)  NULL,
    user_id          BIGINT UNSIGNED NULL,
    status           VARCHAR(12)   NOT NULL DEFAULT 'new', -- new | in_progress | fulfilled | closed
    staff_note       VARCHAR(500)  NULL,
    handled_by       BIGINT UNSIGNED NULL,
    contact_purged_at DATETIME(3)  NULL,
    created_at       DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at       DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_demand_requests_public_id (public_id),
    KEY ix_demand_requests_status (status, created_at),
    KEY ix_demand_requests_target (service_id, location_type, location_id),
    CONSTRAINT fk_demand_requests_service FOREIGN KEY (service_id) REFERENCES services (id) ON DELETE SET NULL,
    CONSTRAINT fk_demand_requests_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Explicit curation for the homepage "Featured" section (§7.3). Never automatic.
ALTER TABLE providers
    ADD COLUMN featured_at    DATETIME(3)     NULL AFTER published_at,
    ADD COLUMN featured_until DATETIME(3)     NULL AFTER featured_at,
    ADD COLUMN featured_by    BIGINT UNSIGNED NULL AFTER featured_until,
    ADD KEY ix_providers_featured (featured_until, listing_status);

-- Search reads published listings by recency for the homepage.
ALTER TABLE providers ADD KEY ix_providers_published (listing_status, published_at);

-- Zero-result reports group analytics by event and time.
ALTER TABLE analytics_events ADD KEY ix_analytics_events_type (event_type, created_at);

-- New permissions (catalogue in code) granted to existing roles; new installs get them from
-- role defaults.
INSERT IGNORE INTO permissions (permission_key, module, description) VALUES
    ('demand_requests.view',   'discovery', 'demand requests view'),
    ('demand_requests.update', 'discovery', 'demand requests update');
INSERT IGNORE INTO role_permissions (role_id, permission_id)
    SELECT r.id, p.id FROM roles r JOIN permissions p
    WHERE (r.role_key = 'administrator' AND p.permission_key IN ('demand_requests.view', 'demand_requests.update'))
       OR (r.role_key = 'support' AND p.permission_key = 'demand_requests.view');

-- +goose Down
DELETE rp FROM role_permissions rp JOIN permissions p ON p.id = rp.permission_id
    WHERE p.permission_key IN ('demand_requests.view', 'demand_requests.update');
DELETE FROM permissions WHERE permission_key IN ('demand_requests.view', 'demand_requests.update');
ALTER TABLE analytics_events DROP KEY ix_analytics_events_type;
ALTER TABLE providers DROP KEY ix_providers_published;
ALTER TABLE providers DROP KEY ix_providers_featured, DROP COLUMN featured_by, DROP COLUMN featured_until, DROP COLUMN featured_at;
DROP TABLE demand_requests;
DROP TABLE seo_landing_pages;
