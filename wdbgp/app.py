from __future__ import annotations

import argparse
import contextlib
import hashlib
import hmac
import html
import http.cookies
import ipaddress
import secrets
import threading
import time
import urllib.parse
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from . import bird
from .config import Config
from .db import (
    catalog,
    connect,
    find_user_by_ip,
    init,
    normalize_cidr,
    set_user_selection,
    transaction,
    user_selection,
)
from .feeds import sync_all


CONFIG = Config()


def esc(value: object) -> str:
    return html.escape(str(value), quote=True)


def page(title: str, body: str) -> bytes:
    return f"""<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<title>{esc(title)}</title>
<style>
*{{box-sizing:border-box}}
body{{font:16px/1.45 system-ui,-apple-system,Segoe UI,sans-serif;max-width:1100px;margin:0 auto;padding:1.5rem 1rem 3rem;color:#18212b;background:#f6f8fb}}
header{{display:flex;gap:1rem;justify-content:space-between;align-items:center;margin:0 0 1rem}}
a{{color:#2457a6}} h1,h2,h3{{margin:.4rem 0 1rem}} code{{font-size:.9em}}
form{{margin:1rem 0}} label{{display:block;margin:.55rem 0;font-weight:600}}
input:not([type]),input[type=text],input[type=password],input[type=number]{{width:100%;max-width:42rem;padding:.6rem .7rem;border:1px solid #c8d2df;border-radius:.55rem;background:white}}
button,.button{{display:inline-block;padding:.65rem 1rem;border:0;border-radius:.6rem;background:#2457a6;color:white;font-weight:700;text-decoration:none;cursor:pointer}}
button.secondary{{background:#e7edf5;color:#243244}} button.danger{{background:#b42318}}
table{{border-collapse:separate;border-spacing:0;width:100%;background:white;border:1px solid #dfe5ee;border-radius:.8rem;overflow:hidden}} td,th{{border-bottom:1px solid #e8edf4;padding:.65rem;text-align:left}} tr:last-child td{{border-bottom:0}}
.card{{background:white;border:1px solid #dfe5ee;border-radius:1rem;padding:1rem;margin:1rem 0;box-shadow:0 8px 24px #16233a0d}}
.grid{{display:grid;grid-template-columns:repeat(auto-fit,minmax(16rem,1fr));gap:1rem}} .row-actions{{display:flex;gap:.5rem;align-items:center;flex-wrap:wrap}}
.muted{{color:#667}} .error{{color:#a00}} .ok{{color:#075}} .pill{{display:inline-block;padding:.15rem .5rem;border-radius:999px;background:#edf2f8;color:#445}}
.selection-form{{padding-bottom:5.5rem}} .save-bar{{position:sticky;top:.5rem;z-index:2;display:flex;gap:1rem;align-items:center;justify-content:space-between;background:#10294f;color:white;border-radius:1rem;padding:.8rem 1rem;box-shadow:0 12px 28px #10294f40}}
.save-bar .muted{{color:#d7e4f5}} .save-bar button{{background:#33a36f}} .save-bar button:disabled{{background:#71829b;cursor:not-allowed}}
.catalog-grid{{display:grid;grid-template-columns:repeat(auto-fit,minmax(18rem,1fr));gap:1rem;margin-top:1rem}} fieldset.category-card{{border:1px solid #dfe5ee;border-radius:1rem;background:white;margin:0;padding:0;overflow:hidden}}
.category-card legend{{float:left;width:100%;padding:.85rem 1rem;background:#eef4fb;border-bottom:1px solid #dfe5ee}} .category-card legend+*{{clear:both}}
.category-title{{display:flex;gap:.55rem;align-items:center;margin:0;font-size:1.05rem}} .service-list{{padding:.75rem 1rem 1rem}} .service-list label{{font-weight:500;margin:.4rem 0}}
.empty{{background:white;border:1px dashed #b9c5d4;border-radius:1rem;padding:1rem}}
</style></head><body>{body}</body></html>""".encode()


def parse_form(handler: BaseHTTPRequestHandler) -> dict[str, list[str]]:
    length = int(handler.headers.get("Content-Length", "0"))
    return urllib.parse.parse_qs(handler.rfile.read(length).decode(), keep_blank_values=True)


def first(form: dict[str, list[str]], key: str, default: str = "") -> str:
    return form.get(key, [default])[0].strip()


