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
инфраструктуры и `IPRanges` с более узкими диапазонами провайдеров, ботов,
мониторинга и privacy-сервисов из
[lord-alfred/ipranges](https://github.com/lord-alfred/ipranges).

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
раскладывает их по отдельным сервисам каталога. Изначально режим отключён:
перед настройкой пользователей его нужно включить и синхронизировать фиды.

## Сетевая модель

Контейнер является самостоятельным BGP speaker со своим `veth`-адресом и не
изменяет RouterOS через API.

- Пользовательские CIDR определяют пользователя веб-интерфейса. Побеждает самая
  специфичная подходящая сеть.
- IP BGP peer и ASN определяют роутер, которому экспортируются маршруты.
- Next hop должен быть достижим с клиентского роутера по нужному VPN-маршруту.

Не публикуйте HTTP и TCP/179 в недоверенные сети.

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

`WDBGP_ADMIN_PASSWORD` и `WDBGP_SESSION_SECRET` обязательны для `serve`.
Старые имена `WDBGP_BIRD_LOCAL_ADDRESS` и
`WDBGP_BIRD_LOCAL_ADDRESS_V6` пока принимаются как совместимые aliases.
Если `WDBGP_BGP_LOCAL_ADDRESS_V6` не задан, IPv6-выбор сохраняется в базе, но
анонсируются только IPv4-префиксы.

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

- Встроенный адаптер нестандартного формата есть только для OpenCCK.
- Изменение BGP-параметров пользователя перезапускает встроенный BGP server;
  изменение выбора маршрутов применяется без перезапуска.
