// Package ffdec decodes H.264 and H.265 with an FFmpeg program, on Windows
// and Linux, where AirDroid has no system decoder to call without cgo.
//
// The encoded frames go to FFmpeg's standard input as they arrive, and the
// pictures come back on its standard output as raw RGBA, already scaled
// to the size they show at: nothing is encoded again, and nothing but the
// pixels the window shows crosses back. FFmpeg decodes with one thread
// and no buffering, so that each frame comes out as soon as it goes in.
package ffdec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CapyTown/AirDroid/internal/media"
	"github.com/CapyTown/AirDroid/internal/proc"
	"github.com/CapyTown/AirDroid/internal/video"
)

// ErrNoFFmpeg is returned when there is no FFmpeg to decode with.
var ErrNoFFmpeg = errors.New("FFmpeg was not found: AirDroid needs it to show the screen")

// Decoder runs one FFmpeg process at a time, again for each new
// configuration and size.
type Decoder struct {
	ffmpeg  string
	out     video.Output
	needKey func()
	pool    video.Pool
	target  atomic.Uint64

	params     *media.ParameterSets
	srcW, srcH int
	p          *process
	waitKey    bool
	resizeAt   time.Time // when the target size first differed from the process's
	asked      bool      // a key frame was asked for this resize
}

// New makes a decoder that runs the FFmpeg program at path.
func New(ffmpeg string, out video.Output, needKey func()) (*Decoder, error) {
	if ffmpeg == "" {
		return nil, ErrNoFFmpeg
	}
	return &Decoder{ffmpeg: ffmpeg, out: out, needKey: needKey, waitKey: true}, nil
}

// SetTargetSize sets the box, in pixels, pictures are scaled to fit.
func (d *Decoder) SetTargetSize(w, h int) {
	d.target.Store(uint64(uint32(w))<<32 | uint64(uint32(h)))
}

// SetFormat starts a new process for new parameter sets, which a key frame
// follows.
func (d *Decoder) SetFormat(p *media.ParameterSets) error {
	if p.Empty() {
		return nil
	}
	info, err := media.ParseSPS(p.Codec, p.SPS[0])
	if err != nil {
		return err
	}
	d.params, d.srcW, d.srcH = p, info.Width, info.Height
	d.stop()
	d.waitKey = true
	return nil
}

func (d *Decoder) wanted() (int, int) {
	t := d.target.Load()
	return video.FitSize(d.srcW, d.srcH, int(t>>32), int(uint32(t)))
}

// Decode sends an access unit to FFmpeg.
func (d *Decoder) Decode(data []byte, key bool) error {
	if d.params == nil || len(data) == 0 {
		return nil
	}
	w, h := d.wanted()
	if d.p != nil && (d.p.w != w || d.p.h != h) {
		// The window changed size: a new process at the next key frame.
		// FFmpeg starts at a key frame; ask the device for one once the
		// size settles, rather than wait for the next.
		if d.resizeAt.IsZero() {
			d.resizeAt = time.Now()
		}
		if key {
			d.stop()
		} else if !d.asked && time.Since(d.resizeAt) > 400*time.Millisecond && d.needKey != nil {
			d.asked = true
			d.needKey()
		}
	}
	if d.p != nil && d.p.exited() {
		d.stop()
		d.waitKey = true
	}
	if d.p == nil {
		if !key {
			d.waitKey = true
			return nil
		}
		if err := d.start(w, h); err != nil {
			return err
		}
		d.resizeAt, d.asked = time.Time{}, false
	}
	if d.waitKey {
		if !key {
			return nil
		}
		d.waitKey = false
	}
	unit := annexB(d.params, data, key)
	select {
	case d.p.in <- unit:
	default:
		// FFmpeg fell behind: skip to the next key frame rather than queue.
		d.waitKey = true
		if d.needKey != nil {
			d.needKey()
		}
	}
	return nil
}

// annexB turns a length-prefixed access unit back into Annex B, which
// FFmpeg reads from a pipe, with the parameter sets before a key frame.
func annexB(p *media.ParameterSets, data []byte, key bool) []byte {
	var b bytes.Buffer
	start := []byte{0, 0, 0, 1}
	if key {
		for _, nal := range p.All() {
			b.Write(start)
			b.Write(nal)
		}
	}
	for _, nal := range media.SplitLengthPrefixed(data) {
		b.Write(start)
		b.Write(nal)
	}
	// An access unit delimiter after the frame tells FFmpeg's parser the
	// frame is whole, without waiting for the next one.
	b.Write(start)
	if p.Codec == media.H265 {
		b.Write([]byte{0x46, 0x01, 0x50})
	} else {
		b.Write([]byte{0x09, 0xf0})
	}
	return b.Bytes()
}

// Args returns FFmpeg's arguments for a stream of codec scaled to w×h.
func Args(codec media.Codec, w, h int) []string {
	format := "h264"
	if codec == media.H265 {
		format = "hevc"
	}
	return []string{
		// No "-fflags nobuffer": on a pipe it holds back half the frames
		// of a raw H.264 stream.
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-flags", "low_delay",
		"-probesize", "32", "-analyzeduration", "0",
		"-threads", "1",
		"-f", format, "-i", "pipe:0",
		"-an", "-vf", "scale=" + strconv.Itoa(w) + ":" + strconv.Itoa(h) + ":flags=bilinear",
		"-pix_fmt", "rgba", "-f", "rawvideo", "-fps_mode", "passthrough", "-flush_packets", "1",
		"pipe:1",
	}
}

type process struct {
	cmd  *exec.Cmd
	w, h int
	in   chan []byte
	done chan struct{}
	once sync.Once
}

func (p *process) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func (d *Decoder) start(w, h int) error {
	cmd := proc.Command(context.Background(), d.ffmpeg, Args(d.params.Codec, w, h)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4096}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start FFmpeg: %w", err)
	}
	p := &process{cmd: cmd, w: w, h: h, in: make(chan []byte, 90), done: make(chan struct{})}
	d.p = p
	go func() {
		defer stdin.Close()
		for {
			select {
			case unit, ok := <-p.in:
				if !ok {
					return
				}
				if _, err := stdin.Write(unit); err != nil {
					return
				}
			case <-p.done:
				return
			}
		}
	}()
	out := d.out
	pool := &d.pool
	go func() {
		defer p.once.Do(func() { close(p.done) })
		for {
			img := pool.Get(w, h)
			if _, err := io.ReadFull(stdout, img.Pix); err != nil {
				pool.Put(img)
				break
			}
			out(img, func() { pool.Put(img) })
		}
		if err := cmd.Wait(); err != nil && stderr.Len() > 0 {
			slog.Warn("ffmpeg", "err", err, "output", stderr.String())
		}
	}()
	return nil
}

func (d *Decoder) stop() {
	if d.p == nil {
		return
	}
	p := d.p
	d.p = nil
	close(p.in)
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

// Close ends the decoder's process.
func (d *Decoder) Close() { d.stop() }

// limitedWriter keeps the first n bytes written to it.
type limitedWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
	n  int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if room := l.n - l.w.Len(); room > 0 {
		l.w.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

var _ video.Decoder = (*Decoder)(nil)
