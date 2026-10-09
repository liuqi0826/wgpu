//go:build windows && amd64

package wgpu

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/liuqi0826/wgpu/native"
)

// Surface presents to a native window.
type Surface struct {
	ptr  atomic.Uintptr
	inst *Instance
	// dev is the device of the last successful configuration, nil when the
	// surface is not configured. wgpu-native aborts on GetCurrentTexture of
	// an unconfigured surface.
	dev atomic.Pointer[Device]

	// mu serialises configuration, acquisition, presentation and the release
	// of the acquired texture.
	mu sync.Mutex
	// cur is the texture returned by CurrentTexture until it is released.
	cur *surfaceTexture
}

// surfaceTexture is a texture acquired from a surface. wgpu-native discards
// it on release unless it was presented, and aborts the process when that
// fails, so the surface keeps track of it.
type surfaceTexture struct {
	surface   *Surface
	handle    native.Texture
	presented bool // guarded by surface.mu
}

// surfaceTextures maps the handles of acquired, unreleased surface textures
// to their surfaceTexture.
var surfaceTextures sync.Map

// releaseTextureHandle releases a texture handle, going through its surface
// for a surface texture.
func releaseTextureHandle(handle native.Texture) {
	if v, ok := surfaceTextures.LoadAndDelete(handle); ok {
		v.(*surfaceTexture).release()
		return
	}
	native.TextureRelease(handle)
}

