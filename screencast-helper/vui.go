package main

import "errors"

// The VA-API H.264 encoder writes no colour description, so receivers have to
// guess the matrix, and some guess BT.601 for the BT.709 frames vapostproc
// produces. withBT709 rewrites an SPS NAL unit to state BT.709, limited range.

type bitReader struct {
	b   []byte
	pos int
}

func (r *bitReader) bit() (uint, error) {
	if r.pos >= len(r.b)*8 {
		return 0, errors.New("sps: out of bits")
	}
	v := uint(r.b[r.pos/8]>>(7-r.pos%8)) & 1
	r.pos++
	return v, nil
}

func (r *bitReader) bits(n int) (uint, error) {
	var v uint
	for range n {
		b, err := r.bit()
		if err != nil {
			return 0, err
		}
		v = v<<1 | b
	}
	return v, nil
}

func (r *bitReader) ue() (uint, error) {
	zeros := 0
	for {
		b, err := r.bit()
		if err != nil {
			return 0, err
		}
		if b == 1 {
			break
		}
		zeros++
		if zeros > 31 {
			return 0, errors.New("sps: bad exp-golomb")
		}
	}
	rest, err := r.bits(zeros)
	return 1<<zeros - 1 + rest, err
}

type bitWriter struct {
	b []byte
	n int
}

func (w *bitWriter) put(v uint, n int) {
	for i := n - 1; i >= 0; i-- {
		if w.n%8 == 0 {
			w.b = append(w.b, 0)
		}
		w.b[len(w.b)-1] |= byte(v>>i&1) << (7 - w.n%8)
		w.n++
	}
}

// copyBits copies bits [from, to) of src.
func (w *bitWriter) copyBits(src []byte, from, to int) {
	for i := from; i < to; i++ {
		w.put(uint(src[i/8]>>(7-i%8))&1, 1)
	}
}

func unescape(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if i+2 < len(b) && b[i] == 0 && b[i+1] == 0 && b[i+2] == 3 {
			out = append(out, 0, 0)
			i += 2
			continue
		}
		out = append(out, b[i])
	}
	return out
}

