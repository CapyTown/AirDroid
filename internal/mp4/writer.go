// Package mp4 writes MP4 and QuickTime movies from encoded H.264 or H.265
// frames, and PCM audio, as they arrive: the samples go to the file at
// once, and the index (moov) follows when the movie is closed. Nothing is
// decoded or encoded: a recording costs the bytes it writes.
package mp4

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"os"

	"github.com/CapyTown/AirDroid/internal/media"
)

// Format is the container.
type Format int

const (
	MP4 Format = iota // .mp4: video only
	MOV               // .mov: video and 16-bit PCM audio
)

// Video describes the video track.
type Video struct {
	Codec         media.Codec
	Width, Height int
	Params        *media.ParameterSets
}

// Audio describes a PCM audio track: signed 16-bit little-endian samples.
type Audio struct {
	SampleRate int
	Channels   int
}

const (
	movieTimescale = 1000
	videoTimescale = 90000
)

type sample struct {
	offset int64
	size   uint32
	pts    int64 // microseconds
	key    bool
	frames uint32 // audio: frames in this chunk
}

type track struct {
	id      uint32
	samples []sample
}

// Writer writes one movie.
type Writer struct {
	f           *os.File
	w           *bufio.Writer
	off         int64
	format      Format
	video       Video
	audio       *Audio
	vt, at      track
	mdatPos     int64
	start       int64 // presentation start, microseconds
	hasStart    bool
	audioPTS    int64 // first audio PTS
	closed      bool
	sampleEntry []byte
}

// Create starts a movie at path. Audio is kept only in MOV files.
func Create(path string, format Format, v Video, a *Audio) (*Writer, error) {
	if v.Params == nil || v.Params.Empty() {
		return nil, errors.New("mp4: no parameter sets")
	}
	entry, err := videoSampleEntry(v)
	if err != nil {
		return nil, err
	}
	if format != MOV {
		a = nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := &Writer{f: f, w: bufio.NewWriterSize(f, 1<<20), format: format, video: v, audio: a, sampleEntry: entry}
	w.vt.id = 1
	w.at.id = 2
	w.writeFtyp()
	// mdat with a 64-bit size, filled in on Close.
	w.mdatPos = w.off
	w.put32(1)
	w.putStr("mdat")
	w.put64(0)
	if err := w.w.Flush(); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	return w, nil
}

// HasAudio reports whether the movie records audio.
func (w *Writer) HasAudio() bool { return w.audio != nil }

// SetStart sets when the movie's presentation starts: frames before it,
// from the key frame that the first frames decode from, are kept but not
// shown.
func (w *Writer) SetStart(pts int64) {
	w.start, w.hasStart = pts, true
}

// WriteVideo appends a length-prefixed access unit.
func (w *Writer) WriteVideo(pts int64, key bool, data []byte) error {
	if w.closed {
		return errors.New("mp4: closed")
	}
	if len(w.vt.samples) == 0 && !key {
		return nil // a movie starts at a key frame
	}
	if n := len(w.vt.samples); n > 0 && pts <= w.vt.samples[n-1].pts {
		pts = w.vt.samples[n-1].pts + 1
	}
	w.vt.samples = append(w.vt.samples, sample{offset: w.off, size: uint32(len(data)), pts: pts, key: key})
	_, err := w.write(data)
	return err
}

// WriteAudio appends PCM samples.
func (w *Writer) WriteAudio(pts int64, pcm []byte) error {
	if w.closed || w.audio == nil || len(w.vt.samples) == 0 {
		return nil
	}
	if w.hasStart && pts < w.start {
		return nil
	}
	frameSize := 2 * w.audio.Channels
	pcm = pcm[:len(pcm)/frameSize*frameSize]
	if len(pcm) == 0 {
		return nil
	}
	if len(w.at.samples) == 0 {
		w.audioPTS = pts
	}
	w.at.samples = append(w.at.samples, sample{offset: w.off, size: uint32(len(pcm)), pts: pts, frames: uint32(len(pcm) / frameSize)})
	_, err := w.write(pcm)
	return err
}

// Frames returns how many video frames the movie holds.
func (w *Writer) Frames() int { return len(w.vt.samples) }

// Close writes the index and closes the file.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	mdatEnd := w.off
	moov := w.moov()
	if _, err := w.write(moov); err != nil {
		w.f.Close()
		return err
	}
	if err := w.w.Flush(); err != nil {
		w.f.Close()
		return err
	}
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(mdatEnd-w.mdatPos))
	if _, err := w.f.WriteAt(size[:], w.mdatPos+8); err != nil {
		w.f.Close()
		return err
	}
	return w.f.Close()
}

