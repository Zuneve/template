# TripGo — Trip Service

Сервис поездок курса «Разработка микросервисов на Go». Лабораторная работа 1:
HTTP API и PostgreSQL.

## Запуск

Локально (нужны Go 1.27+, `make` и `tripgoctl` из `course-infra`):

```bash
tripgoctl cluster start       # один раз на машине
tripgoctl environment start   # поднимает PostgreSQL и создаёт .env
tripgoctl connect             # показывает актуальный адрес PostgreSQL
make migrate                  # накатить миграции
make run                      # go run ./cmd/trip-service
```

Сервис слушает `HTTP_ADDR` (в примере `:8080`). Все переменные обязательны:
если какой-то нет, сервис завершится с ошибкой при запуске.
`environment.toml` уже лежит в корне репозитория; `tripgoctl environment start`
создаёт локальный `.env` с актуальным адресом PostgreSQL. `Makefile` читает
этот файл, поэтому копировать `.env.example` поверх него не нужно.

Через Docker Compose (сервис + PostgreSQL в одной сети, без установки Go
локально):

```bash
cp deploy/.env.example deploy/.env
cd deploy
docker compose up --build
```

Compose поднимает `postgres` (с healthcheck на `pg_isready`), затем
одноразовый `migrate` (накатывает миграции и завершается), затем
`trip-service` — он стартует только после того, как оба предыдущих шага
закончились успешно. Сервис доступен на `localhost:8080`.
Остановить стек можно командой `docker compose down` из каталога `deploy/`.
Если порт 8080 занят, локальный запуск принимает `make run HTTP_ADDR=:18080`,
а для Compose задайте `TRIP_SERVICE_HOST_PORT=18080 docker compose up --build`
из каталога `deploy/`. В примерах ниже тогда поменяйте `BASE_URL` на
`http://localhost:18080`.

## Переменные окружения

Полный список с примером значений — в [`.env.example`](.env.example).

| Переменная | Назначение |
|---|---|
| `HTTP_ADDR` | адрес, на котором слушает HTTP-сервер |
| `LOG_LEVEL` | уровень (`debug`/`info`/`warn`/`error`); сейчас проверяется при старте, фильтрация логов появится в ЛР2 |
| `SHUTDOWN_TIMEOUT` | сколько ждать текущие запросы при `SIGTERM` перед принудительным выходом |
| `DATABASE_URL` | строка подключения к PostgreSQL |
| `DATABASE_MAX_CONNS` / `DATABASE_MIN_CONNS` | границы пула соединений pgx |
| `DATABASE_MAX_CONN_LIFETIME` | максимальное время жизни соединения в пуле |
| `DATABASE_CONNECT_TIMEOUT` | таймаут установления соединения с БД |
| `DATABASE_QUERY_TIMEOUT` | таймаут на каждый запрос к БД |

`TRIP_SERVICE_HOST_PORT` — необязательный порт публикации сервиса только в
Docker Compose (по умолчанию 8080); в окружение самого сервиса он не передаётся.

Для `deploy/docker-compose.yml` дополнительно нужны `POSTGRES_USER`,
`POSTGRES_PASSWORD`, `POSTGRES_DB` — см. `deploy/.env.example`.

## Тесты

```bash
make test   # go test -race ./...
```

Ручная проверка (после `make run` или `docker compose up`; нужны `curl`,
`jq` и `uuidgen`). Команды запускаются из корня репозитория. Новый UUID
водителя при каждом запуске не даёт старым активным поездкам повлиять на
результат проверки:

