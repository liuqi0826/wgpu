//go:build windows && amd64

package wgpu

import (
	"runtime"
	"sync/atomic"

	"github.com/liuqi0826/wgpu/native"
)

// maxColorAttachments is hal::MAX_COLOR_ATTACHMENTS in wgpu-core 29.
const maxColorAttachments = 8

// RenderBundleEncoderDescriptor describes Device.CreateRenderBundleEncoder.
// The formats and sample count must match the passes that execute the
// bundle. A zero SampleCount is 1 and a zero DepthStencilFormat means none.
type RenderBundleEncoderDescriptor struct {
	Label              string
	ColorFormats       []native.TextureFormat
	DepthStencilFormat native.TextureFormat
	SampleCount        uint32
	DepthReadOnly      bool
	StencilReadOnly    bool
}

type bundleOp uint8

const (
	opSetPipeline bundleOp = iota
	opSetBindGroup
	opSetVertexBuffer
	opSetIndexBuffer
	opDraw
	opDrawIndexed
	opDrawIndirect
	opDrawIndexedIndirect
	opPushDebugGroup
	opPopDebugGroup
	opInsertDebugMarker
)

type bundleCmd struct {
	op       bundleOp
	pipeline *RenderPipeline
	group    *BindGroup
	buf      *Buffer
	format   native.IndexFormat
	offsets  []uint32
	a, b, c  uint32
	d        uint32
	base     int32
	off, sz  uint64
	label    string
}

// RenderBundleEncoder records draw commands for a reusable RenderBundle.
//
// Commands are kept in Go and only replayed into wgpu-native by Finish.
// wgpu-native aborts the process when creating a bundle encoder or finishing
// a bundle fails, so Finish first replays the commands into a throwaway render
// pass with matching attachments and returns its validation error instead.
type RenderBundleEncoder struct {
	dev  *Device
	desc RenderBundleEncoderDescriptor
	cmds []bundleCmd
	err  error
	done bool

	debugDepth int
}

// CreateRenderBundleEncoder starts recording a render bundle. Release the
// encoder when it is no longer needed.
func (d *Device) CreateRenderBundleEncoder(desc *RenderBundleEncoderDescriptor) (*RenderBundleEncoder, error) {
	if desc == nil {
		return nil, &Error{Op: "CreateRenderBundleEncoder", Status: "nil descriptor"}
	}
	cp := *desc
	cp.ColorFormats = append([]native.TextureFormat(nil), desc.ColorFormats...)
	cp.SampleCount = orDefault(cp.SampleCount, 1)
	switch {
	case len(cp.ColorFormats) == 0 && cp.DepthStencilFormat == native.TextureFormatUndefined:
		return nil, &Error{Op: "CreateRenderBundleEncoder", Status: "no attachment formats"}
	case len(cp.ColorFormats) > maxColorAttachments:
		return nil, &Error{Op: "CreateRenderBundleEncoder", Status: "too many color formats"}
	case cp.SampleCount > 32 || cp.SampleCount&(cp.SampleCount-1) != 0:
		return nil, &Error{Op: "CreateRenderBundleEncoder", Status: "invalid sample count"}
	}
	for _, f := range cp.ColorFormats {
		if f == native.TextureFormatUndefined {
			return nil, &Error{Op: "CreateRenderBundleEncoder", Status: "undefined color format"}
		}
	}
	return &RenderBundleEncoder{dev: d, desc: cp}, nil
}

func (e *RenderBundleEncoder) record(c bundleCmd) {
	if e.done {
		e.fail("encoder already finished")
		return
	}
	e.cmds = append(e.cmds, c)
}

func (e *RenderBundleEncoder) fail(status string) {
	if e.err == nil {
		e.err = &Error{Op: "RenderBundleEncoder", Status: status}
	}
}