// Abort closes the file without finishing it.
func (w *Writer) Abort() {
	if !w.closed {
		w.closed = true
		w.f.Close()
	}
}

func (w *Writer) write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.off += int64(n)
	return n, err
}

func (w *Writer) put32(v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	w.write(b[:])
}

func (w *Writer) put64(v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	w.write(b[:])
}

func (w *Writer) putStr(s string) { w.write([]byte(s)) }

func (w *Writer) writeFtyp() {
	var b box
	if w.format == MOV {
		b.start("ftyp")
		b.str("qt  ")
		b.u32(0x20050300)
		b.str("qt  ")
	} else {
		b.start("ftyp")
		b.str("isom")
		b.u32(0x200)
		b.str("isom")
		b.str("iso2")
		if w.video.Codec == media.H265 {
			b.str("hvc1")
		} else {
			b.str("avc1")
		}
		b.str("mp41")
	}
	b.end()
	w.write(b.buf)
}

// box builds nested boxes.
type box struct {
	buf   []byte
	stack []int
}

func (b *box) start(typ string) {
	b.stack = append(b.stack, len(b.buf))
	b.buf = append(b.buf, 0, 0, 0, 0)
	b.buf = append(b.buf, typ...)
}

func (b *box) full(typ string, version byte, flags uint32) {
	b.start(typ)
	b.u32(uint32(version)<<24 | flags&0xffffff)
}

func (b *box) end() {
	i := b.stack[len(b.stack)-1]
	b.stack = b.stack[:len(b.stack)-1]
	binary.BigEndian.PutUint32(b.buf[i:], uint32(len(b.buf)-i))
}

func (b *box) u8(v byte)    { b.buf = append(b.buf, v) }
func (b *box) u16(v uint16) { b.buf = binary.BigEndian.AppendUint16(b.buf, v) }
func (b *box) u32(v uint32) { b.buf = binary.BigEndian.AppendUint32(b.buf, v) }
func (b *box) u64(v uint64) { b.buf = binary.BigEndian.AppendUint64(b.buf, v) }
func (b *box) str(s string) { b.buf = append(b.buf, s...) }
func (b *box) bytes(p []byte) {
	b.buf = append(b.buf, p...)
}
func (b *box) zeros(n int) { b.buf = append(b.buf, make([]byte, n)...) }

var identityMatrix = []uint32{0x10000, 0, 0, 0, 0x10000, 0, 0, 0, 0x40000000}

func videoSampleEntry(v Video) ([]byte, error) {
	var b box
	var cfg []byte
	var err error
	if v.Codec == media.H265 {
		cfg, err = media.HVCC(v.Params)
		b.start("hvc1")
	} else {
		cfg, err = media.AVCC(v.Params)
		b.start("avc1")
	}
	if err != nil {
		return nil, err
	}
	b.zeros(6)
	b.u16(1) // data_reference_index
	b.zeros(16)
	b.u16(uint16(v.Width))
	b.u16(uint16(v.Height))
	b.u32(0x00480000)
	b.u32(0x00480000)
	b.u32(0)
	b.u16(1) // frame_count
	name := make([]byte, 32)
	n := copy(name[1:], "MyGo")
	name[0] = byte(n)
	b.bytes(name)
	b.u16(0x18)
	b.u16(0xffff)
	if v.Codec == media.H265 {
		b.start("hvcC")
	} else {
		b.start("avcC")
	}
	b.bytes(cfg)
	b.end()
	b.end()
	return b.buf, nil
}

// videoDurations returns each frame's duration in the video timescale,
// computed from absolute times so that rounding does not drift.
func (w *Writer) videoDurations() []uint32 {
	s := w.vt.samples
	d := make([]uint32, len(s))
	base := s[0].pts
	ts := func(pts int64) int64 { return (pts - base) * videoTimescale / 1e6 }
	for i := 0; i+1 < len(s); i++ {
		v := ts(s[i+1].pts) - ts(s[i].pts)
		if v < 1 {
			v = 1
		}
		d[i] = uint32(v)
	}
	last := uint32(videoTimescale / 60)
	if len(s) > 1 {
		last = d[len(s)-2]
	}
	d[len(s)-1] = last
	return d
}

