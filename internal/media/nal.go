// Package media handles H.264 and H.265 bitstreams: splitting Annex B
// streams into NAL units, finding parameter sets, converting to the
// length-prefixed form decoders and MP4 files take, and reading the
// picture size out of a sequence parameter set.
package media

import (
	"bytes"
	"encoding/binary"
)

// Codec is a video codec.
type Codec int

const (
	H264 Codec = iota + 1
	H265
)

func (c Codec) String() string {
	switch c {
	case H264:
		return "H.264"
	case H265:
		return "H.265"
	}
	return "unknown"
}

// NALType returns the type of a NAL unit.
func NALType(c Codec, nal []byte) int {
	if len(nal) == 0 {
		return -1
	}
	if c == H265 {
		return int(nal[0]>>1) & 0x3f
	}
	return int(nal[0] & 0x1f)
}

// NAL unit types MyGo cares about.
const (
	h264IDR = 5
	h264SPS = 7
	h264PPS = 8
	h264AUD = 9

	h265VPS = 32
	h265SPS = 33
	h265PPS = 34
	h265AUD = 35
)

// IsParameterSet reports whether nal is a VPS, SPS or PPS.
func IsParameterSet(c Codec, nal []byte) bool {
	t := NALType(c, nal)
	if c == H265 {
		return t == h265VPS || t == h265SPS || t == h265PPS
	}
	return t == h264SPS || t == h264PPS
}

// SplitAnnexB returns the NAL units of an Annex B stream, without their
// start codes. The units share data's memory.
func SplitAnnexB(data []byte) [][]byte {
	var nals [][]byte
	start := -1
	i := 0
	for i+2 < len(data) {
		if data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
			if start >= 0 {
				end := i
				if end > start && data[end-1] == 0 {
					end-- // the zero of a 4-byte start code
				}
				nals = appendNAL(nals, data[start:end])
			}
			i += 3
			start = i
			continue
		}
		i++
	}
	if start >= 0 && start <= len(data) {
		nals = appendNAL(nals, data[start:])
	} else if start < 0 && len(data) > 0 {
		// No start code: a single unit.
		nals = appendNAL(nals, data)
	}
	return nals
}

func appendNAL(nals [][]byte, nal []byte) [][]byte {
	// Trailing zeros belong to no unit.
	for len(nal) > 0 && nal[len(nal)-1] == 0 {
		nal = nal[:len(nal)-1]
	}
	if len(nal) == 0 {
		return nals
	}
	return append(nals, nal)
}

// ParameterSets keeps the parameter sets of a stream: VPS (H.265 only),
// SPS and PPS, in that order.
type ParameterSets struct {
	Codec Codec
	VPS   [][]byte
	SPS   [][]byte
	PPS   [][]byte
}

// Empty reports whether the sets lack what a decoder needs.
func (p *ParameterSets) Empty() bool {
	if p.Codec == H265 && len(p.VPS) == 0 {
		return true
	}
	return len(p.SPS) == 0 || len(p.PPS) == 0
}

// All returns the sets in the order decoders take them.
func (p *ParameterSets) All() [][]byte {
	out := make([][]byte, 0, len(p.VPS)+len(p.SPS)+len(p.PPS))
	out = append(out, p.VPS...)
	out = append(out, p.SPS...)
	return append(out, p.PPS...)
}

// Equal reports whether two sets are the same.
func (p *ParameterSets) Equal(o *ParameterSets) bool {
	if p == nil || o == nil {
		return p == o
	}
	eq := func(a, b [][]byte) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if !bytes.Equal(a[i], b[i]) {
				return false
			}
		}
		return true
	}
	return p.Codec == o.Codec && eq(p.VPS, o.VPS) && eq(p.SPS, o.SPS) && eq(p.PPS, o.PPS)
}

