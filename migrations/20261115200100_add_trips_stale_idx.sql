-- +goose NO TRANSACTION

-- +goose Up
-- Индекс под выборку поездок для опроса: статус active, возраст последней
-- координаты (или started_at, если координат не было) больше порога, самые
-- давно молчащие первыми. Частичный индекс держит только активные поездки,
-- завершённых со временем большинство, а сканер их не читает. Выражение
-- COALESCE должно совпадать с запросом дословно, иначе планировщик не
-- сможет использовать индекс. CONCURRENTLY — чтобы не блокировать запись в
-- trips, поэтому миграция идёт вне транзакции.
CREATE INDEX CONCURRENTLY IF NOT EXISTS trips_active_stale_idx
    ON trips ((COALESCE(last_position_at, started_at)))
    WHERE status = 'active';

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS trips_active_stale_idx;
