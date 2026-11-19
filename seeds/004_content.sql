-- Navigation menus and draft information pages (plan §2.1, §7.2, §8.1).
-- Pages are created as DRAFTS with placeholder text so administrators can write and publish
-- them; nothing here is public until an administrator publishes it. Never overwrites edits.
INSERT IGNORE INTO navigation_menus (menu_key, name) VALUES
    ('header', 'Header'),
    ('footer', 'Footer');

INSERT IGNORE INTO managed_pages (public_id, slug, title, body, status) VALUES
    (UUID_TO_BIN(UUID(), 1), 'how-it-works',        'How it works',          'Draft. Write this page before publishing it.', 'draft'),
    (UUID_TO_BIN(UUID(), 1), 'about',               'About us',              'Draft. Write this page before publishing it.', 'draft'),
    (UUID_TO_BIN(UUID(), 1), 'contact',             'Contact us',            'Draft. Write this page before publishing it.', 'draft'),
    (UUID_TO_BIN(UUID(), 1), 'join-as-professional','Join as a professional','Draft. Write this page before publishing it.', 'draft'),
    (UUID_TO_BIN(UUID(), 1), 'privacy-policy',      'Privacy policy',        'Draft. This page needs legal review before publishing (plan §16.2).', 'draft'),
    (UUID_TO_BIN(UUID(), 1), 'terms',               'Terms of use',          'Draft. This page needs legal review before publishing.', 'draft');
