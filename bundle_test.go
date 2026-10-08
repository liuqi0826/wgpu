//go:build windows && amd64

package wgpu

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/liuqi0826/wgpu/native"
)

const bundleWGSL = `
struct Params {
	color: vec4f,
	offset: vec2f,
}
@group(0) @binding(0) var<uniform> params: Params;

@vertex
fn vs_main(@location(0) pos: vec2f) -> @builtin(position) vec4f {
	return vec4f(pos + params.offset, 0.5, 1.0);
}

@fragment
fn fs_main() -> @location(0) vec4f {
	return params.color;
}
`

type bundleScene struct {
	pipeline *RenderPipeline
	groups   [3]*BindGroup
	vertices *Buffer
	indices  *Buffer
	indirect *Buffer
}

// newBundleScene draws one quad covering the top-left quadrant. Each bind
// group moves it to another quadrant with its own color: red stays top-left,
// green goes top-right and blue goes bottom-left.
func newBundleScene(t *testing.T, device *Device, format native.TextureFormat) *bundleScene {
	t.Helper()
	queue := device.Queue()
	module, err := device.CreateShaderModuleWGSL("bundle", bundleWGSL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(module.Release)
	s := &bundleScene{}
	s.pipeline, err = device.CreateRenderPipeline(&RenderPipelineDescriptor{
		Vertex: VertexState{Module: module, Buffers: []VertexBufferLayout{{
			ArrayStride: 8,
			Attributes:  []VertexAttribute{{Format: native.VertexFormatFloat32x2, ShaderLocation: 0}},
		}}},
		Fragment: &FragmentState{Module: module, Targets: []ColorTargetState{{Format: format}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.pipeline.Release)
	layout, err := s.pipeline.BindGroupLayout(0)
	if err != nil {
		t.Fatal(err)
	}
	defer layout.Release()
	uniforms, err := device.CreateBuffer(native.BufferUsageUniform|native.BufferUsageCopyDst, 3*256, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(uniforms.Release)
	params := [][]float32{
		{1, 0, 0, 1, 0, 0},
		{0, 1, 0, 1, 1, 0},
		{0, 0, 1, 1, 0, -1},
	}
	for i, p := range params {
		queue.WriteBuffer(uniforms, uint64(i*256), f32Bytes(append(p, 0, 0)...))
		s.groups[i], err = device.CreateBindGroup(&BindGroupDescriptor{
			Layout:  layout,
			Entries: []BindGroupEntry{{Binding: 0, Buffer: uniforms, Offset: uint64(i * 256), Size: 32}},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.groups[i].Release)
	}
	// Four indexed corners followed by the same quad as six plain vertices.
	s.vertices, err = device.CreateBuffer(native.BufferUsageVertex|native.BufferUsageCopyDst, 10*8, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.vertices.Release)
	queue.WriteBuffer(s.vertices, 0, f32Bytes(
		-1, 0, 0, 0, -1, 1, 0, 1,
		-1, 0, 0, 0, 0, 1, -1, 0, 0, 1, -1, 1,
	))
	s.indices, err = device.CreateBuffer(native.BufferUsageIndex|native.BufferUsageCopyDst, 12, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.indices.Release)
	idx := make([]byte, 12)
	for i, v := range []uint16{0, 1, 2, 2, 1, 3} {
		binary.LittleEndian.PutUint16(idx[2*i:], v)
	}
	queue.WriteBuffer(s.indices, 0, idx)
	// DrawIndexedIndirect arguments: indexCount, instanceCount, firstIndex, baseVertex, firstInstance.
	s.indirect, err = device.CreateBuffer(native.BufferUsageIndirect|native.BufferUsageCopyDst, 20, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.indirect.Release)
	args := make([]byte, 20)
	binary.LittleEndian.PutUint32(args[0:], 6)
	binary.LittleEndian.PutUint32(args[4:], 1)
	queue.WriteBuffer(s.indirect, 0, args)
	return s
}

func TestRenderBundles(t *testing.T) {
	device := newTestDevice(t)
	const size = 64
	format := native.TextureFormatRGBA8Unorm
	target, view := newTarget(t, device, size, size, format)
	s := newBundleScene(t, device, format)

	record := func(fn func(b *RenderBundleEncoder)) *RenderBundle {
		t.Helper()
		be, err := device.CreateRenderBundleEncoder(&RenderBundleEncoderDescriptor{ColorFormats: []native.TextureFormat{format}})
		if err != nil {
			t.Fatal(err)
		}
		defer be.Release()
		be.SetPipeline(s.pipeline)
		fn(be)
		bundle, err := be.Finish()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(bundle.Release)
		return bundle
	}
	red := record(func(b *RenderBundleEncoder) {
		b.SetBindGroup(0, s.groups[0])
		b.SetVertexBuffer(0, s.vertices, 0, 0)
		b.SetIndexBuffer(s.indices, native.IndexFormatUint16, 0, 0)
		b.DrawIndexed(6, 1, 0, 0, 0)
	})
	green := record(func(b *RenderBundleEncoder) {
		b.SetBindGroup(0, s.groups[1])
		b.SetVertexBuffer(0, s.vertices, 4*8, 0)
		b.Draw(6, 1, 0, 0)
	})
	blue := record(func(b *RenderBundleEncoder) {
		b.SetBindGroup(0, s.groups[2])
		b.SetVertexBuffer(0, s.vertices, 0, 0)
		b.SetIndexBuffer(s.indices, native.IndexFormatUint16, 0, 0)
		b.DrawIndexedIndirect(s.indirect, 0)
	})

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
	pass.ExecuteBundles(red, green)
	pass.ExecuteBundles(blue)
	pass.End()
	pass.Release()
	submit(t, device, enc)

	data := readRGBA8(t, device, target, size, size)
	for _, c := range []struct {
		x, y int
		want []byte
	}{
		{16, 16, []byte{255, 0, 0, 255}},
		{48, 16, []byte{0, 255, 0, 255}},
		{16, 48, []byte{0, 0, 255, 255}},
		{48, 48, []byte{0, 0, 0, 255}},
		{1, 1, []byte{255, 0, 0, 255}},
		{62, 1, []byte{0, 255, 0, 255}},
		{1, 62, []byte{0, 0, 255, 255}},
		{62, 62, []byte{0, 0, 0, 255}},
	} {
		if got := texel(data, size, c.x, c.y); !near(got, c.want, 1) {
			t.Errorf("pixel (%d,%d) = %v, want %v", c.x, c.y, got, c.want)
		}
	}
	if err := device.Err(); err != nil {
		t.Fatal(err)
	}
}

// Each of these bundles would make wgpu-native abort the process in
// wgpuRenderBundleEncoderFinish; the binding must return an error instead.
func TestRenderBundleValidation(t *testing.T) {
	device := newTestDevice(t)
	format := native.TextureFormatRGBA8Unorm
	s := newBundleScene(t, device, format)
	cases := []struct {
		name   string
		desc   RenderBundleEncoderDescriptor
		record func(b *RenderBundleEncoder)
		want   string
	}{
		{"no pipeline", RenderBundleEncoderDescriptor{ColorFormats: []native.TextureFormat{format}},
			func(b *RenderBundleEncoder) { b.Draw(3, 1, 0, 0) }, "pipeline"},
		{"format mismatch", RenderBundleEncoderDescriptor{ColorFormats: []native.TextureFormat{native.TextureFormatBGRA8Unorm}},
			func(b *RenderBundleEncoder) { b.SetPipeline(s.pipeline) }, "ncompatible"},
		{"no index buffer", RenderBundleEncoderDescriptor{ColorFormats: []native.TextureFormat{format}},
			func(b *RenderBundleEncoder) {
				b.SetPipeline(s.pipeline)
				b.SetBindGroup(0, s.groups[0])
				b.SetVertexBuffer(0, s.vertices, 0, 0)
				b.DrawIndexed(6, 1, 0, 0, 0)
			}, "ndex buffer"},
		{"vertex range", RenderBundleEncoderDescriptor{ColorFormats: []native.TextureFormat{format}},
			func(b *RenderBundleEncoder) {
				b.SetPipeline(s.pipeline)
				b.SetBindGroup(0, s.groups[0])
				b.SetVertexBuffer(0, s.vertices, 0, 0)
				b.Draw(100, 1, 0, 0)
			}, "ertex"},
		{"nil pipeline", RenderBundleEncoderDescriptor{ColorFormats: []native.TextureFormat{format}},
			func(b *RenderBundleEncoder) { b.SetPipeline(nil) }, "nil or released pipeline"},
	}
	for _, c := range cases {
		be, err := device.CreateRenderBundleEncoder(&c.desc)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		c.record(be)
		bundle, err := be.Finish()
		be.Release()
		if err == nil {
			bundle.Release()
			t.Errorf("%s: Finish succeeded", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q does not mention %q", c.name, err, c.want)
		}
		t.Logf("%s: %.110q", c.name, err.Error())
	}
	if _, err := device.CreateRenderBundleEncoder(&RenderBundleEncoderDescriptor{ColorFormats: make([]native.TextureFormat, 9)}); err == nil {
		t.Error("nine color formats accepted")
	}
	if _, err := device.CreateRenderBundleEncoder(&RenderBundleEncoderDescriptor{ColorFormats: []native.TextureFormat{format}, SampleCount: 3}); err == nil {
		t.Error("sample count 3 accepted")
	}

	// A released bundle is reported by CommandEncoder.Finish.
	be, err := device.CreateRenderBundleEncoder(&RenderBundleEncoderDescriptor{ColorFormats: []native.TextureFormat{format}})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := be.Finish()
	if err != nil {
		t.Fatal(err)
	}
	bundle.Release()
	_, view := newTarget(t, device, 4, 4, format)
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
	if cb, err := enc.Finish(); err == nil {
		cb.Release()
		t.Error("released bundle accepted")
	}
	if err := device.Err(); err != nil {
		t.Fatalf("device error: %v", err)
	}
}
