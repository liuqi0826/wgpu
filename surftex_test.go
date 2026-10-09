//go:build windows && amd64

package wgpu

import (
	"testing"

	"github.com/liuqi0826/wgpu/native"
)

// acquired configures the fixture surface and acquires a texture.
func (f *surfaceFixture) acquired(t *testing.T) *Texture {
	t.Helper()
	if err := f.surface.ConfigureWith(f.device, f.config()); err != nil {
		t.Fatal(err)
	}
	tex, status := f.surface.GetCurrentTexture()
	if tex == nil {
		t.Fatalf("GetCurrentTexture: %v", status)
	}
	return tex
}

// clearCmd records a render pass that clears tex.
func (f *surfaceFixture) clearCmd(t *testing.T, tex *Texture) *CommandBuffer {
	t.Helper()
	view, err := tex.CreateView(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Release()
	enc, err := f.device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	pass, err := enc.BeginRenderPass(&RenderPassDescriptor{ColorAttachments: []RenderPassColorAttachment{{View: view}}})
	if err != nil {
		t.Fatal(err)
	}
	pass.End()
	pass.Release()
	cb, err := enc.Finish()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cb.Release)
	return cb
}

func wantAcquireError(t *testing.T, s *Surface) {
	t.Helper()
	if tex, status := s.GetCurrentTexture(); tex != nil || status != native.SurfaceGetCurrentTextureStatusError {
		t.Fatalf("GetCurrentTexture: %v %v", tex, status)
	}
	if tex, status := s.CurrentTexture(); tex != 0 || status != native.SurfaceGetCurrentTextureStatusError {
		t.Fatalf("CurrentTexture: %v %v", tex, status)
	}
}

// Each case would abort the process in wgpu-native without the Go-side
// tracking of the acquired surface texture.
var surfaceTextureCases = map[string]func(t *testing.T){
	"acquire twice": func(t *testing.T) {
		f := newSurfaceFixture(t)
		tex := f.acquired(t)
		wantAcquireError(t, f.surface)
		if err := f.surface.Present(); err != nil {
			t.Fatal(err)
		}
		// Releasing the previous texture after the next acquisition would
		// discard the new one, so the next acquisition waits for Release.
		wantAcquireError(t, f.surface)
		tex.Release()
		f.frame(t)
	},
	"surface released while acquired": func(t *testing.T) {
		f := newSurfaceFixture(t)
		tex := f.acquired(t)
		f.surface.Release()
		wantErr(t, f.surface.Present(), "surface is nil or released")
		wantAcquireError(t, f.surface)
		tex.Release()
		f.surface.Release()
	},
	"surface released after present": func(t *testing.T) {
		f := newSurfaceFixture(t)
		tex := f.acquired(t)
		if err := f.surface.Present(); err != nil {
			t.Fatal(err)
		}
		f.surface.Release()
		tex.Release()
	},
	"unconfigure after present": func(t *testing.T) {
		f := newSurfaceFixture(t)
		tex := f.acquired(t)
		if err := f.surface.Present(); err != nil {
			t.Fatal(err)
		}
		f.surface.Unconfigure()
		wantAcquireError(t, f.surface)
		tex.Release()
		wantAcquireError(t, f.surface)
		if err := f.surface.ConfigureWith(f.device, f.config()); err != nil {
			t.Fatal(err)
		}
		f.frame(t)
	},
	"unconfigure before present": func(t *testing.T) {
		f := newSurfaceFixture(t)
		tex := f.acquired(t)
		f.surface.Unconfigure()
		tex.Release()
		if err := f.surface.ConfigureWith(f.device, f.config()); err != nil {
			t.Fatal(err)
		}
		f.frame(t)
	},
	"device released while acquired": func(t *testing.T) {
		f := newSurfaceFixture(t)
		tex := f.acquired(t)
		f.device.Release()
		if tex.Native() != 0 {
			t.Fatal("texture handle still set after Device.Release")
		}
		tex.Release()
		wantAcquireError(t, f.surface)
	},
	"device destroyed while acquired": func(t *testing.T) {
		f := newSurfaceFixture(t)
		tex := f.acquired(t)
		f.device.Destroy()
		tex.Release()
		wantAcquireError(t, f.surface)
	},
	"raw texture and device released": func(t *testing.T) {
		f := newSurfaceFixture(t)
		if err := f.surface.ConfigureWith(f.device, f.config()); err != nil {
			t.Fatal(err)
		}
		tex, status := f.surface.CurrentTexture()
		if tex == 0 {
			t.Fatalf("CurrentTexture: %v", status)
		}
		f.device.Release()
		TextureRelease(tex)
	},
	"released surface": func(t *testing.T) {
		f := newSurfaceFixture(t)
		if err := f.surface.ConfigureWith(f.device, f.config()); err != nil {
			t.Fatal(err)
		}
		f.frame(t)
		f.surface.Release()
		wantErr(t, f.surface.Present(), "surface is nil or released")
		wantAcquireError(t, f.surface)
		f.surface.Unconfigure()
		var nilSurface *Surface
		wantErr(t, nilSurface.Present(), "surface is nil or released")
		wantAcquireError(t, nilSurface)
		nilSurface.Unconfigure()
		nilSurface.Release()
	},
	"destroy surface texture": func(t *testing.T) {
		// wgpu-core drops the swapchain image, and Present or Release abort.
		f := newSurfaceFixture(t)
		tex := f.acquired(t)
		tex.Destroy()
		if err := f.surface.Present(); err != nil {
			t.Fatal(err)
		}
		tex.Release()
		tex = f.acquired(t)
		tex.Destroy()
		tex.Release()
		f.frame(t)
	},
	"submit after present": func(t *testing.T) {
		f := newSurfaceFixture(t)
		tex := f.acquired(t)
		cb := f.clearCmd(t, tex)
		if err := f.surface.Present(); err != nil {
			t.Fatal(err)
		}
		wantErr(t, f.device.Queue().Submit(cb), "uses a surface texture that was already presented or released")
		tex.Release()
	},
	"submit after release": func(t *testing.T) {
		f := newSurfaceFixture(t)
		tex := f.acquired(t)
		cb := f.clearCmd(t, tex)
		tex.Release()
		wantErr(t, f.device.Queue().Submit(cb), "uses a surface texture that was already presented or released")
	},
	"valid frame": func(t *testing.T) {
		f := newSurfaceFixture(t)
		for i := 0; i < 3; i++ {
			tex := f.acquired(t)
			if err := f.device.Queue().Submit(f.clearCmd(t, tex)); err != nil {
				t.Fatal(err)
			}
			if err := f.surface.Present(); err != nil {
				t.Fatal(err)
			}
			tex.Release()
		}
		if err := f.device.Err(); err != nil {
			t.Fatal(err)
		}
	},
}

var surfaceTextureCaseNames = registerIsolated("surface-texture/", surfaceTextureCases)

// TestSurfaceTextureChecks runs each case in a child process: without the
// tracking of the acquired texture, wgpu-native aborts the process.
func TestSurfaceTextureChecks(t *testing.T) {
	runIsolated(t, "surface-texture")
}

func init() {
	isolatedCases["surface-texture"] = func(t *testing.T) {
		for _, name := range surfaceTextureCaseNames {
			t.Run(name, isolatedCases[name])
		}
	}
}
