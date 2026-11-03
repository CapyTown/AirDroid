package scrcpy

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CapyTown/AirDroid/internal/adb"
	"github.com/CapyTown/AirDroid/internal/media"
)

// The test binary doubles as a fake adb, which a shell script runs: "adb
// reverse" records the port AirDroid listens on, and "adb shell … app_process"
// starts a fake scrcpy-server that connects there, as the real one does
// through the tunnel, and speaks its protocol.

func TestMain(m *testing.M) {
	if os.Getenv("FAKE_ADB_DIR") != "" {
		os.Exit(fakeADB(os.Args[1:]))
	}
	os.Exit(m.Run())
}

var (
	sps = []byte{0x67, 0x42, 0xc0, 0x1f, 0xda, 0x01, 0x40, 0x16, 0xec, 0x04, 0x40}
	pps = []byte{0x68, 0xce, 0x3c, 0x80}
)

func fakeADB(args []string) int {
	dir := os.Getenv("FAKE_ADB_DIR")
	log, _ := os.OpenFile(filepath.Join(dir, "calls"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	fmt.Fprintln(log, strings.Join(args, " "))
	log.Close()
	if len(args) >= 2 && args[0] == "-s" {
		args = args[2:]
	}
	switch args[0] {
	case "push":
		return 0
	case "reverse":
		if args[1] == "--remove" {
			return 0
		}
		port := strings.TrimPrefix(args[2], "tcp:")
		os.WriteFile(filepath.Join(dir, "port"), []byte(port), 0o644)
		return 0
	case "shell":
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "com.genymobile.scrcpy.Server") {
			return 0
		}
		return fakeServer(dir, args)
	}
	return 0
}

// fakeServer streams like scrcpy-server 5: video, audio and control
// sockets; the video's codec id, a session header, a config packet and
// frames; raw audio; and it echoes control messages to a file.
func fakeServer(dir string, args []string) int {
	version := ""
	audio := true
	for i, a := range args {
		if a == "com.genymobile.scrcpy.Server" {
			version = args[i+1]
		}
		if a == "audio=false" {
			audio = false
		}
	}
	if version != "5.0.1" {
		fmt.Printf("[server] ERROR: The server version (5.0.1) does not match the client (%s)\n", version)
		return 1
	}
	port, _ := os.ReadFile(filepath.Join(dir, "port"))
	addr := "127.0.0.1:" + string(port)
	dial := func() net.Conn {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			panic(err)
		}
		return c
	}
	video := dial()
	var audioConn net.Conn
	if audio {
		audioConn = dial()
	}
	control := dial()
	name := make([]byte, 64)
	copy(name, "Fake Phone")
	video.Write(name)
	be32 := func(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }
	video.Write(be32(CodecH264))
	session := append(be32(1<<31), append(be32(1080), be32(2400)...)...)
	video.Write(session)
	packet := func(w io.Writer, pts uint64, config, key bool, data []byte) {
		v := pts
		if config {
			v = 1 << 62
		} else if key {
			v |= 1 << 61
		}
		hdr := binary.BigEndian.AppendUint64(nil, v)
		hdr = binary.BigEndian.AppendUint32(hdr, uint32(len(data)))
		w.Write(append(hdr, data...))
	}
	config := append(append([]byte{0, 0, 0, 1}, sps...), append([]byte{0, 0, 0, 1}, pps...)...)
	packet(video, 0, true, false, config)
	for i := 0; i < 5; i++ {
		frame := []byte{0, 0, 0, 1, 0x41, byte(i), 0xaa}
		if i == 0 {
			frame = []byte{0, 0, 0, 1, 0x65, 0x88, 0x84}
		}
		packet(video, uint64(1000+i*16666), false, i == 0, frame)
	}
	if audio {
		audioConn.Write(be32(CodecRaw))
		packet(audioConn, 1000, false, false, make([]byte, 960*4))
	}
	// A clipboard message from the device.
	clip := append([]byte{0}, be32(5)...)
	control.Write(append(clip, "hello"...))
	got, _ := io.ReadAll(io.LimitReader(control, 14))
	os.WriteFile(filepath.Join(dir, "control"), got, 0o644)
	time.Sleep(200 * time.Millisecond)
	return 0
}

type recorder struct {
	mu     sync.Mutex
	sizes  [][2]int
	params []*media.ParameterSets
	frames []VideoFrame
	pcm    int
	clip   []string
	done   chan struct{}
}

