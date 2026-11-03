package app

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// recordGreen marks a recording that runs.
var recordGreen = ui.RGB(52, 199, 89)

func pillFill(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.08)
	}
	return ui.RGB(242, 242, 244)
}

// view lays the window out as in the design: the title and the device's
// model under the window controls, the picture with rounded corners, and
// beside it the sidebar's two groups, aligned with the picture's top and
// bottom.
func (m *mirror) view(c *ui.Context) {
	t := c.Theme()
	for _, ts := range m.pendingToasts {
		if ts.action != "" {
			c.ToastAction(ts.text, ts.action, ts.fn)
		} else {
			c.Toast(ts.text)
		}
	}
	m.pendingToasts = nil

	W, H := c.Size()
	bar := c.TitleBar()
	ui.Column(c).Fill().Children(func() {
		// Title bar, one line centered on the window controls: the device's
		// name in bold, then "· Android 15". Drag to move, double-click to
		// zoom.
		rowH := bar.Height
		if rowH < 24 {
			rowH = 28
		}
		ui.Row(c).FillWidth().Height(chromeTop).Padding(0, bar.Right+14, max(0, chromeTop-rowH), bar.Left+10).
			AlignItems(ui.Center).Gap(6).DragWindow().Children(func() {
			ui.Text(c, m.title).FontSize(14).Bold().SingleLine().Ellipsis("…").Shrink(0)
			if m.subtitle != "" {
				ui.Text(c, m.subtitle).FontSize(14).TextColor(t.TextMuted).SingleLine().Ellipsis("…").Shrink(1)
			}
		})

		// The picture, as large as the window lets it be at its aspect.
		availW := W - chromeLeft - chromeGap - sidebarWidth - chromeRight
		availH := H - chromeTop - chromeBottom
		vw, vh := availW, availH
		if m.frameW > 0 && m.frameH > 0 {
			aspect := float32(m.frameW) / float32(m.frameH)
			vw, vh = availW, availW/aspect
			if vh > availH {
				vw, vh = availH*aspect, availH
			}
		}
		vw, vh = float32(math.Max(float64(vw), 40)), float32(math.Max(float64(vh), 80))
		ui.Row(c).FillWidth().Grow(1).PaddingStart(chromeLeft).PaddingEnd(chromeRight).PaddingY(0).
			MarginY(0).Justify(ui.Center).AlignItems(ui.Start).Gap(chromeGap).Children(func() {
			m.videoView(c, vw, vh)
			m.sidebarView(c, vh)
		})
	})
}

func (m *mirror) videoView(c *ui.Context, vw, vh float32) {
	t := c.Theme()
	radius := float32(math.Min(18, float64(vw)*0.05))
	box := ui.Box(c.Key("video")).Size(vw, vh).Radius(radius).Clip().
		Background(ui.RGB(16, 16, 18)).
		Focusable().FocusRing(false).Bind(&m.videoFocus).Label("Device screen").
		HandleInput(func(ev ui.InputEvent) bool { return m.input(ev, vw, vh) }).
		TextCaret(ui.Rect{X: vw / 2, Y: vh - 40, W: 1, H: 18}).
		Draw(func(p *ui.Painter, r ui.Rect) {
			// The decoder scales pictures to the pixels they show at.
			s := p.Scale()
			m.target.Store(uint64(uint32(r.W*s))<<32 | uint64(uint32(r.H*s)))
		})
	if m.state == stateStreaming {
		box.Cursor(ui.CursorDefault)
	}
	box.Children(func() {
		if m.bitmap != nil {
			ui.Image(c, m.bitmap).Size(vw, vh).Fit(ui.FillBox)
		}
		switch {
		case m.state == stateConnecting && m.bitmap == nil:
			overlay(c, func() {
				ui.Spinner(c).Label("Connecting")
				ui.Text(c, "Connecting to "+m.title+"…").FontSize(13).TextColor(ui.RGBA(255, 255, 255, 0.8))
			})
		case m.state == stateConnecting:
			overlay(c, func() { ui.Spinner(c).Label("Reconnecting") }).Background(ui.RGBA(0, 0, 0, 0.35))
		case m.state == stateDisconnected:
			overlay(c, func() {
				ui.Icon(c, iconAlert).FontSize(28).TextColor(ui.RGBA(255, 255, 255, 0.85))
				ui.Text(c, m.stateMsg).FontSize(13).TextColor(ui.RGBA(255, 255, 255, 0.85)).TextAlign(ui.Center).Wrap().MaxWidth(vw - 48)
				if d := m.app.devices[m.serial]; d != nil && d.Ready() {
					if ui.PrimaryButton(c.Key("reconnect"), "Reconnect").Clicked() {
						m.connect()
					}
				} else {
					ui.Text(c, "Waiting for the device to come back…").FontSize(12).TextColor(ui.RGBA(255, 255, 255, 0.6))
				}
			}).Background(ui.RGBA(0, 0, 0, 0.55))
		}
		// The flash of a screenshot.
		if since := time.Since(m.flashAt); since < 300*time.Millisecond {
			a := float32(1 - float64(since)/float64(300*time.Millisecond))
			ui.Box(c).Absolute().Left(0).Top(0).Size(vw, vh).Background(ui.RGBA(255, 255, 255, 0.7*a)).IgnoreInput(true)
			c.AnimationFrame()
		}
		// Text an input method composes, until it commits.
		if m.composing != "" {
			ui.Row(c).Absolute().Left(12).Bottom(12).Padding(6, 10).Radius(8).
				Background(t.Inverse.Alpha(0.85)).IgnoreInput(true).Children(func() {
				ui.Text(c, m.composing).TextColor(t.InverseText).Underline()
			})
		}
	})
}

