package scrcpy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CapyTown/AirDroid/internal/adb"
	"github.com/CapyTown/AirDroid/internal/media"
)

// DevicePath is where the server is pushed, as scrcpy does.
const DevicePath = "/data/local/tmp/scrcpy-server.jar"

// Options configure a session.
type Options struct {
	Serial  string
	Server  Server
	Codec   media.Codec
	MaxSize int // 0: the device's size
	BitRate int // bits per second
	MaxFPS  int // 0: unlimited
	Audio   bool
	// Device state the server sets up and restores when the session ends.
	StayAwake       bool
	ShowTouches     bool
	PowerOffOnClose bool
}

// VideoFrame is an access unit, length-prefixed.
type VideoFrame struct {
	PTS  int64 // microseconds
	Key  bool
	Data []byte
}

// Handler receives what the device sends. Video and audio methods are
// called from the goroutine of their stream, in stream order; Clipboard
// from the control socket's.
type Handler interface {
	// VideoSize reports the size of the frames that follow, which input
	// events must be computed against.
	VideoSize(width, height int)
	// VideoConfig reports the parameter sets of the frames that follow.
	VideoConfig(params *media.ParameterSets)
	VideoFrame(f VideoFrame)
	// AudioPCM delivers 16-bit little-endian stereo samples at 48 kHz.
	AudioPCM(pts int64, pcm []byte)
	Clipboard(text string)
}

// VersionMismatchError reports a server that is not the version announced.
type VersionMismatchError struct{ ServerVersion string }

func (e *VersionMismatchError) Error() string {
	return "scrcpy-server is version " + e.ServerVersion
}

// Session is a running mirroring session.
type Session struct {
	opts    Options
	adb     *adb.Client
	proto   Protocol
	h       Handler
	server  *exec.Cmd
	log     *logBuffer
	video   net.Conn
	audio   net.Conn
	control net.Conn

	// DeviceName is the name the server reported.
	DeviceName string
	// AudioCodec is the audio stream's codec, 0 when the device sends no
	// audio (Android 10 and earlier, or audio turned off).
	AudioCodec uint32
	// InitialWidth and InitialHeight are the first frames' size.
	InitialWidth, InitialHeight int

	sendq     chan []byte
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
	errMu     sync.Mutex
	err       error
}

// Start pushes and starts the server and connects to it. It returns once
// the streams are open; frames then reach h until Close or the device goes
// away (Done).
func Start(ctx context.Context, client *adb.Client, opts Options, h Handler) (*Session, error) {
	if opts.Server.Path == "" {
		return nil, errors.New("scrcpy-server not found")
	}
	if opts.Server.Version == "" {
		return nil, errors.New("the version of scrcpy-server is unknown")
	}
	s := &Session{
		opts:  opts,
		adb:   client,
		proto: ProtocolFor(opts.Server.Version),
		h:     h,
		log:   &logBuffer{},
		sendq: make(chan []byte, 512),
		done:  make(chan struct{}),
	}
	if err := client.Push(ctx, opts.Serial, opts.Server.Path, DevicePath); err != nil {
		return nil, fmt.Errorf("could not copy scrcpy-server to the device: %w", err)
	}
	if err := s.connect(ctx); err != nil {
		s.Close()
		if v := ServerVersionFromMismatch(s.log.String()); v != "" {
			return nil, &VersionMismatchError{ServerVersion: v}
		}
		if msg := s.log.LastError(); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	s.wg.Add(3)
	go s.readVideo()
	go s.readAudio()
	go s.readControl()
	go s.writeControl()
	return s, nil
}

func randomSCID() uint32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return binary.BigEndian.Uint32(b[:]) & 0x7fffffff
}

