package app

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/png"

	"github.com/egoist/mygo/ui"
)

// Icons, drawn in the color of the text. camera, settings and refresh come
// from Lucide (ISC license, see THIRD_PARTY_NOTICES.md); the others are
// AirDroid's own.
func svg(body string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`))
}

var (
	iconCamera   = svg(`<path d="M14.5 4h-5L7 7H4a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2V9a2 2 0 0 0-2-2h-3l-2.5-3z"/><circle cx="12" cy="13" r="3"/>`)
	iconRecord   = svg(`<circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="4.5" fill="currentColor" stroke="none"/>`)
	iconStop     = svg(`<rect x="5" y="5" width="14" height="14" rx="3.5"/>`)
	iconRotate   = svg(`<path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M8 16H3v5"/>`)
	iconSettings = svg(`<path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/><circle cx="12" cy="12" r="3"/>`)
	iconRecents  = svg(`<rect x="5" y="5" width="14" height="14" rx="2.5"/>`)
	iconHome     = svg(`<circle cx="12" cy="12" r="8"/>`)
	iconBack     = svg(`<path d="M17 4.8v14.4a.8.8 0 0 1-1.2.7L5.6 12.7a.8.8 0 0 1 0-1.4l10.2-7.2a.8.8 0 0 1 1.2.7z"/>`)
	iconPhone    = svg(`<rect x="6" y="2" width="12" height="20" rx="2.5"/><path d="M11 18h2"/>`)
	iconMirror   = svg(`<rect x="2" y="3" width="13" height="10" rx="2"/><rect x="16" y="8" width="6" height="13" rx="1.5"/><path d="M6 17h6"/><path d="M9 13v4"/>`)
	iconTools    = svg(`<path d="M4 17l6-6-6-6"/><path d="M12 19h8"/>`)
	iconUSB      = svg(`<rect x="8" y="2" width="8" height="6" rx="1"/><path d="M7 8h10v5a5 5 0 0 1-10 0z"/><path d="M12 18v4"/>`)
	iconWiFi     = svg(`<path d="M12 20h.01"/><path d="M2 8.82a15 15 0 0 1 20 0"/><path d="M5 12.86a10 10 0 0 1 14 0"/><path d="M8.5 16.43a5 5 0 0 1 7 0"/>`)
	iconClose    = svg(`<path d="M18 6 6 18"/><path d="m6 6 12 12"/>`)
	iconCheck    = svg(`<path d="M20 6 9 17l-5-5"/>`)
	iconAlert    = svg(`<circle cx="12" cy="12" r="9"/><path d="M12 8v5"/><path d="M12 16h.01"/>`)
)

// Tray icons: a phone, black on transparent for the menu bar to tint, and
// the same with a red dot while a recording runs.
var (
	trayIconPNG    = drawTrayIcon(false)
	trayIconRecPNG = drawTrayIcon(true)
)

func drawTrayIcon(recording bool) []byte {
	const s = 36 // 18 pt at 2x
	img := image.NewNRGBA(image.Rect(0, 0, s, s))
	ink := color.NRGBA{0, 0, 0, 255}
	// Phone body: a rounded rectangle outline.
	x0, y0, x1, y1 := 10, 3, 26, 33
	r := 4
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			if !inRounded(x, y, x0, y0, x1, y1, r) {
				continue
			}
			inner := inRounded(x, y, x0+3, y0+3, x1-3, y1-3, r-2)
			if !inner {
				img.Set(x, y, ink)
			}
		}
	}
	// Speaker slot and a mirrored-screen bar.
	for x := 16; x < 20; x++ {
		img.Set(x, 29, ink)
	}
	for y := 9; y < 22; y++ {
		for x := 15; x < 21; x++ {
			if y >= 12 && y < 19 {
				img.Set(x, y, color.NRGBA{0, 0, 0, 140})
			}
		}
	}
	if recording {
		red := color.NRGBA{255, 59, 48, 255}
		cx, cy, rr := 28, 8, 6
		for y := cy - rr; y <= cy+rr; y++ {
			for x := cx - rr; x <= cx+rr; x++ {
				if (x-cx)*(x-cx)+(y-cy)*(y-cy) <= rr*rr && x >= 0 && x < s && y >= 0 {
					img.Set(x, y, red)
				}
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func inRounded(x, y, x0, y0, x1, y1, r int) bool {
	if x < x0 || x >= x1 || y < y0 || y >= y1 {
		return false
	}
	cx, cy := x, y
	switch {
	case x < x0+r:
		cx = x0 + r
	case x >= x1-r:
		cx = x1 - r - 1
	}
	switch {
	case y < y0+r:
		cy = y0 + r
	case y >= y1-r:
		cy = y1 - r - 1
	}
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}
