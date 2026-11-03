package media

import (
	"encoding/binary"
	"errors"
)

// unescape removes emulation prevention bytes (00 00 03 → 00 00).
func unescape(nal []byte) []byte {
	out := make([]byte, 0, len(nal))
	zeros := 0
	for _, b := range nal {
		if zeros >= 2 && b == 3 {
			zeros = 0
			continue
		}
		if b == 0 {
			zeros++
		} else {
			zeros = 0
		}
		out = append(out, b)
	}
	return out
}

type bitReader struct {
	b   []byte
	pos int // in bits
	err error
}

func (r *bitReader) bit() uint32 {
	if r.pos >= len(r.b)*8 {
		r.err = errors.New("media: truncated parameter set")
		return 0
	}
	v := uint32(r.b[r.pos/8]>>(7-uint(r.pos%8))) & 1
	r.pos++
	return v
}

func (r *bitReader) bits(n int) uint32 {
	var v uint32
	for i := 0; i < n; i++ {
		v = v<<1 | r.bit()
	}
	return v
}

func (r *bitReader) skip(n int) { r.pos += n }

func (r *bitReader) ue() uint32 {
	zeros := 0
	for r.bit() == 0 {
		zeros++
		if zeros > 31 || r.err != nil {
			r.err = errors.New("media: bad exp-Golomb code")
			return 0
		}
	}
	if zeros == 0 {
		return 0
	}
	return (1<<zeros - 1) + r.bits(zeros)
}

func (r *bitReader) se() int32 {
	v := r.ue()
	if v&1 == 1 {
		return int32((v + 1) / 2)
	}
	return -int32(v / 2)
}

// SPSInfo is what a sequence parameter set says of the pictures.
type SPSInfo struct {
	Width, Height int
	// H.264: profile_idc, constraint flags and level_idc.
	Profile, Compat, Level byte
	// H.265: the general profile_tier_level fields, as hvcC holds them.
	HEVC struct {
		ProfileSpace, Tier, ProfileIDC byte
		CompatFlags                    uint32
		ConstraintFlags                [6]byte
		LevelIDC                       byte
		ChromaFormat                   byte
		BitDepthLuma, BitDepthChroma   byte
		TemporalIDNested               byte
		NumTemporalLayers              byte
	}
}

// ParseSPS reads the size of the pictures and the profile out of an SPS
// NAL unit (with its header).
func ParseSPS(c Codec, nal []byte) (SPSInfo, error) {
	if c == H265 {
		return parseH265SPS(nal)
	}
	return parseH264SPS(nal)
}

func parseH264SPS(nal []byte) (SPSInfo, error) {
	var info SPSInfo
	if len(nal) < 4 {
		return info, errors.New("media: SPS too short")
	}
	b := unescape(nal[1:])
	info.Profile, info.Compat, info.Level = b[0], b[1], b[2]
	r := &bitReader{b: b, pos: 24}
	r.ue() // seq_parameter_set_id
	chroma := uint32(1)
	switch info.Profile {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135:
		chroma = r.ue()
		if chroma == 3 {
			r.skip(1) // separate_colour_plane_flag
		}
		r.ue()            // bit_depth_luma_minus8
		r.ue()            // bit_depth_chroma_minus8
		r.skip(1)         // qpprime_y_zero_transform_bypass_flag
		if r.bit() == 1 { // seq_scaling_matrix_present_flag
			n := 8
			if chroma == 3 {
				n = 12
			}
			for i := 0; i < n; i++ {
				if r.bit() == 1 {
					size := 16
					if i >= 6 {
						size = 64
					}
					last, next := int32(8), int32(8)
					for j := 0; j < size; j++ {
						if next != 0 {
							next = (last + r.se() + 256) % 256
						}
						if next != 0 {
							last = next
						}
					}
				}
			}
		}
	}
	r.ue()          // log2_max_frame_num_minus4
	switch r.ue() { // pic_order_cnt_type
	case 0:
		r.ue()
	case 1:
		r.skip(1)
		r.se()
		r.se()
		n := r.ue()
		for i := uint32(0); i < n && r.err == nil; i++ {
			r.se()
		}
	}
	r.ue()    // max_num_ref_frames
	r.skip(1) // gaps_in_frame_num_value_allowed_flag
	wMbs := r.ue() + 1
	hMapUnits := r.ue() + 1
	frameMbsOnly := r.bit()
	if frameMbsOnly == 0 {
		r.skip(1) // mb_adaptive_frame_field_flag
	}
	r.skip(1) // direct_8x8_inference_flag
	w := int(wMbs) * 16
	h := int(2-frameMbsOnly) * int(hMapUnits) * 16
	if r.bit() == 1 { // frame_cropping_flag
		l, rr, t, bt := r.ue(), r.ue(), r.ue(), r.ue()
		cx, cy := 1, 2-int(frameMbsOnly)
		switch chroma {
		case 1:
			cx, cy = 2, 2*(2-int(frameMbsOnly))
		case 2:
			cx, cy = 2, 2-int(frameMbsOnly)
		}
		w -= cx * int(l+rr)
		h -= cy * int(t+bt)
	}
	if r.err != nil {
		return info, r.err
	}
	info.Width, info.Height = w, h
	return info, nil
}

