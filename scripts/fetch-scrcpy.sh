#!/bin/sh
# Downloads scrcpy's official macOS releases and puts what MyGo ships into
# resources/, where `mygo build` bundles it into the app:
#
#   resources/darwin-arm64/bin/scrcpy         Apple silicon
#   resources/darwin-amd64/bin/scrcpy         Intel
#   resources/darwin/bin/adb                  adb, a universal binary already
#   resources/darwin/scrcpy-server            the server pushed to devices
#   resources/darwin/scrcpy-server.version    its version, which it checks
#
# A universal build (darwin/universal) joins the two architectures with
# lipo. The archives are checked against the SHA-256 sums below.
set -eu

VERSION="${SCRCPY_VERSION:-5.0.1}"
SHA_AARCH64="33611e51977a8289e2e124b220f727895181ef2eb9060e0987a4dc2db9cf0d6b"
SHA_X86_64="31a5467a9e907f162b093267cbbab1276b15c6e709c2ea8159566a8d165beb50"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RES="$ROOT/resources"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

fetch() { # arch-in-release, goarch, sha
	name="scrcpy-macos-$1-v$VERSION"
	url="https://github.com/Genymobile/scrcpy/releases/download/v$VERSION/$name.tar.gz"
	echo "Downloading $url"
	curl -fsSL -o "$WORK/$name.tar.gz" "$url"
	if [ "$VERSION" = "5.0.1" ]; then
		got="$(sha256 "$WORK/$name.tar.gz")"
		if [ "$got" != "$3" ]; then
			echo "SHA-256 mismatch for $name: $got" >&2
			exit 1
		fi
	fi
	tar -xzf "$WORK/$name.tar.gz" -C "$WORK"
	mkdir -p "$RES/darwin-$2/bin" "$RES/darwin/bin"
	install -m 755 "$WORK/$name/scrcpy" "$RES/darwin-$2/bin/scrcpy"
	# Google's adb for macOS serves both architectures: one copy, which
	# a universal build must not join with itself.
	install -m 755 "$WORK/$name/adb" "$RES/darwin/bin/adb"
	install -m 644 "$WORK/$name/scrcpy-server" "$RES/darwin/scrcpy-server"
	printf '%s\n' "$VERSION" > "$RES/darwin/scrcpy-server.version"
	install -m 644 "$WORK/$name/LICENSE" "$RES/darwin/scrcpy-LICENSE.txt"
}

fetch aarch64 arm64 "$SHA_AARCH64"
fetch x86_64 amd64 "$SHA_X86_64"
echo "scrcpy $VERSION and adb are in $RES"
