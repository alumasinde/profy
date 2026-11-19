-- Kenya's 47 counties with their official codes, per the First Schedule of the
-- Constitution of Kenya, 2010. Versioned reference data; idempotent.
-- is_selectable marks the proposed pilot region (Nairobi, Kiambu — plan §3.1).
-- Re-running this seed updates names but never overrides admin-set flags.
INSERT INTO counties (public_id, country_id, code, name, slug, is_selectable) VALUES
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '001', 'Mombasa', 'mombasa', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '002', 'Kwale', 'kwale', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '003', 'Kilifi', 'kilifi', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '004', 'Tana River', 'tana-river', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '005', 'Lamu', 'lamu', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '006', 'Taita-Taveta', 'taita-taveta', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '007', 'Garissa', 'garissa', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '008', 'Wajir', 'wajir', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '009', 'Mandera', 'mandera', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '010', 'Marsabit', 'marsabit', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '011', 'Isiolo', 'isiolo', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '012', 'Meru', 'meru', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '013', 'Tharaka-Nithi', 'tharaka-nithi', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '014', 'Embu', 'embu', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '015', 'Kitui', 'kitui', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '016', 'Machakos', 'machakos', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '017', 'Makueni', 'makueni', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '018', 'Nyandarua', 'nyandarua', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '019', 'Nyeri', 'nyeri', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '020', 'Kirinyaga', 'kirinyaga', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '021', 'Murang''a', 'muranga', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '022', 'Kiambu', 'kiambu', 1),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '023', 'Turkana', 'turkana', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '024', 'West Pokot', 'west-pokot', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '025', 'Samburu', 'samburu', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '026', 'Trans Nzoia', 'trans-nzoia', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '027', 'Uasin Gishu', 'uasin-gishu', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '028', 'Elgeyo-Marakwet', 'elgeyo-marakwet', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '029', 'Nandi', 'nandi', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '030', 'Baringo', 'baringo', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '031', 'Laikipia', 'laikipia', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '032', 'Nakuru', 'nakuru', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '033', 'Narok', 'narok', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '034', 'Kajiado', 'kajiado', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '035', 'Kericho', 'kericho', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '036', 'Bomet', 'bomet', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '037', 'Kakamega', 'kakamega', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '038', 'Vihiga', 'vihiga', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '039', 'Bungoma', 'bungoma', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '040', 'Busia', 'busia', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '041', 'Siaya', 'siaya', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '042', 'Kisumu', 'kisumu', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '043', 'Homa Bay', 'homa-bay', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '044', 'Migori', 'migori', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '045', 'Kisii', 'kisii', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '046', 'Nyamira', 'nyamira', 0),
    (UUID_TO_BIN(UUID(), 1), (SELECT id FROM countries WHERE iso2 = 'KE'), '047', 'Nairobi', 'nairobi', 1)
AS new
ON DUPLICATE KEY UPDATE name = new.name;

-- Counties share the location slug namespace with towns and areas.
INSERT IGNORE INTO location_slugs (slug, location_type, location_id)
SELECT slug, 'county', id FROM counties;
