//go:build windows && amd64

package wgpu

import (
	"sync/atomic"

	"github.com/liuqi0826/wgpu/native"
)

// BufferBinding is a buffer slot in a bind group layout. A zero Type is unused.
type BufferBinding struct {
	Type             native.BufferBindingType
	HasDynamicOffset bool
	MinBindingSize   uint64
}

// SamplerBinding is a sampler slot. A zero Type is unused.
type SamplerBinding struct {
	Type native.SamplerBindingType
}

// TextureBinding is a sampled texture slot. A zero SampleType is unused.
// A zero ViewDimension is 2D.
type TextureBinding struct {
	SampleType    native.TextureSampleType
	ViewDimension native.TextureViewDimension
	Multisampled  bool
}

// StorageTextureBinding is a storage texture slot. A zero Access is unused.
// A zero ViewDimension is 2D.
type StorageTextureBinding struct {
	Access        native.StorageTextureAccess
	Format        native.TextureFormat
	ViewDimension native.TextureViewDimension
}

// BindGroupLayoutEntry describes one binding. Set exactly one resource kind.
type BindGroupLayoutEntry struct {
	Binding          uint32
	Visibility       native.ShaderStage
	BindingArraySize uint32
	Buffer           BufferBinding
	Sampler          SamplerBinding
	Texture          TextureBinding
	StorageTexture   StorageTextureBinding
}

// BindGroupLayoutDescriptor describes Device.CreateBindGroupLayout.
type BindGroupLayoutDescriptor struct {
	Label   string
	Entries []BindGroupLayoutEntry
}

// BindGroupLayout is a WGPUBindGroupLayout.
type BindGroupLayout struct {
	ptr atomic.Uintptr
}

// CreateBindGroupLayout creates a bind group layout.
func (d *Device) CreateBindGroupLayout(desc *BindGroupLayoutDescriptor) (*BindGroupLayout, error) {
	if desc == nil {
		desc = &BindGroupLayoutDescriptor{}
	}
	var m cmem
	raw := cnew(&m, native.BindGroupLayoutDescriptor{Label: m.str(desc.Label)})
	if n := len(desc.Entries); n > 0 {
		entries := cslice[native.BindGroupLayoutEntry](&m, n)
		for i, e := range desc.Entries {
			entry := native.BindGroupLayoutEntry{
				Binding:          e.Binding,
				Visibility:       e.Visibility,
				BindingArraySize: e.BindingArraySize,
				Buffer: native.BufferBindingLayout{
					Type:             e.Buffer.Type,
					HasDynamicOffset: cbool(e.Buffer.HasDynamicOffset),
					MinBindingSize:   e.Buffer.MinBindingSize,
				},
				Sampler: native.SamplerBindingLayout{Type: e.Sampler.Type},
				Texture: native.TextureBindingLayout{
					SampleType:    e.Texture.SampleType,
					ViewDimension: e.Texture.ViewDimension,
					Multisampled:  cbool(e.Texture.Multisampled),
				},
				StorageTexture: native.StorageTextureBindingLayout{
					Access:        e.StorageTexture.Access,
					Format:        e.StorageTexture.Format,
					ViewDimension: e.StorageTexture.ViewDimension,
				},
			}
			if entry.Texture.SampleType != 0 {
				entry.Texture.ViewDimension = orDefault(entry.Texture.ViewDimension, native.TextureViewDimension2D)
			}
			if entry.StorageTexture.Access != 0 {
				entry.StorageTexture.ViewDimension = orDefault(entry.StorageTexture.ViewDimension, native.TextureViewDimension2D)
			}
			entries[i] = entry
		}
		raw.EntryCount = uintptr(n)
		raw.Entries = sliceAddr(entries)
	}
	handle := native.DeviceCreateBindGroupLayout(d.Native(), raw)
	m.done()
	if handle == 0 {
		return nil, d.fail("CreateBindGroupLayout")
	}
	l := &BindGroupLayout{}
	l.ptr.Store(uintptr(handle))
	return l, nil
}

