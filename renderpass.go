//go:build windows && amd64

package wgpu

import (
	"runtime"
	"sync/atomic"
	"unsafe"

	"github.com/liuqi0826/wgpu/native"
)

// RenderPassColorAttachment is one color target of a render pass.
// Zero LoadOp and StoreOp are Clear and Store.
type RenderPassColorAttachment struct {
	View          *TextureView
	ResolveTarget *TextureView
	LoadOp        native.LoadOp
	StoreOp       native.StoreOp
	ClearValue    native.Color
}

// RenderPassDepthStencilAttachment is the depth/stencil target. Unless
// DepthReadOnly is set, zero depth ops are Clear and Store. Stencil ops stay
// as given and must be zero for formats without stencil. DepthClearValue is
// used as is; it is usually 1.
type RenderPassDepthStencilAttachment struct {
	View              *TextureView
	DepthLoadOp       native.LoadOp
	DepthStoreOp      native.StoreOp
	DepthClearValue   float32
	DepthReadOnly     bool
	StencilLoadOp     native.LoadOp
	StencilStoreOp    native.StoreOp
	StencilClearValue uint32
	StencilReadOnly   bool
}

// RenderPassDescriptor describes CommandEncoder.BeginRenderPass.
type RenderPassDescriptor struct {
	Label                  string
	ColorAttachments       []RenderPassColorAttachment
	DepthStencilAttachment *RenderPassDepthStencilAttachment
	OcclusionQuerySet      *QuerySet
	TimestampWrites        *PassTimestampWrites
}

// RenderPassEncoder records draw commands. Call End, then Release.
type RenderPassEncoder struct {
	ptr atomic.Uintptr
	enc *CommandEncoder

	debugDepth  int
	statsActive bool
}

// BeginRenderPass starts a render pass.
func (e *CommandEncoder) BeginRenderPass(desc *RenderPassDescriptor) (*RenderPassEncoder, error) {
	if desc == nil {
		desc = &RenderPassDescriptor{}
	}
	if desc.OcclusionQuerySet != nil && desc.OcclusionQuerySet.Native() == 0 {
		return nil, &Error{Op: "CommandEncoderBeginRenderPass", Status: "released occlusion query set"}
	}
	var m cmem
	writes, err := desc.TimestampWrites.raw(&m, "CommandEncoderBeginRenderPass")
	if err != nil {
		return nil, err
	}
	raw := cnew(&m, native.RenderPassDescriptor{
		Label:             m.str(desc.Label),
		OcclusionQuerySet: desc.OcclusionQuerySet.Native(),
		TimestampWrites:   writes,
	})
	if n := len(desc.ColorAttachments); n > 0 {
		atts := cslice[native.RenderPassColorAttachment](&m, n)
		for i, a := range desc.ColorAttachments {
			atts[i] = native.RenderPassColorAttachment{
				View:          a.View.Native(),
				DepthSlice:    native.DepthSliceUndefined,
				ResolveTarget: a.ResolveTarget.Native(),
				LoadOp:        orDefault(a.LoadOp, native.LoadOpClear),
				StoreOp:       orDefault(a.StoreOp, native.StoreOpStore),
				ClearValue:    a.ClearValue,
			}
		}
		raw.ColorAttachmentCount = uintptr(n)
		raw.ColorAttachments = sliceAddr(atts)
	}
	if ds := desc.DepthStencilAttachment; ds != nil {
		att := native.RenderPassDepthStencilAttachment{
			View:              ds.View.Native(),
			DepthClearValue:   ds.DepthClearValue,
			DepthReadOnly:     cbool(ds.DepthReadOnly),
			StencilLoadOp:     ds.StencilLoadOp,
			StencilStoreOp:    ds.StencilStoreOp,
			StencilClearValue: ds.StencilClearValue,
			StencilReadOnly:   cbool(ds.StencilReadOnly),
		}
		if !ds.DepthReadOnly {
			att.DepthLoadOp = orDefault(ds.DepthLoadOp, native.LoadOpClear)
			att.DepthStoreOp = orDefault(ds.DepthStoreOp, native.StoreOpStore)
		}
		raw.DepthStencilAttachment = addr(cnew(&m, att))
	}
	handle := native.CommandEncoderBeginRenderPass(e.Native(), raw)
	m.done()
	if handle == 0 {
		return nil, &Error{Op: "CommandEncoderBeginRenderPass", Status: "null encoder"}
	}
	pass := &RenderPassEncoder{enc: e}
	pass.ptr.Store(uintptr(handle))
	return pass, nil
}

