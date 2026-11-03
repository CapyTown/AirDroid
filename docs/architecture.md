# Architecture

AirDroid is a Go program of the [MyGo](https://github.com/egoist/mygo)
framework, built with `CGO_ENABLED=0` for macOS, Windows and Linux:

- **No cgo, no Objective-C, no web view.** Its windows are MyGo's native UI
  (package `ui`), drawn on the GPU.
- **System frameworks through purego.** On macOS, the ones MyGo does not wrap
  (CoreMedia, CoreVideo, VideoToolbox, Accelerate) are called through
  [purego](https://github.com/ebitengine/purego).
- **FFmpeg as a program elsewhere.** Windows and Linux have no system decoder
  that Go can call without cgo, so a bundled FFmpeg executable decodes the
  video there (see [Video](#video)).

It reaches phones with `adb`, and streams them with `scrcpy-server`, the
program the [scrcpy](https://github.com/Genymobile/scrcpy) project runs on the
device. [escrcpy](https://github.com/viarotel-org/escrcpy) was the reference
for how to drive scrcpy; none of its code or architecture is used.

## The stream

```
phone ── scrcpy-server ──adb tunnel──► internal/scrcpy (Go: demux, control)
                                        │ encoded H.264/H.265 access units
                         ┌──────────────┴──────────────┐
                         ▼                             ▼
   macOS: internal/vt, VideoToolbox   recorder goroutine (internal/app)
   (GPU decode + scale, purego)       internal/mp4: passthrough muxer
   Windows, Linux: internal/ffdec,
   FFmpeg on a pipe
                         │ RGBA at window size         │ the same bytes
                         ▼                             ▼
                 ui.Bitmap in the window          ~/Downloads/*.mov|mp4
```

### A native client of scrcpy-server

AirDroid does not run scrcpy's client; it is a client of its own
(`internal/scrcpy`).

- **Starting the server.** It pushes the bundled `scrcpy-server` to
  `/data/local/tmp`, then opens a tunnel. `adb reverse` has the device connect
  to a port AirDroid listens on; `adb forward` is the fallback. Then it starts
  the server with `app_process`.
- **The sockets.** The server's video, audio and control sockets connect in
  that order. The first carries the device's name.
- **Two stream framings.** scrcpy 2.x–3.x send a 12-byte codec header with the
  video size, and flag packets as config (bit 63) and key frame (bit 62).
  scrcpy 4.0 and later send a 4-byte codec id, then a session header (bit 63)
  with the video size before each new configuration, and flag packets as
  config (bit 62) and key frame (bit 61). `ProtocolFor` picks the framing from
  the version.
- **Version.** The server refuses a client of another version and prints its
  own. AirDroid reads it from that error and starts again, so a custom
  scrcpy of any version works. The bundled server carries its version in
  `scrcpy-server.version`.
- **Control messages** are encoded as in scrcpy's `control_msg.c`: keys, text,
  touches, scrolls, back-or-screen-on, clipboard, display power, rotation,
  video reset. Device messages bring the phone's clipboard. Pointer moves are
  dropped when the device falls behind; other messages wait.

### Video

- **Access units.** Configuration packets give the parameter sets
  (`internal/media`). Frames are Annex B, converted in place to 4-byte length
  prefixes when every start code is 4 bytes, as MediaCodec writes them.
  Parameter sets found inside a frame become a new configuration. The size
  comes from session headers, or from parsing the SPS (H.264 and H.265).
- **Decoding on macOS** (`internal/vt`) is a VideoToolbox decompression
  session, called through purego. Its output callback is one purego callback,
  made once and routed by an id.
  - Pictures come out as BGRA on IOSurfaces.
  - A VTPixelTransferSession scales them on the GPU to the pixels the window
    shows them at, which the view measures as it draws.
  - vImage turns them into RGBA in one of three reused `image.RGBA`. Only
    those pixels, at the window's size, cross to the CPU.
  - After a decoding error or a lost GPU session, decoding waits for a key
    frame and asks the device for one (`ResetVideo`).
- **Decoding on Windows and Linux** (`internal/ffdec`) runs the bundled FFmpeg.
  - Access units go to its standard input as Annex B: start codes, the
    parameter sets before each key frame, and an access unit delimiter after
    each frame, so that FFmpeg's parser knows the frame is whole.
  - FFmpeg decodes with one thread and `low_delay`, scales to the window's
    pixels and writes raw RGBA to its standard output, read straight into the
    reused pictures. Frames come out at most one frame after they go in.
    `-fflags nobuffer` is left out on purpose: on a pipe it holds back half
    the frames of a raw H.264 stream.
  - A new configuration, or a window that settled at another size, starts a
    new FFmpeg at the next key frame, which AirDroid asks the device for.
    When FFmpeg falls behind, frames are skipped to the next key frame rather
    than queued.
- **Showing.** A mailbox hands the newest picture to the window with
  `Window.Update`, which calls `ui.Bitmap.Update`. Pictures the window had no
  time to show are dropped, not queued, so the picture never lags behind the
  phone.

### Input

The picture's element takes its input with `HandleInput`:

- **The pointer** becomes touches (the mouse pointer id, primary button),
  computed against the stream's size. Right-click is back-or-screen-on,
  middle-click home, and the wheel and trackpad scroll events.
- **Keys** without ⌘ become Android key codes with their meta state.
- **Text** comes from the input method (`TextCaret`). ASCII is typed with
  `InjectText`; other text is set on the device's clipboard and pasted.
- **Menu commands:** the Edit menu's paste sends the Mac's clipboard, and copy
  and cut press the phone's keys, whose clipboard comes back.

### Audio

The server sends raw PCM, 16-bit stereo at 48 kHz: there is nothing to
decode. `pcmStream` is a `mygo/audio` decoder:

- It plays about 60 ms behind, so network jitter doesn't break up the sound.
- It drops what falls more than 250 ms behind, so the sound keeps up with the
  picture.
- When the stream runs dry it plays silence, and buffers again.

### Recording

Recording takes no encoder and no decoder: it writes the access units the
phone encoded, the same bytes the decoder receives, as scrcpy's own
`--record` does.

- **Its own goroutine.** The recorder (`internal/app/record.go`) gets every
  frame through a queue. A slow disk never holds up the stream; if the queue
  overflows, the recording stops with an error and mirroring goes on.
- **It starts at once.** The recorder keeps the frames since the last key
  frame, up to 64 MB, as references with no copies. When recording starts it
  writes them, then sets the movie's start at the frame shown when the button
  was pressed. An edit list hides what came before, so recording neither waits
  for a key frame nor resets the encoder.
- **One shape per file.** A new size or configuration, as when the phone
  rotates, finishes the file and starts the next, `… (2).mov`.
- **The movie** (`internal/mp4`) is written as it goes: samples into `mdat`,
  the index (`moov`) at the end.
  - Video is `avc1`/`avcC` or `hvc1`/`hvcC`.
  - In MOV files, audio is `sowt` PCM; MP4 files hold video only.
  - Timestamps are rebased to 90 kHz from absolute times, so they don't drift.
- **Quitting** waits for the recorders to finish their movies.

### Devices

- **The device list** is the adb server's `host:track-devices-l`, read from
  its socket: no polling. When the server goes away, AirDroid starts it again.
- **Names.** Makers' market names come from system properties, as
  `ro.vendor.oplus.market.name`, else from the user's device name. They are
  shown with the maker, model and Android version.
- **Wi-Fi.** Pairing by QR code advertises a `WIFI:T:ADB;S:<name>;P:<password>;;`
  code and waits for that name among `adb mdns services`, then pairs and
  connects. *Use Wi-Fi* runs `adb tcpip 5555` and connects to the phone's
  Wi-Fi address.
- **Settings** are kept per device by its hardware serial, the same over USB
  and Wi-Fi, in `config.json` in the user's configuration directory:
  `~/Library/Application Support/AirDroid` on macOS, `%AppData%\AirDroid` on
  Windows, `~/.config/AirDroid` on Linux.

## Platforms

| | macOS | Windows | Linux |
|---|---|---|---|
| Video | VideoToolbox (GPU) | bundled FFmpeg | bundled FFmpeg |
| Icon | menu bar, template | system tray | system tray (AppIndicator) |
| Dock | leaves it when no window is open | — | — |
| Shortcuts | ⌘ | Ctrl | Ctrl |
| Helpers | `adb`, `scrcpy` | `adb.exe`, `scrcpy.exe`, `ffmpeg.exe`, run without a console | `adb`, `scrcpy`, `ffmpeg` |
| Package | universal `.app` and `.dmg` | NSIS installer | `.deb`, and a `.tar.gz` with `install.sh` |

## Code

| | |
|---|---|
| `main.go` | entry point, logging (`~/Library/Logs/AirDroid` on macOS, the app's data directory elsewhere) |
| `internal/app` | the app: tray menu and menu bar, mirror window and sidebar, settings, Add Device, recorder, audio, keys |
| `internal/scrcpy` | scrcpy-server client: launch, tunnels, stream framing, control and device messages |
| `internal/adb` | adb: device tracking over the server socket, device info, Wi-Fi, pairing, screencap |
| `internal/video` | what the decoders share: pictures, their pool, their size |
| `internal/vt` | VideoToolbox decoding through purego (macOS) |
| `internal/ffdec` | FFmpeg decoding on a pipe (Windows, Linux) |
| `internal/proc` | runs adb, scrcpy and FFmpeg without a console window on Windows |
| `internal/media` | H.264/H.265: Annex B, parameter sets, SPS parsing, avcC/hvcC |
| `internal/mp4` | MP4/QuickTime passthrough writer |
| `internal/config` | settings, defaults and per-device options |
| `resources/` | the app icon, and per platform the bundled adb, scrcpy and FFmpeg (staged, not committed) |
| `scripts/bundle-*.py` | stage adb, scrcpy and FFmpeg for a platform |
| `mygo.json` | the app's name, identifier, version and Info.plist keys |

## Threads

MyGo runs the views and native calls on the main thread.

- **Main thread only.** Everything the views read (`App`, `mirror`,
  `settingsWindow`, `addDeviceWindow`) is changed only there.
- **Off the main thread.** adb commands, the session's sockets, decoding and
  recording run on goroutines.
- **Coming back.** Their results return through `mygo.RunOnMain` or
  `Window.Update`.
- **Ending a session.** Each session has a generation number. A session that
  was replaced, by a reconnect or a change of settings, stops feeding the
  decoder and the recorder.
