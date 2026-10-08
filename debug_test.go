//go:build windows && amd64

package wgpu

import (
	"strings"
	"testing"

	"github.com/liuqi0826/wgpu/native"
)

// TestDebugMarkers nests debug groups and markers in every kind of encoder
// and checks that the commands inside them still run.
func TestDebugMarkers(t *testing.T) {
	device := newTestDevice(t)
	const size = 64
	format := native.TextureFormatRGBA8Unorm
	target, view := newTarget(t, device, size, size, format)
	s := newBundleScene(t, device, format)

	be, err := device.CreateRenderBundleEncoder(&RenderBundleEncoderDescriptor{ColorFormats: []native.TextureFormat{format}})
	if err != nil {
		t.Fatal(err)
	}
	defer be.Release()
	be.PushDebugGroup("bundle: red quad")
	be.InsertDebugMarker("bind")
	be.SetPipeline(s.pipeline)
	be.SetBindGroup(0, s.groups[0])
	be.PushDebugGroup("draw")
	be.SetVertexBuffer(0, s.vertices, 0, 0)
	be.SetIndexBuffer(s.indices, native.IndexFormatUint16, 0, 0)
	be.DrawIndexed(6, 1, 0, 0, 0)
	be.PopDebugGroup()
	be.PopDebugGroup()
	bundle, err := be.Finish()
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Release()

	enc, err := device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	enc.PushDebugGroup("frame")
	enc.InsertDebugMarker("start")
	enc.PushDebugGroup("compute")
	cpass, err := enc.BeginComputePass(&ComputePassDescriptor{Label: "debug compute"})
	if err != nil {
		t.Fatal(err)
	}
	cpass.PushDebugGroup("empty dispatch group")
	cpass.InsertDebugMarker("nothing to do")
	cpass.PopDebugGroup()
	cpass.End()
	cpass.Release()
	enc.PopDebugGroup()
	pass, err := enc.BeginRenderPass(&RenderPassDescriptor{
		Label:            "debug render",
		ColorAttachments: []RenderPassColorAttachment{{View: view, ClearValue: native.Color{A: 1}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	pass.PushDebugGroup("bundles")
	pass.ExecuteBundles(bundle)
	pass.PopDebugGroup()
	pass.PushDebugGroup("direct green quad")
	pass.InsertDebugMarker("")
	pass.SetPipeline(s.pipeline)
	pass.SetBindGroup(0, s.groups[1])
	pass.PushDebugGroup("nested")
	pass.SetVertexBuffer(0, s.vertices, 4*8, native.WholeSize)
	pass.Draw(6, 1, 0, 0)
	pass.PopDebugGroup()
	pass.PopDebugGroup()
	pass.End()
	pass.Release()
	enc.InsertDebugMarker("end")
	enc.PopDebugGroup()
	submit(t, device, enc)

	data := readRGBA8(t, device, target, size, size)
	for _, c := range []struct {
		x, y int
		want []byte
	}{
		{16, 16, []byte{255, 0, 0, 255}},
		{48, 16, []byte{0, 255, 0, 255}},
		{16, 48, []byte{0, 0, 0, 255}},
	} {
		if got := texel(data, size, c.x, c.y); !near(got, c.want, 1) {
			t.Errorf("pixel (%d,%d) = %v, want %v", c.x, c.y, got, c.want)
		}
	}
	if err := device.Err(); err != nil {
		t.Fatal(err)
	}
}

// TestDebugGroupMismatch checks that unbalanced groups become errors from
// Finish and never reach the queue.
func TestDebugGroupMismatch(t *testing.T) {
	device := newTestDevice(t)
	format := native.TextureFormatRGBA8Unorm
	_, view := newTarget(t, device, 4, 4, format)
	s := newBundleScene(t, device, format)
	renderPass := func(enc *CommandEncoder, fn func(p *RenderPassEncoder)) {
		pass, err := enc.BeginRenderPass(&RenderPassDescriptor{ColorAttachments: []RenderPassColorAttachment{{View: view}}})
		if err != nil {
			t.Fatal(err)
		}
		fn(pass)
		pass.End()
		pass.Release()
	}
	computePass := func(enc *CommandEncoder, fn func(p *ComputePassEncoder)) {
		pass, err := enc.BeginComputePass(nil)
		if err != nil {
			t.Fatal(err)
		}
		fn(pass)
		pass.End()
		pass.Release()
	}
	cases := []struct {
		name   string
		record func(enc *CommandEncoder)
		op     string
		want   string
	}{
		{"encoder pop without push", func(enc *CommandEncoder) {
			enc.PopDebugGroup()
		}, "CommandEncoderPopDebugGroup", "no debug group to pop"},
		{"encoder extra pop", func(enc *CommandEncoder) {
			enc.PushDebugGroup("a")
			enc.PopDebugGroup()
			enc.PopDebugGroup()
		}, "CommandEncoderPopDebugGroup", "no debug group to pop"},
		{"encoder open group", func(enc *CommandEncoder) {
			enc.PushDebugGroup("a")
		}, "CommandEncoderFinish", "1 debug group still open"},
		{"encoder two open groups", func(enc *CommandEncoder) {
			enc.PushDebugGroup("a")
			enc.PushDebugGroup("b")
			enc.InsertDebugMarker("m")
		}, "CommandEncoderFinish", "2 debug groups still open"},
		{"render pass pop without push", func(enc *CommandEncoder) {
			renderPass(enc, func(p *RenderPassEncoder) { p.PopDebugGroup() })
		}, "RenderPassEncoderPopDebugGroup", "no debug group to pop"},
		{"render pass open group", func(enc *CommandEncoder) {
			renderPass(enc, func(p *RenderPassEncoder) { p.PushDebugGroup("a") })
		}, "RenderPassEncoderEnd", "1 debug group still open"},
		{"encoder group popped inside pass", func(enc *CommandEncoder) {
			enc.PushDebugGroup("outer")
			renderPass(enc, func(p *RenderPassEncoder) { p.PopDebugGroup() })
			enc.PopDebugGroup()
		}, "RenderPassEncoderPopDebugGroup", "no debug group to pop"},
		{"pass group popped on encoder", func(enc *CommandEncoder) {
			renderPass(enc, func(p *RenderPassEncoder) { p.PushDebugGroup("inner") })
			enc.PopDebugGroup()
		}, "RenderPassEncoderEnd", "1 debug group still open"},
		{"compute pass pop without push", func(enc *CommandEncoder) {
			computePass(enc, func(p *ComputePassEncoder) { p.PopDebugGroup() })
		}, "ComputePassEncoderPopDebugGroup", "no debug group to pop"},
		{"compute pass open groups", func(enc *CommandEncoder) {
			computePass(enc, func(p *ComputePassEncoder) {
				p.PushDebugGroup("a")
				p.PushDebugGroup("b")
				p.PushDebugGroup("c")
				p.PopDebugGroup()
			})
		}, "ComputePassEncoderEnd", "2 debug groups still open"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			enc, err := device.CreateCommandEncoder()
			if err != nil {
				t.Fatal(err)
			}
			c.record(enc)
			cb, err := enc.Finish()
			if err == nil {
				cb.Release()
				t.Fatal("Finish succeeded")
			}
			werr, ok := err.(*Error)
			if !ok || werr.Op != c.op || !strings.Contains(werr.Status, c.want) {
				t.Fatalf("error = %v, want op %s with %q", err, c.op, c.want)
			}
			t.Logf("%v", err)
		})
	}

	bundleCases := []struct {
		name   string
		record func(b *RenderBundleEncoder)
		want   string
	}{
		{"bundle pop without push", func(b *RenderBundleEncoder) { b.PopDebugGroup() }, "no debug group to pop"},
		{"bundle open group", func(b *RenderBundleEncoder) {
			b.PushDebugGroup("a")
			b.SetPipeline(s.pipeline)
		}, "1 debug group still open"},
	}
	for _, c := range bundleCases {
		t.Run(c.name, func(t *testing.T) {
			be, err := device.CreateRenderBundleEncoder(&RenderBundleEncoderDescriptor{ColorFormats: []native.TextureFormat{format}})
			if err != nil {
				t.Fatal(err)
			}
			defer be.Release()
			c.record(be)
			b, err := be.Finish()
			if err == nil {
				b.Release()
				t.Fatal("Finish succeeded")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want %q", err, c.want)
			}
			t.Logf("%v", err)
		})
	}

	// The device must still be usable.
	enc, err := device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	enc.PushDebugGroup("ok")
	renderPass(enc, func(p *RenderPassEncoder) {
		p.PushDebugGroup("ok")
		p.PopDebugGroup()
	})
	enc.PopDebugGroup()
	submit(t, device, enc)
	device.Poll(true)
	if err := device.Err(); err != nil {
		t.Fatal(err)
	}
}
