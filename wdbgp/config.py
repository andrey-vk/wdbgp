from __future__ import annotations

import os
from dataclasses import dataclass


def _bool(name: str, default: bool = False) -> bool:
    value = os.getenv(name)
    if value is None:
        return default
    return value.lower() in {"1", "true", "yes", "on"}


@dataclass(frozen=True)
class Config:
    db_path: str = os.getenv("WDBGP_DB", "data/wdbgp.sqlite3")
    bird_config: str = os.getenv("WDBGP_BIRD_CONFIG", "runtime/bird.conf")
    bird_socket: str = os.getenv("WDBGP_BIRD_SOCKET", "/run/bird/bird.ctl")
    host: str = os.getenv("WDBGP_HOST", "127.0.0.1")
    port: int = int(os.getenv("WDBGP_PORT", "8080"))
    local_asn: int = int(os.getenv("WDBGP_LOCAL_ASN", "64512"))
    router_id: str = os.getenv("WDBGP_ROUTER_ID", "192.0.2.1")
    bird_local_address: str = os.getenv("WDBGP_BIRD_LOCAL_ADDRESS", "192.0.2.2")
    admin_password: str = os.getenv("WDBGP_ADMIN_PASSWORD", "")
    session_secret: str = os.getenv("WDBGP_SESSION_SECRET", "")
    trust_proxy_headers: bool = _bool("WDBGP_TRUST_PROXY_HEADERS")
    sync_interval: int = int(os.getenv("WDBGP_SYNC_INTERVAL", "3600"))

