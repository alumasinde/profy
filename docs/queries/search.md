# Search query plans and latency (Phase 5)

Plan §10.6 / §14.2 require reviewed plans for critical queries. These were captured on MySQL 8.0
with synthetic data in a disposable database (`tests/perf`, build tag `perf`):
10,000 published providers, 40 services, 2 counties, 24 towns, 240 areas with centroids,
~25,000 coverage rows and 4,000 map pins (40 % of providers).

Reproduce:

```bash
PERF_DATABASE_DSN='user:pass@tcp(127.0.0.1:3306)/hudumalink_perf?parseTime=true' \
PERF_PLANS_OUT=docs/queries/plans.txt go test -tags perf -run Perf -v ./tests/perf
```

## Latency (200 requests each, in-process HTTP, single MySQL instance)

| Request | p50 | p95 | Target (§19.1) |
|---|---|---|---|
| API search, area | 90 ms | 127 ms | ≤ 500 ms |
| API search, town | 33 ms | 65 ms | ≤ 500 ms |
| API search, county (all of Nairobi) | 116 ms | 263 ms | ≤ 500 ms |
| API search, near me (10 km) | 78 ms | 118 ms | ≤ 500 ms |
| Landing page (service × area, HTML) | 89 ms | 118 ms | ≤ 300 ms |
| Service page (HTML) | 14 ms | 19 ms | ≤ 300 ms |
| Location page (town, HTML) | 95 ms | 124 ms | ≤ 300 ms |
| Homepage (HTML) | 35 ms | 53 ms | ≤ 300 ms |
| Provider profile (HTML) | 6 ms | 9 ms | ≤ 300 ms |

Daily landing evaluation over the same data: 10,640 service × location rows in 2.3 s.

## Observations

- Coverage, base-area and offering lookups are covering index range scans
  (`ix_provider_service_areas_reverse`, `ix_providers_base_area`, `ix_provider_services_search`).
- The spatial index (`sx_provider_geo_location`) is used for selective boxes (see "selective box" below).
  For area searches the box is sized to the maximum provider travel radius (50 km), which covers a whole
  city; MySQL then correctly prefers a table scan of `provider_geo`. Exact distances are computed only for
  rows in the box. If pins grow far beyond launch scale, size the box to the largest radius actually in use.
- County searches are the slowest because they expand to every town and area in the county; they remain
  well within budget. Results are capped at 5,000 candidates before ranking.

## Plans

### coverage match (area keys)

```sql
SELECT psa.provider_id, psa.location_id FROM provider_service_areas psa JOIN providers p ON p.id = psa.provider_id AND p.listing_status = 'published' WHERE psa.location_type = 'area' AND psa.location_id IN (1,2,3,4,5,6,7,8,9,10)
```

```text
-> Nested loop inner join  (cost=323 rows=292) (actual time=0.0596..1.38 rows=583 loops=1)
    -> Filter: ((psa.location_type = 'area') and (psa.location_id in (1,2,3,4,5,6,7,8,9,10)))  (cost=119 rows=583) (actual time=0.0495..0.363 rows=583 loops=1)
        -> Covering index range scan on psa using ix_provider_service_areas_reverse over (location_type = 'area' AND location_id = 1) OR (location_type = 'area' AND location_id = 2) OR (8 more)  (cost=119 rows=583) (actual time=0.0474..0.282 rows=583 loops=1)
    -> Filter: (p.listing_status = 'published')  (cost=0.25 rows=0.5) (actual time=0.0015..0.00158 rows=1 loops=583)
        -> Single-row index lookup on p using PRIMARY (id=psa.provider_id)  (cost=0.25 rows=1) (actual time=0.00131..0.00134 rows=1 loops=583)

```

### base area match

```sql
SELECT id, base_area_id FROM providers WHERE listing_status = 'published' AND base_area_id IN (1,2,3,4,5,6,7,8,9,10)
```

```text
-> Filter: ((providers.listing_status = 'published') and (providers.base_area_id in (1,2,3,4,5,6,7,8,9,10)))  (cost=79.6 rows=391) (actual time=0.0231..0.268 rows=391 loops=1)
    -> Covering index range scan on providers using ix_providers_base_area over (base_area_id = 1 AND listing_status = 'published') OR (base_area_id = 2 AND listing_status = 'published') OR (8 more)  (cost=79.6 rows=391) (actual time=0.0215..0.206 rows=391 loops=1)

```

