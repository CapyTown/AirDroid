// Package app is AirDroid: a menu bar app that mirrors and controls Android
// devices, built with the MyGo framework and its native UI.
package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"github.com/CapyTown/AirDroid/internal/adb"
	"github.com/CapyTown/AirDroid/internal/config"
	"github.com/CapyTown/AirDroid/internal/scrcpy"
)

// Where the project lives, for updates and feedback.
var (
	ReleasesURL = "https://github.com/CapyTown/AirDroid/releases"
	IssuesURL   = "https://github.com/CapyTown/AirDroid/issues/new"
	SourceURL   = "https://github.com/CapyTown/AirDroid"
)

// Device is a device the adb server lists.
type Device struct {
	adb.Entry
	Info    adb.Info
	HasInfo bool
}

// WiFi reports a device attached over the network.
func (d *Device) WiFi() bool { return adb.IsWiFiSerial(d.Serial) }

// Name is what people call the device.
func (d *Device) Name() string {
	if d.HasInfo {
		return d.Info.NameIn(mygo.App.Locale())
	}
	if d.Model != "" {
		return strings.ReplaceAll(d.Model, "_", " ")
	}
	return d.Serial
}

// Transport is "USB" or "Wi-Fi".
func (d *Device) Transport() string {
	if d.WiFi() {
		return "Wi-Fi"
	}
	return "USB"
}

// App is AirDroid's state. Everything in it is read and changed on the main
// thread; goroutines hand their results over with mygo.RunOnMain or a
// window's Update.
type App struct {
	cfg *config.Store
	adb *adb.Client

	tray      *mygo.Tray
	devices   map[string]*Device
	trackStop context.CancelFunc
	trackGen  int

	mirrors  map[string]*mirror
	settings *settingsWindow
	add      *addDeviceWindow

	serverMu  sync.Mutex
	server    scrcpy.Server
	serverErr error
	serverFor string // the path server was resolved for

	quitting bool
	// stopping holds the recorders of closed windows, which may still be
	// finishing a movie.
	stopping []*recorder
}

// New prepares the app; Start runs once AirDroid is ready.
func New() (*App, error) {
	store, err := config.Open(config.DefaultPath())
	if err != nil {
		slog.Warn("settings unreadable, using defaults", "err", err)
	}
	a := &App{
		cfg:     store,
		devices: map[string]*Device{},
		mirrors: map[string]*mirror{},
	}
	a.adb = adb.New(a.adbPath())
	return a, nil
}

// Start creates the tray icon and starts watching for devices. It runs on
// the main thread.
func (a *App) Start() {
	if hasDock() {
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	}
	mygo.App.SetMenu(a.menuBar())
	mygo.App.OnWindowAllClosed(func() {}) // a menu bar app lives on
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.quitting = true })
	mygo.App.OnActivate(func(visible bool) {
		if !visible {
			a.openAddDevice()
		}
	})
	mygo.App.OnQuit(a.shutdown)
	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:             "airdroid",
		Icon:           trayIconPNG,
		IconIsTemplate: true,
		ToolTip:        "AirDroid",
	})
	if err != nil {
		slog.Error("tray", "err", err)
	}
	a.tray = tray
	a.refreshMenu()
	a.startTracking()
	go a.resolveServer()
}

func (a *App) shutdown() {
	if a.trackStop != nil {
		a.trackStop()
	}
	for _, m := range a.mirrors {
		m.shutdown()
	}
	// A recording finishes its movie before the app goes.
	deadline := time.After(10 * time.Second)
	for _, r := range a.stopping {
		select {
		case <-r.done:
		case <-deadline:
			return
		}
	}
}

// --- Tools ---------------------------------------------------------------

func resourcesDir() string {
	dir, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		return ""
	}
	return dir
}

// bundled returns a file shipped in the app's resources, or under
// `go run` in the project's resources directory, for this platform.
func bundled(rel ...string) string {
	dir := resourcesDir()
	if dir == "" {
		return ""
	}
	p := filepath.Join(rel...)
	for _, cand := range []string{
		filepath.Join(dir, p),
		filepath.Join(dir, runtime.GOOS+"-"+runtime.GOARCH, p),
		filepath.Join(dir, runtime.GOOS, p),
	} {
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand
		}
	}
	return ""
}

// exe names a program as the platform does: "adb.exe" on Windows.
func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// scrcpyDir is where the bundled scrcpy is: one directory per processor on
// macOS, which runs either.
func scrcpyDir() string {
	if runtime.GOOS == "darwin" {
		return filepath.Join("scrcpy", runtime.GOARCH)
	}
	return "scrcpy"
}

// BundledADB is the adb shipped with AirDroid.
func BundledADB() string { return bundled("platform-tools", exe("adb")) }

// BundledScrcpy is the scrcpy-server shipped with AirDroid.
func BundledScrcpy() string { return bundled(scrcpyDir(), "scrcpy-server") }

