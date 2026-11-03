package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/CapyTown/AirDroid/internal/config"
)

// settingsWindow is the app's settings: pages chosen in a sidebar on the
// left, the page on the right, as in macOS's System Settings.
type settingsWindow struct {
	app  *App
	win  *mygo.Window
	page string

	cfg         config.Config
	openAtLogin bool

	adbCustom    bool
	scrcpyCustom bool
	adbStatus    string
	scrcpyStatus string
	checking     bool
}

var settingsPages = []struct {
	id, name string
	icon     *ui.SVG
}{
	{"general", "General", iconSettings},
	{"mirroring", "Mirroring", iconMirror},
	{"shortcuts", "Shortcuts", iconKeyboard},
	{"advanced", "Advanced", iconTools},
}

func pageName(id string) string {
	for _, p := range settingsPages {
		if p.id == id {
			return p.name
		}
	}
	return ""
}

func (a *App) openSettings() {
	if a.settings != nil {
		a.settings.win.Show()
		a.settings.win.Focus()
		mygo.App.Focus()
		return
	}
	s := &settingsWindow{app: a, page: "general", cfg: a.cfg.Get(), openAtLogin: mygo.App.OpenAtLogin()}
	s.adbCustom = s.cfg.ADBPath != ""
	s.scrcpyCustom = s.cfg.ScrcpyPath != ""
	s.win = mygo.NewWindow(mygo.WindowOptions{
		Title:             "Settings",
		Width:             760,
		Height:            600,
		MinWidth:          660,
		MinHeight:         440,
		TitleBarStyle:     mygo.TitleBarHidden,
		DisableMinimize:   true,
		DisableMaximize:   true,
		DisableFullScreen: true,
		StateKey:          "settings",
		Content:           ui.View(s.view),
	})
	s.win.OnClosed(func() {
		a.settings = nil
		a.windowsChanged()
	})
	a.settings = s
	a.windowsChanged()
	s.win.Focus()
	mygo.App.Focus()
	s.checkTools()
}

func (s *settingsWindow) save(fn func(c *config.Config)) {
	_ = s.app.cfg.Update(fn)
	s.cfg = s.app.cfg.Get()
}

func (s *settingsWindow) view(c *ui.Context) {
	t := c.Theme()
	if c.Shortcut(ui.Cmd, ui.KeyW) {
		s.win.Close()
	}
	for i, p := range settingsPages {
		if c.Shortcut(ui.Cmd, ui.Key1+ui.Key(i)) {
			s.page = p.id
		}
	}
	bar := c.TitleBar()
	top := max(bar.Height, 28)
	sidebarFill := ui.RGBA(0, 0, 0, 0.035)
	if t.Dark {
		sidebarFill = ui.RGBA(255, 255, 255, 0.04)
	}
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		// The sidebar, under the window controls.
		ui.Column(c).Width(200).Background(sidebarFill).Children(func() {
			ui.Box(c).FillWidth().Height(top + 12).DragWindow()
			ui.Sidebar(c.Key("pages"), &s.page, func() {
				for _, p := range settingsPages {
					ui.SidebarItem(c.Key(p.id), p.id, p.icon, p.name)
				}
			}).FillWidth().Grow(1).Padding(0, 10).Background(ui.Transparent)
		})
		ui.Box(c).Width(1).Background(t.Border.Alpha(0.6))
		// The page.
		ui.Column(c).Grow(1).Shrink(1).Children(func() {
			ui.Row(c).FillWidth().Height(top+24).Padding(top/2, 28, 0, 28).AlignItems(ui.Center).DragWindow().Children(func() {
				ui.Text(c, pageName(s.page)).Bold().FontSize(17)
			})
			ui.Scroll(c.Key("page-" + s.page)).FillWidth().Grow(1).Children(func() {
				ui.Column(c).FillWidth().Padding(8, 28, 28, 28).Gap(10).Children(func() {
					switch s.page {
					case "mirroring":
						s.mirroring(c)
					case "shortcuts":
						s.shortcuts(c)
					case "advanced":
						s.advanced(c)
					default:
						s.general(c)
					}
				})
			})
		})
	})
}

// sectionHeading names the section under it.
func sectionHeading(c *ui.Context, title string) {
	ui.Text(c, title).Bold().FontSize(13).Padding(10, 4, 0, 4)
}

