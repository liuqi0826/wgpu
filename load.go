//go:build windows && amd64

package wgpu

import "github.com/liuqi0826/wgpu/native"

// Load loads wgpu_native.dll. An empty path uses the default search.
func Load(path string) error {
	if err := native.Load(path); err != nil {
		return err
	}
	ensureCallbacks()
	native.SetLogLevel(native.LogLevelError)
	return nil
}

// Version returns wgpuGetVersion. Some builds export this symbol as a stub
// that returns zero; a null instance from CreateInstance is the failure signal.
func Version() (uint32, error) {
	if err := Load(""); err != nil {
		return 0, err
	}
	return native.GetVersion(), nil
}
