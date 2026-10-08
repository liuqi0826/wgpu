//go:build windows && amd64

package wgpu

import (
	"strconv"

	"github.com/liuqi0826/wgpu/native"
)

// FeatureMultiDrawIndirectCount is the wgpu-native feature needed for
// MultiDrawIndirectCount and MultiDrawIndexedIndirectCount (DX12 and Vulkan
// 1.2+). MultiDrawIndirect and MultiDrawIndexedIndirect need no feature.
const FeatureMultiDrawIndirectCount = native.FeatureName(native.NativeFeatureMultiDrawIndirectCount)

// Sizes of one indirect argument record.
const (
	drawIndirectStride        = 16 // vertexCount, instanceCount, firstVertex, firstInstance
	drawIndexedIndirectStride = 20 // indexCount, instanceCount, firstIndex, baseVertex, firstInstance
)

// checkIndirect returns why an indirect buffer range must not reach
// wgpu-native, or "". wgpu-native aborts on a null buffer, and wgpu-core
// computes offset + stride*count without an overflow check.
func checkIndirect(buf *Buffer, offset uint64, count uint32, stride uint64) string {
	switch {
	case buf.Native() == 0:
		return "nil or released indirect buffer"
	case offset%4 != 0:
		return "indirect offset is not a multiple of 4"
	case offset > buf.Size() || uint64(count)*stride > buf.Size()-offset:
		return "indirect buffer overrun: offset " + strconv.FormatUint(offset, 10) +
			" + " + strconv.FormatUint(uint64(count), 10) + " x " + strconv.FormatUint(stride, 10) +
			" bytes exceeds size " + strconv.FormatUint(buf.Size(), 10)
	}
	return ""
}

// checkCount does the same for the count buffer of the Count variants. Vulkan
// requires a 4-byte aligned count offset, which wgpu-core does not check.
func (p *RenderPassEncoder) checkCount(countBuf *Buffer, countOffset uint64) string {
	d := p.enc.dev
	switch {
	case d.Native() == 0:
		return "device released"
	case !d.HasFeature(FeatureMultiDrawIndirectCount):
		return "device was not created with FeatureMultiDrawIndirectCount"
	case countBuf.Native() == 0:
		return "nil or released count buffer"
	case countOffset%4 != 0:
		return "count buffer offset is not a multiple of 4"
	case countOffset > countBuf.Size() || countBuf.Size()-countOffset < 4:
		return "count buffer overrun: offset " + strconv.FormatUint(countOffset, 10) +
			" + 4 bytes exceeds size " + strconv.FormatUint(countBuf.Size(), 10)
	}
	return ""
}

func (p *RenderPassEncoder) multiDrawOK(op string, buf *Buffer, offset uint64, count uint32, stride uint64) bool {
	status := ""
	if p.Native() == 0 {
		status = "released pass"
	} else {
		status = checkIndirect(buf, offset, count, stride)
	}
	if status != "" {
		p.enc.setErr(op, status)
		return false
	}
	return true
}

// MultiDrawIndirect issues count draws whose arguments are consecutive
// 16-byte records (vertexCount, instanceCount, firstVertex, firstInstance as
// uint32) in buf starting at offset. buf needs BufferUsageIndirect, offset
// must be a multiple of 4 and the records must fit in buf. Errors are
// returned by CommandEncoder.Finish. Without FeatureMultiDrawIndirectCount on
// the device wgpu may emulate it with separate indirect draws.
//
// On D3D12, @builtin(vertex_index) includes firstVertex (baseVertex for
// indexed draws) only when the instance has InstanceFlagValidationIndirectCall:
// CreateInstance(nil) sets it, InstanceOptions with zero Flags does not. The
// Count variants never include it on D3D12. Vertex buffer fetches are correct
// either way.
func (p *RenderPassEncoder) MultiDrawIndirect(buf *Buffer, offset uint64, count uint32) {
	if p.multiDrawOK("RenderPassEncoderMultiDrawIndirect", buf, offset, count, drawIndirectStride) {
		native.RenderPassEncoderMultiDrawIndirect(p.Native(), buf.Native(), offset, count)
	}
}

// MultiDrawIndexedIndirect is MultiDrawIndirect for indexed draws; each record
// is 20 bytes (indexCount, instanceCount, firstIndex as uint32, baseVertex as
// int32, firstInstance as uint32).
func (p *RenderPassEncoder) MultiDrawIndexedIndirect(buf *Buffer, offset uint64, count uint32) {
	if p.multiDrawOK("RenderPassEncoderMultiDrawIndexedIndirect", buf, offset, count, drawIndexedIndirectStride) {
		native.RenderPassEncoderMultiDrawIndexedIndirect(p.Native(), buf.Native(), offset, count)
	}
}

// MultiDrawIndirectCount is MultiDrawIndirect with the draw count read by the
// GPU as a uint32 from countBuf at countOffset and clamped to maxCount. The
// device needs FeatureMultiDrawIndirectCount, countBuf needs
// BufferUsageIndirect, countOffset must be a multiple of 4, and maxCount
// records must fit in buf.
func (p *RenderPassEncoder) MultiDrawIndirectCount(buf *Buffer, offset uint64, countBuf *Buffer, countOffset uint64, maxCount uint32) {
	const op = "RenderPassEncoderMultiDrawIndirectCount"
	if !p.multiDrawOK(op, buf, offset, maxCount, drawIndirectStride) {
		return
	}
	if status := p.checkCount(countBuf, countOffset); status != "" {
		p.enc.setErr(op, status)
		return
	}
	native.RenderPassEncoderMultiDrawIndirectCount(p.Native(), buf.Native(), offset, countBuf.Native(), countOffset, maxCount)
}

// MultiDrawIndexedIndirectCount is MultiDrawIndexedIndirect with the draw
// count read from countBuf, as in MultiDrawIndirectCount.
func (p *RenderPassEncoder) MultiDrawIndexedIndirectCount(buf *Buffer, offset uint64, countBuf *Buffer, countOffset uint64, maxCount uint32) {
	const op = "RenderPassEncoderMultiDrawIndexedIndirectCount"
	if !p.multiDrawOK(op, buf, offset, maxCount, drawIndexedIndirectStride) {
		return
	}
	if status := p.checkCount(countBuf, countOffset); status != "" {
		p.enc.setErr(op, status)
		return
	}
	native.RenderPassEncoderMultiDrawIndexedIndirectCount(p.Native(), buf.Native(), offset, countBuf.Native(), countOffset, maxCount)
}
