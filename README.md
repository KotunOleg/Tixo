# Tixo

Платформа подій і квитків: концерти, лекції та мітапи на мапі міста, пошук і ШІ-помічник, електронні квитки
з QR-кодом. Інтерфейс українською та англійською.

Курсовий проєкт «Web-технології». Що з вимог курсу вже зроблено і як це продемонструвати —
у [`docs/requirements/`](docs/requirements/).

## Стек

Go 1.26 · chi · html/template + Bootstrap 5 · PostgreSQL 16 (pgx, sqlc, goose, pg_trgm) · Redis 8 ·
RabbitMQ 4 · Docker Compose · Caddy · AWS Lightsail · GitHub Actions.

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
| `tixo flags enable\|disable <прапорець> --org <slug>`, `tixo flags list --org <slug>` | увімкнути чи вимкнути функцію (напр. `sales_analytics`) для організатора без деплою й без зміни плану |
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
| `GOOGLE_CLIENT_ID` | — | OAuth client для входу через Google; без нього в dev вхід вимкнено, у prod обов'язковий |
| `GOOGLE_CLIENT_SECRET` | — | секрет того ж OAuth client, задається разом з `GOOGLE_CLIENT_ID` |
| `STORAGE_DIR` | `./data/uploads` | тека для завантажених файлів (постери подій); у compose — том `uploads` на `/data/uploads` |

## Вхід через Google

Вхід працює через OAuth 2.0 (Authorization Code + PKCE) з Google. Перший вхід реєструє користувача. Сесія живе в Redis
(cookie `tixo_session`: HttpOnly, SameSite=Lax, Secure на https), а вийти можна лише POST-формою.
Без `GOOGLE_CLIENT_ID` у dev кнопки входу немає, решта сайту працює.

Щоб увімкнути вхід локально:

1. [Google Cloud Console](https://console.cloud.google.com/) → створити проєкт (або взяти наявний).
2. **Google Auth Platform → Branding**: назва застосунку `Tixo`, пошта підтримки, пошта розробника.
3. **Audience**: тип **External**, статус **Testing**. У **Test users** додати Google-акаунти, якими входитимете
   (поки застосунок у Testing, інших Google не пустить).
4. **Data access**: достатньо базових `openid`, `.../auth/userinfo.email`, `.../auth/userinfo.profile`.
   Вони не чутливі, тож верифікація Google не потрібна.
5. **Clients → Create client**, тип **Web application**:
   - Authorized JavaScript origins: `http://localhost:8080`
   - Authorized redirect URIs: `http://localhost:8080/auth/google/callback`
6. Скопіювати Client ID і Client secret у `.env` (`GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`) і перезапустити
   `make run` або `docker compose up`.

Redirect URI будується як `BASE_URL` + `/auth/google/callback`. Якщо змінили порт чи домен, змініть `BASE_URL` і
додайте нову адресу в redirect URIs того ж client.

Після входу в меню акаунта з'являються «Мої організатори». На `/org/new` можна створити організатора (тенант), творець стає власником.
Кабінет `/org/{slug}` бачать лише члени організатора, для інших він відповідає 404.
Власник додає учасників за email (роль «редактор»). Людина має хоча б раз увійти в Tixo.

Демо-організатори з подіями для ваших Google-акаунтів:

```sh
make seed OWNERS="kyiv-live=you@gmail.com lviv-sound=friend@gmail.com"
```

Акаунт стає власником вказаного демо-організатора. Під час першого входу через Google з цим email
(email має бути підтверджений) обліковий запис прив'язується автоматично.

`kyiv-live` у демо — Premium: `make seed` створює для нього продажі за 30 днів, і в кабінеті доступна
«Аналітика» (`/org/kyiv-live/analytics`, `GET /api/v1/organizers/kyiv-live/analytics`). `lviv-sound` — Free і бачить
пояснення з 403. Доступ вмикає прапорець `sales_analytics`: за планом Premium або вручну через `tixo flags`.

## Деплой

Прод працює на AWS Lightsail у Docker Compose: [`deploy/compose.prod.yml`](deploy/compose.prod.yml) піднімає `postgres`, `redis`,
`rabbitmq`, `migrate`, `serve` і `caddy`. Caddy приймає 80/443, сам отримує й поновлює сертифікат Let's Encrypt
і проксує на `serve:8080`. Решта сервісів назовні не відкрита.

Кожен зелений `ci` на `main` запускає workflow [`deploy`](.github/workflows/deploy.yml):

1. збирає образ `ghcr.io/kotunoleg/tixo` з тегами `<sha>` і `latest`;
2. копіює `compose.prod.yml` і `Caddyfile` у `/opt/tixo` на сервері;
3. по ssh робить `docker compose pull` і `up -d` з `TIXO_IMAGE_TAG=<sha>`;
4. чекає до 2 хвилин, поки `https://<домен>/healthz` поверне `"status":"ok"` і `"version":"<sha>"`, інакше job червоний.

Деплої йдуть по черзі, історію видно в **Environments → production**. На сервері немає git-чекауту: там лише
файли compose, `Caddyfile` і `.env` із секретами, який CI не бачить і не перезаписує.

### Перше налаштування

**AWS Lightsail** (особистий акаунт, регіон Frankfurt):

1. Create instance: Linux/Unix → OS Only → Ubuntu 24.04 LTS, network type Dual-stack (раннери GitHub Actions
   ходять лише по IPv4), план $12 (2 GB RAM, 2 vCPU, 60 GB SSD).
2. Networking → Create static IP → прив'язати до інстансу (поки прив'язаний, безкоштовний).
3. Інстанс → Networking → IPv4 і IPv6 Firewall: SSH 22, HTTP 80, HTTPS 443 (TCP) і UDP 443 з будь-якої адреси.
4. Billing → Budgets: місячний бюджет $15 з алертом на пошту. Автоматичні snapshots не вмикати — вони платні.