// SetPipeline selects the render pipeline.
func (e *RenderBundleEncoder) SetPipeline(pipeline *RenderPipeline) {
	if pipeline.Native() == 0 {
		e.fail("nil or released pipeline")
		return
	}
	e.record(bundleCmd{op: opSetPipeline, pipeline: pipeline})
}

// SetBindGroup binds group at index with optional dynamic offsets.
func (e *RenderBundleEncoder) SetBindGroup(index uint32, group *BindGroup, dynamicOffsets ...uint32) {
	if group.Native() == 0 {
		e.fail("nil or released bind group")
		return
	}
	e.record(bundleCmd{op: opSetBindGroup, a: index, group: group, offsets: append([]uint32(nil), dynamicOffsets...)})
}

// SetVertexBuffer binds buf to slot. A zero size binds the rest of the buffer.
func (e *RenderBundleEncoder) SetVertexBuffer(slot uint32, buf *Buffer, offset, size uint64) {
	if buf.Native() == 0 {
		e.fail("nil or released vertex buffer")
		return
	}
	e.record(bundleCmd{op: opSetVertexBuffer, a: slot, buf: buf, off: offset, sz: size})
}

// SetIndexBuffer binds the index buffer. A zero size binds the rest of the buffer.
func (e *RenderBundleEncoder) SetIndexBuffer(buf *Buffer, format native.IndexFormat, offset, size uint64) {
	if buf.Native() == 0 {
		e.fail("nil or released index buffer")
		return
	}
	e.record(bundleCmd{op: opSetIndexBuffer, buf: buf, format: format, off: offset, sz: size})
}

// Draw draws non-indexed primitives.
func (e *RenderBundleEncoder) Draw(vertexCount, instanceCount, firstVertex, firstInstance uint32) {
	e.record(bundleCmd{op: opDraw, a: vertexCount, b: instanceCount, c: firstVertex, d: firstInstance})
}

// DrawIndexed draws indexed primitives.
func (e *RenderBundleEncoder) DrawIndexed(indexCount, instanceCount, firstIndex uint32, baseVertex int32, firstInstance uint32) {
	e.record(bundleCmd{op: opDrawIndexed, a: indexCount, b: instanceCount, c: firstIndex, base: baseVertex, d: firstInstance})
}

// DrawIndirect draws with arguments read from buf at offset.
func (e *RenderBundleEncoder) DrawIndirect(buf *Buffer, offset uint64) {
	if buf.Native() == 0 {
		e.fail("nil or released indirect buffer")
		return
	}
	e.record(bundleCmd{op: opDrawIndirect, buf: buf, off: offset})
}

// DrawIndexedIndirect draws indexed primitives with arguments read from buf.
func (e *RenderBundleEncoder) DrawIndexedIndirect(buf *Buffer, offset uint64) {
	if buf.Native() == 0 {
		e.fail("nil or released indirect buffer")
		return
	}
	e.record(bundleCmd{op: opDrawIndexedIndirect, buf: buf, off: offset})
}

// renderRecorder is implemented by render passes and native bundle encoders.
type renderRecorder interface {
	setPipeline(native.RenderPipeline)
	setBindGroup(uint32, native.BindGroup, uintptr, *uint32)
	setVertexBuffer(uint32, native.Buffer, uint64, uint64)
	setIndexBuffer(native.Buffer, native.IndexFormat, uint64, uint64)
	draw(a, b, c, d uint32)
	drawIndexed(a, b, c uint32, base int32, d uint32)
	drawIndirect(native.Buffer, uint64)
	drawIndexedIndirect(native.Buffer, uint64)
	pushDebugGroup(string)
	popDebugGroup()
	insertDebugMarker(string)
}

type passRecorder native.RenderPassEncoder