def token(secret: str) -> str:
    nonce = secrets.token_hex(16)
    signature = hmac.new(secret.encode(), nonce.encode(), hashlib.sha256).hexdigest()
    return f"{nonce}.{signature}"


def valid_token(secret: str, value: str) -> bool:
    try:
        nonce, signature = value.split(".", 1)
    except ValueError:
        return False
    expected = hmac.new(secret.encode(), nonce.encode(), hashlib.sha256).hexdigest()
    return hmac.compare_digest(signature, expected)


class Handler(BaseHTTPRequestHandler):
    server_version = "wdbgp/0.1"

    def log_message(self, fmt: str, *args: object) -> None:
        print(f"{self.address_string()} - {fmt % args}")

    def send_html(self, body: bytes, status: int = 200, headers: dict[str, str] | None = None) -> None:
        self.send_response(status)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        for key, value in (headers or {}).items():
            self.send_header(key, value)
        self.end_headers()
        self.wfile.write(body)

    def redirect(self, location: str, headers: dict[str, str] | None = None) -> None:
        self.send_response(HTTPStatus.SEE_OTHER)
        self.send_header("Location", location)
        for key, value in (headers or {}).items():
            self.send_header(key, value)
        self.end_headers()

    def client_ip(self) -> str:
        if CONFIG.trust_proxy_headers:
            forwarded = self.headers.get("X-Forwarded-For")
            if forwarded:
                return forwarded.split(",", 1)[0].strip()
        return self.client_address[0]

    def is_admin(self) -> bool:
        cookie = http.cookies.SimpleCookie(self.headers.get("Cookie"))
        value = cookie.get("wdbgp_admin")
        return bool(CONFIG.session_secret and value and valid_token(CONFIG.session_secret, value.value))

    def require_admin(self) -> bool:
        if self.is_admin():
            return True
        self.redirect("/admin/login")
        return False

    def do_GET(self) -> None:
        path = urllib.parse.urlparse(self.path).path
        if path == "/":
            self.user_page()
        elif path == "/admin/login":
            self.login_page()
        elif path == "/admin":
            if self.require_admin():
                self.admin_page()
        elif path.startswith("/admin/user/"):
            if self.require_admin():
                self.admin_user_page(int(path.rsplit("/", 1)[1]))
        elif path == "/healthz":
            self.send_html(page("ok", "<p class=ok>ok</p>"))
        else:
            self.send_error(404)

    def do_POST(self) -> None:
        path = urllib.parse.urlparse(self.path).path
        if path == "/selection":
            self.save_own_selection()
        elif path == "/admin/login":
            self.login()
        elif path == "/admin/feed":
            if self.require_admin():
                self.add_feed()
        elif path == "/admin/sync":
            if self.require_admin():
                self.sync()
        elif path == "/admin/user":
            if self.require_admin():
                self.add_user()
        elif path.startswith("/admin/user/") and path.endswith("/delete"):
            if self.require_admin():
                self.delete_admin_user(int(path.split("/")[-2]))
        elif path.startswith("/admin/user/"):
            if self.require_admin():
                self.save_admin_user(int(path.rsplit("/", 1)[1]))
        else:
            self.send_error(404)

    def login_page(self, error: str = "") -> None:
        message = f"<p class=error>{esc(error)}</p>" if error else ""
        self.send_html(page("Вход", f"""<h1>Админка</h1>{message}
<form method=post><label>Пароль <input type=password name=password autofocus></label>
<button>Войти</button></form>"""))

    def login(self) -> None:
        password = first(parse_form(self), "password")
        if CONFIG.admin_password and hmac.compare_digest(password, CONFIG.admin_password):
            cookie = f"wdbgp_admin={token(CONFIG.session_secret)}; Path=/; HttpOnly; SameSite=Strict"
            self.redirect("/admin", {"Set-Cookie": cookie})
        else:
            self.login_page("Неверный пароль")

    def user_page(self, message: str = "") -> None:
        with contextlib.closing(connect(CONFIG.db_path)) as db:
            user = find_user_by_ip(db, self.client_ip())
            if not user:
                self.send_html(page("Нет доступа", f"<h1>Нет доступа</h1><p>IP: <code>{esc(self.client_ip())}</code></p>"), 403)
                return
            categories, services = user_selection(db, user["id"])
            body = selection_form(db, user, categories, services, not user["selection_locked"], message)
        self.send_html(page("Выбор маршрутов", body))

    def save_own_selection(self) -> None:
        form = parse_form(self)
        with transaction(CONFIG.db_path) as db:
            user = find_user_by_ip(db, self.client_ip())
            if not user:
                self.send_error(403)
                return
            if user["selection_locked"]:
                self.send_error(403, "Selection is locked by administrator")
                return
            categories, services = selection_from_form(form)
            set_user_selection(db, user["id"], categories, services)
        ok, output = bird.reload(CONFIG)
        self.redirect("/?saved=1" if ok else "/?saved=0")

    def admin_page(self) -> None:
        with contextlib.closing(connect(CONFIG.db_path)) as db:
            feeds = list(db.execute("SELECT * FROM feeds ORDER BY id"))
            users = list(db.execute(
                """
                SELECT u.*, group_concat(un.cidr, ', ') AS networks
                FROM users u
                LEFT JOIN user_networks un ON un.user_id = u.id
                GROUP BY u.id
                ORDER BY u.name
                """
            ))
        feed_rows = "".join(
            f"<tr><td>{esc(f['name'])}</td><td><code>{esc(f['url'])}</code></td><td>{esc(f['last_success'] or '')}</td><td class=error>{esc(f['last_error'] or '')}</td></tr>"
            for f in feeds
        )
        user_rows = "".join(
            f"<tr><td><a href=/admin/user/{u['id']}>{esc(u['name'])}</a></td><td><code>{esc(u['networks'] or '')}</code></td>"
            f"<td><code>{esc(u['peer_ip'])}</code></td><td>{u['peer_asn']}</td><td>{'да' if u['selection_locked'] else 'нет'}</td></tr>"
            for u in users
        )
        body = f"""<header><h1>Админка</h1><a href=/>Пользовательский интерфейс</a></header>
<section class=card><h2>Фиды</h2><table><tr><th>Имя</th><th>URL</th><th>Последняя загрузка</th><th>Ошибка</th></tr>{feed_rows}</table>
<form method=post action=/admin/feed><h3>Добавить фид</h3>
<label>Имя <input name=name required></label><label>URL <input name=url required></label><button>Добавить</button></form>
<form method=post action=/admin/sync><button>Скачать фиды сейчас</button></form></section>
<section class=card><h2>Пользователи</h2><table><tr><th>Имя</th><th>CIDR</th><th>BGP peer</th><th>ASN</th><th>Выбор заблокирован</th></tr>{user_rows}</table></section>
<section class=card><form method=post action=/admin/user><h3>Добавить пользователя</h3>
<div class=grid><label>Имя <input name=name required></label><label>Пользовательские CIDR, через запятую <input name=networks required></label>
<label>IP BGP peer <input name=peer_ip required></label><label>ASN peer <input type=number name=peer_asn required></label>
<label>Next hop для анонсов (необязательно) <input name=next_hop></label><label>BGP MD5 пароль (необязательно) <input type=password name=bgp_password></label></div>
<button>Добавить</button></form></section>"""
        self.send_html(page("Админка", body))

    def add_feed(self) -> None:
        form = parse_form(self)
        with transaction(CONFIG.db_path) as db:
            db.execute("INSERT INTO feeds(name, url) VALUES (?, ?)", (first(form, "name"), first(form, "url")))
        self.redirect("/admin")

    def sync(self) -> None:
        sync_all(CONFIG.db_path)
        bird.reload(CONFIG)
        self.redirect("/admin")

    def add_user(self) -> None:
        form = parse_form(self)
        data = user_data_from_form(form)
        with transaction(CONFIG.db_path) as db:
            cursor = db.execute(
                "INSERT INTO users(name, peer_ip, peer_asn, next_hop, bgp_password) VALUES (?, ?, ?, ?, ?)",
                (data["name"], data["peer_ip"], data["peer_asn"], data["next_hop"], first(form, "bgp_password") or None),
            )
            db.executemany("INSERT INTO user_networks(user_id, cidr) VALUES (?, ?)", [(cursor.lastrowid, n) for n in data["networks"]])
        bird.reload(CONFIG)
        self.redirect("/admin")

    def admin_user_page(self, user_id: int) -> None:
        with contextlib.closing(connect(CONFIG.db_path)) as db:
            user = db.execute("SELECT * FROM users WHERE id = ?", (user_id,)).fetchone()
            if not user:
                self.send_error(404)
                return
            networks = ", ".join(
                row["cidr"]
                for row in db.execute("SELECT cidr FROM user_networks WHERE user_id = ? ORDER BY cidr", (user_id,))
            )
            categories, services = user_selection(db, user_id)
            bgp_password_note = "Пароль задан. Оставьте поле пустым, чтобы не менять его."
            settings = f"""<header><h1>Пользователь {esc(user['name'])}</h1><a href=/admin>Админка</a></header>
<section class=card><h2>Параметры пользователя</h2>
<form method=post action=/admin/user/{user_id}>
<input type=hidden name=action value=settings>
<div class=grid>
<label>Имя <input name=name value="{esc(user['name'])}" required></label>
<label>Пользовательские CIDR, через запятую <input name=networks value="{esc(networks)}" required></label>
<label>IP BGP peer <input name=peer_ip value="{esc(user['peer_ip'])}" required></label>
<label>ASN peer <input type=number name=peer_asn value="{esc(user['peer_asn'])}" required></label>
<label>Next hop для анонсов (необязательно) <input name=next_hop value="{esc(user['next_hop'] or '')}"></label>
<label>BGP MD5 пароль <input type=password name=bgp_password placeholder="{esc(bgp_password_note if user['bgp_password'] else 'Не задан')}"></label>
</div>
<label><input type=checkbox name=clear_bgp_password> очистить BGP MD5 пароль</label>
<label><input type=checkbox name=enabled{" checked" if user["enabled"] else ""}> пользователь включён</label>
<label><input type=checkbox name=locked{" checked" if user["selection_locked"] else ""}> запретить пользователю менять выбор</label>
<div class=row-actions><button>Сохранить параметры</button></div>
</form>
<form method=post action=/admin/user/{user_id}/delete onsubmit="return confirm('Удалить пользователя? Это также удалит его выбор сервисов.');">
<button class=danger>Удалить пользователя</button>
</form></section>"""
            body = settings + selection_form(db, user, categories, services, True, "", admin=True, include_header=False)
        self.send_html(page(f"Пользователь {user['name']}", body))

    def save_admin_user(self, user_id: int) -> None:
        form = parse_form(self)
        if first(form, "action") == "settings":
            if not self.save_admin_user_settings(user_id, form):
                return
        else:
            categories, services = selection_from_form(form)
            with transaction(CONFIG.db_path) as db:
                set_user_selection(db, user_id, categories, services)
        bird.reload(CONFIG)
        self.redirect(f"/admin/user/{user_id}")

    def save_admin_user_settings(self, user_id: int, form: dict[str, list[str]]) -> bool:
        data = user_data_from_form(form)
        password = first(form, "bgp_password")
        with transaction(CONFIG.db_path) as db:
            user = db.execute("SELECT * FROM users WHERE id = ?", (user_id,)).fetchone()
            if not user:
                self.send_error(404)
                return False
            bgp_password = None if "clear_bgp_password" in form else user["bgp_password"]
            if password:
                bgp_password = password
            db.execute(
                """
                UPDATE users
                SET name = ?, peer_ip = ?, peer_asn = ?, next_hop = ?, bgp_password = ?,
                    selection_locked = ?, enabled = ?
                WHERE id = ?
                """,
                (
                    data["name"],
                    data["peer_ip"],
                    data["peer_asn"],
                    data["next_hop"],
                    bgp_password,
                    1 if "locked" in form else 0,
                    1 if "enabled" in form else 0,
                    user_id,
                ),
            )
            db.execute("DELETE FROM user_networks WHERE user_id = ?", (user_id,))
            db.executemany("INSERT INTO user_networks(user_id, cidr) VALUES (?, ?)", [(user_id, n) for n in data["networks"]])
        return True

    def delete_admin_user(self, user_id: int) -> None:
        with transaction(CONFIG.db_path) as db:
            db.execute("DELETE FROM users WHERE id = ?", (user_id,))
        bird.reload(CONFIG)
        self.redirect("/admin")


