//go:build windows && amd64

package wgpu

import "testing"

// wgpuDevicePoll returns a 1-byte Rust bool. Read as a 32-bit WGPUBool, the
// upper bytes held part of a stack address and Poll reported false even on an
// idle queue.
func TestPollReportsIdleQueue(t *testing.T) {
	device := newTestDevice(t)
	if !device.Poll(true) {
		t.Fatal("Poll(true) on a new device = false, want true")
	}
	enc, err := device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	submit(t, device, enc)
	if !device.Poll(true) {
		t.Fatal("Poll(true) after a submit = false, want true")
	}
	if !device.Poll(false) {
		t.Fatal("Poll(false) on an idle queue = false, want true")
	}
}
