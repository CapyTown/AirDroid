package app

import (
	"github.com/egoist/mygo"
)

// refreshMenu rebuilds the tray menu from the devices, as they come and
// go: the menu shows the new items the next time it opens.
func (a *App) refreshMenu() {
	if a.tray == nil {
		return
	}
	var items []*mygo.MenuItem
	add := func(it ...*mygo.MenuItem) { items = append(items, it...) }

	recording := false
	for _, m := range a.mirrors {
		if m.recording {
			recording = true
			mm := m
			add(&mygo.MenuItem{
				Label: "Stop Recording " + m.title,
				Click: func(*mygo.MenuItem, *mygo.Window) { mm.toggleRecording() },
			})
		}
	}
	if recording {
		add(mygo.Separator())
	}

	add(&mygo.MenuItem{Label: "Devices", Disabled: true})
	devices := a.sortedDevices()
	var usb, wifi []*Device
	for _, d := range devices {
		dev := d
		label := d.Name() + "  ·  " + d.Transport()
		switch {
		case d.Ready():
			_, mirroring := a.mirrors[d.Serial]
			add(&mygo.MenuItem{
				Label:   label,
				Type:    mygo.MenuItemCheckbox,
				Checked: mirroring,
				ToolTip: d.Info.Subtitle(),
				Click: func(it *mygo.MenuItem, _ *mygo.Window) {
					it.SetChecked(true)
					a.openMirror(dev.Serial)
				},
			})
			if d.WiFi() {
				wifi = append(wifi, d)
			} else {
				usb = append(usb, d)
			}
		case d.State == "unauthorized":
			add(&mygo.MenuItem{Label: d.Name() + " — allow USB debugging on the phone", Disabled: true})
		case d.State == "offline":
			add(&mygo.MenuItem{Label: d.Name() + " — offline", Disabled: true})
		case d.State == "no permissions":
			add(&mygo.MenuItem{Label: d.Name() + " — no permission to use the device", Disabled: true})
		default:
			add(&mygo.MenuItem{Label: d.Name() + " — " + d.State, Disabled: true})
		}
	}
	if len(devices) == 0 {
		add(&mygo.MenuItem{Label: "No devices connected", Disabled: true})
	}

	// Wi-Fi devices that connected before and are not here now.
	var recent []*mygo.MenuItem
	for _, k := range a.cfg.Get().KnownWiFi {
		if _, here := a.devices[k.Address]; here {
			continue
		}
		addr := k.Address
		name := k.Name
		if name == "" {
			name = addr
		}
		recent = append(recent, &mygo.MenuItem{
			Label: name + "  ·  " + addr,
			Click: func(*mygo.MenuItem, *mygo.Window) { a.connectWiFi(addr, true) },
		})
	}
	if len(recent) > 0 {
		add(&mygo.MenuItem{Label: "Connect Over Wi-Fi", Submenu: recent})
	}

	// Moving between USB and Wi-Fi.
	var wifiItems []*mygo.MenuItem
	for _, d := range usb {
		serial := d.Serial
		wifiItems = append(wifiItems, &mygo.MenuItem{
			Label: "Switch " + d.Name() + " to Wi-Fi",
			Click: func(*mygo.MenuItem, *mygo.Window) { a.switchToWiFi(serial) },
		})
	}
	for _, d := range wifi {
		serial := d.Serial
		wifiItems = append(wifiItems, &mygo.MenuItem{
			Label: "Disconnect " + d.Name(),
			Click: func(*mygo.MenuItem, *mygo.Window) { a.disconnect(serial) },
		})
	}
	if len(wifiItems) > 0 {
		add(&mygo.MenuItem{Label: "Wireless", Submenu: wifiItems})
	}

	add(mygo.Separator())
	add(&mygo.MenuItem{Label: "Add Device…", Accelerator: "CmdOrCtrl+N", Click: func(*mygo.MenuItem, *mygo.Window) { a.openAddDevice() }})
	add(mygo.Separator())
	add(&mygo.MenuItem{Label: "Settings…", Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) { a.openSettings() }})
	add(&mygo.MenuItem{Role: mygo.RoleQuit, Label: "Quit MyGo"})

	a.tray.SetMenu(mygo.NewMenu(items))
	if recording {
		_ = a.tray.SetIcon(trayIconRecPNG, false)
		a.tray.SetToolTip("MyGo — recording")
	} else {
		_ = a.tray.SetIcon(trayIconPNG, true)
		a.tray.SetToolTip("MyGo")
	}
}