func (p passRecorder) setPipeline(h native.RenderPipeline) {
	native.RenderPassEncoderSetPipeline(native.RenderPassEncoder(p), h)
}
func (p passRecorder) setBindGroup(i uint32, g native.BindGroup, n uintptr, o *uint32) {
	native.RenderPassEncoderSetBindGroup(native.RenderPassEncoder(p), i, g, n, o)
}
func (p passRecorder) setVertexBuffer(s uint32, b native.Buffer, off, sz uint64) {
	native.RenderPassEncoderSetVertexBuffer(native.RenderPassEncoder(p), s, b, off, sz)
}
func (p passRecorder) setIndexBuffer(b native.Buffer, f native.IndexFormat, off, sz uint64) {
	native.RenderPassEncoderSetIndexBuffer(native.RenderPassEncoder(p), b, f, off, sz)
}
func (p passRecorder) draw(a, b, c, d uint32) {
	native.RenderPassEncoderDraw(native.RenderPassEncoder(p), a, b, c, d)
}
func (p passRecorder) drawIndexed(a, b, c uint32, base int32, d uint32) {
	native.RenderPassEncoderDrawIndexed(native.RenderPassEncoder(p), a, b, c, base, d)
}
func (p passRecorder) drawIndirect(b native.Buffer, off uint64) {
	native.RenderPassEncoderDrawIndirect(native.RenderPassEncoder(p), b, off)
}
func (p passRecorder) drawIndexedIndirect(b native.Buffer, off uint64) {
	native.RenderPassEncoderDrawIndexedIndirect(native.RenderPassEncoder(p), b, off)
}

type bundleRecorder native.RenderBundleEncoder

func (r bundleRecorder) setPipeline(h native.RenderPipeline) {
	native.RenderBundleEncoderSetPipeline(native.RenderBundleEncoder(r), h)
}
func (r bundleRecorder) setBindGroup(i uint32, g native.BindGroup, n uintptr, o *uint32) {
	native.RenderBundleEncoderSetBindGroup(native.RenderBundleEncoder(r), i, g, n, o)
}
func (r bundleRecorder) setVertexBuffer(s uint32, b native.Buffer, off, sz uint64) {
	native.RenderBundleEncoderSetVertexBuffer(native.RenderBundleEncoder(r), s, b, off, sz)
}
func (r bundleRecorder) setIndexBuffer(b native.Buffer, f native.IndexFormat, off, sz uint64) {
	native.RenderBundleEncoderSetIndexBuffer(native.RenderBundleEncoder(r), b, f, off, sz)
}
func (r bundleRecorder) draw(a, b, c, d uint32) {
	native.RenderBundleEncoderDraw(native.RenderBundleEncoder(r), a, b, c, d)
}
func (r bundleRecorder) drawIndexed(a, b, c uint32, base int32, d uint32) {
	native.RenderBundleEncoderDrawIndexed(native.RenderBundleEncoder(r), a, b, c, base, d)
}
func (r bundleRecorder) drawIndirect(b native.Buffer, off uint64) {
	native.RenderBundleEncoderDrawIndirect(native.RenderBundleEncoder(r), b, off)
}
func (r bundleRecorder) drawIndexedIndirect(b native.Buffer, off uint64) {
	native.RenderBundleEncoderDrawIndexedIndirect(native.RenderBundleEncoder(r), b, off)
}

// handlesAlive reports whether every object a command uses is still alive.
// wgpu-native aborts on a null handle.
func (e *RenderBundleEncoder) handlesAlive() bool {
	for _, c := range e.cmds {
		switch {
		case c.pipeline != nil && c.pipeline.Native() == 0,
			c.group != nil && c.group.Native() == 0,
			c.buf != nil && c.buf.Native() == 0:
			return false
		}
	}
	return true
}

