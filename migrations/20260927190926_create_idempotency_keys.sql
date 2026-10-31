-- +goose Up
-- Ключ идемпотентности POST /api/v1/trips (задание со звёздочкой). Хранит,
-- какая поездка была создана по конкретному ключу и хэш тела запроса, чтобы
-- отличить повтор того же запроса (тот же хэш — отдать ту же поездку) от
-- повторного использования ключа с другим телом (409 idempotency_conflict).
-- PRIMARY KEY на key одновременно и уникальность, и защита от гонки: если два
-- запроса с одним новым ключом стартуют параллельно, второй INSERT упадёт по
-- unique_violation, а не создаст вторую поездку.
--
-- trip_id — DEFERRABLE INITIALLY DEFERRED: обработчик вставляет строку сюда
-- раньше, чем создаёт саму поездку, чтобы гонка на новом ключе билась именно
-- об unique_violation по key, а не о partial unique index "один активный
-- рейс на водителя" в trips (иначе конкурентный повтор того же запроса иногда
-- получал бы 409 driver_busy вместо честного повторного 200). При обычной,
-- незащищённой FK, INSERT сюда упал бы сразу, потому что строки в trips ещё
-- нет; DEFERRABLE откладывает проверку внешнего ключа до COMMIT транзакции,
-- где обе строки уже на месте.
CREATE TABLE IF NOT EXISTS idempotency_keys (
    key          UUID PRIMARY KEY,
    request_hash TEXT NOT NULL,
    trip_id      UUID NOT NULL REFERENCES trips (id) DEFERRABLE INITIALLY DEFERRED,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS idempotency_keys;
