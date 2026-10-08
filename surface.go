//go:build windows && amd64

package wgpu

import (
	"sync/atomic"
	"unsafe"

	"github.com/liuqi0826/wgpu/native"
)

// Surface presents to a native window.
type Surface struct {
	ptr atomic.Uintptr
	// dev is the device of the last successful configuration, nil when the
	// surface is not configured. wgpu-native aborts on GetCurrentTexture of
	// an unconfigured surface.
	dev atomic.Pointer[Device]
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

// SurfaceConfig describes Surface.ConfigureWith. A zero Usage is
// RenderAttachment, a zero PresentMode is Fifo and a zero AlphaMode is Auto.
type SurfaceConfig struct {
	Format      native.TextureFormat
	Usage       native.TextureUsage
	Width       uint32
	Height      uint32
	PresentMode native.PresentMode
	AlphaMode   native.CompositeAlphaMode
	ViewFormats []native.TextureFormat
}

// ConfigureWith connects the surface to a device with explicit options.
// Call it again after the window size changes.
//
// It does nothing on a lost device, because wgpu-native aborts the process
// when configuration fails.
func (s *Surface) ConfigureWith(device *Device, cfg SurfaceConfig) {
	if device.isLost() {
		return
	}
	var m cmem
	raw := cnew(&m, native.SurfaceConfiguration{
		Device:      device.Native(),
		Format:      cfg.Format,
		Usage:       orDefault(cfg.Usage, native.TextureUsageRenderAttachment),
		Width:       cfg.Width,
		Height:      cfg.Height,
		AlphaMode:   cfg.AlphaMode,
		PresentMode: orDefault(cfg.PresentMode, native.PresentModeFifo),
	})
	if n := len(cfg.ViewFormats); n > 0 {
		formats := cslice[native.TextureFormat](&m, n)
		copy(formats, cfg.ViewFormats)
		raw.ViewFormatCount = uintptr(n)
		raw.ViewFormats = sliceAddr(formats)
	}
	native.SurfaceConfigure(s.Native(), raw)
	m.done()
	s.dev.Store(device)
}

// Configure connects the surface to a device with Fifo presentation and an
// opaque alpha mode.
func (s *Surface) Configure(device *Device, format native.TextureFormat, width, height uint32) {
	s.ConfigureWith(device, SurfaceConfig{
		Format:      format,
		Width:       width,
		Height:      height,
		PresentMode: native.PresentModeFifo,
		AlphaMode:   native.CompositeAlphaModeOpaque,
	})
}

// Unconfigure detaches the surface from its device.
func (s *Surface) Unconfigure() {
	s.dev.Store(nil)
	native.SurfaceUnconfigure(s.Native())
}

// CurrentTexture acquires the next swapchain texture as a raw handle.
// The caller must Release the texture after Present. See GetCurrentTexture.
//
// It returns status Error without calling wgpu-native when the surface is
// not configured or its device is lost, because wgpu-native aborts then.
func (s *Surface) CurrentTexture() (native.Texture, native.SurfaceGetCurrentTextureStatus) {
	if dev := s.dev.Load(); dev == nil || dev.isLost() || dev.Native() == 0 {
		return 0, native.SurfaceGetCurrentTextureStatusError
	}
	var tex native.SurfaceTexture
	native.SurfaceGetCurrentTexture(s.Native(), &tex)
	return tex.Texture, tex.Status
}

// GetCurrentTexture acquires the next swapchain texture. The texture is nil
// unless a texture was returned; on Outdated or Lost reconfigure the surface.
// Release the texture after Present.
func (s *Surface) GetCurrentTexture() (*Texture, native.SurfaceGetCurrentTextureStatus) {
	handle, status := s.CurrentTexture()
	if handle == 0 {
		return nil, status
	}
	return newTexture(handle), status
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
