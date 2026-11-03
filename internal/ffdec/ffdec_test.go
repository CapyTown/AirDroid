package ffdec

import (
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/CapyTown/AirDroid/internal/media"
)

// A stream FFmpeg encodes is decoded back by FFmpeg, scaled to the target.
func TestDecode(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	out := filepath.Join(t.TempDir(), "s.h264")
	if b, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=720x1600:rate=30",
		"-frames:v", "40", "-pix_fmt", "yuv420p", "-c:v", "libx264", "-x264-params", "aud=1:keyint=20:bframes=0", "-f", "h264", out).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg: %v %s", err, b)
	}
	data, _ := os.ReadFile(out)

	var mu sync.Mutex
	var frames []image.Rectangle
	var lit bool
	d, err := New(ffmpeg, func(img *image.RGBA, release func()) {
		mu.Lock()
		frames = append(frames, img.Rect)
		c := img.RGBAAt(img.Rect.Dx()/2, img.Rect.Dy()/2)
		lit = lit || c.R+c.G+c.B > 0
		mu.Unlock()
		release()
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.SetTargetSize(360, 800)

	var cur []byte
	units := 0
	flush := func() {
		if cur == nil {
			return
		}
		f := media.ToLengthPrefixed(media.H264, cur)
		if f.Params != nil && units == 0 {
			if err := d.SetFormat(f.Params); err != nil {
				t.Fatal(err)
			}
		}
		if err := d.Decode(f.Data, f.IDR); err != nil {
			t.Fatal(err)
		}
		units++
		cur = nil
	}
	for _, nal := range media.SplitAnnexB(data) {
		if media.NALType(media.H264, nal) == 9 {
			flush()
		}
		cur = append(append(cur, 0, 0, 0, 1), nal...)
	}
	flush()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(frames)
		mu.Unlock()
		if n >= 39 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	// Frames come out while the input stays open, at most one behind:
	// FFmpeg holds none back.
	if len(frames) < 39 {
		t.Fatalf("%d of 40 frames came out", len(frames))
	}
	if frames[0].Dx() != 360 || frames[0].Dy() != 800 {
		t.Errorf("frame size %v, want 360x800", frames[0])
	}
	if !lit {
		t.Error("the frames are black")
	}
}