// Native returns the raw handle.
func (l *BindGroupLayout) Native() native.BindGroupLayout {
	if l == nil {
		return 0
	}
	return native.BindGroupLayout(l.ptr.Load())
}

// Release drops the layout.
func (l *BindGroupLayout) Release() {
	handle := native.BindGroupLayout(l.ptr.Swap(0))
	if handle != 0 {
		native.BindGroupLayoutRelease(handle)
	}
}

// BindGroupEntry binds one resource. Set Buffer, Sampler or TextureView.
// A zero Size binds the rest of the buffer.
type BindGroupEntry struct {
	Binding     uint32
	Buffer      *Buffer
	Offset      uint64
	Size        uint64
	Sampler     *Sampler
	TextureView *TextureView
}

// BindGroupDescriptor describes Device.CreateBindGroup.
type BindGroupDescriptor struct {
	Label   string
	Layout  *BindGroupLayout
	Entries []BindGroupEntry
}

// BindGroup is a WGPUBindGroup.
type BindGroup struct {
	ptr atomic.Uintptr
}

// CreateBindGroup creates a bind group.
func (d *Device) CreateBindGroup(desc *BindGroupDescriptor) (*BindGroup, error) {
	if desc == nil || desc.Layout == nil {
		return nil, &Error{Op: "CreateBindGroup", Status: "nil layout"}
	}
	var m cmem
	raw := cnew(&m, native.BindGroupDescriptor{
		Label:  m.str(desc.Label),
		Layout: desc.Layout.Native(),
	})
	if n := len(desc.Entries); n > 0 {
		entries := cslice[native.BindGroupEntry](&m, n)
		for i, e := range desc.Entries {
			entry := native.BindGroupEntry{
				Binding:     e.Binding,
				Buffer:      e.Buffer.Native(),
				Offset:      e.Offset,
				Size:        e.Size,
				Sampler:     e.Sampler.Native(),
				TextureView: e.TextureView.Native(),
			}
			if e.Buffer != nil && e.Size == 0 {
				entry.Size = native.WholeSize
			}
			entries[i] = entry
		}
		raw.EntryCount = uintptr(n)
		raw.Entries = sliceAddr(entries)
	}
	handle := native.DeviceCreateBindGroup(d.Native(), raw)
	m.done()
	if handle == 0 {
		return nil, d.fail("CreateBindGroup")
	}
	g := &BindGroup{}
	g.ptr.Store(uintptr(handle))
	return g, nil
}

// Native returns the raw handle.
func (g *BindGroup) Native() native.BindGroup {
	if g == nil {
		return 0
	}
	return native.BindGroup(g.ptr.Load())
}

// Release drops the bind group.
func (g *BindGroup) Release() {
	handle := native.BindGroup(g.ptr.Swap(0))
	if handle != 0 {
		native.BindGroupRelease(handle)
	}
}

// PipelineLayoutDescriptor describes Device.CreatePipelineLayout.
type PipelineLayoutDescriptor struct {
	Label            string
	BindGroupLayouts []*BindGroupLayout
	ImmediateSize    uint32
}

// PipelineLayout is a WGPUPipelineLayout.
type PipelineLayout struct {
	ptr atomic.Uintptr
}

// CreatePipelineLayout creates a pipeline layout.
func (d *Device) CreatePipelineLayout(desc *PipelineLayoutDescriptor) (*PipelineLayout, error) {
	if desc == nil {
		desc = &PipelineLayoutDescriptor{}
	}
	var m cmem
	raw := cnew(&m, native.PipelineLayoutDescriptor{
		Label:         m.str(desc.Label),
		ImmediateSize: desc.ImmediateSize,
	})
	if n := len(desc.BindGroupLayouts); n > 0 {
		layouts := cslice[native.BindGroupLayout](&m, n)
		for i, l := range desc.BindGroupLayouts {
			layouts[i] = l.Native()
		}
		raw.BindGroupLayoutCount = uintptr(n)
		raw.BindGroupLayouts = sliceAddr(layouts)
	}
	handle := native.DeviceCreatePipelineLayout(d.Native(), raw)
	m.done()
	if handle == 0 {
		return nil, d.fail("CreatePipelineLayout")
	}
	l := &PipelineLayout{}
	l.ptr.Store(uintptr(handle))
	return l, nil
}

