"""Build portable release archives with SHA-256 checksums (Python 3, Go required)."""
import hashlib
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
                shutil.copy2(root / "docs" / "installation.md", stage / "INSTALL.md")
                if system == "windows":
                    archive = output / f"{name}.zip"
                    with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED) as bundle:
                        for path in sorted(stage.iterdir()):
                            bundle.write(path, f"{name}/{path.name}")
                else:
                    archive = output / f"{name}.tar.gz"
                    with tarfile.open(archive, "w:gz") as bundle:
                        bundle.add(stage, arcname=name)
                checksums.append(f"{hashlib.sha256(archive.read_bytes()).hexdigest()}  {archive.name}\n")
                print(archive.name, flush=True)
    (output / "SHA256SUMS").write_text("".join(checksums), encoding="utf-8")


if __name__ == "__main__":
    main()