func (e *RenderBundleEncoder) replay(r renderRecorder) {
	for _, c := range e.cmds {
		switch c.op {
		case opSetPipeline:
			r.setPipeline(c.pipeline.Native())
		case opSetBindGroup:
			if len(c.offsets) == 0 {
				r.setBindGroup(c.a, c.group.Native(), 0, nil)
			} else {
				offsets := escape(append([]uint32(nil), c.offsets...))
				r.setBindGroup(c.a, c.group.Native(), uintptr(len(offsets)), &offsets[0])
				runtime.KeepAlive(offsets)
			}
		case opSetVertexBuffer:
			r.setVertexBuffer(c.a, c.buf.Native(), c.off, wholeIfZero(c.sz))
		case opSetIndexBuffer:
			r.setIndexBuffer(c.buf.Native(), c.format, c.off, wholeIfZero(c.sz))
		case opDraw:
			r.draw(c.a, c.b, c.c, c.d)
		case opDrawIndexed:
			r.drawIndexed(c.a, c.b, c.c, c.base, c.d)
		case opDrawIndirect:
			r.drawIndirect(c.buf.Native(), c.off)
		case opDrawIndexedIndirect:
			r.drawIndexedIndirect(c.buf.Native(), c.off)
		case opPushDebugGroup:
			r.pushDebugGroup(c.label)
		case opPopDebugGroup:
			r.popDebugGroup()
		case opInsertDebugMarker:
			r.insertDebugMarker(c.label)
		}
	}
}

func wholeIfZero(size uint64) uint64 {
	if size == 0 {
		return native.WholeSize
	}
	return size
}

func hasDepthAspect(f native.TextureFormat) bool {
	switch f {
	case native.TextureFormatDepth16Unorm, native.TextureFormatDepth24Plus,
		native.TextureFormatDepth24PlusStencil8, native.TextureFormatDepth32Float,
		native.TextureFormatDepth32FloatStencil8:
		return true
	}
	return false
}

func hasStencilAspect(f native.TextureFormat) bool {
	switch f {
	case native.TextureFormatStencil8, native.TextureFormatDepth24PlusStencil8,
		native.TextureFormatDepth32FloatStencil8:
		return true
	}
	return false
}

// validate replays the commands into a render pass on 1x1 attachments that
// match the descriptor. Render passes report errors through error scopes
// instead of aborting, and run the same checks as bundles.
func (e *RenderBundleEncoder) validate() error {
	d := e.dev
	var textures []*Texture
	var views []*TextureView
	defer func() {
		for _, v := range views {
			v.Release()
		}
		for _, t := range textures {
			t.Release()
		}
	}()
	var failed error
	attachment := func(format native.TextureFormat) *TextureView {
		failed = &Error{Op: "RenderBundleEncoderFinish", Status: "cannot create a validation attachment", Message: format.String()}
		tex, err := d.CreateTexture(&TextureDescriptor{
			Label:       "bundle validation",
			Usage:       native.TextureUsageRenderAttachment,
			Width:       1,
			Height:      1,
			Format:      format,
			SampleCount: e.desc.SampleCount,
		})
		if err != nil {
			return nil
		}
		textures = append(textures, tex)
		view, err := tex.CreateView(nil)
		if err != nil {
			return nil
		}
		views = append(views, view)
		failed = nil
		return view
	}
	err := d.guard(func() {
		desc := &RenderPassDescriptor{Label: "bundle validation"}
		for _, f := range e.desc.ColorFormats {
			view := attachment(f)
			if view == nil {
				return
			}
			desc.ColorAttachments = append(desc.ColorAttachments, RenderPassColorAttachment{View: view})
		}
		if f := e.desc.DepthStencilFormat; f != native.TextureFormatUndefined {
			view := attachment(f)
			if view == nil {
				return
			}
			ds := &RenderPassDepthStencilAttachment{
				View:            view,
				DepthClearValue: 1,
				DepthReadOnly:   e.desc.DepthReadOnly || !hasDepthAspect(f),
			}
			if hasStencilAspect(f) {
				ds.StencilReadOnly = e.desc.StencilReadOnly
				if !ds.StencilReadOnly {
					ds.StencilLoadOp = native.LoadOpClear
					ds.StencilStoreOp = native.StoreOpStore
				}
			}
			desc.DepthStencilAttachment = ds
		}
		failed = &Error{Op: "RenderBundleEncoderFinish", Status: "validation pass failed"}
		handle := native.DeviceCreateCommandEncoder(d.Native(), nil)
		if handle == 0 {
			return
		}
		defer native.CommandEncoderRelease(handle)
		enc := &CommandEncoder{dev: d}
		enc.ptr.Store(uintptr(handle))
		pass, err := enc.BeginRenderPass(desc)
		if err != nil {
			return
		}
		e.replay(passRecorder(pass.Native()))
		pass.End()
		pass.Release()
		if cb := native.CommandEncoderFinish(handle, nil); cb != 0 {
			native.CommandBufferRelease(cb)
			failed = nil
		}
	})
	if err != nil {
		return err
	}
	return failed
}

