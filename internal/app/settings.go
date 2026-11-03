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

// settingsWindow is the app's settings, in tabs along the top as in
// macOS's own settings windows.
type settingsWindow struct {
	app *App
	win *mygo.Window
	tab int

	cfg         config.Config
	openAtLogin bool

	adbCustom    bool
	scrcpyCustom bool
	adbStatus    string
	scrcpyStatus string
	checking     bool
}

var settingsTabs = []struct {
	name string
	icon *ui.SVG
}{
	{"General", iconSettings},
	{"Mirroring", iconMirror},
	{"Advanced", iconTools},
}

func (a *App) openSettings() {
	if a.settings != nil {
		a.settings.win.Show()
		a.settings.win.Focus()
		mygo.App.Focus()
		return
	}
	s := &settingsWindow{app: a, cfg: a.cfg.Get(), openAtLogin: mygo.App.OpenAtLogin()}
	s.adbCustom = s.cfg.ADBPath != ""
	s.scrcpyCustom = s.cfg.ScrcpyPath != ""
	s.win = mygo.NewWindow(mygo.WindowOptions{
		Title:             "General",
		Width:             580,
		Height:            620,
		MinWidth:          520,
		MinHeight:         420,
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
	for i := range settingsTabs {
		if c.Shortcut(ui.Cmd, ui.Key1+ui.Key(i)) {
			s.tab = i
		}
	}
	bar := c.TitleBar()
	ui.Column(c).Fill().Children(func() {
		// Title, then the tabs, as a toolbar of the preference style.
		ui.Column(c).FillWidth().Padding(max(bar.Height, 28)/2-9, 0, 0, 0).Gap(8).AlignItems(ui.Center).DragWindow().Children(func() {
			ui.Text(c, settingsTabs[s.tab].name).Bold().FontSize(13)
			ui.Row(c).Gap(4).Padding(0, 0, 8, 0).FocusGroup(ui.Horizontal).Children(func() {
				for i, tab := range settingsTabs {
					i := i
					b := ui.ButtonBase(c.Key("tab" + tab.name)).Width(84).PaddingY(6).Radius(8).Gap(3).
						Column().AlignItems(ui.Center).Label(tab.name).Selected(s.tab == i)
					color := t.TextMuted
					switch {
					case s.tab == i:
						b.Background(t.Text.Alpha(0.06))
						color = t.Accent
					case b.Hovered():
						b.Background(t.Text.Alpha(0.04))
					}
					b.Children(func() {
						ui.Icon(c, tab.icon).FontSize(22).TextColor(color)
						ui.Text(c, tab.name).FontSize(12).TextColor(color)
					})
					if b.Clicked() {
						s.tab = i
						s.win.SetTitle(tab.name)
					}
				}
			})
		})
		ui.Divider(c)
		ui.Scroll(c.Key("page" + settingsTabs[s.tab].name)).FillWidth().Grow(1).Children(func() {
			ui.Column(c).FillWidth().Padding(20, 24, 24, 24).Gap(18).Children(func() {
				switch s.tab {
				case 0:
					s.general(c)
				case 1:
					s.mirroring(c)
				case 2:
					s.advanced(c)
				}
			})
		})
	})
}

func (s *settingsWindow) general(c *ui.Context) {
	t := c.Theme()
	section(c, func() {
		if switchRow(c, "login", "Open at login", "Starts in the menu bar, without opening a window.", &s.openAtLogin) {
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
		if switchRow(c, "dock", "Hide Dock icon when closed", "With AirDroid in the menu bar, closing its windows takes it out of the Dock.", &hide) {
			s.save(func(c *config.Config) { c.HideDockIconWhenClosed = hide })
			s.app.windowsChanged()
		}
	})
	sectionNote(c, "⌘S screenshot · ⇧⌘R record · ⌘R rotate · ⌘B back · ⇧⌘H home · right-click goes back")

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
			if ui.Button(c.Key("revealConfig"), "Show in Finder").Clicked() {
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
