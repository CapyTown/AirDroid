package app

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/audio"
	"github.com/egoist/mygo/ui"

	"github.com/CapyTown/AirDroid/internal/adb"
	"github.com/CapyTown/AirDroid/internal/config"
	"github.com/CapyTown/AirDroid/internal/media"
	"github.com/CapyTown/AirDroid/internal/mp4"
	"github.com/CapyTown/AirDroid/internal/scrcpy"
	"github.com/CapyTown/AirDroid/internal/video"
)

type mirrorState int

const (
	stateConnecting mirrorState = iota
	stateStreaming
	stateDisconnected
)

// mirror is a device's window: its picture, which takes the pointer and
// the keyboard, and the sidebar of actions beside it.
//
// The stream runs on goroutines: the session reads the device's sockets,
// decodes on the reader's goroutine and hands the recorder the encoded
// frames. What the view reads is changed only on the main thread.
type mirror struct {
	app      *App
	serial   string
	identity string
	title    string
	subtitle string
	win      *mygo.Window

	// Main thread.
	state         mirrorState
	stateMsg      string
	session       *scrcpy.Session
	gen           int // the session generation the stream belongs to
	opts          config.MirrorOptions
	editOpts      config.MirrorOptions
	override      bool
	frameW        int // the stream's size, which input is computed against
	frameH        int
	bitmap        *ui.Bitmap
	shown         func() // returns the picture shown to the decoder's pool
	videoFocus    ui.Handle
	pointerOn     bool
	keysDown      map[ui.Key]int
	composing     string
	recording     bool
	recStart      time.Time
	recBusy       bool // between a click and the recorder's answer
	shotBusy      bool
	flashAt       time.Time
	settingsOpen  bool
	pendingToasts []toast
	sized         bool // the window was fitted to the stream once
	lastClip      string
	clipSeq       uint64
	closed        bool
	decErr        string // why there is no decoder

	// Shared with the stream's goroutines.
	decMu  sync.Mutex // the decoder, used by one session's reader at a time
	dec    video.Decoder
	curGen atomic.Int64 // the generation whose frames count
	rec    *recorder
	pcm    *pcmStream
	player *audio.Player
	frames frameMailbox
	target atomic.Uint64
}

type toast struct {
	text   string
	action string
	fn     func()
}

func newMirror(a *App, d *Device) *mirror {
	m := &mirror{
		app:      a,
		serial:   d.Serial,
		keysDown: map[ui.Key]int{},
	}
	m.setInfo(d.Info)
	if !d.HasInfo {
		m.title = d.Name()
	}
	if m.identity == "" {
		m.identity = d.Serial
	}
	m.opts, m.override = a.cfg.MirrorOptionsFor(m.identity)
	m.editOpts = m.opts

	m.rec = newRecorder(func(r recReport) {
		m.win.Update(func() { m.recorderReport(r) })
	}, func() {
		mygo.RunOnMain(func() { m.send(scrcpy.ResetVideo()) })
	})
	m.frames.m = m
	dec, err := newDecoder(a, m.frames.put, func() {
		mygo.RunOnMain(func() { m.send(scrcpy.ResetVideo()) })
	})
	if err != nil {
		slog.Error("decoder", "err", err)
		m.decErr = err.Error()
	} else {
		m.dec = dec
	}

	m.win = mygo.NewWindow(mygo.WindowOptions{
		Title:         m.title,
		Width:         440,
		Height:        860,
		MinWidth:      300,
		MinHeight:     420,
		TitleBarStyle: mygo.TitleBarHidden,
		AlwaysOnTop:   m.opts.AlwaysOnTop,
		StateKey:      "mirror-" + m.identity,
		Content:       ui.View(m.view),
	})
	m.win.OnClose(func(*mygo.CloseEvent) { m.shutdown() })
	m.win.OnClosed(func() { a.mirrorClosed(m) })
	m.win.Focus()
	mygo.App.Focus()
	m.connect()
	return m
}