// Add records a parameter set NAL unit, copied, and reports whether it was
// one.
func (p *ParameterSets) Add(nal []byte) bool {
	t := NALType(p.Codec, nal)
	cp := append([]byte(nil), nal...)
	if p.Codec == H265 {
		switch t {
		case h265VPS:
			p.VPS = append(p.VPS, cp)
		case h265SPS:
			p.SPS = append(p.SPS, cp)
		case h265PPS:
			p.PPS = append(p.PPS, cp)
		default:
			return false
		}
		return true
	}
	switch t {
	case h264SPS:
		p.SPS = append(p.SPS, cp)
	case h264PPS:
		p.PPS = append(p.PPS, cp)
	default:
		return false
	}
	return true
}

// ParseConfig reads the parameter sets of a configuration packet.
func ParseConfig(c Codec, data []byte) *ParameterSets {
	p := &ParameterSets{Codec: c}
	for _, nal := range SplitAnnexB(data) {
		p.Add(nal)
	}
	return p
}

// Frame is an access unit in length-prefixed form (4-byte big-endian
// lengths), as VideoToolbox and MP4 files take it.
type Frame struct {
	Data []byte
	// Params holds parameter sets that came inside the access unit, which
	// is left without them; nil when there were none.
	Params *ParameterSets
	// IDR reports an instantaneous decoder refresh picture.
	IDR bool
}

// ToLengthPrefixed converts an Annex B access unit to 4-byte length
// prefixes, dropping access unit delimiters and moving parameter sets out
// to Frame.Params. When every start code is 4 bytes long and nothing is
// dropped, as MediaCodec writes them, it converts in place without
// copying.
func ToLengthPrefixed(c Codec, data []byte) Frame {
	nals := SplitAnnexB(data)
	var f Frame
	inPlace := true
	kept := nals[:0:0]
	for _, nal := range nals {
		t := NALType(c, nal)
		switch {
		case c == H264 && t == h264AUD, c == H265 && t == h265AUD:
			inPlace = false
			continue
		case IsParameterSet(c, nal):
			if f.Params == nil {
				f.Params = &ParameterSets{Codec: c}
			}
			f.Params.Add(nal)
			inPlace = false
			continue
		}
		if c == H264 && t == h264IDR || c == H265 && t >= 16 && t <= 21 {
			f.IDR = true
		}
		kept = append(kept, nal)
	}
	if inPlace && len(kept) > 0 {
		// Each unit must be preceded by exactly 4 bytes of start code
		// within data, with nothing else between units.
		off := 0
		for _, nal := range kept {
			pos := offsetOf(data, nal)
			if pos != off+4 || !bytes.Equal(data[off:pos], []byte{0, 0, 0, 1}) {
				inPlace = false
				break
			}
			off = pos + len(nal)
		}
		if inPlace && off == len(data) {
			for _, nal := range kept {
				pos := offsetOf(data, nal)
				binary.BigEndian.PutUint32(data[pos-4:pos], uint32(len(nal)))
			}
			f.Data = data
			return f
		}
	}
	size := 0
	for _, nal := range kept {
		size += 4 + len(nal)
	}
	out := make([]byte, size)
	off := 0
	for _, nal := range kept {
		binary.BigEndian.PutUint32(out[off:], uint32(len(nal)))
		copy(out[off+4:], nal)
		off += 4 + len(nal)
	}
	f.Data = out
	return f
}

// offsetOf returns where sub, a subslice of data, starts in it.
func offsetOf(data, sub []byte) int {
	if len(sub) == 0 {
		return -1
	}
	return cap(data) - cap(sub)
}

// SplitLengthPrefixed returns the NAL units of a length-prefixed frame.
func SplitLengthPrefixed(data []byte) [][]byte {
	var nals [][]byte
	for len(data) >= 4 {
		n := int(binary.BigEndian.Uint32(data))
		data = data[4:]
		if n > len(data) {
			break
		}
		nals = append(nals, data[:n])
		data = data[n:]
	}
	return nals
}
