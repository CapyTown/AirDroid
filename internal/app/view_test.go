package app

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/CapyTown/AirDroid/internal/config"
)

// Views render without a window. MYGO_SNAPSHOTS=dir writes what they
// draw there, to look at.
func snapshot(t *testing.T, name string, view func(c *ui.Context), w, h int) *image.RGBA {
	t.Helper()
	img := ui.Render(view, w, h, 2)
	if dir := os.Getenv("MYGO_SNAPSHOTS"); dir != "" {
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}
	return img
}

func testApp(t *testing.T) *App {
	t.Helper()
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &App{cfg: store, devices: map[string]*Device{}, mirrors: map[string]*mirror{}}
}

func testMirror(t *testing.T) *mirror {
	a := testApp(t)
	m := &mirror{
		app:      a,
		serial:   "R5CT",
		title:    "OnePlus Ace 3V",
		subtitle: "OnePlus · PJF110 · Android 15",
		keysDown: map[ui.Key]int{},
		frameW:   1080,
		frameH:   2400,
		state:    stateStreaming,
		opts:     config.DefaultMirrorOptions(),
	}
	// A phone screen: a gradient, as a wallpaper.
	img := image.NewRGBA(image.Rect(0, 0, 270, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 270; x++ {
			i := img.PixOffset(x, y)
			img.Pix[i+0] = uint8(90 + y*100/600)
			img.Pix[i+1] = uint8(60 + x*40/270)
			img.Pix[i+2] = uint8(200 - y*80/600)
			img.Pix[i+3] = 255
		}
	}
	m.bitmap = ui.NewBitmap(img)
	return m
}

func TestMirrorView(t *testing.T) {
	m := testMirror(t)
	snapshot(t, "mirror", m.view, 440, 820)
	m.recording, m.recStart = true, time.Now().Add(-6*time.Second)
	snapshot(t, "mirror-recording", m.view, 440, 820)
	m.state, m.stateMsg = stateDisconnected, "The device disconnected."
	snapshot(t, "mirror-disconnected", m.view, 440, 820)
	m.state, m.recording = stateStreaming, false
	m.settingsOpen = true
	m.editOpts = m.opts
	snapshot(t, "mirror-settings", m.view, 820, 820)
}

func TestMirrorSidebar(t *testing.T) {
	m := testMirror(t)
	tt := ui.NewTester(m.view, 440, 820)
	for _, label := range []string{"Screenshot", "Record", "Rotate", "Settings", "Recent apps", "Home", "Back"} {
		if _, ok := tt.Find(label); !ok {
			t.Errorf("no %q button", label)
		}
	}
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	if !m.settingsOpen || !tt.HasText("Mirroring") {
		t.Error("the gear does not open the settings")
	}
}

func TestSettingsView(t *testing.T) {
	a := testApp(t)
	s := &settingsWindow{app: a, cfg: a.cfg.Get()}
	for i, name := range []string{"general", "mirroring", "advanced"} {
		s.tab = i
		snapshot(t, "settings-"+name, s.view, 580, 640)
	}
}

func TestAddDeviceView(t *testing.T) {
	a := testApp(t)
	a.devices["R5CT"] = &Device{}
	a.devices["R5CT"].Serial, a.devices["R5CT"].State = "R5CT", "device"
	a.devices["R5CT"].Model = "PJF110"
	d := &addDeviceWindow{app: a}
	snapshot(t, "add-usb", d.view, 480, 600)
	d.mode, d.wifiMode = 1, 2
	snapshot(t, "add-wifi-address", d.view, 480, 600)
	d.wifiMode = 1
	snapshot(t, "add-wifi-code", d.view, 480, 600)
}