func parseH265SPS(nal []byte) (SPSInfo, error) {
	var info SPSInfo
	if len(nal) < 15 {
		return info, errors.New("media: SPS too short")
	}
	b := unescape(nal[2:])
	r := &bitReader{b: b}
	r.skip(4) // sps_video_parameter_set_id
	maxSubLayers := r.bits(3)
	nested := r.bit()
	info.HEVC.TemporalIDNested = byte(nested)
	info.HEVC.NumTemporalLayers = byte(maxSubLayers + 1)
	// profile_tier_level(1, max_sub_layers_minus1)
	info.HEVC.ProfileSpace = byte(r.bits(2))
	info.HEVC.Tier = byte(r.bit())
	info.HEVC.ProfileIDC = byte(r.bits(5))
	info.HEVC.CompatFlags = r.bits(32)
	for i := range info.HEVC.ConstraintFlags {
		info.HEVC.ConstraintFlags[i] = byte(r.bits(8))
	}
	info.HEVC.LevelIDC = byte(r.bits(8))
	subProfile := make([]bool, maxSubLayers)
	subLevel := make([]bool, maxSubLayers)
	for i := range subProfile {
		subProfile[i] = r.bit() == 1
		subLevel[i] = r.bit() == 1
	}
	if maxSubLayers > 0 {
		for i := maxSubLayers; i < 8; i++ {
			r.skip(2)
		}
	}
	for i := range subProfile {
		if subProfile[i] {
			r.skip(88)
		}
		if subLevel[i] {
			r.skip(8)
		}
	}
	r.ue() // sps_seq_parameter_set_id
	chroma := r.ue()
	if chroma == 3 {
		r.skip(1)
	}
	w := int(r.ue())
	h := int(r.ue())
	if r.bit() == 1 { // conformance_window_flag
		l, rr, t, bt := r.ue(), r.ue(), r.ue(), r.ue()
		cx, cy := 1, 1
		switch chroma {
		case 1:
			cx, cy = 2, 2
		case 2:
			cx, cy = 2, 1
		}
		w -= cx * int(l+rr)
		h -= cy * int(t+bt)
	}
	info.HEVC.ChromaFormat = byte(chroma)
	info.HEVC.BitDepthLuma = byte(r.ue() + 8)
	info.HEVC.BitDepthChroma = byte(r.ue() + 8)
	if r.err != nil {
		return info, r.err
	}
	info.Width, info.Height = w, h
	return info, nil
}

// AVCC builds an avcC box payload (AVCDecoderConfigurationRecord).
func AVCC(p *ParameterSets) ([]byte, error) {
	if len(p.SPS) == 0 || len(p.PPS) == 0 || len(p.SPS[0]) < 4 {
		return nil, errors.New("media: missing SPS or PPS")
	}
	sps := p.SPS[0]
	out := []byte{1, sps[1], sps[2], sps[3], 0xff, 0xe0 | byte(len(p.SPS))}
	for _, s := range p.SPS {
		out = binary.BigEndian.AppendUint16(out, uint16(len(s)))
		out = append(out, s...)
	}
	out = append(out, byte(len(p.PPS)))
	for _, s := range p.PPS {
		out = binary.BigEndian.AppendUint16(out, uint16(len(s)))
		out = append(out, s...)
	}
	switch sps[1] {
	case 100, 110, 122, 144:
		// High profiles carry their chroma format and bit depths.
		out = append(out, 0xfc|1, 0xf8, 0xf8, 0)
	}
	return out, nil
}

// HVCC builds an hvcC box payload (HEVCDecoderConfigurationRecord).
func HVCC(p *ParameterSets) ([]byte, error) {
	if len(p.VPS) == 0 || len(p.SPS) == 0 || len(p.PPS) == 0 {
		return nil, errors.New("media: missing VPS, SPS or PPS")
	}
	info, err := ParseSPS(H265, p.SPS[0])
	if err != nil {
		return nil, err
	}
	h := info.HEVC
	out := []byte{1, h.ProfileSpace<<6 | h.Tier<<5 | h.ProfileIDC}
	out = binary.BigEndian.AppendUint32(out, h.CompatFlags)
	out = append(out, h.ConstraintFlags[:]...)
	out = append(out, h.LevelIDC)
	out = append(out, 0xf0, 0x00) // min_spatial_segmentation_idc
	out = append(out, 0xfc)       // parallelismType
	out = append(out, 0xfc|h.ChromaFormat)
	out = append(out, 0xf8|(h.BitDepthLuma-8))
	out = append(out, 0xf8|(h.BitDepthChroma-8))
	out = append(out, 0, 0) // avgFrameRate
	out = append(out, (h.NumTemporalLayers&7)<<3|(h.TemporalIDNested&1)<<2|3)
	arrays := []struct {
		t    byte
		nals [][]byte
	}{{h265VPS, p.VPS}, {h265SPS, p.SPS}, {h265PPS, p.PPS}}
	out = append(out, byte(len(arrays)))
	for _, a := range arrays {
		out = append(out, 0x80|a.t) // array_completeness
		out = binary.BigEndian.AppendUint16(out, uint16(len(a.nals)))
		for _, n := range a.nals {
			out = binary.BigEndian.AppendUint16(out, uint16(len(n)))
			out = append(out, n...)
		}
	}
	return out, nil
}
