//go:build windows && amd64

package wgpu

import (
	"strings"
	"testing"

	"github.com/liuqi0826/wgpu/native"
)

// The vertex buffer holds four quads of 6 vertices, one per column, each
// tagged with its column; the column picks the color. vs_index derives the
// column from @builtin(vertex_index) instead.
const multiDrawWGSL = `
struct VOut {
	@builtin(position) pos: vec4f,
	@location(0) @interpolate(flat) col: u32,
}

@vertex
fn vs(@location(0) pos: vec2f, @location(1) col: u32) -> VOut {
	var out: VOut;
	out.pos = vec4f(pos, 0.0, 1.0);
	out.col = col;
	return out;
}

@vertex
fn vs_index(@builtin(vertex_index) vi: u32) -> VOut {
	var quad = array(vec2f(0.0, 0.0), vec2f(1.0, 0.0), vec2f(0.0, 1.0), vec2f(0.0, 1.0), vec2f(1.0, 0.0), vec2f(1.0, 1.0));
	let col = vi / 6u;
	let q = quad[vi % 6u];
	var out: VOut;
	out.pos = vec4f(-1.0 + (f32(col) + q.x) * 0.5, -1.0 + q.y * 2.0, 0.0, 1.0);
	out.col = col;
	return out;
}

@fragment
fn fs(in: VOut) -> @location(0) vec4f {
	var colors = array(vec4f(1.0, 0.0, 0.0, 1.0), vec4f(0.0, 1.0, 0.0, 1.0), vec4f(0.0, 0.0, 1.0, 1.0), vec4f(1.0, 1.0, 1.0, 1.0));
	return colors[in.col];
}
`

// columnQuads returns the vertex buffer for vs: pos.xy as float32, column as uint32.
func columnQuads() []byte {
	quad := [6][2]float32{{0, 0}, {1, 0}, {0, 1}, {0, 1}, {1, 0}, {1, 1}}
	var out []byte
	for c := range 4 {
		for _, q := range quad {
			out = append(out, f32Bytes(-1+(float32(c)+q[0])*0.5, -1+q[1]*2)...)
			out = append(out, u32Bytes(uint32(c))...)
		}
	}
	return out
}

var columnColors = [4][4]byte{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}, {255, 255, 255, 255}}

type multiDrawFixture struct {
	device   *Device
	pipeline *RenderPipeline
	indices  *Buffer
	vertices *Buffer
	tex      *Texture
	view     *TextureView
	count    bool // FeatureMultiDrawIndirectCount is enabled
}

const mdW, mdH = 8, 2

// newMultiDrawFixture requests a device with FeatureMultiDrawIndirectCount
// when the adapter has it.
func newMultiDrawFixture(t *testing.T) *multiDrawFixture {
	t.Helper()
	return newMultiDrawFixtureFrom(t, newTestAdapter(t), false)
}