// BundledScrcpyBinary is the scrcpy executable shipped with AirDroid.
func BundledScrcpyBinary() string { return bundled(scrcpyDir(), exe("scrcpy")) }

// BundledFFmpeg is the FFmpeg that decodes video on Windows and Linux.
func BundledFFmpeg() string { return bundled("ffmpeg", exe("ffmpeg")) }

func lookTool(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	var dirs []string
	switch runtime.GOOS {
	case "darwin":
		dirs = []string{"/opt/homebrew/bin", "/usr/local/bin", filepath.Join(home, "Library", "Android", "sdk", "platform-tools")}
	case "windows":
		dirs = []string{filepath.Join(os.Getenv("LOCALAPPDATA"), "Android", "Sdk", "platform-tools")}
	default:
		dirs = []string{"/usr/local/bin", "/usr/bin", filepath.Join(home, "Android", "Sdk", "platform-tools")}
	}
	for _, dir := range dirs {
		p := filepath.Join(dir, exe(name))
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// ffmpegPath is the FFmpeg that decodes video where there is no
// VideoToolbox: the bundled one, else one installed.
func (a *App) ffmpegPath() string {
	if p := BundledFFmpeg(); p != "" {
		return p
	}
	return lookTool("ffmpeg")
}

func (a *App) adbPath() string {
	if p := a.cfg.Get().ADBPath; p != "" {
		return p
	}
	if p := BundledADB(); p != "" {
		return p
	}
	if p := lookTool("adb"); p != "" {
		return p
	}
	return "adb"
}

func (a *App) scrcpyPath() string {
	if p := a.cfg.Get().ScrcpyPath; p != "" {
		return p
	}
	if p := BundledScrcpy(); p != "" {
		return p
	}
	return lookTool("scrcpy")
}

// openInScrcpy mirrors a device in scrcpy's own window, with the same adb
// and server: a fallback for what AirDroid's window does not do.
func (a *App) openInScrcpy(serial string) {
	server, _ := a.resolveServer()
	bin := server.Binary
	if bin == "" {
		bin = BundledScrcpyBinary()
	}
	if bin == "" {
		bin = lookTool("scrcpy")
	}
	if bin == "" {
		a.alert("scrcpy was not found", "Choose it in Settings › Advanced.")
		return
	}
	cmd := exec.Command(bin, "--serial="+serial)
	cmd.Env = append(os.Environ(), "ADB="+a.adb.Path())
	if server.Path != "" {
		cmd.Env = append(cmd.Env, "SCRCPY_SERVER_PATH="+server.Path)
	}
	if err := cmd.Start(); err != nil {
		a.alert("Could not start scrcpy", err.Error())
		return
	}
	go cmd.Wait()
}

// resolveServer finds scrcpy-server and its version, off the main thread.
func (a *App) resolveServer() (scrcpy.Server, error) {
	path := a.scrcpyPath()
	a.serverMu.Lock()
	defer a.serverMu.Unlock()
	if path == a.serverFor && (a.server.Path != "" || a.serverErr != nil) {
		return a.server, a.serverErr
	}
	a.serverFor = path
	if path == "" {
		a.server, a.serverErr = scrcpy.Server{}, errors.New("scrcpy was not found: choose it in Settings › Advanced")
		return a.server, a.serverErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a.server, a.serverErr = scrcpy.ResolveServer(ctx, path)
	if a.serverErr == nil && a.server.Version == "" {
		// The server tells its version when told another: learn it then.
		a.server.Version = "0.0"
	}
	return a.server, a.serverErr
}

// learnServerVersion records the version a server reported.
func (a *App) learnServerVersion(v string) {
	a.serverMu.Lock()
	a.server.Version = v
	a.serverMu.Unlock()
}

// toolsChanged applies new adb or scrcpy paths.
func (a *App) toolsChanged() {
	newADB := a.adbPath()
	if newADB != a.adb.Path() {
		a.adb.SetPath(newADB)
		a.startTracking()
	}
	a.serverMu.Lock()
	a.serverFor = "\x00" // resolve again
	a.serverMu.Unlock()
	go a.resolveServer()
}

// --- Devices ---------------------------------------------------------------

func (a *App) startTracking() {
	if a.trackStop != nil {
		a.trackStop()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.trackStop = cancel
	a.trackGen++
	gen := a.trackGen
	client := a.adb
	go func() {
		_ = client.Track(ctx, func(list []adb.Entry) {
			mygo.RunOnMain(func() {
				if gen == a.trackGen {
					a.devicesChanged(list)
				}
			})
		})
	}()
}

func (a *App) devicesChanged(list []adb.Entry) {
	seen := map[string]bool{}
	for _, e := range list {
		seen[e.Serial] = true
		d := a.devices[e.Serial]
		if d == nil {
			d = &Device{}
			a.devices[e.Serial] = d
		}
		wasReady := d.Ready()
		d.Entry = e
		if d.Ready() && (!d.HasInfo || !wasReady) {
			a.loadInfo(e.Serial)
		}
	}
	for serial := range a.devices {
		if !seen[serial] {
			delete(a.devices, serial)
		}
	}
	for serial, m := range a.mirrors {
		d := a.devices[serial]
		m.deviceChanged(d != nil && d.Ready())
	}
	a.refreshMenu()
	if a.add != nil {
		a.add.win.Invalidate()
	}
}

func (a *App) loadInfo(serial string) {
	client := a.adb
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		info, err := client.Info(ctx, serial)
		mygo.RunOnMain(func() {
			d := a.devices[serial]
			if d == nil {
				return
			}
			if err != nil {
				slog.Warn("device info", "serial", serial, "err", err)
				return
			}
			d.Info, d.HasInfo = info, true
			if d.WiFi() {
				a.cfg.RememberWiFi(serial, info.Name(), info.Identity())
			}
			if m := a.mirrors[serial]; m != nil {
				m.setInfo(info)
			}
			a.refreshMenu()
			if a.add != nil {
				a.add.win.Invalidate()
			}
		})
	}()
}

// sortedDevices lists the devices: ready ones first, by name.
func (a *App) sortedDevices() []*Device {
	list := make([]*Device, 0, len(a.devices))
	for _, d := range a.devices {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Ready() != list[j].Ready() {
			return list[i].Ready()
		}
		if list[i].Name() != list[j].Name() {
			return list[i].Name() < list[j].Name()
		}
		return list[i].Serial < list[j].Serial
	})
	return list
}

// connectWiFi connects to a device at addr, then mirrors it.
func (a *App) connectWiFi(addr string, mirrorIt bool) {
	client := a.adb
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err := client.Connect(ctx, addr)
		mygo.RunOnMain(func() {
			if err != nil {
				a.alert("Could not connect to "+addr, err.Error())
				return
			}
			if mirrorIt {
				a.mirrorWhenReady(addr)
			}
		})
	}()
}

// mirrorWhenReady opens a device's window as soon as adb lists it ready.
func (a *App) mirrorWhenReady(serial string) {
	deadline := time.Now().Add(10 * time.Second)
	var try func()
	try = func() {
		if d := a.devices[serial]; d != nil && d.Ready() {
			a.openMirror(serial)
			return
		}
		if time.Now().Before(deadline) {
			time.AfterFunc(300*time.Millisecond, func() { mygo.RunOnMain(try) })
		}
	}
	try()
}

func (a *App) switchToWiFi(serial string) {
	client := a.adb
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		addr, err := client.SwitchToWiFi(ctx, serial)
		mygo.RunOnMain(func() {
			if err != nil {
				a.alert("Could not switch to Wi-Fi", err.Error())
				return
			}
			a.notify("Connected over Wi-Fi", "You can unplug the cable: "+addr)
		})
	}()
}

