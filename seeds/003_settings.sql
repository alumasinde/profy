-- Default site settings and feature flags (plan §4.1, §8.1).
-- INSERT IGNORE: defaults are created once and never overwrite administrator changes.
-- "HudumaLink" is a configurable working name, not a product identity.
INSERT IGNORE INTO site_settings (setting_key, value_json, is_public) VALUES
    ('site_name',         JSON_QUOTE('HudumaLink'), 1),
    ('site_short_name',   JSON_QUOTE('HudumaLink'), 1),
    ('site_tagline',      JSON_QUOTE('Find trusted professionals near you.'), 1),
    ('site_description',  JSON_QUOTE('Discover local professionals and service businesses in Kenya by service and location.'), 1),
    ('site_url',          JSON_QUOTE(''), 1),
    ('support_email',     JSON_QUOTE(''), 1),
    ('support_phone',     JSON_QUOTE(''), 1),
    ('whatsapp_number',   JSON_QUOTE(''), 1),
    ('social_links',      JSON_ARRAY(), 1),
    ('logo_light',        JSON_QUOTE(''), 1),
    ('logo_dark',         JSON_QUOTE(''), 1),
    ('favicon',           JSON_QUOTE(''), 1),
    ('default_og_image',  JSON_QUOTE(''), 1),
    ('primary_color',     JSON_QUOTE('#0f766e'), 1),
    ('secondary_color',   JSON_QUOTE('#1e293b'), 1),
    ('accent_color',      JSON_QUOTE('#b45309'), 1),
    ('default_locale',    JSON_QUOTE('en'), 1),
    ('default_timezone',  JSON_QUOTE('Africa/Nairobi'), 1),
    ('default_country',   JSON_QUOTE('KE'), 1),
    ('default_currency',  JSON_QUOTE('KES'), 1),
    ('terms_version',     JSON_QUOTE('2026-11-draft'), 1),
    ('privacy_version',   JSON_QUOTE('2026-11-draft'), 1),
    ('otp_channels',      JSON_ARRAY('sms', 'email'), 0),
    ('maintenance_mode',  CAST('false' AS JSON), 0);

INSERT IGNORE INTO feature_flags (flag_key, is_enabled, description) VALUES
    ('jobs',      0, 'Job records and My Jobs navigation (follow-up release)'),
    ('pin_login', 1, 'Allow PIN login on trusted devices');
