# TripGo — Trip Service

Сервис поездок курса «Разработка микросервисов на Go». Лабораторная работа 1:
HTTP API и PostgreSQL. Лабораторная работа 2: слоистая архитектура, тесты,
линтер и observability (трейсы, метрики, структурные логи).

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
| `LOG_LEVEL` | уровень логов (`debug`/`info`/`warn`/`error`) |
| `SHUTDOWN_TIMEOUT` | сколько ждать текущие запросы при `SIGTERM` перед принудительным выходом |
| `DATABASE_URL` | строка подключения к PostgreSQL |
| `DATABASE_MAX_CONNS` / `DATABASE_MIN_CONNS` | границы пула соединений pgx |
| `DATABASE_MAX_CONN_LIFETIME` | максимальное время жизни соединения в пуле |
| `DATABASE_CONNECT_TIMEOUT` | таймаут установления соединения с БД |
| `DATABASE_QUERY_TIMEOUT` | таймаут на каждый запрос к БД |

Телеметрию настраивают стандартные переменные OpenTelemetry (их читает SDK,
сервис их не парсит): `OTEL_SERVICE_NAME`, `OTEL_EXPORTER_OTLP_ENDPOINT`,
`OTEL_EXPORTER_OTLP_PROTOCOL` (поддерживается только `http/protobuf`),
`OTEL_RESOURCE_ATTRIBUTES`, `OTEL_TRACES_SAMPLER`,
`OTEL_METRIC_EXPORT_INTERVAL` (мс). Они необязательны: коллектор — не критичная
зависимость, и если он недоступен, сервис продолжает работать, а ошибки экспорта
пишутся в лог с уровнем `WARN`. В Docker Compose коллектора нет, поэтому там
такие предупреждения ожидаемы.

`TRIP_SERVICE_HOST_PORT` — необязательный порт публикации сервиса только в
Docker Compose (по умолчанию 8080); в окружение самого сервиса он не передаётся.

Для `deploy/docker-compose.yml` дополнительно нужны `POSTGRES_USER`,
`POSTGRES_PASSWORD`, `POSTGRES_DB` — см. `deploy/.env.example`.

## Архитектура

Код лежит в `internal/` и разложен по слоям. Зависимости направлены только
внутрь: внешний слой знает о внутреннем, но не наоборот.

```text
cmd/trip-service            точка входа: конфиг, логгер, телеметрия, container, graceful shutdown
        │
internal/container          единственное место, где известны все конкретные типы
        │                   (сборка графа зависимостей)
        ▼
internal/http               транспорт: роутер, middleware, по пакету на ручку,
        │                   единое отображение ошибок в problem+json (domainerror)
        ▼
internal/usecase/trip/*     сценарии: create, get, finish — по пакету на действие
        │
        ▼
internal/domain/trip        сущность Trip, правила, доменные ошибки (ничего не импортирует снаружи)

internal/repository/*       реализации хранилищ поверх pgx + squirrel (реализуют интерфейсы usecase)
internal/infra/postgres     пул соединений, трассировка запросов, менеджер транзакций
internal/infra/telemetry    OpenTelemetry, метрики, JSON-логгер с trace_id/span_id
internal/model              структуры для хранилища, у которых нет места в домене
                            (история статусов, запись идемпотентности)
```

Кто от кого зависит:

- `domain` не импортирует ничего, кроме стандартной библиотеки и `uuid`;
- `usecase` знает `domain` и `model`; про HTTP, pgx и SQL не знает;
- `http` знает `usecase` (через свои интерфейсы), `domain` (для ошибок и DTO)
  и сгенерированные типы API; в БД не ходит;
- `repository` знает `domain`, `model` и `infra/postgres/transaction`;
- `container` знает всех и больше никем не используется, кроме `main`.

Правила для `domain` и `usecase` закреплены в `.golangci.yml` через `depguard`:
домен не может импортировать `net/http`, `chi`, `pgx`, `squirrel` и
`otel`; usecase — `net/http`, `chi`, `pgx` и `squirrel`. Нарушение ломает
`make lint`. Дополнительно можно проверить так:

```bash
go list -deps ./internal/domain/... | grep -E 'jackc/pgx|go-chi|net/http' && echo "НАРУШЕНИЕ"
```

