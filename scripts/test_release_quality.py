"""Release chain security policy checks; stdlib-only, used in CI."""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]
SHA = re.compile(r"^\s*-\s*uses:\s+([a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+)@([0-9a-f]{40})\s*$", re.M)
ALL_USES = re.compile(r"^\s*-\s*uses:\s+(\S+)", re.M)

def verify_policy(workflow: str, release: bool) -> list[str]:
    problems = []
    if "permissions:\n  contents: read" not in workflow:
        problems.append("default GITHUB_TOKEN permissions must be contents: read")
    if re.search(r"^\s*pull_request_target\s*:", workflow, re.M):
        problems.append("pull_request_target is not allowed")
    if "persist-credentials: true" in workflow:
        problems.append("checkout must not persist credentials")
    actions = ALL_USES.findall(workflow)
    if not actions:
        problems.append("workflow must use an audited pinned checkout action")
    for action in actions:
        match = re.fullmatch(r"([a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+)@[0-9a-f]{40}", action)
        if not match:
            problems.append(f"un-pinned action: {action}")
    if release:
        if "needs: [test, security]" not in workflow:
            problems.append("release draft must require tests and dependency audit")
        if not re.search(r"(?m)^  draft:\n(?:.*\n)*?    permissions:\n      contents: write\n", workflow):
            problems.append("release writer must be restricted to draft job")
        if not re.search(r"(?m)^  security:", workflow):
            problems.append("release dependency scanning job is required")
    else:
        for step in ("go mod verify", "govulncheck", "npm audit", "sha256sum -c SHA256SUMS"):
            if step not in workflow:
                problems.append(f"CI missing check: {step}")
    return problems

class ReleaseQualityPolicyTests(unittest.TestCase):
    def test_ci_security_policy(self):
        text = (ROOT / ".github/workflows/ci.yml").read_text(encoding="utf-8")
        self.assertEqual(verify_policy(text, False), [])

    def test_tag_release_security_policy(self):
        text = (ROOT / ".github/workflows/release.yml").read_text(encoding="utf-8")
        self.assertEqual(verify_policy(text, True), [])

    def test_unpinned_actions_fail_closed(self):
        text = "permissions:\n  contents: read\nsteps:\n  - uses: actions/checkout@v6\n"
        self.assertTrue(any("un-pinned" in msg for msg in verify_policy(text, False)))

    def test_full_sha_pin_is_required(self):
        self.assertTrue(SHA.fullmatch("  - uses: actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803"))

if __name__ == "__main__":
    unittest.main()
