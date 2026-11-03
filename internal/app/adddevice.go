package app

import (
	"context"
	"image"
	"image/color"
	"net"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"rsc.io/qr"

	"github.com/CapyTown/AirDroid/internal/adb"
	"github.com/CapyTown/AirDroid/locales"
)

// addDeviceWindow is a dialog for attaching a device: over USB, or over
// Wi-Fi by scanning a QR code, typing a pairing code, or an address.
type addDeviceWindow struct {
	app *App
	win *mygo.Window

	mode     int // 0 USB, 1 Wi-Fi
	wifiMode int // 0 QR code, 1 pairing code, 2 address

	// QR pairing, which runs while its page shows.
	qr       adb.QRPairing
	qrImage  *ui.Bitmap
	qrCancel context.CancelFunc
	qrStatus string
	qrErr    bool

	pairAddr    string
	pairCode    string
	connectAddr string
	busy        bool
	status      string
	statusErr   bool
}

func (a *App) openAddDevice() {
	if a.add != nil {
		a.add.win.Show()
		a.add.win.Focus()
		mygo.App.Focus()
		return
	}
	d := &addDeviceWindow{app: a}
	d.win = mygo.NewWindow(mygo.WindowOptions{
		Title:             locales.AddDevice(),
		Width:             480,
		Height:            600,
		TitleBarStyle:     mygo.TitleBarHidden,
		DisableResize:     true,
		DisableMinimize:   true,
		DisableMaximize:   true,
		DisableFullScreen: true,
		Content:           ui.View(d.view),
	})
	d.win.OnClosed(func() {
		d.stopQR()
		a.add = nil
		a.windowsChanged()
	})
	a.add = d
	a.windowsChanged()
	d.win.Center()
	d.win.Focus()
	mygo.App.Focus()
}

func (d *addDeviceWindow) view(c *ui.Context) {
	t := c.Theme()
	if c.Shortcut(0, ui.KeyEscape) || c.Shortcut(ui.Cmd, ui.KeyW) {
		d.win.Close()
	}
	// The QR code's pairing runs only while its page shows.
	if d.mode == 1 && d.wifiMode == 0 {
		d.startQR()
	} else {
		d.stopQR()
	}
	bar := c.TitleBar()
	ui.Column(c).Fill().Children(func() {
		// The heading, from the window's top edge, drags the window.
		ui.Column(c).FillWidth().Padding(max(bar.Height, 28)+4, 24, 16, 24).Gap(4).AlignItems(ui.Center).DragWindow().Children(func() {
			ui.Text(c, locales.AddDevice()).Bold().FontSize(17)
			ui.Text(c, locales.AddDeviceIntro()).FontSize(12.5).TextColor(t.TextMuted)
		})
		d.body(c)
	})
}

func (d *addDeviceWindow) body(c *ui.Context) {
	ui.Column(c).FillWidth().Grow(1).Padding(0, 24, 20, 24).Gap(16).Children(func() {
		ui.Row(c).FillWidth().Justify(ui.Center).Children(func() {
			ui.Segmented(c.Key("mode"), &d.mode, locales.TransportUSB(), locales.TransportWiFi()).Label(locales.Connection())
		})
		ui.Column(c).FillWidth().Grow(1).Gap(14).Children(func() {
			if d.mode == 0 {
				d.usb(c)
			} else {
				d.wifi(c)
			}
		})
		ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
			if ui.Button(c.Key("done"), locales.Done()).Clicked() {
				d.win.Close()
			}
		})
	})
}

func step(c *ui.Context, n, text string) {
	t := c.Theme()
	ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
		ui.Text(c, n).FontSize(11).Bold().TextColor(t.AccentText).Background(t.Accent).
			Size(18, 18).Radius(9).TextAlign(ui.Center).FixedLineHeight(18)
		ui.Text(c, text).FontSize(12.5).Wrap().Grow(1).Shrink(1)
	})
}