func (m *mirror) setInfo(info adb.Info) {
	if info.Serial == "" {
		return
	}
	m.title = info.Name()
	m.subtitle = info.Subtitle()
	if m.identity == "" {
		m.identity = info.Identity()
	}
	if m.win != nil {
		m.win.SetTitle(m.title)
		m.win.Invalidate()
	}
}

// --- The stream ---------------------------------------------------------

// connect starts a session with the current options.
func (m *mirror) connect() {
	if m.decErr != "" {
		m.state, m.stateMsg = stateDisconnected, m.decErr
		return
	}
	if m.session != nil {
		m.session.Close()
		m.session = nil
	}
	m.gen++
	gen := m.gen
	m.curGen.Store(int64(gen))
	m.state, m.stateMsg = stateConnecting, ""
	m.stopAudio()
	opts := m.opts
	codec := media.H264
	if opts.Codec == config.CodecH265 {
		codec = media.H265
	}
	if opts.Audio {
		m.pcm = newPCMStream()
		m.player = audio.NewPlayer(m.pcm)
		m.player.Play()
	}
	p := &pipeline{m: m, gen: gen, pcm: m.pcm, screenOff: opts.TurnScreenOff}
	client := m.app.adb
	serial := m.serial
	go func() {
		server, err := m.app.resolveServer()
		var s *scrcpy.Session
		if err == nil {
			sopts := scrcpy.Options{
				Serial:          serial,
				Server:          server,
				Codec:           codec,
				MaxSize:         opts.MaxSize,
				BitRate:         opts.BitRateMbps * 1_000_000,
				MaxFPS:          opts.MaxFPS,
				Audio:           opts.Audio,
				StayAwake:       opts.StayAwake,
				ShowTouches:     opts.ShowTouches,
				PowerOffOnClose: opts.PowerOffOnClose,
			}
			for attempt := 0; attempt < 2; attempt++ {
				ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
				s, err = scrcpy.Start(ctx, client, sopts, p)
				cancel()
				var vm *scrcpy.VersionMismatchError
				if errors.As(err, &vm) && vm.ServerVersion != sopts.Server.Version {
					m.app.learnServerVersion(vm.ServerVersion)
					sopts.Server.Version = vm.ServerVersion
					continue
				}
				break
			}
		}
		m.win.Update(func() {
			if gen != m.gen || m.closed {
				if s != nil {
					s.Close()
				}
				return
			}
			if err != nil {
				m.state, m.stateMsg = stateDisconnected, err.Error()
				m.stopAudio()
				return
			}
			m.session = s
			if s.AudioCodec == 0 {
				m.stopAudio()
			}
		})
		if s == nil {
			return
		}
		<-s.Done()
		s.Wait()
		err = s.Err()
		m.win.Update(func() {
			if gen != m.gen || m.closed {
				return
			}
			m.session = nil
			m.state = stateDisconnected
			m.stateMsg = "The device disconnected."
			if err != nil {
				m.stateMsg = err.Error()
			}
			m.stopAudio()
			if m.recording {
				m.rec.stop()
			}
		})
	}()
}

func (m *mirror) stopAudio() {
	if m.player != nil {
		m.player.Close()
		m.player = nil
	}
	m.pcm = nil
}

// deviceChanged learns that the device came back or went away.
func (m *mirror) deviceChanged(present bool) {
	if present && m.state == stateDisconnected && m.session == nil && !m.closed {
		m.connect()
	}
}

// shutdown ends the stream and finishes a recording, as the window
// closes or the app quits.
func (m *mirror) shutdown() {
	if m.closed {
		return
	}
	m.closed = true
	m.gen++
	m.curGen.Store(int64(m.gen))
	if m.session != nil {
		m.session.Close()
		m.session = nil
	}
	m.stopAudio()
	m.rec.quit()
	m.app.stopping = append(m.app.stopping, m.rec)
	if m.dec != nil {
		go func() {
			m.decMu.Lock()
			defer m.decMu.Unlock()
			m.dec.Close()
			m.dec = nil
		}()
	}
}

// send sends a control message to the device.
func (m *mirror) send(msg []byte) {
	if m.session != nil {
		m.session.Send(msg)
	}
}

