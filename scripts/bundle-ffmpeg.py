#!/usr/bin/env python3
"""Stage the checksum-pinned FFmpeg that decodes the mirrored screen on
Windows and Linux.

macOS decodes with VideoToolbox, the system's hardware decoder, and needs
no FFmpeg: for darwin this script stages nothing.
"""
import argparse
import gzip
import hashlib
import io
import json
from pathlib import Path
import shutil
import tempfile
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
RELEASE = "b6.1.1"
BASE_URL = f"https://github.com/eugeneware/ffmpeg-static/releases/download/{RELEASE}/"
MAX_BYTES = 200 * 1024 * 1024
# SHA-256 of the pinned release's assets.
ASSETS = {
    "windows": {
        "asset": "win32-x64",
        "executable": "ffmpeg.exe",
        "binary": "8883a3dffbd0a16cf4ef95206ea05283f78908dbfb118f73c83f4951dcc06d77",
        "LICENSE": "8ceb4b9ee5adedde47b31e975c1d90c73ad27b6b165a1dcd80c7c545eb65b903",
        "README": "a636a7183c58006351acbaf35303c0ed85c6e1320fd4e80de453ba6157de6311",
        "builder": "https://www.gyan.dev/ffmpeg/builds/",
    },
    "linux": {
        "asset": "linux-x64",
        "executable": "ffmpeg",
        "binary": "bfe8a8fc511530457b528c48d77b5737527b504a3797a9bc4866aeca69c2dffa",
        "LICENSE": "8ceb4b9ee5adedde47b31e975c1d90c73ad27b6b165a1dcd80c7c545eb65b903",
        "README": "72f4b1b06d419d22ace6e7cc75f06826f90737345aa0b1736158929f4aacc537",
        "builder": "https://johnvansickle.com/ffmpeg/",
    },
}


def read_asset(name, digest, local_directory):
    if local_directory:
        stream = (local_directory / name).open("rb")
    else:
        stream = urllib.request.urlopen(BASE_URL + name, timeout=90)
    with stream:
        data = stream.read(MAX_BYTES + 1)
    if len(data) > MAX_BYTES:
        raise ValueError(f"Asset is unexpectedly large: {name}")
    if hashlib.sha256(data).hexdigest() != digest:
        raise ValueError(f"Asset SHA-256 mismatch: {name}")
    return data


def stage(platform, local_directory):
    pinned = ASSETS[platform]
    asset = pinned["asset"]
    parent = ROOT / "resources" / platform
    parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".ffmpeg-", dir=parent) as temporary:
        staged = Path(temporary) / "ffmpeg"
        staged.mkdir()
        filename = f"ffmpeg-{asset}.gz"
        data = read_asset(filename, pinned["binary"], local_directory)
        with gzip.GzipFile(fileobj=io.BytesIO(data)) as compressed:
            binary = compressed.read(MAX_BYTES + 1)
        if len(binary) > MAX_BYTES:
            raise ValueError(f"Expanded executable is unexpectedly large: {filename}")
        executable = staged / pinned["executable"]
        executable.write_bytes(binary)
        executable.chmod(0o755)
        for kind in ("LICENSE", "README"):
            (staged / f"{kind}.txt").write_bytes(read_asset(f"{asset}.{kind}", pinned[kind], local_directory))
        origin = {
            "release": RELEASE,
            "architecture": "amd64",
            "source": BASE_URL + filename,
            "archiveSHA256": pinned["binary"],
            "executableSHA256": hashlib.sha256(binary).hexdigest(),
            "builder": pinned["builder"],
            "buildRecipe": f"https://github.com/eugeneware/ffmpeg-static/blob/{RELEASE}/download-binaries/index.sh",
            "sourceCode": "https://ffmpeg.org/releases/ffmpeg-6.1.1.tar.xz",
        }
        (staged / "origin.json").write_text(json.dumps(origin, indent=2) + "\n", encoding="utf-8")
        (staged / "SOURCE.txt").write_text(
            "FFmpeg is a separate executable distributed under its own license (GPL v3).\n"
            "AirDroid runs it as a separate program to decode the mirrored screen.\n"
            "See LICENSE.txt and README.txt for upstream notices and build details.\n"
            f"Binary distribution: https://github.com/eugeneware/ffmpeg-static/releases/tag/{RELEASE}\n"
            f"Original binary builder: {pinned['builder']}\n"
            f"Build recipe: {origin['buildRecipe']}\n"
            f"FFmpeg source release: {origin['sourceCode']}\n",
            encoding="utf-8")
        destination = parent / "ffmpeg"
        if destination.exists():
            shutil.rmtree(destination)
        staged.rename(destination)
    print(f"Staged FFmpeg ({RELEASE}) in {destination}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("platform", choices=("darwin", "windows", "linux"))
    parser.add_argument("--from-directory", type=Path,
                        help="Read the pinned .gz binary and LICENSE/README assets from a local directory")
    args = parser.parse_args()
    if args.platform == "darwin":
        print("macOS decodes with VideoToolbox: no FFmpeg to bundle")
        return
    stage(args.platform, args.from_directory)


if __name__ == "__main__":
    main()