// Native returns the raw handle.
func (l *PipelineLayout) Native() native.PipelineLayout {
	if l == nil {
		return 0
	}
	return native.PipelineLayout(l.ptr.Load())
}

// Release drops the pipeline layout.
func (l *PipelineLayout) Release() {
	handle := native.PipelineLayout(l.ptr.Swap(0))
	if handle != 0 {
		native.PipelineLayoutRelease(handle)
	}
}

// ConstantEntry overrides a WGSL pipeline-overridable constant.
type ConstantEntry struct {
	Key   string
	Value float64
}

// VertexAttribute is one attribute inside a vertex buffer.
type VertexAttribute struct {
	Format         native.VertexFormat
	Offset         uint64
	ShaderLocation uint32
}

// VertexBufferLayout describes one vertex buffer slot. A zero StepMode is Vertex.
type VertexBufferLayout struct {
	ArrayStride uint64
	StepMode    native.VertexStepMode
	Attributes  []VertexAttribute
}

// VertexState is the vertex stage. An empty EntryPoint picks the only
// @vertex function in the module.
type VertexState struct {
	Module     *ShaderModule
	EntryPoint string
	Constants  []ConstantEntry
	Buffers    []VertexBufferLayout
}

// PrimitiveState controls assembly and rasterization. Zero values are
// TriangleList, CCW and no culling.
type PrimitiveState struct {
	Topology         native.PrimitiveTopology
	StripIndexFormat native.IndexFormat
	FrontFace        native.FrontFace
	CullMode         native.CullMode
	UnclippedDepth   bool
}

// DepthStencilState enables a depth/stencil attachment. A zero DepthCompare is
// Always, zero stencil faces are Always/Keep and zero masks are 0xFFFFFFFF.
type DepthStencilState struct {
	Format              native.TextureFormat
	DepthWriteEnabled   bool
	DepthCompare        native.CompareFunction
	StencilFront        native.StencilFaceState
	StencilBack         native.StencilFaceState
	StencilReadMask     uint32
	StencilWriteMask    uint32
	DepthBias           int32
	DepthBiasSlopeScale float32
	DepthBiasClamp      float32
}

// MultisampleState sets MSAA. Zero Count is 1 and zero Mask is all samples.
type MultisampleState struct {
	Count                  uint32
	Mask                   uint32
	AlphaToCoverageEnabled bool
}

// Common blend states.
var (
	BlendReplace = native.BlendState{
		Color: native.BlendComponent{Operation: native.BlendOperationAdd, SrcFactor: native.BlendFactorOne, DstFactor: native.BlendFactorZero},
		Alpha: native.BlendComponent{Operation: native.BlendOperationAdd, SrcFactor: native.BlendFactorOne, DstFactor: native.BlendFactorZero},
	}
	BlendAlpha = native.BlendState{
		Color: native.BlendComponent{Operation: native.BlendOperationAdd, SrcFactor: native.BlendFactorSrcAlpha, DstFactor: native.BlendFactorOneMinusSrcAlpha},
		Alpha: native.BlendComponent{Operation: native.BlendOperationAdd, SrcFactor: native.BlendFactorOne, DstFactor: native.BlendFactorOneMinusSrcAlpha},
	}
	BlendPremultiplied = native.BlendState{
		Color: native.BlendComponent{Operation: native.BlendOperationAdd, SrcFactor: native.BlendFactorOne, DstFactor: native.BlendFactorOneMinusSrcAlpha},
		Alpha: native.BlendComponent{Operation: native.BlendOperationAdd, SrcFactor: native.BlendFactorOne, DstFactor: native.BlendFactorOneMinusSrcAlpha},
	}
)