def selection_from_form(form: dict[str, list[str]]) -> tuple[set[str], set[tuple[str, str]]]:
    categories = {value for value in form.get("category", []) if value}
    services: set[tuple[str, str]] = set()
    for value in form.get("service", []):
        if ":" in value:
            category, service = value.split(":", 1)
            services.add((urllib.parse.unquote(category), urllib.parse.unquote(service)))
    return categories, services


def user_data_from_form(form: dict[str, list[str]]) -> dict[str, object]:
    networks = [normalize_cidr(v) for v in first(form, "networks").split(",") if v.strip()]
    peer_address = ipaddress.ip_address(first(form, "peer_ip"))
    next_hop_raw = first(form, "next_hop")
    next_hop_address = ipaddress.ip_address(next_hop_raw) if next_hop_raw else None
    return {
        "name": first(form, "name"),
        "networks": networks,
        "peer_ip": str(peer_address),
        "peer_asn": int(first(form, "peer_asn")),
        "next_hop": str(next_hop_address) if next_hop_address else None,
    }


def selection_form(
    db, user, selected_categories: set[str], selected_services: set[tuple[str, str]],
    editable: bool, message: str, admin: bool = False, include_header: bool = True
) -> str:
    disabled = "" if editable else " disabled"
    fields = []
    for category, services in catalog(db).items():
        service_fields = "".join(
            f'<label><input type=checkbox name=service value="{esc(urllib.parse.quote(category, safe=""))}:{esc(urllib.parse.quote(service, safe=""))}"'
            f'{" checked" if (category, service) in selected_services else ""}{disabled}> {esc(service)}</label>'
            for service in services
        )
        fields.append(
            f"<fieldset class=category-card><legend><label class=category-title><input type=checkbox name=category value=\"{esc(category)}\""
            f"{' checked' if category in selected_categories else ''}{disabled}> <strong>{esc(category)}</strong> <span class=pill>целиком</span></label></legend>"
            f"<div class=service-list>{service_fields}</div></fieldset>"
        )
    action = f"/admin/user/{user['id']}" if admin else "/selection"
    note = "<p class=muted>Выбор заблокирован администратором.</p>" if not editable else ""
    header = f"<header><h1>{esc(user['name'])}</h1><a href=/admin>Админка</a></header>" if include_header else ""
    selected_count = len(selected_categories) + len(selected_services)
    save_label = "Сохранить выбор" if admin else "Сохранить маршруты"
    hidden_action = "<input type=hidden name=action value=selection>" if admin else ""
    catalog_body = f"<div class=catalog-grid>{''.join(fields)}</div>" if fields else '<p class=empty>Каталог пока пуст.</p>'
    return f"""{header}<section class=card><h2>Выбор сервисов</h2>
<p class=muted>Категория целиком включает также сервисы, которые появятся в ней позже.</p>{note}
<form class=selection-form method=post action={action}>{hidden_action}
<div class=save-bar><div><strong>{esc(user['name'])}</strong><br><span class=muted>Выбрано: {selected_count}</span></div><button{disabled}>{save_label}</button></div>
{catalog_body}
</form></section>"""