### spatial candidates (10 km box)

```sql
SELECT pg.provider_id, ST_Distance_Sphere(pg.location, ST_SRID(POINT(36.8, -1.28), 4326)), COALESCE(p.service_radius_km, 0) FROM provider_geo pg JOIN providers p ON p.id = pg.provider_id WHERE MBRContains(ST_GeomFromText('POLYGON((36.35 -1.73,37.25 -1.73,37.25 -0.83,36.35 -0.83,36.35 -1.73))', 4326, 'axis-order=long-lat'), pg.location) AND p.listing_status = 'published'
```

```text
-> Nested loop inner join  (cost=1774 rows=1966) (actual time=0.0414..32.5 rows=3951 loops=1)
    -> Filter: mbrcontains(<cache>(st_geomfromtext('POLYGON((36.35 -1.73,37.25 -1.73,37.25 -0.83,36.35 -0.83,36.35 -1.73))',4326,'axis-order=long-lat')),pg.location)  (cost=398 rows=3931) (actual time=0.0333..24.1 rows=3951 loops=1)
        -> Table scan on pg  (cost=398 rows=3931) (actual time=0.00864..2.49 rows=3951 loops=1)
    -> Filter: (p.listing_status = 'published')  (cost=0.25 rows=0.5) (actual time=0.00185..0.00194 rows=1 loops=3951)
        -> Single-row index lookup on p using PRIMARY (id=pg.provider_id)  (cost=0.25 rows=1) (actual time=0.00158..0.00161 rows=1 loops=3951)

```

### offerings for candidates

```sql
SELECT provider_id, service_id FROM provider_services WHERE status = 'active' AND service_id IN (1,2) AND provider_id IN (1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20)
```

```text
-> Filter: ((provider_services.`status` = 'active') and (provider_services.service_id in (1,2)) and (provider_services.provider_id in (1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20)))  (cost=8.33 rows=40) (actual time=0.0621..0.0621 rows=0 loops=1)
    -> Covering index range scan on provider_services using ix_provider_services_search over (service_id = 1 AND status = 'active' AND provider_id = 1) OR (service_id = 1 AND status = 'active' AND provider_id = 2) OR (38 more)  (cost=8.33 rows=40) (actual time=0.0612..0.0612 rows=0 loops=1)

```

### offerings, no location

```sql
SELECT ps.provider_id, ps.service_id FROM provider_services ps JOIN providers p ON p.id = ps.provider_id AND p.listing_status = 'published' WHERE ps.status = 'active' AND ps.service_id IN (1) LIMIT 20000
```

```text
-> Limit: 20000 row(s)  (cost=200 rows=220) (actual time=0.0259..0.944 rows=441 loops=1)
    -> Nested loop inner join  (cost=200 rows=220) (actual time=0.0253..0.918 rows=441 loops=1)
        -> Covering index lookup on ps using ix_provider_services_search (service_id=1, status='active')  (cost=45.2 rows=441) (actual time=0.0177..0.175 rows=441 loops=1)
        -> Filter: (p.listing_status = 'published')  (cost=0.25 rows=0.5) (actual time=0.00143..0.00152 rows=1 loops=441)
            -> Single-row index lookup on p using PRIMARY (id=ps.provider_id)  (cost=0.25 rows=1) (actual time=0.00123..0.00126 rows=1 loops=441)

```

### landing state lookup

```sql
SELECT id, robots_mode, indexed, provider_count FROM seo_landing_pages WHERE service_id = 1 AND location_type = 'area' AND location_id = 1
```

```text
-> Rows fetched before execution  (cost=0..0 rows=1) (actual time=69e-6..131e-6 rows=1 loops=1)

```

### spatial candidates, selective box (2 km)

```text
-> Filter: mbrcontains(<cache>(st_geomfromtext('POLYGON((36.79 -1.29,36.81 -1.29,36.81 -1.27,36.79 -1.27,36.79 -1.29))',4326,'axis-order=long-lat')),pg.location)  (cost=20.1 rows=44) (actual time=0.958..4.39 rows=35 loops=1)
    -> Index range scan on pg using sx_provider_geo_location over (location unprintable_geometry_value)  (cost=20.1 rows=44) (actual time=0.881..4.08 rows=35 loops=1)
```