// menuBar is the app's menu bar, shown while a window of MyGo is open.
func (a *App) menuBar() *mygo.Menu {
	onMirror := func(fn func(m *mirror)) func(*mygo.MenuItem, *mygo.Window) {
		return func(_ *mygo.MenuItem, w *mygo.Window) {
			if m := a.mirrorOf(w); m != nil {
				fn(m)
			}
		}
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "MyGo", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout, Label: "About MyGo"},
			mygo.Separator(),
			{Label: "Settings…", Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) { a.openSettings() }},
			mygo.Separator(),
			{Role: mygo.RoleHide},
			{Role: mygo.RoleHideOthers},
			{Role: mygo.RoleUnhide},
			mygo.Separator(),
			{Role: mygo.RoleQuit, Label: "Quit MyGo"},
		}},
		{Label: "File", Submenu: []*mygo.MenuItem{
			{Label: "Add Device…", Accelerator: "CmdOrCtrl+N", Click: func(*mygo.MenuItem, *mygo.Window) { a.openAddDevice() }},
			mygo.Separator(),
			{Role: mygo.RoleClose},
		}},
		{Role: mygo.RoleEditMenu},
		{Label: "Device", Submenu: []*mygo.MenuItem{
			{Label: "Take Screenshot", Accelerator: "CmdOrCtrl+S", Click: onMirror(func(m *mirror) { m.screenshot() })},
			{Label: "Start or Stop Recording", Accelerator: "CmdOrCtrl+Shift+R", Click: onMirror(func(m *mirror) { m.toggleRecording() })},
			{Label: "Rotate", Accelerator: "CmdOrCtrl+R", Click: onMirror(func(m *mirror) { m.rotate() })},
			{Label: "Mirroring Settings…", Accelerator: "CmdOrCtrl+I", Click: onMirror(func(m *mirror) { m.settingsOpen = !m.settingsOpen; m.win.Invalidate() })},
			mygo.Separator(),
			{Label: "Back", Accelerator: "CmdOrCtrl+B", Click: onMirror(func(m *mirror) { m.back() })},
			{Label: "Home", Accelerator: "CmdOrCtrl+Shift+H", Click: onMirror(func(m *mirror) { m.home() })},
			{Label: "Recent Apps", Accelerator: "CmdOrCtrl+Shift+A", Click: onMirror(func(m *mirror) { m.recents() })},
			mygo.Separator(),
			{Label: "Notifications", Accelerator: "CmdOrCtrl+Shift+N", Click: onMirror(func(m *mirror) { m.notifications() })},
			{Label: "Volume Up", Accelerator: "CmdOrCtrl+Up", Click: onMirror(func(m *mirror) { m.key(akVolumeUp) })},
			{Label: "Volume Down", Accelerator: "CmdOrCtrl+Down", Click: onMirror(func(m *mirror) { m.key(akVolumeDown) })},
			{Label: "Power", Accelerator: "CmdOrCtrl+P", Click: onMirror(func(m *mirror) { m.key(akPower) })},
			mygo.Separator(),
			{Label: "Open in scrcpy Window", Click: onMirror(func(m *mirror) { a.openInScrcpy(m.serial) })},
		}},
		{Role: mygo.RoleWindowMenu},
	})
}
