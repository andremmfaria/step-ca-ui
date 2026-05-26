# Step-CA UI — приоритетный план развития

Обновлено: 2026-05-26  
Базовая версия: v1.4.7

Цель документа: перераспределить все идеи по срочности, сложности и фактической
ценности для проекта. Приоритет выше у задач, которые уменьшают риск потери CA,
повышают безопасность, упрощают эксплуатацию production и закрывают реальные
операционные проблемы.

---

## Шкала оценки

**Срочность**

- P0 — критично для безопасности/восстановления/production reliability.
- P1 — важно для нормальной эксплуатации.
- P2 — полезно для удобства и зрелости продукта.
- P3 — стратегическое развитие, можно делать позже.

**Сложность**

- S — маленькая задача, низкий риск.
- M — средняя задача, требует аккуратного тестирования.
- L — крупная задача, затрагивает несколько подсистем.
- XL — большая функциональная область, нужна отдельная серия релизов.

---

## 1. Краткий итог приоритетов

Самое важное делать в таком порядке:

1. Production safety: `SESSION_SECURE`, preflight check, system health.
2. Backup/restore: сначала безопасный экспорт, потом restore-процедуры.
3. CA integrity checks: provisioner password, claims, cert chain, volumes.
4. Security hardening: 2FA, audit admin-действий, initial password policy.
5. PKI usability: certificate details, validation, templates, bundles.
6. Notifications: expiration, failed renew, failed login bursts.
7. Let's Encrypt cleanup: Route53 и понятные ошибки.
8. SSH inventory and deployment automation.
9. Web console только после 2FA и audit.

---

## 2. План версий

### v1.4.8 — Production safety baseline

**Цель:** закрыть базовые production-риски, не затрагивая сложную бизнес-логику.

| Задача | Срочность | Сложность | Причина |
|--------|-----------|-----------|---------|
| `SESSION_SECURE=true/false` через env | P0 | S | Сейчас cookie работает с `Secure: false`; для HTTPS production это нужно исправить. |
| Страница System Health | P0 | M | Админ должен видеть состояние CA, DB, volumes, cert chain, version. |
| Preflight check | P0 | M | Быстрая диагностика перед update/release: CA health, DB, claims, password file, disk. |
| Проверка совпадения provisioner password | P0 | M | Ошибка password file ломает выпуск сертификатов. |
| Предупреждение о `smallstep/step-ca:latest` | P1 | S | `latest` снижает воспроизводимость; сначала показываем warning. |

**Definition of Done**

- `/admin/about` или отдельная `/admin/health` показывает понятные статусы.
- Есть env `SESSION_SECURE`.
- Preflight можно запускать из UI или CLI/endpoint для admin.
- Все проверки не раскрывают секреты.

---

### v1.4.9 — Backup and restore safety

**Цель:** перед дальнейшими крупными изменениями дать безопасный путь восстановления.

| Задача | Срочность | Сложность | Причина |
|--------|-----------|-----------|---------|
| Backup PostgreSQL из UI | P0 | M | БД хранит пользователей, cert metadata, logs. |
| Backup `step-ca-data` из UI/CLI | P0 | M | Это ключи CA; потеря критична. |
| Backup manifest | P0 | S | Нужен список версий, volumes, timestamp, checksums. |
| Restore-документация | P0 | S | Restore опасен, сначала документируем ручной путь. |
| Download backup bundle | P1 | M | Удобство эксплуатации. |

**Важно**

Restore из UI не делать сразу. Это рискованная операция. В v1.4.9 достаточно
надежного backup и проверенной restore-инструкции.

---

### v1.5.0 — CA integrity and update guardrails

**Цель:** защитить пользователя от неправильной конфигурации и опасного update.

| Задача | Срочность | Сложность | Причина |
|--------|-----------|-----------|---------|
| CA integrity page | P0 | M | Проверка root/intermediate/provisioner/claims. |
| Проверка full chain | P1 | S | Chain должен быть корректным для nginx/apache. |
| Проверка duration claims | P1 | S | Частая причина ошибок выпуска на 1-10 лет. |
| Update checklist в UI/docs | P1 | S | Уменьшает шанс `down -v` и потери данных. |
| Pin `smallstep/step-ca` версии | P1 | M | Убирает непредсказуемость `latest`. |

**Решение по `step-ca`**

Лучше перейти с `smallstep/step-ca:latest` на конкретный тег после отдельного
теста чистой установки и production upgrade.

---

### v1.5.1 — Certificate details and validation

**Цель:** сделать UI полезным для диагностики сертификатов, а не только выпуска.

