package app

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

var recordRed = ui.RGB(255, 59, 48)

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
		// Title bar: drag to move, double-click to zoom.
		ui.Row(c).FillWidth().Height(chromeTop).PaddingStart(bar.Left + 14).PaddingEnd(bar.Right + 14).
			AlignItems(ui.Center).DragWindow().Children(func() {
			ui.Column(c).Gap(1).Shrink(1).Children(func() {
				ui.Text(c, m.title).FontSize(15).Bold().SingleLine().Ellipsis("…")
				if m.subtitle != "" {
					ui.Text(c, m.subtitle).FontSize(12).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
				}
			})
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
			m.sideButton(c, "screenshot", "Screenshot", "Take a screenshot  ⌘S", iconCamera, false, m.shotBusy, m.state != stateStreaming, m.screenshot)
			recLabel, recTip, recIcon := "Record", "Start recording  ⇧⌘R", iconRecord
			if m.recording {
				recLabel, recTip, recIcon = "Stop recording", "Stop recording  ⇧⌘R", iconStop
			}
			m.sideButton(c, "record", recLabel, recTip, recIcon, m.recording, m.recBusy, m.state != stateStreaming && !m.recording, m.toggleRecording)
			if m.recording {
				d := time.Since(m.recStart)
				ui.Text(c, clock(d)).FontSize(12).Bold().TextColor(recordRed).FontFeatures("tnum").
					Label("Recording for " + d.Truncate(time.Second).String()).MarginY(-4)
				c.After(time.Second - d%time.Second)
			}
			m.sideButton(c, "rotate", "Rotate", "Rotate the device  ⌘R", iconRotate, false, false, m.state != stateStreaming, m.rotate)
			gear := m.sideButton(c, "settings", "Settings", "Mirroring settings  ⌘I", iconSettings, m.settingsOpen, false, false, func() {
				m.settingsOpen = !m.settingsOpen
				m.editOpts = m.opts
			})
			m.settingsPopover(c, gear)
		})
		pill(c, func() {
			m.sideButton(c, "recents", "Recent apps", "Recent apps  ⇧⌘A", iconRecents, false, false, m.state != stateStreaming, m.recents)
			m.sideButton(c, "home", "Home", "Home  ⇧⌘H", iconHome, false, false, m.state != stateStreaming, m.home)
			m.sideButton(c, "back", "Back", "Back  ⌘B", iconBack, false, false, m.state != stateStreaming, m.back)
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
	case b.Hovered() || (active && key == "settings"):
		b.Background(t.Text.Alpha(0.07))
	}
	color := t.Text
	if active && key == "record" {
		color = recordRed
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

// settingsPopover edits this device's options, beside the sidebar.
func (m *mirror) settingsPopover(c *ui.Context, anchor ui.Element) {
	t := c.Theme()
	ui.PopoverBase(c, anchor, &m.settingsOpen, func(panel ui.Element) {
		panel.AttachTo(anchor, ui.AnchorTopLeft, ui.AnchorTopRight).Margin(0, 10, 0, 0).
			Width(340).Padding(14).Gap(12).Radius(14).Background(t.Background).
			Border(1, t.Border.Alpha(0.6)).Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.18))
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Mirroring").Bold().FontSize(14).Grow(1)
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
}
