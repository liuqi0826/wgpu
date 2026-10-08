//go:build windows && amd64

package wgpu

import (
	"encoding/binary"
	"testing"

	"github.com/liuqi0826/wgpu/native"
)

const overrideComputeWGSL = `
override scale: u32 = 1u;
@id(7) override offset: u32 = 0u;
override wg_size: u32 = 64u;

@group(0) @binding(0) var<storage, read_write> data: array<u32>;

@compute @workgroup_size(wg_size)
fn main(@builtin(global_invocation_id) id: vec3u) {
	if (id.x < arrayLength(&data)) {
		data[id.x] = data[id.x] * scale + offset;
	}
}
`

// runOverrideCompute runs the shader over 0..n-1 with the given constants
// and dispatches n/groupSize workgroups.
func runOverrideCompute(t *testing.T, device *Device, module *ShaderModule, constants []ConstantEntry, n, groupSize uint32) []uint32 {
	t.Helper()
	pipeline, err := device.CreateComputePipeline(&ComputePipelineDescriptor{
		Compute: ComputeState{Module: module, Constants: constants},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pipeline.Release()
	size := uint64(n) * 4
	storage, err := device.CreateBuffer(native.BufferUsageStorage|native.BufferUsageCopyDst|native.BufferUsageCopySrc, size, false)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Release()
	readback, err := device.CreateBuffer(native.BufferUsageMapRead|native.BufferUsageCopyDst, size, false)
	if err != nil {
		t.Fatal(err)
	}
	defer readback.Release()
	input := make([]byte, size)
	for i := uint32(0); i < n; i++ {
		binary.LittleEndian.PutUint32(input[4*i:], i)
	}
	device.Queue().WriteBuffer(storage, 0, input)
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
	pass.DispatchWorkgroups(n/groupSize, 1, 1)
	pass.End()
	pass.Release()
	enc.CopyBufferToBuffer(storage, 0, readback, 0, size)
	submit(t, device, enc)
	if err := readback.Map(native.MapModeRead, 0, size); err != nil {
		t.Fatal(err)
	}
	raw, err := readback.ReadMapped(0, size)
	readback.Unmap()
	if err != nil {
		t.Fatal(err)
	}
	out := make([]uint32, n)
	for i := range out {
		out[i] = binary.LittleEndian.Uint32(raw[4*i:])
	}
	return out
}

func TestComputeOverrideConstants(t *testing.T) {
	device := newTestDevice(t)
	module, err := device.CreateShaderModuleWGSL("override", overrideComputeWGSL)
	if err != nil {
		t.Fatal(err)
	}
	defer module.Release()
	const n = 256

	// Defaults: scale 1, offset 0, 64-wide workgroups.
	got := runOverrideCompute(t, device, module, nil, n, 64)
	for i, v := range got {
		if v != uint32(i) {
			t.Fatalf("defaults: data[%d] = %d, want %d", i, v, i)
		}
	}
	// By name, by @id, and the workgroup size. With 32-wide workgroups only
	// n/32 dispatches cover the array if the override took effect.
	got = runOverrideCompute(t, device, module, []ConstantEntry{
		{Key: "scale", Value: 3},
		{Key: "7", Value: 5},
		{Key: "wg_size", Value: 32},
	}, n, 32)
	for i, v := range got {
		if want := uint32(i)*3 + 5; v != want {
			t.Fatalf("overridden: data[%d] = %d, want %d", i, v, want)
		}
	}

	// An unknown key is a validation error.
	device.PushErrorScope(native.ErrorFilterValidation)
	if p, err := device.CreateComputePipeline(&ComputePipelineDescriptor{
		Compute: ComputeState{Module: module, Constants: []ConstantEntry{{Key: "missing", Value: 1}}},
	}); err == nil {
		p.Release()
	}
	if err := device.PopErrorScope(); err == nil {
		t.Fatal("unknown override key accepted")
	} else {
		t.Logf("unknown key: %.100q", err.Error())
	}
	if err := device.Err(); err != nil {
		t.Fatal(err)
	}
}

const overrideRenderWGSL = `
override narrow: bool = false;
override red: f32 = 0.0;
override green: f32 = 0.0;
@id(3) override blue: f32 = 1.0;

@vertex
fn vs_main(@builtin(vertex_index) i: u32) -> @builtin(position) vec4f {
	let uv = vec2f(f32((i << 1u) & 2u), f32(i & 2u));
	var p = uv * 2.0 - 1.0;
	if (narrow) {
		// The triangle spans x in [-1, 3]; squeeze that into [-1, 0], so the
		// left quarter is fully covered and the right half stays clear.
		p.x = (p.x + 1.0) * 0.25 - 1.0;
	}
	return vec4f(p, 0.5, 1.0);
}

@fragment
fn fs_main() -> @location(0) vec4f {
	return vec4f(red, green, blue, 1.0);
}
`

func TestRenderOverrideConstants(t *testing.T) {
	device := newTestDevice(t)
	module, err := device.CreateShaderModuleWGSL("override", overrideRenderWGSL)
	if err != nil {
		t.Fatal(err)
	}
	defer module.Release()
	const size = 32
	format := native.TextureFormatRGBA8Unorm
	draw := func(vertex, fragment []ConstantEntry) []byte {
		t.Helper()
		pipeline, err := device.CreateRenderPipeline(&RenderPipelineDescriptor{
			Vertex:   VertexState{Module: module, Constants: vertex},
			Fragment: &FragmentState{Module: module, Constants: fragment, Targets: []ColorTargetState{{Format: format}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer pipeline.Release()
		target, view := newTarget(t, device, size, size, format)
		enc, err := device.CreateCommandEncoder()
		if err != nil {
			t.Fatal(err)
		}
		pass, err := enc.BeginRenderPass(&RenderPassDescriptor{
			ColorAttachments: []RenderPassColorAttachment{{View: view, ClearValue: native.Color{A: 1}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		pass.SetPipeline(pipeline)
		pass.Draw(3, 1, 0, 0)
		pass.End()
		pass.Release()
		submit(t, device, enc)
		return readRGBA8(t, device, target, size, size)
	}
	check := func(name string, data []byte, x, y int, want []byte) {
		t.Helper()
		if got := texel(data, size, x, y); !near(got, want, 1) {
			t.Errorf("%s: pixel (%d,%d) = %v, want %v", name, x, y, got, want)
		}
	}

	def := draw(nil, nil)
	check("defaults", def, 4, 16, []byte{0, 0, 255, 255})
	check("defaults", def, 28, 16, []byte{0, 0, 255, 255})

	over := draw(
		[]ConstantEntry{{Key: "narrow", Value: 1}},
		[]ConstantEntry{{Key: "red", Value: 1}, {Key: "green", Value: 0.5}, {Key: "3", Value: 0}},
	)
	check("overridden", over, 4, 16, []byte{255, 128, 0, 255})
	check("overridden", over, 4, 30, []byte{255, 128, 0, 255})
	check("overridden", over, 20, 16, []byte{0, 0, 0, 255})
	check("overridden", over, 28, 2, []byte{0, 0, 0, 255})
	if err := device.Err(); err != nil {
		t.Fatal(err)
	}
}
