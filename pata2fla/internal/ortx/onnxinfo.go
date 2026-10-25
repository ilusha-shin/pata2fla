package ortx

import (
	"fmt"
	"io"
	"os"
)

// Reading input/output shapes straight from the .onnx file (protobuf), because the
// onnxruntime binding does not expose them. Only the small ValueInfo messages are read;
// the weights (initializers) are skipped with Seek, so a 300 MB model costs a few KB of I/O.

// ValueInfo is a graph input or output.
type ValueInfo struct {
	Name string
	Dims []int64 // -1 = dynamic / unknown
}

type pbField struct {
	num  int
	wt   int
	v    uint64
	data []byte
}

type memReader struct {
	b []byte
	p int
}

func (m *memReader) varint() (uint64, error) {
	var v uint64
	var sh uint
	for {
		if m.p >= len(m.b) {
			return 0, io.ErrUnexpectedEOF
		}
		c := m.b[m.p]
		m.p++
		v |= uint64(c&0x7f) << sh
		sh += 7
		if c&0x80 == 0 {
			return v, nil
		}
		if sh > 63 {
			return 0, fmt.Errorf("bad varint")
		}
	}
}

func parseMessage(b []byte) ([]pbField, error) {
	m := &memReader{b: b}
	var out []pbField
	for m.p < len(b) {
		t, err := m.varint()
		if err != nil {
			return nil, err
		}
		f := pbField{num: int(t >> 3), wt: int(t & 7)}
		switch f.wt {
		case 0:
			if f.v, err = m.varint(); err != nil {
				return nil, err
			}
		case 2:
			n, err := m.varint()
			if err != nil {
				return nil, err
			}
			if m.p+int(n) > len(b) {
				return nil, io.ErrUnexpectedEOF
			}
			f.data = b[m.p : m.p+int(n)]
			m.p += int(n)
		case 1:
			m.p += 8
		case 5:
			m.p += 4
		default:
			return nil, fmt.Errorf("unsupported wire type %d", f.wt)
		}
		out = append(out, f)
	}
	return out, nil
}

func parseValueInfo(b []byte) (ValueInfo, error) {
	var vi ValueInfo
	fields, err := parseMessage(b)
	if err != nil {
		return vi, err
	}
	for _, f := range fields {
		switch f.num {
		case 1:
			vi.Name = string(f.data)
		case 2: // TypeProto
			tp, err := parseMessage(f.data)
			if err != nil {
				return vi, err
			}
			for _, t := range tp {
				if t.num != 1 { // tensor_type
					continue
				}
				tt, err := parseMessage(t.data)
				if err != nil {
					return vi, err
				}
				for _, s := range tt {
					if s.num != 2 { // shape
						continue
					}
					sh, err := parseMessage(s.data)
					if err != nil {
						return vi, err
					}
					for _, d := range sh {
						if d.num != 1 { // dim
							continue
						}
						dim := int64(-1)
						df, err := parseMessage(d.data)
						if err != nil {
							return vi, err
						}
						for _, x := range df {
							if x.num == 1 && x.wt == 0 {
								dim = int64(x.v)
							}
						}
						vi.Dims = append(vi.Dims, dim)
					}
				}
			}
		}
	}
	return vi, nil
}

type fileReader struct{ f *os.File }

func (r *fileReader) varint() (uint64, error) {
	var v uint64
	var sh uint
	var buf [1]byte
	for {
		if _, err := io.ReadFull(r.f, buf[:]); err != nil {
			return 0, err
		}
		v |= uint64(buf[0]&0x7f) << sh
		sh += 7
		if buf[0]&0x80 == 0 {
			return v, nil
		}
		if sh > 63 {
			return 0, fmt.Errorf("bad varint")
		}
	}
}

func (r *fileReader) pos() int64 {
	p, _ := r.f.Seek(0, io.SeekCurrent)
	return p
}

func (r *fileReader) skip(n int64) error {
	_, err := r.f.Seek(n, io.SeekCurrent)
	return err
}

func (r *fileReader) skipValue(wt int) error {
	switch wt {
	case 0:
		_, err := r.varint()
		return err
	case 1:
		return r.skip(8)
	case 5:
		return r.skip(4)
	}
	return fmt.Errorf("unsupported wire type %d", wt)
}

// ReadGraphIO returns the real inputs (initializers excluded) and the outputs of the model.
func ReadGraphIO(path string) (inputs, outputs []ValueInfo, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	size := st.Size()
	r := &fileReader{f: f}
	inits := map[string]bool{}
	var allInputs []ValueInfo

	for r.pos() < size {
		t, err := r.varint()
		if err != nil {
			return nil, nil, err
		}
		field, wt := int(t>>3), int(t&7)
		if wt != 2 {
			if err := r.skipValue(wt); err != nil {
				return nil, nil, err
			}
			continue
		}
		n, err := r.varint()
		if err != nil {
			return nil, nil, err
		}
		if field != 7 { // not the graph
			if err := r.skip(int64(n)); err != nil {
				return nil, nil, err
			}
			continue
		}
		gEnd := r.pos() + int64(n)
		for r.pos() < gEnd {
			gt, err := r.varint()
			if err != nil {
				return nil, nil, err
			}
			gf, gw := int(gt>>3), int(gt&7)
			if gw != 2 {
				if err := r.skipValue(gw); err != nil {
					return nil, nil, err
				}
				continue
			}
			m, err := r.varint()
			if err != nil {
				return nil, nil, err
			}
			switch gf {
			case 11, 12: // graph input / output
				buf := make([]byte, m)
				if _, err := io.ReadFull(f, buf); err != nil {
					return nil, nil, err
				}
				vi, err := parseValueInfo(buf)
				if err != nil {
					return nil, nil, err
				}
				if gf == 11 {
					allInputs = append(allInputs, vi)
				} else {
					outputs = append(outputs, vi)
				}
			case 5: // initializer: only its name is needed
				iEnd := r.pos() + int64(m)
				for r.pos() < iEnd {
					it, err := r.varint()
					if err != nil {
						return nil, nil, err
					}
					if int(it&7) != 2 {
						if err := r.skipValue(int(it & 7)); err != nil {
							return nil, nil, err
						}
						continue
					}
					k, err := r.varint()
					if err != nil {
						return nil, nil, err
					}
					if int(it>>3) == 8 {
						nb := make([]byte, k)
						if _, err := io.ReadFull(f, nb); err != nil {
							return nil, nil, err
						}
						inits[string(nb)] = true
						if err := r.skip(iEnd - r.pos()); err != nil {
							return nil, nil, err
						}
						break
					}
					if err := r.skip(int64(k)); err != nil {
						return nil, nil, err
					}
				}
				if r.pos() < iEnd {
					_ = r.skip(iEnd - r.pos())
				}
			default:
				if err := r.skip(int64(m)); err != nil {
					return nil, nil, err
				}
			}
		}
	}
	for _, vi := range allInputs {
		if !inits[vi.Name] {
			inputs = append(inputs, vi)
		}
	}
	return inputs, outputs, nil
}
