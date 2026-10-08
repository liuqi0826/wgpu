//go:build windows && amd64

package native

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

var (
	loadMu   sync.Mutex
	loaded   bool
	loadPath string
)

// Load loads wgpu_native.dll from path. An empty path searches next to the
// working directory, the executable, and WGPU_NATIVE_PATH.
// A later call is a no-op after a successful load.
func Load(path string) error {
	loadMu.Lock()
	defer loadMu.Unlock()
	if loaded {
		return nil
	}
	if path == "" {
		var err error
		path, err = findDLL()
		if err != nil {
			return err
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("wgpu: resolve library path: %w", err)
	}
	handle, err := syscall.LoadLibrary(abs)
	if err != nil {
		return fmt.Errorf("wgpu: load %s: %w", abs, err)
	}
	register(uintptr(handle))
	loaded = true
	loadPath = abs
	return nil
}

// Loaded reports whether Load has succeeded.
func Loaded() bool {
	loadMu.Lock()
	defer loadMu.Unlock()
	return loaded
}

// LibraryPath returns the DLL path passed to LoadLibrary.
func LibraryPath() string {
	loadMu.Lock()
	defer loadMu.Unlock()
	return loadPath
}

func findDLL() (string, error) {
	if env := os.Getenv("WGPU_NATIVE_PATH"); env != "" {
		if fileExists(env) {
			return env, nil
		}
		return "", fmt.Errorf("wgpu: WGPU_NATIVE_PATH=%s does not exist", env)
	}
	names := []string{
		filepath.Join("lib", "wgpu_native.dll"),
		"wgpu_native.dll",
		filepath.Join("..", "lib", "wgpu_native.dll"),
		filepath.Join("..", "..", "lib", "wgpu_native.dll"),
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		names = append(names,
			filepath.Join(dir, "wgpu_native.dll"),
			filepath.Join(dir, "lib", "wgpu_native.dll"),
		)
	}
	for _, name := range names {
		if fileExists(name) {
			return name, nil
		}
	}
	return "", fmt.Errorf("wgpu: wgpu_native.dll not found; set WGPU_NATIVE_PATH or place it in lib/")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
