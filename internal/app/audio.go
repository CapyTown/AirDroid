package app

import (
	"encoding/binary"
	"sync"

	"github.com/egoist/mygo/audio"
)

// pcmStream plays the device's audio as it arrives: 16-bit stereo PCM at
// 48 kHz, which MyGo's mixer plays as it is, with nothing to decode. It
// keeps about 60 ms buffered, to ride out the network's jitter, and drops
// what lags further behind, so that sound keeps up with the picture.
type pcmStream struct {
	mu        sync.Mutex
	buf       []int16 // interleaved, from head
	head      int
	buffering bool
	muted     bool
}

const (
	pcmRate     = 48000
	pcmChannels = 2
	pcmTarget   = pcmRate * 60 / 1000 * pcmChannels  // samples kept ahead
	pcmMax      = pcmRate * 250 / 1000 * pcmChannels // beyond, catch up
)

func newPCMStream() *pcmStream { return &pcmStream{buffering: true} }

// push queues PCM bytes, s16le interleaved.
func (s *pcmStream) push(pcm []byte) {
	n := len(pcm) / 2
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.head > 0 && s.head >= len(s.buf)/2 {
		s.buf = append(s.buf[:0], s.buf[s.head:]...)
		s.head = 0
	}
	for i := 0; i < n; i++ {
		s.buf = append(s.buf, int16(binary.LittleEndian.Uint16(pcm[2*i:])))
	}
	if avail := len(s.buf) - s.head; avail > pcmMax {
		drop := avail - pcmTarget
		drop -= drop % pcmChannels
		s.head += drop
	}
}

func (s *pcmStream) setMuted(m bool) {
	s.mu.Lock()
	s.muted = m
	s.mu.Unlock()
}

func (s *pcmStream) SampleRate() int               { return pcmRate }
func (s *pcmStream) Channels() int                 { return pcmChannels }
func (s *pcmStream) Len() int64                    { return -1 }
func (s *pcmStream) SetPosition(frame int64) error { return nil }

// Read never blocks: what has not arrived plays as silence.
func (s *pcmStream) Read(out []float32) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	avail := len(s.buf) - s.head
	if s.buffering {
		if avail < pcmTarget {
			clear(out)
			return len(out), nil
		}
		s.buffering = false
	}
	n := min(avail, len(out))
	n -= n % pcmChannels
	const scale = 1.0 / 32768
	src := s.buf[s.head : s.head+n]
	if s.muted {
		clear(out[:n])
	} else {
		for i, v := range src {
			out[i] = float32(v) * scale
		}
	}
	s.head += n
	if n < len(out) {
		clear(out[n:])
		s.buffering = true // ran dry: build the buffer up again
	}
	return len(out), nil
}

var _ audio.Decoder = (*pcmStream)(nil)
