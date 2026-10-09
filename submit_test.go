//go:build windows && amd64

package wgpu

import (
	"testing"
	"time"

	"github.com/liuqi0826/wgpu/native"
)

type submitFixture struct {
	device *Device
	src    *Buffer
	dst    *Buffer
}

func newSubmitFixture(t *testing.T) *submitFixture {
	t.Helper()
	d := newTestDevice(t)
	f := &submitFixture{device: d}
	f.src = f.buffer(t, native.BufferUsageCopySrc|native.BufferUsageMapWrite, 16, false)
	f.dst = f.buffer(t, native.BufferUsageCopyDst|native.BufferUsageMapRead, 16, false)
	return f
}

func (f *submitFixture) buffer(t *testing.T, usage native.BufferUsage, size uint64, mapped bool) *Buffer {
	t.Helper()
	b, err := f.device.CreateBuffer(usage, size, mapped)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Release)
	return b
}

func (f *submitFixture) texture(t *testing.T, usage native.TextureUsage) (*Texture, *TextureView) {
	t.Helper()
	tex, err := f.device.CreateTexture(&TextureDescriptor{Usage: usage, Width: 4, Height: 4, Format: native.TextureFormatRGBA8Unorm})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tex.Release)
	view, err := tex.CreateView(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.Release)
	return tex, view
}

// encode records fn and returns the finished command buffer.
func (f *submitFixture) encode(t *testing.T, fn func(e *CommandEncoder)) *CommandBuffer {
	t.Helper()
	enc, err := f.device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	fn(enc)
	cb, err := enc.Finish()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cb.Release)
	return cb
}

func (f *submitFixture) copyCmd(t *testing.T, src, dst *Buffer) *CommandBuffer {
	return f.encode(t, func(e *CommandEncoder) { e.CopyBufferToBuffer(src, 0, dst, 0, 16) })
}

// noUncaptured checks that a rejected submission never reached wgpu-native.
func (f *submitFixture) noUncaptured(t *testing.T) {
	t.Helper()
	if err := f.device.Err(); err != nil {
		t.Fatalf("device error: %v", err)
	}
}

