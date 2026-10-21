"""Build portable release archives with SHA-256 checksums (Python 3, Go required)."""
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import zipfile


def main():
    version = sys.argv[1] if len(sys.argv) == 2 else ""
    if not re.fullmatch(r"v\d+\.\d+\.\d+(?:-(?:alpha|beta|rc)\.\d+)?|dev", version):
        raise SystemExit("usage: python scripts/package.py v0.1.0-alpha.1 (or dev)")
    root = Path(__file__).resolve().parents[1]
    output = root / "dist" / version
    # Never mix assets from different invocations or overwrite a release silently.
    output.mkdir(parents=True, exist_ok=False)
    subprocess.run(["go", "mod", "download", "all"], cwd=root, check=True)
    module_data = subprocess.check_output(["go", "list", "-m", "-json", "all"], cwd=root, text=True)
    decoder = json.JSONDecoder()
    licenses = []
    while module_data.strip():
        module, end = decoder.raw_decode(module_data.lstrip())
        module_data = module_data.lstrip()[end:]
        if module.get("Main"):
            continue
        directory = Path(module["Dir"])
        files = [p for p in directory.iterdir() if p.is_file() and
                 p.name.upper().startswith(("LICENSE", "COPYING", "NOTICE"))]
        if not files:
            raise RuntimeError(f"No license found for {module['Path']}")
        folder = module["Path"].replace("/", "_") + "@" + module["Version"]
        licenses.extend((folder, p) for p in files)
    checksums = []
    for system in ("linux", "darwin", "windows"):
        for arch in ("amd64", "arm64"):
            name = f"aegis_{version}_{system}_{arch}"
            with tempfile.TemporaryDirectory() as directory:
                stage = Path(directory) / name
                stage.mkdir()
                binary = stage / ("aegis.exe" if system == "windows" else "aegis")
                env = dict(os.environ, GOOS=system, GOARCH=arch, CGO_ENABLED="0")
                subprocess.run(
                    ["go", "build", "-trimpath", "-ldflags", f"-s -w -X main.version={version}",
                     "-o", str(binary), "./cmd/aegis"], cwd=root, env=env, check=True,
                )
                shutil.copy2(root / "README.md", stage)
                shutil.copy2(root / "LICENSE", stage)
                shutil.copy2(root / "NOTICE", stage)
                shutil.copy2(root / "docs" / "installation.md", stage / "INSTALL.md")
                shutil.copy2(root / "docs" / "metrics.md", stage / "METRICS.md")
                shutil.copy2(root / "docs" / "configuration.md", stage / "CONFIGURATION.md")
                shutil.copy2(root / "configs" / "aegis.example.json", stage / "aegis.example.json")
                for folder, path in licenses:
                    destination = stage / "THIRD_PARTY_LICENSES" / folder
                    destination.mkdir(parents=True, exist_ok=True)
                    shutil.copy2(path, destination / path.name)
                if system == "windows":
                    archive = output / f"{name}.zip"
                    with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED) as bundle:
                        for path in sorted(stage.rglob("*")):
                            if path.is_file():
                                bundle.write(path, f"{name}/{path.relative_to(stage).as_posix()}")
                else:
                    archive = output / f"{name}.tar.gz"
                    with tarfile.open(archive, "w:gz") as bundle:
                        bundle.add(stage, arcname=name)
                checksums.append(f"{hashlib.sha256(archive.read_bytes()).hexdigest()}  {archive.name}\n")
                print(archive.name, flush=True)
    (output / "SHA256SUMS").write_text("".join(checksums), encoding="utf-8")


if __name__ == "__main__":
    main()