| Задача | Срочность | Сложность | Причина |
|--------|-----------|-----------|---------|
| Детальная страница сертификата | P1 | M | SAN, issuer, serial, fingerprint, key usage, expiry. |
| Проверка cert/key pair | P1 | M | При импорте важно понимать, что ключ подходит к cert. |
| Проверка hostname/IP match | P1 | M | Частая ошибка при выпуске internal certs. |
| Валидация chain | P1 | M | Полезно перед установкой на серверы. |
| Download bundle presets | P2 | S | Cert/key/fullchain/root в удобных форматах. |

---

### v1.5.2 — Certificate templates and policies

**Цель:** уменьшить ошибки при выпуске сертификатов.

| Задача | Срочность | Сложность | Причина |
|--------|-----------|-----------|---------|
| Шаблоны сертификатов | P1 | M | server/client/wildcard/internal service. |
| Policy presets | P1 | M | срок, key type, SAN rules. |
| Default key type policy | P2 | S | Сейчас defaults есть, но лучше сделать явными. |
| Copy-paste deploy snippets | P2 | S | nginx/apache/traefik команды для пользователя. |

---

### v1.5.3 — Notifications

**Цель:** система должна сама сообщать о проблемах.

| Задача | Срочность | Сложность | Причина |
|--------|-----------|-----------|---------|
| Expiration notifications | P1 | M | Главный практический сценарий уведомлений. |
| Failed issue/renew notifications | P1 | M | Ошибки выпуска важно видеть сразу. |
| Failed login burst notifications | P1 | M | Security visibility. |
| Webhook provider | P1 | M | Универсальный вариант для homelab. |
| Email provider | P2 | M | Требует SMTP config. |
| Telegram provider | P2 | M | Удобно, но не всем нужно. |

**Порядок реализации**

Сначала webhook, потом email, потом Telegram. Webhook проще и универсальнее.

---

### v1.6.0 — 2FA / TOTP

**Цель:** защитить admin/manager аккаунты.

| Задача | Срочность | Сложность | Причина |
|--------|-----------|-----------|---------|
| TOTP для admin | P0 | L | CA UI требует сильной защиты. |
| TOTP для manager | P1 | M | Manager может выпускать сертификаты. |
| QR-code enrollment | P1 | M | Нормальный UX включения 2FA. |
| Recovery codes | P1 | M | Без них легко потерять доступ. |
| Audit log 2FA событий | P1 | S | Нужно для безопасности. |
| Force 2FA policy | P2 | M | Включить после стабильного TOTP. |

---

### v1.6.1 — Admin audit hardening

**Цель:** сделать действия admin полностью прослеживаемыми.

| Задача | Срочность | Сложность | Причина |
|--------|-----------|-----------|---------|
| Audit всех admin POST actions | P1 | M | Пользователи, настройки, сертификаты. |
| Audit скачивания private key | P1 | S | Скачивание ключа должно быть видно. |
| Audit backup downloads | P1 | S | Backup содержит критичные данные. |
| Audit config changes | P1 | M | Изменение env/settings должно быть видно. |

---

### v1.6.2 — Password recovery

**Цель:** восстановление доступа без ручного SQL.

| Задача | Срочность | Сложность | Причина |
|--------|-----------|-----------|---------|
| SMTP config | P2 | M | Нужно для email reset. |
| Password reset tokens | P2 | M | Безопасный reset flow. |
| Reset token TTL | P2 | S | Ограничение риска. |
| Rate limit reset requests | P2 | S | Защита от abuse. |
| Audit reset flow | P2 | S | Нужна трассировка. |

---

### v1.7.0 — Let's Encrypt cleanup

**Цель:** убрать расхождение между UI и фактической реализацией.

| Задача | Срочность | Сложность | Причина |
|--------|-----------|-----------|---------|
| Проверить `http01` flow | P1 | M | Базовый LE-сценарий. |
| Проверить Cloudflare flow | P1 | M | Уже заявлен и частично реализован. |
| Route53: реализовать или скрыть | P1 | M/L | Сейчас заявлен не полностью. |
| Улучшить ошибки LEGO | P1 | M | Пользователь должен понимать причину. |
| Renew/delete/autorenew test matrix | P1 | M | LE ломается незаметно без тестов. |

**Решение**

Если Route53 нужен реально — реализовать. Если нет — скрыть до отдельного
релиза, чтобы UI не обещал неготовую функцию.

---

### v1.8.0 — Web console, ограниченная

**Цель:** дать admin диагностические команды без полноценного root shell.

| Задача | Срочность | Сложность | Причина |
|--------|-----------|-----------|---------|
| Read-only diagnostic commands | P2 | M | Логи, ps, disk, CA status. |
| Allowlist команд | P1 | M | Безопасность. |
| 2FA requirement | P1 | M | Console только после 2FA. |
| Audit всех команд | P1 | S | Обязательно. |
| Запрет произвольного shell input | P0 | S | Нельзя давать unrestricted shell. |