func (s *Session) serverArgs(scid uint32, forward bool) []string {
	o := s.opts
	args := []string{
		"-s", o.Serial, "shell",
		"CLASSPATH=" + DevicePath, "app_process", "/", "com.genymobile.scrcpy.Server",
		o.Server.Version,
		fmt.Sprintf("scid=%08x", scid),
		"log_level=info",
	}
	if o.Codec == media.H265 {
		args = append(args, "video_codec=h265")
	} else {
		args = append(args, "video_codec=h264")
	}
	if o.BitRate > 0 {
		args = append(args, "video_bit_rate="+strconv.Itoa(o.BitRate))
	}
	if o.MaxSize > 0 {
		args = append(args, "max_size="+strconv.Itoa(o.MaxSize))
	}
	if o.MaxFPS > 0 {
		args = append(args, "max_fps="+strconv.Itoa(o.MaxFPS))
	}
	if o.Audio {
		args = append(args, "audio_codec=raw")
	} else {
		args = append(args, "audio=false")
	}
	if forward {
		args = append(args, "tunnel_forward=true")
	}
	if o.StayAwake {
		args = append(args, "stay_awake=true")
	}
	if o.ShowTouches {
		args = append(args, "show_touches=true")
	}
	if o.PowerOffOnClose {
		args = append(args, "power_off_on_close=true")
	}
	return args
}

func (s *Session) socketCount() int {
	if s.opts.Audio {
		return 3
	}
	return 2
}

// connect opens the tunnel, starts the server and accepts its sockets:
// through "adb reverse", the device connecting to MyGo, else through "adb
// forward", MyGo connecting to the device.
func (s *Session) connect(ctx context.Context) error {
	scid := randomSCID()
	name := fmt.Sprintf("localabstract:scrcpy_%08x", scid)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := s.adb.Reverse(ctx, s.opts.Serial, name, "tcp:"+strconv.Itoa(port)); err == nil {
		defer ln.Close()
		if err := s.startServer(ctx, scid, false); err != nil {
			s.adb.ReverseRemove(context.Background(), s.opts.Serial, name)
			return err
		}
		conns, err := s.accept(ctx, ln)
		s.adb.ReverseRemove(context.Background(), s.opts.Serial, name)
		if err != nil {
			return err
		}
		return s.handshake(conns, false)
	} else {
		slog.Info("adb reverse failed, using adb forward", "serial", s.opts.Serial, "err", err)
	}
	ln.Close()

	// Forward: reserve a local port, have adb listen on it.
	l2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port = l2.Addr().(*net.TCPAddr).Port
	l2.Close()
	local := "tcp:" + strconv.Itoa(port)
	if err := s.adb.Forward(ctx, s.opts.Serial, local, name); err != nil {
		return fmt.Errorf("could not open a tunnel to the device: %w", err)
	}
	defer s.adb.ForwardRemove(context.Background(), s.opts.Serial, local)
	if err := s.startServer(ctx, scid, true); err != nil {
		return err
	}
	conns := make([]net.Conn, 0, 3)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	for len(conns) < s.socketCount() {
		var c net.Conn
		for {
			if err := s.serverExited(); err != nil {
				closeAll(conns)
				return err
			}
			var d net.Dialer
			c, err = d.DialContext(ctx, "tcp", addr)
			if err == nil && len(conns) == 0 {
				// The server sends one byte on its first socket: reading it
				// tells the server listens, as adb accepts at once anyway.
				c.SetReadDeadline(time.Now().Add(2 * time.Second))
				var one [1]byte
				if _, err = io.ReadFull(c, one[:]); err != nil {
					c.Close()
				}
				c.SetReadDeadline(time.Time{})
			}
			if err == nil {
				break
			}
			select {
			case <-ctx.Done():
				closeAll(conns)
				return errors.New("timed out connecting to the device")
			case <-time.After(100 * time.Millisecond):
			}
		}
		conns = append(conns, c)
	}
	return s.handshake(conns, true)
}

func closeAll(conns []net.Conn) {
	for _, c := range conns {
		c.Close()
	}
}

func (s *Session) startServer(ctx context.Context, scid uint32, forward bool) error {
	// The server outlives ctx, which only bounds connecting.
	cmd := s.adb.Command(context.Background(), s.serverArgs(scid, forward)...)
	cmd.Stdout = s.log
	cmd.Stderr = s.log
	if err := cmd.Start(); err != nil {
		return err
	}
	s.server = cmd
	exited := make(chan error, 1)
	s.log.exited = exited
	go func() {
		err := cmd.Wait()
		if err == nil {
			err = errors.New("scrcpy-server stopped")
		}
		exited <- err
		close(exited)
	}()
	return nil
}

func (s *Session) serverExited() error {
	select {
	case err, ok := <-s.log.exited:
		if ok {
			return fmt.Errorf("scrcpy-server stopped: %v", err)
		}
		return errors.New("scrcpy-server stopped")
	default:
		return nil
	}
}

