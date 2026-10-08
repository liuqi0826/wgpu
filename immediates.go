//go:build windows && amd64

package wgpu

import (
	"math"
	"runtime"
	"unsafe"

	"github.com/liuqi0826/wgpu/native"
)

// FeatureImmediates is the wgpu-native feature needed for immediate data
// (SetImmediates and PipelineLayoutDescriptor.ImmediateSize). Check
// Adapter.HasFeature, then put it in DeviceOptions.RequiredFeatures together
// with a RequiredLimits.MaxImmediateSize large enough for the layouts; the
// default limit is 0.
const FeatureImmediates = native.FeatureName(native.NativeFeatureImmediates)

// checkImmediates returns why SetImmediates arguments must not reach
// wgpu-native, or "" when they may. wgpu-native declares the size as a 32-bit
// integer while wgpu.h passes size_t, so a size of 4 GiB or more would be
// silently truncated; render bundles abort the process on misalignment.
func checkImmediates(d *Device, offset uint32, n int) string {
	switch {
	case d.Native() == 0:
		return "device released"
	case !d.HasFeature(FeatureImmediates):
		return "device was not created with FeatureImmediates"
	case offset%4 != 0:
		return "offset is not a multiple of 4"
	case n%4 != 0:
		return "data size is not a multiple of 4"
	case uint64(n) > math.MaxUint32:
		return "data size is 4 GiB or more; wgpu-native reads it as a 32-bit value"
	case uint64(offset)+uint64(n) > math.MaxUint32:
		return "offset + data size exceeds 4 GiB"
	}
	return ""
}

// SetImmediates writes data to the immediate data of the current pipeline at
// byte offset. offset and len(data) must be multiples of 4 and the range must
// lie within the pipeline layout's ImmediateSize. The device needs
// FeatureImmediates. Errors are returned by CommandEncoder.Finish; empty data
// does nothing.
func (p *RenderPassEncoder) SetImmediates(offset uint32, data []byte) {
	const op = "RenderPassEncoderSetImmediates"
	if p.Native() == 0 {
		p.enc.setErr(op, "released pass")
		return
	}
	if len(data) == 0 {
		return
	}
	if status := checkImmediates(p.enc.dev, offset, len(data)); status != "" {
		p.enc.setErr(op, status)
		return
	}
	native.RenderPassEncoderSetImmediates(p.Native(), offset, unsafe.Pointer(&data[0]), uintptr(len(data)))
	runtime.KeepAlive(data)
}

// SetImmediates writes data to the immediate data of the current pipeline at
// byte offset. The rules are those of RenderPassEncoder.SetImmediates.
func (p *ComputePassEncoder) SetImmediates(offset uint32, data []byte) {
	const op = "ComputePassEncoderSetImmediates"
	if p.Native() == 0 {
		p.enc.setErr(op, "released pass")
		return
	}
	if len(data) == 0 {
		return
	}
	if status := checkImmediates(p.enc.dev, offset, len(data)); status != "" {
		p.enc.setErr(op, status)
		return
	}
	native.ComputePassEncoderSetImmediates(p.Native(), offset, unsafe.Pointer(&data[0]), uintptr(len(data)))
	runtime.KeepAlive(data)
}

// SetImmediates records a write to the immediate data of the current
// pipeline. The rules are those of RenderPassEncoder.SetImmediates; data is
// copied. wgpu-native aborts the process on a misaligned offset or size in a
// bundle, so those are rejected here, and a range outside the pipeline layout
// is reported by Finish through its validation pass.
func (e *RenderBundleEncoder) SetImmediates(offset uint32, data []byte) {
	if e.done {
		e.fail("encoder already finished")
		return
	}
	if len(data) == 0 {
		return
	}
	if status := checkImmediates(e.dev, offset, len(data)); status != "" {
		e.fail(status)
		return
	}
	e.record(bundleCmd{op: opSetImmediates, a: offset, data: append([]byte(nil), data...)})
}

func (p passRecorder) setImmediates(offset uint32, data []byte) {
	native.RenderPassEncoderSetImmediates(native.RenderPassEncoder(p), offset, unsafe.Pointer(&data[0]), uintptr(len(data)))
	runtime.KeepAlive(data)
}

func (r bundleRecorder) setImmediates(offset uint32, data []byte) {
	native.RenderBundleEncoderSetImmediates(native.RenderBundleEncoder(r), offset, unsafe.Pointer(&data[0]), uintptr(len(data)))
	runtime.KeepAlive(data)
}
