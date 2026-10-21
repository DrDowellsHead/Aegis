"""Static service template checks; safe in CI, no host service mutations."""
import configparser
from pathlib import Path
import plistlib
import unittest

ROOT = Path(__file__).resolve().parents[1]

class ServiceTemplateTests(unittest.TestCase):
    def test_linux_unit_hardening(self):
        for component in ("agent", "receiver"):
            with self.subTest(component=component):
                p = ROOT / "deploy" / "systemd" / f"aegis-{component}.service"
                parser = configparser.ConfigParser(interpolation=None)
                parser.optionxform = str
                with p.open(encoding="utf-8") as source:
                    parser.read_file(source)
                unit = parser["Service"]
                self.assertEqual(unit["User"], "aegis")
                self.assertEqual(unit["Group"], "aegis")
                self.assertEqual(unit["Restart"], "on-failure")
                self.assertEqual(unit["NoNewPrivileges"], "true")
                self.assertEqual(unit["ProtectSystem"], "strict")
                self.assertEqual(unit["UMask"], "0077")
                self.assertIn("start --config", unit["ExecStart"])
                self.assertIn("--component " + component, unit["ExecStart"])
                self.assertIn("TimeoutStopSec", unit)
                self.assertNotIn("sudo ", unit["ExecStart"])
                if component == "receiver":
                    self.assertIn("AEGIS_DATABASE_URL_FILE=", unit["Environment"])
                    self.assertNotIn("postgres://", unit["Environment"])

    def test_launchd_plists_are_valid_private_daemons(self):
        for component in ("agent", "receiver"):
            with self.subTest(component=component):
                path = ROOT / "deploy" / "launchd" / f"com.aegis.{component}.plist"
                with path.open("rb") as fh:
                    data = plistlib.load(fh)
                self.assertEqual(data["Label"], "com.aegis." + component)
                self.assertEqual(data["UserName"], "aegis")
                self.assertEqual(data["GroupName"], "aegis")
                self.assertTrue(data["RunAtLoad"])
                self.assertEqual(data["KeepAlive"], {"SuccessfulExit": False})
                self.assertGreaterEqual(data["ThrottleInterval"], 10)
                self.assertEqual(data["ProgramArguments"][-1], component)
                self.assertIn("--config", data["ProgramArguments"])
                env = data.get("EnvironmentVariables", {})
                self.assertNotIn("AEGIS_DATABASE_URL", env)
                if component == "receiver":
                    self.assertIn("AEGIS_DATABASE_URL_FILE", env)

    def test_windows_scripts_require_restricted_identity(self):
        install = (ROOT / "deploy" / "windows" / "install-service.ps1").read_text()
        remove = (ROOT / "deploy" / "windows" / "remove-service.ps1").read_text()
        for component in ("agent", "receiver"):
            self.assertIn(component, install)
        self.assertIn("#Requires -RunAsAdministrator", install)
        self.assertIn("[System.Management.Automation.PSCredential]$Credential", install)
        self.assertIn("New-Service", install)
        self.assertIn("-Credential $Credential", install)
        self.assertIn("Get-Service", install)
        self.assertIn("sc.exe failure", install)
        self.assertIn("validate-config", install)
        self.assertIn("Stop-Service", remove)
        self.assertIn("sc.exe delete", remove)
        self.assertNotIn("Remove-Item", remove)

if __name__ == "__main__":
    unittest.main()
