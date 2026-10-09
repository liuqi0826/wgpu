//go:build windows && amd64

package wgpu

import (
	"slices"
	"testing"

	"github.com/liuqi0826/wgpu/native"
)

func (f *surfaceFixture) config() SurfaceConfig {
	return SurfaceConfig{Format: f.caps.Formats[0], Width: 64, Height: 64}
}

func (f *surfaceFixture) backend() native.BackendType { return f.device.adapterInfo.Backend }

// frame acquires, presents and releases one texture.
func (f *surfaceFixture) frame(t *testing.T) {
	t.Helper()
	tex, status := f.surface.GetCurrentTexture()
	if tex == nil {
		t.Fatalf("GetCurrentTexture: %v", status)
	}
	if err := f.surface.Present(); err != nil {
		t.Fatal(err)
	}
	tex.Release()
}

// otherDevice returns a device of another instance.
func otherDevice(t *testing.T) (*Adapter, *Device) {
	t.Helper()
	inst := testInstance(t)
	t.Cleanup(inst.Release)
	adapter, err := inst.RequestAdapter(&native.RequestAdapterOptions{PowerPreference: native.PowerPreferenceHighPerformance})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adapter.Release)
	device, err := adapter.RequestDevice()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(device.Release)
	return adapter, device
}

// Each case ends with a configuration that wgpu-native would abort on.
var surfaceConfigCases = map[string]func(t *testing.T){
	"unsupported format": func(t *testing.T) {
		f := newSurfaceFixture(t)
		cfg := f.config()
		cfg.Format = native.TextureFormatR8Unorm
		if slices.Contains(f.caps.Formats, cfg.Format) {
			t.Skip("surface supports R8Unorm")
		}
		wantErr(t, f.surface.ConfigureWith(f.device, cfg), "format R8Unorm is not supported by the surface")
	},
	"undefined format": func(t *testing.T) {
		f := newSurfaceFixture(t)
		cfg := f.config()
		cfg.Format = native.TextureFormatUndefined
		wantErr(t, f.surface.ConfigureWith(f.device, cfg), "format is Undefined")
	},
	"zero size": func(t *testing.T) {
		f := newSurfaceFixture(t)
		cfg := f.config()
		cfg.Width = 0
		wantErr(t, f.surface.ConfigureWith(f.device, cfg), "size 0x64 is empty")
		cfg.Width, cfg.Height = 64, 0
		wantErr(t, f.surface.ConfigureWith(f.device, cfg), "size 64x0 is empty")
	},
	"too large": func(t *testing.T) {
		f := newSurfaceFixture(t)
		limits, err := f.device.Limits()
		if err != nil {
			t.Fatal(err)
		}
		cfg := f.config()
		cfg.Width = limits.MaxTextureDimension2D + 1
		wantErr(t, f.surface.ConfigureWith(f.device, cfg), "exceeds MaxTextureDimension2D")
	},
	"unsupported present mode": func(t *testing.T) {
		f := newSurfaceFixture(t)
		cfg := f.config()
		cfg.PresentMode = 0x99
		wantErr(t, f.surface.ConfigureWith(f.device, cfg), "present mode")
		for _, mode := range []native.PresentMode{native.PresentModeFifoRelaxed, native.PresentModeImmediate, native.PresentModeMailbox} {
			if !slices.Contains(f.caps.PresentModes, mode) {
				cfg.PresentMode = mode
				wantErr(t, f.surface.ConfigureWith(f.device, cfg), "present mode "+mode.String()+" is not supported")
				return
			}
		}
		t.Log("surface supports every present mode")
	},
	"unsupported alpha mode": func(t *testing.T) {
		f := newSurfaceFixture(t)
		cfg := f.config()
		cfg.AlphaMode = 0x99
		wantErr(t, f.surface.ConfigureWith(f.device, cfg), "alpha mode")
		for _, mode := range []native.CompositeAlphaMode{native.CompositeAlphaModeOpaque, native.CompositeAlphaModePremultiplied,
			native.CompositeAlphaModeUnpremultiplied, native.CompositeAlphaModeInherit} {
			if !slices.Contains(f.caps.AlphaModes, mode) {
				cfg.AlphaMode = mode
				wantErr(t, f.surface.ConfigureWith(f.device, cfg), "alpha mode "+mode.String()+" is not supported")
				return
			}
		}
		t.Log("surface supports every alpha mode")
	},
	"unsupported usage": func(t *testing.T) {
		f := newSurfaceFixture(t)
		cfg := f.config()
		for _, usage := range []native.TextureUsage{native.TextureUsageCopySrc, native.TextureUsageCopyDst,
			native.TextureUsageTextureBinding, native.TextureUsageStorageBinding, 1 << 40} {
			if f.caps.Usages&usage == 0 {
				cfg.Usage = native.TextureUsageRenderAttachment | usage
				wantErr(t, f.surface.ConfigureWith(f.device, cfg), "is not supported by the surface")
				return
			}
		}
		t.Fatalf("surface supports usage %v", f.caps.Usages)
	},
	"incompatible view format": func(t *testing.T) {
		f := newSurfaceFixture(t)
		cfg := f.config()
		cfg.ViewFormats = []native.TextureFormat{native.TextureFormatR8Unorm}
		wantErr(t, f.surface.ConfigureWith(f.device, cfg), "view format R8Unorm is neither")
		cfg.ViewFormats = []native.TextureFormat{0x9999}
		wantErr(t, f.surface.ConfigureWith(f.device, cfg), "is neither")
	},
	"srgb view format": func(t *testing.T) {
		f := newSurfaceFixture(t)
		cfg := f.config()
		var view native.TextureFormat
		for _, format := range f.caps.Formats {
			if removeSrgb(format) == format {
				for v := native.TextureFormat(1); v < 0x100; v++ {
					if v != format && removeSrgb(v) == format {
						cfg.Format, view = format, v
					}
				}
			}
			if view != 0 {
				break
			}
		}
		if view == 0 {
			t.Skipf("no surface format with an sRGB variant in %v", f.caps.Formats)
		}
		cfg.ViewFormats = []native.TextureFormat{cfg.Format, view}
		err := f.surface.ConfigureWith(f.device, cfg)
		if b := f.backend(); b == native.BackendTypeOpenGL || b == native.BackendTypeOpenGLES {
			wantErr(t, err, "the OpenGL backend only supports the surface format")
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		f.frame(t)
	},
	"released device": func(t *testing.T) {
		f := newSurfaceFixture(t)
		f.device.Release()
		wantErr(t, f.surface.ConfigureWith(f.device, f.config()), "device is nil or released")
		wantErr(t, f.surface.ConfigureWith(nil, f.config()), "device is nil or released")
	},
	"lost device": func(t *testing.T) {
		f := newSurfaceFixture(t)
		f.device.Destroy()
		wantErr(t, f.surface.ConfigureWith(f.device, f.config()), "DeviceLost")
	},
	"released surface": func(t *testing.T) {
		f := newSurfaceFixture(t)
		f.surface.Release()
		wantErr(t, f.surface.ConfigureWith(f.device, f.config()), "surface is nil or released")
		_, err := f.surface.Capabilities(f.adapter)
		wantErr(t, err, "surface is nil or released")
	},
	"texture not released": func(t *testing.T) {
		f := newSurfaceFixture(t)
		if err := f.surface.ConfigureWith(f.device, f.config()); err != nil {
			t.Fatal(err)
		}
		tex, status := f.surface.GetCurrentTexture()
		if tex == nil {
			t.Fatalf("GetCurrentTexture: %v", status)
		}
		wantErr(t, f.surface.ConfigureWith(f.device, f.config()), "has not been released")
		if err := f.surface.Present(); err != nil {
			t.Fatal(err)
		}
		// Configure resets the presented flag, so releasing a presented
		// texture after it would discard it and abort as well.
		wantErr(t, f.surface.ConfigureWith(f.device, f.config()), "has not been released")
		tex.Release()
		if err := f.surface.ConfigureWith(f.device, f.config()); err != nil {
			t.Fatal(err)
		}
		f.frame(t)
	},
	"raw texture not released": func(t *testing.T) {
		f := newSurfaceFixture(t)
		if err := f.surface.ConfigureWith(f.device, f.config()); err != nil {
			t.Fatal(err)
		}
		tex, status := f.surface.CurrentTexture()
		if tex == 0 {
			t.Fatalf("CurrentTexture: %v", status)
		}
		wantErr(t, f.surface.ConfigureWith(f.device, f.config()), "has not been released")
		TextureRelease(tex)
		if err := f.surface.ConfigureWith(f.device, f.config()); err != nil {
			t.Fatal(err)
		}
		f.frame(t)
	},
	"device of another instance": func(t *testing.T) {
		f := newSurfaceFixture(t)
		_, device := otherDevice(t)
		wantErr(t, f.surface.ConfigureWith(device, f.config()), "device was created from another instance")
	},
	"adapter of another instance": func(t *testing.T) {
		f := newSurfaceFixture(t)
		adapter, _ := otherDevice(t)
		_, err := f.surface.Capabilities(adapter)
		wantErr(t, err, "adapter was created from another instance")
		adapter.Release()
		_, err = f.surface.Capabilities(adapter)
		wantErr(t, err, "adapter is nil or released")
	},
	"valid configuration": func(t *testing.T) {
		f := newSurfaceFixture(t)
		t.Logf("caps: usages %v, formats %v, present modes %v, alpha modes %v", f.caps.Usages, f.caps.Formats, f.caps.PresentModes, f.caps.AlphaModes)
		cfg := f.config()
		cfg.ViewFormats = []native.TextureFormat{cfg.Format}
		if err := f.surface.ConfigureWith(f.device, cfg); err != nil {
			t.Fatal(err)
		}
		f.frame(t)
		for _, mode := range f.caps.PresentModes {
			cfg.PresentMode = mode
			cfg.Width, cfg.Height = 32, 48
			if err := f.surface.ConfigureWith(f.device, cfg); err != nil {
				t.Fatalf("present mode %v: %v", mode, err)
			}
			f.frame(t)
		}
		err := f.surface.Configure(f.device, f.caps.Formats[0], 16, 16)
		if slices.Contains(f.caps.AlphaModes, native.CompositeAlphaModeOpaque) {
			if err != nil {
				t.Fatal(err)
			}
			f.frame(t)
		} else {
			wantErr(t, err, "alpha mode Opaque")
		}
		if err := f.device.Err(); err != nil {
			t.Fatal(err)
		}
	},
}

var surfaceConfigCaseNames = registerIsolated("surface-config/", surfaceConfigCases)

// TestSurfaceConfigChecks runs each case in a child process: without the
// Go-side checks in Surface.ConfigureWith, wgpu-native aborts the process.
func TestSurfaceConfigChecks(t *testing.T) {
	runIsolated(t, "surface-config")
}

func init() {
	isolatedCases["surface-config"] = func(t *testing.T) {
		for _, name := range surfaceConfigCaseNames {
			t.Run(name, isolatedCases[name])
		}
	}
}
