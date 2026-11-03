# MyGo

A macOS menu bar app that mirrors and controls Android phones, written in Go
alone with the [MyGo](https://mygo.egoist.dev/) framework: no cgo, no
Objective-C, no web view. Its windows are MyGo's native UI, drawn on the GPU,
and it reaches macOS's frameworks through [purego](https://github.com/ebitengine/purego).

MyGo talks to phones through `adb` and streams their screens with
`scrcpy-server`, both bundled. [escrcpy](https://github.com/viarotel-org/escrcpy)
was the reference for how to drive scrcpy; none of its code or architecture is
used.

## What it does

- **Menu bar.** The icon's menu lists connected devices, and their state
  (unauthorized, offline). Click one to mirror it. The menu also offers known
  Wi-Fi devices to reconnect, switching a USB device to Wi-Fi, and disconnecting.
  While a recording runs, the icon shows a red dot and the menu a *Stop
  Recording* item.
- **Add Device.** A dialog (⌘N) for:
  - USB, with live status;
  - Wi-Fi by QR code (Android 11+ *Pair device with QR code*);
  - Wi-Fi by six-digit pairing code;
  - Wi-Fi by IP address, with recent addresses.
- **Mirror window.** The device's screen, with its name and model in the
  title bar. A sidebar beside it has:
  - **Screenshot** (⌘S): a full-resolution PNG, saved and copied.
  - **Record** (⇧⌘R): the button turns into a red stop button with a running
    timer.
  - **Rotate** (⌘R).
  - **Settings** (⌘I): this device's mirroring options.
  - **Recent apps** (⇧⌘A), **Home** (⇧⌘H) and **Back** (⌘B).

  Buttons light up on hover, show tooltips with their shortcuts, and show a
  spinner while busy.
- **Input.**
  - The mouse is a finger: click and drag to touch. Right-click goes back,
    middle-click goes home. The wheel and trackpad scroll.
  - Typing goes to the phone, including input methods. Text Android cannot
    type, such as Chinese or emoji, is pasted.
  - ⌘V pastes the Mac's clipboard on the phone. Text copied on the phone
    lands on the Mac's clipboard.
- **Audio.** The phone's sound plays on the Mac (Android 11 and later).
- **Settings** (⌘,) in three tabs:
  - **General:** open at login, the Dock icon, where screenshots and
    recordings go, and the recording format.
  - **Mirroring:** defaults for resolution, bit rate, frame rate, codec,
    audio, screen off, stay awake, show touches, and keep on top.
  - **Advanced:** bundled or custom `adb` and `scrcpy` paths, with their
    versions shown, and restarting the adb server.

## How it works

```
phone ── scrcpy-server ──adb tunnel──► internal/scrcpy (Go: demux, control)
                                        │ encoded H.264/H.265 access units
                         ┌──────────────┴──────────────┐
                         ▼                             ▼
             internal/vt: VideoToolbox        internal/app recorder goroutine
             (GPU decode + scale, purego)     internal/mp4: passthrough muxer
                         │ RGBA at window size         │ same bytes → .mov/.mp4
                         ▼                             ▼
                 ui.Bitmap in the window          ~/Movies/MyGo
```

- **Native client of scrcpy-server.** MyGo pushes the bundled
  `scrcpy-server`, opens an `adb reverse` tunnel (falling back to
  `adb forward`), starts the server and reads its video, audio and control
  sockets. It handles both stream framings: scrcpy 2.x–3.x, and 4.0 and later
  with session headers. If the server reports a different version than the one
  announced, MyGo learns it and retries, so a custom scrcpy of another version
  works too.
- **Decoding** is VideoToolbox on the GPU. Frames are scaled there to the
  pixels they show at, and only that picture crosses to the CPU.
- **Recording takes no extra encoder or decoder.** It writes the H.264/H.265
  access units the phone already encoded, the same bytes the decoder gets,
  into the movie. This is what scrcpy's own `--record` does: a pure-Go muxer
  writing a QuickTime movie, or an MP4 for video only.
  - It runs on its own goroutine behind a queue, so a slow disk never holds up
    the picture or the input.
  - It keeps the frames since the last key frame. A recording therefore
    starts at the frame shown when you press the button, with no wait for a
    key frame and no encoder reset; an edit list hides the frames before it.
  - Rotating the phone starts a new file.
  - Stopping writes the index at once. Quitting waits for it.
  - A write error stops the recording, says so, and keeps mirroring.
- **Audio** comes as raw PCM: nothing to decode, played by MyGo's mixer
  with about 60 ms of jitter buffer. It is recorded as PCM in MOV files.
- **Device list** comes from the adb server's `host:track-devices-l` socket,
  without polling.

## Build

Go 1.27 or later. `make` downloads scrcpy 5.0.1's official macOS release
(SHA-256 checked) into `resources/`, which MyGo bundles into the app.

```sh
make dev     # run it, rebuilt on changes
make build   # build/darwin-universal/MyGo.app and its .dmg
make test    # unit and end-to-end tests (ffmpeg checks the recordings)
make vet     # go vet for darwin, and mygo vet for native UI lifetimes
```

Everything builds with `CGO_ENABLED=0`, from any OS. The disk image and code
signing need a Mac; `.github/workflows/ci.yml` builds the app on a macOS
runner.

## Layout

| | |
|---|---|
| `main.go` | entry point |
| `internal/app` | the app: tray menu, mirror window, sidebar, settings, Add Device, recorder, audio |
| `internal/scrcpy` | scrcpy-server client: launch, tunnels, stream framing, control and device messages |
| `internal/adb` | adb: device tracking over the server socket, device info, Wi-Fi, pairing, screencap |
| `internal/vt` | VideoToolbox decoding through purego |
| `internal/media` | H.264/H.265: Annex B, parameter sets, SPS parsing, avcC/hvcC |
| `internal/mp4` | MP4/QuickTime passthrough writer |
| `internal/config` | settings, defaults and per-device options |
| `scripts/fetch-scrcpy.sh` | downloads the bundled scrcpy and adb |
