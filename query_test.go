//go:build windows && amd64

package wgpu

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/liuqi0826/wgpu/native"
)

// readU64s resolves count queries and reads them back.
func readU64s(t *testing.T, device *Device, enc *CommandEncoder, q *QuerySet, count uint32) []uint64 {
	t.Helper()
	size := uint64(count) * 8
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
	enc.ResolveQuerySet(q, 0, count, resolve, 0)
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
	out := make([]uint64, count)
	for i := range out {
		out[i] = binary.LittleEndian.Uint64(data[8*i:])
	}
	return out
}

const fullscreenWGSL = `
@vertex
fn vs_main(@builtin(vertex_index) i: u32) -> @builtin(position) vec4f {
	let uv = vec2f(f32((i << 1u) & 2u), f32(i & 2u));
	return vec4f(uv * 2.0 - 1.0, 0.5, 1.0);
}

@fragment
fn fs_main() -> @location(0) vec4f {
	return vec4f(1.0, 1.0, 1.0, 1.0);
}
`

func TestOcclusionQuery(t *testing.T) {
	device := newTestDevice(t)
	const size = 32
	_, view := newTarget(t, device, size, size, native.TextureFormatRGBA8Unorm)
	depth, err := device.CreateTexture(&TextureDescriptor{
		Usage: native.TextureUsageRenderAttachment, Width: size, Height: size, Format: native.TextureFormatDepth24Plus,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer depth.Release()
	depthView, err := depth.CreateView(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer depthView.Release()
	module, err := device.CreateShaderModuleWGSL("fullscreen", fullscreenWGSL)
	if err != nil {
		t.Fatal(err)
	}
	defer module.Release()
	pipeline, err := device.CreateRenderPipeline(&RenderPipelineDescriptor{
		Vertex: VertexState{Module: module},
		DepthStencil: &DepthStencilState{
			Format:            native.TextureFormatDepth24Plus,
			DepthWriteEnabled: true,
			DepthCompare:      native.CompareFunctionLess,
		},
		Fragment: &FragmentState{Module: module, Targets: []ColorTargetState{{Format: native.TextureFormatRGBA8Unorm}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pipeline.Release()
	queries, err := device.CreateQuerySet(&QuerySetDescriptor{Label: "occlusion", Type: native.QueryTypeOcclusion, Count: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer queries.Release()
	if queries.Count() != 3 || queries.Type() != native.QueryTypeOcclusion {
		t.Fatalf("query set reports %d %v", queries.Count(), queries.Type())
	}

	enc, err := device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	pass, err := enc.BeginRenderPass(&RenderPassDescriptor{
		ColorAttachments:       []RenderPassColorAttachment{{View: view}},
		DepthStencilAttachment: &RenderPassDepthStencilAttachment{View: depthView, DepthClearValue: 1},
		OcclusionQuerySet:      queries,
	})
	if err != nil {
		t.Fatal(err)
	}
	pass.SetPipeline(pipeline)
	// 0: visible. 1: same depth again, fails Less. 2: draws nothing.
	pass.BeginOcclusionQuery(0)
	pass.Draw(3, 1, 0, 0)
	pass.EndOcclusionQuery()
	pass.BeginOcclusionQuery(1)
	pass.Draw(3, 1, 0, 0)
	pass.EndOcclusionQuery()
	pass.BeginOcclusionQuery(2)
	pass.EndOcclusionQuery()
	pass.End()
	pass.Release()
	got := readU64s(t, device, enc, queries, 3)
	t.Logf("occlusion results %v", got)
	if got[0] == 0 || got[1] != 0 || got[2] != 0 {
		t.Fatalf("occlusion results %v, want [non-zero 0 0]", got)
	}
	if err := device.Err(); err != nil {
		t.Fatal(err)
	}
}

const busyWGSL = `
@group(0) @binding(0) var<storage, read_write> data: array<u32>;

@compute @workgroup_size(64)
fn main(@builtin(global_invocation_id) id: vec3u) {
	var x = id.x;
	for (var i = 0u; i < 4096u; i++) {
		x = x * 1664525u + 1013904223u;
	}
	data[id.x] = x;
}
`

func TestTimestampQuery(t *testing.T) {
	adapter := newTestAdapter(t)
	if !adapter.HasFeature(native.FeatureNameTimestampQuery) {
		t.Skip("adapter has no TimestampQuery")
	}
	device, err := adapter.RequestDeviceWith(&DeviceOptions{RequiredFeatures: []native.FeatureName{native.FeatureNameTimestampQuery}})
	if err != nil {
		t.Fatal(err)
	}
	defer device.Release()

	if _, err := device.CreateQuerySet(&QuerySetDescriptor{Type: native.QueryTypeTimestamp, Count: 5000}); err == nil {
		t.Error("query set over the 4096 limit accepted")
	}
	queries, err := device.CreateQuerySet(&QuerySetDescriptor{Label: "timestamps", Type: native.QueryTypeTimestamp, Count: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer queries.Release()

	module, err := device.CreateShaderModuleWGSL("busy", busyWGSL)
	if err != nil {
		t.Fatal(err)
	}
	defer module.Release()
	pipeline, err := device.CreateComputePipeline(&ComputePipelineDescriptor{Compute: ComputeState{Module: module}})
	if err != nil {
		t.Fatal(err)
	}
	defer pipeline.Release()
	const n = 1 << 18
	storage, err := device.CreateBuffer(native.BufferUsageStorage, n*4, false)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Release()
	layout, err := pipeline.BindGroupLayout(0)
	if err != nil {
		t.Fatal(err)
	}
	defer layout.Release()
	group, err := device.CreateBindGroup(&BindGroupDescriptor{Layout: layout, Entries: []BindGroupEntry{{Binding: 0, Buffer: storage}}})
	if err != nil {
		t.Fatal(err)
	}
	defer group.Release()
	_, view := newTarget(t, device, 256, 256, native.TextureFormatRGBA8Unorm)

	enc, err := device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	cpass, err := enc.BeginComputePass(&ComputePassDescriptor{
		TimestampWrites: &PassTimestampWrites{QuerySet: queries, BeginningOfPassWriteIndex: 0, EndOfPassWriteIndex: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	cpass.SetPipeline(pipeline)
	cpass.SetBindGroup(0, group)
	cpass.DispatchWorkgroups(n/64, 1, 1)
	cpass.End()
	cpass.Release()
	rpass, err := enc.BeginRenderPass(&RenderPassDescriptor{
		ColorAttachments: []RenderPassColorAttachment{{View: view}},
		TimestampWrites:  &PassTimestampWrites{QuerySet: queries, BeginningOfPassWriteIndex: 2, EndOfPassWriteIndex: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	rpass.End()
	rpass.Release()
	ts := readU64s(t, device, enc, queries, 4)
	period := float64(device.Queue().TimestampPeriod())
	compute := time.Duration(float64(ts[1]-ts[0]) * period)
	render := time.Duration(float64(ts[3]-ts[2]) * period)
	t.Logf("raw %v, period %v ns, compute pass %v, render pass %v", ts, period, compute, render)
	if ts[1] <= ts[0] || ts[3] < ts[2] {
		t.Fatalf("timestamps not increasing: %v", ts)
	}
	if compute <= 0 || compute > 10*time.Second {
		t.Fatalf("compute pass took %v", compute)
	}
	if err := device.Err(); err != nil {
		t.Fatal(err)
	}

	// Timestamp writes without a query set would abort in wgpu-native.
	enc2, err := device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	defer enc2.Release()
	if _, err := enc2.BeginComputePass(&ComputePassDescriptor{TimestampWrites: &PassTimestampWrites{}}); err == nil {
		t.Fatal("timestamp writes without a query set accepted")
	}
}
