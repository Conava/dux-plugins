package main

import (
	"bytes"
	"io"
)

var startCode = []byte{0, 0, 1}

// nalCodec tells H.264 and HEVC NAL headers apart: the unit type sits in
// different bits, and so do the delimiter and key frame types.
type nalCodec struct{ hevc bool }

func (c nalCodec) unitType(b byte) int {
	if c.hevc {
		return int(b>>1) & 0x3f
	}
	return int(b) & 0x1f
}

func (c nalCodec) isAUD(t int) bool {
	if c.hevc {
		return t == 35
	}
	return t == 9
}

func (c nalCodec) isKey(t int) bool {
	if c.hevc {
		return t >= 16 && t <= 23 // IRAP pictures
	}
	return t == 5
}

// readAccessUnits splits an Annex B stream into access units at its access
// unit delimiters (the encoder must emit them) and reports whether each one
// holds a key frame.
func readAccessUnits(r io.Reader, c nalCodec, emit func(au []byte, key bool)) error {
	var buf []byte
	chunk := make([]byte, 256<<10)
	for {
		n, err := r.Read(chunk)
		buf = append(buf, chunk[:n]...)
		for {
			// The first AUD starts the current unit; the next one ends it.
			first := c.nextAUD(buf, 0)
			if first < 0 {
				break
			}
			end := c.nextAUD(buf, first+4)
			if end < 0 {
				buf = buf[first:]
				break
			}
			au := buf[first:end]
			emit(bytes.Clone(au), c.hasKey(au))
			buf = buf[end:]
		}
		if err != nil {
			return err
		}
	}
}

// nextAUD returns the offset of the next AUD NAL unit's start code at or after
// from, including a leading zero byte of a 4-byte start code.
func (c nalCodec) nextAUD(b []byte, from int) int {
	for i := from; i+3 < len(b); {
		j := bytes.Index(b[i:], startCode)
		if j < 0 || i+j+3 >= len(b) {
			return -1
		}
		pos := i + j
		if c.isAUD(c.unitType(b[pos+3])) {
			if pos > 0 && b[pos-1] == 0 {
				return pos - 1
			}
			return pos
		}
		i = pos + 3
	}
	return -1
}

func (c nalCodec) hasKey(au []byte) bool {
	for i := 0; ; {
		j := bytes.Index(au[i:], startCode)
		if j < 0 || i+j+3 >= len(au) {
			return false
		}
		if c.isKey(c.unitType(au[i+j+3])) {
			return true
		}
		i += j + 3
	}
}