func (s *Session) accept(ctx context.Context, ln net.Listener) ([]net.Conn, error) {
	type result struct {
		c   net.Conn
		err error
	}
	conns := make([]net.Conn, 0, 3)
	for len(conns) < s.socketCount() {
		ch := make(chan result, 1)
		go func() {
			c, err := ln.Accept()
			ch <- result{c, err}
		}()
		select {
		case r := <-ch:
			if r.err != nil {
				closeAll(conns)
				return nil, r.err
			}
			conns = append(conns, r.c)
		case err, ok := <-s.log.exited:
			ln.Close()
			closeAll(conns)
			if ok && err != nil {
				return nil, fmt.Errorf("scrcpy-server stopped: %v", err)
			}
			return nil, errors.New("scrcpy-server stopped")
		case <-ctx.Done():
			ln.Close()
			closeAll(conns)
			return nil, errors.New("timed out waiting for the device")
		}
	}
	return conns, nil
}

// handshake reads the device name and the video codec header.
func (s *Session) handshake(conns []net.Conn, forward bool) error {
	s.video = conns[0]
	if s.opts.Audio {
		s.audio = conns[1]
		s.control = conns[2]
	} else {
		s.control = conns[1]
	}
	for _, c := range conns {
		if tc, ok := c.(*net.TCPConn); ok {
			tc.SetNoDelay(true)
		}
	}
	s.video.SetReadDeadline(time.Now().Add(15 * time.Second))
	defer s.video.SetReadDeadline(time.Time{})
	var name [64]byte
	if _, err := io.ReadFull(s.video, name[:]); err != nil {
		return fmt.Errorf("the device did not answer: %w", err)
	}
	s.DeviceName = string(bytes.TrimRight(name[:], "\x00"))
	var id [4]byte
	if _, err := io.ReadFull(s.video, id[:]); err != nil {
		return err
	}
	codec := binary.BigEndian.Uint32(id[:])
	switch codec {
	case 0:
		return errors.New("the device has no video to mirror")
	case 1:
		return errors.New("the device could not start its video encoder")
	case CodecH264, CodecH265:
	default:
		return fmt.Errorf("unsupported video codec %s", CodecName(codec))
	}
	if s.proto == ProtocolV2 {
		var size [8]byte
		if _, err := io.ReadFull(s.video, size[:]); err != nil {
			return err
		}
		s.InitialWidth = int(binary.BigEndian.Uint32(size[0:]))
		s.InitialHeight = int(binary.BigEndian.Uint32(size[4:]))
	}
	return nil
}

func (s *Session) videoCodec() media.Codec {
	if s.opts.Codec == media.H265 {
		return media.H265
	}
	return media.H264
}

func (s *Session) readVideo() {
	defer s.wg.Done()
	defer s.finish(nil)
	codec := s.videoCodec()
	r := bufio.NewReaderSize(s.video, 256<<10)
	var hdrBuf [12]byte
	var params *media.ParameterSets
	w, h := s.InitialWidth, s.InitialHeight
	if w > 0 && h > 0 {
		s.h.VideoSize(w, h)
	}
	sessionSize := false
	for {
		hdr, err := ReadHeader(r, s.proto, &hdrBuf)
		if err != nil {
			s.finish(streamErr(err))
			return
		}
		if hdr.Session {
			if hdr.Width > 0 && hdr.Height > 0 {
				w, h, sessionSize = hdr.Width, hdr.Height, true
				s.h.VideoSize(w, h)
			}
			continue
		}
		data := make([]byte, hdr.Size)
		if _, err := io.ReadFull(r, data); err != nil {
			s.finish(streamErr(err))
			return
		}
		if hdr.Packet.Config {
			p := media.ParseConfig(codec, data)
			if p.Empty() {
				continue
			}
			params = p
			if !sessionSize {
				if info, err := media.ParseSPS(codec, p.SPS[0]); err == nil && (info.Width != w || info.Height != h) {
					w, h = info.Width, info.Height
					s.h.VideoSize(w, h)
				}
			}
			s.h.VideoConfig(p)
			continue
		}
		f := media.ToLengthPrefixed(codec, data)
		if f.Params != nil && !f.Params.Empty() && !f.Params.Equal(params) {
			params = f.Params
			s.h.VideoConfig(params)
		}
		if len(f.Data) == 0 {
			continue
		}
		s.h.VideoFrame(VideoFrame{PTS: hdr.Packet.PTS, Key: hdr.Packet.KeyFrame || f.IDR, Data: f.Data})
	}
}