func (s *settingsWindow) general(c *ui.Context) {
	t := c.Theme()
	sectionHeading(c, "Startup")
	section(c, func() {
		if switchRow(c, "login", "Open at login", "Starts in the background, without opening a window.", &s.openAtLogin) {
			on := s.openAtLogin
			go func() {
				err := mygo.App.SetOpenAtLogin(on)
				s.win.Update(func() {
					if err != nil {
						s.openAtLogin = !on
						s.app.alert("Could not change Open at login", err.Error())
					}
				})
			}()
		}
		hide := s.cfg.HideDockIconWhenClosed
		if hasDock() && switchRow(c, "dock", "Hide Dock icon when closed", "With AirDroid in the menu bar, closing its windows takes it out of the Dock.", &hide) {
			s.save(func(c *config.Config) { c.HideDockIconWhenClosed = hide })
			s.app.windowsChanged()
		}
	})
	sectionHeading(c, "Screenshot")
	section(c, func() {
		formRow(c, "Save screenshots to", displayPath(s.cfg.ScreenshotDir), func() {
			if ui.Button(c.Key("shotDir"), "Choose…").Clicked() {
				s.chooseDir("Save Screenshots To", s.cfg.ScreenshotDir, func(dir string) {
					s.save(func(c *config.Config) { c.ScreenshotDir = dir })
				})
			}
		})
		copyShot := s.cfg.CopyScreenshot
		if switchRow(c, "copyShot", "Copy screenshots", "Put each screenshot on the clipboard, too.", &copyShot) {
			s.save(func(c *config.Config) { c.CopyScreenshot = copyShot })
		}
	})

	sectionHeading(c, "Record")
	section(c, func() {
		formRow(c, "Save recordings to", displayPath(s.cfg.RecordingDir), func() {
			if ui.Button(c.Key("recDir"), "Choose…").Clicked() {
				s.chooseDir("Save Recordings To", s.cfg.RecordingDir, func(dir string) {
					s.save(func(c *config.Config) { c.RecordingDir = dir })
				})
			}
		})
		format := s.cfg.RecordingFormat
		if segmentedRow(c, "format", "Format", "Recordings keep the phone's own video, as it was encoded: nothing is encoded again.", &format,
			[]string{config.FormatMOV, config.FormatMP4}, []string{"MOV", "MP4"}) {
			s.save(func(c *config.Config) { c.RecordingFormat = format })
		}
		recAudio := s.cfg.RecordAudio
		sub := "The phone's sound goes into MOV movies."
		if s.cfg.RecordingFormat == config.FormatMP4 {
			sub = "MP4 recordings hold video only: choose MOV to keep the sound."
		}
		if switchRow(c, "recAudio", "Record device audio", sub, &recAudio) {
			s.save(func(c *config.Config) { c.RecordAudio = recAudio })
		}
	})

	sectionHeading(c, "About")
	section(c, func() {
		version := mygo.App.Version()
		if version == "" {
			version = "development build"
		}
		formRow(c, "AirDroid "+version, "Updates are published on GitHub.", func() {
			if ui.Button(c.Key("updates"), "Check for Updates…").Clicked() {
				mygo.Shell.OpenExternal(ReleasesURL)
			}
		})
		formRow(c, "Feedback", "Tell of a bug, or an idea.", func() {
			ui.Link(c.Key("feedback"), "Send Feedback…", IssuesURL).TextColor(t.Accent)
		})
	})
}

// shortcuts lists the keys and clicks of AirDroid.
func (s *settingsWindow) shortcuts(c *ui.Context) {
	sectionNote(c, "In a device's window. The keys AirDroid does not take go to the phone.")
	groups := []struct {
		title string
		rows  [][2]string
	}{
		{"Capture", [][2]string{{"Take a screenshot", keys("S")}, {"Start or stop recording", keys("Shift+R")}}},
		{"Device", [][2]string{
			{"Rotate", keys("R")},
			{"Back", keys("B")},
			{"Home", keys("Shift+H")},
			{"Recent apps", keys("Shift+A")},
			{"Pull down the notifications", keys("Shift+N")},
			{"Volume up", keys("↑")},
			{"Volume down", keys("↓")},
			{"Power", keys("P")},
			{"Paste the computer's clipboard on the phone", keys("V")},
		}},
		{"AirDroid", [][2]string{
			{"This device's mirroring settings", keys("I")},
			{"Add Device", keys("N")},
			{"Settings", keys(",")},
		}},
		{"Mouse", [][2]string{
			{"Tap, and swipe", "Click, drag"},
			{"Back", "Right-click"},
			{"Home", "Middle-click"},
			{"Scroll", "Wheel, trackpad"},
		}},
	}
	for _, g := range groups {
		sectionHeading(c, g.title)
		section(c, func() {
			for _, r := range g.rows {
				formRow(c, r[0], "", func() { keycap(c, r[1]) })
			}
		})
	}
}

// keycap shows a shortcut as keys do.
func keycap(c *ui.Context, k string) {
	t := c.Theme()
	ui.Text(c, k).FontSize(12).FontFeatures("tnum").Padding(3, 8).Radius(6).
		Background(t.Background).Border(1, t.Border).TextColor(t.Text)
}

