from __future__ import annotations

import contextlib
import ipaddress
import os
import sqlite3
from collections.abc import Iterator


SCHEMA = """
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;

CREATE TABLE IF NOT EXISTS feeds (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    url TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    last_success TEXT,
    last_error TEXT
);

CREATE TABLE IF NOT EXISTS catalog_entries (
    feed_id INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
    category TEXT NOT NULL,
    service TEXT NOT NULL,
    cidr TEXT NOT NULL,
    PRIMARY KEY (feed_id, category, service, cidr)
);

CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    peer_ip TEXT NOT NULL UNIQUE,
    peer_asn INTEGER NOT NULL,
    next_hop TEXT,
    bgp_password TEXT,
    selection_locked INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS user_networks (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    cidr TEXT NOT NULL UNIQUE,
    PRIMARY KEY (user_id, cidr)
);

CREATE TABLE IF NOT EXISTS selected_categories (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category TEXT NOT NULL,
    PRIMARY KEY (user_id, category)
);

CREATE TABLE IF NOT EXISTS selected_services (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category TEXT NOT NULL,
    service TEXT NOT NULL,
    PRIMARY KEY (user_id, category, service)
);
"""

DEFAULT_FEEDS = (
    ("opencck-main", "https://iplist.opencck.org/?format=json&data=cidr4"),
    ("opencck-beta", "https://beta.iplist.opencck.org/?format=json&data=cidr4"),
)


def connect(path: str) -> sqlite3.Connection:
    parent = os.path.dirname(path)
    if parent:
        os.makedirs(parent, exist_ok=True)
    db = sqlite3.connect(path, timeout=30)
    db.row_factory = sqlite3.Row
    db.execute("PRAGMA foreign_keys = ON")
    return db


def init(path: str) -> None:
    with contextlib.closing(connect(path)) as db:
        db.executescript(SCHEMA)
        db.executemany("INSERT OR IGNORE INTO feeds(name, url) VALUES (?, ?)", DEFAULT_FEEDS)
        db.commit()


@contextlib.contextmanager
def transaction(path: str) -> Iterator[sqlite3.Connection]:
    db = connect(path)
    try:
        with db:
            yield db
    finally:
        db.close()


def normalize_cidr(value: str) -> str:
    return str(ipaddress.ip_network(value.strip(), strict=False))


def find_user_by_ip(db: sqlite3.Connection, address: str) -> sqlite3.Row | None:
    ip = ipaddress.ip_address(address)
    matches: list[tuple[int, int]] = []
    for row in db.execute("SELECT user_id, cidr FROM user_networks"):
        network = ipaddress.ip_network(row["cidr"])
        if ip.version == network.version and ip in network:
            matches.append((network.prefixlen, row["user_id"]))
    if not matches:
        return None
    _, user_id = max(matches)
    return db.execute("SELECT * FROM users WHERE id = ? AND enabled = 1", (user_id,)).fetchone()


def catalog(db: sqlite3.Connection) -> dict[str, list[str]]:
    result: dict[str, list[str]] = {}
    rows = db.execute(
        "SELECT DISTINCT category, service FROM catalog_entries ORDER BY category, service"
    )
    for row in rows:
        result.setdefault(row["category"], []).append(row["service"])
    return result


def user_selection(db: sqlite3.Connection, user_id: int) -> tuple[set[str], set[tuple[str, str]]]:
    categories = {
        row["category"]
        for row in db.execute("SELECT category FROM selected_categories WHERE user_id = ?", (user_id,))
    }
    services = {
        (row["category"], row["service"])
        for row in db.execute(
            "SELECT category, service FROM selected_services WHERE user_id = ?", (user_id,)
        )
    }
    return categories, services


def set_user_selection(
    db: sqlite3.Connection,
    user_id: int,
    categories: set[str],
    services: set[tuple[str, str]],
) -> None:
    db.execute("DELETE FROM selected_categories WHERE user_id = ?", (user_id,))
    db.execute("DELETE FROM selected_services WHERE user_id = ?", (user_id,))
    db.executemany(
        "INSERT INTO selected_categories(user_id, category) VALUES (?, ?)",
        [(user_id, category) for category in sorted(categories)],
    )
    db.executemany(
        "INSERT INTO selected_services(user_id, category, service) VALUES (?, ?, ?)",
        [(user_id, category, service) for category, service in sorted(services)],
    )


def selected_prefixes(db: sqlite3.Connection, user_id: int) -> list[str]:
    rows = db.execute(
        """
        SELECT DISTINCT ce.cidr
        FROM catalog_entries ce
        WHERE ce.category IN (
            SELECT category FROM selected_categories WHERE user_id = ?
        ) OR EXISTS (
            SELECT 1 FROM selected_services ss
            WHERE ss.user_id = ?
              AND ss.category = ce.category
              AND ss.service = ce.service
        )
        ORDER BY ce.cidr
        """,
        (user_id, user_id),
    )
    return [row["cidr"] for row in rows]