// ColorTargetState is one fragment output. A nil Blend disables blending and
// a zero WriteMask is All.
type ColorTargetState struct {
	Format    native.TextureFormat
	Blend     *native.BlendState
	WriteMask native.ColorWriteMask
}

// FragmentState is the fragment stage. An empty EntryPoint picks the only
// @fragment function in the module.
type FragmentState struct {
	Module     *ShaderModule
	EntryPoint string
	Constants  []ConstantEntry
	Targets    []ColorTargetState
}

// RenderPipelineDescriptor describes Device.CreateRenderPipeline.
// A nil Layout derives the layout from the shaders.
type RenderPipelineDescriptor struct {
	Label        string
	Layout       *PipelineLayout
	Vertex       VertexState
	Primitive    PrimitiveState
	DepthStencil *DepthStencilState
	Multisample  MultisampleState
	Fragment     *FragmentState
}

// RenderPipeline is a WGPURenderPipeline.
type RenderPipeline struct {
	ptr atomic.Uintptr
}

func packConstants(m *cmem, in []ConstantEntry) (uintptr, uintptr) {
	if len(in) == 0 {
		return 0, 0
	}
	out := cslice[native.ConstantEntry](m, len(in))
	for i, c := range in {
		out[i] = native.ConstantEntry{Key: m.str(c.Key), Value: c.Value}
	}
	return uintptr(len(out)), sliceAddr(out)
}

func stencilFace(f native.StencilFaceState) native.StencilFaceState {
	return native.StencilFaceState{
		Compare:     orDefault(f.Compare, native.CompareFunctionAlways),
		FailOp:      orDefault(f.FailOp, native.StencilOperationKeep),
		DepthFailOp: orDefault(f.DepthFailOp, native.StencilOperationKeep),
		PassOp:      orDefault(f.PassOp, native.StencilOperationKeep),
	}
}