func (w *Writer) moov() []byte {
	var b box
	b.start("moov")
	if len(w.vt.samples) == 0 {
		w.mvhd(&b, 0, 2)
		b.end()
		return b.buf
	}
	durs := w.videoDurations()
	var mediaDur int64
	for _, d := range durs {
		mediaDur += int64(d)
	}
	first := w.vt.samples[0].pts
	start := first
	if w.hasStart && w.start > first {
		start = w.start
	}
	skip := (start - first) * videoTimescale / 1e6 // media time hidden at the start
	if skip >= mediaDur {
		skip = 0
	}
	shownMs := (mediaDur - skip) * movieTimescale / videoTimescale
	movieDur := shownMs

	var audioFrames int64
	for _, s := range w.at.samples {
		audioFrames += int64(s.frames)
	}
	var audioGapMs, audioMs int64
	if w.audio != nil && audioFrames > 0 {
		audioMs = audioFrames * movieTimescale / int64(w.audio.SampleRate)
		if gap := w.audioPTS - start; gap > 0 {
			audioGapMs = gap / 1000
		}
		if audioGapMs+audioMs > movieDur {
			movieDur = audioGapMs + audioMs
		}
	}
	next := uint32(2)
	if audioFrames > 0 {
		next = 3
	}
	w.mvhd(&b, movieDur, next)

	// Video.
	b.start("trak")
	w.tkhd(&b, w.vt.id, shownMs, true)
	b.start("edts")
	b.full("elst", 0, 0)
	b.u32(1)
	b.u32(uint32(shownMs))
	b.u32(uint32(skip))
	b.u32(0x10000)
	b.end()
	b.end()
	b.start("mdia")
	mdhd(&b, videoTimescale, mediaDur)
	w.hdlr(&b, "vide", "VideoHandler")
	b.start("minf")
	b.full("vmhd", 0, 1)
	b.zeros(8)
	b.end()
	dinf(&b)
	b.start("stbl")
	b.full("stsd", 0, 0)
	b.u32(1)
	b.bytes(w.sampleEntry)
	b.end()
	// stts, run-length
	b.full("stts", 0, 0)
	runsAt := len(b.buf)
	b.u32(0)
	runs := uint32(0)
	for i := 0; i < len(durs); {
		j := i
		for j < len(durs) && durs[j] == durs[i] {
			j++
		}
		b.u32(uint32(j - i))
		b.u32(durs[i])
		runs++
		i = j
	}
	binary.BigEndian.PutUint32(b.buf[runsAt:], runs)
	b.end()
	// stss
	b.full("stss", 0, 0)
	countAt := len(b.buf)
	b.u32(0)
	keys := uint32(0)
	for i, s := range w.vt.samples {
		if s.key {
			b.u32(uint32(i + 1))
			keys++
		}
	}
	binary.BigEndian.PutUint32(b.buf[countAt:], keys)
	b.end()
	// stsc: one sample per chunk
	b.full("stsc", 0, 0)
	b.u32(1)
	b.u32(1)
	b.u32(1)
	b.u32(1)
	b.end()
	b.full("stsz", 0, 0)
	b.u32(0)
	b.u32(uint32(len(w.vt.samples)))
	for _, s := range w.vt.samples {
		b.u32(s.size)
	}
	b.end()
	b.full("co64", 0, 0)
	b.u32(uint32(len(w.vt.samples)))
	for _, s := range w.vt.samples {
		b.u64(uint64(s.offset))
	}
	b.end()
	b.end() // stbl
	b.end() // minf
	b.end() // mdia
	b.end() // trak

	if audioFrames > 0 {
		w.audioTrak(&b, audioFrames, audioGapMs, audioMs)
	}
	b.end()
	return b.buf
}