// pipeline takes what one session receives.
type pipeline struct {
	m         *mirror
	gen       int
	pcm       *pcmStream
	screenOff bool
	started   bool
}

// current reports whether the session is still the window's.
func (p *pipeline) current() bool { return int64(p.gen) == p.m.curGen.Load() }

func (p *pipeline) VideoSize(w, h int) {
	if !p.current() {
		return
	}
	p.m.rec.size(w, h)
	p.m.win.Update(func() {
		if p.gen != p.m.gen {
			return
		}
		rotated := p.m.frameW != 0 && (p.m.frameW > p.m.frameH) != (w > h)
		p.m.frameW, p.m.frameH = w, h
		if !p.m.sized || rotated {
			p.m.sized = true
			p.m.fitWindow(rotated)
		}
	})
}

func (p *pipeline) VideoConfig(params *media.ParameterSets) {
	if !p.current() {
		return
	}
	p.m.rec.config(params)
	p.m.decMu.Lock()
	defer p.m.decMu.Unlock()
	if p.m.dec != nil && p.current() {
		if err := p.m.dec.SetFormat(params); err != nil {
			slog.Error("video format", "err", err)
		}
	}
}

func (p *pipeline) VideoFrame(f scrcpy.VideoFrame) {
	if !p.current() {
		return
	}
	p.m.rec.frame(f)
	p.m.decMu.Lock()
	if p.m.dec != nil && p.current() {
		t := p.m.target.Load()
		p.m.dec.SetTargetSize(int(t>>32), int(uint32(t)))
		if err := p.m.dec.Decode(f.Data, f.Key); err != nil {
			slog.Debug("decode", "err", err)
		}
	}
	p.m.decMu.Unlock()
	if !p.started {
		p.started = true
		screenOff := p.screenOff
		p.m.win.Update(func() {
			if p.gen != p.m.gen {
				return
			}
			p.m.state = stateStreaming
			if screenOff {
				p.m.send(scrcpy.SetDisplayPower(false))
			}
			p.m.videoFocus.Focus()
		})
	}
}

func (p *pipeline) AudioPCM(pts int64, pcm []byte) {
	if !p.current() {
		return
	}
	if p.pcm != nil {
		p.pcm.push(pcm)
	}
	p.m.rec.audio(pts, pcm)
}

func (p *pipeline) Clipboard(text string) {
	p.m.win.Update(func() {
		if text == p.m.lastClip {
			return
		}
		p.m.lastClip = text
		mygo.Clipboard.WriteText(text)
	})
}

// frameMailbox hands decoded pictures to the window, keeping only the
// newest when the window draws slower than frames come.
type frameMailbox struct {
	m         *mirror
	mu        sync.Mutex
	pending   *image.RGBA
	release   func()
	scheduled bool
}

func (b *frameMailbox) put(img *image.RGBA, release func()) {
	b.mu.Lock()
	if b.pending != nil {
		b.release()
	}
	b.pending, b.release = img, release
	schedule := !b.scheduled
	b.scheduled = true
	b.mu.Unlock()
	if schedule {
		b.m.win.Update(b.show)
	}
}

// show runs on the main thread.
func (b *frameMailbox) show() {
	b.mu.Lock()
	img, release := b.pending, b.release
	b.pending, b.release, b.scheduled = nil, nil, false
	b.mu.Unlock()
	if img == nil {
		return
	}
	m := b.m
	if m.closed {
		release()
		return
	}
	if m.bitmap == nil {
		m.bitmap = ui.NewBitmap(img)
	} else {
		m.bitmap.Update(img)
	}
	if m.shown != nil {
		m.shown()
	}
	m.shown = release
}

// --- Window size ----------------------------------------------------------

const (
	chromeTop    = 56
	chromeBottom = 12
	chromeLeft   = 12
	chromeGap    = 12
	sidebarWidth = 62
	chromeRight  = 14
)

