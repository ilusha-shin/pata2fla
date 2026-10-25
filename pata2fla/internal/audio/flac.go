package audio

import (
	"encoding/binary"
	"fmt"
)

// Pure-Go FLAC decoder (all subframe types, all stereo modes, 4..32 bit).
// CRCs are not verified; a damaged frame stops decoding and keeps what was decoded so far.

type bitReader struct {
	d   []byte
	pos int // bit position
}

func (b *bitReader) eof() bool { return b.pos>>3 >= len(b.d) }

func (b *bitReader) bits(n int) uint64 {
	var v uint64
	for n > 0 {
		byteIdx := b.pos >> 3
		if byteIdx >= len(b.d) {
			panic(errFlacEOF)
		}
		avail := 8 - (b.pos & 7)
		take := avail
		if take > n {
			take = n
		}
		cur := uint64(b.d[byteIdx]>>uint(avail-take)) & ((1 << uint(take)) - 1)
		v = v<<uint(take) | cur
		b.pos += take
		n -= take
	}
	return v
}

func (b *bitReader) sbits(n int) int64 {
	if n == 0 {
		return 0
	}
	v := b.bits(n)
	if v>>uint(n-1)&1 == 1 {
		return int64(v) - (int64(1) << uint(n))
	}
	return int64(v)
}

func (b *bitReader) unary() int {
	n := 0
	for b.bits(1) == 0 {
		n++
	}
	return n
}

func (b *bitReader) align() { b.pos = (b.pos + 7) &^ 7 }

type flacError string

func (e flacError) Error() string { return string(e) }

const errFlacEOF = flacError("unexpected end of FLAC data")

var flacBlockSizes = map[int]int{1: 192, 2: 576, 3: 1152, 4: 2304, 5: 4608,
	8: 256, 9: 512, 10: 1024, 11: 2048, 12: 4096, 13: 8192, 14: 16384, 15: 32768}
var flacSampleSizes = map[int]int{1: 8, 2: 12, 4: 16, 5: 20, 6: 24, 7: 32}

func decodeFlac(data []byte) (a *Audio, err error) {
	if len(data) < 8 || string(data[0:4]) != "fLaC" {
		return nil, fmt.Errorf("not a FLAC file")
	}
	pos := 4
	sr, nch, bps := 0, 0, 0
	var total int64
	gotInfo := false
	for {
		if pos+4 > len(data) {
			return nil, fmt.Errorf("FLAC: truncated metadata")
		}
		h := data[pos]
		last := h>>7 == 1
		typ := int(h & 0x7f)
		ln := int(data[pos+1])<<16 | int(data[pos+2])<<8 | int(data[pos+3])
		pos += 4
		if typ == 0 && ln >= 18 && pos+ln <= len(data) {
			br := &bitReader{d: data[pos : pos+ln]}
			br.bits(16)
			br.bits(16)
			br.bits(24)
			br.bits(24)
			sr = int(br.bits(20))
			nch = int(br.bits(3)) + 1
			bps = int(br.bits(5)) + 1
			total = int64(br.bits(36))
			gotInfo = true
		}
		pos += ln
		if last {
			break
		}
	}
	if !gotInfo || sr == 0 {
		return nil, fmt.Errorf("FLAC: STREAMINFO missing")
	}

	out := make([][]int32, nch)
	if total > 0 && total < 1<<31 {
		for c := range out {
			out[c] = make([]int32, 0, int(total))
		}
	}
	br := &bitReader{d: data, pos: pos * 8}

	// A truncated / damaged tail must not lose the decoded part: recover from panics.
	defer func() {
		if r := recover(); r != nil {
			if fe, ok := r.(flacError); ok {
				a, err = buildFlacAudio(out, sr, bps)
				if a == nil || a.Frames() == 0 {
					err = fe
				} else {
					err = nil
				}
				return
			}
			panic(r)
		}
	}()

	for !br.eof() {
		frameStart := br.pos
		if br.bits(14) != 0x3FFE {
			break
		}
		br.bits(2) // reserved + blocking strategy
		bsc := int(br.bits(4))
		src := int(br.bits(4))
		chanAssign := int(br.bits(4))
		ssc := int(br.bits(3))
		br.bits(1)

		first := int(br.bits(8))
		extra := 0
		for extra < 8 && (first>>(7-uint(extra)))&1 == 1 {
			extra++
		}
		if extra > 0 {
			extra--
		}
		for i := 0; i < extra; i++ {
			br.bits(8)
		}

		var blk int
		switch {
		case bsc == 6:
			blk = int(br.bits(8)) + 1
		case bsc == 7:
			blk = int(br.bits(16)) + 1
		default:
			v, ok := flacBlockSizes[bsc]
			if !ok {
				return nil, fmt.Errorf("FLAC: bad block size code %d (frame at bit %d)", bsc, frameStart)
			}
			blk = v
		}
		switch src {
		case 12:
			br.bits(8)
		case 13, 14:
			br.bits(16)
		}
		br.bits(8) // header CRC

		fb := bps
		if ssc != 0 {
			v, ok := flacSampleSizes[ssc]
			if !ok {
				return nil, fmt.Errorf("FLAC: bad sample size code %d", ssc)
			}
			fb = v
		}
		nc := nch
		if chanAssign < 8 {
			nc = chanAssign + 1
		} else {
			nc = 2
		}
		if nc != nch {
			return nil, fmt.Errorf("FLAC: channel count changes inside the stream")
		}

		chans := make([][]int64, nc)
		for c := 0; c < nc; c++ {
			b := fb
			if (chanAssign == 8 && c == 1) || (chanAssign == 9 && c == 0) || (chanAssign == 10 && c == 1) {
				b++
			}
			s, serr := flacSubframe(br, blk, b)
			if serr != nil {
				return nil, serr
			}
			chans[c] = s
		}
		br.align()
		br.bits(16) // frame CRC

		switch chanAssign {
		case 8: // left / side
			for i := 0; i < blk; i++ {
				chans[1][i] = chans[0][i] - chans[1][i]
			}
		case 9: // side / right
			for i := 0; i < blk; i++ {
				chans[0][i] = chans[1][i] + chans[0][i]
			}
		case 10: // mid / side
			for i := 0; i < blk; i++ {
				m := chans[0][i]
				s := chans[1][i]
				m = (m << 1) | (s & 1)
				chans[0][i] = (m + s) >> 1
				chans[1][i] = (m - s) >> 1
			}
		}
		for c := 0; c < nc; c++ {
			for i := 0; i < blk; i++ {
				out[c] = append(out[c], int32(chans[c][i]))
			}
		}
	}
	return buildFlacAudio(out, sr, bps)
}

