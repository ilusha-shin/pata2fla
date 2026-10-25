// Package ortx is a thin float32 wrapper around the cgo-free onnxruntime binding
// github.com/cnxxy-cn/onnxruntime_purego. The onnxruntime shared library (onnxruntime.dll /
// libonnxruntime.so) is loaded at run time from the program folder; no C compiler is needed.
package ortx

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	ort "github.com/cnxxy-cn/onnxruntime_purego"
)

// DefaultIntraOpThreads — ORT IntraOpNumThreads for every session (stem + ADTOF + CNN).
// 4 is a sweet spot on ~6–12 core machines; set 2 for quieter CPU, 0 via sessionOpts for ORT default.
const DefaultIntraOpThreads int32 = 4

var engine *ort.Engine

// LibName is the file name of the onnxruntime shared library for this OS.
func LibName() string {
	switch runtime.GOOS {
	case "windows":
		return "onnxruntime.dll"
	case "darwin":
		return "libonnxruntime.dylib"
	default:
		return "libonnxruntime.so"
	}
}

// Init loads the shared library found in dir.
func Init(dir string) error {
	if engine != nil {
		return nil
	}
	lib := filepath.Join(dir, LibName())
	if _, err := os.Stat(lib); err != nil {
		return fmt.Errorf("%s not found next to the program (%s). Run setup.bat / setup.sh", LibName(), dir)
	}
	e, err := ort.NewEngine(lib)
	if err != nil {
		return fmt.Errorf("cannot load %s: %v\n(it must be onnxruntime 1.24.x - re-run setup)", lib, err)
	}
	engine = e
	return nil
}

// Shutdown releases the onnxruntime engine.
func Shutdown() {
	if engine != nil {
		engine.Destroy()
		engine = nil
	}
}

type extraInput struct {
	name string
	dims []int64
}

// Model is an ONNX model with one main float32 input and optional extra float32 inputs
// (e.g. physics vector for mel_phys models).
type Model struct {
	Path    string
	InName  string
	InDims  []int64 // -1 = dynamic
	OutName string
	OutDims []int64
	extras  []extraInput
	sess    *ort.Session
}

// sessionOpts builds ORT SessionOptions with IntraOpNumThreads.
// threads <= 0 → nil options (ORT default thread count).
func sessionOpts(threads int32) (*ort.SessionOptions, error) {
	if threads <= 0 {
		return nil, nil
	}
	opts, err := engine.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("session options: %w", err)
	}
	if err := opts.SetIntraOpNumThreads(threads); err != nil {
		opts.Destroy()
		return nil, fmt.Errorf("SetIntraOpNumThreads(%d): %w", threads, err)
	}
	return opts, nil
}

// Load opens the model with DefaultIntraOpThreads (all models: stem, ADTOF, CNN).
func Load(path string) (*Model, error) {
	if engine == nil {
		return nil, fmt.Errorf("onnxruntime is not initialized")
	}
	ins, outs, err := ReadGraphIO(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read the structure of %s: %v", filepath.Base(path), err)
	}
	opts, err := sessionOpts(DefaultIntraOpThreads)
	if err != nil {
		return nil, err
	}
	if opts != nil {
		defer opts.Destroy()
	}
	sess, err := engine.NewSession(path, opts)
	if err != nil {
		return nil, fmt.Errorf("cannot create session for %s: %v", filepath.Base(path), err)
	}
	if len(sess.InputNames) == 0 || len(sess.OutputNames) == 0 {
		sess.Destroy()
		return nil, fmt.Errorf("%s has no inputs or outputs", filepath.Base(path))
	}
	m := &Model{Path: path, sess: sess, InName: sess.InputNames[0], OutName: sess.OutputNames[0]}
	dimsOf := func(list []ValueInfo, name string) []int64 {
		for _, v := range list {
			if v.Name == name {
				return v.Dims
			}
		}
		return nil
	}
	m.InDims = dimsOf(ins, m.InName)
	m.OutDims = dimsOf(outs, m.OutName)
	for _, name := range sess.InputNames[1:] {
		m.extras = append(m.extras, extraInput{name: name, dims: dimsOf(ins, name)})
	}
	return m, nil
}

// ExtraInputNames returns names of non-primary inputs (e.g. input_phys).
func (m *Model) ExtraInputNames() []string {
	out := make([]string, len(m.extras))
	for i, e := range m.extras {
		out[i] = e.name
	}
	return out
}

// HasExtras reports whether the model expects additional inputs beyond mel.
func (m *Model) HasExtras() bool {
	return len(m.extras) > 0
}

// Close frees the session.
func (m *Model) Close() {
	if m.sess != nil {
		m.sess.Destroy()
		m.sess = nil
	}
}

// Run executes the model on the main float32 tensor.
// extraData is optional: map input_name -> float32 buffer for secondary inputs
// (physics). Missing extras are zero-filled (legacy behaviour).
func (m *Model) Run(shape []int64, data []float32, extraData ...map[string][]float32) ([]float32, []int64, error) {
	var extras map[string][]float32
	if len(extraData) > 0 {
		extras = extraData[0]
	}

	in, err := ort.NewTensor(shape, data)
	if err != nil {
		return nil, nil, err
	}
	defer in.Destroy()
	inputs := map[string]*ort.Value{m.InName: in}

	for _, e := range m.extras {
		dims := make([]int64, len(e.dims))
		total := int64(1)
		for i, d := range e.dims {
			if d <= 0 {
				// dynamic: prefer batch from main shape, else 1
				if i == 0 && len(shape) > 0 && shape[0] > 0 {
					d = shape[0]
				} else {
					d = 1
				}
			}
			dims[i] = d
			total *= d
		}
		buf := make([]float32, total)
		if extras != nil {
			if src, ok := extras[e.name]; ok && len(src) > 0 {
				copy(buf, src)
			}
		}
		t, terr := ort.NewTensor(dims, buf)
		if terr != nil {
			return nil, nil, terr
		}
		defer t.Destroy()
		inputs[e.name] = t
	}

	outputs, err := m.sess.Run(inputs)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		for _, v := range outputs {
			if v != nil {
				v.Destroy()
			}
		}
	}()
	ov := outputs[m.OutName]
	if ov == nil {
		for _, v := range outputs {
			ov = v
			break
		}
	}
	if ov == nil {
		return nil, nil, fmt.Errorf("%s returned no output", filepath.Base(m.Path))
	}
	raw, err := ort.GetTensorData[float32](ov)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: output is not a float32 tensor: %v", filepath.Base(m.Path), err)
	}
	res := append([]float32(nil), raw...)
	oshape, err := ov.GetShape()
	if err != nil {
		return nil, nil, err
	}
	return res, oshape, nil
}
