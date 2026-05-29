<div align="center">

# Step-CA UI

**Self-hosted веб-интерфейс для [Smallstep step-ca](https://smallstep.com/docs/step-ca/) — управляйте собственным PKI прямо из браузера.**

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)
[![Made with Go](https://img.shields.io/badge/Made%20with-Go%201.26-00ADD8.svg)](https://go.dev)
[![Docker](https://img.shields.io/badge/Docker-Compose-2496ED.svg)](https://docs.docker.com/compose/)
[![Current version](https://img.shields.io/badge/version-v1.6.0-success.svg)](https://github.com/UncleFi1/step-ca-ui/releases/tag/v1.6.0)
[![Latest release](https://img.shields.io/badge/release-v1.6.0-success.svg)](https://github.com/UncleFi1/step-ca-ui/releases/latest)

[English](README.md) · **Русский**

</div>

---

> Удобный веб-интерфейс над `smallstep/step-ca` для небольших команд. Никаких облаков, телеметрии и vendor lock-in — всё работает на вашем сервере в трёх Docker-контейнерах.

## Текущий релиз

**Последняя стабильная версия:** [v1.6.0](https://github.com/UncleFi1/step-ca-ui/releases/tag/v1.6.0)

Главное:
- TOTP 2FA с подключением authenticator app, QR-кодом и recovery-кодами
- запрос 2FA-кода при входе после проверки пароля
- обновлённый визуальный стиль главной страницы, страницы 2FA и списка сертификатов
- адаптивный список сертификатов для широких экранов, ноутбуков и узких окон браузера

## Возможности

- 📋 **Управление сертификатами** — выпуск, перевыпуск, отзыв и импорт X.509
- 👥 **Ролевая модель** — `admin` / `manager` / `viewer`
- ⏱️ **Временные пользователи** — гостевые аккаунты с автоматическим истечением *(новинка v1.4.0)*
- 📅 **Кастомный date picker** — в стиле сайта, без браузерного виджета *(новинка v1.4.0)*
- 🌍 **Учёт часового пояса** — настраивается через переменную `TZ`
- 🎨 **4 темы** — тёмная, светлая, синяя, авто (по системе)
- 🧭 **Админ-пространство** — обновлённый интерфейс админки с корректными тёмной, светлой и синей темами *(новинка v1.4.11)*
- 🛡️ **Встроенная безопасность** — CSRF-токены, rate limiting, блокировка IP, журнал
- 🌐 **Provisioner'ы step-ca** — список и редактирование
- 💾 **Экспорт бэкапа** — backup bundle из UI и CLI с manifest checksums *(новинка v1.4.9)*
- 🔎 **CA integrity checks** — проверка root/intermediate chain, provisioner claims, password sync и закреплённого step-ca image *(новинка v1.5.0)*
- 🔬 **Детали сертификата** — SAN, fingerprints, key usage, cert/key pair и chain validation *(новинка v1.5.1)*
- 🧩 **Шаблоны сертификатов** — presets для server, internal service, wildcard и client identity *(новинка v1.5.2)*
- 🔔 **Webhook-уведомления** — тестовая отправка, ошибки выпуска/перевыпуска, серия неудачных входов и контроль истечения *(новинка v1.5.3)*
- 🔐 **TOTP 2FA** — подключение authenticator app, QR-код, проверка при входе и recovery-коды *(новинка v1.6.0)*
- **OIDC SSO** — маппинг группа→роль через любой OIDC-провайдер (JumpCloud как референсный); отключён по умолчанию

## Быстрый старт

```bash
git clone https://github.com/UncleFi1/step-ca-ui.git
cd step-ca-ui
sudo ./install.sh
```

Установщик поддерживает русский/английский язык, чистую установку и безопасное
обновление:

```bash
sudo ./install.sh --mode install --lang ru
sudo ./install.sh --mode update --lang ru
```

В режиме обновления сначала создаётся бэкап, сохраняются `.env` и Docker volumes,
затем выполняется `docker compose up -d --build`. Команда
`docker compose down -v` не используется.

И всё. Скрипт сам:
1. Определит ОС и установит Docker, если его нет
2. Автоопределит IP сервера (с подтверждением)
3. Сгенерирует надёжные пароли
4. Создаст `.env` и `credentials.txt` (chmod 600)
5. Соберёт и запустит контейнеры
6. Покажет URL и пароль администратора

На свежей виртуалке всё занимает 2–4 минуты.

## Системные требования

|                | Минимум | Рекомендуется | Высокая нагрузка |
|----------------|---------|---------------|------------------|
| **CPU**        | 1 vCPU  | 2 vCPU        | 4+ vCPU          |
| **RAM**        | 1 ГБ    | 2 ГБ          | 4+ ГБ            |
| **Диск**       | 5 ГБ    | 20 ГБ SSD     | 50+ ГБ NVMe      |
| **Сеть**       | 10 Мбит/с | 100 Мбит/с  | 1 Гбит/с         |
| **Пользователи** | до 50  | до 500        | 500+             |
| **Сертификаты** | до 500  | до 10 000     | 10 000+          |

**Программное обеспечение:**
- Linux kernel 4.4+ (Ubuntu 20.04+, Debian 11+, CentOS Stream 9+, Rocky 9+, Alma 9+)
- Docker Engine 20.10+ с плагином Compose v2+
- Открытые порты: `443/tcp` (HTTPS UI), опционально `9000/tcp` (API step-ca)

> Не тестировано, но должно работать: macOS / Windows через Docker Desktop (только для разработки). \
> **Не поддерживается:** shared hosting без Docker, Raspberry Pi Zero (мало RAM).

## Стек

| Слой         | Технология                  |
|--------------|-----------------------------|
| Backend      | Go 1.26, [chi](https://github.com/go-chi/chi) router |
| Frontend     | Server-side HTML + чистый JS, без сборки |
| База данных  | PostgreSQL 16 |
| CA           | [smallstep/step-ca](https://hub.docker.com/r/smallstep/step-ca) |
| Деплой       | Docker Compose |
| OS контейнера| Alpine 3.23 + tzdata        |

## Архитектура

```
                          ┌────────────┐
   Браузер  ─── HTTPS ───►│  step-ui   │  Go-приложение, порт 8443
                          │  (chi)     │
                          └──┬─────┬───┘
                             │     │
                  SQL ◄──────┘     └──────► HTTPS API
                             │     │
                          ┌──▼──┐ ┌▼──────────┐
                          │ pg  │ │ step-ca   │  порт 9000
                          │ 16  │ │ (PKI)     │
                          └─────┘ └───────────┘

   step-ui наружу :443  →  внутри редирект на :8443
   step-ca наружу :9000 →  по умолчанию закрыт от внешней сети
```

## Роли

| Роль    | Просмотр | Выпуск/Импорт | Отзыв | Управление пользователями |
|---------|----------|---------------|-------|---------------------------|
| viewer  | да       | нет           | нет   | нет                       |
| manager | да       | да            | нет   | нет                       |
| admin   | да       | да            | да    | да                        |

**Временные пользователи** могут иметь любую роль; они автоматически блокируются по истечении `expires_at` (отдельная горутина проверяет раз в минуту).

## Аутентификация / SSO

Step-CA UI поддерживает локальный вход по паролю и OIDC SSO. Оба метода могут работать одновременно; установщик оставляет OIDC выключенным, чтобы существующие развёртывания не пострадали.

### Локальный вход

Вход по логину/паролю включён по умолчанию (`LOCAL_LOGIN_ENABLED=true`). Оставляйте его включённым, пока настраиваете OIDC — отключение до проверки SSO приведёт к потере доступа. Локальный вход служит резервным путём, если IdP недоступен.

TOTP/2FA применяется только к локальным аккаунтам. Пользователи SSO полагаются на MFA своего IdP.

### OIDC SSO

Установите `OIDC_ENABLED=true`, чтобы активировать SSO. Реализация использует authorization code flow с PKCE и проверяет state, nonce и подпись ID token по JWKS эмитента. JumpCloud является референсным IdP, но подойдёт любой стандартный OIDC-провайдер.

**Маршруты, регистрируемые при `OIDC_ENABLED=true`:**

| Маршрут | Назначение |
|---------|------------|
| `GET /auth/oidc/login` | Инициирует authorization code + PKCE flow |
| `GET /auth/oidc/callback` | Принимает редирект от провайдера и завершает вход |

**Маппинг группа→роль**

Claim с именем, заданным в `OIDC_GROUP_CLAIM` (по умолчанию `groups`), содержит группы пользователя. Приложение сопоставляет группы с ролями по принципу наивысшего приоритета:

```
admin > manager > viewer
```

Если ни одно из трёх имён групп не совпадает и `OIDC_DEFAULT_ROLE` пуст — доступ запрещён. Устанавливайте `OIDC_DEFAULT_ROLE` в `viewer` только если каждый аутентифицированный пользователь вашего IdP должен иметь доступ на чтение.

При `OIDC_SYNC_ROLE=true` (по умолчанию) роль обновляется при каждом входе, и членство в группах IdP остаётся авторитативным. Установите `OIDC_SYNC_ROLE=false`, чтобы сохранять роли, назначенные вручную внутри приложения.

**Переменные окружения**

| Переменная | По умолчанию | Описание |
|------------|--------------|----------|
| `OIDC_ENABLED` | `false` | Установите `true` для включения SSO |
| `OIDC_ISSUER_URL` | — | Issuer URL провайдера (напр. `https://oauth.id.jumpcloud.com/`) |
| `OIDC_CLIENT_ID` | — | OAuth2 client ID из IdP |
| `OIDC_CLIENT_SECRET` | — | OAuth2 client secret из IdP |
| `OIDC_REDIRECT_URL` | — | Должен быть `https://<your-host>/auth/oidc/callback` |
| `OIDC_GROUP_CLAIM` | `groups` | Claim в ID token, содержащий группы |
| `OIDC_GROUP_ADMIN` | — | Имя группы IdP, сопоставляемой с ролью `admin` |
| `OIDC_GROUP_MANAGER` | — | Имя группы IdP, сопоставляемой с ролью `manager` |
| `OIDC_GROUP_VIEWER` | — | Имя группы IdP, сопоставляемой с ролью `viewer` |
| `OIDC_DEFAULT_ROLE` | _(пусто — запрет)_ | Роль при отсутствии совпадений |
| `OIDC_SYNC_ROLE` | `true` | Синхронизировать роль из групп IdP при каждом входе |
| `LOCAL_LOGIN_ENABLED` | `true` | Показывать форму логин/пароль (резервный вход) |

**Чек-лист настройки JumpCloud:**

1. SSO > + Add New Application > Custom OIDC App
2. Задайте Redirect URI: `https://<your-host>/auth/oidc/callback`
3. Скопируйте Client ID и Client Secret в `.env`
4. Добавьте атрибут группы с Attribute Name `groups`
5. Заполните `OIDC_GROUP_ADMIN` / `OIDC_GROUP_MANAGER` / `OIDC_GROUP_VIEWER` именами групп JumpCloud

## Безопасность

- **CSRF-защита** — токены на каждой форме и серверная проверка POST-маршрутов
- **Rate limiting** — 5 неудачных попыток входа → блокировка IP на 15 минут
- **Security headers** — CSP, X-Frame-Options, X-Content-Type-Options, Referrer-Policy, опциональный HSTS
- **Таймаут сессии** — 8 часов, скользящий
- **Журнал входов** — каждая попытка с IP и User-Agent
- **Self-signed TLS** — генерируется при первом запуске, валидность 10 лет
- **Хэширование паролей** — bcrypt для новых/изменённых паролей, прозрачная миграция legacy SHA-256 при следующем успешном входе

**`SECRET_KEY` обязателен.** Приложение откажется запускаться, если `SECRET_KEY` содержит значение-заглушку по умолчанию или короче 32 символов. Этот ключ подписывает cookie сессий и CSRF-токены; его утечка позволяет подделать сессию любого пользователя.

```bash
# Сгенерировать подходящее значение:
openssl rand -base64 48 | tr -dc 'A-Za-z0-9' | head -c 48
```

**`SESSION_SECURE`** (по умолчанию `true`) устанавливает флаг `Secure` на cookie сессии. Задайте `false` только для локальной HTTP-разработки; при старте приложение выводит предупреждение, если этот параметр равен false.

**`TRUST_PROXY`** управляет тем, как приложение определяет IP клиента:

| Значение | Поведение |
|----------|-----------|
| `false` (по умолчанию) | Использует реальный TCP-сокет. Rate limiter и журнал входов не поддаются подделке через заголовки `X-Forwarded-For`. Используйте, когда приложение выходит напрямую в интернет или стоит за прокси, которому вы не доверяете. |
| `true` | Перезаписывает `RemoteAddr` из `X-Forwarded-For` / `X-Real-IP` (через middleware `RealIP` от chi). Используйте только когда доверенный reverse proxy (nginx, Traefik, Caddy) очищает или перезаписывает эти заголовки перед проксированием. |

> **Совет для production:** поставьте step-ui за reverse proxy с настоящим TLS-сертификатом, ограничьте доступ через VPN/Tailscale, регулярно бэкапьте том `step-ca-data`.

## Конфигурация

Все настройки в `.env`. Установщик создаёт его автоматически, но вы можете редактировать вручную. Полный аннотированный список — в `.env.example`.

**Основные переменные:**

```env
HOST_IP=192.168.1.100              # SAN в self-signed серте; DNS-имя для step-ca
UI_HTTPS_PORT=443                  # внешний HTTPS-порт
PROVISIONER=admin                  # идентификатор provisioner'а step-ca
CA_PASSWORD=<сгенерировано>        # пароль provisioner'а step-ca
STEP_CA_IMAGE=smallstep/step-ca:0.30.2 # закреплённый step-ca image
SECRET_KEY=<сгенерировано>         # ключ подписи сессий и CSRF — ОБЯЗАТЕЛЕН, минимум 32 символа
SESSION_SECURE=true                # secure cookie сессии для HTTPS (предупреждение при false)
ENABLE_HSTS=false                  # включайте только с доверенным TLS-сертификатом
POSTGRES_PASSWORD=<сгенерировано>  # пароль базы
TZ=UTC                             # часовой пояс контейнеров
STEPCA_DEFAULT_TLS_CERT_DURATION=8760h
STEPCA_MAX_TLS_CERT_DURATION=87600h
```

**Переменные для reverse proxy и OIDC** (отключены по умолчанию — задайте только нужное):

```env
TRUST_PROXY=false                  # true только за доверенным прокси, очищающим XFF
LOCAL_LOGIN_ENABLED=true           # false чтобы скрыть форму пароля (оставьте true до проверки OIDC)
OIDC_ENABLED=false
OIDC_ISSUER_URL=https://oauth.id.jumpcloud.com/
OIDC_CLIENT_ID=
OIDC_CLIENT_SECRET=
OIDC_REDIRECT_URL=https://<your-host>/auth/oidc/callback
OIDC_GROUP_CLAIM=groups
OIDC_GROUP_ADMIN=step-ca-admins
OIDC_GROUP_MANAGER=step-ca-managers
OIDC_GROUP_VIEWER=step-ca-viewers
OIDC_DEFAULT_ROLE=                 # пусто = запрет при отсутствии совпадений (рекомендуется)
OIDC_SYNC_ROLE=true
```

После изменения `.env` пересоздайте контейнеры:

```bash
sudo docker compose up -d --force-recreate
```

## FAQ

<details>
<summary><b>Как изменить порт HTTPS с 443?</b></summary>

Отредактируйте `docker-compose.yml`:
```yaml
services:
  step-ui:
    ports:
      - "8443:8443"   # было "443:8443"
```
И перезапустите: `sudo docker compose up -d --force-recreate step-ui`.
</details>

<details>
<summary><b>Как сделать бэкап и восстановление?</b></summary>

Через UI: `Админ-панель -> Бэкап -> Скачать backup bundle`.

CLI-экспорт тоже поддерживается:

```bash
sudo ./install.sh --mode backup --lang ru
```

Бэкап включает PostgreSQL, `step-ca-data`, данные/сертификаты/uploads Step-CA UI
и `manifest.json` с SHA-256 checksums. Restore намеренно ручной; инструкция в
[BACKUP_RESTORE.md](BACKUP_RESTORE.md).
</details>

<details>
<summary><b>Как сбросить пароль admin'а?</b></summary>

```bash
sudo docker compose exec postgres psql -U stepui -d stepui -c \
  "UPDATE users SET password_hash = encode(sha256('newpass'::bytea), 'hex') WHERE username='admin';"
```
Войдите как `admin` / `newpass` и сразу смените пароль через интерфейс.
Legacy SHA-256 значение принимается для восстановления и после входа перехэшируется в bcrypt.
</details>

<details>
<summary><b>Браузер ругается на self-signed сертификат. Как поставить свой?</b></summary>

Замените `step-ui-go/ssl/server.crt` и `server.key` на свой сертификат + ключ (например от Let's Encrypt или вашего внутреннего CA), затем перезапустите `step-ui`. Убедитесь, что серт покрывает ваш `HOST_IP` или hostname.
</details>

<details>
<summary><b>Можно поставить за Cloudflare / Caddy / nginx?</b></summary>

Да. Направьте reverse proxy на `step-ui:8443` (HTTPS upstream) либо переключите step-ui на чистый HTTP и обрабатывайте TLS на прокси. Не забудьте передавать `X-Forwarded-Proto: https`, иначе step-ui будет генерировать неверные URL.
</details>

<details>
<summary><b>Как обновиться на новую версию?</b></summary>

```bash
sudo ./install.sh --mode update --lang ru
```
Режим обновления сначала создаёт бэкап, сохраняет текущие Docker volumes, при необходимости переключается на выбранный тег и запускает миграции автоматически при старте. Перед обновлением всегда смотрите [release notes](https://github.com/UncleFi1/step-ca-ui/releases) — мажорные версии могут содержать breaking changes.
</details>

## Container image

Публикуемый образ: `ghcr.io/andremmfaria/step-ca-ui`.

```bash
docker pull ghcr.io/andremmfaria/step-ca-ui:main
```

Образ собран на Alpine 3.23 с bundled smallstep CLI v0.30.2. Открывает порт 8443; маппинг на нужный порт хоста задаётся при запуске.

Запуск без Compose возможен, но требует доступного PostgreSQL и работающего step-ca. Для большинства развёртываний используйте готовый `docker-compose.yml`:

```bash
git clone https://github.com/andremmfaria/step-ca-ui.git
cd step-ca-ui
cp .env.example .env   # заполните значения
sudo docker compose up -d
```

Локальная сборка без публикации:

```bash
docker build -f step-ui-go/Dockerfile step-ui-go
```

**Когда публикуется образ:**

| Триггер | Образ публикуется? | Теги |
|---------|--------------------|------|
| Push в `main` | Да | `main`, short SHA |
| Тег версии (`v*`) | Да | semver (напр. `1.7.0`, `1.7`) |
| Pull request | Нет (только сборка) | PR ref |

## CI/CD и безопасность цепочки поставок

На каждый push и pull request запускается пять GitHub Actions workflows.

### CI

Выполняет проверку форматирования `gofmt`, `go vet`, `go build`, `go test -race` и golangci-lint v2. Все проверки должны пройти до слияния.

### Meta Lint

Проверяет Dockerfile через hadolint, workflow-файлы через actionlint и YAML-файлы в `.github/` через yamllint.

### Security Scanning

Запускает четыре сканера и загружает результаты в формате SARIF на вкладку GitHub Security:

| Сканер | Что проверяет |
|--------|---------------|
| gosec | Go-код на антипаттерны безопасности |
| govulncheck | Известные CVE в зависимостях Go-модулей |
| gitleaks | Секреты, случайно попавшие в репозиторий |
| trivy (filesystem) | Уязвимости зависимостей и конфигурации в репозитории |
| trivy (image) | Уязвимости в собранном образе контейнера |

Все сканеры работают в неблокирующем режиме, пока формируется исходный baseline. Коды завершения будут ужесточены после триажа baseline.

### CodeQL

Собственное сканирование Go-кода от GitHub; запускается при push в `main`, на pull request и еженедельно.

### Dependabot

Автоматические PR с обновлением зависимостей формируются и открываются еженедельно (понедельник, 06:00 UTC) для Go-модулей, базовых Docker-образов и GitHub Actions. Все сторонние actions в workflows закреплены по commit SHA.

## Участие в разработке

Pull request'ы приветствуются. Для крупных изменений сначала откройте issue, чтобы обсудить.

```bash
git clone https://github.com/UncleFi1/step-ca-ui.git
cd step-ca-ui/step-ui-go
go mod download
go run .  # нужны запущенные postgres + step-ca
```

При сабмите:
- Запустите `gofmt -w .` и `go vet ./...`
- Обновите соответствующие тесты
- Делайте коммиты сфокусированными и с понятными сообщениями

## Структура проекта

```
.
├── docker-compose.yml         # 3 сервиса: postgres, step-ca, step-ui
├── .env.example               # шаблон конфигурации
├── install.sh                 # установщик в одну команду
├── LICENSE                    # GPL-3.0
├── README.md                  # английская версия
├── README.ru.md               # этот файл (русская)
└── step-ui-go/
    ├── main.go                # точка входа, настройка роутера
    ├── config/                # загрузка конфига из env
    ├── db/                    # все SQL-запросы
    ├── handlers/              # HTTP-хендлеры (по файлу на раздел)
    ├── middleware/            # auth, security headers, CSRF
    ├── models/                # структуры данных
    ├── security/              # хэширование паролей, rate limiting, CSRF
    ├── templates/             # HTML-шаблоны (Go html/template)
    ├── static/                # CSS, JS, favicon, картинки
    ├── Dockerfile             # multi-stage Alpine build
    └── entrypoint.sh          # ждёт зависимости, генерит SSL, запускает app
```

## Лицензия

Проект распространяется по лицензии **GNU General Public License v3.0** — см. файл [LICENSE](LICENSE).

Кратко: вы можете использовать, изменять и распространять это ПО, но любые производные работы тоже должны быть выпущены под GPLv3.