func (w *Writer) audioTrak(b *box, frames, gapMs, durMs int64) {
	a := w.audio
	b.start("trak")
	w.tkhd(b, w.at.id, gapMs+durMs, false)
	b.start("edts")
	n := uint32(1)
	if gapMs > 0 {
		n = 2
	}
	b.full("elst", 0, 0)
	b.u32(n)
	if gapMs > 0 {
		b.u32(uint32(gapMs))
		b.u32(0xffffffff) // empty edit
		b.u32(0x10000)
	}
	b.u32(uint32(durMs))
	b.u32(0)
	b.u32(0x10000)
	b.end()
	b.end()
	b.start("mdia")
	mdhd(b, uint32(a.SampleRate), frames)
	w.hdlr(b, "soun", "SoundHandler")
	b.start("minf")
	b.full("smhd", 0, 0)
	b.u32(0)
	b.end()
	dinf(b)
	b.start("stbl")
	b.full("stsd", 0, 0)
	b.u32(1)
	// QuickTime sound description, version 0, little-endian 16-bit PCM.
	b.start("sowt")
	b.zeros(6)
	b.u16(1)
	b.u16(0) // version
	b.u16(0) // revision
	b.u32(0) // vendor
	b.u16(uint16(a.Channels))
	b.u16(16)
	b.u16(0) // compression id
	b.u16(0) // packet size
	b.u32(uint32(a.SampleRate) << 16)
	b.end()
	b.end()
	b.full("stts", 0, 0)
	b.u32(1)
	b.u32(uint32(frames))
	b.u32(1)
	b.end()
	// stsc: chunks of varying frame counts, run-length.
	b.full("stsc", 0, 0)
	countAt := len(b.buf)
	b.u32(0)
	entries := uint32(0)
	prev := uint32(0)
	for i, s := range w.at.samples {
		if s.frames != prev {
			b.u32(uint32(i + 1))
			b.u32(s.frames)
			b.u32(1)
			entries++
			prev = s.frames
		}
	}
	binary.BigEndian.PutUint32(b.buf[countAt:], entries)
	b.end()
	b.full("stsz", 0, 0)
	b.u32(uint32(2 * a.Channels))
	b.u32(uint32(frames))
	b.end()
	b.full("co64", 0, 0)
	b.u32(uint32(len(w.at.samples)))
	for _, s := range w.at.samples {
		b.u64(uint64(s.offset))
	}
	b.end()
	b.end() // stbl
	b.end() // minf
	b.end() // mdia
	b.end() // trak
}

func (w *Writer) mvhd(b *box, durMs int64, nextTrack uint32) {
	b.full("mvhd", 0, 0)
	b.u32(0) // creation
	b.u32(0) // modification
	b.u32(movieTimescale)
	b.u32(uint32(durMs))
	b.u32(0x10000) // rate
	b.u16(0x100)   // volume
	b.zeros(10)
	for _, m := range identityMatrix {
		b.u32(m)
	}
	b.zeros(24)
	b.u32(nextTrack)
	b.end()
}

func (w *Writer) tkhd(b *box, id uint32, durMs int64, video bool) {
	b.full("tkhd", 0, 3) // enabled, in movie
	b.u32(0)
	b.u32(0)
	b.u32(id)
	b.u32(0)
	b.u32(uint32(durMs))
	b.zeros(8)
	b.u16(0) // layer
	if video {
		b.u16(0)
		b.u16(0) // volume
	} else {
		b.u16(1)
		b.u16(0x100)
	}
	b.u16(0)
	for _, m := range identityMatrix {
		b.u32(m)
	}
	if video {
		b.u32(uint32(w.video.Width) << 16)
		b.u32(uint32(w.video.Height) << 16)
	} else {
		b.u32(0)
		b.u32(0)
	}
	b.end()
}

func mdhd(b *box, timescale uint32, dur int64) {
	b.full("mdhd", 0, 0)
	b.u32(0)
	b.u32(0)
	b.u32(timescale)
	b.u32(uint32(dur))
	b.u16(0x55c4) // "und"
	b.u16(0)
	b.end()
}

func (w *Writer) hdlr(b *box, typ, name string) {
	b.full("hdlr", 0, 0)
	if w.format == MOV {
		b.str("mhlr")
	} else {
		b.u32(0)
	}
	b.str(typ)
	b.zeros(12)
	if w.format == MOV {
		b.u8(byte(len(name))) // Pascal string
		b.str(name)
	} else {
		b.str(name)
		b.u8(0)
	}
	b.end()
}

func dinf(b *box) {
	b.start("dinf")
	b.full("dref", 0, 0)
	b.u32(1)
	b.full("url ", 0, 1)
	b.end()
	b.end()
	b.end()
}

var _ io.Writer = (*bufio.Writer)(nil)
