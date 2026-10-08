//go:build windows && amd64

package wgpu

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"wgpu/native"
)

func newTestDevice(t *testing.T) *Device {
	t.Helper()
	instance, err := CreateInstance(&InstanceOptions{Backends: native.InstanceBackendPrimary})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(instance.Release)
	adapter, err := instance.RequestAdapter(&native.RequestAdapterOptions{
		PowerPreference: native.PowerPreferenceHighPerformance,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adapter.Release)
	device, err := adapter.RequestDevice()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(device.Release)
	return device
}

func f32Bytes(values ...float32) []byte {
	out := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(out[4*i:], math.Float32bits(v))
	}
	return out
}

func submit(t *testing.T, d *Device, enc *CommandEncoder) {
	t.Helper()
	commands, err := enc.Finish()
	if err != nil {
		t.Fatal(err)
	}
	d.Queue().Submit(commands)
	commands.Release()
}

const triangleWGSL = `
@group(0) @binding(0) var<uniform> tint: vec4f;

struct VSOut {
	@builtin(position) pos: vec4f,
	@location(0) color: vec3f,
}

@vertex
fn vs_main(@location(0) pos: vec2f, @location(1) color: vec3f) -> VSOut {
	var out: VSOut;
	out.pos = vec4f(pos, 0.0, 1.0);
	out.color = color;
	return out;
}

@fragment
fn fs_main(in: VSOut) -> @location(0) vec4f {
	return vec4f(in.color, 1.0) * tint;
}
`

