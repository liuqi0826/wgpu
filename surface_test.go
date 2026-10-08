//go:build windows && amd64

package wgpu

import (
	"testing"

	"github.com/liuqi0826/wgpu/native"
)

// The surface handles here are zero, so any call that reached wgpu-native
// would abort the process ("invalid surface").
func TestSurfaceTextureGuards(t *testing.T) {
	var s Surface
	if tex, status := s.GetCurrentTexture(); tex != nil || status != native.SurfaceGetCurrentTextureStatusError {
		t.Fatalf("unconfigured surface: %v %v", tex, status)
	}
	adapter := newTestAdapter(t)
	device, err := adapter.RequestDevice()
	if err != nil {
		t.Fatal(err)
	}
	defer device.Release()
	s.dev.Store(device)
	device.Destroy()
	if tex, status := s.GetCurrentTexture(); tex != nil || status != native.SurfaceGetCurrentTextureStatusError {
		t.Fatalf("lost device: %v %v", tex, status)
	}
	s.ConfigureWith(device, SurfaceConfig{Format: native.TextureFormatBGRA8Unorm, Width: 4, Height: 4})
}