func (a *App) disconnect(serial string) {
	client := a.adb
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.Disconnect(ctx, serial)
	}()
}

// --- Windows ---------------------------------------------------------------

// openMirror shows the device's window, opening it when needed.
func (a *App) openMirror(serial string) {
	if m := a.mirrors[serial]; m != nil {
		m.win.Show()
		m.win.Focus()
		mygo.App.Focus()
		return
	}
	d := a.devices[serial]
	if d == nil || !d.Ready() {
		return
	}
	m := newMirror(a, d)
	a.mirrors[serial] = m
	a.windowsChanged()
	a.refreshMenu()
}

func (a *App) mirrorClosed(m *mirror) {
	if a.mirrors[m.serial] == m {
		delete(a.mirrors, m.serial)
	}
	a.windowsChanged()
	a.refreshMenu()
}

// focusedMirror is the mirror whose window has the focus.
func (a *App) mirrorOf(w *mygo.Window) *mirror {
	for _, m := range a.mirrors {
		if m.win == w || (w == nil && m.win.IsFocused()) {
			return m
		}
	}
	return nil
}

// windowsChanged shows AirDroid in the Dock while it has windows open, unless
// the user wants it there always.
func (a *App) windowsChanged() {
	if !hasDock() {
		return
	}
	open := len(a.mirrors) > 0 || a.settings != nil || a.add != nil
	if open || !a.cfg.Get().HideDockIconWhenClosed {
		if mygo.App.ActivationPolicy() != mygo.ActivationPolicyRegular {
			mygo.App.SetActivationPolicy(mygo.ActivationPolicyRegular)
		}
	} else if mygo.App.ActivationPolicy() != mygo.ActivationPolicyAccessory {
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	}
}

func (a *App) alert(title, detail string) {
	go func() {
		_, _ = mygo.Dialog.Message(mygo.MessageOptions{
			Type:    mygo.MessageWarning,
			Message: title,
			Detail:  detail,
			Buttons: []string{"OK"},
		})
	}()
}

func (a *App) notify(title, body string) {
	n := mygo.NewNotification(mygo.NotificationOptions{Title: title, Body: body})
	if n != nil {
		_ = n.Show()
	}
}
