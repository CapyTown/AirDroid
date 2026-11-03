#!/usr/bin/env python3
"""Stage checksum-pinned official scrcpy portable releases for AirDroid's platform resources."""
import argparse
import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import shutil
import stat
import tarfile
import tempfile
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parent.parent
VERSION = "5.0.1"
BASE_URL = f"https://github.com/Genymobile/scrcpy/releases/download/v{VERSION}/"
# Published by upstream in doc/macos.md, doc/windows.md and doc/linux.md at
# tag v5.0.1, and in the release's SHA256SUMS.txt.
ARCHIVES = {
    "darwin": {
        "arm64": ("scrcpy-macos-aarch64-v5.0.1.tar.gz", "33611e51977a8289e2e124b220f727895181ef2eb9060e0987a4dc2db9cf0d6b"),
        "amd64": ("scrcpy-macos-x86_64-v5.0.1.tar.gz", "31a5467a9e907f162b093267cbbab1276b15c6e709c2ea8159566a8d165beb50"),
    },
    "windows": {
        "amd64": ("scrcpy-win64-v5.0.1.zip", "b12a2c4ee8be317422451fc7dcf8ee20a71b5ea7ef9ad73ddd825a25316227a5"),
    },
    "linux": {
        "amd64": ("scrcpy-linux-x86_64-v5.0.1.tar.gz", "9f969d30cc574816077edecda65719c1c70b0aee1df1ba5fa00779ac07b8fd6e"),
    },
}
MAX_BYTES = 200 * 1024 * 1024


def relative_path(name, prefix):
    path = PurePosixPath(name)
    if ("\\" in name or ":" in name or path.is_absolute()
            or ".." in path.parts or not path.parts or path.parts[0] != prefix):
        raise ValueError(f"Unsafe archive path: {name}")
    return Path(*path.parts[1:])


def extract(data, filename, destination):
    """Extract only regular files/directories; these official bundles have no links."""
    prefix = filename.removesuffix(".tar.gz").removesuffix(".zip")
    total = 0
    seen = set()

    def write(name, size, mode, stream):
        nonlocal total
        relative = relative_path(name, prefix)
        total += size
        if relative == Path(".") or relative in seen or size < 0 or total > MAX_BYTES:
            raise ValueError(f"Invalid or oversized archive entry: {name}")
        seen.add(relative)
        target = destination / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        with target.open("xb") as output:
            shutil.copyfileobj(stream, output)
        target.chmod(0o755 if mode & 0o111 else 0o644)

    if filename.endswith(".zip"):
        with zipfile.ZipFile(io.BytesIO(data)) as archive:
            for entry in archive.infolist():
                relative_path(entry.filename, prefix)
                mode = entry.external_attr >> 16
                if stat.S_IFMT(mode) not in (0, stat.S_IFREG, stat.S_IFDIR):
                    raise ValueError(f"Unsupported archive entry: {entry.filename}")
                if entry.is_dir():
                    continue
                with archive.open(entry) as stream:
                    write(entry.filename, entry.file_size, mode, stream)
    else:
        with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as archive:
            for entry in archive:
                relative_path(entry.name, prefix)
                if entry.isdir():
                    continue
                if not entry.isfile():
                    raise ValueError(f"Unsupported archive entry: {entry.name}")
                with archive.extractfile(entry) as stream:
                    write(entry.name, entry.size, entry.mode, stream)


def stage(platform, architectures, local_archives):
    parent = ROOT / "resources" / platform
    parent.mkdir(parents=True, exist_ok=True)
    # Finish every architecture before replacing an existing installation.
    with tempfile.TemporaryDirectory(prefix=".scrcpy-", dir=parent) as temporary:
        staged = Path(temporary) / "scrcpy"
        staged.mkdir()
        for architecture in architectures:
            filename, expected_hash = ARCHIVES[platform][architecture]
            url = BASE_URL + filename
            source = local_archives.get(filename)
            if source:
                with source.open("rb") as stream:
                    data = stream.read(MAX_BYTES + 1)
            else:
                with urllib.request.urlopen(url, timeout=90) as response:
                    data = response.read(MAX_BYTES + 1)
            if len(data) > MAX_BYTES:
                raise ValueError(f"Archive is unexpectedly large: {filename}")
            digest = hashlib.sha256(data).hexdigest()
            if digest != expected_hash:
                raise ValueError(f"Archive SHA-256 mismatch: {filename}")
            destination = staged / architecture if platform == "darwin" else staged
            destination.mkdir(exist_ok=True)
            extract(data, filename, destination)
            required = {
                "scrcpy.exe", "scrcpy-server", "LICENSE.txt", "SDL3.dll", "libusb-1.0.dll",
                "avcodec-63.dll", "avformat-63.dll", "avutil-61.dll", "swresample-7.dll",
            } if platform == "windows" else {"scrcpy", "scrcpy-server", "LICENSE"}
            missing = {name for name in required if not (destination / name).is_file()}
            if missing:
                raise ValueError(f"Incomplete archive: missing {sorted(missing)}")
            if platform != "windows":
                (destination / "scrcpy").chmod(0o755)
            (destination / "origin.json").write_text(json.dumps({
                "version": VERSION, "architecture": architecture,
                "source": url, "archiveSHA256": digest,
            }, indent=2) + "\n", encoding="utf-8")
        destination = parent / "scrcpy"
        if destination.exists():
            shutil.rmtree(destination)
        staged.rename(destination)
    print(f"Staged scrcpy {VERSION} ({', '.join(architectures)}) in {destination}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("platform", choices=ARCHIVES)
    parser.add_argument("--archive", type=Path, action="append", default=[],
                        help="Use a local official archive with its original filename; repeat for both Mac architectures")
    parser.add_argument("--arch", action="append", default=[],
                        help="Stage only this processor's scrcpy (arm64, amd64); repeatable. "
                             "By default every one of the platform's: both on macOS, for a universal app")
    args = parser.parse_args()
    architectures = args.arch or list(ARCHIVES[args.platform])
    unknown = [a for a in architectures if a not in ARCHIVES[args.platform]]
    if unknown:
        parser.error(f"No scrcpy for {args.platform}/{', '.join(unknown)}: choose from {', '.join(ARCHIVES[args.platform])}")
    architectures = list(dict.fromkeys(architectures))
    expected = {ARCHIVES[args.platform][a][0] for a in architectures}
    local = {path.name: path for path in args.archive}
    if len(local) != len(args.archive) or not local.keys() <= expected:
        parser.error("Local archives must have unique, expected official filenames")
    if local and local.keys() != expected:
        parser.error("Provide every archive for this platform when using --archive")
    stage(args.platform, architectures, local)


if __name__ == "__main__":
    main()
