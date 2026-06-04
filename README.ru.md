# wdbgp

[![Tests](https://github.com/andrey-vk/wdbgp/actions/workflows/tests.yml/badge.svg)](https://github.com/andrey-vk/wdbgp/actions/workflows/tests.yml)
[![Publish Docker Image](https://github.com/andrey-vk/wdbgp/actions/workflows/deploy.yml/badge.svg)](https://github.com/andrey-vk/wdbgp/actions/workflows/deploy.yml)
[![License](https://img.shields.io/github/license/andrey-vk/wdbgp)](LICENSE)
[![Docker Image Version](https://img.shields.io/docker/v/wh1ted/wdbgp?label=docker)](https://hub.docker.com/r/wh1ted/wdbgp)
[![Docker Pulls](https://img.shields.io/docker/pulls/wh1ted/wdbgp)](https://hub.docker.com/r/wh1ted/wdbgp)
![Python](https://img.shields.io/badge/python-3.14-blue)
![Alpine](https://img.shields.io/badge/alpine-3.23-0d597f)
![BIRD](https://img.shields.io/badge/BIRD-2.x-green)
![RouterOS](https://img.shields.io/badge/RouterOS-container-blue)
![IPv4](https://img.shields.io/badge/IP-IPv4_only-orange)

[English version](README.md)

`wdbgp` скачивает категоризированные IPv4 CIDR-фиды, собирает динамический
каталог сервисов, позволяет пользователям выбирать категории или отдельные
сервисы и анонсирует выбранные префиксы на пользовательские роутеры через BGP.

Контейнер включает:

- небольшое Python-веб-приложение с SQLite;
- BIRD 2 как BGP speaker;
- отдельную BGP export policy для каждого пользовательского роутера.

## Важная сетевая модель

Контейнер является самостоятельным BGP speaker со своим `veth`-адресом. Он не
меняет конфигурацию BGP в RouterOS через API.

У каждого пользователя есть два разных набора адресов:

- **Пользовательские сети** определяют, кто открыл веб-интерфейс. Если подходит
  несколько подсетей, выбирается самая специфичная.
- **IP BGP peer** определяет пользовательский роутер, которому будут
  анонсироваться маршруты.

BGP next hop должен быть достижим для пользовательского роутера через VPN.
Обычно это стабильный адрес контейнера, маршрутизируемый через MikroTik.
MikroTik дальше должен пересылать трафик для анонсированных префиксов по
нужному пути.

Не публикуйте TCP/179 и веб-интерфейс в недоверенные сети. В RouterOS firewall
лучше разрешать доступ только из нужных VPN-подсетей.

## Формат фидов

По умолчанию установлены два OpenCCK-фида:

```text
https://iplist.opencck.org/?format=json&data=cidr4
https://beta.iplist.opencck.org/?format=json&data=cidr4
```

Ответ OpenCCK `data=cidr4` не содержит категории. Для каждого OpenCCK-фида
приложение дополнительно запрашивает компактный `data=group` и объединяет эти
данные. Большой полный JSON OpenCCK не скачивается.

Первое скачивание фидов запускается сразу при старте веб-сервиса, затем
повторяется каждые `WDBGP_SYNC_INTERVAL` секунд.

Канонический JSON-формат:

```json
{
  "entries": [
    {
      "category": "ai",
      "service": "openai",
      "cidrs": ["104.18.0.0/16", "172.64.0.0/13"]
    },
    {
      "category": "video",
      "service": "netflix",
      "cidrs": ["198.38.96.0/19"]
    }
  ]
}
```

Также принимается один объект entry или массив entry-объектов верхнего уровня.
Дублирующиеся префиксы дедуплицируются. Один префикс может относиться к
нескольким сервисам. Текущая версия намеренно принимает только IPv4.

Выбор категории включает все текущие и будущие сервисы этой категории. Выбор
отдельных сервисов добавляет только их. Итоговый набор экспортируемых маршрутов
является объединением всех выбранных пунктов.

## Локальный запуск

Обязательные переменные окружения:

```text
WDBGP_ADMIN_PASSWORD=change-me
WDBGP_SESSION_SECRET=a-long-random-secret
WDBGP_LOCAL_ASN=64512
WDBGP_ROUTER_ID=172.31.255.2
WDBGP_BIRD_LOCAL_ADDRESS=172.31.255.2
```

Сборка и запуск:

```sh
docker build -t wdbgp:latest .
docker run --rm \
  -p 8080:8080 \
  -p 179:179 \
  -v wdbgp-data:/data \
  -e WDBGP_ADMIN_PASSWORD=change-me \
  -e WDBGP_SESSION_SECRET=a-long-random-secret \
  -e WDBGP_LOCAL_ASN=64512 \
  -e WDBGP_ROUTER_ID=172.31.255.2 \
  -e WDBGP_BIRD_LOCAL_ADDRESS=172.31.255.2 \
  wdbgp:latest
```

Откройте `/admin`, добавьте пользователей и проверьте состояние OpenCCK-фидов.
Публичная страница `/` определяет пользователя по исходному IP-адресу.

Полезные команды:

```sh
python -m unittest discover -s tests
python -m wdbgp render-bird
python -m wdbgp sync
python -m wdbgp stats
```

## Контейнер на MikroTik

Имена интерфейсов и адреса должны соответствовать вашему роутеру. В этом примере
контейнер использует `172.31.255.2`, а RouterOS - `172.31.255.1`:

```routeros
/interface/veth/add name=veth-wdbgp address=172.31.255.2/30 gateway=172.31.255.1
/interface/bridge/add name=br-containers
/interface/bridge/port/add bridge=br-containers interface=veth-wdbgp
/ip/address/add address=172.31.255.1/30 interface=br-containers

/container/envs/add list=wdbgp key=WDBGP_ADMIN_PASSWORD value="change-me"
/container/envs/add list=wdbgp key=WDBGP_SESSION_SECRET value="replace-with-a-long-random-secret"
/container/envs/add list=wdbgp key=WDBGP_LOCAL_ASN value="64512"
/container/envs/add list=wdbgp key=WDBGP_ROUTER_ID value="172.31.255.2"
/container/envs/add list=wdbgp key=WDBGP_BIRD_LOCAL_ADDRESS value="172.31.255.2"

/container/mounts/add name=wdbgp-data src=disk1/wdbgp-data dst=/data
/container/add remote-image=YOUR_REGISTRY/wdbgp:latest interface=veth-wdbgp \
  root-dir=disk1/images/wdbgp mounts=wdbgp-data envlist=wdbgp \
  start-on-boot=yes logging=yes
```

Добавьте правила RouterOS firewall/routing для:

- HTTP-доступа к `172.31.255.2:8080` из пользовательских VPN-сетей;
- TCP/179 между `172.31.255.2` и настроенными BGP peers;
- достижимости `172.31.255.2` от каждого peer, если этот адрес используется как
  BGP next hop;
- нужного forwarding path для трафика к анонсированным destination CIDR.

RouterOS containers по умолчанию отключены и должны использовать внешнее
хранилище. См. официальную документацию
[MikroTik Container](https://help.mikrotik.com/docs/display/ROS/Container).

## Текущие ограничения

- Только IPv4.
- Единственный встроенный адаптер нестандартного фида - OpenCCK.
- Пока нет формы удаления и редактирования фидов.
- Админские сессии инвалидируются только при смене `WDBGP_SESSION_SECRET`.
- Большие каталоги рендерятся в BIRD prefix sets; для целевой модели MikroTik и
  ожидаемого количества маршрутов нужно отдельное capacity testing.