def sync_loop() -> None:
    while True:
        sync_all(CONFIG.db_path)
        bird.reload(CONFIG)
        time.sleep(CONFIG.sync_interval)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("command", choices=["init", "serve", "sync", "render-bird", "stats"])
    args = parser.parse_args()
    init(CONFIG.db_path)
    if args.command == "init":
        bird.write_config(CONFIG)
    elif args.command == "sync":
        errors = sync_all(CONFIG.db_path)
        ok, output = bird.reload(CONFIG)
        if errors:
            raise SystemExit("\n".join(errors))
        if not ok:
            raise SystemExit(output)
    elif args.command == "render-bird":
        print(bird.render(CONFIG), end="")
    elif args.command == "stats":
        with contextlib.closing(connect(CONFIG.db_path)) as db:
            categories = db.execute("SELECT COUNT(DISTINCT category) FROM catalog_entries").fetchone()[0]
            services = db.execute("SELECT COUNT(DISTINCT service) FROM catalog_entries").fetchone()[0]
            entries = db.execute("SELECT COUNT(*) FROM catalog_entries").fetchone()[0]
            print(f"catalog: {categories} categories, {services} services, {entries} entries")
            for feed in db.execute("SELECT name, last_success, last_error FROM feeds ORDER BY id"):
                status = feed["last_success"] or "never"
                if feed["last_error"]:
                    status = f"error: {feed['last_error']}"
                print(f"{feed['name']}: {status}")
    elif args.command == "serve":
        if not CONFIG.admin_password or not CONFIG.session_secret:
            raise SystemExit("WDBGP_ADMIN_PASSWORD and WDBGP_SESSION_SECRET are required")
        threading.Thread(target=sync_loop, daemon=True).start()
        ThreadingHTTPServer((CONFIG.host, CONFIG.port), Handler).serve_forever()
