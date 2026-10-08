//go:build windows && amd64

package wgpu

import (
	"strings"
	"testing"

	"github.com/liuqi0826/wgpu/native"
)

func TestDrawIndirectPixels(t *testing.T) {
	f := newMultiDrawFixture(t)
	records := f.buffer(t, native.BufferUsageIndirect, drawRecords(0, 1, 2, 3))
	indexed := f.buffer(t, native.BufferUsageIndirect, indexedRecords(0, 1, 2, 3))
	f.check(t, func(p *RenderPassEncoder) { p.DrawIndirect(records, 32) }, [4]bool{false, false, true, false})
	f.check(t, func(p *RenderPassEncoder) {
		p.DrawIndexedIndirect(indexed, 20)
		p.DrawIndirect(records, 48) // the last record ends exactly at the end of the buffer
	}, [4]bool{false, true, false, true})
}

// Misuse of single indirect draws and dispatches must surface as errors from
// Finish instead of aborting the process; wgpu-native aborts on a nil buffer.
func TestIndirectMisuse(t *testing.T) {
	f := newMultiDrawFixture(t)
	records := f.buffer(t, native.BufferUsageIndirect, drawRecords(0))     // 16 bytes
	indexed := f.buffer(t, native.BufferUsageIndirect, indexedRecords(0))  // 20 bytes
	dispatch := f.buffer(t, native.BufferUsageIndirect, u32Bytes(1, 1, 1)) // 12 bytes
	noUsage := f.buffer(t, native.BufferUsageVertex, drawRecords(0))
	noUsageIndexed := f.buffer(t, native.BufferUsageVertex, indexedRecords(0))
	released := f.buffer(t, native.BufferUsageIndirect, drawRecords(0))
	released.Release()

	renderCases := []struct {
		name string
		fn   func(p *RenderPassEncoder)
		want string
	}{
		{"draw nil buffer", func(p *RenderPassEncoder) { p.DrawIndirect(nil, 0) }, "nil or released indirect buffer"},
		{"draw released buffer", func(p *RenderPassEncoder) { p.DrawIndirect(released, 0) }, "nil or released indirect buffer"},
		{"draw misaligned", func(p *RenderPassEncoder) { p.DrawIndirect(records, 2) }, "not a multiple of 4"},
		{"draw overrun", func(p *RenderPassEncoder) { p.DrawIndirect(records, 4) }, "indirect buffer overrun"},
		{"draw offset past end", func(p *RenderPassEncoder) { p.DrawIndirect(records, 1<<63) }, "indirect buffer overrun"},
		{"draw max offset", func(p *RenderPassEncoder) { p.DrawIndirect(records, 1<<64-4) }, "indirect buffer overrun"},
		{"draw missing usage", func(p *RenderPassEncoder) { p.DrawIndirect(noUsage, 0) }, "INDIRECT"},
		{"indexed nil buffer", func(p *RenderPassEncoder) { p.DrawIndexedIndirect(nil, 0) }, "nil or released indirect buffer"},
		{"indexed released buffer", func(p *RenderPassEncoder) { p.DrawIndexedIndirect(released, 0) }, "nil or released indirect buffer"},
		{"indexed misaligned", func(p *RenderPassEncoder) { p.DrawIndexedIndirect(indexed, 1) }, "not a multiple of 4"},
		{"indexed overrun", func(p *RenderPassEncoder) { p.DrawIndexedIndirect(records, 0) }, "indirect buffer overrun"},
		{"indexed missing usage", func(p *RenderPassEncoder) { p.DrawIndexedIndirect(noUsageIndexed, 0) }, "INDIRECT"},
	}
	for _, tc := range renderCases {
		t.Run(tc.name, func(t *testing.T) {
			err := f.run(t, tc.fn)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Finish error %v, want %q", err, tc.want)
			}
			t.Logf("%.160s", err)
		})
	}

	computeCases := []struct {
		name string
		buf  *Buffer
		off  uint64
		want string
	}{
		{"dispatch nil buffer", nil, 0, "nil or released indirect buffer"},
		{"dispatch released buffer", released, 0, "nil or released indirect buffer"},
		{"dispatch misaligned", dispatch, 2, "not a multiple of 4"},
		{"dispatch overrun", dispatch, 4, "indirect buffer overrun"},
		{"dispatch offset past end", dispatch, 1 << 63, "indirect buffer overrun"},
		{"dispatch max offset", dispatch, 1<<64 - 4, "indirect buffer overrun"},
	}
	for _, tc := range computeCases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := f.device.CreateCommandEncoder()
			if err != nil {
				t.Fatal(err)
			}
			pass, err := enc.BeginComputePass(nil)
			if err != nil {
				t.Fatal(err)
			}
			pass.DispatchWorkgroupsIndirect(tc.buf, tc.off)
			pass.End()
			pass.Release()
			cb, err := enc.Finish()
			if cb != nil {
				cb.Release()
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Finish error %v, want %q", err, tc.want)
			}
			t.Logf("%.160s", err)
		})
	}

	t.Run("released passes", func(t *testing.T) {
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
		pass.DrawIndirect(records, 0)
		pass.DrawIndexedIndirect(indexed, 0)
		cpass, err := enc.BeginComputePass(nil)
		if err != nil {
			t.Fatal(err)
		}
		cpass.End()
		cpass.Release()
		cpass.DispatchWorkgroupsIndirect(dispatch, 0)
		cb, err := enc.Finish()
		if cb != nil {
			cb.Release()
		}
		if err == nil || !strings.Contains(err.Error(), "released pass") {
			t.Fatalf("Finish error %v", err)
		}
		t.Logf("%.160s", err)
	})

	bundleCases := []struct {
		name string
		fn   func(e *RenderBundleEncoder)
		want string
	}{
		{"draw nil buffer", func(e *RenderBundleEncoder) { e.DrawIndirect(nil, 0) }, "nil or released indirect buffer"},
		{"draw released buffer", func(e *RenderBundleEncoder) { e.DrawIndirect(released, 0) }, "nil or released indirect buffer"},
		{"draw misaligned", func(e *RenderBundleEncoder) { e.DrawIndirect(records, 2) }, "not a multiple of 4"},
		{"draw overrun", func(e *RenderBundleEncoder) { e.DrawIndirect(records, 16) }, "indirect buffer overrun"},
		{"draw max offset", func(e *RenderBundleEncoder) { e.DrawIndirect(records, 1<<64-4) }, "indirect buffer overrun"},
		{"indexed nil buffer", func(e *RenderBundleEncoder) { e.DrawIndexedIndirect(nil, 0) }, "nil or released indirect buffer"},
		{"indexed misaligned", func(e *RenderBundleEncoder) { e.DrawIndexedIndirect(indexed, 6) }, "not a multiple of 4"},
		{"indexed overrun", func(e *RenderBundleEncoder) { e.DrawIndexedIndirect(records, 0) }, "indirect buffer overrun"},
		{"draw missing usage", func(e *RenderBundleEncoder) {
			e.SetPipeline(f.pipeline)
			e.SetVertexBuffer(0, f.vertices, 0, 0)
			e.DrawIndirect(noUsage, 0)
		}, "INDIRECT"},
	}
	for _, tc := range bundleCases {
		t.Run("bundle "+tc.name, func(t *testing.T) {
			be, err := f.device.CreateRenderBundleEncoder(&RenderBundleEncoderDescriptor{ColorFormats: []native.TextureFormat{native.TextureFormatRGBA8Unorm}})
			if err != nil {
				t.Fatal(err)
			}
			defer be.Release()
			tc.fn(be)
			b, err := be.Finish()
			if b != nil {
				b.Release()
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Finish error %v, want %q", err, tc.want)
			}
			t.Logf("%.160s", err)
		})
	}

	t.Run("bundle valid", func(t *testing.T) {
		be, err := f.device.CreateRenderBundleEncoder(&RenderBundleEncoderDescriptor{ColorFormats: []native.TextureFormat{native.TextureFormatRGBA8Unorm}})
		if err != nil {
			t.Fatal(err)
		}
		defer be.Release()
		be.SetPipeline(f.pipeline)
		be.SetVertexBuffer(0, f.vertices, 0, 0)
		be.SetIndexBuffer(f.indices, native.IndexFormatUint32, 0, 0)
		be.DrawIndirect(records, 0)
		be.DrawIndexedIndirect(indexed, 0)
		b, err := be.Finish()
		if err != nil {
			t.Fatal(err)
		}
		defer b.Release()
		f.check(t, func(p *RenderPassEncoder) { p.ExecuteBundles(b) }, [4]bool{true})
	})

	if err := f.device.Err(); err != nil {
		t.Fatalf("misuse leaked an uncaptured error: %v", err)
	}
}
