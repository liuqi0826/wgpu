//go:build windows && amd64

package wgpu

import (
	"testing"
	"time"

	"github.com/liuqi0826/wgpu/native"
)

// submitBusy submits a compute pass that keeps the GPU busy for a while.
func submitBusy(t *testing.T, device *Device, groups uint32) {
	t.Helper()
	module, err := device.CreateShaderModuleWGSL("busy", busyWGSL)
	if err != nil {
		t.Fatal(err)
	}
	defer module.Release()
	pipeline, err := device.CreateComputePipeline(&ComputePipelineDescriptor{Compute: ComputeState{Module: module}})
	if err != nil {
		t.Fatal(err)
	}
	defer pipeline.Release()
	storage, err := device.CreateBuffer(native.BufferUsageStorage, uint64(groups)*64*4, false)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Release()
	layout, err := pipeline.BindGroupLayout(0)
	if err != nil {
		t.Fatal(err)
	}
	defer layout.Release()
	group, err := device.CreateBindGroup(&BindGroupDescriptor{Layout: layout, Entries: []BindGroupEntry{{Binding: 0, Buffer: storage}}})
	if err != nil {
		t.Fatal(err)
	}
	defer group.Release()
	enc, err := device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	pass, err := enc.BeginComputePass(nil)
	if err != nil {
		t.Fatal(err)
	}
	pass.SetPipeline(pipeline)
	pass.SetBindGroup(0, group)
	pass.DispatchWorkgroups(groups, 1, 1)
	pass.End()
	pass.Release()
	submit(t, device, enc)
}

func ready(ch <-chan error) (error, bool) {
	select {
	case err := <-ch:
		return err, true
	default:
		return nil, false
	}
}

// TestOnSubmittedWorkDone records how this wgpu-native delivers the callback:
// never inside the call, only while the device is polled.
func TestOnSubmittedWorkDone(t *testing.T) {
	device := newTestDevice(t)
	queue := device.Queue()

	// Idle queue: wgpu-core defers the closure to the next maintenance.
	ch := queue.OnSubmittedWorkDone()
	if _, ok := ready(ch); ok {
		t.Fatal("idle queue: callback ran inside OnSubmittedWorkDone")
	}
	time.Sleep(20 * time.Millisecond)
	if _, ok := ready(ch); ok {
		t.Fatal("idle queue: callback ran without polling")
	}
	device.Poll(false)
	if err, ok := ready(ch); !ok || err != nil {
		t.Fatalf("idle queue: after Poll got %v, %v", err, ok)
	}

	// Busy queue: pending until a poll observes the submission finishing.
	submitBusy(t, device, 4096)
	start := time.Now()
	ch = queue.OnSubmittedWorkDone()
	if _, ok := ready(ch); ok {
		t.Fatal("busy queue: callback ran inside OnSubmittedWorkDone")
	}
	polls := 0
	for {
		device.Poll(false)
		polls++
		if err, ok := ready(ch); ok {
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Since(start) > 5*time.Second {
			t.Fatal("busy queue: work never finished")
		}
		time.Sleep(100 * time.Microsecond)
	}
	t.Logf("busy queue: done after %d polls, %v", polls, time.Since(start))

	// A later Submit also runs maintenance and delivers pending callbacks.
	ch = queue.OnSubmittedWorkDone()
	enc, err := device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	submit(t, device, enc)
	_, bySubmit := ready(ch)
	t.Logf("idle queue: delivered by a later Submit without Poll: %v", bySubmit)
	device.Poll(false)
	if _, ok := ready(ch); !ok && !bySubmit {
		t.Fatal("callback not delivered after Submit and Poll")
	}

	// Blocking forms.
	submitBusy(t, device, 4096)
	start = time.Now()
	if err := queue.WaitSubmittedWorkDone(0); err != nil {
		t.Fatal(err)
	}
	t.Logf("WaitSubmittedWorkDone(0): %v", time.Since(start))
	submitBusy(t, device, 4096)
	if err := queue.WaitSubmittedWorkDone(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	if err := device.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkDoneCancelled(t *testing.T) {
	adapter := newTestAdapter(t)
	device, err := adapter.RequestDevice()
	if err != nil {
		t.Fatal(err)
	}
	submitBusy(t, device, 4096)
	ch := device.Queue().OnSubmittedWorkDone()
	device.Release()
	select {
	case err := <-ch:
		if err == nil {
			t.Fatal("pending work-done succeeded after Release")
		}
		t.Logf("after Release: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("pending work-done never completed after Release")
	}
	if _, ok := <-ch; ok {
		t.Fatal("channel not closed")
	}

	device, err = adapter.RequestDevice()
	if err != nil {
		t.Fatal(err)
	}
	defer device.Release()
	ch = device.Queue().OnSubmittedWorkDone()
	device.Destroy()
	if err, ok := ready(ch); !ok || err == nil {
		t.Fatalf("after Destroy got %v, %v", err, ok)
	} else {
		t.Logf("after Destroy: %v", err)
	}
	if err := device.Queue().WaitSubmittedWorkDone(time.Second); err == nil {
		t.Fatal("wait on a lost device succeeded")
	}
}
