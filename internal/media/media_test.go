package media

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestSplitAnnexB(t *testing.T) {
	data := []byte{0, 0, 0, 1, 0x67, 1, 2, 0, 0, 1, 0x68, 3, 0, 0, 0, 1, 0x65, 4, 5, 0}
	nals := SplitAnnexB(data)
	want := [][]byte{{0x67, 1, 2}, {0x68, 3}, {0x65, 4, 5}}
	if len(nals) != len(want) {
		t.Fatalf("got %d units, want %d: %x", len(nals), len(want), nals)
	}
	for i := range want {
		if !bytes.Equal(nals[i], want[i]) {
			t.Errorf("unit %d = %x, want %x", i, nals[i], want[i])
		}
	}
}

func TestToLengthPrefixedInPlace(t *testing.T) {
	data := []byte{0, 0, 0, 1, 0x65, 1, 2, 3, 0, 0, 0, 1, 0x41, 9}
	before := &data[0]
	f := ToLengthPrefixed(H264, data)
	if &f.Data[0] != before {
		t.Error("converted with a copy; 4-byte start codes convert in place")
	}
	want := []byte{0, 0, 0, 4, 0x65, 1, 2, 3, 0, 0, 0, 2, 0x41, 9}
	if !bytes.Equal(f.Data, want) {
		t.Errorf("got %x, want %x", f.Data, want)
	}
	if !f.IDR || f.Params != nil {
		t.Errorf("IDR %v, params %v", f.IDR, f.Params)
	}
}

func TestToLengthPrefixedMovesParameterSets(t *testing.T) {
	data := []byte{0, 0, 0, 1, 0x09, 0xf0, 0, 0, 1, 0x67, 1, 2, 3, 0, 0, 1, 0x68, 4, 0, 0, 1, 0x65, 7}
	f := ToLengthPrefixed(H264, data)
	if f.Params == nil || len(f.Params.SPS) != 1 || len(f.Params.PPS) != 1 {
		t.Fatalf("params %+v", f.Params)
	}
	want := []byte{0, 0, 0, 2, 0x65, 7}
	if !bytes.Equal(f.Data, want) {
		t.Errorf("got %x, want %x", f.Data, want)
	}
}

// encode makes a short stream with ffmpeg, as Annex B with access unit
// delimiters, or skips the test without ffmpeg.
func encode(t *testing.T, codec Codec, w, h, frames int) []byte {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	out := filepath.Join(t.TempDir(), "stream")
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i",
		"testsrc2=size=" + itoa(w) + "x" + itoa(h) + ":rate=30", "-frames:v", itoa(frames), "-pix_fmt", "yuv420p"}
	if codec == H265 {
		args = append(args, "-c:v", "libx265", "-x265-params", "aud=1:log-level=error:keyint=15", "-f", "hevc", out)
	} else {
		args = append(args, "-c:v", "libx264", "-profile:v", "high", "-x264-params", "aud=1:keyint=15:bframes=0", "-f", "h264", out)
	}
	if b, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg: %v %s", err, b)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestParseSPSOfRealStreams(t *testing.T) {
	for _, tc := range []struct {
		codec Codec
		w, h  int
	}{{H264, 1080, 2400}, {H264, 720, 406}, {H265, 1080, 2400}, {H265, 640, 360}} {
		data := encode(t, tc.codec, tc.w, tc.h, 2)
		p := ParseConfig(tc.codec, data)
		if p.Empty() {
			t.Fatalf("%v: no parameter sets", tc.codec)
		}
		info, err := ParseSPS(tc.codec, p.SPS[0])
		if err != nil {
			t.Fatalf("%v: %v", tc.codec, err)
		}
		if info.Width != tc.w || info.Height != tc.h {
			t.Errorf("%v: size %dx%d, want %dx%d", tc.codec, info.Width, info.Height, tc.w, tc.h)
		}
		if tc.codec == H265 {
			if _, err := HVCC(p); err != nil {
				t.Error(err)
			}
		} else if _, err := AVCC(p); err != nil {
			t.Error(err)
		}
	}
}
