//go:build windows && amd64

package wgpu

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/liuqi0826/wgpu/native"
)

// newStatsDevice returns a device with FeaturePipelineStatisticsQuery or
// skips the test, checking the adapter first because asking for a missing
// feature fails the request.
func newStatsDevice(t *testing.T) *Device {
	t.Helper()
	adapter := newTestAdapter(t)
	if !adapter.HasFeature(FeaturePipelineStatisticsQuery) {
		info, _ := adapter.Info()
		t.Skipf("%s backend (%s) does not support PipelineStatisticsQuery", info.Backend, info.Device)
	}
	device, err := adapter.RequestDeviceWith(&DeviceOptions{RequiredFeatures: []native.FeatureName{FeaturePipelineStatisticsQuery}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(device.Release)
	return device
}

// readStats resolves every query of q, submits enc and decodes the results.
func readStats(t *testing.T, device *Device, enc *CommandEncoder, q *QuerySet) []PipelineStatistics {
	t.Helper()
	size := q.ResultStride() * uint64(q.Count())
	resolve, err := device.CreateBuffer(native.BufferUsageQueryResolve|native.BufferUsageCopySrc, size, false)
	if err != nil {
		t.Fatal(err)
	}
	defer resolve.Release()
	readback, err := device.CreateBuffer(native.BufferUsageMapRead|native.BufferUsageCopyDst, size, false)
	if err != nil {
		t.Fatal(err)
	}
	defer readback.Release()
	enc.ResolveQuerySet(q, 0, q.Count(), resolve, 0)
	enc.CopyBufferToBuffer(resolve, 0, readback, 0, size)
	submit(t, device, enc)
	if err := readback.Map(native.MapModeRead, 0, size); err != nil {
		t.Fatal(err)
	}
	data, err := readback.ReadMapped(0, size)
	readback.Unmap()
	if err != nil {
		t.Fatal(err)
	}
	stats, err := q.ParsePipelineStatistics(data)
	if err != nil {
		t.Fatal(err)
	}
	return stats
}

func TestPipelineStatisticsRender(t *testing.T) {
	device := newStatsDevice(t)
	const size = 64
	format := native.TextureFormatRGBA8Unorm
	_, view := newTarget(t, device, size, size, format)

	// Listed out of order on purpose: results come back in name order.
	q, err := device.CreateQuerySet(&QuerySetDescriptor{
		Label: "render stats",
		Type:  QueryTypePipelineStatistics,
		Count: 2,
		PipelineStatistics: []native.PipelineStatisticName{
			native.PipelineStatisticNameFragmentShaderInvocations,
			native.PipelineStatisticNameVertexShaderInvocations,
			native.PipelineStatisticNameClipperPrimitivesOut,
			native.PipelineStatisticNameClipperInvocations,
			native.PipelineStatisticNameVertexShaderInvocations,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Release()
	if got := q.PipelineStatistics(); len(got) != 4 || got[0] != native.PipelineStatisticNameVertexShaderInvocations || got[3] != native.PipelineStatisticNameFragmentShaderInvocations {
		t.Fatalf("PipelineStatistics() = %v", got)
	}
	if q.ResultStride() != 32 || q.Type() != QueryTypePipelineStatistics {
		t.Fatalf("stride %d, type %#x", q.ResultStride(), uint32(q.Type()))
	}

	module, err := device.CreateShaderModuleWGSL("fullscreen", fullscreenWGSL)
	if err != nil {
		t.Fatal(err)
	}
	defer module.Release()
	pipeline, err := device.CreateRenderPipeline(&RenderPipelineDescriptor{
		Vertex:   VertexState{Module: module},
		Fragment: &FragmentState{Module: module, Targets: []ColorTargetState{{Format: format}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pipeline.Release()

	enc, err := device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	pass, err := enc.BeginRenderPass(&RenderPassDescriptor{ColorAttachments: []RenderPassColorAttachment{{View: view}}})
	if err != nil {
		t.Fatal(err)
	}
	pass.SetPipeline(pipeline)
	pass.BeginPipelineStatisticsQuery(q, 0)
	pass.Draw(3, 1, 0, 0) // one triangle covering the whole target
	pass.EndPipelineStatisticsQuery()
	pass.BeginPipelineStatisticsQuery(q, 1)
	pass.Draw(3, 4, 0, 0) // the same triangle four times
	pass.EndPipelineStatisticsQuery()
	pass.End()
	pass.Release()
	stats := readStats(t, device, enc, q)
	for i, s := range stats {
		t.Logf("query %d: vertex %d, clipper %d, clipped primitives %d, fragment %d",
			i, s.VertexShaderInvocations, s.ClipperInvocations, s.ClipperPrimitivesOut, s.FragmentShaderInvocations)
		if s.Has(native.PipelineStatisticNameComputeShaderInvocations) || !s.Has(native.PipelineStatisticNameClipperInvocations) {
			t.Errorf("query %d: wrong statistics present", i)
		}
	}
	one, four := stats[0], stats[1]
	switch {
	case one.VertexShaderInvocations < 3:
		t.Errorf("vertex invocations %d, want >= 3", one.VertexShaderInvocations)
	case four.VertexShaderInvocations < 3:
		// NVIDIA/Vulkan reports 3 here: the shader ignores instance_index,
		// and drivers may reuse vertex results across instances.
		t.Errorf("instanced vertex invocations %d, want >= 3", four.VertexShaderInvocations)
	case one.ClipperInvocations < 1 || four.ClipperInvocations < 4:
		t.Errorf("clipper invocations %d/%d, want >= 1/4", one.ClipperInvocations, four.ClipperInvocations)
	case one.ClipperPrimitivesOut < 1 || four.ClipperPrimitivesOut < 4:
		t.Errorf("clipper primitives out %d/%d, want >= 1/4", one.ClipperPrimitivesOut, four.ClipperPrimitivesOut)
	case one.FragmentShaderInvocations < size*size:
		t.Errorf("fragment invocations %d, want >= %d", one.FragmentShaderInvocations, size*size)
	case four.FragmentShaderInvocations < 4*size*size:
		t.Errorf("instanced fragment invocations %d, want >= %d", four.FragmentShaderInvocations, 4*size*size)
	}
	if v := one.Value(native.PipelineStatisticNameVertexShaderInvocations); v != one.VertexShaderInvocations {
		t.Errorf("Value() = %d, field = %d", v, one.VertexShaderInvocations)
	}

	// A query left open when the pass ends fails Finish.
	enc, err = device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	pass, err = enc.BeginRenderPass(&RenderPassDescriptor{ColorAttachments: []RenderPassColorAttachment{{View: view}}})
	if err != nil {
		t.Fatal(err)
	}
	pass.BeginPipelineStatisticsQuery(q, 0)
	pass.End()
	pass.Release()
	if cb, err := enc.Finish(); err == nil || !strings.Contains(err.Error(), "still active") {
		if cb != nil {
			cb.Release()
		}
		t.Errorf("open query: %v", err)
	} else {
		t.Logf("open query: %v", err)
	}
	if err := device.Err(); err != nil {
		t.Fatal(err)
	}
}

const statsComputeWGSL = `
@group(0) @binding(0) var<storage, read_write> counter: atomic<u32>;

@compute @workgroup_size(8, 4, 2)
fn main() {
	atomicAdd(&counter, 1u);
}
`

func TestPipelineStatisticsCompute(t *testing.T) {
	device := newStatsDevice(t)
	q, err := device.CreateQuerySet(&QuerySetDescriptor{
		Label: "compute stats",
		Type:  QueryTypePipelineStatistics,
		Count: 2,
		PipelineStatistics: []native.PipelineStatisticName{
			native.PipelineStatisticNameComputeShaderInvocations,
			native.PipelineStatisticNameVertexShaderInvocations,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Release()

	module, err := device.CreateShaderModuleWGSL("stats compute", statsComputeWGSL)
	if err != nil {
		t.Fatal(err)
	}
	defer module.Release()
	pipeline, err := device.CreateComputePipeline(&ComputePipelineDescriptor{Compute: ComputeState{Module: module}})
	if err != nil {
		t.Fatal(err)
	}
	defer pipeline.Release()
	layout, err := pipeline.BindGroupLayout(0)
	if err != nil {
		t.Fatal(err)
	}
	defer layout.Release()
	counter, err := device.CreateBuffer(native.BufferUsageStorage|native.BufferUsageCopySrc|native.BufferUsageCopyDst, 4, false)
	if err != nil {
		t.Fatal(err)
	}
	defer counter.Release()
	device.Queue().WriteBuffer(counter, 0, make([]byte, 4))
	group, err := device.CreateBindGroup(&BindGroupDescriptor{Layout: layout, Entries: []BindGroupEntry{{Binding: 0, Buffer: counter}}})
	if err != nil {
		t.Fatal(err)
	}
	defer group.Release()

	const threads = 8 * 4 * 2
	const gx, gy, gz = 3, 2, 5
	enc, err := device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	pass, err := enc.BeginComputePass(nil)
	if err != nil {
		t.Fatal(err)
	}
	pass.SetPipeline(pipeline)
	pass.SetBindGroup(0, group)
	pass.BeginPipelineStatisticsQuery(q, 0)
	pass.DispatchWorkgroups(gx, gy, gz)
	pass.EndPipelineStatisticsQuery()
	pass.BeginPipelineStatisticsQuery(q, 1)
	pass.DispatchWorkgroups(1, 1, 1)
	pass.End()
	pass.Release()
	// The second query is still open: Finish must report it.
	if cb, err := enc.Finish(); err == nil {
		cb.Release()
		t.Fatal("query left open at pass end was accepted")
	} else {
		t.Logf("open query: %v", err)
	}

	enc, err = device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	pass, err = enc.BeginComputePass(nil)
	if err != nil {
		t.Fatal(err)
	}
	pass.SetPipeline(pipeline)
	pass.SetBindGroup(0, group)
	pass.BeginPipelineStatisticsQuery(q, 0)
	pass.DispatchWorkgroups(gx, gy, gz)
	pass.EndPipelineStatisticsQuery()
	pass.BeginPipelineStatisticsQuery(q, 1)
	pass.DispatchWorkgroups(1, 1, 1)
	pass.EndPipelineStatisticsQuery()
	pass.End()
	pass.Release()
	stats := readStats(t, device, enc, q)
	for i, s := range stats {
		t.Logf("query %d: compute %d, vertex %d", i, s.ComputeShaderInvocations, s.VertexShaderInvocations)
	}
	if got, want := stats[0].ComputeShaderInvocations, uint64(gx*gy*gz*threads); got != want {
		t.Errorf("compute invocations %d, want %d", got, want)
	}
	if got := stats[1].ComputeShaderInvocations; got != threads {
		t.Errorf("compute invocations %d, want %d", got, threads)
	}
	if !stats[0].Has(native.PipelineStatisticNameVertexShaderInvocations) || stats[0].VertexShaderInvocations != 0 {
		t.Errorf("vertex invocations in a compute pass: %d", stats[0].VertexShaderInvocations)
	}

	// Nested queries are rejected too.
	enc, err = device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	pass, err = enc.BeginComputePass(nil)
	if err != nil {
		t.Fatal(err)
	}
	pass.BeginPipelineStatisticsQuery(q, 0)
	pass.BeginPipelineStatisticsQuery(q, 1)
	pass.EndPipelineStatisticsQuery()
	pass.End()
	pass.Release()
	if cb, err := enc.Finish(); err == nil {
		cb.Release()
		t.Fatal("nested queries accepted")
	} else {
		t.Logf("nested: %v", err)
	}
	if err := device.Err(); err != nil {
		t.Fatal(err)
	}
}

// TestPipelineStatisticsValidation runs on every backend: everything here is
// rejected on the Go side before wgpu-native could abort.
func TestPipelineStatisticsValidation(t *testing.T) {
	device := newTestDevice(t)
	vertex := []native.PipelineStatisticName{native.PipelineStatisticNameVertexShaderInvocations}
	for _, c := range []struct {
		name string
		desc QuerySetDescriptor
		want string
	}{
		{"feature not enabled", QuerySetDescriptor{Type: QueryTypePipelineStatistics, Count: 1, PipelineStatistics: vertex}, "FeaturePipelineStatisticsQuery"},
		{"no statistics", QuerySetDescriptor{Type: QueryTypePipelineStatistics, Count: 1}, "without statistics"},
		{"unknown statistic", QuerySetDescriptor{Type: QueryTypePipelineStatistics, Count: 1, PipelineStatistics: []native.PipelineStatisticName{7}}, "unknown pipeline statistic 7"},
		{"statistics on occlusion", QuerySetDescriptor{Type: native.QueryTypeOcclusion, Count: 1, PipelineStatistics: vertex}, "pipeline statistics listed"},
		{"unknown type", QuerySetDescriptor{Type: 0x1234, Count: 1}, "unknown query type 0x1234"},
	} {
		q, err := device.CreateQuerySet(&c.desc)
		if err == nil {
			q.Release()
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want %q", c.name, err, c.want)
		}
		t.Logf("%s: %v", c.name, err)
	}

	occlusion, err := device.CreateQuerySet(&QuerySetDescriptor{Type: native.QueryTypeOcclusion, Count: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer occlusion.Release()
	if _, err := occlusion.ParsePipelineStatistics(make([]byte, 8)); err == nil {
		t.Error("parsed an occlusion query set as pipeline statistics")
	}
	_, view := newTarget(t, device, 4, 4, native.TextureFormatRGBA8Unorm)
	for _, q := range []*QuerySet{nil, occlusion} {
		enc, err := device.CreateCommandEncoder()
		if err != nil {
			t.Fatal(err)
		}
		pass, err := enc.BeginRenderPass(&RenderPassDescriptor{ColorAttachments: []RenderPassColorAttachment{{View: view}}})
		if err != nil {
			t.Fatal(err)
		}
		pass.BeginPipelineStatisticsQuery(q, 0)
		pass.End()
		pass.Release()
		cpass, err := enc.BeginComputePass(nil)
		if err != nil {
			t.Fatal(err)
		}
		cpass.BeginPipelineStatisticsQuery(q, 0)
		cpass.End()
		cpass.Release()
		if cb, err := enc.Finish(); err == nil {
			cb.Release()
			t.Error("Begin with a bad query set accepted")
		} else {
			t.Logf("begin: %v", err)
		}
	}

	// End without Begin.
	enc, err := device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	cpass, err := enc.BeginComputePass(nil)
	if err != nil {
		t.Fatal(err)
	}
	cpass.EndPipelineStatisticsQuery()
	cpass.End()
	cpass.Release()
	if cb, err := enc.Finish(); err == nil || !strings.Contains(err.Error(), "no active pipeline statistics query") {
		if cb != nil {
			cb.Release()
		}
		t.Errorf("End without Begin: %v", err)
	} else {
		t.Logf("end: %v", err)
	}

	adapter := newTestAdapter(t)
	if !adapter.HasFeature(FeaturePipelineStatisticsQuery) {
		_, err := adapter.RequestDeviceWith(&DeviceOptions{RequiredFeatures: []native.FeatureName{FeaturePipelineStatisticsQuery}})
		if err == nil || !strings.Contains(err.Error(), "PipelineStatisticsQuery") {
			t.Errorf("unsupported feature request: %v", err)
		} else {
			t.Logf("request: %v", err)
		}
	}
	if err := device.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestDecodePipelineStatistics(t *testing.T) {
	stats := []native.PipelineStatisticName{
		native.PipelineStatisticNameComputeShaderInvocations,
		native.PipelineStatisticNameVertexShaderInvocations,
	}
	data := make([]byte, 32)
	for i, v := range []uint64{10, 20, 30, 40} {
		binary.LittleEndian.PutUint64(data[8*i:], v)
	}
	got, err := DecodePipelineStatistics(stats, data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].VertexShaderInvocations != 10 || got[0].ComputeShaderInvocations != 20 ||
		got[1].VertexShaderInvocations != 30 || got[1].ComputeShaderInvocations != 40 {
		t.Fatalf("decoded %+v", got)
	}
	if got[0].Has(native.PipelineStatisticNameFragmentShaderInvocations) || got[0].Value(native.PipelineStatisticNameFragmentShaderInvocations) != 0 {
		t.Error("fragment statistic reported as present")
	}
	if _, err := DecodePipelineStatistics(stats, data[:24]); err == nil {
		t.Error("partial query accepted")
	}
	if _, err := DecodePipelineStatistics(nil, data); err == nil {
		t.Error("empty statistics accepted")
	}
}