```bash
BASE_URL=http://localhost:8080
DRIVER_ID=$(uuidgen)
cat > /tmp/tripgo-trip.json <<JSON
{"user_id":"5cb72c04-7650-45c9-a79b-bcdba0631e0c",
 "driver_id":"$DRIVER_ID",
 "start_point":{"latitude":59.9398,"longitude":30.3146},
 "end_point":{"latitude":59.9290,"longitude":30.3626},
 "price":1450}
JSON

# 201 Created: заголовок Location и JSON с id
curl -sS -D /tmp/tripgo-headers -o /tmp/tripgo-created.json \
  -X POST "$BASE_URL/api/v1/trips" \
  -H 'content-type: application/json' -d @/tmp/tripgo-trip.json
cat /tmp/tripgo-headers
jq . /tmp/tripgo-created.json
TRIP_ID=$(jq -r .id /tmp/tripgo-created.json)

# тот же водитель, пока поездка активна: 409 driver_busy
curl -sS -i -X POST "$BASE_URL/api/v1/trips" \
  -H 'content-type: application/json' -d @/tmp/tripgo-trip.json

curl -sS "$BASE_URL/api/v1/trips/$TRIP_ID" | jq
curl -sS -i -X POST "$BASE_URL/api/v1/trips/$TRIP_ID/finish"  # 200
curl -sS -i -X POST "$BASE_URL/api/v1/trips/$TRIP_ID/finish"  # 409 trip_completed

# первый запрос с ключом: 201; повтор с тем же ключом и телом: 200, тот же id
IDEMPOTENCY_KEY=$(uuidgen)
curl -sS -i -X POST "$BASE_URL/api/v1/trips" \
  -H 'content-type: application/json' -H "Idempotency-Key: $IDEMPOTENCY_KEY" \
  -d @/tmp/tripgo-trip.json
curl -sS -i -X POST "$BASE_URL/api/v1/trips" \
  -H 'content-type: application/json' -H "Idempotency-Key: $IDEMPOTENCY_KEY" \
  -d @/tmp/tripgo-trip.json

# тот же ключ, но другая цена: 409 idempotency_conflict
jq '.price = 1500' /tmp/tripgo-trip.json > /tmp/tripgo-changed.json
curl -sS -i -X POST "$BASE_URL/api/v1/trips" \
  -H 'content-type: application/json' -H "Idempotency-Key: $IDEMPOTENCY_KEY" \
  -d @/tmp/tripgo-changed.json

# 20 одновременных запросов новому водителю: один 201, остальные 409
jq --arg driver "$(uuidgen)" '.driver_id = $driver' \
  /tmp/tripgo-trip.json > /tmp/tripgo-race.json
seq 20 | xargs -P20 -I{} curl -s -o /dev/null -w '%{http_code}\n' \
  -X POST "$BASE_URL/api/v1/trips" -H 'content-type: application/json' \
  -d @/tmp/tripgo-race.json | sort | uniq -c

# 20 одновременных завершений одной поездки: один 200, остальные 409
jq --arg driver "$(uuidgen)" '.driver_id = $driver' \
  /tmp/tripgo-trip.json > /tmp/tripgo-finish.json
FINISH_ID=$(curl -sS -X POST "$BASE_URL/api/v1/trips" \
  -H 'content-type: application/json' -d @/tmp/tripgo-finish.json | jq -r .id)
seq 20 | xargs -P20 -I{} curl -s -o /dev/null -w '%{http_code}\n' \
  -X POST "$BASE_URL/api/v1/trips/$FINISH_ID/finish" | sort | uniq -c

curl -sS -i "$BASE_URL/health"  # 200, без обращения к БД
curl -sS -i "$BASE_URL/ready"   # 200 при доступной БД
```

`make generate` повторно создаёт `internal/generated/api.gen.go` из
`contracts/openapi/trip-service.openapi.yaml`. Миграции можно проверить
командой `go tool goose -dir migrations postgres "$DATABASE_URL" status`;
для неё нужно загрузить переменные из `.env` в текущую оболочку (например,
`set -a; . ./.env; set +a`). `make migrate` применяет все новые миграции.

## Что сделано

- HTTP-ручки создания, получения и завершения поездки, а также `/health` и
  `/ready` — по [OpenAPI-контракту](contracts/openapi/trip-service.openapi.yaml).
  `oapi-codegen` генерирует типы, серверный
  интерфейс и роутинг (`make generate`); реализация хендлеров написана в
  `internal/httpapi/`.
- Хранение поездок и истории статусов в PostgreSQL, миграции через `goose`
  (`migrations/`).
- Запрет двух активных поездок у одного водителя.
- Создание и завершение поездки обновляют `trips` и `trip_status_history`
  в одной транзакции.
- Грейсфул-шатдаун по `SIGTERM` с ожиданием текущих запросов
  (`SHUTDOWN_TIMEOUT`).
- `/health` и `/ready`: `/ready` проверяет соединение с БД и отдаёт `503`,
  если её нет.

### Бонусные задания

- **Идемпотентность `POST /api/v1/trips`** по заголовку `Idempotency-Key`:
  повторный запрос с тем же ключом и тем же телом возвращает ту же поездку и
  `200`; тот же ключ с другим телом — `409 idempotency_conflict`. Проверено
  в том числе на конкурентной гонке (5 параллельных запросов с одним новым
  ключом → один `201`, остальные `200` с тем же `trip_id`). Ключ хранится
  **24 часа** с первого успешного создания; при следующем запросе с ключом
  просроченные записи удаляются. Поездки и их история при этом сохраняются.
