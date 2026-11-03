package mp4

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/CapyTown/AirDroid/internal/media"
)

// accessUnits encodes a test stream with ffmpeg and splits it into access
// units, Annex B, as scrcpy-server sends them.
func accessUnits(t *testing.T, codec media.Codec, w, h, frames int) [][]byte {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	out := filepath.Join(t.TempDir(), "stream")
	size := strconv.Itoa(w) + "x" + strconv.Itoa(h)
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=" + size + ":rate=30",
		"-frames:v", strconv.Itoa(frames), "-pix_fmt", "yuv420p"}
	aud := 9
	if codec == media.H265 {
		aud = 35
		args = append(args, "-c:v", "libx265", "-x265-params", "aud=1:log-level=error:keyint=30:bframes=0", "-f", "hevc", out)
	} else {
		args = append(args, "-c:v", "libx264", "-x264-params", "aud=1:keyint=30:bframes=0", "-f", "h264", out)
	}
	if b, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg: %v %s", err, b)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var units [][]byte
	var cur []byte
	for _, nal := range media.SplitAnnexB(data) {
		if media.NALType(codec, nal) == aud && cur != nil {
			units = append(units, cur)
			cur = nil
		}
		cur = append(cur, 0, 0, 0, 1)
		cur = append(cur, nal...)
	}
	if cur != nil {
		units = append(units, cur)
	}
	return units
}

type probe struct {
	Streams []struct {
		CodecName string `json:"codec_name"`
		CodecType string `json:"codec_type"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		NbFrames  string `json:"nb_frames"`
		Duration  string `json:"duration"`
	} `json:"streams"`
}

func ffprobe(t *testing.T, path string) probe {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-show_streams", "-of", "json", path).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	var p probe
	if err := json.Unmarshal(out, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func decodes(t *testing.T, path string) {
	t.Helper()
	out, err := exec.Command("ffmpeg", "-v", "error", "-i", path, "-f", "null", "-").CombinedOutput()
	if err != nil || len(out) > 0 {
		t.Errorf("ffmpeg could not decode %s: %v %s", filepath.Base(path), err, out)
	}
}

func writeMovie(t *testing.T, codec media.Codec, format Format, withAudio bool) string {
	const w, h, n = 360, 640, 60
	units := accessUnits(t, codec, w, h, n)
	var params *media.ParameterSets
	var frames []media.Frame
	for _, u := range units {
		f := media.ToLengthPrefixed(codec, u)
		if f.Params != nil && params == nil {
			params = f.Params
		}
		frames = append(frames, f)
	}
	path := filepath.Join(t.TempDir(), "movie.mp4")
	var a *Audio
	if withAudio {
		path = filepath.Join(t.TempDir(), "movie.mov")
		a = &Audio{SampleRate: 48000, Channels: 2}
	}
	wr, err := Create(path, format, Video{Codec: codec, Width: w, Height: h, Params: params}, a)
	if err != nil {
		t.Fatal(err)
	}
	base := int64(1_000_000_000)
	wr.SetStart(base + 10*33_333) // the recording started on the 11th frame
	for i, f := range frames {
		pts := base + int64(i)*33_333
		if err := wr.WriteVideo(pts, f.IDR, f.Data); err != nil {
			t.Fatal(err)
		}
		if withAudio && i >= 10 {
			// 1600 frames of a 440 Hz tone per video frame.
			pcm := make([]byte, 1600*4)
			for k := 0; k < 1600; k++ {
				v := int16(8000 * math.Sin(2*math.Pi*440*float64(i*1600+k)/48000))
				binary.LittleEndian.PutUint16(pcm[4*k:], uint16(v))
				binary.LittleEndian.PutUint16(pcm[4*k+2:], uint16(v))
			}
			if err := wr.WriteAudio(pts, pcm); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := wr.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWriteH264MP4(t *testing.T) {
	path := writeMovie(t, media.H264, MP4, false)
	p := ffprobe(t, path)
	if len(p.Streams) != 1 || p.Streams[0].CodecName != "h264" || p.Streams[0].Width != 360 || p.Streams[0].Height != 640 {
		t.Fatalf("streams: %+v", p.Streams)
	}
	if p.Streams[0].NbFrames != "60" {
		t.Errorf("frames %s, want 60", p.Streams[0].NbFrames)
	}
	// 60 frames at 30 fps, the first 10 hidden by the edit list.
	if d, _ := strconv.ParseFloat(p.Streams[0].Duration, 64); math.Abs(d-50.0/30) > 0.05 {
		t.Errorf("duration %v, want %.3f", d, 50.0/30)
	}
	decodes(t, path)
}

func TestWriteH265MP4(t *testing.T) {
	path := writeMovie(t, media.H265, MP4, false)
	p := ffprobe(t, path)
	if len(p.Streams) != 1 || p.Streams[0].CodecName != "hevc" || p.Streams[0].Width != 360 {
		t.Fatalf("streams: %+v", p.Streams)
	}
	decodes(t, path)
}

func TestWriteMOVWithAudio(t *testing.T) {
	path := writeMovie(t, media.H264, MOV, true)
	p := ffprobe(t, path)
	if len(p.Streams) != 2 {
		t.Fatalf("streams: %+v", p.Streams)
	}
	var audio bool
	for _, s := range p.Streams {
		if s.CodecType == "audio" {
			audio = true
			if s.CodecName != "pcm_s16le" {
				t.Errorf("audio codec %s", s.CodecName)
			}
			if d, _ := strconv.ParseFloat(s.Duration, 64); math.Abs(d-50.0/30) > 0.05 {
				t.Errorf("audio duration %v", d)
			}
		}
	}
	if !audio {
		t.Error("no audio stream")
	}
	decodes(t, path)
}
