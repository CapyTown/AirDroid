package app

import (
	"github.com/egoist/mygo"

	"github.com/CapyTown/AirDroid/locales"
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
				Label: locales.StopRecordingOf(m.title),
				Click: func(*mygo.MenuItem, *mygo.Window) { mm.toggleRecording() },
			})
		}
	}
	if recording {
		add(mygo.Separator())
	}

	add(&mygo.MenuItem{Label: locales.Devices(), Disabled: true})
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
				ToolTip: d.Info.SubtitleIn(mygo.App.Locale()),
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
			add(&mygo.MenuItem{Label: locales.DeviceUnauthorized(d.Name()), Disabled: true})
		case d.State == "offline":
			add(&mygo.MenuItem{Label: locales.DeviceOffline(d.Name()), Disabled: true})
		case d.State == "no permissions":
			add(&mygo.MenuItem{Label: locales.DeviceNoPermission(d.Name()), Disabled: true})
		default:
			add(&mygo.MenuItem{Label: locales.DeviceState(d.Name(), d.State), Disabled: true})
		}
	}
	if len(devices) == 0 {
		add(&mygo.MenuItem{Label: locales.NoDevices(), Disabled: true})
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
		add(&mygo.MenuItem{Label: locales.ConnectOverWiFi(), Submenu: recent})
	}

	// Moving between USB and Wi-Fi.
	var wifiItems []*mygo.MenuItem
	for _, d := range usb {
		serial := d.Serial
		wifiItems = append(wifiItems, &mygo.MenuItem{
			Label: locales.SwitchToWiFiOf(d.Name()),
			Click: func(*mygo.MenuItem, *mygo.Window) { a.switchToWiFi(serial) },
		})
	}
	for _, d := range wifi {
		serial := d.Serial
		wifiItems = append(wifiItems, &mygo.MenuItem{
			Label: locales.DisconnectOf(d.Name()),
			Click: func(*mygo.MenuItem, *mygo.Window) { a.disconnect(serial) },
		})
	}
	if len(wifiItems) > 0 {
		add(&mygo.MenuItem{Label: locales.Wireless(), Submenu: wifiItems})
	}

	add(mygo.Separator())
	add(&mygo.MenuItem{Label: locales.AddDeviceMenu(), Accelerator: "CmdOrCtrl+N", Click: func(*mygo.MenuItem, *mygo.Window) { a.openAddDevice() }})
	add(mygo.Separator())
	add(&mygo.MenuItem{Label: locales.SettingsMenu(), Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) { a.openSettings() }})
	add(&mygo.MenuItem{Label: locales.AboutApp(), Click: func(*mygo.MenuItem, *mygo.Window) { a.openSettingsPage("about") }})
	add(&mygo.MenuItem{Role: mygo.RoleQuit, Label: locales.Quit()})

	a.tray.SetMenu(mygo.NewMenu(items))
	if recording {
		_ = a.tray.SetIcon(trayIconRecPNG, false)
		a.tray.SetToolTip(locales.TrayRecording())
	} else {
		_ = a.tray.SetIcon(trayIconPNG, true)
		a.tray.SetToolTip("AirDroid")
	}
}

// menuBar is the app's menu bar, shown while a window of AirDroid is open.
func (a *App) menuBar() *mygo.Menu {
	onMirror := func(fn func(m *mirror)) func(*mygo.MenuItem, *mygo.Window) {
		return func(_ *mygo.MenuItem, w *mygo.Window) {
			if m := a.mirrorOf(w); m != nil {
				fn(m)
			}
		}
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "AirDroid", Submenu: []*mygo.MenuItem{
			{Label: locales.AboutApp(), Click: func(*mygo.MenuItem, *mygo.Window) { a.openSettingsPage("about") }},
			mygo.Separator(),
			{Label: locales.SettingsMenu(), Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) { a.openSettings() }},
			mygo.Separator(),
			{Role: mygo.RoleHide},
			{Role: mygo.RoleHideOthers},
			{Role: mygo.RoleUnhide},
			mygo.Separator(),
			{Role: mygo.RoleQuit, Label: locales.Quit()},
		}},
		{Label: locales.MenuFile(), Submenu: []*mygo.MenuItem{
			{Label: locales.AddDeviceMenu(), Accelerator: "CmdOrCtrl+N", Click: func(*mygo.MenuItem, *mygo.Window) { a.openAddDevice() }},
			mygo.Separator(),
			{Role: mygo.RoleClose},
		}},
		{Role: mygo.RoleEditMenu},
		{Label: locales.MenuDevice(), Submenu: []*mygo.MenuItem{
			{Label: locales.TakeScreenshot(), Accelerator: "CmdOrCtrl+S", Click: onMirror(func(m *mirror) { m.screenshot() })},
			{Label: locales.StartStopRecording(), Accelerator: "CmdOrCtrl+Shift+R", Click: onMirror(func(m *mirror) { m.toggleRecording() })},
			{Label: locales.Rotate(), Accelerator: "CmdOrCtrl+R", Click: onMirror(func(m *mirror) { m.rotate() })},
			{Label: locales.MirroringSettingsMenu(), Accelerator: "CmdOrCtrl+I", Click: onMirror(func(m *mirror) { m.toggleSettings() })},
			mygo.Separator(),
			{Label: locales.Back(), Accelerator: "CmdOrCtrl+B", Click: onMirror(func(m *mirror) { m.back() })},
			{Label: locales.Home(), Accelerator: "CmdOrCtrl+Shift+H", Click: onMirror(func(m *mirror) { m.home() })},
			{Label: locales.RecentAppsMenu(), Accelerator: "CmdOrCtrl+Shift+A", Click: onMirror(func(m *mirror) { m.recents() })},
			mygo.Separator(),
			{Label: locales.Notifications(), Accelerator: "CmdOrCtrl+Shift+N", Click: onMirror(func(m *mirror) { m.notifications() })},
			{Label: locales.VolumeUp(), Accelerator: "CmdOrCtrl+Up", Click: onMirror(func(m *mirror) { m.key(akVolumeUp) })},
			{Label: locales.VolumeDown(), Accelerator: "CmdOrCtrl+Down", Click: onMirror(func(m *mirror) { m.key(akVolumeDown) })},
			{Label: locales.Power(), Accelerator: "CmdOrCtrl+P", Click: onMirror(func(m *mirror) { m.key(akPower) })},
			mygo.Separator(),
			{Label: locales.OpenInScrcpy(), Click: onMirror(func(m *mirror) { a.openInScrcpy(m.serial) })},
		}},
		{Role: mygo.RoleWindowMenu},
	})
}
