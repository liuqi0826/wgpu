//go:build windows && amd64

package wgpu

import (
	"sync/atomic"

	"github.com/liuqi0826/wgpu/native"
)

// TextureDescriptor describes Device.CreateTexture. Zero Dimension,
// DepthOrArrayLayers, MipLevelCount and SampleCount mean 2D, 1, 1 and 1.
type TextureDescriptor struct {
	Label              string
	Usage              native.TextureUsage
	Dimension          native.TextureDimension
	Width              uint32
	Height             uint32
	DepthOrArrayLayers uint32
	Format             native.TextureFormat
	MipLevelCount      uint32
	SampleCount        uint32
	ViewFormats        []native.TextureFormat
}

// Texture is a WGPUTexture.
type Texture struct {
	ptr       atomic.Uintptr
	destroyed atomic.Bool // see Queue.Submit
}

func newTexture(handle native.Texture) *Texture {
	tex := &Texture{}
	tex.ptr.Store(uintptr(handle))
	return tex
}

// CreateTexture creates a texture.
func (d *Device) CreateTexture(desc *TextureDescriptor) (*Texture, error) {
	if desc == nil {
		return nil, &Error{Op: "CreateTexture", Status: "nil descriptor"}
	}
	var m cmem
	raw := cnew(&m, native.TextureDescriptor{
		Label:     m.str(desc.Label),
		Usage:     desc.Usage,
		Dimension: orDefault(desc.Dimension, native.TextureDimension2D),
		Size: native.Extent3D{
			Width:              desc.Width,
			Height:             desc.Height,
			DepthOrArrayLayers: orDefault(desc.DepthOrArrayLayers, 1),
		},
		Format:        desc.Format,
		MipLevelCount: orDefault(desc.MipLevelCount, 1),
		SampleCount:   orDefault(desc.SampleCount, 1),
	})
	if n := len(desc.ViewFormats); n > 0 {
		formats := cslice[native.TextureFormat](&m, n)
		copy(formats, desc.ViewFormats)
		raw.ViewFormatCount = uintptr(n)
		raw.ViewFormats = sliceAddr(formats)
	}
	handle := native.DeviceCreateTexture(d.Native(), raw)
	m.done()
	if handle == 0 {
		return nil, d.fail("CreateTexture")
	}
	return newTexture(handle), nil
}

// Native returns the raw handle.
func (t *Texture) Native() native.Texture {
	if t == nil {
		return 0
	}
	return native.Texture(t.ptr.Load())
}

// Width returns the texture width in texels.
func (t *Texture) Width() uint32 { return native.TextureGetWidth(t.Native()) }

// Height returns the texture height in texels.
func (t *Texture) Height() uint32 { return native.TextureGetHeight(t.Native()) }

// DepthOrArrayLayers returns the depth or layer count.
func (t *Texture) DepthOrArrayLayers() uint32 {
	return native.TextureGetDepthOrArrayLayers(t.Native())
}

// Format returns the texel format.
func (t *Texture) Format() native.TextureFormat { return native.TextureGetFormat(t.Native()) }

// Usage returns the usage flags.
func (t *Texture) Usage() native.TextureUsage { return native.TextureGetUsage(t.Native()) }

// Dimension returns the texture dimension.
func (t *Texture) Dimension() native.TextureDimension {
	return native.TextureGetDimension(t.Native())
}

// MipLevelCount returns the number of mip levels.
func (t *Texture) MipLevelCount() uint32 { return native.TextureGetMipLevelCount(t.Native()) }

// SampleCount returns the sample count.
func (t *Texture) SampleCount() uint32 { return native.TextureGetSampleCount(t.Native()) }

// Destroy frees the GPU memory now. Work already submitted still completes.
// The handle stays valid until Release. Queue.Submit rejects command buffers
// that use a destroyed texture.
func (t *Texture) Destroy() {
	if handle := t.Native(); handle != 0 {
		t.destroyed.Store(true)
		native.TextureDestroy(handle)
	}
}

// Release drops the texture reference. For a surface texture, call it after Present.
func (t *Texture) Release() {
	handle := native.Texture(t.ptr.Swap(0))
	if handle != 0 {
		releaseTextureHandle(handle)
	}
}

// TextureViewDescriptor describes Texture.CreateView. Zero MipLevelCount and
// ArrayLayerCount cover the remaining levels and layers. A zero Aspect is All.
// Zero Format and Dimension follow the texture.
type TextureViewDescriptor struct {
	Label           string
	Format          native.TextureFormat
	Dimension       native.TextureViewDimension
	BaseMipLevel    uint32
	MipLevelCount   uint32
	BaseArrayLayer  uint32
	ArrayLayerCount uint32
	Aspect          native.TextureAspect
	Usage           native.TextureUsage
}

