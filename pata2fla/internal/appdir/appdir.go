// Package appdir locates files that live next to the executable (models, onnxruntime library).
package appdir

import (
	"os"
	"path/filepath"
)

// Dir returns the directory that contains the running executable.
func Dir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if resolved, err2 := filepath.EvalSymlinks(exe); err2 == nil {
		exe = resolved
	}
	return filepath.Dir(exe)
}

// Resolve looks for a helper file next to the executable first, then in the
// current working directory. It returns the absolute path.
func Resolve(name string) (string, bool) {
	candidates := []string{filepath.Join(Dir(), name), name}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			if abs, err2 := filepath.Abs(c); err2 == nil {
				return abs, true
			}
			return c, true
		}
	}
	return "", false
}

// ResolveDir resolves a directory given by the user. Absolute paths are returned
// untouched; relative ones are tried against the working directory and then
// against the executable directory.
func ResolveDir(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		if abs, err2 := filepath.Abs(path); err2 == nil {
			return abs
		}
		return path
	}
	p := filepath.Join(Dir(), path)
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		return p
	}
	return path
}

