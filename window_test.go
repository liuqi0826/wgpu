//go:build windows && amd64

package wgpu

import (
	"testing"

	"github.com/liuqi0826/wgpu/native"
)

// surfaceForWindow creates a surface, an adapter and a device for hwnd and
// configures the surface.
func surfaceForWindow(t *testing.T, hwnd, hinstance uintptr) (*Surface, *Device, SurfaceConfig) {
	t.Helper()
	inst := testInstance(t)
	t.Cleanup(inst.Release)
	s, err := inst.CreateSurfaceHWND(hwnd, hinstance)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Release)
	adapter, err := inst.RequestAdapter(&native.RequestAdapterOptions{CompatibleSurface: s.Native()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adapter.Release)
	device, err := adapter.RequestDevice()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(device.Release)
	caps, err := s.Capabilities(adapter)
	if err != nil {
		t.Fatal(err)
	}
	cfg := SurfaceConfig{Format: caps.Formats[0], Width: 64, Height: 64}
	if err := s.ConfigureWith(device, cfg); err != nil {
		t.Fatal(err)
	}
	return s, device, cfg
}

func createSurfaceErr(t *testing.T, hwnd uintptr) error {
	t.Helper()
	inst := testInstance(t)
	t.Cleanup(inst.Release)
	s, err := inst.CreateSurfaceHWND(hwnd, testHInstance())
	if s != nil {
		s.Release()
	}
	return err
}

// Each case would abort the process in wgpu-native without the Go-side checks.
var surfaceWindowCases = map[string]func(t *testing.T){
	"zero hinstance": func(t *testing.T) {
		// The Vulkan backend aborts on a zero hinstance.
		s, _, _ := surfaceForWindow(t, newTestWindow(t, 64, 64), 0)
		tex, status := s.GetCurrentTexture()
		if tex == nil {
			t.Fatalf("GetCurrentTexture: %v", status)
		}
		if err := s.Present(); err != nil {
			t.Fatal(err)
		}
		tex.Release()
	},
	"zero hwnd": func(t *testing.T) {
		// Creation succeeds; configuration aborts on D3D12 and GL.
		wantErr(t, createSurfaceErr(t, 0), "hwnd 0x0 is not a window")
	},
	"invalid hwnd": func(t *testing.T) {
		wantErr(t, createSurfaceErr(t, 0x12345678), "hwnd 0x12345678 is not a window")
	},
	"destroyed hwnd": func(t *testing.T) {
		hwnd := newTestWindow(t, 64, 64)
		testDestroyWindow.Call(hwnd)
		wantErr(t, createSurfaceErr(t, hwnd), "is not a window")
	},
	"released instance": func(t *testing.T) {
		inst := testInstance(t)
		inst.Release()
		_, err := inst.CreateSurfaceHWND(newTestWindow(t, 64, 64), testHInstance())
		wantErr(t, err, "instance is nil or released")
		_, err = (*Instance)(nil).CreateSurfaceHWND(newTestWindow(t, 64, 64), testHInstance())
		wantErr(t, err, "instance is nil or released")
	},
	"window destroyed after configure": func(t *testing.T) {
		// Reconfiguring aborts on D3D12 and GL; on GL presenting fails and
		// releasing the unpresented texture then aborts.
		f := newSurfaceFixture(t)
		cfg := f.config()
		if err := f.surface.ConfigureWith(f.device, cfg); err != nil {
			t.Fatal(err)
		}
		f.frame(t)
		tex, status := f.surface.GetCurrentTexture()
		if tex == nil {
			t.Fatalf("GetCurrentTexture: %v", status)
		}
		testDestroyWindow.Call(f.hwnd)
		wantErr(t, f.surface.Present(), "the window of the surface was destroyed")
		tex.Release()
		cfg.Width = 32
		wantErr(t, f.surface.ConfigureWith(f.device, cfg), "the window of the surface was destroyed")
	},
}

var surfaceWindowCaseNames = registerIsolated("surface-window/", surfaceWindowCases)

// TestSurfaceWindowChecks runs each case in a child process: without the
// window checks, wgpu-native aborts the process.
func TestSurfaceWindowChecks(t *testing.T) {
	runIsolated(t, "surface-window")
}

func init() {
	isolatedCases["surface-window"] = func(t *testing.T) {
		for _, name := range surfaceWindowCaseNames {
			t.Run(name, isolatedCases[name])
		}
	}
}