func (d *addDeviceWindow) usb(c *ui.Context) {
	t := c.Theme()
	section(c, func() {
		ui.Column(c).PaddingY(12).Gap(10).Children(func() {
			step(c, "1", locales.UsbStep1())
			step(c, "2", locales.UsbStep2())
			step(c, "3", locales.UsbStep3(thisComputer()))
		})
	})
	ui.Text(c, locales.Devices()).Bold().FontSize(12.5).PaddingStart(4)
	devices := d.app.sortedDevices()
	any := false
	section(c, func() {
		for _, dev := range devices {
			if dev.WiFi() {
				continue
			}
			any = true
			d.deviceRow(c, dev)
		}
		if !any {
			ui.Row(c).PaddingY(14).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(16, 16)
				ui.Text(c, locales.WaitingUSB()).FontSize(12.5).TextColor(t.TextMuted)
			})
		}
	})
}

func (d *addDeviceWindow) deviceRow(c *ui.Context, dev *Device) {
	t := c.Theme()
	serial := dev.Serial
	ui.Row(c.Key(serial)).FillWidth().MinHeight(52).PaddingY(8).Gap(12).AlignItems(ui.Center).Children(func() {
		icon := iconUSB
		if dev.WiFi() {
			icon = iconWiFi
		}
		ui.Icon(c, icon).FontSize(20).TextColor(t.TextMuted)
		ui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
			ui.Text(c, dev.Name()).FontSize(13).SingleLine().Ellipsis("…")
			sub := dev.Info.Subtitle()
			switch dev.State {
			case "device":
			case "unauthorized":
				sub = locales.TapAllow()
			case "offline":
				sub = locales.OfflineReplug()
			default:
				sub = dev.State
			}
			if sub == "" {
				sub = serial
			}
			ui.Text(c, sub).FontSize(11.5).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
		})
		if dev.Ready() {
			if !dev.WiFi() {
				if ui.Button(c.Key("wifi"), locales.UseWiFi()).Tooltip(locales.UseWiFiTip()).Clicked() {
					d.app.switchToWiFi(serial)
				}
			}
			if ui.PrimaryButton(c.Key("mirror"), locales.Mirror()).Clicked() {
				d.app.openMirror(serial)
				d.win.Close()
			}
		}
	})
}

func (d *addDeviceWindow) wifi(c *ui.Context) {
	ui.Row(c).FillWidth().Justify(ui.Center).Children(func() {
		ui.Tabs(c.Key("wifiMode"), &d.wifiMode, locales.QrCode(), locales.PairingCode(), locales.IpAddress())
	})
	switch d.wifiMode {
	case 0:
		d.qrPage(c)
	case 1:
		d.codePage(c)
	case 2:
		d.addressPage(c)
	}
	d.statusLine(c)
	// Wi-Fi devices attached now.
	var list []*Device
	for _, dev := range d.app.sortedDevices() {
		if dev.WiFi() {
			list = append(list, dev)
		}
	}
	if len(list) > 0 {
		section(c, func() {
			for _, dev := range list {
				d.deviceRow(c, dev)
			}
		})
	}
}

func (d *addDeviceWindow) statusLine(c *ui.Context) {
	t := c.Theme()
	if d.status == "" {
		return
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).PaddingStart(4).Children(func() {
		switch {
		case d.busy:
			ui.Spinner(c).Size(14, 14)
		case d.statusErr:
			ui.Icon(c, iconAlert).FontSize(15).TextColor(t.Danger)
		default:
			ui.Icon(c, iconCheck).FontSize(15).TextColor(t.Success)
		}
		color := t.TextMuted
		if d.statusErr {
			color = t.Danger
		}
		ui.Text(c, d.status).FontSize(12).TextColor(color).Wrap().Shrink(1)
	})
}