func (s *settingsWindow) mirroring(c *ui.Context) {
	sectionNote(c, "What a device starts with. Each device can change them from the gear of its window's sidebar.")
	o := s.cfg.Mirror
	if mirrorOptionsForm(c, &o, false) {
		s.save(func(c *config.Config) { c.Mirror = o })
		// Devices without options of their own follow the defaults.
		for _, m := range s.app.mirrors {
			if !m.override {
				m.applyOptionsDefault(o)
			}
		}
	}
	if len(s.cfg.Devices) > 0 {
		has := false
		for _, p := range s.cfg.Devices {
			if p != nil && p.Override != nil {
				has = true
			}
		}
		if has {
			section(c, func() {
				formRow(c, "Devices with their own options", "", func() {
					if ui.Button(c.Key("resetDevices"), "Reset All").Clicked() {
						s.save(func(c *config.Config) {
							for _, p := range c.Devices {
								if p != nil {
									p.Override = nil
								}
							}
						})
						for _, m := range s.app.mirrors {
							m.override = false
							m.applyOptionsDefault(s.cfg.Mirror)
						}
					}
				})
			})
		}
	}
}

func (s *settingsWindow) advanced(c *ui.Context) {
	t := c.Theme()
	sectionNote(c, "AirDroid comes with adb and scrcpy. Choose others to use, such as the ones Homebrew or Android Studio install.")
	section(c, func() {
		s.toolRow(c, "adb", "adb", &s.adbCustom, s.cfg.ADBPath, s.adbStatus, BundledADB(), func(path string) {
			s.save(func(c *config.Config) { c.ADBPath = path })
		})
	})
	section(c, func() {
		s.toolRow(c, "scrcpy", "scrcpy", &s.scrcpyCustom, s.cfg.ScrcpyPath, s.scrcpyStatus, BundledScrcpy(), func(path string) {
			s.save(func(c *config.Config) { c.ScrcpyPath = path })
		})
	})
	section(c, func() {
		formRow(c, "adb server", "Restart it when devices stop showing up.", func() {
			if ui.Button(c.Key("restartADB"), "Restart").Disabled(s.checking).Clicked() {
				client := s.app.adb
				s.checking = true
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					_ = client.KillServer(ctx)
					_ = client.StartServer(ctx)
					s.win.Update(func() { s.checking = false })
				}()
			}
		})
		formRow(c, "Settings file", displayPath(config.DefaultPath()), func() {
			if ui.Button(c.Key("revealConfig"), revealLabel()).Clicked() {
				mygo.Shell.ShowItemInFolder(config.DefaultPath())
			}
		})
	})
	_ = t
}

// toolRow chooses between the bundled tool and one of the user's.
func (s *settingsWindow) toolRow(c *ui.Context, key, name string, custom *bool, path, status, bundledPath string, set func(string)) {
	t := c.Theme()
	before := *custom
	sub := status
	formRow(c, name, sub, func() {
		idx := 0
		if *custom {
			idx = 1
		}
		ui.Segmented(c.Key(key+"Mode"), &idx, "Bundled", "Custom").Label(name + " source")
		*custom = idx == 1
	})
	if before != *custom && !*custom {
		set("")
		s.toolsChanged()
	}
	if *custom {
		formRow(c, "Path", "", func() {
			p := path
			in := ui.TextInput(c.Key(key+"Path"), &p).Placeholder("/opt/homebrew/bin/" + name).Width(260).SingleLine()
			if in.Submitted() {
				set(strings.TrimSpace(p))
				s.toolsChanged()
			}
			if ui.Button(c.Key(key+"Choose"), "Choose…").Clicked() {
				go func() {
					paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: s.win, Title: "Choose " + name, ShowHiddenFiles: true})
					if err != nil || len(paths) == 0 {
						return
					}
					s.win.Update(func() {
						set(paths[0])
						s.toolsChanged()
					})
				}()
			}
		})
	} else if bundledPath == "" {
		ui.Text(c, "This build has no bundled "+name+": AirDroid looks for one on your PATH.").FontSize(11.5).TextColor(t.TextMuted).Padding(0, 0, 10, 0)
	}
}

func (s *settingsWindow) toolsChanged() {
	s.app.toolsChanged()
	s.checkTools()
}

// checkTools runs the tools to show their versions.
func (s *settingsWindow) checkTools() {
	a := s.app
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		adbStatus := a.adb.Path()
		if v, err := a.adb.Version(ctx); err == nil {
			adbStatus = "adb " + v + " · " + displayPath(a.adb.Path())
		} else {
			adbStatus = "Not working: " + err.Error()
		}
		scrcpyStatus := ""
		if srv, err := a.resolveServer(); err == nil {
			v := srv.Version
			if v == "" || v == "0.0" {
				v = "version learned on first use"
			}
			scrcpyStatus = "scrcpy-server " + v + " · " + displayPath(srv.Path)
		} else {
			scrcpyStatus = "Not found: " + err.Error()
		}
		s.win.Update(func() { s.adbStatus, s.scrcpyStatus = adbStatus, scrcpyStatus })
	}()
}

func (s *settingsWindow) chooseDir(title, current string, set func(string)) {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: s.win, Title: title, DefaultPath: current, Directory: true, CreateDirectories: true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		s.win.Update(func() { set(paths[0]) })
	}()
}

// displayPath shortens the home directory to ~.
func displayPath(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}