// newMultiDrawFixtureFrom uses adapter; byIndex selects the vs_index pipeline.
func newMultiDrawFixtureFrom(t *testing.T, adapter *Adapter, byIndex bool) *multiDrawFixture {
	t.Helper()
	f := &multiDrawFixture{count: adapter.HasFeature(FeatureMultiDrawIndirectCount)}
	opts := &DeviceOptions{}
	if f.count {
		opts.RequiredFeatures = []native.FeatureName{FeatureMultiDrawIndirectCount}
	}
	device, err := adapter.RequestDeviceWith(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(device.Release)
	info, _ := device.AdapterInfo()
	t.Logf("%s: FeatureMultiDrawIndirectCount %v", info.Backend, f.count)
	f.device = device
	module, err := device.CreateShaderModuleWGSL("multidraw", multiDrawWGSL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(module.Release)
	vertex := VertexState{Module: module, EntryPoint: "vs", Buffers: []VertexBufferLayout{{
		ArrayStride: 12,
		Attributes: []VertexAttribute{
			{Format: native.VertexFormatFloat32x2, Offset: 0, ShaderLocation: 0},
			{Format: native.VertexFormatUint32, Offset: 8, ShaderLocation: 1},
		},
	}}}
	if byIndex {
		vertex = VertexState{Module: module, EntryPoint: "vs_index"}
	}
	f.pipeline, err = device.CreateRenderPipeline(&RenderPipelineDescriptor{
		Label:    "multidraw",
		Vertex:   vertex,
		Fragment: &FragmentState{Module: module, EntryPoint: "fs", Targets: []ColorTargetState{{Format: native.TextureFormatRGBA8Unorm}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.pipeline.Release)
	idx := make([]uint32, 24)
	for i := range idx {
		idx[i] = uint32(i % 6) // baseVertex selects the column
	}
	f.indices = f.buffer(t, native.BufferUsageIndex, u32Bytes(idx...))
	f.vertices = f.buffer(t, native.BufferUsageVertex, columnQuads())
	f.tex, f.view = newTarget(t, device, mdW, mdH, native.TextureFormatRGBA8Unorm)
	return f
}

func (f *multiDrawFixture) buffer(t *testing.T, usage native.BufferUsage, data []byte) *Buffer {
	t.Helper()
	buf, err := f.device.CreateBuffer(usage|native.BufferUsageCopyDst, uint64(len(data)), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(buf.Release)
	f.device.Queue().WriteBuffer(buf, 0, data)
	return buf
}

// drawRecords returns non-indexed records drawing the given columns.
func drawRecords(cols ...uint32) []byte {
	var v []uint32
	for _, c := range cols {
		v = append(v, 6, 1, 6*c, 0)
	}
	return u32Bytes(v...)
}

// indexedRecords returns indexed records drawing the given columns through baseVertex.
func indexedRecords(cols ...uint32) []byte {
	var v []uint32
	for _, c := range cols {
		v = append(v, 6, 1, 0, 6*c, 0)
	}
	return u32Bytes(v...)
}

// run records fn in a pass and returns the Finish error.
func (f *multiDrawFixture) run(t *testing.T, fn func(p *RenderPassEncoder)) error {
	t.Helper()
	enc, err := f.device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	pass, err := enc.BeginRenderPass(&RenderPassDescriptor{ColorAttachments: []RenderPassColorAttachment{{View: f.view}}})
	if err != nil {
		t.Fatal(err)
	}
	pass.SetPipeline(f.pipeline)
	pass.SetIndexBuffer(f.indices, native.IndexFormatUint32, 0, 0)
	pass.SetVertexBuffer(0, f.vertices, 0, 0)
	fn(pass)
	pass.End()
	pass.Release()
	cb, err := enc.Finish()
	if err != nil {
		return err
	}
	f.device.Queue().Submit(cb)
	cb.Release()
	return nil
}

// check renders fn and compares the four columns; drawn[i] false means clear.
func (f *multiDrawFixture) check(t *testing.T, fn func(p *RenderPassEncoder), drawn [4]bool) {
	t.Helper()
	f.device.PushErrorScope(native.ErrorFilterValidation)
	if err := f.run(t, fn); err != nil {
		t.Fatal(err)
	}
	if err := f.device.PopErrorScope(); err != nil {
		t.Fatal(err)
	}
	pixels := readRGBA8(t, f.device, f.tex, mdW, mdH)
	var got [4][]byte
	for c := range 4 {
		got[c] = texel(pixels, mdW, 2*c+1, 1)
	}
	t.Logf("columns %v", got)
	for c := range 4 {
		want := [4]byte{}
		if drawn[c] {
			want = columnColors[c]
		}
		wantTexel(t, pixels, mdW, 2*c+1, 1, want)
		wantTexel(t, pixels, mdW, 2*c, 0, want)
	}
}

func TestMultiDrawIndirect(t *testing.T) {
	f := newMultiDrawFixture(t)
	usage := native.BufferUsageIndirect

	t.Run("draw", func(t *testing.T) {
		// One padding record, then columns 0..3; draw 3 records from offset 16.
		buf := f.buffer(t, usage, append(drawRecords(3), drawRecords(0, 1, 2, 3)...))
		f.check(t, func(p *RenderPassEncoder) { p.MultiDrawIndirect(buf, 16, 3) }, [4]bool{true, true, true, false})
	})
	t.Run("draw count 0", func(t *testing.T) {
		// With indirect validation wgpu-core 29 would still draw record 0.
		buf := f.buffer(t, usage, drawRecords(0, 1, 2, 3))
		f.check(t, func(p *RenderPassEncoder) {
			p.MultiDrawIndirect(buf, 0, 0)
			p.MultiDrawIndexedIndirect(buf, 0, 0)
			p.MultiDrawIndirect(buf, 64, 0) // an empty range at the end is valid
		}, [4]bool{})
	})
	t.Run("indexed", func(t *testing.T) {
		buf := f.buffer(t, usage, indexedRecords(1, 3, 0))
		f.check(t, func(p *RenderPassEncoder) { p.MultiDrawIndexedIndirect(buf, 0, 2) }, [4]bool{false, true, false, true})
	})

	t.Run("count", func(t *testing.T) {
		if !f.count {
			t.Skip("adapter does not support FeatureMultiDrawIndirectCount")
		}
		buf := f.buffer(t, usage, drawRecords(0, 1, 2, 3))
		counts := f.buffer(t, usage, u32Bytes(5, 2))
		// count 2 read from offset 4, max 3.
		f.check(t, func(p *RenderPassEncoder) { p.MultiDrawIndirectCount(buf, 0, counts, 4, 3) }, [4]bool{true, true, false, false})
		// count 5 read from offset 0 is clamped to max 3.
		f.check(t, func(p *RenderPassEncoder) { p.MultiDrawIndirectCount(buf, 0, counts, 0, 3) }, [4]bool{true, true, true, false})
	})
	t.Run("indexed count", func(t *testing.T) {
		if !f.count {
			t.Skip("adapter does not support FeatureMultiDrawIndirectCount")
		}
		buf := f.buffer(t, usage, indexedRecords(3, 2, 1, 0))
		counts := f.buffer(t, usage, u32Bytes(0, 1, 9))
		f.check(t, func(p *RenderPassEncoder) { p.MultiDrawIndexedIndirectCount(buf, 0, counts, 4, 4) }, [4]bool{false, false, false, true})
		f.check(t, func(p *RenderPassEncoder) { p.MultiDrawIndexedIndirectCount(buf, 20, counts, 8, 2) }, [4]bool{false, true, true, false})
		f.check(t, func(p *RenderPassEncoder) { p.MultiDrawIndexedIndirectCount(buf, 0, counts, 0, 4) }, [4]bool{})
	})
}

// Misuse must surface as errors from Finish instead of aborting the process.
func TestMultiDrawIndirectMisuse(t *testing.T) {
	f := newMultiDrawFixture(t)
	records := f.buffer(t, native.BufferUsageIndirect, drawRecords(0, 1, 2, 3)) // 64 bytes
	indexed := f.buffer(t, native.BufferUsageIndirect, indexedRecords(0, 1))    // 40 bytes
	counts := f.buffer(t, native.BufferUsageIndirect, u32Bytes(1, 2))           // 8 bytes
	noUsage := f.buffer(t, native.BufferUsageVertex, drawRecords(0))
	released := f.buffer(t, native.BufferUsageIndirect, drawRecords(0))
	released.Release()

	cases := []struct {
		name string
		fn   func(p *RenderPassEncoder)
		want string
	}{
		{"nil buffer", func(p *RenderPassEncoder) { p.MultiDrawIndirect(nil, 0, 1) }, "nil or released indirect buffer"},
		{"released buffer", func(p *RenderPassEncoder) { p.MultiDrawIndexedIndirect(released, 0, 1) }, "nil or released indirect buffer"},
		{"misaligned offset", func(p *RenderPassEncoder) { p.MultiDrawIndirect(records, 2, 1) }, "not a multiple of 4"},
		{"overrun", func(p *RenderPassEncoder) { p.MultiDrawIndirect(records, 16, 4) }, "overrun"},
		{"indexed overrun", func(p *RenderPassEncoder) { p.MultiDrawIndexedIndirect(indexed, 4, 2) }, "overrun"},
		{"offset past end", func(p *RenderPassEncoder) { p.MultiDrawIndirect(records, 1<<63, 1) }, "overrun"},
		{"huge count", func(p *RenderPassEncoder) { p.MultiDrawIndirect(records, 0, 0xffffffff) }, "overrun"},
		{"missing indirect usage", func(p *RenderPassEncoder) { p.MultiDrawIndirect(noUsage, 0, 1) }, "INDIRECT"},
		{"count nil indirect buffer", func(p *RenderPassEncoder) { p.MultiDrawIndirectCount(nil, 0, counts, 0, 1) }, "nil or released indirect buffer"},
		{"count overrun", func(p *RenderPassEncoder) { p.MultiDrawIndexedIndirectCount(indexed, 0, counts, 0, 3) }, "overrun"},
	}
	if f.count {
		cases = append(cases, []struct {
			name string
			fn   func(p *RenderPassEncoder)
			want string
		}{
			{"nil count buffer", func(p *RenderPassEncoder) { p.MultiDrawIndirectCount(records, 0, nil, 0, 1) }, "nil or released count buffer"},
			{"released count buffer", func(p *RenderPassEncoder) { p.MultiDrawIndexedIndirectCount(indexed, 0, released, 0, 1) }, "nil or released count buffer"},
			{"misaligned count offset", func(p *RenderPassEncoder) { p.MultiDrawIndirectCount(records, 0, counts, 2, 1) }, "count buffer offset is not a multiple of 4"},
			{"count buffer overrun", func(p *RenderPassEncoder) { p.MultiDrawIndirectCount(records, 0, counts, 8, 1) }, "count buffer overrun"},
			{"count offset past end", func(p *RenderPassEncoder) { p.MultiDrawIndirectCount(records, 0, counts, 1<<63, 1) }, "count buffer overrun"},
			{"count buffer usage", func(p *RenderPassEncoder) { p.MultiDrawIndirectCount(records, 0, noUsage, 0, 1) }, "INDIRECT"},
		}...)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := f.run(t, tc.fn)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Finish error %v, want %q", err, tc.want)
			}
			t.Logf("%.200s", err)
		})
	}

	t.Run("released pass", func(t *testing.T) {
		enc, err := f.device.CreateCommandEncoder()
		if err != nil {
			t.Fatal(err)
		}
		pass, err := enc.BeginRenderPass(&RenderPassDescriptor{ColorAttachments: []RenderPassColorAttachment{{View: f.view}}})
		if err != nil {
			t.Fatal(err)
		}
		pass.End()
		pass.Release()
		pass.MultiDrawIndirect(records, 0, 1)
		pass.MultiDrawIndexedIndirectCount(indexed, 0, counts, 0, 1)
		if _, err := enc.Finish(); err == nil || !strings.Contains(err.Error(), "released pass") {
			t.Fatalf("Finish error %v", err)
		}
	})

	t.Run("count feature not enabled", func(t *testing.T) {
		plain := newTestDevice(t)
		_, view := newTarget(t, plain, 4, 4, native.TextureFormatRGBA8Unorm)
		buf, err := plain.CreateBuffer(native.BufferUsageIndirect, 64, false)
		if err != nil {
			t.Fatal(err)
		}
		defer buf.Release()
		enc, err := plain.CreateCommandEncoder()
		if err != nil {
			t.Fatal(err)
		}
		pass, err := enc.BeginRenderPass(&RenderPassDescriptor{ColorAttachments: []RenderPassColorAttachment{{View: view}}})
		if err != nil {
			t.Fatal(err)
		}
		pass.MultiDrawIndirectCount(buf, 0, buf, 60, 2)
		pass.End()
		pass.Release()
		if _, err := enc.Finish(); err == nil || !strings.Contains(err.Error(), "FeatureMultiDrawIndirectCount") {
			t.Fatalf("Finish error %v", err)
		}
	})
	if err := f.device.Err(); err != nil {
		t.Fatalf("misuse leaked an uncaptured error: %v", err)
	}
}

// TestMultiDrawVertexIndex checks @builtin(vertex_index) in indirect draws.
// On D3D12 it includes firstVertex/baseVertex only when the instance has
// InstanceFlagValidationIndirectCall, which rewrites the indirect arguments.
// Zero InstanceOptions.Flags now means InstanceFlagDefault, which includes it;
// ExplicitFlags with zero Flags creates an instance without it. The Count
// variants never include it on D3D12 because wgpu-hal 29 uses the plain
// command signature for them.
func TestMultiDrawVertexIndex(t *testing.T) {
	backends, err := BackendsFromEnv(native.InstanceBackendPrimary)
	if err != nil {
		t.Fatal(err)
	}
	all := [4]bool{true, true, true, true}
	firstOnly := [4]bool{true}
	for _, tc := range []struct {
		name string
		opts InstanceOptions
	}{
		{"zero flags", InstanceOptions{Backends: backends}},
		{"explicit no flags", InstanceOptions{Backends: backends, ExplicitFlags: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			instance, err := CreateInstance(&tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(instance.Release)
			adapter, err := instance.RequestAdapter(&native.RequestAdapterOptions{PowerPreference: native.PowerPreferenceHighPerformance})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(adapter.Release)
			f := newMultiDrawFixtureFrom(t, adapter, true)
			info, _ := f.device.AdapterInfo()
			d3d12 := info.Backend == native.BackendTypeD3D12
			records := f.buffer(t, native.BufferUsageIndirect, drawRecords(0, 1, 2, 3))
			indexed := f.buffer(t, native.BufferUsageIndirect, indexedRecords(0, 1, 2, 3))
			counts := f.buffer(t, native.BufferUsageIndirect, u32Bytes(4))

			// Without the flag D3D12 puts every draw in column 0.
			want := all
			if d3d12 && tc.opts.ExplicitFlags {
				want = firstOnly
			}
			t.Run("direct", func(t *testing.T) {
				f.check(t, func(p *RenderPassEncoder) { p.MultiDrawIndirect(records, 0, 4) }, want)
				f.check(t, func(p *RenderPassEncoder) { p.MultiDrawIndexedIndirect(indexed, 0, 4) }, want)
				for c := range uint32(4) {
					col := [4]bool{}
					col[c] = true
					if d3d12 && tc.opts.ExplicitFlags {
						col = firstOnly
					}
					f.check(t, func(p *RenderPassEncoder) { p.DrawIndirect(records, 16*uint64(c)) }, col)
					f.check(t, func(p *RenderPassEncoder) { p.DrawIndexedIndirect(indexed, 20*uint64(c)) }, col)
				}
			})
			t.Run("count", func(t *testing.T) {
				if !f.count {
					t.Skip("adapter does not support FeatureMultiDrawIndirectCount")
				}
				if d3d12 {
					f.check(t, func(p *RenderPassEncoder) { p.MultiDrawIndirectCount(records, 0, counts, 0, 4) }, firstOnly)
					f.check(t, func(p *RenderPassEncoder) { p.MultiDrawIndexedIndirectCount(indexed, 0, counts, 0, 4) }, firstOnly)
					t.Log("D3D12: vertex_index of Count draws lacks firstVertex (wgpu-hal 29 limitation), all draws land in column 0")
					return
				}
				f.check(t, func(p *RenderPassEncoder) { p.MultiDrawIndirectCount(records, 0, counts, 0, 4) }, all)
				f.check(t, func(p *RenderPassEncoder) { p.MultiDrawIndexedIndirectCount(indexed, 0, counts, 0, 4) }, all)
			})
		})
	}
}