// fitWindow sizes the window to the stream's shape: as tall as most of the
// screen for a phone held upright, as wide for one turned.
func (m *mirror) fitWindow(keepCenter bool) {
	if m.frameW == 0 || m.frameH == 0 || m.win.IsFullScreen() {
		return
	}
	b := m.win.Bounds()
	center := mygo.Point{X: b.X + b.Width/2, Y: b.Y + b.Height/2}
	area := mygo.Screen.DisplayNearestPoint(center).WorkArea
	aspect := float64(m.frameW) / float64(m.frameH)
	maxH := float64(area.Height)*0.86 - chromeTop - chromeBottom
	maxW := float64(area.Width)*0.86 - chromeLeft - chromeGap - sidebarWidth - chromeRight
	vh := math.Min(maxH, 920)
	vw := vh * aspect
	if vw > maxW {
		vw = maxW
		vh = vw / aspect
	}
	w := int(vw) + chromeLeft + chromeGap + sidebarWidth + chromeRight
	h := int(vh) + chromeTop + chromeBottom
	m.win.SetContentSize(w, h)
	if keepCenter {
		nb := m.win.Bounds()
		m.win.SetPosition(center.X-nb.Width/2, max(area.Y, center.Y-nb.Height/2))
	}
}

// --- Actions ---------------------------------------------------------------

func (m *mirror) key(code int) {
	m.send(scrcpy.InjectKeycode(scrcpy.ActionDown, code, 0, 0))
	m.send(scrcpy.InjectKeycode(scrcpy.ActionUp, code, 0, 0))
}

func (m *mirror) back() {
	m.send(scrcpy.BackOrScreenOn(scrcpy.ActionDown))
	m.send(scrcpy.BackOrScreenOn(scrcpy.ActionUp))
}

func (m *mirror) home()          { m.key(akHome) }
func (m *mirror) recents()       { m.key(akAppSwitch) }
func (m *mirror) rotate()        { m.send(scrcpy.RotateDevice()) }
func (m *mirror) notifications() { m.send(scrcpy.ExpandNotificationPanel()) }

// screenshot saves a PNG of the device's screen, at its full resolution.
func (m *mirror) screenshot() {
	if m.shotBusy {
		return
	}
	m.shotBusy = true
	m.flashAt = time.Now()
	cfg := m.app.cfg.Get()
	client := m.app.adb
	serial, name := m.serial, m.title
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		data, err := client.Screencap(ctx, serial)
		var path string
		if err == nil {
			path = filepath.Join(cfg.ScreenshotDir, recordingName(name, time.Now().Format("2006-01-02 at 15.04.05"))+".png")
			if err = os.MkdirAll(cfg.ScreenshotDir, 0o755); err == nil {
				err = os.WriteFile(path, data, 0o644)
			}
		}
		if err == nil && cfg.CopyScreenshot {
			mygo.Clipboard.WriteImage(data)
		}
		m.win.Update(func() {
			m.shotBusy = false
			if err != nil {
				m.pendingToasts = append(m.pendingToasts, toast{text: "Screenshot failed: " + err.Error()})
				return
			}
			text := "Screenshot saved"
			if cfg.CopyScreenshot {
				text = "Screenshot saved and copied"
			}
			m.pendingToasts = append(m.pendingToasts, toast{text: text, action: revealLabel(), fn: func() { mygo.Shell.ShowItemInFolder(path) }})
		})
	}()
}

// toggleRecording starts or stops recording the stream to a movie.
func (m *mirror) toggleRecording() {
	if m.recBusy {
		return
	}
	if m.recording {
		m.recBusy = true
		m.rec.stop()
		return
	}
	if m.state != stateStreaming {
		return
	}
	cfg := m.app.cfg.Get()
	format := mp4.MP4
	if cfg.RecordingFormat == config.FormatMOV {
		format = mp4.MOV
	}
	path := filepath.Join(cfg.RecordingDir, recordingName(m.title, time.Now().Format("2006-01-02 at 15.04.05")))
	m.recBusy = true
	m.rec.start(recRequest{path: path, format: format, audio: cfg.RecordAudio && format == mp4.MOV && m.pcm != nil})
}

