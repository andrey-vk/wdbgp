# wdbgp

[![Publish Docker Image](https://github.com/andrey-vk/wdbgp/actions/workflows/deploy.yml/badge.svg)](https://github.com/andrey-vk/wdbgp/actions/workflows/deploy.yml)
[![License](https://img.shields.io/github/license/andrey-vk/wdbgp)](LICENSE)
[![Docker Image Version](https://img.shields.io/docker/v/wh1ted/wdbgp?label=docker)](https://hub.docker.com/r/wh1ted/wdbgp)
[![Docker Pulls](https://img.shields.io/docker/pulls/wh1ted/wdbgp)](https://hub.docker.com/r/wh1ted/wdbgp)
![Python](https://img.shields.io/badge/python-3.14-blue)
![Alpine](https://img.shields.io/badge/alpine-3.23-0d597f)
![BIRD](https://img.shields.io/badge/BIRD-2.x-green)
![RouterOS](https://img.shields.io/badge/RouterOS-container-blue)
![IPv4](https://img.shields.io/badge/IP-IPv4_only-orange)

[Русская версия](README.ru.md)

`wdbgp` downloads categorized IPv4 CIDR feeds, builds a dynamic service catalog,
lets each VPN-connected user select categories or individual services, and
announces the resulting prefix set to that user's router over BGP.

The container includes:

- a small Python web application with SQLite storage;
- BIRD 2 as the BGP speaker;
- one independent BGP export policy per user router.

## Important network model

The container is a BGP speaker with its own `veth` address. It does not modify
RouterOS BGP configuration through an API.

Each user has two different kinds of addresses:

- **User networks** identify web requests. The most specific matching subnet
  wins.
- **BGP peer IP** identifies the user's router.

The BGP next hop must be reachable by the user router through the VPN. Usually
this is the container's stable BGP source address, routed through the MikroTik.
The MikroTik must then forward traffic for the announced destinations according
to the intended policy.

Do not expose TCP/179 or the web UI to untrusted networks. RouterOS firewall
rules should allow them only from the required VPN subnets.

## Feed format

Two OpenCCK feeds are installed automatically:

```text
https://iplist.opencck.org/?format=json&data=cidr4
https://beta.iplist.opencck.org/?format=json&data=cidr4
```

OpenCCK's `data=cidr4` response does not include categories. For each OpenCCK
feed, the service also requests the compact `data=group` response and combines
the two service-keyed objects. It does not download OpenCCK's much larger full
JSON response.

The first download starts immediately when the web service starts, then repeats
every `WDBGP_SYNC_INTERVAL` seconds.

The canonical JSON format is:

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

A single entry object or a top-level array of entry objects is also accepted.
Duplicate prefixes are deduplicated. A prefix may belong to multiple services.
The current MVP intentionally rejects IPv6 prefixes.

Selecting a category includes every current and future service in that category.
Selecting individual services adds only those services. The exported route set
is the union of all selections.

## Run locally

Required environment variables:

```text
WDBGP_ADMIN_PASSWORD=change-me
WDBGP_SESSION_SECRET=a-long-random-secret
WDBGP_LOCAL_ASN=64512
WDBGP_ROUTER_ID=172.31.255.2
WDBGP_BIRD_LOCAL_ADDRESS=172.31.255.2
```

Build and run:

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

Open `/admin`, add users, and monitor the preconfigured OpenCCK feeds. The
public `/` page identifies the user by source address.

Useful commands:

```sh
python -m unittest discover -s tests
python -m wdbgp render-bird
python -m wdbgp sync
python -m wdbgp stats
```

## MikroTik container outline

The exact addresses and interface names must match the router. This example
uses `172.31.255.2` for the container and `172.31.255.1` for RouterOS:

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

Add RouterOS firewall and routing rules for:

- HTTP access to `172.31.255.2:8080` from user VPN networks;
- TCP/179 between `172.31.255.2` and configured BGP peers;
- reachability of `172.31.255.2` from each peer when it is used as BGP next hop;
- the desired forwarding path for traffic matching announced destination CIDRs.

RouterOS containers are disabled by default and should use external storage.
See the official [MikroTik Container documentation](https://help.mikrotik.com/docs/display/ROS/Container).

## Current limitations

- IPv4 only.
- The only built-in non-canonical feed adapter is OpenCCK.
- No delete/edit form for feeds.
- Admin sessions are invalidated only when `WDBGP_SESSION_SECRET` changes.
- Large catalogs are rendered into BIRD prefix sets; capacity testing is needed
  for the target MikroTik model and expected route count.