// Finish validates the recorded commands and creates the bundle. The encoder
// cannot be used afterwards; it still needs Release.
func (e *RenderBundleEncoder) Finish() (*RenderBundle, error) {
	fail := func(status, msg string) (*RenderBundle, error) {
		return nil, &Error{Op: "RenderBundleEncoderFinish", Status: status, Message: msg}
	}
	if e.done {
		return fail("encoder already finished", "")
	}
	e.done = true
	if e.err == nil && e.debugDepth > 0 {
		e.fail(openGroupsStatus(e.debugDepth))
	}
	if e.err != nil {
		return nil, e.err
	}
	if e.dev.isLost() {
		return nil, e.dev.Err()
	}
	if !e.handlesAlive() {
		return fail("a recorded object was released", "")
	}
	if err := e.validate(); err != nil {
		if werr, ok := err.(*Error); ok {
			return fail(werr.Status, werr.Message)
		}
		return nil, err
	}

	var m cmem
	raw := cnew(&m, native.RenderBundleEncoderDescriptor{
		Label:              m.str(e.desc.Label),
		DepthStencilFormat: e.desc.DepthStencilFormat,
		SampleCount:        e.desc.SampleCount,
		DepthReadOnly:      cbool(e.desc.DepthReadOnly),
		StencilReadOnly:    cbool(e.desc.StencilReadOnly),
	})
	if n := len(e.desc.ColorFormats); n > 0 {
		formats := cslice[native.TextureFormat](&m, n)
		copy(formats, e.desc.ColorFormats)
		raw.ColorFormatCount = uintptr(n)
		raw.ColorFormats = sliceAddr(formats)
	}
	encoder := native.DeviceCreateRenderBundleEncoder(e.dev.Native(), raw)
	m.done()
	if encoder == 0 {
		return fail("null encoder", "")
	}
	defer native.RenderBundleEncoderRelease(encoder)
	e.replay(bundleRecorder(encoder))
	var m2 cmem
	handle := native.RenderBundleEncoderFinish(encoder, cnew(&m2, native.RenderBundleDescriptor{Label: m2.str(e.desc.Label)}))
	m2.done()
	if handle == 0 {
		return fail("null bundle", "")
	}
	b := &RenderBundle{}
	b.ptr.Store(uintptr(handle))
	e.cmds = nil
	return b, nil
}

// Release drops the recorded commands.
func (e *RenderBundleEncoder) Release() {
	e.cmds = nil
	e.done = true
}

// RenderBundle is a WGPURenderBundle, executed with RenderPassEncoder.ExecuteBundles.
type RenderBundle struct {
	ptr atomic.Uintptr
}

// Native returns the raw handle.
func (b *RenderBundle) Native() native.RenderBundle {
	if b == nil {
		return 0
	}
	return native.RenderBundle(b.ptr.Load())
}

// Release drops the bundle.
func (b *RenderBundle) Release() {
	handle := native.RenderBundle(b.ptr.Swap(0))
	if handle != 0 {
		native.RenderBundleRelease(handle)
	}
}
