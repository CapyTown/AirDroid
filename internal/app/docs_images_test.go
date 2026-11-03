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

// phoneHome draws an Android home screen, to stand in for a phone's.
func phoneHome(c *ui.Context) {
	white := ui.RGB(255, 255, 255)
	apps := []struct {
		name  string
		color ui.Color
	}{
		{"Camera", ui.RGB(52, 58, 64)}, {"Photos", ui.RGB(255, 149, 0)}, {"Maps", ui.RGB(52, 199, 89)}, {"Music", ui.RGB(255, 45, 85)},
		{"Notes", ui.RGB(255, 204, 0)}, {"Clock", ui.RGB(88, 86, 214)}, {"Mail", ui.RGB(0, 122, 255)}, {"Files", ui.RGB(90, 200, 250)},
	}
	dock := []ui.Color{ui.RGB(52, 199, 89), ui.RGB(48, 209, 88), ui.RGB(0, 122, 255), ui.RGB(142, 142, 147)}
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
					ui.Box(c).Size(58, 58).Radius(16).Background(a.color)
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
			for _, col := range dock {
				ui.Box(c).Size(54, 54).Radius(16).Background(col)
			}
		})
		ui.Row(c).FillWidth().Justify(ui.Center).Padding(14, 0, 2, 0).Children(func() {
			ui.Box(c).Size(110, 4).Radius(2).Background(white.Alpha(0.85))
		})
	})
}
