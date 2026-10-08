//go:build windows && amd64

package wgpu

import (
	"testing"

	"wgpu/native"
)

const sampleWGSL = `
@group(0) @binding(0) var tex: texture_2d<f32>;
@group(0) @binding(1) var samp: sampler;

struct VSOut {
	@builtin(position) pos: vec4f,
	@location(0) uv: vec2f,
	@location(1) solid: f32,
}

@vertex
fn vs_main(@location(0) pos: vec2f, @location(1) uv: vec2f, @builtin(instance_index) inst: u32) -> VSOut {
	var out: VSOut;
	// Instance 0 is the textured quad at depth 0.5; instance 1 is a solid green
	// quad at depth 0.7 that the depth test must hide.
	let z = select(0.5, 0.7, inst == 1u);
	out.pos = vec4f(pos, z, 1.0);
	out.uv = uv;
	out.solid = f32(inst);
	return out;
}

@fragment
fn fs_main(in: VSOut) -> @location(0) vec4f {
	let c = textureSample(tex, samp, in.uv);
	return mix(c, vec4f(0.0, 1.0, 0.0, 1.0), in.solid);
}
`

func TestOffscreenTexturedQuad(t *testing.T) {
	device := newTestDevice(t)
	queue := device.Queue()
	const size = 64
	const rowBytes = size * 4

	device.PushErrorScope(native.ErrorFilterValidation)
	// 2x2 checkerboard: red on the diagonal, blue elsewhere.
	checker, err := device.CreateTexture(&TextureDescriptor{
		Label:  "checker",
		Usage:  native.TextureUsageTextureBinding | native.TextureUsageCopyDst,
		Width:  2,
		Height: 2,
		Format: native.TextureFormatRGBA8Unorm,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer checker.Release()
	red := []byte{255, 0, 0, 255}
	blue := []byte{0, 0, 255, 255}
	texels := append(append(append(append([]byte{}, red...), blue...), blue...), red...)
	queue.WriteTexture(TexelCopyTexture{Texture: checker}, texels,
		native.TexelCopyBufferLayout{BytesPerRow: 8, RowsPerImage: 2},
		native.Extent3D{Width: 2, Height: 2, DepthOrArrayLayers: 1})
	checkerView, err := checker.CreateView(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer checkerView.Release()
	sampler, err := device.CreateSampler(nil) // nearest, clamp
	if err != nil {
		t.Fatal(err)
	}
	defer sampler.Release()

	target, err := device.CreateTexture(&TextureDescriptor{
		Usage:  native.TextureUsageRenderAttachment | native.TextureUsageCopySrc,
		Width:  size,
		Height: size,
		Format: native.TextureFormatRGBA8Unorm,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Release()
	targetView, err := target.CreateView(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer targetView.Release()
	depth, err := device.CreateTexture(&TextureDescriptor{
		Usage:  native.TextureUsageRenderAttachment,
		Width:  size,
		Height: size,
		Format: native.TextureFormatDepth24Plus,
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

	bgl, err := device.CreateBindGroupLayout(&BindGroupLayoutDescriptor{
		Entries: []BindGroupLayoutEntry{
			{Binding: 0, Visibility: native.ShaderStageFragment, Texture: TextureBinding{SampleType: native.TextureSampleTypeFloat}},
			{Binding: 1, Visibility: native.ShaderStageFragment, Sampler: SamplerBinding{Type: native.SamplerBindingTypeFiltering}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bgl.Release()
	group, err := device.CreateBindGroup(&BindGroupDescriptor{
		Layout: bgl,
		Entries: []BindGroupEntry{
			{Binding: 0, TextureView: checkerView},
			{Binding: 1, Sampler: sampler},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer group.Release()
	layout, err := device.CreatePipelineLayout(&PipelineLayoutDescriptor{BindGroupLayouts: []*BindGroupLayout{bgl}})
	if err != nil {
		t.Fatal(err)
	}
	defer layout.Release()
	module, err := device.CreateShaderModuleWGSL("sample", sampleWGSL)
	if err != nil {
		t.Fatal(err)
	}
	defer module.Release()
	pipeline, err := device.CreateRenderPipeline(&RenderPipelineDescriptor{
		Layout: layout,
		Vertex: VertexState{
			Module: module,
			Buffers: []VertexBufferLayout{{
				ArrayStride: 16,
				Attributes: []VertexAttribute{
					{Format: native.VertexFormatFloat32x2, Offset: 0, ShaderLocation: 0},
					{Format: native.VertexFormatFloat32x2, Offset: 8, ShaderLocation: 1},
				},
			}},
		},
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

	// Full-screen quad as a triangle list; uv (0,0) is the top-left texel.
	vertices, err := device.CreateBuffer(native.BufferUsageVertex|native.BufferUsageCopyDst, 6*16, false)
	if err != nil {
		t.Fatal(err)
	}
	defer vertices.Release()
	queue.WriteBuffer(vertices, 0, f32Bytes(
		-1, -1, 0, 1,
		1, -1, 1, 1,
		1, 1, 1, 0,
		-1, -1, 0, 1,
		1, 1, 1, 0,
		-1, 1, 0, 0,
	))
	readback, err := device.CreateBuffer(native.BufferUsageMapRead|native.BufferUsageCopyDst, rowBytes*size, false)
	if err != nil {
		t.Fatal(err)
	}
	defer readback.Release()

	enc, err := device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	pass, err := enc.BeginRenderPass(&RenderPassDescriptor{
		ColorAttachments:       []RenderPassColorAttachment{{View: targetView}},
		DepthStencilAttachment: &RenderPassDepthStencilAttachment{View: depthView, DepthClearValue: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	pass.SetPipeline(pipeline)
	pass.SetBindGroup(0, group)
	pass.SetVertexBuffer(0, vertices, 0, 0)
	pass.Draw(6, 2, 0, 0)
	pass.End()
	pass.Release()
	enc.CopyTextureToBuffer(TexelCopyTexture{Texture: target},
		TexelCopyBuffer{Buffer: readback, Layout: native.TexelCopyBufferLayout{BytesPerRow: rowBytes, RowsPerImage: size}},
		native.Extent3D{Width: size, Height: size, DepthOrArrayLayers: 1})
	submit(t, device, enc)
	if err := device.PopErrorScope(); err != nil {
		t.Fatal(err)
	}
	if err := readback.Map(native.MapModeRead, 0, rowBytes*size); err != nil {
		t.Fatal(err)
	}
	data, err := readback.ReadMapped(0, rowBytes*size)
	readback.Unmap()
	if err != nil {
		t.Fatal(err)
	}
	pixel := func(x, y int) []byte { return data[y*rowBytes+x*4 : y*rowBytes+x*4+4] }
	for _, c := range []struct {
		x, y int
		want []byte
	}{
		{16, 16, red}, {48, 16, blue}, {16, 48, blue}, {48, 48, red},
		{2, 2, red}, {61, 2, blue}, {2, 61, blue}, {61, 61, red},
	} {
		if got := pixel(c.x, c.y); !near(got, c.want, 1) {
			t.Errorf("pixel (%d,%d) = %v, want %v", c.x, c.y, got, c.want)
		}
	}
	if err := device.Err(); err != nil {
		t.Fatal(err)
	}
}
