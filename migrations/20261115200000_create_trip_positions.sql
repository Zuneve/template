-- +goose Up
-- last_position_at — время фиксации самой свежей координаты поездки (по
-- recorded_at, а не по приходу в сервис). NULL, пока координат не было.
ALTER TABLE trips ADD COLUMN IF NOT EXISTS last_position_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS trip_positions (
    id          BIGSERIAL PRIMARY KEY,
    trip_id     UUID NOT NULL REFERENCES trips(id) ON DELETE CASCADE,
    latitude    DOUBLE PRECISION NOT NULL CHECK (latitude BETWEEN -90 AND 90),
    longitude   DOUBLE PRECISION NOT NULL CHECK (longitude BETWEEN -180 AND 180),
    recorded_at TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Маршрут строится по recorded_at: устройство копит точки офлайн и шлёт их
-- пачкой, поэтому порядок прихода в сервис с порядком точек не совпадает.
CREATE INDEX IF NOT EXISTS trip_positions_trip_recorded_idx
    ON trip_positions (trip_id, recorded_at, id);

-- +goose Down
DROP TABLE IF EXISTS trip_positions;
ALTER TABLE trips DROP COLUMN IF EXISTS last_position_at;