func (d *addDeviceWindow) qrPage(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).FillWidth().Gap(18).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(164, 164).Padding(10).Radius(12).Background(ui.RGB(255, 255, 255)).Children(func() {
			if d.qrImage != nil {
				ui.Image(c, d.qrImage).Size(144, 144).Label(locales.PairingQR())
			}
		})
		ui.Column(c).Grow(1).Shrink(1).Gap(10).Children(func() {
			step(c, "1", locales.QrStep1(thisComputer()))
			step(c, "2", locales.QrStep2())
			step(c, "3", locales.QrStep3())
		})
	})
	ui.Row(c).Gap(8).AlignItems(ui.Center).PaddingStart(4).Children(func() {
		if !d.qrErr {
			ui.Spinner(c).Size(14, 14)
		} else {
			ui.Icon(c, iconAlert).FontSize(15).TextColor(t.Danger)
		}
		color := t.TextMuted
		if d.qrErr {
			color = t.Danger
		}
		ui.Text(c, d.qrStatus).FontSize(12).TextColor(color).Wrap().Shrink(1)
		if d.qrErr && ui.Button(c.Key("qrRetry"), locales.TryAgain()).Clicked() {
			d.stopQR()
		}
	})
}

func (d *addDeviceWindow) codePage(c *ui.Context) {
	section(c, func() {
		ui.Column(c).PaddingY(12).Gap(10).Children(func() {
			step(c, "1", locales.CodeStep1())
			step(c, "2", locales.CodeStep2())
		})
	})
	section(c, func() {
		formRow(c, locales.IpAndPort(), "", func() {
			ui.TextInput(c.Key("pairAddr"), &d.pairAddr).Placeholder("192.168.1.20:37099").Width(200).SingleLine().Label(locales.PairingAddress())
		})
		formRow(c, locales.PairingCode(), "", func() {
			ui.TextInput(c.Key("pairCode"), &d.pairCode).Placeholder("123456").Width(200).SingleLine().Label(locales.PairingCode())
		})
	})
	ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
		ok := validAddr(d.pairAddr) && len(strings.TrimSpace(d.pairCode)) >= 6
		if ui.PrimaryButton(c.Key("pair"), locales.Pair()).Disabled(!ok || d.busy).Clicked() {
			d.pairWithCode()
		}
	})
}

func (d *addDeviceWindow) addressPage(c *ui.Context) {
	t := c.Theme()
	section(c, func() {
		ui.Column(c).PaddingY(12).Gap(10).Children(func() {
			step(c, "1", locales.AddrStep1())
			step(c, "2", locales.AddrStep2())
		})
	})
	section(c, func() {
		formRow(c, locales.IpAndPort(), "", func() {
			in := ui.TextInput(c.Key("connectAddr"), &d.connectAddr).Placeholder("192.168.1.20:5555").Width(200).SingleLine().Label(locales.Address())
			if in.Submitted() && d.connectAddr != "" {
				d.connect(withPort(d.connectAddr))
			}
		})
	})
	ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
		if ui.PrimaryButton(c.Key("connect"), locales.Connect()).Disabled(strings.TrimSpace(d.connectAddr) == "" || d.busy).Clicked() {
			d.connect(withPort(d.connectAddr))
		}
	})
	known := d.app.cfg.Get().KnownWiFi
	if len(known) > 0 {
		ui.Text(c, locales.Recent()).Bold().FontSize(12.5).PaddingStart(4)
		section(c, func() {
			for _, k := range known {
				addr := k.Address
				ui.Row(c.Key(addr)).FillWidth().MinHeight(44).PaddingY(6).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).Shrink(1).Children(func() {
						name := k.Name
						if name == "" {
							name = addr
						}
						ui.Text(c, name).FontSize(13).SingleLine().Ellipsis("…")
						ui.Text(c, addr).FontSize(11.5).TextColor(t.TextMuted)
					})
					if ui.Button(c.Key("go"), locales.Connect()).Disabled(d.busy).Clicked() {
						d.connect(addr)
					}
					forget := ui.ButtonBase(c.Key("forget")).Size(24, 24).Radius(12).AlignItems(ui.Center).Justify(ui.Center).
						Label(locales.ForgetOf(addr)).Tooltip(locales.Forget())
					if forget.Hovered() {
						forget.Background(t.Text.Alpha(0.08))
					}
					forget.Children(func() { ui.Icon(c, iconClose).FontSize(13).TextColor(t.TextMuted) })
					if forget.Clicked() {
						d.app.cfg.ForgetWiFi(addr)
						d.app.refreshMenu()
					}
				})
			}
		})
	}
}

