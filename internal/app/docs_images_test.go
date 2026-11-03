package app

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/CapyTown/AirDroid/internal/adb"
)

// TestDocsImages renders the screenshots of README.md into the directory
// AIRDROID_DOCS_IMAGES names:
//
//	AIRDROID_DOCS_IMAGES=$PWD/docs/images go test -run DocsImages ./internal/app
func TestDocsImages(t *testing.T) {
	dir := os.Getenv("AIRDROID_DOCS_IMAGES")
	if dir == "" {
		t.Skip("AIRDROID_DOCS_IMAGES not set")
	}
	// The screenshots show the Mac app.
	defer func(g string) { goos = g }(goos)
	goos = "darwin"
	save := func(name string, img *image.RGBA) {
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}

	m := testMirror(t)
	m.bitmap = ui.NewBitmap(ui.Render(phoneHome, 360, 800, 3))
	save("mirror", ui.Render(m.view, 440, 860, 2))
	m.recording, m.recStart = true, time.Now().Add(-83*time.Second)
	save("recording", ui.Render(m.view, 440, 860, 2))
	m.recording = false
	m.settingsOpen, m.editOpts = true, m.opts
	save("device-settings", ui.Render(m.view, 800, 860, 2))

	a := testApp(t)
	s := &settingsWindow{app: a, cfg: a.cfg.Get(), openAtLogin: true,
		adbStatus:    "adb 36.0.0 · /Applications/AirDroid.app/Contents/Resources/bin/adb",
		scrcpyStatus: "scrcpy-server 5.0.1 · /Applications/AirDroid.app/Contents/Resources/scrcpy-server"}
	for i, name := range []string{"settings-general", "settings-mirroring", "settings-advanced"} {
		s.tab = i
		save(name, ui.Render(s.view, 580, 700, 2))
	}

	d := &addDeviceWindow{app: a}
	dev := &Device{HasInfo: true}
	dev.Serial, dev.State = "R5CT1234ABC", "device"
	dev.Info = adb.Info{Serial: dev.Serial, Manufacturer: "OnePlus", Model: "PJF110", MarketName: "OnePlus Ace 3V", Release: "15"}
	a.devices[dev.Serial] = dev
	save("add-usb", ui.Render(d.view, 480, 600, 2))
	d.mode, d.wifiMode = 1, 0
	d.qrCancel = func() {} // show the code without pairing
	d.qr = adb.QRPairing{Name: "airdroid-docs", Password: "example"}
	d.qrImage = qrBitmap(d.qr.Payload())
	d.qrStatus = "Waiting for the phone to scan the code…"
	save("add-wifi", ui.Render(d.view, 480, 600, 2))
}

// Icons of the apps on the stand-in home screen: generic system apps,
// drawn for these screenshots.
var (
	glyphPhotos   = svg(`<rect x="3" y="4" width="18" height="16" rx="2.5"/><circle cx="9" cy="9.5" r="1.8"/><path d="m21 16-5-5-9 9"/>`)
	glyphMaps     = svg(`<path d="M12 21s-6.5-5.6-6.5-11a6.5 6.5 0 0 1 13 0c0 5.4-6.5 11-6.5 11z"/><circle cx="12" cy="10" r="2.3"/>`)
	glyphMusic    = svg(`<path d="M9 18V5l11-2v13"/><circle cx="6.5" cy="18" r="2.5"/><circle cx="17.5" cy="16" r="2.5"/>`)
	glyphNotes    = svg(`<path d="M6 3h9l4 4v14H6z"/><path d="M14.5 3v4.5H19"/><path d="M9 12h7M9 16h7"/>`)
	glyphClock    = svg(`<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>`)
	glyphMail     = svg(`<rect x="3" y="5" width="18" height="14" rx="2.5"/><path d="m3.5 7 8.5 6 8.5-6"/>`)
	glyphFiles    = svg(`<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>`)
	glyphCall     = svg(`<path d="M5 3.5h3.5l2 5-2.5 1.5a11 11 0 0 0 6 6l1.5-2.5 5 2V19a2 2 0 0 1-2 2A17 17 0 0 1 3 5.5a2 2 0 0 1 2-2"/>`)
	glyphMessages = svg(`<path d="M21 11.5a8.5 8.5 0 0 1-12.4 7.6L3 21l1.9-5.6A8.5 8.5 0 1 1 21 11.5z"/><path d="M8.5 11.5h.01M12 11.5h.01M15.5 11.5h.01"/>`)
	glyphBrowser  = svg(`<circle cx="12" cy="12" r="9"/><path d="m15.5 8.5-2 5-5 2 2-5z"/>`)
)

