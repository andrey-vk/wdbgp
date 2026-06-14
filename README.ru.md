# wdbgp

[![Tests](https://github.com/andrey-vk/wdbgp/actions/workflows/tests.yml/badge.svg)](https://github.com/andrey-vk/wdbgp/actions/workflows/tests.yml)
[![Publish Docker Image](https://github.com/andrey-vk/wdbgp/actions/workflows/deploy.yml/badge.svg)](https://github.com/andrey-vk/wdbgp/actions/workflows/deploy.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Docker Alpha Version](https://img.shields.io/docker/v/wh1ted/wdbgp/alpha?label=docker%20alpha)](https://hub.docker.com/r/wh1ted/wdbgp/tags)
[![Docker Pulls](https://img.shields.io/docker/pulls/wh1ted/wdbgp)](https://hub.docker.com/r/wh1ted/wdbgp)
![Go](https://img.shields.io/badge/Go-1.26-00ADD8)
![Alpine](https://img.shields.io/badge/Alpine-3.23-0d597f)
![GoBGP](https://img.shields.io/badge/GoBGP-3.x-green)
![RouterOS](https://img.shields.io/badge/RouterOS-container-blue)
![Dual Stack](https://img.shields.io/badge/IP-IPv4%20%2B%20IPv6-blueviolet)

[English version](README.md)

`wdbgp` скачивает категоризированные IPv4/IPv6 CIDR-фиды, формирует каталог
сервисов и анонсирует выбранные пользователями префиксы их роутерам через BGP.

Это один статически собранный Go-бинарник со встроенными HTTP-сервером,
SQLite-хранилищем и GoBGP. BIRD и Python больше не требуются. Маршруты хранятся
в памяти GoBGP: одинаковый префикс создаётся один раз, а отдельные export policy
определяют, каким клиентам он доступен.

## Режимы каталогов

Встроены режимы `OpenCCK` с широким покрытием сервисов на базе ASN и общей
инфраструктуры и `IPRanges` с диапазонами провайдеров, платформ, CDN, сетей и
privacy-сервисов из
[antonme/ipranges](https://github.com/antonme/ipranges).

Администратор может включать и выключать режимы и назначать каждому фиду режим.
У пользователя есть один активный режим и независимый сохранённый выбор
категорий и сервисов для каждого режима. В BGP попадают только маршруты
активного режима. Существующие базы автоматически переносятся в `OpenCCK` без
изменения текущего выбора.

По умолчанию пользователь не может менять режим, пока администратор явно не
разрешит это. Отключённые режимы сохраняют загруженные данные и выборы, но не
создают маршруты. Диагностика CIDR работает для одного выбранного режима и
показывает только пользователей, у которых этот режим активен.

Встроенный адаптер IPRanges загружает merged IPv4/IPv6-списки upstream и
раскладывает их по отдельным сервисам каталога. Upstream объединяет публичные
данные провайдеров, диапазоны по ASN и адреса, полученные через DNS, поэтому
охват списков различается по сервисам. Изначально режим отключён: перед
настройкой пользователей его нужно включить и синхронизировать фиды.

## Сетевая модель

Контейнер является самостоятельным BGP speaker со своим `veth`-адресом и не
изменяет RouterOS через API.

- Пользовательские CIDR определяют пользователя веб-интерфейса. Побеждает самая
  специфичная подходящая сеть.
- IP BGP peer и ASN определяют роутер, которому экспортируются маршруты.
- Next hop должен быть достижим с клиентского роутера по нужному VPN-маршруту.

Не публикуйте HTTP и TCP/179 в недоверенные сети.

## Веб-аутентификация

Режим веб-аутентификации пользователя управляет доступом к странице выбора:

- **network** — определение по IP, совпадающему с CIDR пользователя
- **login** — вход по логину/паролю
- **both** — совпадение IP И учётные данные
- **any** — совпадение IP ИЛИ учётные данные (любой из способов)

Учётные данные (логин + bcrypt-хеш пароля) управляются через админку для каждого
пользователя. Страница `/login` обслуживает аутентификацию по учётным данным.
`WDBGP_DEFAULT_WEB_AUTH` задаёт режим по умолчанию для новых пользователей
(по умолчанию: `network`).

## Фиды

При первом старте добавляются основные и beta-фиды OpenCCK для IPv4 и IPv6.
Первое обновление начинается сразу после запуска, последующие выполняются через
`WDBGP_SYNC_INTERVAL` секунд. При ошибке старый успешно загруженный снимок фида
сохраняется.

Канонический формат пользовательского фида:

```json
{
  "entries": [
    {
      "category": "ai",
      "service": "openai",
      "cidrs": ["104.18.0.0/16", "172.64.0.0/13"]
    }
  ]
}
```

Поддерживаются также один entry-объект и массив entry-объектов. Префиксы
нормализуются и дедуплицируются. Выбор категории автоматически включает новые
сервисы, которые позже появятся в ней.

Фиды можно добавлять, редактировать, включать, выключать и удалять через
админку. Выключение сохраняет последний загруженный снимок и пользовательский
выбор в базе, но исключает сервисы и префиксы фида из каталога и BGP-анонсов.
После включения снимок снова используется до следующей синхронизации. Изменение
URL очищает старый снимок, а удаление полностью удаляет фид.

Адаптеры фидов являются JavaScript-программами, хранятся в базе данных и
редактируются из админки. В дистрибутив входят адаптеры для канонического JSON,
OpenCCK и IPRanges. Для каждого фида выбирается один адаптер. Адаптер должен
определять функцию:

```javascript
function sync(feed, api) {
    const data = JSON.parse(api.httpGet(feed.url));
    return data.entries;
}
```

Функция возвращает объекты с полями `category`, `service` и `cidrs`. Среда
выполнения предоставляет только `api.httpGet()`. Ограничиваются разрешённые
хосты, количество запросов, размеры ответов, общий объём загрузки и время
выполнения. Хост URL фида разрешён автоматически; дополнительные хосты
указываются у адаптера в админке. Go-приложение проверяет и нормализует каждый
CIDR, после чего атомарно заменяет предыдущий снимок. Ошибка скрипта или
синхронизации не удаляет последний успешный снимок.

Существующий адаптер можно протестировать на одном из назначенных ему фидов из
админки. Для теста используется текст, находящийся в редакторе, включая
несохранённые изменения. Показываются первые 100 нормализованных CIDR, при этом
каталог, статус фида, ревизия адаптера и состояние BGP не изменяются.

У каждого адаптера есть отдельная страница редактора. Ошибки синтаксиса и
выполнения показываются на ней с позициями в JavaScript и stack trace, а
отправленный текст сохраняется в форме. Встроенные адаптеры можно сбросить к
версии из дистрибутива: восстанавливаются исходные имя, текст и разрешённые
хосты, после чего ревизия увеличивается.

При редактировании или удалении адаптера его предыдущий исходный код
сохраняется в `WDBGP_ADAPTER_BACKUP_DIR` (по умолчанию
`<db_dir>/backup/adapters`). Директория создаётся автоматически. Задайте
пустую строку в `WDBGP_ADAPTER_BACKUP_DIR`, чтобы отключить бэкапы.
`WDBGP_ADAPTER_BACKUP_MAX` (по умолчанию 10) задаёт максимальное количество
хранимых копий на адаптер; старые файлы удаляются первыми. 0 — без ограничений.

В админке также доступна диагностика CIDR. Для IP-адреса или подсети она
показывает полное и частичное покрытие сервисами, их совокупное покрытие и
покрытие выбранными категориями и сервисами каждого включённого пользователя
до и после применения его эффективных фильтров маршрутов.

## Фильтрация маршрутов

Администратор задаёт глобальные списки allow и deny. Пустой allow разрешает все
выбранные из фидов префиксы, а deny вычитается из результата. Вычитание точное:
если запретить `1.1.1.1/32` внутри выбранного `1.0.0.0/8`, сеть `/8` будет
раздроблена на CIDR, которые больше не покрывают `1.1.1.1`.

Каждый пользователь может наследовать глобальные списки, дополнять их
пользовательскими списками или использовать полный пользовательский override. В
режиме дополнения allow и deny объединяются с глобальными списками перед
фильтрацией. Администратор отдельно разрешает пользователю редактировать режим
и списки через пользовательский интерфейс. Default routes из фидов всегда
отбрасываются, а расширение ограничено для защиты от случайного взрыва
количества префиксов.

Миграция фильтров маршрутов инициализирует глобальный deny распространёнными
private, loopback, link-local, documentation, benchmark, multicast и reserved-сетями.

## Настройки

Все настройки приложения хранятся в базе данных и редактируются на странице
`/admin/settings`. Переменные окружения всегда переопределяют сохранённые значения —
настройки из ENV затемнены и показывают подсказку. Настройки не из ENV применяются
сразу где возможно; BGP и сетевые требуют перезапуска.

Глобальные списки фильтрации маршрутов также находятся на этой странице.

## Запуск

```sh
docker run --rm \
  -p 8080:8080 \
  -p 179:179 \
  -v wdbgp-data:/data \
  -e WDBGP_ADMIN_PASSWORD=change-me \
  -e WDBGP_SESSION_SECRET=a-long-random-secret \
  -e WDBGP_LOCAL_ASN=64512 \
  -e WDBGP_ROUTER_ID=172.31.255.2 \
  -e WDBGP_BGP_LOCAL_ADDRESS=172.31.255.2 \
  -e WDBGP_BGP_LOCAL_ADDRESS_V6=fd00:31:255::2 \
  wh1ted/wdbgp:alpha
```

Откройте `/admin`, добавьте пользователей и настройте выбор сервисов. Страница
`/` определяет пользователя по исходному IP. Если приложение находится за
доверенным reverse proxy, включите `WDBGP_TRUST_PROXY_HEADERS=true`.

Веб-интерфейс доступен на русском и английском языках. Язык выбирается по
`Accept-Language` браузера, а явный выбор `EN`/`RU` сохраняется в cookie.
Fallback-язык задаётся через `WDBGP_DEFAULT_LANGUAGE` и по умолчанию равен `en`.

Cookie админки по умолчанию используют `WDBGP_ADMIN_COOKIE_SECURE=auto`. Если
web-интерфейс админки доступен без HTTPS, обязательно установите
`WDBGP_ADMIN_COOKIE_SECURE=false`; иначе браузер может не сохранить session
cookie и после правильного пароля снова перекинет на страницу входа. Принудите
`true` только когда админка всегда открывается через HTTPS.

### Переменные окружения

| Переменная | Значение по умолчанию |
| --- | --- |
| `WDBGP_DB` | `/data/wdbgp.sqlite3` |
| `WDBGP_HOST` / `WDBGP_PORT` | `0.0.0.0` / `8080` |
| `WDBGP_BGP_PORT` | `179` |
| `WDBGP_LOCAL_ASN` | `64512` |
| `WDBGP_ROUTER_ID` | `192.0.2.1` |
| `WDBGP_BGP_LOCAL_ADDRESS` | `192.0.2.2` |
| `WDBGP_BGP_LOCAL_ADDRESS_V6` | пусто |
| `WDBGP_SYNC_INTERVAL` | `3600` секунд |
| `WDBGP_ADMIN_COOKIE_SECURE` | `auto` |
| `WDBGP_DEFAULT_LANGUAGE` | `en` |
| `WDBGP_SECURITY_HEADERS` | `false` |
| `WDBGP_RATE_LIMIT_LOGIN` | `5` |
| `WDBGP_RATE_LIMIT_ADMIN` | `30` |
| `WDBGP_SESSION_MAX_AGE` | `28800` |
| `WDBGP_LOG_LEVEL` | `INFO` |
| `WDBGP_LOG_FORMAT` | `text` |
| `WDBGP_TRUST_PROXY_HEADERS` | `false` |
| `WDBGP_STATUS_ALLOWED` | пусто (IP не разрешены) |
| `WDBGP_STATUS_TOKEN` | пусто (токен не задан) |
| `WDBGP_DEFAULT_WEB_AUTH` | `network` |
| `WDBGP_JS_TIMEOUT` | `120` секунд |
| `WDBGP_JS_MAX_SOURCE` | `1048576` (1 МиБ) |
| `WDBGP_JS_MAX_RESPONSE` | `16777216` (16 МиБ) |
| `WDBGP_JS_MAX_TOTAL` | `67108864` (64 МиБ) |
| `WDBGP_JS_MAX_ENTRIES` | `1000000` |
| `WDBGP_JS_MAX_REQUESTS` | `200` |
| `WDBGP_JS_MAX_CALL_STACK` | `1000` |
| `WDBGP_ADAPTER_BACKUP_DIR` | `<db_dir>/backup/adapters` |
| `WDBGP_ADAPTER_BACKUP_MAX` | `10` |

`WDBGP_ADMIN_PASSWORD` и `WDBGP_SESSION_SECRET` обязательны для `serve`.
Старые имена `WDBGP_BIRD_LOCAL_ADDRESS` и
`WDBGP_BIRD_LOCAL_ADDRESS_V6` пока принимаются как совместимые aliases.
Если `WDBGP_BGP_LOCAL_ADDRESS_V6` не задан, IPv6-выбор сохраняется в базе, но
анонсируются только IPv4-префиксы.

Эндпоинт `/status` возвращает операционные данные в JSON. Доступ требует
либо IP клиента из `WDBGP_STATUS_ALLOWED` (CIDR через запятую), либо
заголовок `Authorization: Bearer <WDBGP_STATUS_TOKEN>`. Если ни то, ни другое не задано — 403.

### BGP Communities

Каждой категории и сервису назначается BGP Large Community (`ASN:0:Number`).
Значения сообществ генерируются автоматически по читаемой человеком схеме (группы: 10000, 20000, 30000…;
сервисы: группа+1, группа+2…) и могут редактироваться администратором на странице `/admin/communities`.
Эти сообщества добавляются к каждому анонсируемому BGP-префиксу, позволяя настраивать
маршрутизацию по категориям и сервисам на стороне роутера.

### Проверка и ограничения

Все значения проверяются при запуске с понятными сообщениями об ошибках. Если не указаны, применяются значения по умолчанию.

| Переменная | Ограничения |
| --- | --- |
| `WDBGP_PORT` / `WDBGP_BGP_PORT` | Целое число 1–65535 |
| `WDBGP_LOCAL_ASN` | Целое число 1–4294967295 |
| `WDBGP_SYNC_INTERVAL` | Целое число ≥1 (секунд) |
| `WDBGP_ROUTER_ID` | Корректный IPv4-адрес |
| `WDBGP_BGP_LOCAL_ADDRESS` | Корректный IPv4-адрес |
| `WDBGP_BGP_LOCAL_ADDRESS_V6` | Корректный IPv6-адрес (или пусто для отключения IPv6-анонсов) |
| `WDBGP_SECURITY_HEADERS` | Логическое; включает HTTP security headers (CSP, HSTS, X-Frame-Options и др.) |
| `WDBGP_RATE_LIMIT_LOGIN` | Целое число 1–1000; запросов входа в минуту (по умолчанию 5) |
| `WDBGP_RATE_LIMIT_ADMIN` | Целое число 1–1000; запросов admin API в минуту (по умолчанию 30) |
| `WDBGP_SESSION_MAX_AGE` | Целое число 60–31536000; срок действия session cookie в секундах (по умолчанию 28800 = 8 часов) |
| `WDBGP_LOG_LEVEL` | DEBUG, INFO, WARN, ERROR, FATAL, PANIC (по умолчанию INFO) |
| `WDBGP_LOG_FORMAT` | text или json (по умолчанию text) |
| `WDBGP_TRUST_PROXY_HEADERS` | Логическое; доверять заголовку X-Forwarded-Proto для определения безопасности cookie |
| `WDBGP_DEFAULT_WEB_AUTH` | network, login, both или any |
| `WDBGP_JS_TIMEOUT` | Целое число ≥1; таймаут выполнения адаптера в секундах (по умолчанию 120) |
| `WDBGP_JS_MAX_SOURCE` | Целое число ≥1; макс. размер исходного кода адаптера в байтах (по умолчанию 1 МиБ) |
| `WDBGP_JS_MAX_RESPONSE` | Целое число ≥1; макс. байт HTTP-ответа на запрос (по умолчанию 16 МиБ) |
| `WDBGP_JS_MAX_TOTAL` | Целое число ≥1; макс. суммарных байт HTTP-ответов за запуск адаптера (по умолчанию 64 МиБ) |
| `WDBGP_JS_MAX_ENTRIES` | Целое число ≥1; макс. CIDR-записей на выходе адаптера (по умолчанию 1 000 000) |
| `WDBGP_JS_MAX_REQUESTS` | Целое число ≥1; макс. HTTP-запросов за запуск адаптера (по умолчанию 200) |
| `WDBGP_JS_MAX_CALL_STACK` | Целое число ≥1; макс. глубина call stack JavaScript (по умолчанию 1000) |

Приложение предоставляет эндпоинт `/status` для мониторинга состояния, возвращающий базовую информацию о работоспособности и версии в формате JSON.

## Миграции

Миграции SQLite выполняются автоматически и транзакционно при каждом запуске
любой команды. Существующая база Python-версии обновляется на месте без смены
формата данных. Применённые версии записываются в `schema_migrations`.

Приложение не запускается с неизвестной более новой версией схемы. Перед
крупным обновлением всё равно рекомендуется остановить контейнер и скопировать
постоянный том `/data`.

```sh
docker run --rm -v wdbgp-data:/data wh1ted/wdbgp:latest migrate
docker run --rm -v wdbgp-data:/data wh1ted/wdbgp:latest stats
docker run --rm -v wdbgp-data:/data wh1ted/wdbgp:latest sync
```

## Разработка

```sh
go test ./...
go vet ./...
go build ./cmd/wdbgp
docker build -t wdbgp:latest .
```

## Контейнер на MikroTik

Пример использует `172.31.255.2` для контейнера и `172.31.255.1` для RouterOS:

```routeros
/interface/veth/add name=veth-wdbgp address=172.31.255.2/30 gateway=172.31.255.1
/interface/bridge/add name=br-containers
/interface/bridge/port/add bridge=br-containers interface=veth-wdbgp
/ip/address/add address=172.31.255.1/30 interface=br-containers

/container/envs/add list=wdbgp key=WDBGP_ADMIN_PASSWORD value="change-me"
/container/envs/add list=wdbgp key=WDBGP_SESSION_SECRET value="replace-with-a-long-random-secret"
/container/envs/add list=wdbgp key=WDBGP_LOCAL_ASN value="64512"
/container/envs/add list=wdbgp key=WDBGP_ROUTER_ID value="172.31.255.2"
/container/envs/add list=wdbgp key=WDBGP_BGP_LOCAL_ADDRESS value="172.31.255.2"

/container/mounts/add name=wdbgp-data src=disk1/wdbgp-data dst=/data
/container/add remote-image=wh1ted/wdbgp:latest interface=veth-wdbgp \
  root-dir=disk1/images/wdbgp mounts=wdbgp-data envlist=wdbgp \
  start-on-boot=yes logging=yes
```

Разрешите в RouterOS firewall HTTP к порту 8080 из пользовательских сетей,
TCP/179 между контейнером и BGP peers, а также forwarding к полученным
destination-префиксам. Для IPv6 добавьте адрес контейнера и
`WDBGP_BGP_LOCAL_ADDRESS_V6`.

## Ограничения

- Изменение BGP-параметров пользователя перезапускает встроенный BGP server;
  изменение выбора маршрутов применяется без перезапуска.