// Native returns the raw handle.
func (p *RenderPassEncoder) Native() native.RenderPassEncoder {
	if p == nil {
		return 0
	}
	return native.RenderPassEncoder(p.ptr.Load())
}

// SetPipeline selects the render pipeline. A nil or released pipeline is
// reported by CommandEncoder.Finish.
func (p *RenderPassEncoder) SetPipeline(pipeline *RenderPipeline) {
	if pipeline.Native() == 0 {
		p.enc.setErr("RenderPassEncoderSetPipeline", "nil or released pipeline")
		return
	}
	native.RenderPassEncoderSetPipeline(p.Native(), pipeline.Native())
}

// ExecuteBundles replays render bundles in this pass. A nil or released
// bundle is reported by CommandEncoder.Finish and nothing is executed.
func (p *RenderPassEncoder) ExecuteBundles(bundles ...*RenderBundle) {
	if len(bundles) == 0 {
		return
	}
	raw := escape(make([]native.RenderBundle, len(bundles)))
	for i, b := range bundles {
		raw[i] = b.Native()
		if raw[i] == 0 {
			p.enc.setErr("RenderPassEncoderExecuteBundles", "nil or released bundle")
			return
		}
	}
	native.RenderPassEncoderExecuteBundles(p.Native(), uintptr(len(raw)), &raw[0])
	runtime.KeepAlive(raw)
}

// DrawIndirect draws with arguments read from buf at offset.
func (p *RenderPassEncoder) DrawIndirect(buf *Buffer, offset uint64) {
	native.RenderPassEncoderDrawIndirect(p.Native(), buf.Native(), offset)
}

// DrawIndexedIndirect draws indexed primitives with arguments read from buf.
func (p *RenderPassEncoder) DrawIndexedIndirect(buf *Buffer, offset uint64) {
	native.RenderPassEncoderDrawIndexedIndirect(p.Native(), buf.Native(), offset)
}

// SetBindGroup binds group at index with optional dynamic offsets.
func (p *RenderPassEncoder) SetBindGroup(index uint32, group *BindGroup, dynamicOffsets ...uint32) {
	if len(dynamicOffsets) == 0 {
		native.RenderPassEncoderSetBindGroup(p.Native(), index, group.Native(), 0, nil)
		return
	}
	offsets := escape(append([]uint32(nil), dynamicOffsets...))
	native.RenderPassEncoderSetBindGroup(p.Native(), index, group.Native(), uintptr(len(offsets)), &offsets[0])
	runtime.KeepAlive(offsets)
}

// SetVertexBuffer binds buf to slot. A zero size binds the rest of the buffer.
func (p *RenderPassEncoder) SetVertexBuffer(slot uint32, buf *Buffer, offset, size uint64) {
	if size == 0 {
		size = native.WholeSize
	}
	native.RenderPassEncoderSetVertexBuffer(p.Native(), slot, buf.Native(), offset, size)
}

// SetIndexBuffer binds the index buffer. A zero size binds the rest of the buffer.
func (p *RenderPassEncoder) SetIndexBuffer(buf *Buffer, format native.IndexFormat, offset, size uint64) {
	if size == 0 {
		size = native.WholeSize
	}
	native.RenderPassEncoderSetIndexBuffer(p.Native(), buf.Native(), format, offset, size)
}

// SetViewport sets the viewport in framebuffer pixels.
func (p *RenderPassEncoder) SetViewport(x, y, width, height, minDepth, maxDepth float32) {
	native.RenderPassEncoderSetViewport(p.Native(), x, y, width, height, minDepth, maxDepth)
}