func buildFlacAudio(out [][]int32, sr, bps int) (*Audio, error) {
	a := &Audio{SR: sr, Ch: make([][]float32, len(out))}
	scale := float32(1.0 / float64(int64(1)<<uint(bps-1)))
	for c := range out {
		a.Ch[c] = make([]float32, len(out[c]))
		for i, v := range out[c] {
			a.Ch[c][i] = float32(v) * scale
		}
	}
	return a, nil
}

func flacSubframe(br *bitReader, blk, bps int) ([]int64, error) {
	br.bits(1)
	t := int(br.bits(6))
	wasted := 0
	if br.bits(1) == 1 {
		wasted = br.unary() + 1
	}
	b := bps - wasted
	if b <= 0 || b > 33 {
		return nil, fmt.Errorf("FLAC: bad sample depth")
	}
	s := make([]int64, blk)
	switch {
	case t == 0:
		v := br.sbits(b)
		for i := range s {
			s[i] = v
		}
	case t == 1:
		for i := range s {
			s[i] = br.sbits(b)
		}
	case t >= 8 && t <= 12:
		order := t - 8
		if order > blk {
			return nil, fmt.Errorf("FLAC: predictor order larger than block")
		}
		for i := 0; i < order; i++ {
			s[i] = br.sbits(b)
		}
		res, err := flacResidual(br, blk, order)
		if err != nil {
			return nil, err
		}
		for i := order; i < blk; i++ {
			r := res[i-order]
			switch order {
			case 0:
				s[i] = r
			case 1:
				s[i] = r + s[i-1]
			case 2:
				s[i] = r + 2*s[i-1] - s[i-2]
			case 3:
				s[i] = r + 3*s[i-1] - 3*s[i-2] + s[i-3]
			default:
				s[i] = r + 4*s[i-1] - 6*s[i-2] + 4*s[i-3] - s[i-4]
			}
		}
	case t >= 32:
		order := (t & 31) + 1
		if order > blk {
			return nil, fmt.Errorf("FLAC: LPC order larger than block")
		}
		for i := 0; i < order; i++ {
			s[i] = br.sbits(b)
		}
		prec := int(br.bits(4)) + 1
		shift := int(br.sbits(5))
		if shift < 0 {
			shift = 0
		}
		co := make([]int64, order)
		for j := range co {
			co[j] = br.sbits(prec)
		}
		res, err := flacResidual(br, blk, order)
		if err != nil {
			return nil, err
		}
		for i := order; i < blk; i++ {
			var acc int64
			for j := 0; j < order; j++ {
				acc += co[j] * s[i-1-j]
			}
			s[i] = res[i-order] + (acc >> uint(shift))
		}
	default:
		return nil, fmt.Errorf("FLAC: bad subframe type %d", t)
	}
	if wasted > 0 {
		for i := range s {
			s[i] <<= uint(wasted)
		}
	}
	return s, nil
}

func flacResidual(br *bitReader, blk, order int) ([]int64, error) {
	method := int(br.bits(2))
	if method > 1 {
		return nil, fmt.Errorf("FLAC: bad residual coding method")
	}
	po := int(br.bits(4))
	pb := 4
	if method == 1 {
		pb = 5
	}
	esc := (1 << uint(pb)) - 1
	res := make([]int64, 0, blk-order)
	parts := 1 << uint(po)
	for p := 0; p < parts; p++ {
		n := (blk >> uint(po))
		if p == 0 {
			n -= order
		}
		if n < 0 {
			return nil, fmt.Errorf("FLAC: bad partition")
		}
		k := int(br.bits(pb))
		if k == esc {
			nb := int(br.bits(5))
			for i := 0; i < n; i++ {
				if nb == 0 {
					res = append(res, 0)
				} else {
					res = append(res, br.sbits(nb))
				}
			}
			continue
		}
		for i := 0; i < n; i++ {
			q := br.unary()
			v := uint64(q) << uint(k)
			if k > 0 {
				v |= br.bits(k)
			}
			res = append(res, int64(v>>1)^-int64(v&1))
		}
	}
	return res, nil
}

var _ = binary.LittleEndian // keep encoding/binary referenced for future header parsing