func (f *submitFixture) storageGroup(t *testing.T, buf *Buffer) *BindGroup {
	t.Helper()
	layout, err := f.device.CreateBindGroupLayout(&BindGroupLayoutDescriptor{Entries: []BindGroupLayoutEntry{
		{Binding: 0, Visibility: native.ShaderStageCompute, Buffer: BufferBinding{Type: native.BufferBindingTypeStorage}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(layout.Release)
	group, err := f.device.CreateBindGroup(&BindGroupDescriptor{Layout: layout, Entries: []BindGroupEntry{{Binding: 0, Buffer: buf}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(group.Release)
	return group
}

func (f *submitFixture) computeWithGroup(t *testing.T, g *BindGroup) *CommandBuffer {
	return f.encode(t, func(e *CommandEncoder) {
		pass, err := e.BeginComputePass(nil)
		if err != nil {
			t.Fatal(err)
		}
		pass.SetBindGroup(0, g)
		pass.End()
		pass.Release()
	})
}

// Each case ends with a submission that wgpu-native would abort on.
var submitCases = map[string]func(t *testing.T){
	"mapped at creation": func(t *testing.T) {
		f := newSubmitFixture(t)
		src := f.buffer(t, native.BufferUsageCopySrc|native.BufferUsageMapWrite, 16, true)
		cb := f.copyCmd(t, src, f.dst)
		wantErr(t, f.device.Queue().Submit(cb), "command buffer 0 uses a buffer that is still mapped")
		src.Unmap()
		if err := f.device.Queue().Submit(cb); err != nil {
			t.Fatalf("submit after Unmap: %v", err)
		}
		f.noUncaptured(t)
	},
	"pending map": func(t *testing.T) {
		f := newSubmitFixture(t)
		cb := f.copyCmd(t, f.src, f.dst)
		ch := f.dst.MapAsync(native.MapModeRead, 0, 0)
		wantErr(t, f.device.Queue().Submit(cb), "uses a buffer with a pending MapAsync")
		if err := f.device.waitFor(ch, 5*time.Second, "MapAsync"); err != nil {
			t.Fatal(err)
		}
		wantErr(t, f.device.Queue().Submit(cb), "uses a buffer that is still mapped")
		f.dst.Unmap()
		if err := f.device.Queue().Submit(cb); err != nil {
			t.Fatal(err)
		}
		f.noUncaptured(t)
	},
	"released buffer": func(t *testing.T) {
		f := newSubmitFixture(t)
		cb := f.copyCmd(t, f.src, f.dst)
		f.dst.Release()
		wantErr(t, f.device.Queue().Submit(cb), "uses a released buffer")
		f.noUncaptured(t)
	},
	"destroyed copy texture": func(t *testing.T) {
		f := newSubmitFixture(t)
		tex, _ := f.texture(t, native.TextureUsageCopySrc)
		dst := f.buffer(t, native.BufferUsageCopyDst, 1024, false)
		cb := f.encode(t, func(e *CommandEncoder) {
			e.CopyTextureToBuffer(TexelCopyTexture{Texture: tex},
				TexelCopyBuffer{Buffer: dst, Layout: native.TexelCopyBufferLayout{BytesPerRow: 256}},
				native.Extent3D{Width: 4, Height: 4, DepthOrArrayLayers: 1})
		})
		tex.Destroy()
		wantErr(t, f.device.Queue().Submit(cb), "uses a destroyed texture")
		f.noUncaptured(t)
	},
	"released texture is fine": func(t *testing.T) {
		// Release only drops the reference; the command buffer keeps the texture.
		f := newSubmitFixture(t)
		tex, _ := f.texture(t, native.TextureUsageCopyDst)
		cb := f.encode(t, func(e *CommandEncoder) {
			e.CopyBufferToTexture(TexelCopyBuffer{Buffer: f.buffer(t, native.BufferUsageCopySrc, 1024, false), Layout: native.TexelCopyBufferLayout{BytesPerRow: 256}},
				TexelCopyTexture{Texture: tex}, native.Extent3D{Width: 4, Height: 4, DepthOrArrayLayers: 1})
		})
		tex.Release()
		if err := f.device.Queue().Submit(cb); err != nil {
			t.Fatal(err)
		}
		f.noUncaptured(t)
	},
	"destroyed attachment": func(t *testing.T) {
		f := newSubmitFixture(t)
		tex, view := f.texture(t, native.TextureUsageRenderAttachment)
		cb := f.encode(t, func(e *CommandEncoder) {
			pass, err := e.BeginRenderPass(&RenderPassDescriptor{ColorAttachments: []RenderPassColorAttachment{{View: view}}})
			if err != nil {
				t.Fatal(err)
			}
			pass.End()
			pass.Release()
		})
		tex.Destroy()
		wantErr(t, f.device.Queue().Submit(cb), "uses a destroyed texture")
		f.noUncaptured(t)
	},
	"bind group released buffer": func(t *testing.T) {
		f := newSubmitFixture(t)
		buf := f.buffer(t, native.BufferUsageStorage, 16, false)
		cb := f.computeWithGroup(t, f.storageGroup(t, buf))
		buf.Release()
		wantErr(t, f.device.Queue().Submit(cb), "bind group uses a released buffer")
		f.noUncaptured(t)
	},
	"bind group destroyed texture": func(t *testing.T) {
		f := newSubmitFixture(t)
		tex, view := f.texture(t, native.TextureUsageTextureBinding)
		layout, err := f.device.CreateBindGroupLayout(&BindGroupLayoutDescriptor{Entries: []BindGroupLayoutEntry{
			{Binding: 0, Visibility: native.ShaderStageCompute, Texture: TextureBinding{SampleType: native.TextureSampleTypeFloat}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(layout.Release)
		g, err := f.device.CreateBindGroup(&BindGroupDescriptor{Layout: layout, Entries: []BindGroupEntry{{Binding: 0, TextureView: view}}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(g.Release)
		cb := f.computeWithGroup(t, g)
		tex.Destroy()
		wantErr(t, f.device.Queue().Submit(cb), "bind group uses a destroyed texture")
		f.noUncaptured(t)
	},
	"render bundle released buffer": func(t *testing.T) {
		f := newSubmitFixture(t)
		_, view := f.texture(t, native.TextureUsageRenderAttachment)
		vb := f.buffer(t, native.BufferUsageVertex, 64, false)
		be, err := f.device.CreateRenderBundleEncoder(&RenderBundleEncoderDescriptor{ColorFormats: []native.TextureFormat{native.TextureFormatRGBA8Unorm}})
		if err != nil {
			t.Fatal(err)
		}
		be.SetVertexBuffer(0, vb, 0, 0)
		bundle, err := be.Finish()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(bundle.Release)
		cb := f.encode(t, func(e *CommandEncoder) {
			pass, err := e.BeginRenderPass(&RenderPassDescriptor{ColorAttachments: []RenderPassColorAttachment{{View: view}}})
			if err != nil {
				t.Fatal(err)
			}
			pass.ExecuteBundles(bundle)
			pass.End()
			pass.Release()
		})
		vb.Release()
		wantErr(t, f.device.Queue().Submit(cb), "render bundle uses a released buffer")
		f.noUncaptured(t)
	},
	"destroyed query set": func(t *testing.T) {
		f := newSubmitFixture(t)
		q, err := f.device.CreateQuerySet(&QuerySetDescriptor{Type: native.QueryTypeOcclusion, Count: 1})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(q.Release)
		dst := f.buffer(t, native.BufferUsageQueryResolve, 256, false)
		cb := f.encode(t, func(e *CommandEncoder) { e.ResolveQuerySet(q, 0, 1, dst, 0) })
		q.Destroy()
		wantErr(t, f.device.Queue().Submit(cb), "uses a destroyed query set")
		// wgpuQuerySetDestroy followed by wgpuQuerySetRelease used to abort.
		q.Destroy()
		q.Release()
		if q.Count() != 1 || q.Type() != native.QueryTypeOcclusion {
			t.Fatalf("query set reports %d %v", q.Count(), q.Type())
		}
		enc, err := f.device.CreateCommandEncoder()
		if err != nil {
			t.Fatal(err)
		}
		enc.ResolveQuerySet(q, 0, 1, dst, 0)
		_, err = enc.Finish()
		wantErr(t, err, "released query set")
		f.noUncaptured(t)
	},
	"submitted twice": func(t *testing.T) {
		f := newSubmitFixture(t)
		cb := f.copyCmd(t, f.src, f.dst)
		q := f.device.Queue()
		wantErr(t, q.Submit(cb, cb), "command buffer 1 passed twice")
		if err := q.Submit(cb); err != nil {
			t.Fatal(err)
		}
		wantErr(t, q.Submit(cb), "command buffer 0 was already submitted")
		f.noUncaptured(t)
	},
	"nil or released command buffer": func(t *testing.T) {
		f := newSubmitFixture(t)
		cb := f.copyCmd(t, f.src, f.dst)
		wantErr(t, f.device.Queue().Submit(cb, nil), "command buffer 1 is nil or released")
		cb.Release()
		wantErr(t, f.device.Queue().Submit(cb), "command buffer 0 is nil or released")
		f.noUncaptured(t)
	},
	"other device": func(t *testing.T) {
		f := newSubmitFixture(t)
		other := newTestDevice(t)
		cb := f.copyCmd(t, f.src, f.dst)
		wantErr(t, other.Queue().Submit(cb), "was created on another device")
		f.noUncaptured(t)
	},
	"released device": func(t *testing.T) {
		f := newSubmitFixture(t)
		cb := f.copyCmd(t, f.src, f.dst)
		q := f.device.Queue()
		f.device.Release()
		wantErr(t, q.Submit(cb), "device released")
	},
	"lost device": func(t *testing.T) {
		f := newSubmitFixture(t)
		cb := f.copyCmd(t, f.src, f.dst)
		f.device.Destroy()
		wantErr(t, f.device.Queue().Submit(cb), "DeviceLost")
	},
	"buffer of another device": func(t *testing.T) {
		// wgpu-core 29 accepts this, although the buffer belongs to another
		// device.
		f := newSubmitFixture(t)
		other := newTestDevice(t)
		src, err := other.CreateBuffer(native.BufferUsageCopySrc, 16, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(src.Release)
		enc, err := f.device.CreateCommandEncoder()
		if err != nil {
			t.Fatal(err)
		}
		enc.CopyBufferToBuffer(src, 0, f.dst, 0, 16)
		cb, err := enc.Finish()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(cb.Release)
		wantErr(t, f.device.Queue().Submit(cb), "uses a buffer of another device")
		f.noUncaptured(t)
	},
	"valid submission": func(t *testing.T) {
		f := newSubmitFixture(t)
		if err := f.src.Map(native.MapModeWrite, 0, 0); err != nil {
			t.Fatal(err)
		}
		data, err := f.src.MappedBytes(0, 16)
		if err != nil {
			t.Fatal(err)
		}
		copy(data, []byte("0123456789abcdef"))
		f.src.Unmap()
		cb := f.copyCmd(t, f.src, f.dst)
		if err := f.device.Queue().Submit(cb); err != nil {
			t.Fatal(err)
		}
		if err := f.device.Queue().Submit(); err != nil {
			t.Fatalf("empty submit: %v", err)
		}
		if err := f.dst.Map(native.MapModeRead, 0, 0); err != nil {
			t.Fatal(err)
		}
		got, err := f.dst.ReadMapped(0, 16)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "0123456789abcdef" {
			t.Fatalf("read %q", got)
		}
		f.dst.Unmap()
		f.noUncaptured(t)
	},
}

var submitCaseNames = registerIsolated("submit/", submitCases)

// TestSubmitChecks runs each case in a child process: without the Go-side
// checks in Queue.Submit, wgpu-native aborts the process on these submissions.
func TestSubmitChecks(t *testing.T) {
	runIsolated(t, "submit")
}

func init() {
	isolatedCases["submit"] = func(t *testing.T) {
		for _, name := range submitCaseNames {
			t.Run(name, isolatedCases[name])
		}
	}
}
