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


def _prefix_version(prefix: str) -> int:
    return ipaddress.ip_network(prefix).version


def _versioned_prefixes(prefixes: list[str], version: int) -> list[str]:
    return [prefix for prefix in prefixes if _prefix_version(prefix) == version]


def _filter_lines(name: str, prefixes: list[str]) -> list[str]:
    prefix_set = ", ".join(prefixes)
    return [
        f"filter {name} {{",
        f"  if net ~ [ {prefix_set} ] then accept;" if prefixes else "  reject;",
        "  reject;" if prefixes else "",
        "}",
        "",
    ]


def _static_protocol_lines(name: str, channel: str, prefixes: list[str]) -> list[str]:
    lines = [
        f"protocol static {name} {{",
        f"  {channel};",
    ]
    lines.extend(f"  route {prefix} blackhole;" for prefix in prefixes)
    lines.extend(["}", ""])
    return lines


def _next_hop_line(next_hop: str | None, version: int) -> str | None:
    if not next_hop:
        return None
    if ipaddress.ip_address(next_hop).version != version:
        return None
    return f"    next hop address {next_hop};"


def _local_address(config: Config, peer_ip: str) -> str:
    peer_version = ipaddress.ip_address(peer_ip).version
    if peer_version == 6 and config.bird_local_address_v6:
        return config.bird_local_address_v6
    return config.bird_local_address


def render(config: Config, include_secrets: bool = True) -> str:
    with contextlib.closing(connect(config.db_path)) as db:
        users = list(db.execute("SELECT * FROM users WHERE enabled = 1 ORDER BY id"))
        all_selected_prefixes = sorted(
            {
                prefix
                for user in users
                for prefix in selected_prefixes(db, user["id"])
            }
        )
        all_v4_prefixes = _versioned_prefixes(all_selected_prefixes, 4)
        all_v6_prefixes = _versioned_prefixes(all_selected_prefixes, 6)

        lines = [
            f"router id {config.router_id};",
            "log stderr all;",
            "protocol device {}",
            "",
        ]
        lines.extend(_static_protocol_lines("catalog_v4", "ipv4", all_v4_prefixes))
        lines.extend(_static_protocol_lines("catalog_v6", "ipv6", all_v6_prefixes))

        for user in users:
            prefixes = selected_prefixes(db, user["id"])
            v4_prefixes = _versioned_prefixes(prefixes, 4)
            v6_prefixes = _versioned_prefixes(prefixes, 6)
            user_name = _name(user["id"])
            local_address = _local_address(config, user["peer_ip"])
            lines.extend(_filter_lines(f"export_{user_name}_v4", v4_prefixes))
            lines.extend(_filter_lines(f"export_{user_name}_v6", v6_prefixes))
            lines.extend([
                f"protocol bgp {user_name} {{",
                f"  local {local_address} as {config.local_asn};",
                f"  neighbor {user['peer_ip']} as {user['peer_asn']};",
            ])
            if user["bgp_password"]:
                password = user["bgp_password"] if include_secrets else "<redacted>"
                escaped = password.replace("\\", "\\\\").replace('"', '\\"')
                lines.append(f'  password "{escaped}";')
            lines.extend(
                [
                    "  multihop;",
                    "  ipv4 {",
                    "    import none;",
                    f"    export filter export_{user_name}_v4;",
                ]
            )
            lines.extend(line for line in [_next_hop_line(user["next_hop"], 4)] if line)
            lines.extend([
                "  };",
                "  ipv6 {",
                "    import none;",
                f"    export filter export_{user_name}_v6;",
            ])
            lines.extend(line for line in [_next_hop_line(user["next_hop"], 6)] if line)
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
