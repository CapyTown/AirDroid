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
	"github.com/CapyTown/AirDroid/locales"
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
		subtitle: "· Android 15",
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
	m.editOpts = m.opts
	snapshot(t, "device-settings", m.settingsView, settingsWidth, settingsHeight)
}

func TestMirrorSidebar(t *testing.T) {
	m := testMirror(t)
	tt := ui.NewTester(m.view, 440, 820)
	for _, label := range []string{"Screenshot", "Record", "Settings", "Recent apps", "Home", "Back"} {
		if _, ok := tt.Find(label); !ok {
			t.Errorf("no %q button", label)
		}
	}
	if _, ok := tt.Find("Rotate"); ok {
		t.Error("the sidebar has a Rotate button: rotating is in the Device menu")
	}
}

// The picture keeps the stream's aspect inside the window.
func TestVideoRect(t *testing.T) {
	m := testMirror(t)
	vw, vh := m.videoRect(440, 820, 1080, 2400)
	if vw > float64(440-chromeW)+0.5 || vh > float64(820-chromeH)+0.5 {
		t.Errorf("picture %vx%v does not fit", vw, vh)
	}
	if r := vw / vh; r < 0.449 || r > 0.451 {
		t.Errorf("aspect %v, want 0.45", r)
	}
}

func TestSettingsView(t *testing.T) {
	a := testApp(t)
	s := &settingsWindow{app: a, cfg: a.cfg.Get()}
	for _, page := range []string{"general", "mirroring", "shortcuts", "advanced", "about"} {
		s.page = page
		snapshot(t, "settings-"+page, s.view, 860, 600)
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

// The views say the language the app shows.
func TestMirrorSidebarChinese(t *testing.T) {
	t.Setenv("MYGO_LOCALE", "zh-Hans")
	m := testMirror(t)
	tt := ui.NewTester(m.view, 440, 820)
	for _, label := range []string{"截屏", "录制", "手机息屏", "设置", "最近任务", "主屏幕", "返回"} {
		if _, ok := tt.Find(label); !ok {
			t.Errorf("no %q button", label)
		}
	}
	if got := locales.RecordingSaved(3); got != "录屏已保存为 3 个文件" {
		t.Errorf("plural %q", got)
	}
	t.Setenv("MYGO_LOCALE", "en")
	if got := locales.RecordingSaved(1); got != "Recording saved" {
		t.Errorf("one %q", got)
	}
}