func (m *mirror) recorderReport(r recReport) {
	switch {
	case r.started:
		m.recording, m.recBusy, m.recStart = true, false, time.Now()
	case r.finished:
		m.recording, m.recBusy = false, false
		switch {
		case r.err != nil && len(r.paths) > 0:
			path := r.paths[0]
			m.pendingToasts = append(m.pendingToasts, toast{text: r.err.Error(), action: revealLabel(), fn: func() { mygo.Shell.ShowItemInFolder(path) }})
		case r.err != nil:
			m.pendingToasts = append(m.pendingToasts, toast{text: "Recording failed: " + r.err.Error()})
		case len(r.paths) > 0:
			path := r.paths[0]
			text := "Recording saved"
			if len(r.paths) > 1 {
				text = fmt.Sprintf("Recording saved in %d files", len(r.paths))
			}
			m.pendingToasts = append(m.pendingToasts, toast{text: text, action: revealLabel(), fn: func() { mygo.Shell.ShowItemInFolder(path) }})
		}
	}
	m.app.refreshMenu()
}

// applyOptions saves options of the device's own and applies them.
func (m *mirror) applyOptions(o config.MirrorOptions) {
	o = o.Normalize()
	m.override = true
	id, title := m.identity, m.title
	_ = m.app.cfg.Update(func(c *config.Config) {
		p := c.Devices[id]
		if p == nil {
			p = &config.DeviceProfile{}
			c.Devices[id] = p
		}
		p.Name = title
		oc := o
		p.Override = &oc
	})
	m.setOptions(o)
}

// applyOptionsDefault applies the defaults, which changed, to a device
// without options of its own.
func (m *mirror) applyOptionsDefault(o config.MirrorOptions) { m.setOptions(o.Normalize()) }

// useDefaults drops the device's own options.
func (m *mirror) useDefaults() {
	id := m.identity
	_ = m.app.cfg.Update(func(c *config.Config) {
		if p := c.Devices[id]; p != nil {
			p.Override = nil
		}
	})
	m.override = false
	m.setOptions(m.app.cfg.Get().Mirror)
}

// setOptions applies options: live where they can be, by reconnecting
// where the stream must change.
func (m *mirror) setOptions(o config.MirrorOptions) {
	old := m.opts
	m.opts, m.editOpts = o, o
	if o.AlwaysOnTop != old.AlwaysOnTop {
		m.win.SetAlwaysOnTop(o.AlwaysOnTop)
	}
	if o.TurnScreenOff != old.TurnScreenOff && m.state == stateStreaming {
		m.send(scrcpy.SetDisplayPower(!o.TurnScreenOff))
	}
	if old.RequiresRestart(o) && !m.closed {
		if m.recording {
			m.rec.stop()
		}
		m.connect()
	}
}

// --- Input -------------------------------------------------------------------

// devicePos converts a point of the picture, in DIPs, to the device's
// pixels.
func (m *mirror) devicePos(x, y, vw, vh float32) scrcpy.Position {
	fx := float64(x) / float64(vw) * float64(m.frameW)
	fy := float64(y) / float64(vh) * float64(m.frameH)
	fx = math.Max(0, math.Min(fx, float64(m.frameW-1)))
	fy = math.Max(0, math.Min(fy, float64(m.frameH-1)))
	return scrcpy.Position{X: int32(fx), Y: int32(fy), Width: uint16(m.frameW), Height: uint16(m.frameH)}
}

