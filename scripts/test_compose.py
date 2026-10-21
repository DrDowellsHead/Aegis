"""Verify the normalized Docker Compose model without starting services.

Requires Docker Compose v2. This checks the actual effective YAML model,
not fragile line-based matching against the source file.
"""
import json
import subprocess
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DIR = ROOT / "deploy" / "compose"


def model():
    proc = subprocess.run(
        ["docker", "compose", "--env-file", str(DIR / ".env.example"),
         "--profile", "notifications", "--profile", "dashboard",
         "-f", str(DIR / "compose.yaml"), "config", "--format", "json"],
        cwd=DIR, capture_output=True, text=True, check=True,
    )
    return json.loads(proc.stdout)


class ComposeSecurityTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.config = model()
        cls.services = cls.config["services"]

    def test_expected_service_boundaries(self):
        self.assertEqual(set(self.services), {
            "postgres", "migrate", "receiver", "watch", "notify", "dashboard",
        })
        networks = self.config["networks"]
        self.assertTrue(networks["backend"]["internal"])
        self.assertEqual(set(self.services["postgres"]["networks"]), {"backend"})
        self.assertEqual(set(self.services["migrate"]["networks"]), {"backend"})
        self.assertEqual(set(self.services["watch"]["networks"]), {"backend"})
        self.assertEqual(set(self.services["notify"]["networks"]), {"backend", "outbound"})
        self.assertEqual(set(self.services["receiver"]["networks"]), {"backend", "edge"})
        self.assertEqual(set(self.services["dashboard"]["networks"]), {"backend", "edge"})

    def test_ports_and_profiles(self):
        for name in ("postgres", "migrate", "watch", "notify"):
            self.assertFalse(self.services[name].get("ports"), name)
        grpc = self.services["receiver"]["ports"]
        self.assertEqual(len(grpc), 1)
        self.assertEqual(str(grpc[0]["target"]), "9090")
        self.assertEqual(str(grpc[0]["published"]), "9090")
        self.assertEqual(grpc[0].get("host_ip"), "0.0.0.0")
        web = self.services["dashboard"]["ports"]
        self.assertEqual(len(web), 1)
        self.assertEqual(web[0]["host_ip"], "127.0.0.1")
        self.assertEqual(str(web[0]["target"]), "8443")
        self.assertEqual(self.services["dashboard"]["profiles"], ["dashboard"])
        self.assertEqual(self.services["notify"]["profiles"], ["notifications"])

    def test_immutable_secrets_and_least_privilege(self):
        services = self.services
        for name in ("migrate", "receiver", "watch", "notify", "dashboard"):
            svc = services[name]
            self.assertTrue(svc["read_only"], name)
            self.assertEqual(svc["user"], "10001:10001")
            self.assertEqual(svc["cap_drop"], ["ALL"])
            self.assertIn("no-new-privileges:true", svc["security_opt"])
            self.assertEqual(svc["environment"]["AEGIS_DATABASE_URL_FILE"], "/run/aegis/db-url")
            self.assertNotIn("AEGIS_DATABASE_URL", svc["environment"])
            mounts = svc["volumes"]
            self.assertTrue(any(m["target"] == "/run/aegis/db-url" and m["read_only"] for m in mounts))
            self.assertFalse(any("/var/run/docker.sock" in m["target"] for m in mounts))
        self.assertEqual(self.services["notify"]["environment"]["AEGIS_TELEGRAM_TOKEN_FILE"],
                         "/run/aegis/notify/telegram-token")
        for name in ("migrate", "receiver", "watch", "dashboard"):
            self.assertNotIn("AEGIS_TELEGRAM_TOKEN_FILE", services[name]["environment"])
        self.assertTrue(any(v["target"] == "/run/aegis/receiver" for v in services["receiver"]["volumes"]))
        for name in ("watch", "notify", "dashboard", "migrate"):
            self.assertFalse(any(v["target"] == "/run/aegis/receiver" for v in services[name]["volumes"]))
        self.assertTrue(any(v["target"] == "/var/lib/postgresql/data" for v in services["postgres"]["volumes"]))

    def test_migrations_precede_services(self):
        self.assertEqual(self.services["migrate"]["depends_on"]["postgres"]["condition"], "service_healthy")
        for name in ("receiver", "watch", "notify", "dashboard"):
            deps = self.services[name]["depends_on"]
            self.assertEqual(deps["postgres"]["condition"], "service_healthy")
            self.assertEqual(deps["migrate"]["condition"], "service_completed_successfully")
        self.assertEqual(self.services["migrate"]["restart"], "no")

    def test_dockerfile_and_secret_exclusion(self):
        df = (DIR / "Dockerfile").read_text(encoding="utf-8")
        self.assertIn("CGO_ENABLED=0", df)
        self.assertIn("USER 10001:10001", df)
        self.assertIn("npm ci", df)
        self.assertIn("COPY --from=web-build", df)
        ignore = (ROOT / ".dockerignore").read_text(encoding="utf-8")
        self.assertIn("deploy/compose/private", ignore)
        self.assertIn("deploy/compose/.env", ignore)
        self.assertNotIn("AEGIS_DATABASE_URL=", (DIR / ".env.example").read_text())


if __name__ == "__main__":
    unittest.main()
