# Tixo

Платформа подій і квитків: концерти, лекції та мітапи на мапі міста, пошук і ШІ-помічник, електронні квитки
з QR-кодом. Інтерфейс українською та англійською.

Курсовий проєкт «Web-технології». Що з вимог курсу вже зроблено і як це продемонструвати —
у [`docs/requirements/`](docs/requirements/).

## Стек

Go 1.26 · chi · html/template + Bootstrap 5 · PostgreSQL 16 (pgx, sqlc, goose, pg_trgm) · Redis 8 ·
RabbitMQ 4 · Docker Compose · GitHub Actions.

## Швидкий старт

Потрібен лише Docker.

```sh
docker compose up --build
```

| Що | Адреса |
|---|---|
| Сайт | http://localhost:8080 (англійською — http://localhost:8080/en/) |
| Стан залежностей | http://localhost:8080/healthz |
| RabbitMQ UI | http://localhost:15672 (`tixo` / `tixo`) |

Порт 8080 зайнятий — `HTTP_PORT=8081 docker compose up --build`. Зупинити — `docker compose down`
(з `-v` — разом із даними).

Compose піднімає `postgres`, `redis`, `rabbitmq`, одноразовий `migrate` (застосовує міграції) і `serve`.
`serve` стартує тільки після того, як залежності здорові, а міграції пройшли.

## Розробка

```sh
make infra             # postgres/redis/rabbitmq з портами 55432/56379/55672 (compose.override.yml)
make run               # go run ./cmd/tixo serve проти цих сервісів
make test              # unit-тести (інтеграційні пропускаються)
make test-integration  # усі тести проти сервісів з compose
make lint              # golangci-lint
make gen               # sqlc generate після зміни internal/db/queries або міграцій
make check             # lint + перевірка згенерованого коду + усі тести, як у CI
```

Інструменти (golangci-lint, sqlc) закріплені в `go.mod` і запускаються через `go tool` — окремо ставити не треба.

Бінарник `tixo` має підкоманди:

| Команда | Що робить |
|---|---|
| `tixo serve` | вебсервер |
| `tixo migrate up\|down\|reset\|status` | міграції БД (`down` і `reset` заборонені при `APP_ENV=prod`) |
| `tixo probe` | перевірка `/healthz` для healthcheck контейнера |
| `tixo version` | версія збірки |

## Конфігурація

Усе — через змінні оточення, приклад у [`.env.example`](.env.example). Неправильний конфіг зупиняє процес
на старті з назвою змінної.

| Змінна | За замовчуванням | Призначення |
|---|---|---|
| `APP_ENV` | `dev` | `dev` / `test` / `prod` |
| `HTTP_ADDR` | `:8080` | адреса вебсервера |
| `BASE_URL` | `http://localhost:8080` | публічна адреса сайту (canonical, hreflang); у prod — лише https |
| `DATABASE_URL` | — | PostgreSQL |
| `DB_MAX_CONNS` | `10` | розмір пулу з'єднань (1–50) |
| `REDIS_URL` | — | Redis |
| `AMQP_URL` | — | RabbitMQ |
| `SESSION_SECRET` | генерується в dev | ключ сесій, у prod обов'язковий, ≥ 32 байти |

## Структура

```
cmd/tixo/              точка входу і підкоманди
internal/config/       конфіг з env і його валідація
internal/server/       роутер, middleware, graceful shutdown
internal/health/       /healthz
internal/web/          MVC: controllers, view (рендер шаблонів), i18n
internal/db/           міграції, SQL-запити, згенерований sqlc-код (gen/ — не редагувати)
internal/platform/     клієнти Postgres, Redis, RabbitMQ, логер
internal/repolint/     перевірка правил репо (у коді немає коментарів)
web/                   шаблони, статика, переклади (вбудовуються в бінарник)
deploy/                ініціалізація БД для compose
docs/requirements/     докази виконання вимог курсу
```

## Ліцензія

[MIT](LICENSE)
