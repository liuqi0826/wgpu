//go:build windows && amd64

package wgpu

import (
	"testing"

	"github.com/liuqi0826/wgpu/native"
)

// An invalid pass must surface from Finish. Submitting the resulting command
// buffer would make wgpu-native abort the process.
func TestFinishReportsPassError(t *testing.T) {
	device := newTestDevice(t)
	target, err := device.CreateTexture(&TextureDescriptor{
		Usage:  native.TextureUsageRenderAttachment,
		Width:  4,
		Height: 4,
		Format: native.TextureFormatRGBA8Unorm,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Release()
	view, err := target.CreateView(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Release()

	enc, err := device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Release()
	pass, err := enc.BeginRenderPass(&RenderPassDescriptor{
		ColorAttachments: []RenderPassColorAttachment{{View: view}},
	})
	if err != nil {
		t.Fatal(err)
	}
	pass.Draw(3, 1, 0, 0) // no pipeline
	pass.End()
	pass.Release()
	commands, err := enc.Finish()
	if err == nil {
		commands.Release()
		t.Fatal("Finish accepted a draw without a pipeline")
	}
	t.Logf("finish: %.120q", err.Error())
	if _, err := enc.Finish(); err == nil {
		t.Fatal("second Finish succeeded")
	}

	// The device stays usable.
	enc2, err := device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	submit(t, device, enc2)
	if err := device.Err(); err != nil {
		t.Fatalf("device error after a guarded Finish: %v", err)
	}
}