func escape(b []byte) []byte {
	out := make([]byte, 0, len(b)+8)
	zeros := 0
	for _, c := range b {
		if zeros >= 2 && c <= 3 {
			out = append(out, 3)
			zeros = 0
		}
		out = append(out, c)
		if c == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return out
}

// withBT709 takes an SPS NAL unit (header byte included, no start code) and
// returns it with video_signal_type set to BT.709 primaries, transfer and
// matrix in limited range. Everything else is copied bit for bit.
func withBT709(nal []byte) ([]byte, error) {
	rbsp := unescape(nal[1:])
	r := &bitReader{b: rbsp}
	skip := func(n int) error { _, err := r.bits(n); return err }
	ue := func() error { _, err := r.ue(); return err }

	profile, err := r.bits(8)
	if err != nil {
		return nil, err
	}
	if err := skip(16); err != nil { // constraint flags, level
		return nil, err
	}
	if err := ue(); err != nil { // seq_parameter_set_id
		return nil, err
	}
	switch profile {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135:
		return nil, errors.New("sps: high profiles not handled")
	}
	if err := ue(); err != nil { // log2_max_frame_num_minus4
		return nil, err
	}
	pocType, err := r.ue()
	if err != nil {
		return nil, err
	}
	switch pocType {
	case 0:
		err = ue()
	case 1:
		return nil, errors.New("sps: pic_order_cnt_type 1 not handled")
	}
	if err != nil {
		return nil, err
	}
	if err := ue(); err != nil { // max_num_ref_frames
		return nil, err
	}
	if err := skip(1); err != nil { // gaps_in_frame_num_allowed
		return nil, err
	}
	if err := ue(); err != nil { // pic_width_in_mbs_minus1
		return nil, err
	}
	if err := ue(); err != nil { // pic_height_in_map_units_minus1
		return nil, err
	}
	frameMbsOnly, err := r.bit()
	if err != nil {
		return nil, err
	}
	if frameMbsOnly == 0 {
		if err := skip(1); err != nil {
			return nil, err
		}
	}
	if err := skip(1); err != nil { // direct_8x8_inference
		return nil, err
	}
	cropping, err := r.bit()
	if err != nil {
		return nil, err
	}
	if cropping == 1 {
		for range 4 {
			if err := ue(); err != nil {
				return nil, err
			}
		}
	}

	w := &bitWriter{}
	vuiFlag := r.pos
	vui, err := r.bit()
	if err != nil {
		return nil, err
	}
	stop := lastSetBit(rbsp)
	if stop < 0 {
		return nil, errors.New("sps: no stop bit")
	}

	if vui == 0 {
		w.copyBits(rbsp, 0, vuiFlag)
		w.put(1, 1) // vui_parameters_present
		w.put(0, 2) // aspect_ratio_info, overscan_info
		writeSignalType(w)
		w.put(0, 6) // chroma_loc, timing, nal_hrd, vcl_hrd, pic_struct, bitstream_restriction
	} else {
		aspect, err := r.bit()
		if err != nil {
			return nil, err
		}
		if aspect == 1 {
			idc, err := r.bits(8)
			if err != nil {
				return nil, err
			}
			if idc == 255 {
				if err := skip(32); err != nil {
					return nil, err
				}
			}
		}
		overscan, err := r.bit()
		if err != nil {
			return nil, err
		}
		if overscan == 1 {
			if err := skip(1); err != nil {
				return nil, err
			}
		}
		w.copyBits(rbsp, 0, r.pos)
		present, err := r.bit()
		if err != nil {
			return nil, err
		}
		if present == 1 {
			if err := skip(4); err != nil { // video_format, full_range
				return nil, err
			}
			desc, err := r.bit()
			if err != nil {
				return nil, err
			}
			if desc == 1 {
				if err := skip(24); err != nil {
					return nil, err
				}
			}
		}
		writeSignalType(w)
		w.copyBits(rbsp, r.pos, stop)
	}
	w.put(1, 1) // rbsp_stop_one_bit
	for w.n%8 != 0 {
		w.put(0, 1)
	}
	return append([]byte{nal[0]}, escape(w.b)...), nil
}

func writeSignalType(w *bitWriter) {
	w.put(1, 1) // video_signal_type_present
	w.put(5, 3) // video_format: unspecified
	w.put(0, 1) // full range: no
	w.put(1, 1) // colour_description_present
	w.put(1, 8) // primaries: BT.709
	w.put(1, 8) // transfer: BT.709
	w.put(1, 8) // matrix: BT.709
}

func lastSetBit(b []byte) int {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] != 0 {
			for bit := 0; bit < 8; bit++ {
				if b[i]>>bit&1 == 1 {
					return i*8 + 7 - bit
				}
			}
		}
	}
	return -1
}

// fixColour rewrites the SPS inside an H.264 access unit; on any parse
// problem the unit is returned unchanged.
func fixColour(au []byte) []byte {
	for i := 0; ; {
		j := indexStartCode(au, i)
		if j < 0 || j+3 >= len(au) {
			return au
		}
		start := j + 3
		if au[start]&0x1f != 7 {
			i = start
			continue
		}
		end := indexStartCode(au, start)
		if end < 0 {
			end = len(au)
		} else if end > start && au[end-1] == 0 {
			end-- // leading zero of a 4-byte start code belongs to the next unit
		}
		sps, err := withBT709(au[start:end])
		if err != nil {
			return au
		}
		out := append(append(append([]byte{}, au[:start]...), sps...), au[end:]...)
		return out
	}
}

func indexStartCode(b []byte, from int) int {
	for i := from; i+2 < len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			return i
		}
	}
	return -1
}