**Домен:** на [duckdns.org](https://www.duckdns.org) створити піддомен і вписати в нього static IP.

**Сервер** (`ssh ubuntu@<static-ip>` своїм ключем):

```sh
sudo apt-get update && sudo apt-get -y upgrade
sudo apt-get install -y fail2ban
curl -fsSL https://get.docker.com | sudo sh

sudo fallocate -l 2G /swapfile && sudo chmod 600 /swapfile && sudo mkswap /swapfile && sudo swapon /swapfile
echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab

printf 'PasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitRootLogin no\n' \
  | sudo tee /etc/ssh/sshd_config.d/10-tixo.conf
sudo systemctl reload ssh

sudo adduser --disabled-password --gecos "" deploy
sudo usermod -aG docker deploy
sudo install -d -m 700 -o deploy -g deploy /home/deploy/.ssh
sudo install -d -m 750 -o deploy -g deploy /opt/tixo
```

Ключ для CI генерується локально, окремий від адмінського:

```sh
ssh-keygen -t ed25519 -N "" -C tixo-deploy -f tixo-deploy
```

Вміст `tixo-deploy.pub` покласти на сервері в `/home/deploy/.ssh/authorized_keys` (власник `deploy`, права `600`).
Перевірити: `ssh -i tixo-deploy deploy@<static-ip> docker ps`.

Файл `/opt/tixo/.env` (власник `deploy`, права `600`) — за зразком [`deploy/prod.env.example`](deploy/prod.env.example):
`DOMAIN` — піддомен DuckDNS, `POSTGRES_PASSWORD`, `RABBITMQ_PASSWORD` і `SESSION_SECRET` — кожен з
`openssl rand -hex 32`, `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` — з Google Console (нижче). Без них
`migrate` і `serve` у prod не стартують і називають змінну в лозі.

**Google OAuth:** у тому ж client (або окремому для проду) додати Authorized JavaScript origin `https://<домен>` і
redirect URI `https://<домен>/auth/google/callback`. Поки застосунок у статусі Testing, входять лише Test users.

**GitHub** → Settings → Secrets and variables → Actions:

| Тип | Назва | Значення |
|---|---|---|
| Secret | `DEPLOY_HOST` | static IP |
| Secret | `DEPLOY_USER` | `deploy` |
| Secret | `DEPLOY_SSH_KEY` | вміст приватного `tixo-deploy` |
| Secret | `DEPLOY_KNOWN_HOSTS` | вивід `ssh-keyscan -t ed25519 <static-ip>`; відбиток звірити з `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub` на сервері |
| Variable | `DEPLOY_DOMAIN` | піддомен DuckDNS, без `https://` |

**Перший запуск:** Actions → deploy → Run workflow (`ref` = `main`). Новий пакет у GHCR приватний, тому перший
`pull` на сервері впаде. Тоді: профіль GitHub → Packages → `tixo` → Package settings → Change visibility → Public,
і в run — **Re-run failed jobs**.

### Відкат і діагностика

- Відкат: Actions → deploy → Run workflow, `ref` = SHA попереднього робочого коміту.
- Що зараз на проді: `curl https://<домен>/healthz` — поле `version`.
- Логи: `ssh deploy@<static-ip>`, далі `cd /opt/tixo && docker compose -f compose.prod.yml logs -f serve`.
- Міграції лише вперед: `migrate down` і `reset` при `APP_ENV=prod` заборонені.

## Структура

```
cmd/tixo/              точка входу і підкоманди
internal/config/       конфіг з env і його валідація
internal/server/       роутер, middleware, graceful shutdown
internal/health/       /healthz
internal/web/          MVC: controllers, view (рендер шаблонів), i18n
internal/auth/         Google OAuth (state + PKCE), сесії в Redis, поточний користувач
internal/users/        користувачі: реєстрація та вхід через Google
internal/organizers/   організатори-тенанти, membership, кабінет
internal/db/           міграції, SQL-запити, згенерований sqlc-код (gen/ — не редагувати)
internal/platform/     клієнти Postgres, Redis, RabbitMQ, логер
internal/repolint/     перевірка правил репо (у коді немає коментарів)
web/                   шаблони, статика, переклади (вбудовуються в бінарник)
deploy/                prod compose і Caddyfile, ініціалізація тестової БД для dev compose
docs/requirements/     докази виконання вимог курсу
```

## Ліцензія

[MIT](LICENSE)