func (st *surfaceTexture) release() {
	s := st.surface
	s.mu.Lock()
	defer s.mu.Unlock()
	native.TextureRelease(st.handle)
	if s.cur == st {
		s.cur = nil
	}
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
//
// It returns an error for a nil or released surface or adapter and for an
// adapter of another instance, on which wgpu-native aborts the process.
func (s *Surface) Capabilities(adapter *Adapter) (SurfaceCapabilities, error) {
	const op = "SurfaceGetCapabilities"
	switch {
	case s.Native() == 0:
		return SurfaceCapabilities{}, &Error{Op: op, Status: "surface is nil or released"}
	case adapter.Native() == 0:
		return SurfaceCapabilities{}, &Error{Op: op, Status: "adapter is nil or released"}
	case s.inst != nil && adapter.inst != nil && s.inst != adapter.inst:
		return SurfaceCapabilities{}, &Error{Op: op, Status: "adapter was created from another instance"}
	}
	return s.capabilities(adapter.Native())
}

func (s *Surface) capabilities(adapter native.Adapter) (SurfaceCapabilities, error) {
	var raw native.SurfaceCapabilities
	status := native.SurfaceGetCapabilities(s.Native(), adapter, &raw)
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
// RenderAttachment, a zero PresentMode is Fifo and a zero AlphaMode is Auto,
// which picks Opaque or else Inherit.
type SurfaceConfig struct {
	Format      native.TextureFormat
	Usage       native.TextureUsage
	Width       uint32
	Height      uint32
	PresentMode native.PresentMode
	AlphaMode   native.CompositeAlphaMode
	// ViewFormats may only list Format and its sRGB or non-sRGB variant.
	// The OpenGL backend does not support other view formats than Format.
	ViewFormats []native.TextureFormat
}

// check returns why wgpu-core would reject cfg, or "".
func (cfg *SurfaceConfig) check(caps SurfaceCapabilities, maxSize uint32, backend native.BackendType) string {
	switch {
	case cfg.Format == native.TextureFormatUndefined:
		return "format is Undefined"
	case !slices.Contains(caps.Formats, cfg.Format):
		return fmt.Sprintf("format %v is not supported by the surface, supported: %v", cfg.Format, caps.Formats)
	case cfg.Width == 0 || cfg.Height == 0:
		return fmt.Sprintf("size %dx%d is empty", cfg.Width, cfg.Height)
	case cfg.Width > maxSize || cfg.Height > maxSize:
		return fmt.Sprintf("size %dx%d exceeds MaxTextureDimension2D %d", cfg.Width, cfg.Height, maxSize)
	}
	if usage := orDefault(cfg.Usage, native.TextureUsageRenderAttachment); usage&^caps.Usages != 0 {
		return fmt.Sprintf("usage %v is not supported by the surface, supported: %v", usage, caps.Usages)
	}
	if mode := orDefault(cfg.PresentMode, native.PresentModeFifo); !slices.Contains(caps.PresentModes, mode) {
		return fmt.Sprintf("present mode %v is not supported by the surface, supported: %v", mode, caps.PresentModes)
	}
	switch alpha := cfg.AlphaMode; {
	case alpha == native.CompositeAlphaModeAuto:
		if !slices.Contains(caps.AlphaModes, native.CompositeAlphaModeOpaque) && !slices.Contains(caps.AlphaModes, native.CompositeAlphaModeInherit) {
			return fmt.Sprintf("alpha mode Auto needs Opaque or Inherit, supported: %v", caps.AlphaModes)
		}
	case !slices.Contains(caps.AlphaModes, alpha):
		return fmt.Sprintf("alpha mode %v is not supported by the surface, supported: %v", alpha, caps.AlphaModes)
	}
	for _, f := range cfg.ViewFormats {
		switch {
		case f == cfg.Format:
		case removeSrgb(f) != removeSrgb(cfg.Format):
			return fmt.Sprintf("view format %v is neither %v nor its sRGB variant", f, cfg.Format)
		case backend == native.BackendTypeOpenGL || backend == native.BackendTypeOpenGLES:
			return fmt.Sprintf("view format %v: the OpenGL backend only supports the surface format", f)
		}
	}
	return ""
}

var (
	srgbOnce  sync.Once
	srgbBases map[native.TextureFormat]native.TextureFormat
)

// removeSrgb returns the non-sRGB variant of an sRGB format and f otherwise.
func removeSrgb(f native.TextureFormat) native.TextureFormat {
	srgbOnce.Do(func() {
		byName := make(map[string]native.TextureFormat)
		for v := native.TextureFormat(1); v < 0x100; v++ {
			byName[v.String()] = v
		}
		srgbBases = make(map[native.TextureFormat]native.TextureFormat)
		for name, v := range byName {
			if base, ok := byName[strings.TrimSuffix(name, "Srgb")]; ok && base != v {
				srgbBases[v] = base
			}
		}
	})
	if base, ok := srgbBases[f]; ok {
		return base
	}
	return f
}

// ConfigureWith connects the surface to a device with explicit options.
// Call it again after the window size changes.
//
// wgpu-native aborts the process when configuration fails, so ConfigureWith
// first checks cfg against the surface capabilities of the device's adapter
// and the device limits, and returns an error without configuring when
// wgpu-core would reject it. It also returns an error for a nil or released
// surface or device, a lost device, a device of another instance, and while
// the texture from GetCurrentTexture has not been released.
func (s *Surface) ConfigureWith(device *Device, cfg SurfaceConfig) error {
	const op = "SurfaceConfigure"
	switch {
	case s.Native() == 0:
		return &Error{Op: op, Status: "surface is nil or released"}
	case device.Native() == 0:
		return &Error{Op: op, Status: "device is nil or released"}
	case device.isLost():
		return device.Err()
	case s.inst != nil && device.inst != nil && s.inst != device.inst:
		return &Error{Op: op, Status: "device was created from another instance"}
	}
	caps, err := s.capabilities(native.Adapter(device.adapter.Load()))
	if err != nil {
		return err
	}
	limits, err := device.Limits()
	if err != nil {
		return err
	}
	if status := cfg.check(caps, limits.MaxTextureDimension2D, device.adapterInfo.Backend); status != "" {
		return &Error{Op: op, Status: status}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur != nil {
		return &Error{Op: op, Status: "the texture from GetCurrentTexture has not been released"}
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
	return nil
}

// Configure connects the surface to a device with Fifo presentation and an
// opaque alpha mode. See ConfigureWith.
func (s *Surface) Configure(device *Device, format native.TextureFormat, width, height uint32) error {
	return s.ConfigureWith(device, SurfaceConfig{
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
// The caller must release the texture with TextureRelease after Present.
// See GetCurrentTexture.
//
// It returns status Error without calling wgpu-native when the surface is
// not configured or its device is lost, because wgpu-native aborts then.
func (s *Surface) CurrentTexture() (native.Texture, native.SurfaceGetCurrentTextureStatus) {
	if dev := s.dev.Load(); dev == nil || dev.isLost() || dev.Native() == 0 {
		return 0, native.SurfaceGetCurrentTextureStatusError
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var tex native.SurfaceTexture
	native.SurfaceGetCurrentTexture(s.Native(), &tex)
	if tex.Texture != 0 {
		st := &surfaceTexture{surface: s, handle: tex.Texture}
		s.cur = st
		surfaceTextures.Store(tex.Texture, st)
	}
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
	s.mu.Lock()
	defer s.mu.Unlock()
	status := native.SurfacePresent(s.Native())
	if status != native.StatusSuccess {
		return &Error{Op: "SurfacePresent", Status: status.String()}
	}
	if s.cur != nil {
		s.cur.presented = true
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
		releaseTextureHandle(texture)
	}
}