**Важно**

Это не должна быть web shell. Только заранее заданные команды.

---

### v2.0.0 — SSH server inventory

**Цель:** начать управлять сертификатами на внешних серверах.

| Задача | Срочность | Сложность | Причина |
|--------|-----------|-----------|---------|
| Модель серверов | P2 | M | База для SSH automation. |
| Add/edit/delete servers | P2 | M | Управление inventory. |
| SSH connection test | P2 | M | Проверка доступности. |
| Хранение metadata | P2 | M | OS, hostname, services. |
| Role access для inventory | P2 | M | Не всем пользователям нужен доступ. |

---

### v2.1.0 — Certificate discovery over SSH

**Цель:** read-only инвентаризация сертификатов на серверах.

| Задача | Срочность | Сложность | Причина |
|--------|-----------|-----------|---------|
| Поиск cert/key paths | P2 | L | Разные ОС и пути. |
| nginx discovery | P2 | M | Частый сценарий. |
| apache discovery | P2 | M | Частый сценарий. |
| postfix discovery | P3 | M | Полезно позже. |
| Отображение найденных certs | P2 | M | UI для анализа. |

**Правило**

В этой версии только read-only. Никакой замены файлов.

---

### v2.2.0 — Manual certificate deployment

**Цель:** ручная безопасная замена сертификатов на серверах.

| Задача | Срочность | Сложность | Причина |
|--------|-----------|-----------|---------|
| Deploy cert/key в один клик | P2 | L | Основная ценность SSH automation. |
| Backup старых файлов | P1 | M | Обязательно перед заменой. |
| Dry-run | P1 | M | Снижает риск. |
| Проверка прав и путей | P1 | M | Без этого deploy будет ломаться. |
| Rollback files | P1 | L | Нужно для production safety. |

---

### v2.3.0 — Service reload integration

**Цель:** безопасно применять новые сертификаты.

| Задача | Срочность | Сложность | Причина |
|--------|-----------|-----------|---------|
| nginx config test + reload | P2 | M | Самый частый сервис. |
| apache config test + reload | P2 | M | Второй частый сервис. |
| postfix reload | P3 | M | Позже. |
| Rollback при reload error | P1 | L | Production safety. |
| Audit deploy/reload | P1 | S | Обязательно. |

---

### v2.4.0 — Automatic deployment and renewal

**Цель:** полный цикл автоматизации сертификатов.

| Задача | Срочность | Сложность | Причина |
|--------|-----------|-----------|---------|
| Auto-renew local certs | P2 | L | Автоматизация lifecycle. |
| Auto-deploy на servers | P2 | XL | Самая сложная часть. |
| Per-cert policy manual/auto | P2 | M | Нужен контроль риска. |
| Notifications success/failure | P1 | M | Без уведомлений auto-flow опасен. |
| Full audit trail | P1 | M | Обязательно. |

---

## 3. Backlog без версии

Эти задачи полезны, но пока не требуют фиксированной версии:

| Задача | Приоритет | Комментарий |
|--------|-----------|-------------|
| Multi-language UI toggle | P3 | README уже bilingual, UI пока в основном RU. |
| Import/export settings | P3 | Полезно после появления большего числа настроек. |
| API tokens | P3 | Нужно только если появится external automation. |
| Prometheus metrics | P3 | Полезно для homelab monitoring, но не срочно. |
| Dark/light theme polish | P3 | Косметика после функциональных задач. |
| Public demo screenshots | P3 | Для GitHub/README, не влияет на production. |

---

## 4. Задачи, которые не стоит делать рано

### Unrestricted web shell

Не делать. Если нужна web console, только allowlist команд, 2FA и audit.

### Restore из UI до стабильного backup

Не делать сразу. Restore может уничтожить данные. Сначала backup, manifest,
документация и ручной restore.

### Auto-deploy до read-only discovery

Не делать. Сначала нужно научиться безопасно находить сертификаты на серверах
без изменения файлов.

### Force migration существующих CA

Не делать без backup и preflight. CA keys и provisioner config — критичные
данные.

---

## 5. Рекомендуемый ближайший порядок

Ближайшие 5 релизов:

1. v1.4.8 — `SESSION_SECURE`, System Health, Preflight.
2. v1.4.9 — Backup export и restore documentation.
3. v1.5.0 — CA integrity checks и pin `step-ca`.
4. v1.5.1 — Certificate details/validation.
5. v1.5.2 — Templates/policies.

Такой порядок снижает риск для production и постепенно превращает проект в
надежный certificate management tool, а не просто UI над `step` CLI.
