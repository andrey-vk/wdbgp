import tempfile
import unittest

from wdbgp.config import Config
from wdbgp.db import find_user_by_ip, init, selected_prefixes, set_user_selection, transaction
from wdbgp.feeds import metadata_url, parse_category_lookup, parse_entries
from wdbgp.bird import render
from wdbgp.app import selection_form, user_data_from_form


class CoreTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.db_path = f"{self.tmp.name}/db.sqlite3"
        init(self.db_path)

    def tearDown(self):
        self.tmp.cleanup()

    def test_parse_entries_deduplicates_and_normalizes(self):
        payload = {"entries": [{"category": "ai", "service": "example", "cidrs": ["10.0.0.1/24", "10.0.0.0/24"]}]}
        self.assertEqual(parse_entries(payload), [("ai", "example", "10.0.0.0/24")])

    def test_parse_entries_rejects_ipv6_until_it_can_be_announced(self):
        payload = {"category": "ai", "service": "example", "cidrs": ["2001:db8::/32"]}
        with self.assertRaisesRegex(ValueError, "IPv6"):
            parse_entries(payload)

    def test_parse_opencck_entries_uses_group_and_cidr4(self):
        payload = {
            "chatgpt.com": {
                "name": "chatgpt.com",
                "group": "ai",
                "cidr4": ["104.18.0.1/16"],
                "cidr6": ["2001:db8::/32"],
            }
        }
        self.assertEqual(parse_entries(payload), [("ai", "chatgpt.com", "104.18.0.0/16")])

    def test_opencck_metadata_url_removes_data_parameter(self):
        url = "https://iplist.opencck.org/?format=json&data=cidr4"
        self.assertEqual(metadata_url(url), "https://iplist.opencck.org/?format=json&data=group")

    def test_beta_opencck_uses_compact_group_metadata(self):
        url = "https://beta.iplist.opencck.org/?format=json&data=cidr4"
        self.assertEqual(metadata_url(url), "https://beta.iplist.opencck.org/?format=json&data=group")

    def test_opencck_group_response_becomes_category_lookup(self):
        self.assertEqual(
            parse_category_lookup({"chatgpt.com": "ai", "netflix.com": "video"}),
            {"chatgpt.com": {"ai"}, "netflix.com": {"video"}},
        )

    def test_flat_opencck_entries_reuse_known_category(self):
        payload = {"chatgpt.com": ["104.18.0.1/16"], "new.example": ["192.0.2.1/24"]}
        self.assertEqual(
            parse_entries(payload, {"chatgpt.com": {"ai"}}, "opencck-beta"),
            [
                ("ai", "chatgpt.com", "104.18.0.0/16"),
                ("opencck-beta", "new.example", "192.0.2.0/24"),
            ],
        )

    def test_most_specific_user_network_wins(self):
        with transaction(self.db_path) as db:
            a = db.execute("INSERT INTO users(name, peer_ip, peer_asn) VALUES ('a', '192.0.2.1', 65001)").lastrowid
            b = db.execute("INSERT INTO users(name, peer_ip, peer_asn) VALUES ('b', '192.0.2.2', 65002)").lastrowid
            db.execute("INSERT INTO user_networks(user_id, cidr) VALUES (?, '10.0.0.0/8')", (a,))
            db.execute("INSERT INTO user_networks(user_id, cidr) VALUES (?, '10.1.0.0/16')", (b,))
            db.commit()
            self.assertEqual(find_user_by_ip(db, "10.1.2.3")["name"], "b")

    def test_category_and_service_selection_are_union(self):
        with transaction(self.db_path) as db:
            feed = db.execute("INSERT INTO feeds(name, url) VALUES ('f', 'file:///unused')").lastrowid
            db.executemany(
                "INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES (?, ?, ?, ?)",
                [(feed, "ai", "a", "10.0.0.0/24"), (feed, "video", "v", "10.0.1.0/24")],
            )
            user = db.execute("INSERT INTO users(name, peer_ip, peer_asn) VALUES ('u', '192.0.2.1', 65001)").lastrowid
            set_user_selection(db, user, {"ai"}, {("video", "v")})
            db.commit()
            self.assertEqual(selected_prefixes(db, user), ["10.0.0.0/24", "10.0.1.0/24"])

    def test_bird_config_has_per_user_filter(self):
        with transaction(self.db_path) as db:
            feed = db.execute("INSERT INTO feeds(name, url) VALUES ('f', 'file:///unused')").lastrowid
            db.execute("INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES (?, 'ai', 'a', '10.0.0.0/24')", (feed,))
            user = db.execute("INSERT INTO users(name, peer_ip, peer_asn, next_hop) VALUES ('u', '198.51.100.1', 65001, '198.51.100.254')").lastrowid
            set_user_selection(db, user, {"ai"}, set())
            db.commit()
        text = render(Config(db_path=self.db_path))
        self.assertIn("route 10.0.0.0/24 blackhole;", text)
        self.assertIn("neighbor 198.51.100.1 as 65001;", text)
        self.assertIn("next hop address 198.51.100.254;", text)

    def test_user_form_data_normalizes_addresses(self):
        data = user_data_from_form({
            "name": ["u"],
            "networks": ["10.0.0.1/24, 10.1.2.3/32"],
            "peer_ip": ["198.51.100.1"],
            "peer_asn": ["65001"],
            "next_hop": ["198.51.100.254"],
        })
        self.assertEqual(data["networks"], ["10.0.0.0/24", "10.1.2.3/32"])
        self.assertEqual(data["peer_ip"], "198.51.100.1")
        self.assertEqual(data["next_hop"], "198.51.100.254")

    def test_selection_form_has_sticky_save_bar(self):
        with transaction(self.db_path) as db:
            feed = db.execute("INSERT INTO feeds(name, url) VALUES ('f', 'file:///unused')").lastrowid
            db.execute("INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES (?, 'ai', 'openai', '10.0.0.0/24')", (feed,))
            user_id = db.execute("INSERT INTO users(name, peer_ip, peer_asn) VALUES ('u', '198.51.100.1', 65001)").lastrowid
            user = db.execute("SELECT * FROM users WHERE id = ?", (user_id,)).fetchone()
            html = selection_form(db, user, {"ai"}, set(), True, "")
        self.assertIn("class=save-bar", html)
        self.assertIn("class=category-card", html)
        self.assertIn("Сохранить маршруты", html)


if __name__ == "__main__":
    unittest.main()