func (r *recorder) VideoSize(w, h int) {
	r.mu.Lock()
	r.sizes = append(r.sizes, [2]int{w, h})
	r.mu.Unlock()
}
func (r *recorder) VideoConfig(p *media.ParameterSets) {
	r.mu.Lock()
	r.params = append(r.params, p)
	r.mu.Unlock()
}
func (r *recorder) VideoFrame(f VideoFrame) {
	r.mu.Lock()
	r.frames = append(r.frames, f)
	n := len(r.frames)
	r.mu.Unlock()
	if n == 5 {
		close(r.done)
	}
}
func (r *recorder) AudioPCM(pts int64, pcm []byte) {
	r.mu.Lock()
	r.pcm += len(pcm)
	r.mu.Unlock()
}
func (r *recorder) Clipboard(text string) {
	r.mu.Lock()
	r.clip = append(r.clip, text)
	r.mu.Unlock()
}

func fakeADBClient(t *testing.T) (*adb.Client, string) {
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "adb")
	body := fmt.Sprintf("#!/bin/sh\nFAKE_ADB_DIR=%q exec %q \"$@\"\n", dir, exe)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return adb.New(script), dir
}

func TestSessionEndToEnd(t *testing.T) {
	client, dir := fakeADBClient(t)
	server := filepath.Join(dir, "scrcpy-server")
	os.WriteFile(server, []byte("jar"), 0o644)
	rec := &recorder{done: make(chan struct{})}
	opts := Options{Serial: "R5CT", Server: Server{Path: server, Version: "4.0"}, Audio: true, Codec: media.H264, MaxSize: 1920, BitRate: 8_000_000}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// A server of another version: the error tells which.
	_, err := Start(ctx, client, opts, rec)
	var vm *VersionMismatchError
	if !errorsAs(err, &vm) || vm.ServerVersion != "5.0.1" {
		t.Fatalf("Start with the wrong version: %v", err)
	}
	opts.Server.Version = vm.ServerVersion
	s, err := Start(ctx, client, opts, rec)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.DeviceName != "Fake Phone" {
		t.Errorf("device name %q", s.DeviceName)
	}
	select {
	case <-rec.done:
	case <-time.After(10 * time.Second):
		t.Fatal("frames did not arrive")
	}
	s.Send(InjectKeycode(ActionDown, 3, 0, 0))
	deadline := time.Now().Add(5 * time.Second)
	var control []byte
	for time.Now().Before(deadline) {
		control, _ = os.ReadFile(filepath.Join(dir, "control"))
		if len(control) == 14 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !bytes.Equal(control, InjectKeycode(ActionDown, 3, 0, 0)) {
		t.Errorf("the server got %x", control)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.sizes) == 0 || rec.sizes[0] != [2]int{1080, 2400} {
		t.Errorf("sizes %v", rec.sizes)
	}
	if len(rec.params) != 1 || !bytes.Equal(rec.params[0].SPS[0], sps) || !bytes.Equal(rec.params[0].PPS[0], pps) {
		t.Errorf("params %+v", rec.params)
	}
	if !rec.frames[0].Key || rec.frames[0].PTS != 1000 || rec.frames[1].Key || rec.frames[4].PTS != 1000+4*16666 {
		t.Errorf("frames %+v", rec.frames)
	}
	if !bytes.Equal(rec.frames[1].Data, []byte{0, 0, 0, 3, 0x41, 1, 0xaa}) {
		t.Errorf("frame data %x, want length-prefixed", rec.frames[1].Data)
	}
	if rec.pcm != 960*4 {
		t.Errorf("audio bytes %d", rec.pcm)
	}
	if len(rec.clip) != 1 || rec.clip[0] != "hello" {
		t.Errorf("clipboard %q", rec.clip)
	}
	calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
	for _, want := range []string{
		`push .*scrcpy-server /data/local/tmp/scrcpy-server.jar`,
		`reverse localabstract:scrcpy_[0-9a-f]{8} tcp:\d+`,
		`shell CLASSPATH=/data/local/tmp/scrcpy-server.jar app_process / com.genymobile.scrcpy.Server 5.0.1 scid=[0-9a-f]{8} log_level=info video_codec=h264 video_bit_rate=8000000 max_size=1920 audio_codec=raw`,
		`reverse --remove localabstract:scrcpy_`,
	} {
		if !regexp.MustCompile(want).Match(calls) {
			t.Errorf("no adb call matching %q in:\n%s", want, calls)
		}
	}
}

func errorsAs(err error, target **VersionMismatchError) bool {
	if e, ok := err.(*VersionMismatchError); ok {
		*target = e
		return true
	}
	return false
}
