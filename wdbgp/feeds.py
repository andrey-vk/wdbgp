from __future__ import annotations

import datetime as dt
import ipaddress
import json
import urllib.parse
import urllib.request
from collections.abc import Iterable, Mapping

from .db import normalize_cidr, transaction


def parse_entries(
    payload: object,
    category_lookup: Mapping[str, set[str]] | None = None,
    default_category: str | None = None,
) -> list[tuple[str, str, str]]:
    """Parse canonical feed JSON into (category, service, cidr) tuples.

    Supported shapes:
      {"category": "ai", "service": "openai", "cidrs": ["1.2.3.0/24"]}
      [{"category": "ai", "service": "openai", "cidrs": [...]}]
      {"entries": [{...}, {...}]}
    """
    if isinstance(payload, dict) and "entries" in payload:
        items: Iterable[object] = payload["entries"]
    elif _is_opencck_payload(payload):
        return _parse_opencck_entries(payload)
    elif _is_opencck_cidr_payload(payload):
        if not default_category:
            raise ValueError("flat OpenCCK CIDR data requires a default category")
        return _parse_opencck_cidr_entries(payload, category_lookup or {}, default_category)
    elif isinstance(payload, list):
        items = payload
    else:
        items = [payload]

    result: set[tuple[str, str, str]] = set()
    for item in items:
        if not isinstance(item, dict):
            raise ValueError("each feed entry must be an object")
        category = str(item.get("category", "")).strip()
        service = str(item.get("service", "")).strip()
        cidrs = item.get("cidrs")
        if not category or not service or not isinstance(cidrs, list):
            raise ValueError("each entry requires category, service and cidrs[]")
        for cidr in cidrs:
            network = ipaddress.ip_network(str(cidr).strip(), strict=False)
            result.add((category, service, str(network)))
    return sorted(result)


def _is_opencck_payload(payload: object) -> bool:
    if not isinstance(payload, dict) or not payload:
        return False
    return all(
        isinstance(value, dict)
        and isinstance(value.get("group"), str)
        and isinstance(value.get("cidr4"), list)
        and (value.get("cidr6") is None or isinstance(value.get("cidr6"), list))
        for value in payload.values()
    )


def _is_opencck_cidr_payload(payload: object) -> bool:
    return (
        isinstance(payload, dict)
        and bool(payload)
        and all(isinstance(service, str) and isinstance(cidrs, list) for service, cidrs in payload.items())
    )


def _parse_opencck_entries(payload: dict[object, object]) -> list[tuple[str, str, str]]:
    result: set[tuple[str, str, str]] = set()
    for key, value in payload.items():
        assert isinstance(value, dict)
        category = str(value["group"]).strip()
        service = str(value.get("name") or key).strip()
        if not category or not service:
            raise ValueError("OpenCCK entry requires group and name")
        for cidr in [*value["cidr4"], *value.get("cidr6", [])]:
            network = ipaddress.ip_network(str(cidr).strip(), strict=False)
            result.add((category, service, str(network)))
    return sorted(result)


def _parse_opencck_cidr_entries(
    payload: dict[object, object],
    category_lookup: Mapping[str, set[str]],
    default_category: str,
) -> list[tuple[str, str, str]]:
    result: set[tuple[str, str, str]] = set()
    for key, cidrs in payload.items():
        service = str(key).strip()
        categories = category_lookup.get(service) or {default_category}
        assert isinstance(cidrs, list)
        for cidr in cidrs:
            network = ipaddress.ip_network(str(cidr).strip(), strict=False)
            result.update((category, service, str(network)) for category in categories)
    return sorted(result)


def download(url: str, timeout: int = 30) -> object:
    request = urllib.request.Request(url, headers={"User-Agent": "wdbgp/0.1"})
    with urllib.request.urlopen(request, timeout=timeout) as response:
        return json.load(response)


def metadata_url(url: str) -> str:
    parsed = urllib.parse.urlsplit(url)
    if parsed.hostname not in {"iplist.opencck.org", "beta.iplist.opencck.org"}:
        return url
    query = urllib.parse.parse_qsl(parsed.query, keep_blank_values=True)
    if not any(key == "data" and value in {"cidr4", "cidr6"} for key, value in query):
        return url
    query = [(key, "group" if key == "data" else value) for key, value in query]
    return urllib.parse.urlunsplit(
        (parsed.scheme, parsed.netloc, parsed.path, urllib.parse.urlencode(query), parsed.fragment)
    )


def parse_category_lookup(payload: object) -> dict[str, set[str]]:
    if not isinstance(payload, dict):
        raise ValueError("OpenCCK group response must be an object")
    result: dict[str, set[str]] = {}
    for service, category in payload.items():
        if not isinstance(service, str) or not isinstance(category, str):
            raise ValueError("OpenCCK group response must map services to categories")
        result.setdefault(service, set()).add(category)
    return result


def sync_all(db_path: str) -> list[str]:
    errors: list[str] = []
    with transaction(db_path) as db:
        feeds = list(db.execute("SELECT * FROM feeds WHERE enabled = 1 ORDER BY id"))

    for feed in feeds:
        try:
            with transaction(db_path) as db:
                rows = db.execute(
                    "SELECT DISTINCT category, service FROM catalog_entries WHERE feed_id != ?",
                    (feed["id"],),
                )
                category_lookup: dict[str, set[str]] = {}
                for row in rows:
                    category_lookup.setdefault(row["service"], set()).add(row["category"])
            group_url = metadata_url(feed["url"])
            if group_url != feed["url"]:
                for service, categories in parse_category_lookup(
                    download(group_url, timeout=120)
                ).items():
                    category_lookup.setdefault(service, set()).update(categories)
            entries = parse_entries(
                download(feed["url"], timeout=120),
                category_lookup=category_lookup,
                default_category=feed["name"],
            )
            now = dt.datetime.now(dt.UTC).isoformat()
            with transaction(db_path) as db:
                db.execute("DELETE FROM catalog_entries WHERE feed_id = ?", (feed["id"],))
                db.executemany(
                    "INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES (?, ?, ?, ?)",
                    [(feed["id"], category, service, normalize_cidr(cidr)) for category, service, cidr in entries],
                )
                db.execute(
                    "UPDATE feeds SET last_success = ?, last_error = NULL WHERE id = ?",
                    (now, feed["id"]),
                )
        except Exception as exc:  # Keep the previous good snapshot on failed downloads.
            message = f'{feed["name"]}: {exc}'
            errors.append(message)
            with transaction(db_path) as db:
                db.execute("UPDATE feeds SET last_error = ? WHERE id = ?", (str(exc), feed["id"]))
    return errors