// CreateRenderPipeline creates a render pipeline.
func (d *Device) CreateRenderPipeline(desc *RenderPipelineDescriptor) (*RenderPipeline, error) {
	if desc == nil || desc.Vertex.Module == nil {
		return nil, &Error{Op: "CreateRenderPipeline", Status: "nil vertex module"}
	}
	var m cmem
	prim := desc.Primitive
	ms := desc.Multisample
	raw := cnew(&m, native.RenderPipelineDescriptor{
		Label:  m.str(desc.Label),
		Layout: desc.Layout.Native(),
		Vertex: native.VertexState{
			Module:     desc.Vertex.Module.Native(),
			EntryPoint: m.optStr(desc.Vertex.EntryPoint),
		},
		Primitive: native.PrimitiveState{
			Topology:         orDefault(prim.Topology, native.PrimitiveTopologyTriangleList),
			StripIndexFormat: prim.StripIndexFormat,
			FrontFace:        orDefault(prim.FrontFace, native.FrontFaceCCW),
			CullMode:         orDefault(prim.CullMode, native.CullModeNone),
			UnclippedDepth:   cbool(prim.UnclippedDepth),
		},
		Multisample: native.MultisampleState{
			Count:                  orDefault(ms.Count, 1),
			Mask:                   orDefault(ms.Mask, ^uint32(0)),
			AlphaToCoverageEnabled: cbool(ms.AlphaToCoverageEnabled),
		},
	})
	raw.Vertex.ConstantCount, raw.Vertex.Constants = packConstants(&m, desc.Vertex.Constants)
	if n := len(desc.Vertex.Buffers); n > 0 {
		buffers := cslice[native.VertexBufferLayout](&m, n)
		for i, b := range desc.Vertex.Buffers {
			attrs := cslice[native.VertexAttribute](&m, len(b.Attributes))
			for j, a := range b.Attributes {
				attrs[j] = native.VertexAttribute{
					Format:         a.Format,
					Offset:         a.Offset,
					ShaderLocation: a.ShaderLocation,
				}
			}
			buffers[i] = native.VertexBufferLayout{
				StepMode:       orDefault(b.StepMode, native.VertexStepModeVertex),
				ArrayStride:    b.ArrayStride,
				AttributeCount: uintptr(len(attrs)),
				Attributes:     sliceAddr(attrs),
			}
		}
		raw.Vertex.BufferCount = uintptr(n)
		raw.Vertex.Buffers = sliceAddr(buffers)
	}
	if ds := desc.DepthStencil; ds != nil {
		write := native.OptionalBoolFalse
		if ds.DepthWriteEnabled {
			write = native.OptionalBoolTrue
		}
		raw.DepthStencil = addr(cnew(&m, native.DepthStencilState{
			Format:              ds.Format,
			DepthWriteEnabled:   write,
			DepthCompare:        orDefault(ds.DepthCompare, native.CompareFunctionAlways),
			StencilFront:        stencilFace(ds.StencilFront),
			StencilBack:         stencilFace(ds.StencilBack),
			StencilReadMask:     orDefault(ds.StencilReadMask, ^uint32(0)),
			StencilWriteMask:    orDefault(ds.StencilWriteMask, ^uint32(0)),
			DepthBias:           ds.DepthBias,
			DepthBiasSlopeScale: ds.DepthBiasSlopeScale,
			DepthBiasClamp:      ds.DepthBiasClamp,
		}))
	}
	if fs := desc.Fragment; fs != nil {
		if fs.Module == nil {
			return nil, &Error{Op: "CreateRenderPipeline", Status: "nil fragment module"}
		}
		frag := cnew(&m, native.FragmentState{
			Module:     fs.Module.Native(),
			EntryPoint: m.optStr(fs.EntryPoint),
		})
		frag.ConstantCount, frag.Constants = packConstants(&m, fs.Constants)
		if n := len(fs.Targets); n > 0 {
			targets := cslice[native.ColorTargetState](&m, n)
			for i, t := range fs.Targets {
				target := native.ColorTargetState{
					Format:    t.Format,
					WriteMask: orDefault(t.WriteMask, native.ColorWriteMaskAll),
				}
				if t.Blend != nil {
					target.Blend = addr(cnew(&m, *t.Blend))
				}
				targets[i] = target
			}
			frag.TargetCount = uintptr(n)
			frag.Targets = sliceAddr(targets)
		}
		raw.Fragment = addr(frag)
	}
	handle := native.DeviceCreateRenderPipeline(d.Native(), raw)
	m.done()
	if handle == 0 {
		return nil, d.fail("CreateRenderPipeline")
	}
	p := &RenderPipeline{}
	p.ptr.Store(uintptr(handle))
	return p, nil
}

// Native returns the raw handle.
func (p *RenderPipeline) Native() native.RenderPipeline {
	if p == nil {
		return 0
	}
	return native.RenderPipeline(p.ptr.Load())
}

// BindGroupLayout returns the layout of group index, which is useful with an
// automatic pipeline layout. The caller must Release it.
func (p *RenderPipeline) BindGroupLayout(index uint32) (*BindGroupLayout, error) {
	handle := native.RenderPipelineGetBindGroupLayout(p.Native(), index)
	if handle == 0 {
		return nil, &Error{Op: "RenderPipelineGetBindGroupLayout", Status: "null layout"}
	}
	l := &BindGroupLayout{}
	l.ptr.Store(uintptr(handle))
	return l, nil
}

// Release drops the pipeline.
func (p *RenderPipeline) Release() {
	handle := native.RenderPipeline(p.ptr.Swap(0))
	if handle != 0 {
		native.RenderPipelineRelease(handle)
	}
}