func (s *Session) readAudio() {
	defer s.wg.Done()
	if s.audio == nil {
		return
	}
	r := bufio.NewReaderSize(s.audio, 64<<10)
	var id [4]byte
	if _, err := io.ReadFull(r, id[:]); err != nil {
		return
	}
	codec := binary.BigEndian.Uint32(id[:])
	if codec != CodecRaw {
		if codec > 1 {
			slog.Warn("unexpected audio codec", "codec", CodecName(codec))
		}
		return // 0: no audio on this device; 1: it failed. Video goes on.
	}
	s.AudioCodec = codec
	var hdrBuf [12]byte
	for {
		hdr, err := ReadHeader(r, s.proto, &hdrBuf)
		if err != nil {
			return
		}
		if hdr.Session {
			continue
		}
		data := make([]byte, hdr.Size)
		if _, err := io.ReadFull(r, data); err != nil {
			return
		}
		if hdr.Packet.Config {
			continue
		}
		s.h.AudioPCM(hdr.Packet.PTS, data)
	}
}

func (s *Session) readControl() {
	defer s.wg.Done()
	r := bufio.NewReader(s.control)
	for {
		msg, err := ReadDeviceMessage(r)
		if err != nil {
			return
		}
		if msg.Clipboard != nil {
			s.h.Clipboard(*msg.Clipboard)
		}
	}
}

func (s *Session) writeControl() {
	for {
		select {
		case <-s.done:
			return
		case msg := <-s.sendq:
			if _, err := s.control.Write(msg); err != nil {
				s.finish(err)
				return
			}
		}
	}
}

// Send queues a control message. Messages queued while the device is slow
// to take them wait; once the queue is full, pointer moves are dropped.
func (s *Session) Send(msg []byte) {
	select {
	case <-s.done:
		return
	default:
	}
	droppable := len(msg) == 32 && msg[0] == msgInjectTouch && msg[1] == ActionMove
	if droppable {
		select {
		case s.sendq <- msg:
		default:
		}
		return
	}
	select {
	case s.sendq <- msg:
	case <-s.done:
	case <-time.After(2 * time.Second):
		slog.Warn("control message dropped: the device is not reading")
	}
}

// Done is closed when the session ends.
func (s *Session) Done() <-chan struct{} { return s.done }

// Err returns why the session ended: nil after Close.
func (s *Session) Err() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.err
}

func streamErr(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return errors.New("the device disconnected")
	}
	return err
}

func (s *Session) finish(err error) {
	s.errMu.Lock()
	if s.err == nil && err != nil {
		s.err = err
	}
	s.errMu.Unlock()
	s.closeOnce.Do(func() {
		close(s.done)
		for _, c := range []net.Conn{s.video, s.audio, s.control} {
			if c != nil {
				c.Close()
			}
		}
		if s.server != nil && s.server.Process != nil {
			srv := s.server
			// The server quits when its sockets close; make sure.
			go func() {
				time.Sleep(3 * time.Second)
				srv.Process.Kill()
			}()
		}
	})
}

// Close ends the session.
func (s *Session) Close() { s.finish(nil) }

// Wait waits for the session's streams to end.
func (s *Session) Wait() { s.wg.Wait() }

// logBuffer keeps the end of the server's output.
type logBuffer struct {
	mu     sync.Mutex
	buf    []byte
	exited chan error
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			slog.Debug("scrcpy-server", "line", line)
		}
	}
	l.buf = append(l.buf, p...)
	if len(l.buf) > 16<<10 {
		l.buf = l.buf[len(l.buf)-16<<10:]
	}
	return len(p), nil
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return string(l.buf)
}

// LastError returns the server's last ERROR line.
func (l *logBuffer) LastError() string {
	lines := strings.Split(l.String(), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if i := strings.Index(line, "ERROR:"); i >= 0 {
			return strings.TrimSpace(line[i+len("ERROR:"):])
		}
	}
	return ""
}
