//go:build windows && amd64

package wgpu

import (
	"encoding/binary"
	"strings"
	"testing"
	"unsafe"

	"github.com/liuqi0826/wgpu/native"
)

// newImmediatesDevice returns a device with FeatureImmediates and the
// adapter's MaxImmediateSize, or skips the test.
func newImmediatesDevice(t *testing.T) *Device {
	t.Helper()
	adapter := newTestAdapter(t)
	if !adapter.HasFeature(FeatureImmediates) {
		t.Skip("adapter does not support FeatureImmediates")
	}
	best, err := adapter.Limits()
	if err != nil {
		t.Fatal(err)
	}
	limits := UndefinedLimits()
	limits.MaxImmediateSize = best.MaxImmediateSize
	device, err := adapter.RequestDeviceWith(&DeviceOptions{
		RequiredFeatures: []native.FeatureName{FeatureImmediates},
		RequiredLimits:   &limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(device.Release)
	got, err := device.Limits()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("maxImmediateSize %d", got.MaxImmediateSize)
	if got.MaxImmediateSize < 32 {
		t.Skipf("maxImmediateSize %d is below 32", got.MaxImmediateSize)
	}
	return device
}

const immRenderWGSL = `
struct Imm {
	color: vec4f,
	scale: vec4f,
}
var<immediate> imm: Imm;

@vertex
fn vs(@builtin(vertex_index) i: u32) -> @builtin(position) vec4f {
	let p = array(vec2f(-1.0, -1.0), vec2f(3.0, -1.0), vec2f(-1.0, 3.0));
	return vec4f(p[i], 0.0, 1.0);
}

@fragment
fn fs() -> @location(0) vec4f {
	return imm.color * imm.scale;
}
`

const immComputeWGSL = `
struct Imm {
	index: u32,
	a: u32,
	b: u32,
	c: u32,
}
var<immediate> imm: Imm;
@group(0) @binding(0) var<storage, read_write> out: array<u32>;

@compute @workgroup_size(1)
fn main() {
	out[imm.index] = imm.a * imm.b + imm.c;
}
`

func u32Bytes(values ...uint32) []byte {
	out := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(out[4*i:], v)
	}
	return out
}

// immRenderPipeline builds a fullscreen-triangle pipeline whose layout has 32
// bytes of immediate data: color at 0 and scale at 16.
func immRenderPipeline(t *testing.T, device *Device) *RenderPipeline {
	t.Helper()
	module, err := device.CreateShaderModuleWGSL("immediates", immRenderWGSL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(module.Release)
	layout, err := device.CreatePipelineLayout(&PipelineLayoutDescriptor{Label: "immediates", ImmediateSize: 32})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(layout.Release)
	pipeline, err := device.CreateRenderPipeline(&RenderPipelineDescriptor{
		Label:    "immediates",
		Layout:   layout,
		Vertex:   VertexState{Module: module, EntryPoint: "vs"},
		Fragment: &FragmentState{Module: module, EntryPoint: "fs", Targets: []ColorTargetState{{Format: native.TextureFormatRGBA8Unorm}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pipeline.Release)
	return pipeline
}

func wantTexel(t *testing.T, data []byte, w, x, y int, want [4]byte) {
	t.Helper()
	got := texel(data, w, x, y)
	for i := range want {
		if d := int(got[i]) - int(want[i]); d < -1 || d > 1 {
			t.Fatalf("texel (%d,%d) = %v, want %v", x, y, got, want)
		}
	}
}

func TestRenderPassSetImmediates(t *testing.T) {
	device := newImmediatesDevice(t)
	pipeline := immRenderPipeline(t, device)
	const w, h = 8, 4
	tex, view := newTarget(t, device, w, h, native.TextureFormatRGBA8Unorm)

	device.PushErrorScope(native.ErrorFilterValidation)
	enc, err := device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	pass, err := enc.BeginRenderPass(&RenderPassDescriptor{ColorAttachments: []RenderPassColorAttachment{{View: view}}})
	if err != nil {
		t.Fatal(err)
	}
	pass.SetPipeline(pipeline)
	// Left half: color yellow, scale 1.
	pass.SetImmediates(0, f32Bytes(1, 1, 0, 1, 1, 1, 1, 1))
	pass.SetViewport(0, 0, w/2, h, 0, 1)
	pass.Draw(3, 1, 0, 0)
	// Right half: only the scale at offset 16 changes, so yellow * (0,1,1,1) is green.
	pass.SetImmediates(16, f32Bytes(0, 1, 1, 1))
	pass.SetImmediates(0, nil) // empty data does nothing
	pass.SetViewport(w/2, 0, w/2, h, 0, 1)
	pass.Draw(3, 1, 0, 0)
	pass.End()
	pass.Release()
	submit(t, device, enc)
	if err := device.PopErrorScope(); err != nil {
		t.Fatal(err)
	}
	data := readRGBA8(t, device, tex, w, h)
	t.Logf("left %v right %v", texel(data, w, 1, 1), texel(data, w, 6, 2))
	wantTexel(t, data, w, 1, 1, [4]byte{255, 255, 0, 255})
	wantTexel(t, data, w, 6, 2, [4]byte{0, 255, 0, 255})
}

func TestComputePassSetImmediates(t *testing.T) {
	device := newImmediatesDevice(t)
	module, err := device.CreateShaderModuleWGSL("immediates", immComputeWGSL)
	if err != nil {
		t.Fatal(err)
	}
	defer module.Release()
	bgl, err := device.CreateBindGroupLayout(&BindGroupLayoutDescriptor{Entries: []BindGroupLayoutEntry{{
		Binding:    0,
		Visibility: native.ShaderStageCompute,
		Buffer:     BufferBinding{Type: native.BufferBindingTypeStorage},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	defer bgl.Release()
	layout, err := device.CreatePipelineLayout(&PipelineLayoutDescriptor{BindGroupLayouts: []*BindGroupLayout{bgl}, ImmediateSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	defer layout.Release()
	pipeline, err := device.CreateComputePipeline(&ComputePipelineDescriptor{Layout: layout, Compute: ComputeState{Module: module}})
	if err != nil {
		t.Fatal(err)
	}
	defer pipeline.Release()
	const size = 16
	storage, err := device.CreateBuffer(native.BufferUsageStorage|native.BufferUsageCopySrc, size, false)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Release()
	readback, err := device.CreateBuffer(native.BufferUsageMapRead|native.BufferUsageCopyDst, size, false)
	if err != nil {
		t.Fatal(err)
	}
	defer readback.Release()
	group, err := device.CreateBindGroup(&BindGroupDescriptor{Layout: bgl, Entries: []BindGroupEntry{{Binding: 0, Buffer: storage}}})
	if err != nil {
		t.Fatal(err)
	}
	defer group.Release()

	device.PushErrorScope(native.ErrorFilterValidation)
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
	pass.SetImmediates(0, u32Bytes(0, 6, 7, 1)) // out[0] = 43
	pass.DispatchWorkgroups(1, 1, 1)
	pass.SetImmediates(0, u32Bytes(1, 100, 3, 5)) // out[1] = 305
	pass.DispatchWorkgroups(1, 1, 1)
	pass.SetImmediates(0, u32Bytes(3)) // only index changes: out[3] = 305
	pass.DispatchWorkgroups(1, 1, 1)
	pass.SetImmediates(8, u32Bytes(2, 9)) // b and c change: out[3] = 100*2+9 = 209
	pass.DispatchWorkgroups(1, 1, 1)
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
	got := []uint32{binary.LittleEndian.Uint32(data[0:]), binary.LittleEndian.Uint32(data[4:]), binary.LittleEndian.Uint32(data[8:]), binary.LittleEndian.Uint32(data[12:])}
	t.Logf("storage %v", got)
	if want := []uint32{43, 305, 0, 209}; got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Fatalf("storage %v, want %v", got, want)
	}
}

func TestRenderBundleSetImmediates(t *testing.T) {
	device := newImmediatesDevice(t)
	pipeline := immRenderPipeline(t, device)
	const w, h = 4, 4
	tex, view := newTarget(t, device, w, h, native.TextureFormatRGBA8Unorm)
	be, err := device.CreateRenderBundleEncoder(&RenderBundleEncoderDescriptor{ColorFormats: []native.TextureFormat{native.TextureFormatRGBA8Unorm}})
	if err != nil {
		t.Fatal(err)
	}
	defer be.Release()
	be.SetPipeline(pipeline)
	data := f32Bytes(0, 0, 1, 1, 1, 1, 1, 1) // blue
	be.SetImmediates(0, data)
	data[8] = 0xff // the bundle keeps its own copy
	be.SetImmediates(16, f32Bytes(1, 1, 1, 1))
	be.Draw(3, 1, 0, 0)
	bundle, err := be.Finish()
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Release()

	device.PushErrorScope(native.ErrorFilterValidation)
	enc, err := device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	pass, err := enc.BeginRenderPass(&RenderPassDescriptor{ColorAttachments: []RenderPassColorAttachment{{View: view}}})
	if err != nil {
		t.Fatal(err)
	}
	pass.ExecuteBundles(bundle)
	pass.End()
	pass.Release()
	submit(t, device, enc)
	if err := device.PopErrorScope(); err != nil {
		t.Fatal(err)
	}
	pixels := readRGBA8(t, device, tex, w, h)
	t.Logf("bundle texel %v", texel(pixels, w, 2, 2))
	wantTexel(t, pixels, w, 2, 2, [4]byte{0, 0, 255, 255})
}

// hugeSlice has a length of 4 GiB + 16 without backing memory. SetImmediates
// must reject it before reading it; wgpu-native would truncate it to 16.
func hugeSlice() []byte {
	var small [16]byte
	return unsafe.Slice(&small[0], 1<<32+16)
}

// Misuse must surface as errors instead of aborting the process.
func TestSetImmediatesMisuse(t *testing.T) {
	device := newImmediatesDevice(t)
	pipeline := immRenderPipeline(t, device)
	_, view := newTarget(t, device, 4, 4, native.TextureFormatRGBA8Unorm)

	renderCase := func(t *testing.T, dev *Device, fn func(p *RenderPassEncoder)) error {
		t.Helper()
		enc, err := dev.CreateCommandEncoder()
		if err != nil {
			t.Fatal(err)
		}
		pass, err := enc.BeginRenderPass(&RenderPassDescriptor{ColorAttachments: []RenderPassColorAttachment{{View: view}}})
		if err != nil {
			t.Fatal(err)
		}
		fn(pass)
		pass.End()
		pass.Release()
		cb, err := enc.Finish()
		if cb != nil {
			cb.Release()
		}
		return err
	}
	cases := []struct {
		name string
		fn   func(p *RenderPassEncoder)
		want string
	}{
		{"misaligned offset", func(p *RenderPassEncoder) { p.SetPipeline(pipeline); p.SetImmediates(2, make([]byte, 4)) }, "offset is not a multiple of 4"},
		{"size 3", func(p *RenderPassEncoder) { p.SetPipeline(pipeline); p.SetImmediates(0, make([]byte, 3)) }, "data size is not a multiple of 4"},
		{"4 GiB", func(p *RenderPassEncoder) { p.SetPipeline(pipeline); p.SetImmediates(0, hugeSlice()) }, "32-bit"},
		{"offset+size overflow", func(p *RenderPassEncoder) { p.SetPipeline(pipeline); p.SetImmediates(0xfffffffc, make([]byte, 8)) }, "exceeds 4 GiB"},
		{"beyond layout", func(p *RenderPassEncoder) { p.SetPipeline(pipeline); p.SetImmediates(16, make([]byte, 32)) }, ""},
		{"no pipeline", func(p *RenderPassEncoder) { p.SetImmediates(0, make([]byte, 16)) }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := renderCase(t, device, tc.fn)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Finish error %v, want %q", err, tc.want)
			}
			t.Logf("%.160s", err)
		})
	}

	t.Run("released pass", func(t *testing.T) {
		enc, err := device.CreateCommandEncoder()
		if err != nil {
			t.Fatal(err)
		}
		pass, err := enc.BeginRenderPass(&RenderPassDescriptor{ColorAttachments: []RenderPassColorAttachment{{View: view}}})
		if err != nil {
			t.Fatal(err)
		}
		pass.End()
		pass.Release()
		pass.SetImmediates(0, make([]byte, 4))
		cpass, err := enc.BeginComputePass(nil)
		if err != nil {
			t.Fatal(err)
		}
		cpass.End()
		cpass.Release()
		cpass.SetImmediates(0, make([]byte, 4))
		if _, err := enc.Finish(); err == nil || !strings.Contains(err.Error(), "released pass") {
			t.Fatalf("Finish error %v", err)
		}
	})

	t.Run("compute misaligned", func(t *testing.T) {
		enc, err := device.CreateCommandEncoder()
		if err != nil {
			t.Fatal(err)
		}
		pass, err := enc.BeginComputePass(nil)
		if err != nil {
			t.Fatal(err)
		}
		pass.SetImmediates(4, make([]byte, 6))
		pass.End()
		pass.Release()
		if _, err := enc.Finish(); err == nil || !strings.Contains(err.Error(), "data size is not a multiple of 4") {
			t.Fatalf("Finish error %v", err)
		}
	})

	t.Run("feature not enabled", func(t *testing.T) {
		plain := newTestDevice(t)
		_, plainView := newTarget(t, plain, 4, 4, native.TextureFormatRGBA8Unorm)
		enc, err := plain.CreateCommandEncoder()
		if err != nil {
			t.Fatal(err)
		}
		pass, err := enc.BeginRenderPass(&RenderPassDescriptor{ColorAttachments: []RenderPassColorAttachment{{View: plainView}}})
		if err != nil {
			t.Fatal(err)
		}
		pass.SetImmediates(0, make([]byte, 16))
		pass.End()
		pass.Release()
		if _, err := enc.Finish(); err == nil || !strings.Contains(err.Error(), "FeatureImmediates") {
			t.Fatalf("Finish error %v", err)
		}
		be, err := plain.CreateRenderBundleEncoder(&RenderBundleEncoderDescriptor{ColorFormats: []native.TextureFormat{native.TextureFormatRGBA8Unorm}})
		if err != nil {
			t.Fatal(err)
		}
		defer be.Release()
		be.SetImmediates(0, make([]byte, 16))
		if _, err := be.Finish(); err == nil || !strings.Contains(err.Error(), "FeatureImmediates") {
			t.Fatalf("bundle Finish error %v", err)
		}
	})

	bundleCases := []struct {
		name string
		fn   func(e *RenderBundleEncoder)
		want string
	}{
		// wgpu-core asserts alignment in render bundles, which would abort.
		{"misaligned offset", func(e *RenderBundleEncoder) {
			e.SetPipeline(pipeline)
			e.SetImmediates(1, make([]byte, 4))
			e.Draw(3, 1, 0, 0)
		}, "offset is not a multiple of 4"},
		{"misaligned size", func(e *RenderBundleEncoder) {
			e.SetPipeline(pipeline)
			e.SetImmediates(0, make([]byte, 5))
			e.Draw(3, 1, 0, 0)
		}, "data size is not a multiple of 4"},
		{"4 GiB", func(e *RenderBundleEncoder) {
			e.SetPipeline(pipeline)
			e.SetImmediates(0, hugeSlice())
			e.Draw(3, 1, 0, 0)
		}, "32-bit"},
		// Bundle finish errors abort, so the validation pass must catch these.
		{"beyond layout", func(e *RenderBundleEncoder) {
			e.SetPipeline(pipeline)
			e.SetImmediates(28, make([]byte, 8))
			e.Draw(3, 1, 0, 0)
		}, "validation"},
		{"no pipeline", func(e *RenderBundleEncoder) { e.SetImmediates(0, make([]byte, 16)) }, "validation"},
	}
	for _, tc := range bundleCases {
		t.Run("bundle "+tc.name, func(t *testing.T) {
			be, err := device.CreateRenderBundleEncoder(&RenderBundleEncoderDescriptor{ColorFormats: []native.TextureFormat{native.TextureFormatRGBA8Unorm}})
			if err != nil {
				t.Fatal(err)
			}
			defer be.Release()
			tc.fn(be)
			b, err := be.Finish()
			if b != nil {
				b.Release()
			}
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
				t.Fatalf("Finish error %v, want %q", err, tc.want)
			}
			t.Logf("%.160s", err)
		})
	}
	if err := device.Err(); err != nil {
		t.Fatalf("misuse leaked an uncaptured error: %v", err)
	}
}
