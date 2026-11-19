-- Country reference data. Kenya is the launch country; the schema is not Kenya-specific.
INSERT INTO countries (iso2, name, slug, calling_code) VALUES
    ('KE', 'Kenya', 'kenya', '254')
AS new
ON DUPLICATE KEY UPDATE name = new.name, calling_code = new.calling_code;
