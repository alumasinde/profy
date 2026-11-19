-- Platform operations and location reference data (plan §4, §10.1, §13).
-- All timestamps are DATETIME(3) in UTC.

-- +goose Up
CREATE TABLE site_settings (
    setting_key   VARCHAR(100)  NOT NULL,
    value_json    JSON          NOT NULL,
    is_public     TINYINT(1)    NOT NULL DEFAULT 0,
    updated_by    BIGINT UNSIGNED NULL,
    updated_at    DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (setting_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE feature_flags (
    flag_key      VARCHAR(100)  NOT NULL,
    is_enabled    TINYINT(1)    NOT NULL DEFAULT 0,
    description   VARCHAR(255)  NOT NULL DEFAULT '',
    updated_by    BIGINT UNSIGNED NULL,
    updated_at    DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (flag_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Durable background work (outbox). Never store OTPs, PINs or tokens in payload.
CREATE TABLE background_jobs (
    id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    kind          VARCHAR(100)  NOT NULL,
    payload       JSON          NOT NULL,
    status        VARCHAR(20)   NOT NULL DEFAULT 'pending', -- pending | running | done | failed
    attempts      INT UNSIGNED  NOT NULL DEFAULT 0,
    max_attempts  INT UNSIGNED  NOT NULL DEFAULT 8,
    run_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    locked_at     DATETIME(3)   NULL,
    locked_by     VARCHAR(64)   NULL,
    last_error    VARCHAR(1000) NULL,
    dedupe_key    VARCHAR(150)  NULL,
    created_at    DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at    DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_background_jobs_dedupe (dedupe_key),
    KEY ix_background_jobs_claim (status, run_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Append-only through the application (§5.4). Sensitive values are redacted before insert.
CREATE TABLE audit_logs (
    id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    actor_user_id BIGINT UNSIGNED NULL,
    action        VARCHAR(100)  NOT NULL,
    entity_type   VARCHAR(60)   NOT NULL,
    entity_id     VARCHAR(64)   NULL,
    old_values    JSON          NULL,
    new_values    JSON          NULL,
    ip_address    VARCHAR(45)   NULL,
    user_agent    VARCHAR(255)  NULL,
    request_id    VARCHAR(64)   NULL,
    created_at    DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    KEY ix_audit_logs_actor (actor_user_id, created_at),
    KEY ix_audit_logs_entity (entity_type, entity_id, created_at),
    KEY ix_audit_logs_created (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE countries (
    id            SMALLINT UNSIGNED NOT NULL AUTO_INCREMENT,
    iso2          CHAR(2)       NOT NULL,
    name          VARCHAR(100)  NOT NULL,
    slug          VARCHAR(120)  NOT NULL,
    calling_code  VARCHAR(5)    NOT NULL,
    status        VARCHAR(20)   NOT NULL DEFAULT 'active',
    created_at    DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at    DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_countries_iso2 (iso2),
    UNIQUE KEY uq_countries_slug (slug)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE counties (
    id            INT UNSIGNED  NOT NULL AUTO_INCREMENT,
    country_id    SMALLINT UNSIGNED NOT NULL,
    code          VARCHAR(10)   NOT NULL,
    name          VARCHAR(100)  NOT NULL,
    slug          VARCHAR(120)  NOT NULL,
    is_selectable TINYINT(1)    NOT NULL DEFAULT 0,
    is_indexable  TINYINT(1)    NOT NULL DEFAULT 0,
    status        VARCHAR(20)   NOT NULL DEFAULT 'active', -- active | archived
    meta_title        VARCHAR(160) NULL,
    meta_description  VARCHAR(320) NULL,
    created_at    DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at    DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_counties_country_code (country_id, code),
    UNIQUE KEY uq_counties_slug (slug),
    KEY ix_counties_selectable (country_id, status, is_selectable),
    CONSTRAINT fk_counties_country FOREIGN KEY (country_id) REFERENCES countries (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- +goose Down
DROP TABLE counties;
DROP TABLE countries;
DROP TABLE audit_logs;
DROP TABLE background_jobs;
DROP TABLE feature_flags;
DROP TABLE site_settings;