// input forwards what the picture takes to the device.
func (m *mirror) input(ev ui.InputEvent, vw, vh float32) bool {
	if m.session == nil || m.frameW == 0 {
		return false
	}
	switch ev.Kind {
	case ui.InputPointerDown:
		m.videoFocus.Focus()
		pos := m.devicePos(ev.X, ev.Y, vw, vh)
		switch ev.Button {
		case 0:
			m.pointerOn = true
			m.send(scrcpy.InjectTouch(scrcpy.ActionDown, scrcpy.PointerMouse, pos, 1, scrcpy.ButtonPrimary, scrcpy.ButtonPrimary))
		case 1:
			m.send(scrcpy.BackOrScreenOn(scrcpy.ActionDown))
		case 2:
			m.send(scrcpy.InjectKeycode(scrcpy.ActionDown, akHome, 0, 0))
		}
		return true
	case ui.InputPointerMove:
		if m.pointerOn && ev.Button == 0 {
			m.send(scrcpy.InjectTouch(scrcpy.ActionMove, scrcpy.PointerMouse, m.devicePos(ev.X, ev.Y, vw, vh), 1, 0, scrcpy.ButtonPrimary))
			return true
		}
		return false
	case ui.InputPointerUp:
		switch ev.Button {
		case 0:
			if m.pointerOn {
				m.pointerOn = false
				m.send(scrcpy.InjectTouch(scrcpy.ActionUp, scrcpy.PointerMouse, m.devicePos(ev.X, ev.Y, vw, vh), 0, scrcpy.ButtonPrimary, 0))
			}
		case 1:
			m.send(scrcpy.BackOrScreenOn(scrcpy.ActionUp))
		case 2:
			m.send(scrcpy.InjectKeycode(scrcpy.ActionUp, akHome, 0, 0))
		}
		return true
	case ui.InputScroll:
		pos := m.devicePos(ev.X, ev.Y, vw, vh)
		var h, v float32
		if ev.Precise {
			h, v = ev.DX/24, -ev.DY/24
		} else {
			h, v = sign(ev.DX), -sign(ev.DY)
		}
		if h != 0 || v != 0 {
			m.send(scrcpy.InjectScroll(pos, h, v, 0))
		}
		return true
	case ui.InputKeyDown:
		if ev.Mods&ui.Cmd != 0 {
			return false // AirDroid's shortcuts and menus
		}
		if textKey(ev.Key) && ev.Mods&^ui.Shift == 0 {
			return false // typed as text, which InputText brings
		}
		code := androidKey(ev.Key)
		if code == 0 {
			return false
		}
		repeat := 0
		if ev.Repeat {
			m.keysDown[ev.Key]++
			repeat = m.keysDown[ev.Key]
		} else {
			m.keysDown[ev.Key] = 0
		}
		m.send(scrcpy.InjectKeycode(scrcpy.ActionDown, code, repeat, metaState(ev.Mods)))
		return true
	case ui.InputKeyUp:
		if _, down := m.keysDown[ev.Key]; !down {
			return false
		}
		delete(m.keysDown, ev.Key)
		if code := androidKey(ev.Key); code != 0 {
			m.send(scrcpy.InjectKeycode(scrcpy.ActionUp, code, 0, metaState(ev.Mods)))
		}
		return true
	case ui.InputText:
		m.composing = ""
		m.typeText(ev.Text)
		return true
	case ui.InputCompose:
		m.composing = ev.Text
		return true
	case ui.InputCommand:
		switch ev.Text {
		case "paste", "pasteAndMatchStyle":
			m.paste()
		case "copy":
			m.key(akCopy)
		case "cut":
			m.key(akCut)
		case "selectAll":
			m.send(scrcpy.InjectKeycode(scrcpy.ActionDown, akA, 0, scrcpy.MetaCtrlOn))
			m.send(scrcpy.InjectKeycode(scrcpy.ActionUp, akA, 0, scrcpy.MetaCtrlOn))
		default:
			return false
		}
		return true
	}
	return false
}

func sign(v float32) float32 {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

// typeText types text on the device: as key events where Android's key
// map has the characters, else by pasting it, as for Chinese or emoji.
func (m *mirror) typeText(text string) {
	if text == "" {
		return
	}
	if isASCII(text) {
		for len(text) > 0 {
			n := min(len(text), scrcpy.MaxTextLength)
			m.send(scrcpy.InjectText(text[:n]))
			text = text[n:]
		}
		return
	}
	m.clipSeq++
	m.lastClip = text
	m.send(scrcpy.SetClipboard(m.clipSeq, text, true))
}

// paste pastes the computer's clipboard on the device.
func (m *mirror) paste() {
	text := mygo.Clipboard.ReadText()
	if text == "" {
		return
	}
	m.clipSeq++
	m.lastClip = text
	m.send(scrcpy.SetClipboard(m.clipSeq, text, true))
}