- **Dockerfile с многостадийной сборкой** (`deploy/Dockerfile`): стадия
  `builder` на `golang:1.27-alpine` собирает статический бинарь
  (`CGO_ENABLED=0`, `-trimpath -ldflags="-s -w"`), итоговый образ — на
  `gcr.io/distroless/static-debian12:nonroot` (без shell, пакетного
  менеджера, исходников и тулчейна), процесс запускается под `nonroot`
  (uid 65532). Итоговый размер образа — **около 20.7 MB**. Запуск рядом с
  PostgreSQL — через `deploy/docker-compose.yml`.

## Решения

**Уровень изоляции транзакций — Read Committed** (значение PostgreSQL по
умолчанию), задаётся в `internal/postgres/tx.go` при открытии транзакции.
Правило "один активный рейс на водителя" обеспечено не
`SELECT` внутри транзакции (это не защитило бы от гонки — оба конкурентных
запроса прошли бы проверку раньше, чем любой из них вставил строку), а
частичным уникальным индексом `trips_one_active_per_driver_idx` на уровне
БД (`driver_id WHERE status = 'active'`). Поэтому более строгий уровень
изоляции не нужен.

**Менеджер транзакций** (`internal/postgres/tx.go`, `TxManager.Do`) открывает
транзакцию, кладёт её в `context.Context` и вызывает переданную функцию с
обогащённым контекстом; `nil` на выходе — `COMMIT`, ошибка или паника —
`ROLLBACK` и проброс наружу. Репозитории не получают транзакцию аргументом
метода: они достают исполнителя (`Executor` — общий интерфейс `*pgxpool.Pool`
и `pgx.Tx`) из контекста через `postgres.ExecutorFromContext` и работают
одинаково что с пулом, что с транзакцией. Вложенный вызов `Do` внутри `Do`
переиспользует уже открытую транзакцию, а не открывает вторую.

**Запрет двух активных поездок у одного водителя** обеспечен частичным
уникальным индексом `trips_one_active_per_driver_idx` (`driver_id WHERE
status = 'active'`, см. `migrations/20260927181004_create_trips.sql`).
Конкурентная вставка второй активной поездки тому же водителю падает по
`unique_violation`; репозиторий (`internal/repository/trip.go`) распознаёт
эту ошибку по имени constraint'а и возвращает `ErrDriverBusy`, который
хендлер превращает в `409 driver_busy`.

**Конкурентное завершение** устроено одним `UPDATE` с условием
`WHERE id = ... AND status = 'active'`. Первый запрос переводит поездку в
`completed`; второй уже не находит активную строку, дополнительно читает
поездку и получает `409 trip_completed`. В той же транзакции записывается
строка истории со сменой статуса. Без условия по статусу второй запрос мог
бы перезаписать `finished_at`.

**Проверки состояния:** `/health` отвечает `200`, пока работает процесс, и
не обращается к БД. `/ready` проверяет PostgreSQL с таймаутом и отвечает
`503`, если сервис пока не может обслуживать запросы. Разделение позволяет
убрать временно неготовый экземпляр из трафика, не перезапуская живой процесс.

**Идемпотентность** (`internal/repository/idempotency.go`,
`internal/httpapi/trips.go`) хранит по каждому `Idempotency-Key` SHA-256
хэш тела запроса и `id` созданной поездки. Внутри одной транзакции строка в
`idempotency_keys` вставляется *раньше* строки в `trips`: `PRIMARY KEY` на
`key` — это и уникальность, и защита от гонки (конкурентный `INSERT` с тем
же новым ключом падает по `unique_violation`, а не создаёт вторую поездку).
Порядок вставок важен: если бы ключ фиксировался после создания поездки,
конкурентные запросы с одним и тем же новым ключом сначала натыкались бы на
`trips_one_active_per_driver_idx` (у них у всех один и тот же `driver_id`) и
получали бы `409 driver_busy` вместо честного повторного `200`. Поскольку
на момент вставки ключа строки в `trips` ещё не существует, внешний ключ
`idempotency_keys.trip_id → trips.id` объявлен `DEFERRABLE INITIALLY
DEFERRED` — проверка переносится на `COMMIT`, к которому обе строки уже на
месте. Перед каждым запросом с ключом сервис удаляет записи старше 24 часов;
повтор после этого срока обрабатывается как новый запрос и подчиняется
обычным правилам создания поездки, включая запрет второй активной поездки
водителю.