func overlay(c *ui.Context, fn func()) ui.Element {
	return ui.Column(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).
		AlignItems(ui.Center).Justify(ui.Center).Gap(12).Padding(16).Children(fn)
}

// sidebarView is the sidebar: capture and device actions above, Android's
// navigation below.
func (m *mirror) sidebarView(c *ui.Context, vh float32) {
	ui.Column(c).Width(sidebarWidth).Height(vh).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
		pill(c, func() {
			m.sideButton(c, "screenshot", "Screenshot", "Take a screenshot  "+keys("S"), iconCamera, false, m.shotBusy, m.state != stateStreaming, m.screenshot)
			recLabel, recTip, recIcon := "Record", "Start recording  "+keys("Shift+R"), iconRecord
			if m.recording {
				recLabel, recTip, recIcon = "Stop recording", "Stop recording  "+keys("Shift+R"), iconStop
			}
			m.sideButton(c, "record", recLabel, recTip, recIcon, m.recording, m.recBusy, m.state != stateStreaming && !m.recording, m.toggleRecording)
			if m.recording {
				d := time.Since(m.recStart)
				ui.Text(c, clock(d)).FontSize(12).Bold().TextColor(recordGreen).FontFeatures("tnum").
					Label("Recording for " + d.Truncate(time.Second).String()).MarginY(-4)
				c.After(time.Second - d%time.Second)
			}
			screenTip := "Turn the phone's screen off, on again while it is locked"
			if m.opts.TurnScreenOff {
				screenTip = "Turn the phone's screen back on"
			}
			m.sideButton(c, "screen", "Phone screen off", screenTip, iconScreenOff, m.opts.TurnScreenOff, false, m.state != stateStreaming && !m.opts.TurnScreenOff, m.toggleScreenOff)
			m.sideButton(c, "settings", "Settings", "Mirroring settings  "+keys("I"), iconSettings, m.settingsWin != nil, false, false, m.toggleSettings)
		})
		pill(c, func() {
			m.sideButton(c, "recents", "Recent apps", "Recent apps  "+keys("Shift+A"), iconRecents, false, false, m.state != stateStreaming, m.recents)
			m.sideButton(c, "home", "Home", "Home  "+keys("Shift+H"), iconHome, false, false, m.state != stateStreaming, m.home)
			m.sideButton(c, "back", "Back", "Back  "+keys("B"), iconBack, false, false, m.state != stateStreaming, m.back)
		})
	})
}

