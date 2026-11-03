package app

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/CapyTown/AirDroid/internal/media"
	"github.com/CapyTown/AirDroid/internal/mp4"
	"github.com/CapyTown/AirDroid/internal/scrcpy"
)

// recorder writes the stream to movies on a goroutine of its own, so that a
// slow disk never holds up the picture or the input.
//
// It gets every frame, recording or not, to keep the frames since the last
// key frame: a recording then starts at once, at the frame shown when the
// button was pressed, decoding from that key frame (an edit list hides
// what came before it). The frames are the ones the device encoded,
// written as they are: nothing is decoded or encoded to record.
type recorder struct {
	events  chan recEvent
	dropped atomic.Bool // an event did not fit: the disk fell behind
	active  atomic.Bool // recording: audio is sent too
	// report runs on the recorder's goroutine.
	report func(recReport)
	// needKey asks the device for a key frame.
	needKey func()
	// done is closed once the recorder stopped, its movie finished.
	done chan struct{}
}

type recEventKind int

const (
	evConfig recEventKind = iota
	evSize
	evFrame
	evAudio
	evStart
	evStop
	evQuit
)

type recEvent struct {
	kind   recEventKind
	params *media.ParameterSets
	w, h   int
	frame  scrcpy.VideoFrame
	pts    int64
	pcm    []byte
	req    recRequest
}

type recRequest struct {
	path   string // without the extension
	format mp4.Format
	audio  bool
}

// recReport tells the window what became of a recording.
type recReport struct {
	started  bool
	finished bool
	paths    []string
	err      error
}

const gopLimit = 64 << 20 // bytes of frames kept since the last key frame

func newRecorder(report func(recReport), needKey func()) *recorder {
	r := &recorder{events: make(chan recEvent, 4096), report: report, needKey: needKey, done: make(chan struct{})}
	go r.run()
	return r
}

func (r *recorder) send(ev recEvent) {
	select {
	case r.events <- ev:
	default:
		r.dropped.Store(true)
	}
}

// Stream events, from the session's goroutines.
func (r *recorder) config(p *media.ParameterSets) { r.send(recEvent{kind: evConfig, params: p}) }
func (r *recorder) size(w, h int)                 { r.send(recEvent{kind: evSize, w: w, h: h}) }
func (r *recorder) frame(f scrcpy.VideoFrame)     { r.send(recEvent{kind: evFrame, frame: f}) }
func (r *recorder) audio(pts int64, pcm []byte) {
	if r.active.Load() {
		r.send(recEvent{kind: evAudio, pts: pts, pcm: pcm})
	}
}

// Control, from the window. Start and stop must reach the recorder even
// when frames crowd its queue.
func (r *recorder) start(req recRequest) { r.events <- recEvent{kind: evStart, req: req} }
func (r *recorder) stop()                { r.events <- recEvent{kind: evStop} }
func (r *recorder) quit()                { r.events <- recEvent{kind: evQuit} }

type recState struct {
	params   *media.ParameterSets
	w, h     int
	gop      []scrcpy.VideoFrame
	gopBytes int
	gopOK    bool // gop starts at a key frame and lacks nothing
	lastPTS  int64

	recording bool
	req       recRequest
	writer    *mp4.Writer
	segment   int
	paths     []string
	startPTS  int64
	hasStart  bool
}

