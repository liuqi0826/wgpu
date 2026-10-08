//go:build windows && amd64

package wgpu

import (
	"encoding/binary"
	"testing"

	"wgpu/native"
)

const doubleWGSL = `
@group(0) @binding(0) var<storage, read_write> data: array<u32>;

@compute @workgroup_size(64)
fn main(@builtin(global_invocation_id) id: vec3u) {
	if (id.x < arrayLength(&data)) {
		data[id.x] = data[id.x] * 2u;
	}
}
`

func TestComputeDouble(t *testing.T) {
	device := newTestDevice(t)
	queue := device.Queue()
	const n = 1000 // not a multiple of 64, so the bounds check matters
	const size = n * 4
	groups := uint32((n + 63) / 64)

	device.PushErrorScope(native.ErrorFilterValidation)
	module, err := device.CreateShaderModuleWGSL("double", doubleWGSL)
	if err != nil {
		t.Fatal(err)
	}
	defer module.Release()
	pipeline, err := device.CreateComputePipeline(&ComputePipelineDescriptor{
		Label:   "double",
		Compute: ComputeState{Module: module},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pipeline.Release()
	bgl, err := pipeline.BindGroupLayout(0)
	if err != nil {
		t.Fatal(err)
	}
	defer bgl.Release()

	storage, err := device.CreateBuffer(native.BufferUsageStorage|native.BufferUsageCopyDst|native.BufferUsageCopySrc, size, false)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Release()
	input := make([]byte, size)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint32(input[4*i:], uint32(i))
	}
	queue.WriteBuffer(storage, 0, input)

	indirect, err := device.CreateBuffer(native.BufferUsageIndirect|native.BufferUsageCopyDst, 12, false)
	if err != nil {
		t.Fatal(err)
	}
	defer indirect.Release()
	args := make([]byte, 12)
	binary.LittleEndian.PutUint32(args[0:], groups)
	binary.LittleEndian.PutUint32(args[4:], 1)
	binary.LittleEndian.PutUint32(args[8:], 1)
	queue.WriteBuffer(indirect, 0, args)

	readback, err := device.CreateBuffer(native.BufferUsageMapRead|native.BufferUsageCopyDst, size, false)
	if err != nil {
		t.Fatal(err)
	}
	defer readback.Release()

	group, err := device.CreateBindGroup(&BindGroupDescriptor{
		Layout:  bgl,
		Entries: []BindGroupEntry{{Binding: 0, Buffer: storage}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer group.Release()

	enc, err := device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	pass, err := enc.BeginComputePass(&ComputePassDescriptor{Label: "double"})
	if err != nil {
		t.Fatal(err)
	}
	pass.SetPipeline(pipeline)
	pass.SetBindGroup(0, group)
	pass.DispatchWorkgroups(groups, 1, 1)        // x2
	pass.DispatchWorkgroupsIndirect(indirect, 0) // x2 again
	pass.End()
	pass.Release()
	enc.CopyBufferToBuffer(storage, 0, readback, 0, size)
	submit(t, device, enc)
	if err := device.PopErrorScope(); err != nil {
		t.Fatal(err)
	}

	if err := readback.Map(native.MapModeRead, 0, size); err != nil {
		t.Fatal(err)
	}
	data, err := readback.ReadMapped(0, size)
	readback.Unmap()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if got, want := binary.LittleEndian.Uint32(data[4*i:]), uint32(i)*4; got != want {
			t.Fatalf("data[%d] = %d, want %d", i, got, want)
		}
	}
	if err := device.Err(); err != nil {
		t.Fatal(err)
	}
}
