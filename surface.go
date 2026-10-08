//go:build windows && amd64

package wgpu

import (
	"runtime"
	"sync/atomic"
	"unsafe"

	"wgpu/native"
)

// Surface presents to a native window.
type Surface struct {
	ptr atomic.Uintptr
}

// Native returns the raw surface handle.
func (s *Surface) Native() native.Surface {
	if s == nil {
		return 0
	}
	return native.Surface(s.ptr.Load())
}

// Release drops the surface.
func (s *Surface) Release() {
	handle := native.Surface(s.ptr.Swap(0))
	if handle != 0 {
		native.SurfaceRelease(handle)
	}
}

// Capabilities lists formats and present modes supported by adapter.
func (s *Surface) Capabilities(adapter *Adapter) (SurfaceCapabilities, error) {
	var raw native.SurfaceCapabilities
	status := native.SurfaceGetCapabilities(s.Native(), adapter.Native(), &raw)
	if status != native.StatusSuccess {
		return SurfaceCapabilities{}, &Error{Op: "SurfaceGetCapabilities", Status: status.String()}
	}
	caps := SurfaceCapabilities{
		Usages:       raw.Usages,
		Formats:      readSlice[native.TextureFormat](raw.Formats, raw.FormatCount),
		PresentModes: readSlice[native.PresentMode](raw.PresentModes, raw.PresentModeCount),
		AlphaModes:   readSlice[native.CompositeAlphaMode](raw.AlphaModes, raw.AlphaModeCount),
	}
	native.SurfaceCapabilitiesFreeMembers(raw)
	return caps, nil
}

// SurfaceCapabilities is a Go copy of wgpuSurfaceGetCapabilities.
type SurfaceCapabilities struct {
	Usages       native.TextureUsage
	Formats      []native.TextureFormat
	PresentModes []native.PresentMode
	AlphaModes   []native.CompositeAlphaMode
}

// Configure connects the surface to a device.
func (s *Surface) Configure(device *Device, format native.TextureFormat, width, height uint32) {
	config := native.SurfaceConfiguration{
		Device:      device.Native(),
		Format:      format,
		Usage:       native.TextureUsageRenderAttachment,
		Width:       width,
		Height:      height,
		AlphaMode:   native.CompositeAlphaModeOpaque,
		PresentMode: native.PresentModeFifo,
	}
	native.SurfaceConfigure(s.Native(), &config)
	runtime.KeepAlive(config)
}

// CurrentTexture acquires the next swapchain texture.
// The caller must Release the texture after Present.
func (s *Surface) CurrentTexture() (native.Texture, native.SurfaceGetCurrentTextureStatus) {
	var tex native.SurfaceTexture
	native.SurfaceGetCurrentTexture(s.Native(), &tex)
	return tex.Texture, tex.Status
}

// Present presents the current texture.
func (s *Surface) Present() error {
	status := native.SurfacePresent(s.Native())
	if status != native.StatusSuccess {
		return &Error{Op: "SurfacePresent", Status: status.String()}
	}
	return nil
}

func readSlice[T any](ptr uintptr, count uintptr) []T {
	if ptr == 0 || count == 0 {
		return nil
	}
	src := unsafe.Slice((*T)(cPointer(ptr)), count)
	out := make([]T, count)
	copy(out, src)
	return out
}

// TextureRelease releases a texture returned by CurrentTexture.
func TextureRelease(texture native.Texture) {
	if texture != 0 {
		native.TextureRelease(texture)
	}
}