func (r *recorder) run() {
	defer close(r.done)
	var s recState
	for ev := range r.events {
		if r.dropped.Swap(false) {
			s.gop, s.gopBytes, s.gopOK = nil, 0, false
			if s.recording {
				r.finish(&s, errors.New("recording stopped: the disk could not keep up"))
			}
		}
		switch ev.kind {
		case evConfig:
			if ev.params.Equal(s.params) {
				continue
			}
			s.params = ev.params
			r.formatChanged(&s)
		case evSize:
			if ev.w == s.w && ev.h == s.h {
				continue
			}
			s.w, s.h = ev.w, ev.h
			r.formatChanged(&s)
		case evFrame:
			r.onFrame(&s, ev.frame)
		case evAudio:
			if s.writer != nil {
				if err := s.writer.WriteAudio(ev.pts, ev.pcm); err != nil {
					r.finish(&s, err)
				}
			}
		case evStart:
			if s.recording {
				continue
			}
			s.recording, s.req, s.segment, s.paths, s.hasStart = true, ev.req, 0, nil, false
			r.active.Store(ev.req.audio)
			r.report(recReport{started: true})
			if s.gopOK && len(s.gop) > 0 && s.params != nil {
				if err := r.open(&s); err != nil {
					r.finish(&s, err)
					continue
				}
				s.writer.SetStart(s.lastPTS)
				s.startPTS, s.hasStart = s.lastPTS, true
				for _, f := range s.gop {
					if err := s.writer.WriteVideo(f.PTS, f.Key, f.Data); err != nil {
						r.finish(&s, err)
						break
					}
				}
			} else if r.needKey != nil {
				r.needKey()
			}
		case evStop:
			if s.recording {
				r.finish(&s, nil)
			}
		case evQuit:
			if s.recording {
				r.finish(&s, nil)
			}
			return
		}
	}
}

func (r *recorder) onFrame(s *recState, f scrcpy.VideoFrame) {
	s.lastPTS = f.PTS
	if f.Key {
		s.gop, s.gopBytes, s.gopOK = s.gop[:0], 0, true
	}
	if s.gopOK {
		if s.gopBytes+len(f.Data) > gopLimit {
			s.gop, s.gopBytes, s.gopOK = nil, 0, false
		} else {
			s.gop = append(s.gop, f)
			s.gopBytes += len(f.Data)
		}
	}
	if !s.recording {
		return
	}
	if s.writer == nil {
		if !f.Key || s.params == nil {
			return // wait for a key frame
		}
		if err := r.open(s); err != nil {
			r.finish(s, err)
			return
		}
		if !s.hasStart {
			s.writer.SetStart(f.PTS)
			s.startPTS, s.hasStart = f.PTS, true
		}
	}
	if err := s.writer.WriteVideo(f.PTS, f.Key, f.Data); err != nil {
		r.finish(s, err)
	}
}

// formatChanged starts a new file: a movie holds frames of one size and
// one configuration, as when the device rotates.
func (r *recorder) formatChanged(s *recState) {
	s.gop, s.gopBytes, s.gopOK = nil, 0, false
	if s.writer != nil {
		if err := s.writer.Close(); err != nil {
			slog.Error("finishing a recording", "err", err)
		}
		s.writer = nil
	}
}

func (r *recorder) open(s *recState) error {
	ext := ".mp4"
	if s.req.format == mp4.MOV {
		ext = ".mov"
	}
	s.segment++
	path := s.req.path + ext
	if s.segment > 1 {
		path = fmt.Sprintf("%s (%d)%s", s.req.path, s.segment, ext)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	w, h := s.w, s.h
	if w == 0 || h == 0 {
		if info, err := media.ParseSPS(s.params.Codec, s.params.SPS[0]); err == nil {
			w, h = info.Width, info.Height
		}
	}
	var a *mp4.Audio
	if s.req.audio {
		a = &mp4.Audio{SampleRate: pcmRate, Channels: pcmChannels}
	}
	wr, err := mp4.Create(path, s.req.format, mp4.Video{Codec: s.params.Codec, Width: w, Height: h, Params: s.params}, a)
	if err != nil {
		return err
	}
	s.writer = wr
	s.paths = append(s.paths, path)
	return nil
}

func (r *recorder) finish(s *recState, cause error) {
	var err error
	if s.writer != nil {
		err = s.writer.Close()
		if s.writer.Frames() == 0 {
			os.Remove(s.paths[len(s.paths)-1])
			s.paths = s.paths[:len(s.paths)-1]
		}
		s.writer = nil
	}
	if cause != nil {
		err = cause
	}
	s.recording = false
	r.active.Store(false)
	r.report(recReport{finished: true, paths: s.paths, err: err})
}

// recordingName is the file name of a recording, without its extension.
func recordingName(device string, stamp string) string {
	name := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '-'
		}
		return r
	}, device)
	return "AirDroid " + name + " " + stamp
}
