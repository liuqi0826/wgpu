//go:build windows && amd64

package wgpu

import (
	"testing"

	"github.com/liuqi0826/wgpu/native"
)

// newTarget creates a render target that can be copied out.
func newTarget(t *testing.T, device *Device, w, h uint32, format native.TextureFormat) (*Texture, *TextureView) {
	t.Helper()
	tex, err := device.CreateTexture(&TextureDescriptor{
		Usage:  native.TextureUsageRenderAttachment | native.TextureUsageCopySrc,
		Width:  w,
		Height: h,
		Format: format,
	})
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

// readRGBA8 copies a 4-byte-per-texel texture back as tightly packed rows.
func readRGBA8(t *testing.T, device *Device, tex *Texture, w, h uint32) []byte {
	t.Helper()
	row := (w*4 + 255) &^ 255
	readback, err := device.CreateBuffer(native.BufferUsageMapRead|native.BufferUsageCopyDst, uint64(row*h), false)
	if err != nil {
		t.Fatal(err)
	}
	defer readback.Release()
	enc, err := device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	enc.CopyTextureToBuffer(TexelCopyTexture{Texture: tex},
		TexelCopyBuffer{Buffer: readback, Layout: native.TexelCopyBufferLayout{BytesPerRow: row, RowsPerImage: h}},
		native.Extent3D{Width: w, Height: h, DepthOrArrayLayers: 1})
	submit(t, device, enc)
	if err := readback.Map(native.MapModeRead, 0, uint64(row*h)); err != nil {
		t.Fatal(err)
	}
	data, err := readback.ReadMapped(0, uint64(row*h))
	readback.Unmap()
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 0, w*h*4)
	for y := uint32(0); y < h; y++ {
		out = append(out, data[y*row:y*row+w*4]...)
	}
	return out
}

func texel(data []byte, w, x, y int) []byte {
	i := (y*w + x) * 4
	return data[i : i+4]
}