func validAddr(s string) bool {
	host, port, err := net.SplitHostPort(strings.TrimSpace(s))
	return err == nil && host != "" && port != ""
}

func withPort(s string) string {
	s = strings.TrimSpace(s)
	if _, _, err := net.SplitHostPort(s); err != nil {
		return net.JoinHostPort(s, "5555")
	}
	return s
}

// pairStage says how far pairing got.
func pairStage(s adb.PairStage) string {
	switch s {
	case adb.PairWaiting:
		return locales.WaitingScan()
	case adb.PairPairing:
		return locales.Pairing()
	default:
		return locales.ConnectingEllipsis()
	}
}

func (d *addDeviceWindow) setStatus(text string, busy, isErr bool) {
	d.status, d.busy, d.statusErr = text, busy, isErr
}

func (d *addDeviceWindow) connect(addr string) {
	d.setStatus(locales.ConnectingToAddr(addr), true, false)
	client := d.app.adb
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err := client.Connect(ctx, addr)
		d.win.Update(func() {
			if err != nil {
				d.setStatus(err.Error(), false, true)
				return
			}
			d.setStatus(locales.ConnectedTo(addr), false, false)
			d.app.mirrorWhenReady(addr)
		})
	}()
}

func (d *addDeviceWindow) pairWithCode() {
	addr, code := strings.TrimSpace(d.pairAddr), strings.TrimSpace(d.pairCode)
	client := d.app.adb
	d.setStatus(locales.Pairing(), true, false)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		connected, err := client.PairCode(ctx, addr, code, "", func(s adb.PairStage) {
			d.win.Update(func() { d.setStatus(pairStage(s), true, false) })
		})
		d.win.Update(func() {
			if err != nil {
				d.setStatus(err.Error(), false, true)
				return
			}
			d.pairCode = ""
			d.setStatus(locales.PairedConnected(connected), false, false)
			d.app.mirrorWhenReady(connected)
		})
	}()
}

// startQR shows a new code and waits for a phone to scan it.
func (d *addDeviceWindow) startQR() {
	if d.qrCancel != nil {
		return
	}
	d.qr = adb.NewQRPairing()
	d.qrImage = qrBitmap(d.qr.Payload())
	d.qrStatus, d.qrErr = locales.WaitingScan(), false
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	d.qrCancel = cancel
	client := d.app.adb
	q := d.qr
	go func() {
		addr, err := client.PairQR(ctx, q, func(s adb.PairStage) {
			d.win.Update(func() {
				if d.qr == q {
					d.qrStatus = pairStage(s)
				}
			})
		})
		d.win.Update(func() {
			if d.qr != q {
				return
			}
			if err != nil {
				if ctx.Err() == context.Canceled {
					return
				}
				d.qrStatus, d.qrErr = locales.PairingFailed(err.Error()), true
				return
			}
			d.qrStatus = locales.PairedConnected(addr)
			d.app.mirrorWhenReady(addr)
		})
	}()
}

func (d *addDeviceWindow) stopQR() {
	if d.qrCancel != nil {
		d.qrCancel()
		d.qrCancel = nil
		d.qr = adb.QRPairing{}
	}
}

// qrBitmap draws a QR code, a pixel per module, which the view scales up
// in squares.
func qrBitmap(text string) *ui.Bitmap {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil
	}
	const quiet = 1
	n := code.Size + 2*quiet
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			c := color.RGBA{255, 255, 255, 255}
			if code.Black(x-quiet, y-quiet) {
				c = color.RGBA{0, 0, 0, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return ui.NewBitmap(img).Pixelated()
}