**Где объявлены интерфейсы и почему там.** Интерфейс объявляет тот, кто его
использует, — в файле `contract.go` рядом со своим кодом, неэкспортируемым:
`usecase/trip/create/contract.go` описывает `tripRepository`,
`idempotencyRepository`, `txManager` ровно с теми методами, которые нужны
этому сценарию; `http/trip/create/contract.go` описывает `usecase`. Репозиторий
не знает, что его кто-то использует через интерфейс, поэтому `usecase` не
тянет за собой `repository`, а тесты подставляют `gomock`-моки, не поднимая БД.
Моки генерируются командой `go generate ./...` (`make generate`) в
`mocks_test.go` рядом с контрактом. Один сценарий — один маленький интерфейс:
`finish` не видит `Create`, а `get` не видит транзакций.

**Что изменится при втором транспорте (ЛР4).** Usecase и domain останутся
как есть. Появится пакет транспорта рядом с `internal/http` (например,
`internal/grpc` с пакетом на каждый метод); у него будет свой `contract.go` с
теми же usecase-интерфейсами и своё отображение доменных ошибок в коды
транспорта — аналог `http/domainerror`. В `container` добавится сборка второго
сервера. Менять придётся только то, что связано с транспортом: DTO, коды
ошибок, middleware.

**Единственное место отображения ошибок** — `internal/http/domainerror`:
`ValidationError` → `400 invalid_request`, `ErrTripNotFound` → `404`,
`ErrTripCompleted`, `ErrDriverBusy`, `ErrIdempotencyKeyReused` → `409`, всё
остальное → `500 internal_error` без подробностей (текст ошибки уходит в
лог, а не клиенту).

Отличия от схемы из задания:

- точка входа — `cmd/trip-service`, а не `cmd/service` (так назывался
  каталог в ЛР1);
- работа с идемпотентностью вынесена в отдельный пакет `repository/idempotency`,
  а `AddStatusHistory` живёт в `repository/trip`: история статусов меняется
  вместе с поездкой, отдельный пакет ради одного метода не нужен;
- в `http` добавлены `response` (запись JSON/problem+json), `trip/dto`
  (domain → API-тип), `health` и `server` (роутер и `http.Server`);
- поле `reason` в истории статусов не заполняется: оно не входит в контракт,
  а миграции в этой работе добавлять нельзя;
- `Timeout`-middleware стоит внутри роутера после четырёх обязательных
  (`Recover → Tracing → Logging → Metrics → Timeout`), порядок первых четырёх
  не нарушен;
- поддерживается только OTLP по `http/protobuf`.

## Observability

Порядок middleware: `Recover → Tracing → Logging → Metrics → роутер`.
`Recover` стоит снаружи: он отвечает `500` уже после того, как остальные
записали запрос. Три наблюдающих middleware не перехватывают панику сами
(иначе потерялся бы стектрейс), а помечают запрос как не завершившийся, поэтому
паника попадает в трейс, лог и метрики как `500`.

- **Трейсы.** Корневой спан сервера называется `METHOD /route/{template}`,
  принимает входящий `traceparent`; запросы к БД — дочерние спаны вида
  `INSERT trips` с `db.collection.name` и `db.operation.name`. `5xx` помечают
  спан ошибкой, `4xx` — нет. Фоновая выборка для gauge `tripgo.trips.active`
  идёт с несэмплируемым контекстом и не засоряет Jaeger.
- **Метрики.** `http.server.request.duration`, `http.server.active_requests`,
  `db.client.operation.duration` (корзины гистограмм в секундах),
  `tripgo.trips.created`, `tripgo.trips.completed` и `tripgo.trips.active`
  (читается из БД при сборе). Метка `http.route` — шаблон маршрута, а не
  конкретный путь, иначе на каждую поездку появлялся бы свой временной ряд.
- **Логи.** JSON через `slog`, в каждой записи есть `service`, а внутри запроса —
  `trace_id` и `span_id`; по ним запись из лога находится в Jaeger. Строка
  `request handled` — `INFO`, для `5xx` — `ERROR`. Тела запросов и
  персональные данные в лог не попадают.

## Тесты

```bash
make test               # go test -race, печатает итоговое покрытие и покрытие usecase + domain
make test-integration   # то же + интеграционные тесты на настоящем PostgreSQL (тег integration)
make lint               # golangci-lint, включая depguard
make mutate             # мутационное тестирование internal/domain и internal/usecase
```

Интеграционным тестам нужен `DATABASE_URL` (его кладёт в `.env` `tripgoctl`,
`make` экспортирует его сам). Unit-тесты не требуют БД: usecase проверяются с
`gomock`-моками, хендлеры — через `httptest`. Покрытие `usecase` и `domain` —
100% (требование — не ниже 50%).

### Мутационное тестирование

Прогон `gremlins` (`make mutate`) по `internal/domain` и `internal/usecase` и
ручные мутанты, которые `gremlins` не создаёт (убрать вызов, подменить поле,
перепутать статус). Покрытие показывает, что строка выполнялась, но не то, что
её проверили, поэтому важны именно выжившие.

