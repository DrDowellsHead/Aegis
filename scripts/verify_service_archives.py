"""Inspect built archives without extracting or running service installers."""
import sys
import tarfile
from pathlib import Path
from zipfile import ZipFile

def verify(directory: Path) -> None:
    for system in ("linux", "darwin", "windows"):
        for arch in ("amd64", "arm64"):
            archive = next(iter(sorted(directory.glob(f"aegis_*_{system}_{arch}.*"))), None)
            if archive is None:
                raise RuntimeError(f"missing {system}/{arch} archive")
            if system == "windows":
                with ZipFile(archive) as z:
                    names = set(z.namelist())
                needed = {"install-service.ps1", "remove-service.ps1"}
                service_dir = "SERVICE/"
            else:
                with tarfile.open(archive, "r:gz") as z:
                    names = set(z.getnames())
                needed = (
                    {"aegis-agent.service", "aegis-receiver.service"} if system == "linux"
                    else {"com.aegis.agent.plist", "com.aegis.receiver.plist"}
                )
                service_dir = "SERVICE/"
            prefix = archive.name.split(f"_{system}_{arch}")[0] + f"_{system}_{arch}/"
            for expected in {"SERVICES.md", "CONFIGURATION.md"} | {
                service_dir + name for name in needed
            }:
                if prefix + expected not in names:
                    raise RuntimeError(f"{archive.name}: missing {expected}")

if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: verify_service_archives.py dist/dev")
    verify(Path(sys.argv[1]))
    print("Six platform archives contain service files and documentation")