// SetScissorRect sets the scissor rectangle in framebuffer pixels.
func (p *RenderPassEncoder) SetScissorRect(x, y, width, height uint32) {
	native.RenderPassEncoderSetScissorRect(p.Native(), x, y, width, height)
}

// SetBlendConstant sets the color used by BlendFactorConstant.
func (p *RenderPassEncoder) SetBlendConstant(c native.Color) {
	var m cmem
	native.RenderPassEncoderSetBlendConstant(p.Native(), cnew(&m, c))
	m.done()
}

// SetStencilReference sets the stencil reference value.
func (p *RenderPassEncoder) SetStencilReference(ref uint32) {
	native.RenderPassEncoderSetStencilReference(p.Native(), ref)
}

// Draw draws non-indexed primitives.
func (p *RenderPassEncoder) Draw(vertexCount, instanceCount, firstVertex, firstInstance uint32) {
	native.RenderPassEncoderDraw(p.Native(), vertexCount, instanceCount, firstVertex, firstInstance)
}

// DrawIndexed draws indexed primitives.
func (p *RenderPassEncoder) DrawIndexed(indexCount, instanceCount, firstIndex uint32, baseVertex int32, firstInstance uint32) {
	native.RenderPassEncoderDrawIndexed(p.Native(), indexCount, instanceCount, firstIndex, baseVertex, firstInstance)
}

// End finishes recording the pass. Debug groups still open make
// CommandEncoder.Finish fail.
func (p *RenderPassEncoder) End() {
	if p.debugDepth > 0 {
		p.enc.setErr("RenderPassEncoderEnd", openGroupsStatus(p.debugDepth))
		p.debugDepth = 0
	}
	if p.statsActive {
		p.enc.setErr("RenderPassEncoderEnd", "pipeline statistics query still active")
		p.statsActive = false
	}
	native.RenderPassEncoderEnd(p.Native())
}

// Release drops the pass encoder.
func (p *RenderPassEncoder) Release() {
	handle := native.RenderPassEncoder(p.ptr.Swap(0))
	if handle != 0 {
		native.RenderPassEncoderRelease(handle)
	}
}

// CopyBufferToBuffer copies size bytes between buffers.
func (e *CommandEncoder) CopyBufferToBuffer(src *Buffer, srcOffset uint64, dst *Buffer, dstOffset, size uint64) {
	native.CommandEncoderCopyBufferToBuffer(e.Native(), src.Native(), srcOffset, dst.Native(), dstOffset, size)
}

// CopyTextureToBuffer copies a texture region into a buffer.
func (e *CommandEncoder) CopyTextureToBuffer(src TexelCopyTexture, dst TexelCopyBuffer, size native.Extent3D) {
	var m cmem
	native.CommandEncoderCopyTextureToBuffer(e.Native(), cnew(&m, src.raw()), cnew(&m, dst.raw()), cnew(&m, size))
	m.done()
}

// CopyBufferToTexture copies buffer data into a texture region.
func (e *CommandEncoder) CopyBufferToTexture(src TexelCopyBuffer, dst TexelCopyTexture, size native.Extent3D) {
	var m cmem
	native.CommandEncoderCopyBufferToTexture(e.Native(), cnew(&m, src.raw()), cnew(&m, dst.raw()), cnew(&m, size))
	m.done()
}

// WriteTexture uploads data into a texture region. BytesPerRow has no 256-byte
// alignment requirement here.
func (q *Queue) WriteTexture(dst TexelCopyTexture, data []byte, layout native.TexelCopyBufferLayout, size native.Extent3D) {
	if len(data) == 0 {
		return
	}
	var m cmem
	native.QueueWriteTexture(q.ptr, cnew(&m, dst.raw()), unsafe.Pointer(&data[0]), uintptr(len(data)),
		cnew(&m, copyLayout(layout)), cnew(&m, size))
	m.done()
	runtime.KeepAlive(data)
}