// CreateView creates a view. A nil descriptor views the whole texture.
func (t *Texture) CreateView(desc *TextureViewDescriptor) (*TextureView, error) {
	var m cmem
	var raw *native.TextureViewDescriptor
	if desc != nil {
		raw = cnew(&m, native.TextureViewDescriptor{
			Label:           m.str(desc.Label),
			Format:          desc.Format,
			Dimension:       desc.Dimension,
			BaseMipLevel:    desc.BaseMipLevel,
			MipLevelCount:   orDefault(desc.MipLevelCount, native.MipLevelCountUndefined),
			BaseArrayLayer:  desc.BaseArrayLayer,
			ArrayLayerCount: orDefault(desc.ArrayLayerCount, native.ArrayLayerCountUndefined),
			Aspect:          orDefault(desc.Aspect, native.TextureAspectAll),
			Usage:           desc.Usage,
		})
	}
	handle := native.TextureCreateView(t.Native(), raw)
	m.done()
	if handle == 0 {
		return nil, &Error{Op: "TextureCreateView", Status: "null view"}
	}
	view := &TextureView{tex: t}
	view.ptr.Store(uintptr(handle))
	return view, nil
}

// TextureView is a WGPUTextureView.
type TextureView struct {
	ptr atomic.Uintptr
	tex *Texture // the viewed texture, see Queue.Submit
}

// Native returns the raw handle.
func (v *TextureView) Native() native.TextureView {
	if v == nil {
		return 0
	}
	return native.TextureView(v.ptr.Load())
}

// Release drops the view.
func (v *TextureView) Release() {
	handle := native.TextureView(v.ptr.Swap(0))
	if handle != 0 {
		native.TextureViewRelease(handle)
	}
}

// SamplerDescriptor describes Device.CreateSampler. Zero address modes are
// ClampToEdge, zero filters are Nearest, a zero LodMaxClamp is 32 and a zero
// MaxAnisotropy is 1. A zero Compare is a non-comparison sampler.
type SamplerDescriptor struct {
	Label         string
	AddressModeU  native.AddressMode
	AddressModeV  native.AddressMode
	AddressModeW  native.AddressMode
	MagFilter     native.FilterMode
	MinFilter     native.FilterMode
	MipmapFilter  native.MipmapFilterMode
	LodMinClamp   float32
	LodMaxClamp   float32
	Compare       native.CompareFunction
	MaxAnisotropy uint16
}

// CreateSampler creates a sampler. A nil descriptor uses the defaults.
func (d *Device) CreateSampler(desc *SamplerDescriptor) (*Sampler, error) {
	if desc == nil {
		desc = &SamplerDescriptor{}
	}
	var m cmem
	raw := cnew(&m, native.SamplerDescriptor{
		Label:         m.str(desc.Label),
		AddressModeU:  orDefault(desc.AddressModeU, native.AddressModeClampToEdge),
		AddressModeV:  orDefault(desc.AddressModeV, native.AddressModeClampToEdge),
		AddressModeW:  orDefault(desc.AddressModeW, native.AddressModeClampToEdge),
		MagFilter:     orDefault(desc.MagFilter, native.FilterModeNearest),
		MinFilter:     orDefault(desc.MinFilter, native.FilterModeNearest),
		MipmapFilter:  orDefault(desc.MipmapFilter, native.MipmapFilterModeNearest),
		LodMinClamp:   desc.LodMinClamp,
		LodMaxClamp:   orDefault(desc.LodMaxClamp, 32),
		Compare:       desc.Compare,
		MaxAnisotropy: orDefault(desc.MaxAnisotropy, 1),
	})
	handle := native.DeviceCreateSampler(d.Native(), raw)
	m.done()
	if handle == 0 {
		return nil, d.fail("CreateSampler")
	}
	s := &Sampler{}
	s.ptr.Store(uintptr(handle))
	return s, nil
}

// Sampler is a WGPUSampler.
type Sampler struct {
	ptr atomic.Uintptr
}

// Native returns the raw handle.
func (s *Sampler) Native() native.Sampler {
	if s == nil {
		return 0
	}
	return native.Sampler(s.ptr.Load())
}

// Release drops the sampler.
func (s *Sampler) Release() {
	handle := native.Sampler(s.ptr.Swap(0))
	if handle != 0 {
		native.SamplerRelease(handle)
	}
}

// TexelCopyTexture names a texture region for copies. A zero Aspect is All.
type TexelCopyTexture struct {
	Texture  *Texture
	MipLevel uint32
	Origin   native.Origin3D
	Aspect   native.TextureAspect
}

func (c TexelCopyTexture) raw() native.TexelCopyTextureInfo {
	return native.TexelCopyTextureInfo{
		Texture:  c.Texture.Native(),
		MipLevel: c.MipLevel,
		Origin:   c.Origin,
		Aspect:   orDefault(c.Aspect, native.TextureAspectAll),
	}
}

// TexelCopyBuffer names a buffer region for copies. For buffer copies
// BytesPerRow must be a multiple of 256. Zero BytesPerRow or RowsPerImage
// is treated as undefined.
type TexelCopyBuffer struct {
	Buffer *Buffer
	Layout native.TexelCopyBufferLayout
}

func copyLayout(l native.TexelCopyBufferLayout) native.TexelCopyBufferLayout {
	l.BytesPerRow = orDefault(l.BytesPerRow, native.CopyStrideUndefined)
	l.RowsPerImage = orDefault(l.RowsPerImage, native.CopyStrideUndefined)
	return l
}

func (c TexelCopyBuffer) raw() native.TexelCopyBufferInfo {
	return native.TexelCopyBufferInfo{
		Layout: copyLayout(c.Layout),
		Buffer: c.Buffer.Native(),
	}
}
