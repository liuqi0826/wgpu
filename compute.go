//go:build windows && amd64

package wgpu

import (
	"runtime"
	"sync/atomic"

	"github.com/liuqi0826/wgpu/native"
)

// ComputeState is the compute stage. An empty EntryPoint picks the only
// @compute function in the module.
type ComputeState struct {
	Module     *ShaderModule
	EntryPoint string
	Constants  []ConstantEntry
}

// ComputePipelineDescriptor describes Device.CreateComputePipeline.
// A nil Layout derives the layout from the shader.
type ComputePipelineDescriptor struct {
	Label   string
	Layout  *PipelineLayout
	Compute ComputeState
}

// ComputePipeline is a WGPUComputePipeline.
type ComputePipeline struct {
	ptr atomic.Uintptr
}

// CreateComputePipeline creates a compute pipeline.
func (d *Device) CreateComputePipeline(desc *ComputePipelineDescriptor) (*ComputePipeline, error) {
	if desc == nil || desc.Compute.Module == nil {
		return nil, &Error{Op: "CreateComputePipeline", Status: "nil compute module"}
	}
	var m cmem
	raw := cnew(&m, native.ComputePipelineDescriptor{
		Label:  m.str(desc.Label),
		Layout: desc.Layout.Native(),
		Compute: native.ComputeState{
			Module:     desc.Compute.Module.Native(),
			EntryPoint: m.optStr(desc.Compute.EntryPoint),
		},
	})
	raw.Compute.ConstantCount, raw.Compute.Constants = packConstants(&m, desc.Compute.Constants)
	handle := native.DeviceCreateComputePipeline(d.Native(), raw)
	m.done()
	if handle == 0 {
		return nil, d.fail("CreateComputePipeline")
	}
	p := &ComputePipeline{}
	p.ptr.Store(uintptr(handle))
	return p, nil
}

// Native returns the raw handle.
func (p *ComputePipeline) Native() native.ComputePipeline {
	if p == nil {
		return 0
	}
	return native.ComputePipeline(p.ptr.Load())
}

// BindGroupLayout returns the layout of group index, which is useful with an
// automatic pipeline layout. The caller must Release it.
func (p *ComputePipeline) BindGroupLayout(index uint32) (*BindGroupLayout, error) {
	handle := native.ComputePipelineGetBindGroupLayout(p.Native(), index)
	if handle == 0 {
		return nil, &Error{Op: "ComputePipelineGetBindGroupLayout", Status: "null layout"}
	}
	l := &BindGroupLayout{}
	l.ptr.Store(uintptr(handle))
	return l, nil
}

// Release drops the pipeline.
func (p *ComputePipeline) Release() {
	handle := native.ComputePipeline(p.ptr.Swap(0))
	if handle != 0 {
		native.ComputePipelineRelease(handle)
	}
}

// ComputePassDescriptor describes CommandEncoder.BeginComputePass.
type ComputePassDescriptor struct {
	Label           string
	TimestampWrites *PassTimestampWrites
}

// ComputePassEncoder records dispatches. Call End, then Release.
type ComputePassEncoder struct {
	ptr atomic.Uintptr
}

// BeginComputePass starts a compute pass. desc may be nil.
func (e *CommandEncoder) BeginComputePass(desc *ComputePassDescriptor) (*ComputePassEncoder, error) {
	var m cmem
	var raw *native.ComputePassDescriptor
	if desc != nil {
		writes, err := desc.TimestampWrites.raw(&m, "CommandEncoderBeginComputePass")
		if err != nil {
			return nil, err
		}
		raw = cnew(&m, native.ComputePassDescriptor{Label: m.str(desc.Label), TimestampWrites: writes})
	}
	handle := native.CommandEncoderBeginComputePass(e.Native(), raw)
	m.done()
	if handle == 0 {
		return nil, &Error{Op: "CommandEncoderBeginComputePass", Status: "null encoder"}
	}
	pass := &ComputePassEncoder{}
	pass.ptr.Store(uintptr(handle))
	return pass, nil
}

// Native returns the raw handle.
func (p *ComputePassEncoder) Native() native.ComputePassEncoder {
	if p == nil {
		return 0
	}
	return native.ComputePassEncoder(p.ptr.Load())
}

// SetPipeline selects the compute pipeline.
func (p *ComputePassEncoder) SetPipeline(pipeline *ComputePipeline) {
	native.ComputePassEncoderSetPipeline(p.Native(), pipeline.Native())
}

// SetBindGroup binds group at index with optional dynamic offsets.
func (p *ComputePassEncoder) SetBindGroup(index uint32, group *BindGroup, dynamicOffsets ...uint32) {
	if len(dynamicOffsets) == 0 {
		native.ComputePassEncoderSetBindGroup(p.Native(), index, group.Native(), 0, nil)
		return
	}
	offsets := escape(append([]uint32(nil), dynamicOffsets...))
	native.ComputePassEncoderSetBindGroup(p.Native(), index, group.Native(), uintptr(len(offsets)), &offsets[0])
	runtime.KeepAlive(offsets)
}

// DispatchWorkgroups dispatches x*y*z workgroups.
func (p *ComputePassEncoder) DispatchWorkgroups(x, y, z uint32) {
	native.ComputePassEncoderDispatchWorkgroups(p.Native(), x, y, z)
}

// DispatchWorkgroupsIndirect reads three uint32 workgroup counts from buf at
// offset. buf needs BufferUsageIndirect.
func (p *ComputePassEncoder) DispatchWorkgroupsIndirect(buf *Buffer, offset uint64) {
	native.ComputePassEncoderDispatchWorkgroupsIndirect(p.Native(), buf.Native(), offset)
}

// End finishes recording the pass.
func (p *ComputePassEncoder) End() {
	native.ComputePassEncoderEnd(p.Native())
}

// Release drops the pass encoder.
func (p *ComputePassEncoder) Release() {
	handle := native.ComputePassEncoder(p.ptr.Swap(0))
	if handle != 0 {
		native.ComputePassEncoderRelease(handle)
	}
}
