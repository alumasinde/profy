-- Verification badge labels and explanations (plan §12.1) and marketplace settings.
-- INSERT IGNORE: administrators' edits are never overwritten.
INSERT IGNORE INTO verification_badge_types (badge_type, label, description, sort_order) VALUES
    ('phone_verified',      'Phone verified',      'The professional confirmed they control this phone number with a one-time code.', 10),
    ('email_verified',      'Email verified',      'The professional confirmed they control this email address with a one-time code.', 20),
    ('identity_checked',    'Identity checked',    'Our team reviewed a government ID that matches the account holder''s name.', 30),
    ('business_registered', 'Business registered', 'Our team reviewed business registration documents for this business.', 40),
    ('licence_checked',     'Licence checked',     'Our team reviewed a professional licence for this service. Licences are re-checked when they expire.', 50);

INSERT IGNORE INTO site_settings (setting_key, value_json, is_public) VALUES
    ('provider_terms_version',     JSON_QUOTE('2026-11-draft'), 1),
    ('listing_edit_moderation',    JSON_QUOTE('post'), 0),
    ('portfolio_moderation',       JSON_QUOTE('post'), 0),
    ('default_contact_visibility', JSON_QUOTE('reveal_on_click'), 0),
    ('portfolio_max_items',        CAST('30' AS JSON), 0),
    ('upload_max_mb',              CAST('8' AS JSON), 0);