| Прогон | Мутантов | Убито | Выжило | Не покрыто |
|---|---|---|---|---|
| `gremlins`, `internal/domain` (все операторы) | 28 | 28 | 0 | 0 |
| `gremlins`, `internal/usecase` (все операторы) | 20 | 18 | 0 | 2 |
| Ручные мутанты, **до** | 28 (жизнеспособных) | 22 | 6 | — |
| Ручные мутанты, **после** | 28 (жизнеспособных) | 27 | 1 | — |

Разбор:

- Два «не покрыто» от `gremlins` — артефакт: условия `}); err != nil` и
  `case err == nil` стоят в `create/usecase.go` сразу после замыкания, и
  `gremlins` не относит их строки к покрытым блокам. Проверено вручную:
  инверсия каждого условия краснит тесты.
- Шесть выживших ручных мутантов были одной проблемой: `requestHash` в
  `create` не проверялся по отдельным полям. Если убрать из хэша `user_id`,
  широту или долготу начала и конца или перепутать начало и конец, то тесты
  оставались зелёными, и сервис принял бы запрос с тем же ключом, но другим
  телом за повтор. Закрыто шестью строками в таблице
  `TestExecute_KnownKey` (другой пользователь, по одной координате и
  перестановка начала и конца).
- Один выживший мутант (в хэше поменять местами начало и конец при
  вычислении) — эквивалентный: хэш сравнивается только с хэшем, посчитанным
  той же функцией, поэтому порядок полей внутри него не влияет на поведение.
  Убить его нельзя.
- Мутант «читать поездку без `FOR UPDATE`» не компилируется: интерфейс
  `tripRepository` в `finish` не содержит обычного `GetByID`, так что ошибку
  отлавливает компилятор.

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
  интерфейс и роутинг (`make generate`); хендлеры лежат в `internal/http/`.
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
умолчанию), задаётся в `internal/infra/postgres/transaction/manager.go` при
открытии транзакции.
Правило "один активный рейс на водителя" обеспечено не
`SELECT` внутри транзакции (это не защитило бы от гонки — оба конкурентных
запроса прошли бы проверку раньше, чем любой из них вставил строку), а
частичным уникальным индексом `trips_one_active_per_driver_idx` на уровне
БД (`driver_id WHERE status = 'active'`). Поэтому более строгий уровень
изоляции не нужен.

**Менеджер транзакций** (`internal/infra/postgres/transaction`, `Manager.Do`)
открывает транзакцию, кладёт её в `context.Context` и вызывает переданную
функцию с обогащённым контекстом; `nil` на выходе — `COMMIT`, ошибка или
паника — `ROLLBACK` и проброс наружу. Usecase видит только интерфейс
`txManager` из своего `contract.go`. Репозитории не получают транзакцию
аргументом метода: они достают её из контекста через
`transaction.TxFromContext` и работают одинаково что с пулом, что с
транзакцией. Вложенный вызов `Do` внутри `Do` переиспользует уже открытую
транзакцию, а не открывает вторую.

**Запрет двух активных поездок у одного водителя** обеспечен частичным
уникальным индексом `trips_one_active_per_driver_idx` (`driver_id WHERE
status = 'active'`, см. `migrations/20260927181004_create_trips.sql`).
Конкурентная вставка второй активной поездки тому же водителю падает по
`unique_violation`; репозиторий (`internal/repository/trip`) распознаёт
эту ошибку по имени constraint'а и возвращает `ErrDriverBusy`, который
хендлер превращает в `409 driver_busy`.

**Конкурентное завершение:** usecase `finish` в одной транзакции читает
поездку через `SELECT ... FOR UPDATE`, вызывает доменный метод `Trip.Finish`,
сохраняет поездку и пишет строку истории. Блокировка строки выстраивает
конкурентные завершения в очередь: первый переводит поездку в `completed`,
остальные после снятия блокировки видят статус `completed` и получают
`409 trip_completed`. Правило перехода статусов лежит в домене, а не в SQL.

**Проверки состояния:** `/health` отвечает `200`, пока работает процесс, и
не обращается к БД. `/ready` проверяет PostgreSQL с таймаутом и отвечает
`503`, если сервис пока не может обслуживать запросы. Разделение позволяет
убрать временно неготовый экземпляр из трафика, не перезапуская живой процесс.

**Идемпотентность** (`internal/repository/idempotency`,
`internal/usecase/trip/create`) хранит по каждому `Idempotency-Key` SHA-256
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
