package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/CapyTown/AirDroid/internal/media"
	"github.com/CapyTown/AirDroid/internal/mp4"
	"github.com/CapyTown/AirDroid/internal/scrcpy"
)

// stream encodes a test stream with ffmpeg and returns its parameter sets
// and frames as the session hands them over.
func stream(t *testing.T, w, h, n int) (*media.ParameterSets, []scrcpy.VideoFrame) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	out := filepath.Join(t.TempDir(), "s.h264")
	size := strconv.Itoa(w) + "x" + strconv.Itoa(h)
	if b, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size="+size+":rate=30",
		"-frames:v", strconv.Itoa(n), "-pix_fmt", "yuv420p", "-c:v", "libx264", "-x264-params", "aud=1:keyint=20:bframes=0", "-f", "h264", out).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg: %v %s", err, b)
	}
	data, _ := os.ReadFile(out)
	var params *media.ParameterSets
	var frames []scrcpy.VideoFrame
	var cur []byte
	flush := func() {
		if cur == nil {
			return
		}
		f := media.ToLengthPrefixed(media.H264, cur)
		if f.Params != nil && params == nil {
			params = f.Params
		}
		frames = append(frames, scrcpy.VideoFrame{PTS: int64(len(frames)) * 33_333, Key: f.IDR, Data: f.Data})
		cur = nil
	}
	for _, nal := range media.SplitAnnexB(data) {
		if media.NALType(media.H264, nal) == 9 {
			flush()
		}
		cur = append(append(cur, 0, 0, 0, 1), nal...)
	}
	flush()
	return params, frames
}

func countFrames(t *testing.T, path string) int {
	out, err := exec.Command("ffprobe", "-v", "error", "-count_frames", "-select_streams", "v:0",
		"-show_entries", "stream=nb_read_frames", "-of", "json", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	var p struct {
		Streams []struct {
			N string `json:"nb_read_frames"`
		} `json:"streams"`
	}
	json.Unmarshal(out, &p)
	if len(p.Streams) == 0 {
		t.Fatalf("no video in %s", path)
	}
	n, _ := strconv.Atoi(p.Streams[0].N)
	return n
}

// A recording started mid-GOP starts at once, from the cached key frame,
// and a new size starts a new file.
func TestRecorder(t *testing.T) {
	params, frames := stream(t, 320, 640, 60)
	reports := make(chan recReport, 8)
	needKey := 0
	r := newRecorder(func(rep recReport) { reports <- rep }, func() { needKey++ })
	r.config(params)
	r.size(320, 640)
	for _, f := range frames[:25] { // key frames at 0 and 20
		r.frame(f)
	}
	base := filepath.Join(t.TempDir(), "rec")
	r.start(recRequest{path: base, format: mp4.MP4})
	if rep := <-reports; !rep.started {
		t.Fatalf("first report %+v", rep)
	}
	for _, f := range frames[25:40] {
		r.frame(f)
	}
	// The phone rotates: a new size and configuration.
	p2, f2 := stream(t, 640, 320, 30)
	r.size(640, 320)
	r.config(p2)
	for i, f := range f2 {
		f.PTS = frames[39].PTS + int64(i+1)*33_333
		r.frame(f)
	}
	r.stop()
	var rep recReport
	select {
	case rep = <-reports:
	case <-time.After(10 * time.Second):
		t.Fatal("the recorder did not finish")
	}
	if rep.err != nil || len(rep.paths) != 2 {
		t.Fatalf("report %+v", rep)
	}
	if needKey != 0 {
		t.Error("asked for a key frame though one was cached")
	}
	// From the key frame at 20 to 39: 20 frames, of which 15 show.
	if n := countFrames(t, rep.paths[0]); n < 15 || n > 20 {
		t.Errorf("first file: %d frames", n)
	}
	if n := countFrames(t, rep.paths[1]); n != 30 {
		t.Errorf("second file: %d frames, want 30", n)
	}
	r.quit()
}

func TestPCMStream(t *testing.T) {
	s := newPCMStream()
	out := make([]float32, 960)
	s.Read(out)
	for _, v := range out {
		if v != 0 {
			t.Fatal("plays before it buffered")
		}
	}
	pcm := make([]byte, pcmTarget*2)
	for i := 0; i < len(pcm); i += 2 {
		pcm[i+1] = 0x40 // 0x4000 = 0.5
	}
	s.push(pcm)
	s.Read(out)
	if out[0] != 0.5 {
		t.Errorf("sample %v, want 0.5", out[0])
	}
	// Far behind: it catches up rather than lag.
	for i := 0; i < 20; i++ {
		s.push(pcm)
	}
	s.mu.Lock()
	avail := len(s.buf) - s.head
	s.mu.Unlock()
	if avail > pcmMax {
		t.Errorf("%d samples buffered, more than %d", avail, pcmMax)
	}
}

func TestKeys(t *testing.T) {
	if androidKey(ui.KeyA) != akA || androidKey(ui.KeyZ) != akA+25 || androidKey(ui.Key0) != ak0 {
		t.Error("letters and digits")
	}
	if androidKey(ui.KeyBackspace) != akDel || androidKey(ui.KeyEnter) != akEnter || androidKey(ui.KeyLeft) != akDpadLeft {
		t.Error("editing keys")
	}
	if !textKey(ui.KeyA) || textKey(ui.KeyEnter) {
		t.Error("text keys")
	}
	if metaState(ui.Shift|ui.Ctrl) != scrcpy.MetaShiftOn|scrcpy.MetaCtrlOn {
		t.Error("meta state")
	}
	if !isASCII("hello, world") || isASCII("你好") {
		t.Error("ascii")
	}
}
