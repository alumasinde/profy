-- Identity, authentication and authorization (plan §5, §11, §13.2).

-- +goose Up
CREATE TABLE users (
    id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    public_id         BINARY(16)    NOT NULL,
    first_name        VARCHAR(100)  NOT NULL,
    middle_name       VARCHAR(100)  NULL,
    last_name         VARCHAR(100)  NOT NULL,
    email             VARCHAR(254)  NULL,          -- normalized lowercase
    phone_e164        VARCHAR(16)   NULL,          -- normalized E.164
    email_verified_at DATETIME(3)   NULL,
    phone_verified_at DATETIME(3)   NULL,
    preferred_locale  VARCHAR(10)   NOT NULL DEFAULT 'en',
    status            VARCHAR(20)   NOT NULL DEFAULT 'active', -- active | suspended | pending_deletion | deleted
    last_login_at     DATETIME(3)   NULL,
    created_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_users_public_id (public_id),
    UNIQUE KEY uq_users_email (email),
    UNIQUE KEY uq_users_phone (phone_e164),
    KEY ix_users_status_created (status, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE roles (
    id            INT UNSIGNED  NOT NULL AUTO_INCREMENT,
    role_key      VARCHAR(60)   NOT NULL,
    name          VARCHAR(100)  NOT NULL,
    description   VARCHAR(255)  NOT NULL DEFAULT '',
    is_system     TINYINT(1)    NOT NULL DEFAULT 0,
    is_protected  TINYINT(1)    NOT NULL DEFAULT 0, -- only super administrators may assign
    created_at    DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at    DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_roles_key (role_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Keys are defined in code (internal/authz/catalogue.go) and synchronized by `server seed`.
CREATE TABLE permissions (
    id              INT UNSIGNED  NOT NULL AUTO_INCREMENT,
    permission_key  VARCHAR(100)  NOT NULL,
    description     VARCHAR(255)  NOT NULL DEFAULT '',
    module          VARCHAR(60)   NOT NULL,
    created_at      DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_permissions_key (permission_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE role_permissions (
    role_id         INT UNSIGNED  NOT NULL,
    permission_id   INT UNSIGNED  NOT NULL,
    created_at      DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (role_id, permission_id),
    KEY ix_role_permissions_permission (permission_id),
    CONSTRAINT fk_role_permissions_role FOREIGN KEY (role_id) REFERENCES roles (id) ON DELETE CASCADE,
    CONSTRAINT fk_role_permissions_permission FOREIGN KEY (permission_id) REFERENCES permissions (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE user_roles (
    user_id         BIGINT UNSIGNED NOT NULL,
    role_id         INT UNSIGNED  NOT NULL,
    assigned_by     BIGINT UNSIGNED NULL,
    created_at      DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (user_id, role_id),
    KEY ix_user_roles_role (role_id),
    CONSTRAINT fk_user_roles_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT fk_user_roles_role FOREIGN KEY (role_id) REFERENCES roles (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- A browser/app installation. PIN login is only allowed on trusted devices.
CREATE TABLE user_devices (
    id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    public_id         BINARY(16)    NOT NULL,
    user_id           BIGINT UNSIGNED NOT NULL,
    device_token_hash BINARY(32)    NOT NULL,
    name              VARCHAR(120)  NOT NULL DEFAULT '',
    trusted_at        DATETIME(3)   NULL,
    last_seen_at      DATETIME(3)   NULL,
    revoked_at        DATETIME(3)   NULL,
    created_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_user_devices_public_id (public_id),
    UNIQUE KEY uq_user_devices_user_token (user_id, device_token_hash),
    CONSTRAINT fk_user_devices_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Opaque server-side sessions; only the SHA-256 of the token is stored.
CREATE TABLE user_sessions (
    id                  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    public_id           BINARY(16)    NOT NULL,
    user_id             BIGINT UNSIGNED NOT NULL,
    device_id           BIGINT UNSIGNED NULL,
    token_hash          BINARY(32)    NOT NULL,
    auth_method         VARCHAR(20)   NOT NULL, -- otp | pin | recovery
    ip_address          VARCHAR(45)   NULL,
    user_agent          VARCHAR(255)  NULL,
    reauthenticated_at  DATETIME(3)   NULL,
    last_seen_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    idle_expires_at     DATETIME(3)   NOT NULL,
    absolute_expires_at DATETIME(3)   NOT NULL,
    revoked_at          DATETIME(3)   NULL,
    revoked_reason      VARCHAR(40)   NULL,
    created_at          DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_user_sessions_token_hash (token_hash),
    UNIQUE KEY uq_user_sessions_public_id (public_id),
    KEY ix_user_sessions_user (user_id, revoked_at),
    KEY ix_user_sessions_expiry (absolute_expires_at),
    CONSTRAINT fk_user_sessions_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT fk_user_sessions_device FOREIGN KEY (device_id) REFERENCES user_devices (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Credentials other than OTP. PIN is Argon2id (PHC string).
CREATE TABLE user_credentials (
    user_id             BIGINT UNSIGNED NOT NULL,
    credential_type     VARCHAR(20)   NOT NULL, -- pin
    secret_hash         VARCHAR(255)  NOT NULL,
    failed_attempts     INT UNSIGNED  NOT NULL DEFAULT 0,
    blocked_until       DATETIME(3)   NULL,     -- progressive delay
    locked_at           DATETIME(3)   NULL,     -- requires OTP to unlock
    created_at          DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at          DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (user_id, credential_type),
    CONSTRAINT fk_user_credentials_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- OTP challenges. code_hash is HMAC-SHA256(server secret, challenge id || code).
-- user_id is NULL for registration and for decoy challenges (unknown destinations).
CREATE TABLE otp_challenges (
    id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    public_id       BINARY(16)    NOT NULL,
    purpose         VARCHAR(30)   NOT NULL, -- registration | login | recovery | sensitive_change
    channel         VARCHAR(10)   NOT NULL, -- sms | email
    destination     VARCHAR(254)  NOT NULL,
    user_id         BIGINT UNSIGNED NULL,
    code_hash       BINARY(32)    NOT NULL,
    attempts        INT UNSIGNED  NOT NULL DEFAULT 0,
    max_attempts    INT UNSIGNED  NOT NULL DEFAULT 5,
    metadata        JSON          NULL,     -- registration names; never secrets
    ip_address      VARCHAR(45)   NULL,
    expires_at      DATETIME(3)   NOT NULL,
    consumed_at     DATETIME(3)   NULL,
    created_at      DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uq_otp_challenges_public_id (public_id),
    KEY ix_otp_challenges_destination (destination, purpose, created_at),
    KEY ix_otp_challenges_created (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE user_consents (
    id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    user_id         BIGINT UNSIGNED NOT NULL,
    consent_type    VARCHAR(30)   NOT NULL, -- terms | privacy | marketing
    version         VARCHAR(30)   NOT NULL,
    granted         TINYINT(1)    NOT NULL DEFAULT 1,
    ip_address      VARCHAR(45)   NULL,
    created_at      DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    KEY ix_user_consents_user (user_id, consent_type, created_at),
    CONSTRAINT fk_user_consents_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- +goose Down
DROP TABLE user_consents;
DROP TABLE otp_challenges;
DROP TABLE user_credentials;
DROP TABLE user_sessions;
DROP TABLE user_devices;
DROP TABLE user_roles;
DROP TABLE role_permissions;
DROP TABLE permissions;
DROP TABLE roles;
DROP TABLE users;
