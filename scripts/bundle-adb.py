#!/usr/bin/env python3
"""Stage official Android platform-tools for AirDroid's platform resources."""
import argparse
import hashlib
import io
import json
from pathlib import Path
import shutil
import urllib.request
import urllib.error
import zipfile

ROOT = Path(__file__).resolve().parent.parent
SOURCES = {
    "darwin": "https://dl.google.com/android/repository/platform-tools-latest-darwin.zip",
    "windows": "https://dl.google.com/android/repository/platform-tools-latest-windows.zip",
    "linux": "https://dl.google.com/android/repository/platform-tools-latest-linux.zip",
}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("platform", choices=SOURCES)
    parser.add_argument("--from-sdk", type=Path, help="Use an existing platform-tools directory for this OS")
    parser.add_argument("--archive", type=Path, help="Use an already downloaded official platform-tools ZIP")
    parser.add_argument("--sha256", help="Require this SHA-256 when downloading a pinned release")
    parser.add_argument("--url", help="Override with a pinned official Google platform-tools ZIP URL")
    args = parser.parse_args()
    dest = ROOT / "resources" / args.platform / "platform-tools"
    dest.mkdir(parents=True, exist_ok=True)
    if args.platform == "windows":
        required = {"adb.exe", "AdbWinApi.dll", "AdbWinUsbApi.dll", "NOTICE.txt", "source.properties"}
    else:
        required = {"adb", "NOTICE.txt", "source.properties"}
    # Include support libraries: newer ADB versions can load them dynamically.
    def wanted(name):
        return name in required or name.startswith("lib64/") or name.lower().endswith((".dll", ".dylib", ".so"))
    if args.from_sdk:
        source = args.from_sdk.resolve()
        for name in required:
            if not (source / name).is_file():
                parser.error(f"Missing {source / name}")
        for path in source.rglob("*"):
            if path.is_file() and wanted(path.relative_to(source).as_posix()):
                target = dest / path.relative_to(source)
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(path, target)
        origin = {"source": "local Android SDK", "version": (dest / "source.properties").read_text()}
    else:
        url = args.url or SOURCES[args.platform]
        if not url.startswith("https://dl.google.com/android/repository/platform-tools-"):
            parser.error("URL must point to Google's official platform-tools repository")
        if args.archive:
            data = args.archive.read_bytes()
        else:
            try:
                response = urllib.request.urlopen(url, timeout=90)
            except urllib.error.URLError:
                # Google's alternate official download endpoint can be reachable
                # on networks where the direct host is unavailable.
                url = url.replace("https://dl.google.com/android/", "https://redirector.gvt1.com/edgedl/android/")
                response = urllib.request.urlopen(url, timeout=90)
            with response:
                data = response.read(100 * 1024 * 1024 + 1)
        if len(data) > 100 * 1024 * 1024:
            raise ValueError("Archive is unexpectedly large")
        digest = hashlib.sha256(data).hexdigest()
        if args.sha256 and digest.lower() != args.sha256.lower():
            raise ValueError("Archive SHA-256 mismatch")
        copied = set()
        with zipfile.ZipFile(io.BytesIO(data)) as archive:
            for entry in archive.infolist():
                relative = Path(entry.filename)
                if relative.is_absolute() or ".." in relative.parts or not entry.filename.startswith("platform-tools/"):
                    continue
                name = entry.filename.removeprefix("platform-tools/")
                if entry.is_dir() or not wanted(name):
                    continue
                if entry.file_size > 100 * 1024 * 1024:
                    raise ValueError("Archive entry is unexpectedly large")
                target = dest / name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(archive.read(entry))
                copied.add(name)
        if not required.issubset(copied):
            raise ValueError(f"Incomplete archive: missing {required - copied}")
        origin = {"source": "local platform-tools archive" if args.archive else url, "archiveSHA256": digest, "version": (dest / "source.properties").read_text()}
    if args.platform != "windows":
        (dest / "adb").chmod(0o755)
    (dest / "origin.json").write_text(json.dumps(origin, indent=2) + "\n")
    print(f"Staged ADB in {dest}")


if __name__ == "__main__":
    main()
