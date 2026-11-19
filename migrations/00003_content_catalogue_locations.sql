-- Phase 3: managed pages, navigation, slug redirects, service catalogue and the location
-- hierarchy below counties (plan §8, §9, §10, §13).

-- +goose Up
CREATE TABLE managed_pages (
    id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    public_id         BINARY(16)    NOT NULL,
    slug              VARCHAR(120)  NOT NULL,
    title             VARCHAR(160)  NOT NULL,
    body              MEDIUMTEXT    NOT NULL,           -- restricted Markdown, rendered safely server-side
    status            VARCHAR(20)   NOT NULL DEFAULT 'draft', -- draft | published | archived
    meta_title        VARCHAR(160)  NULL,
    meta_description  VARCHAR(320)  NULL,
    robots_index      TINYINT(1)    NOT NULL DEFAULT 1,
    published_at      DATETIME(3)   NULL,
    updated_by        BIGINT UNSIGNED NULL,
    created_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_managed_pages_public_id (public_id),
    UNIQUE KEY uq_managed_pages_slug (slug),
    KEY ix_managed_pages_status (status, updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE navigation_menus (
    id            SMALLINT UNSIGNED NOT NULL AUTO_INCREMENT,
    menu_key      VARCHAR(40)   NOT NULL, -- header | footer
    name          VARCHAR(100)  NOT NULL,
    created_at    DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_navigation_menus_key (menu_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE navigation_items (
    id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    public_id     BINARY(16)    NOT NULL,
    menu_id       SMALLINT UNSIGNED NOT NULL,
    label         VARCHAR(80)   NOT NULL,
    url           VARCHAR(500)  NOT NULL,   -- internal path ("/about") or https URL
    position      INT UNSIGNED  NOT NULL DEFAULT 0,
    is_active     TINYINT(1)    NOT NULL DEFAULT 1,
    created_at    DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at    DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_navigation_items_public_id (public_id),
    KEY ix_navigation_items_menu (menu_id, position),
    CONSTRAINT fk_navigation_items_menu FOREIGN KEY (menu_id) REFERENCES navigation_menus (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 301 redirects for changed public slugs; chains are collapsed on write (§9.4).
CREATE TABLE slug_redirects (
    id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    old_path      VARCHAR(300)  NOT NULL,
    new_path      VARCHAR(300)  NOT NULL,
    status_code   SMALLINT UNSIGNED NOT NULL DEFAULT 301,
    source        VARCHAR(20)   NOT NULL DEFAULT 'auto', -- auto | manual
    created_by    BIGINT UNSIGNED NULL,
    created_at    DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_slug_redirects_old_path (old_path),
    KEY ix_slug_redirects_new_path (new_path)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Two-level catalogue: top-level categories and subcategories (parent_id).
CREATE TABLE categories (
    id                INT UNSIGNED  NOT NULL AUTO_INCREMENT,
    public_id         BINARY(16)    NOT NULL,
    parent_id         INT UNSIGNED  NULL,
    slug              VARCHAR(120)  NOT NULL,
    name              VARCHAR(120)  NOT NULL,
    description       TEXT          NULL,
    sort_order        INT           NOT NULL DEFAULT 0,
    status            VARCHAR(20)   NOT NULL DEFAULT 'draft', -- draft | published | archived
    meta_title        VARCHAR(160)  NULL,
    meta_description  VARCHAR(320)  NULL,
    published_at      DATETIME(3)   NULL,     -- first publication; slug changes after it create redirects
    created_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_categories_public_id (public_id),
    UNIQUE KEY uq_categories_slug (slug),
    KEY ix_categories_parent (parent_id, status, sort_order),
    CONSTRAINT fk_categories_parent FOREIGN KEY (parent_id) REFERENCES categories (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE services (
    id                INT UNSIGNED  NOT NULL AUTO_INCREMENT,
    public_id         BINARY(16)    NOT NULL,
    category_id       INT UNSIGNED  NOT NULL,
    slug              VARCHAR(120)  NOT NULL,
    name              VARCHAR(120)  NOT NULL,
    description       TEXT          NULL,
    requires_licence  TINYINT(1)    NOT NULL DEFAULT 0,  -- regulated trade (§12.1 licence badge)
    sort_order        INT           NOT NULL DEFAULT 0,
    status            VARCHAR(20)   NOT NULL DEFAULT 'draft',
    meta_title        VARCHAR(160)  NULL,
    meta_description  VARCHAR(320)  NULL,
    published_at      DATETIME(3)   NULL,
    created_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_services_public_id (public_id),
    UNIQUE KEY uq_services_slug (slug),
    KEY ix_services_category (category_id, status, sort_order),
    CONSTRAINT fk_services_category FOREIGN KEY (category_id) REFERENCES categories (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Search terms such as "fundi wa maji" → plumbing (§10.4).
CREATE TABLE service_synonyms (
    id                  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    service_id          INT UNSIGNED  NOT NULL,
    synonym             VARCHAR(120)  NOT NULL,
    synonym_normalized  VARCHAR(120)  NOT NULL,
    created_at          DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_service_synonyms (service_id, synonym_normalized),
    KEY ix_service_synonyms_normalized (synonym_normalized),
    CONSTRAINT fk_service_synonyms_service FOREIGN KEY (service_id) REFERENCES services (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Counties get a public identifier like every other API resource.
ALTER TABLE counties ADD COLUMN public_id BINARY(16) NULL AFTER id;
UPDATE counties SET public_id = UUID_TO_BIN(UUID(), 1) WHERE public_id IS NULL;
ALTER TABLE counties MODIFY public_id BINARY(16) NOT NULL, ADD UNIQUE KEY uq_counties_public_id (public_id);

CREATE TABLE towns (
    id                INT UNSIGNED  NOT NULL AUTO_INCREMENT,
    public_id         BINARY(16)    NOT NULL,
    county_id         INT UNSIGNED  NOT NULL,
    name              VARCHAR(120)  NOT NULL,
    slug              VARCHAR(140)  NOT NULL,
    is_selectable     TINYINT(1)    NOT NULL DEFAULT 0,
    is_indexable      TINYINT(1)    NOT NULL DEFAULT 0,
    status            VARCHAR(20)   NOT NULL DEFAULT 'active', -- active | archived
    merged_into_id    INT UNSIGNED  NULL,
    meta_title        VARCHAR(160)  NULL,
    meta_description  VARCHAR(320)  NULL,
    created_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_towns_public_id (public_id),
    UNIQUE KEY uq_towns_slug (slug),
    KEY ix_towns_county (county_id, status, name),
    CONSTRAINT fk_towns_county FOREIGN KEY (county_id) REFERENCES counties (id),
    CONSTRAINT fk_towns_merged FOREIGN KEY (merged_into_id) REFERENCES towns (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE areas (
    id                INT UNSIGNED  NOT NULL AUTO_INCREMENT,
    public_id         BINARY(16)    NOT NULL,
    town_id           INT UNSIGNED  NOT NULL,
    name              VARCHAR(120)  NOT NULL,
    slug              VARCHAR(140)  NOT NULL,
    is_selectable     TINYINT(1)    NOT NULL DEFAULT 0,
    is_indexable      TINYINT(1)    NOT NULL DEFAULT 0,
    status            VARCHAR(20)   NOT NULL DEFAULT 'active',
    merged_into_id    INT UNSIGNED  NULL,
    meta_title        VARCHAR(160)  NULL,
    meta_description  VARCHAR(320)  NULL,
    created_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_areas_public_id (public_id),
    UNIQUE KEY uq_areas_slug (slug),
    KEY ix_areas_town (town_id, status, name),
    CONSTRAINT fk_areas_town FOREIGN KEY (town_id) REFERENCES towns (id),
    CONSTRAINT fk_areas_merged FOREIGN KEY (merged_into_id) REFERENCES areas (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- County, town and area slugs share one URL namespace (/services/{service}/{location}),
-- so uniqueness is enforced across all three here (§9.1).
CREATE TABLE location_slugs (
    slug           VARCHAR(140) NOT NULL,
    location_type  VARCHAR(10)  NOT NULL, -- county | town | area
    location_id    INT UNSIGNED NOT NULL,
    PRIMARY KEY (slug),
    UNIQUE KEY uq_location_slugs_target (location_type, location_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
INSERT INTO location_slugs (slug, location_type, location_id) SELECT slug, 'county', id FROM counties;

CREATE TABLE location_aliases (
    id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    location_type     VARCHAR(10)   NOT NULL,
    location_id       INT UNSIGNED  NOT NULL,
    alias             VARCHAR(120)  NOT NULL,
    alias_normalized  VARCHAR(120)  NOT NULL,
    created_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_location_aliases_normalized (alias_normalized), -- one alias → one location
    KEY ix_location_aliases_target (location_type, location_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Representative centroids. Spatial-indexed columns must be NOT NULL (§10.3), so a
-- location without a centroid simply has no row. Points are built with
-- ST_SRID(POINT(lng, lat), 4326) and read with ST_Latitude/ST_Longitude.
CREATE TABLE location_geo (
    location_type  VARCHAR(10)  NOT NULL,
    location_id    INT UNSIGNED NOT NULL,
    centroid       POINT        NOT NULL SRID 4326,
    source         VARCHAR(20)  NOT NULL DEFAULT 'admin',
    updated_at     DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (location_type, location_id),
    SPATIAL INDEX sx_location_geo_centroid (centroid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- +goose Down
DROP TABLE location_geo;
DROP TABLE location_aliases;
DROP TABLE location_slugs;
DROP TABLE areas;
DROP TABLE towns;
ALTER TABLE counties DROP INDEX uq_counties_public_id, DROP COLUMN public_id;
DROP TABLE service_synonyms;
DROP TABLE services;
DROP TABLE categories;
DROP TABLE slug_redirects;
DROP TABLE navigation_items;
DROP TABLE navigation_menus;
DROP TABLE managed_pages;
