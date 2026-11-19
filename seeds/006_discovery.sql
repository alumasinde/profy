-- Discovery settings (plan §9.6, §10.5). INSERT IGNORE keeps administrators' edits.
INSERT IGNORE INTO site_settings (setting_key, value_json, is_public) VALUES
    ('landing_min_providers',    CAST('3' AS JSON), 0),
    ('landing_hysteresis_days',  CAST('14' AS JSON), 0),
    ('rank_weight_service',      CAST('35' AS JSON), 0),
    ('rank_weight_location',     CAST('30' AS JSON), 0),
    ('rank_weight_completeness', CAST('15' AS JSON), 0),
    ('rank_weight_rating',       CAST('10' AS JSON), 0),
    ('rank_weight_activity',     CAST('10' AS JSON), 0),
    ('rank_exploration_points',  CAST('5' AS JSON), 0);