func TestOffscreenTriangle(t *testing.T) {
	device := newTestDevice(t)
	queue := device.Queue()
	const size = 64
	const rowBytes = size * 4 // 256, already aligned for buffer copies

	device.PushErrorScope(native.ErrorFilterValidation)

	target, err := device.CreateTexture(&TextureDescriptor{
		Label:  "target",
		Usage:  native.TextureUsageRenderAttachment | native.TextureUsageCopySrc,
		Width:  size,
		Height: size,
		Format: native.TextureFormatRGBA8Unorm,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Release()
	defer target.Destroy()
	if target.Width() != size || target.Height() != size || target.Format() != native.TextureFormatRGBA8Unorm {
		t.Fatalf("texture %dx%d %s", target.Width(), target.Height(), target.Format())
	}
	view, err := target.CreateView(&TextureViewDescriptor{Label: "target view", Dimension: native.TextureViewDimension2D})
	if err != nil {
		t.Fatal(err)
	}
	defer view.Release()

	depth, err := device.CreateTexture(&TextureDescriptor{
		Label:  "depth",
		Usage:  native.TextureUsageRenderAttachment,
		Width:  size,
		Height: size,
		Format: native.TextureFormatDepth32Float,
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

	sampler, err := device.CreateSampler(&SamplerDescriptor{Label: "linear", MagFilter: native.FilterModeLinear, MinFilter: native.FilterModeLinear})
	if err != nil {
		t.Fatal(err)
	}
	defer sampler.Release()

	module, err := device.CreateShaderModuleWGSL("triangle", triangleWGSL)
	if err != nil {
		t.Fatal(err)
	}
	defer module.Release()

	bgl, err := device.CreateBindGroupLayout(&BindGroupLayoutDescriptor{
		Label: "tint layout",
		Entries: []BindGroupLayoutEntry{{
			Binding:    0,
			Visibility: native.ShaderStageFragment,
			Buffer:     BufferBinding{Type: native.BufferBindingTypeUniform, MinBindingSize: 16},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bgl.Release()

	uniform, err := device.CreateBuffer(native.BufferUsageUniform|native.BufferUsageCopyDst, 16, false)
	if err != nil {
		t.Fatal(err)
	}
	defer uniform.Release()
	queue.WriteBuffer(uniform, 0, f32Bytes(1, 1, 1, 1))

	group, err := device.CreateBindGroup(&BindGroupDescriptor{
		Label:   "tint",
		Layout:  bgl,
		Entries: []BindGroupEntry{{Binding: 0, Buffer: uniform}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer group.Release()

	layout, err := device.CreatePipelineLayout(&PipelineLayoutDescriptor{
		Label:            "triangle layout",
		BindGroupLayouts: []*BindGroupLayout{bgl},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer layout.Release()

	pipeline, err := device.CreateRenderPipeline(&RenderPipelineDescriptor{
		Label:  "triangle",
		Layout: layout,
		Vertex: VertexState{
			Module:     module,
			EntryPoint: "vs_main",
			Buffers: []VertexBufferLayout{{
				ArrayStride: 5 * 4,
				Attributes: []VertexAttribute{
					{Format: native.VertexFormatFloat32x2, Offset: 0, ShaderLocation: 0},
					{Format: native.VertexFormatFloat32x3, Offset: 2 * 4, ShaderLocation: 1},
				},
			}},
		},
		Primitive: PrimitiveState{CullMode: native.CullModeBack},
		DepthStencil: &DepthStencilState{
			Format:            native.TextureFormatDepth32Float,
			DepthWriteEnabled: true,
			DepthCompare:      native.CompareFunctionLess,
		},
		Fragment: &FragmentState{
			Module:     module,
			EntryPoint: "fs_main",
			Targets:    []ColorTargetState{{Format: native.TextureFormatRGBA8Unorm, Blend: &BlendAlpha}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pipeline.Release()

	vertices, err := device.CreateBuffer(native.BufferUsageVertex|native.BufferUsageCopyDst, 3*5*4, false)
	if err != nil {
		t.Fatal(err)
	}
	defer vertices.Release()
	queue.WriteBuffer(vertices, 0, f32Bytes(
		-0.5, -0.5, 1, 0, 0,
		0.5, -0.5, 0, 1, 0,
		0.0, 0.5, 0, 0, 1,
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
		Label: "offscreen",
		ColorAttachments: []RenderPassColorAttachment{{
			View:       view,
			LoadOp:     native.LoadOpClear,
			StoreOp:    native.StoreOpStore,
			ClearValue: native.Color{R: 0.2, G: 0.4, B: 0.6, A: 1},
		}},
		DepthStencilAttachment: &RenderPassDepthStencilAttachment{
			View:            depthView,
			DepthClearValue: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	pass.SetPipeline(pipeline)
	pass.SetBindGroup(0, group)
	pass.SetVertexBuffer(0, vertices, 0, 0)
	pass.SetViewport(0, 0, size, size, 0, 1)
	pass.SetScissorRect(0, 0, size, size)
	pass.Draw(3, 1, 0, 0)
	pass.End()
	pass.Release()
	enc.CopyTextureToBuffer(
		TexelCopyTexture{Texture: target},
		TexelCopyBuffer{Buffer: readback, Layout: native.TexelCopyBufferLayout{BytesPerRow: rowBytes, RowsPerImage: size}},
		native.Extent3D{Width: size, Height: size, DepthOrArrayLayers: 1},
	)
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

	clear := []byte{51, 102, 153, 255}
	for _, p := range [][2]int{{0, 0}, {size - 1, 0}, {0, size - 1}, {size - 1, size - 1}} {
		if got := pixel(p[0], p[1]); !near(got, clear, 1) {
			t.Errorf("corner %v = %v, want clear %v", p, got, clear)
		}
	}
	// Centroid (0, -1/6) in NDC is pixel (32, 37); each color weight is ~1/3.
	center := pixel(32, 37)
	t.Logf("center pixel %v", center)
	for i, c := range center[:3] {
		if c < 60 || c > 110 {
			t.Errorf("center channel %d = %d, want about 85", i, c)
		}
	}
	if center[3] != 255 {
		t.Errorf("center alpha = %d", center[3])
	}
	// Near each corner of the triangle its vertex color dominates.
	for _, c := range []struct {
		x, y, ch int
	}{{17, 46, 0}, {46, 46, 1}, {32, 18, 2}} {
		got := pixel(c.x, c.y)
		if got[c.ch] < 180 {
			t.Errorf("pixel (%d,%d) = %v, channel %d should dominate", c.x, c.y, got, c.ch)
		}
	}
	if err := device.Err(); err != nil {
		t.Fatal(err)
	}
}

func near(got, want []byte, tol int) bool {
	for i := range want {
		d := int(got[i]) - int(want[i])
		if d < -tol || d > tol {
			return false
		}
	}
	return true
}

func TestWriteTextureRoundTrip(t *testing.T) {
	device := newTestDevice(t)
	const w, h = 4, 4
	tex, err := device.CreateTexture(&TextureDescriptor{
		Usage:  native.TextureUsageCopyDst | native.TextureUsageCopySrc,
		Width:  w,
		Height: h,
		Format: native.TextureFormatRGBA8Unorm,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tex.Release()
	src := make([]byte, w*h*4)
	for i := range src {
		src[i] = byte(i * 3)
	}
	device.PushErrorScope(native.ErrorFilterValidation)
	device.Queue().WriteTexture(TexelCopyTexture{Texture: tex}, src,
		native.TexelCopyBufferLayout{BytesPerRow: w * 4, RowsPerImage: h},
		native.Extent3D{Width: w, Height: h, DepthOrArrayLayers: 1})

	readback, err := device.CreateBuffer(native.BufferUsageMapRead|native.BufferUsageCopyDst, 256*h, false)
	if err != nil {
		t.Fatal(err)
	}
	defer readback.Release()
	enc, err := device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	enc.CopyTextureToBuffer(TexelCopyTexture{Texture: tex},
		TexelCopyBuffer{Buffer: readback, Layout: native.TexelCopyBufferLayout{BytesPerRow: 256}},
		native.Extent3D{Width: w, Height: h, DepthOrArrayLayers: 1})
	submit(t, device, enc)
	if err := device.PopErrorScope(); err != nil {
		t.Fatal(err)
	}
	if err := readback.Map(native.MapModeRead, 0, 256*h); err != nil {
		t.Fatal(err)
	}
	data, err := readback.ReadMapped(0, 256*h)
	readback.Unmap()
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < h; y++ {
		got := data[y*256 : y*256+w*4]
		want := src[y*w*4 : (y+1)*w*4]
		if !bytes.Equal(got, want) {
			t.Fatalf("row %d = % x, want % x", y, got, want)
		}
	}
}

func TestPipelineValidationError(t *testing.T) {
	device := newTestDevice(t)
	module, err := device.CreateShaderModuleWGSL("triangle", triangleWGSL)
	if err != nil {
		t.Fatal(err)
	}
	defer module.Release()
	device.PushErrorScope(native.ErrorFilterValidation)
	// The shader needs a vertex buffer at location 0; omitting it must fail.
	pipeline, err := device.CreateRenderPipeline(&RenderPipelineDescriptor{
		Vertex:   VertexState{Module: module},
		Fragment: &FragmentState{Module: module, Targets: []ColorTargetState{{Format: native.TextureFormatRGBA8Unorm}}},
	})
	if err == nil {
		defer pipeline.Release()
	}
	if err := device.PopErrorScope(); err == nil {
		t.Fatal("pipeline without vertex buffers was accepted")
	} else {
		t.Logf("validation: %v", err)
	}
}
