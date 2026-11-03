# Third-party notices

## scrcpy

AirDroid bundles scrcpy and scrcpy-server from scrcpy's official releases
for macOS, Windows and Linux (https://github.com/Genymobile/scrcpy),
downloaded and checked against their SHA-256 by `scripts/bundle-scrcpy.py`.
scrcpy is Copyright (C) 2018 Genymobile and Copyright (C) 2018-2026 Romain
Vimont, licensed under the Apache License 2.0; its licence ships in the app
beside it, in `scrcpy/`.

## Android SDK Platform-Tools

AirDroid bundles adb from Google's Android SDK Platform-Tools
(https://developer.android.com/tools/releases/platform-tools), downloaded by
`scripts/bundle-adb.py`, licensed under the Apache License 2.0; its
`NOTICE.txt` ships in the app beside it, in `platform-tools/`.

## FFmpeg

On Windows and Linux, AirDroid bundles an FFmpeg executable, which it runs as
a separate program to decode the mirrored screen. It comes from the
ffmpeg-static project's release b6.1.1
(https://github.com/eugeneware/ffmpeg-static/releases/tag/b6.1.1), built by
https://www.gyan.dev/ffmpeg/builds/ (Windows) and
https://johnvansickle.com/ffmpeg/ (Linux) from FFmpeg 6.1.1
(https://ffmpeg.org/releases/ffmpeg-6.1.1.tar.xz), downloaded and checked
against their SHA-256 by `scripts/bundle-ffmpeg.py`. These builds are
licensed under the GNU General Public License version 3; the licence, the
builders' notes and where to get the source ship in the app beside the
executable, in `ffmpeg/`. AirDroid's own code is MIT-licensed and does not link
to FFmpeg. The macOS app contains no FFmpeg: it decodes with VideoToolbox.

AirDroid implements a client of scrcpy-server's protocol; it does not contain
scrcpy's client code.

## Lucide

The camera, settings and rotate icons are from Lucide
(https://lucide.dev), ISC License:

> Copyright (c) for portions of Lucide are held by Cole Bemis 2013-2022 as
> part of Feather (MIT). All other copyright (c) for Lucide are held by
> Lucide Contributors 2022.
>
> Permission to use, copy, modify, and/or distribute this software for any
> purpose with or without fee is hereby granted, provided that the above
> copyright notice and this permission notice appear in all copies.
>
> THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
> WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
> MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY
> SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
> WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
> ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF OR
> IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