func pill(c *ui.Context, fn func()) ui.Element {
	return ui.Column(c).Width(sidebarWidth).PaddingY(8).Gap(6).Radius(sidebarWidth / 2).
		Background(pillFill(c.Theme())).AlignItems(ui.Center).Children(fn)
}

// sideButton is a button of the sidebar: an icon that lights as the
// pointer comes over it, with a tip naming it and its shortcut.
func (m *mirror) sideButton(c *ui.Context, key, label, tip string, icon *ui.SVG, active, busy, disabled bool, fn func()) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Size(46, 46).Radius(14).AlignItems(ui.Center).Justify(ui.Center).
		Label(label).Tooltip(tip).Disabled(disabled || busy)
	switch {
	case disabled:
	case b.Pressed():
		b.Background(t.Text.Alpha(0.14))
	case active && key == "screen":
		b.Background(t.Accent.Alpha(0.16))
	case b.Hovered() || (active && key == "settings"):
		b.Background(t.Text.Alpha(0.07))
	}
	color := t.Text
	if active && key == "record" {
		color = recordGreen
	}
	if active && key == "screen" {
		color = t.Accent
	}
	if disabled {
		color = t.Text.Alpha(0.3)
	}
	b.Children(func() {
		if busy && !(key == "record" && m.recording) {
			ui.Spinner(c).Size(20, 20)
		} else {
			ui.Icon(c, icon).FontSize(25).TextColor(color)
		}
	})
	b.OnClick(fn)
	return b
}

func clock(d time.Duration) string {
	s := int(d / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// settingsWidth and settingsHeight size the window of a device's settings,
// which does not resize.
const (
	settingsWidth  = 380
	settingsHeight = 660
)

// toggleSettings opens this device's settings in a window of their own,
// beside the mirror, or closes it.
func (m *mirror) toggleSettings() {
	if m.settingsWin != nil {
		m.settingsWin.Close()
		return
	}
	m.editOpts = m.opts
	b := m.win.Bounds()
	area := mygo.Screen.DisplayNearestPoint(mygo.Point{X: b.X + b.Width/2, Y: b.Y + b.Height/2}).WorkArea
	x := b.X + b.Width + 12
	if x+settingsWidth > area.X+area.Width {
		x = max(area.X, b.X-settingsWidth-12)
	}
	y := max(area.Y, min(b.Y, area.Y+area.Height-settingsHeight-28))
	w := mygo.NewWindow(mygo.WindowOptions{
		Title:             "Mirroring · " + m.title,
		Width:             settingsWidth,
		Height:            settingsHeight,
		UseContentSize:    true,
		X:                 x,
		Y:                 y,
		DisableResize:     true,
		DisableMinimize:   true,
		DisableMaximize:   true,
		DisableFullScreen: true,
		Parent:            m.win,
		Content:           ui.View(m.settingsView),
	})
	w.OnClosed(func() {
		if m.settingsWin == w {
			m.settingsWin = nil
			m.win.Invalidate()
		}
	})
	m.settingsWin = w
	m.win.Invalidate()
}

// settingsView edits this device's options.
func (m *mirror) settingsView(c *ui.Context) {
	t := c.Theme()
	if c.Shortcut(ui.Cmd, ui.KeyW) || c.Shortcut(0, ui.KeyEscape) {
		m.settingsWin.Close()
	}
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().Padding(14, 16, 16, 16).Gap(12).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
					ui.Text(c, m.title).Bold().FontSize(14).SingleLine().Ellipsis("…")
					note := "Following Settings › Mirroring"
					if m.override {
						note = "Options of this device"
					}
					ui.Text(c, note).FontSize(11.5).TextColor(t.TextMuted)
				})
				if m.override {
					if ui.Button(c.Key("defaults"), "Use Defaults").Tooltip("Use the options of Settings › Mirroring").Clicked() {
						m.useDefaults()
					}
				}
			})
			if mirrorOptionsForm(c, &m.editOpts, true) {
				m.applyOptions(m.editOpts)
			}
			ui.Text(c, "Resolution, bit rate, frame rate, codec, audio and staying awake reconnect the stream.").
				FontSize(11).TextColor(t.TextMuted).Wrap()
		})
	})
}
