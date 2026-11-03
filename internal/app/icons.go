package app

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/png"

	"github.com/egoist/mygo/ui"
)

// Icons, drawn in the color of the text. camera and settings come from
// Lucide (ISC license, see docs/third-party-notices.md); the others are
// AirDroid's own.
func svg(body string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`))
}

var (
	iconCamera    = svg(`<path d="M14.5 4h-5L7 7H4a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2V9a2 2 0 0 0-2-2h-3l-2.5-3z"/><circle cx="12" cy="13" r="3"/>`)
	iconRecord    = svg(`<circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="4.5" fill="currentColor" stroke="none"/>`)
	iconStop      = svg(`<rect x="5" y="5" width="14" height="14" rx="3.5"/>`)
	iconSettings  = svg(`<path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/><circle cx="12" cy="12" r="3"/>`)
	iconRecents   = svg(`<rect x="5" y="5" width="14" height="14" rx="2.5"/>`)
	iconHome      = svg(`<circle cx="12" cy="12" r="8"/>`)
	iconBack      = svg(`<path d="M17 4.8v14.4a.8.8 0 0 1-1.2.7L5.6 12.7a.8.8 0 0 1 0-1.4l10.2-7.2a.8.8 0 0 1 1.2.7z"/>`)
	iconScreenOff = svg(`<rect x="6" y="2" width="12" height="20" rx="2.5"/><path d="M11 18h2"/><path d="M3 3l18 18"/>`)
	iconPhone     = svg(`<rect x="6" y="2" width="12" height="20" rx="2.5"/><path d="M11 18h2"/>`)
	iconMirror    = svg(`<rect x="2" y="3" width="13" height="10" rx="2"/><rect x="16" y="8" width="6" height="13" rx="1.5"/><path d="M6 17h6"/><path d="M9 13v4"/>`)
	iconKeyboard  = svg(`<rect x="2" y="5" width="20" height="14" rx="2.5"/><path d="M6 9h.01M10 9h.01M14 9h.01M18 9h.01M6 12.5h.01M10 12.5h.01M14 12.5h.01M18 12.5h.01M8 16h8"/>`)
	iconInfo      = svg(`<circle cx="12" cy="12" r="9"/><path d="M12 11v5"/><path d="M12 8h.01"/>`)
	iconTools     = svg(`<path d="M4 17l6-6-6-6"/><path d="M12 19h8"/>`)
	iconUSB       = svg(`<rect x="8" y="2" width="8" height="6" rx="1"/><path d="M7 8h10v5a5 5 0 0 1-10 0z"/><path d="M12 18v4"/>`)
	iconWiFi      = svg(`<path d="M12 20h.01"/><path d="M2 8.82a15 15 0 0 1 20 0"/><path d="M5 12.86a10 10 0 0 1 14 0"/><path d="M8.5 16.43a5 5 0 0 1 7 0"/>`)
	iconClose     = svg(`<path d="M18 6 6 18"/><path d="m6 6 12 12"/>`)
	iconCheck     = svg(`<path d="M20 6 9 17l-5-5"/>`)
	iconAlert     = svg(`<circle cx="12" cy="12" r="9"/><path d="M12 8v5"/><path d="M12 16h.01"/>`)
)

// Tray icons: the head of AirDroid's logo, black on transparent for the
// menu bar to tint, and the same with a red dot while a recording runs.
var (
	trayIconPNG    = drawTrayIcon(false)
	trayIconRecPNG = drawTrayIcon(true)
)

func drawTrayIcon(recording bool) []byte {
	const s = 36 // 18 pt at 2x
	const n = 4  // samples per pixel side, for smooth edges
	img := image.NewNRGBA(image.Rect(0, 0, s, s))
	for py := 0; py < s; py++ {
		for px := 0; px < s; px++ {
			ink, red := 0, 0
			for sy := 0; sy < n; sy++ {
				for sx := 0; sx < n; sx++ {
					x := float64(px) + (float64(sx)+0.5)/n
					y := float64(py) + (float64(sy)+0.5)/n
					if recording && inDisc(x, y, 30, 7, 5.5) {
						red++
					} else if inLogo(x, y) {
						ink++
					}
				}
			}
			switch {
			case red > 0:
				img.Set(px, py, color.NRGBA{255, 59, 48, uint8(red * 255 / (n * n))})
			case ink > 0:
				img.Set(px, py, color.NRGBA{0, 0, 0, uint8(ink * 255 / (n * n))})
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// inLogo reports whether a point of the 36×36 icon is inked: a half disc
// with two eyes cut out, and two antennas.
func inLogo(x, y float64) bool {
	const cx, base, r = 18.0, 27.5, 14.0
	head := y <= base && inDisc(x, y, cx, base, r)
	if head && (inDisc(x, y, 12.5, 21.5, 2) || inDisc(x, y, 23.5, 21.5, 2)) {
		return false
	}
	return head || nearSegment(x, y, 11.5, 16.5, 7.5, 9.5, 1.3) || nearSegment(x, y, 24.5, 16.5, 28.5, 9.5, 1.3)
}

func inDisc(x, y, cx, cy, r float64) bool {
	return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r
}

// nearSegment reports whether a point is within w of the segment from
// (x0, y0) to (x1, y1): a stroke with round caps.
func nearSegment(x, y, x0, y0, x1, y1, w float64) bool {
	dx, dy := x1-x0, y1-y0
	t := ((x-x0)*dx + (y-y0)*dy) / (dx*dx + dy*dy)
	t = max(0, min(1, t))
	px, py := x0+t*dx-x, y0+t*dy-y
	return px*px+py*py <= w*w
}
