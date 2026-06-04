from __future__ import annotations

import contextlib
import ipaddress
import os
import subprocess
import tempfile

from .config import Config
from .db import connect, selected_prefixes


def _name(user_id: int) -> str:
    return f"user_{user_id}"


def render(config: Config) -> str:
    with contextlib.closing(connect(config.db_path)) as db:
        users = list(db.execute("SELECT * FROM users WHERE enabled = 1 ORDER BY id"))
        all_prefixes = sorted(
            {
                prefix
                for user in users
                for prefix in selected_prefixes(db, user["id"])
                if ipaddress.ip_network(prefix).version == 4
            }
        )

        lines = [
            f"router id {config.router_id};",
            "log stderr all;",
            "protocol device {}",
            "",
            "protocol static catalog_v4 {",
            "  ipv4;",
        ]
        lines.extend(f"  route {prefix} blackhole;" for prefix in all_prefixes)
        lines.extend(["}", ""])

        for user in users:
            prefixes = [
                p for p in selected_prefixes(db, user["id"]) if ipaddress.ip_network(p).version == 4
            ]
            prefix_set = ", ".join(prefixes)
            lines.extend(
                [
                    f"filter export_{_name(user['id'])} {{",
                    f"  if net ~ [ {prefix_set} ] then accept;" if prefixes else "  reject;",
                    "  reject;" if prefixes else "",
                    "}",
                    "",
                    f"protocol bgp {_name(user['id'])} {{",
                    f"  local {config.bird_local_address} as {config.local_asn};",
                    f"  neighbor {user['peer_ip']} as {user['peer_asn']};",
                ]
            )
            if user["bgp_password"]:
                escaped = user["bgp_password"].replace("\\", "\\\\").replace('"', '\\"')
                lines.append(f'  password "{escaped}";')
            lines.extend(
                [
                    "  multihop;",
                    "  ipv4 {",
                    "    import none;",
                    f"    export filter export_{_name(user['id'])};",
                ]
            )
            if user["next_hop"]:
                lines.append(f"    next hop address {user['next_hop']};")
            lines.extend(["  };", "}", ""])
    return "\n".join(line for line in lines if line is not None) + "\n"


def write_config(config: Config) -> None:
    content = render(config)
    parent = os.path.dirname(config.bird_config)
    if parent:
        os.makedirs(parent, exist_ok=True)
    fd, tmp_path = tempfile.mkstemp(prefix="bird.", dir=parent or ".")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            handle.write(content)
        os.replace(tmp_path, config.bird_config)
    finally:
        if os.path.exists(tmp_path):
            os.unlink(tmp_path)


def reload(config: Config) -> tuple[bool, str]:
    write_config(config)
    try:
        process = subprocess.run(
            ["birdc", "-s", config.bird_socket, "configure"],
            capture_output=True,
            text=True,
            timeout=30,
            check=False,
        )
    except OSError as exc:
        return False, str(exc)
    output = (process.stdout + process.stderr).strip()
    return process.returncode == 0, output
