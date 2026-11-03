# Development

## Requirements

- **Go 1.27.3 or later** (`GOTOOLCHAIN=auto` downloads it). Nothing else:
  AirDroid builds with `CGO_ENABLED=0`, from macOS, Linux or Windows.
- **A Mac** to run it, and to make the disk image and sign the app.
- **Optional:** [ffmpeg](https://ffmpeg.org), which the tests use to check the
  movies AirDroid writes; without it those tests are skipped.

The MyGo CLI is a tool of the module: `go tool mygo` runs the version
`go.mod` pins.

## Commands

```sh
make deps    # scrcpy 5.0.1 and adb into resources/ (SHA-256 checked)
make dev     # run AirDroid, rebuilt as its sources change (go tool mygo dev)
make build   # build/darwin-universal/AirDroid.app and its .dmg
make test    # go test ./...
make vet     # go vet for darwin/arm64 and darwin/amd64, and mygo vet
```

`make deps` runs `scripts/fetch-scrcpy.sh`. It downloads scrcpy's official
macOS releases and lays them out where `mygo build` bundles them:

```
resources/darwin-arm64/bin/scrcpy         Apple silicon
resources/darwin-amd64/bin/scrcpy         Intel
resources/darwin/bin/adb                  universal already
resources/darwin/scrcpy-server            pushed to the phone
resources/darwin/scrcpy-server.version    the version it expects
```

A universal build joins the two `scrcpy` with lipo. These files are not
committed. To bundle another release, set `SCRCPY_VERSION` (the checksums are
only checked for 5.0.1).

Without them, as under `go run .`, AirDroid looks for `adb` and `scrcpy` on
the `PATH`, in Homebrew's directories and in Android Studio's SDK. Settings ›
Advanced chooses others.

Set `MYGO_DEBUG=1` for debug logs, which include scrcpy-server's output. They
go to `~/Library/Logs/AirDroid/airdroid.log`, and to the terminal in
development builds.

## Tests

`go test ./...` runs everything headless, on any OS:

- **`internal/scrcpy`**
  - Control messages, byte for byte against scrcpy's serialization.
  - Both stream framings.
  - An end-to-end session: the test binary plays a fake `adb` and a fake
    scrcpy-server 5.0.1. It covers the version-mismatch retry, the reverse
    tunnel, session headers, config and frames, raw audio, the device's
    clipboard and control messages.
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

The VideoToolbox decoder (`internal/vt`) compiles everywhere but runs only on
macOS; try it on a Mac with a phone.

## The README's screenshots

The screenshots in `docs/images` are AirDroid's real views, rendered without a
window. A home screen drawn with MyGo, with app icons of its own, stands in for
the phone's:

```sh
AIRDROID_DOCS_IMAGES=$PWD/docs/images go test -run DocsImages ./internal/app
```

## Releasing

Releases are built and published by `.github/workflows/release.yml` when a
tag is pushed:

1. Set `version` in `mygo.json`, and bump `buildVersion` (a whole number,
   one more for each release).
2. Commit, then tag the commit `v<version>-<buildVersion>`, as `v0.1.0-1`,
   and push the tag. The workflow refuses a tag that does not match
   `mygo.json`.
3. On a macOS runner, the workflow:
   - runs `go test`, `go vet` and `mygo vet`;
   - bundles adb and scrcpy (`scripts/fetch-scrcpy.sh`);
   - builds the universal app and its `.dmg`, signed ad hoc
     (`-sign=- -skip-notarize`).
4. A last job publishes a GitHub release of the tag with the `.dmg`.

The workflow can also run by hand (*Run workflow*), which builds the `.dmg`
as an artifact without publishing it.

To ship builds that open without a Gatekeeper warning, set a Developer ID in
`macos.signingIdentity` and notarize; see MyGo's
[distribution guide](https://github.com/egoist/mygo/blob/main/docs/distribution.md).