type homeApp struct {
	name     string
	from, to ui.Color
	glyph    *ui.SVG
	ink      ui.Color
}

func appIcon(c *ui.Context, a homeApp, size float32) {
	ink := a.ink
	if ink == (ui.Color{}) {
		ink = ui.RGB(255, 255, 255)
	}
	ui.Box(c).Size(size, size).Radius(size*0.28).
		LinearGradient(ui.LinearGradient{Angle: 180, From: a.from, To: a.to}).
		Shadow(0, 2, 6, 0, ui.RGBA(0, 0, 0, 0.18)).
		AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		ui.Icon(c, a.glyph).FontSize(size * 0.52).TextColor(ink)
	})
}

// phoneHome draws an Android home screen, to stand in for a phone's.
func phoneHome(c *ui.Context) {
	white := ui.RGB(255, 255, 255)
	apps := []homeApp{
		{"Camera", ui.RGB(84, 90, 98), ui.RGB(36, 40, 46), iconCamera, ui.Color{}},
		{"Photos", ui.RGB(255, 183, 77), ui.RGB(244, 108, 64), glyphPhotos, ui.Color{}},
		{"Maps", ui.RGB(102, 214, 128), ui.RGB(28, 160, 90), glyphMaps, ui.Color{}},
		{"Music", ui.RGB(255, 102, 128), ui.RGB(226, 32, 92), glyphMusic, ui.Color{}},
		{"Notes", ui.RGB(255, 230, 120), ui.RGB(250, 196, 30), glyphNotes, ui.RGB(120, 84, 10)},
		{"Clock", ui.RGB(70, 72, 84), ui.RGB(24, 26, 34), glyphClock, ui.RGB(255, 186, 60)},
		{"Mail", ui.RGB(96, 172, 255), ui.RGB(20, 110, 240), glyphMail, ui.Color{}},
		{"Files", ui.RGB(120, 214, 255), ui.RGB(40, 150, 230), glyphFiles, ui.Color{}},
	}
	dock := []homeApp{
		{"Phone", ui.RGB(110, 230, 120), ui.RGB(36, 186, 72), glyphCall, ui.Color{}},
		{"Messages", ui.RGB(120, 170, 255), ui.RGB(52, 110, 240), glyphMessages, ui.Color{}},
		{"Browser", ui.RGB(255, 255, 255), ui.RGB(226, 232, 240), glyphBrowser, ui.RGB(30, 120, 240)},
		{"Settings", ui.RGB(150, 156, 166), ui.RGB(96, 102, 112), iconSettings, ui.Color{}},
	}
	ui.Column(c).Fill().LinearGradient(ui.LinearGradient{Angle: 160, Stops: []ui.GradientStop{
		{Color: ui.RGB(88, 52, 190), At: 0}, {Color: ui.RGB(176, 120, 220), At: 0.5}, {Color: ui.RGB(46, 92, 170), At: 1},
	}}).Padding(14, 20, 10, 20).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, "14:06").FontSize(14).Bold().TextColor(white).Grow(1)
			ui.Text(c, "5G  ▮ 82%").FontSize(11).TextColor(white)
		})
		ui.Column(c).FillWidth().Padding(28, 0, 0, 0).Gap(2).Children(func() {
			ui.Text(c, "Tuesday, 3 November").FontSize(13).TextColor(white.Alpha(0.85))
			ui.Text(c, "21°  Clear").FontSize(30).Bold().TextColor(white)
		})
		ui.Grid(c).Columns(4).FillWidth().GapX(14).GapY(18).Padding(36, 0, 0, 0).Children(func() {
			for _, a := range apps {
				ui.Column(c).AlignItems(ui.Center).Gap(6).Children(func() {
					appIcon(c, a, 58)
					ui.Text(c, a.name).FontSize(11).TextColor(white)
				})
			}
		})
		ui.Spacer(c)
		ui.Row(c).FillWidth().Justify(ui.Center).Gap(6).Padding(0, 0, 16, 0).Children(func() {
			for i := 0; i < 3; i++ {
				a := float32(0.45)
				if i == 1 {
					a = 1
				}
				ui.Box(c).Size(6, 6).Radius(3).Background(white.Alpha(a))
			}
		})
		ui.Row(c).FillWidth().Justify(ui.SpaceBetween).Padding(10, 12).Radius(28).Background(white.Alpha(0.2)).Children(func() {
			for _, a := range dock {
				appIcon(c, a, 54)
			}
		})
		ui.Row(c).FillWidth().Justify(ui.Center).Padding(14, 0, 2, 0).Children(func() {
			ui.Box(c).Size(110, 4).Radius(2).Background(white.Alpha(0.85))
		})
	})
}
