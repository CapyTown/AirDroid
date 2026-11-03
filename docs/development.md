# Development

## Requirements

- **Go 1.27.3 or later** (`GOTOOLCHAIN=auto` downloads it). AirDroid builds
  with `CGO_ENABLED=0`, so any of macOS, Windows and Linux builds the app of
  every platform.
- **Python 3**, for the scripts that stage adb, scrcpy and FFmpeg.
- **The platform itself** to run the app, and for its installer: a Mac for
  the disk image, NSIS for the Windows installer (`mygo build` downloads it
  on Windows).
- **Optional:** [ffmpeg](https://ffmpeg.org) with libx264, which the tests use
  to encode streams and check the movies AirDroid writes; without it those
  tests are skipped.

The MyGo CLI is a tool of the module: `go tool mygo` runs the version
`go.mod` pins.

## Commands

```sh
make deps    # adb, scrcpy and FFmpeg for this platform into resources/
make dev     # run AirDroid, rebuilt as its sources change (go tool mygo dev)
make build   # the app and its installer for this platform, in build/
make test    # go test ./...
make vet     # go vet for macOS, Windows and Linux, and mygo vet
```

`make deps` runs the three bundling scripts for the platform it is on;
`PLATFORM=windows make deps` stages another. Each script takes `darwin`,
`windows` or `linux`:

```sh
python3 scripts/bundle-adb.py linux       # Google's platform-tools
python3 scripts/bundle-scrcpy.py linux    # scrcpy 5.0.1, SHA-256 pinned
python3 scripts/bundle-ffmpeg.py linux    # FFmpeg 6.1.1, SHA-256 pinned (none for darwin)
```

They lay the files out where `mygo build` bundles them, one directory per
platform:

```
resources/darwin/platform-tools/adb
resources/darwin/scrcpy/arm64/{scrcpy,scrcpy-server,origin.json}   Apple silicon
resources/darwin/scrcpy/amd64/{scrcpy,scrcpy-server,origin.json}   Intel
resources/windows/platform-tools/adb.exe (+ AdbWin*.dll)
resources/windows/scrcpy/{scrcpy.exe,scrcpy-server,origin.json,*.dll}
resources/windows/ffmpeg/ffmpeg.exe
resources/linux/platform-tools/adb
resources/linux/scrcpy/{scrcpy,scrcpy-server,origin.json}
resources/linux/ffmpeg/ffmpeg
```

`origin.json` records the scrcpy version, which scrcpy-server must be told.
These files are not committed. The scripts take local archives too
(`--archive`, `--from-directory`, `--from-sdk`), for builds without network.

Without them, as under `go run .` before `make deps`, AirDroid looks for
`adb`, `scrcpy` and `ffmpeg` on the `PATH`, in Homebrew's directories and in
Android Studio's SDK. Settings › Advanced chooses others.

Set `MYGO_DEBUG=1` for debug logs, which include scrcpy-server's output. They
go to `airdroid.log` in the app's logs directory (`~/Library/Logs/AirDroid`
on macOS), and to the terminal in development builds.

## Languages

AirDroid's texts are in `locales/en.json`, the source, and
`locales/zh-Hans.json`, in the ICU message format of
[MyGo's catalogs](https://mygo.egoist.dev/). Code says them through the
accessors of package `locales`, as `locales.StopRecordingOf(name)`. After
changing a catalog:

```sh
go tool mygo generate    # writes locales/catalog_gen.go, which is committed
go tool mygo i18n check  # every message in every language
```

`go tool mygo dev -lang zh-Hans` runs AirDroid in Chinese without changing
the language it keeps, and `MYGO_LOCALE=zh-Hans go test ./internal/app`
renders the views in it. The language follows the system's unless
Settings › General › Language chooses one.

## Tests

`go test ./...` runs everything headless, on any OS:

- **`internal/scrcpy`**
  - Control messages, byte for byte against scrcpy's serialization.
  - Both stream framings.
  - An end-to-end session: the test binary plays a fake `adb` and a fake
    scrcpy-server 5.0.1. It covers the version-mismatch retry, the reverse
    tunnel, session headers, config and frames, raw audio, the device's
    clipboard and control messages.
- **`internal/ffdec`:** a stream ffmpeg encodes, decoded by the FFmpeg
  decoder, scaled, with every frame out while the input stays open.
- **`internal/media`:** Annex B parsing and conversion, and SPS sizes of real
  H.264 and H.265 streams from ffmpeg.
- **`internal/mp4`:** H.264 and H.265 MP4 and MOV-with-PCM movies, checked with
  ffprobe and decoded by ffmpeg, including the edit list.
- **`internal/app`**
  - The recorder: an instant start from cached frames, and a new file on
    rotation, both decoded by ffmpeg.
  - The audio buffer, and the key map.
  - The views, rendered and clicked with `ui.Tester`.
- **`internal/adb`, `internal/config`:** parsers, host protocol framing,
  device names, settings.

The VideoToolbox decoder (`internal/vt`) builds only for macOS; try it on a
Mac with a phone. The end-to-end session test runs a shell script as `adb`,
and is skipped on Windows.

## The README's screenshots

The screenshots in `docs/images` are AirDroid's real views, rendered without a
window. A home screen drawn with MyGo, with app icons of its own, stands in for
the phone's:

```sh
AIRDROID_DOCS_IMAGES=$PWD/docs/images go test -run DocsImages ./internal/app
```

## Releasing

`.github/workflows/release.yml` builds the apps of every platform when a tag
is pushed, and publishes them:

1. Set `version` in `mygo.json`, and bump `buildVersion` (a whole number,
   one more for each release).
2. Commit, then tag the commit `v<version>-<buildVersion>`, as `v0.1.0-2`,
   and push the tag. The workflow refuses a tag that does not match
   `mygo.json`.
3. Four runners build at once:

   | Runner | Builds |
   |---|---|
   | macOS | the Apple silicon `.dmg`, `airdroid-<version>-arm64.dmg` |
   | macOS | the universal `.dmg`, `airdroid-<version>-universal.dmg` |
   | Windows | the x64 installer, `airdroid-setup-<version>-amd64.exe` |
   | Ubuntu | the x64 `.deb` and `.tar.gz` |

   Each runs `go test`, `go vet` and `mygo vet`, stages adb, scrcpy and FFmpeg
   with the three scripts, and builds with `-sign=- -skip-notarize`.
4. A last job publishes a GitHub release of the tag with every package.

*Run workflow* (Actions › Release) builds the packages as artifacts, without
publishing them.

To ship builds that open without a warning, set a Developer ID in
`macos.signingIdentity` and notarize, and a certificate in
`windows.certificate`; see MyGo's
[distribution guide](https://github.com/egoist/mygo/blob/main/docs/distribution.md).
