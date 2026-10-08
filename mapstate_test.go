//go:build windows && amd64

package wgpu

import (
	"testing"
	"time"

	"github.com/liuqi0826/wgpu/native"
)

func wantMapState(t *testing.T, b *Buffer, want native.BufferMapState, when string) {
	t.Helper()
	if got := b.MapState(); got != want {
		t.Errorf("%s: MapState = %v, want %v", when, got, want)
	}
}

// wgpuBufferGetMapState aborts the process in wgpu-native 29, so MapState is
// answered from the Go-side tracking. This walks through every transition.
func TestBufferMapState(t *testing.T) {
	device := newTestDevice(t)
	usage := native.BufferUsageMapRead | native.BufferUsageCopyDst

	buf, err := device.CreateBuffer(usage, 16, false)
	if err != nil {
		t.Fatal(err)
	}
	defer buf.Release()
	wantMapState(t, buf, native.BufferMapStateUnmapped, "new buffer")

	ch := buf.MapAsync(native.MapModeRead, 0, 0)
	wantMapState(t, buf, native.BufferMapStatePending, "after MapAsync")
	if err := device.waitFor(ch, 5*time.Second, "MapAsync"); err != nil {
		t.Fatal(err)
	}
	wantMapState(t, buf, native.BufferMapStateMapped, "after the mapping completed")
	buf.Unmap()
	wantMapState(t, buf, native.BufferMapStateUnmapped, "after Unmap")

	// Unmap while the request is in flight.
	ch = buf.MapAsync(native.MapModeRead, 0, 0)
	buf.Unmap()
	wantMapState(t, buf, native.BufferMapStateUnmapped, "after Unmap of a pending mapping")
	if err := device.waitFor(ch, 5*time.Second, "MapAsync"); err == nil {
		t.Error("MapAsync cancelled by Unmap returned nil")
	}
	wantMapState(t, buf, native.BufferMapStateUnmapped, "after the cancelled callback")

	// A request that fails validation (no MapWrite usage).
	if err := device.waitFor(buf.MapAsync(native.MapModeWrite, 0, 0), 5*time.Second, "MapAsync"); err == nil {
		t.Error("MapAsync with MapModeWrite on a MapRead buffer returned nil")
	}
	wantMapState(t, buf, native.BufferMapStateUnmapped, "after a failed mapping")

	created, err := device.CreateBuffer(native.BufferUsageCopySrc, 16, true)
	if err != nil {
		t.Fatal(err)
	}
	wantMapState(t, created, native.BufferMapStateMapped, "mapped at creation")
	created.Unmap()
	wantMapState(t, created, native.BufferMapStateUnmapped, "mapped at creation, then Unmap")
	created.Release()
	wantMapState(t, created, native.BufferMapStateUnmapped, "released buffer")
}

// Releasing or losing the device cancels a pending mapping, and the buffer
// must not stay Pending afterwards.
func TestBufferMapStateCancelled(t *testing.T) {
	for _, end := range []string{"Release", "Destroy"} {
		t.Run(end, func(t *testing.T) {
			instance := testInstance(t)
			defer instance.Release()
			adapter, err := instance.RequestAdapter(nil)
			if err != nil {
				t.Fatal(err)
			}
			defer adapter.Release()
			device, err := adapter.RequestDevice()
			if err != nil {
				t.Fatal(err)
			}
			released := false
			defer func() {
				if !released {
					device.Release()
				}
			}()
			buf, err := device.CreateBuffer(native.BufferUsageMapRead|native.BufferUsageCopyDst, 16, false)
			if err != nil {
				t.Fatal(err)
			}
			defer buf.Release()
			ch := buf.MapAsync(native.MapModeRead, 0, 0)
			wantMapState(t, buf, native.BufferMapStatePending, "after MapAsync")
			if end == "Release" {
				device.Release()
				released = true
			} else {
				device.Destroy()
			}
			select {
			case err := <-ch:
				if err == nil {
					t.Error("pending MapAsync completed with nil")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("pending MapAsync was not completed")
			}
			wantMapState(t, buf, native.BufferMapStateUnmapped, "after device "+end)
			if again := <-buf.MapAsync(native.MapModeRead, 0, 0); again == nil {
				t.Error("MapAsync on a dead device returned nil")
			}
			wantMapState(t, buf, native.BufferMapStateUnmapped, "after MapAsync on a dead device")
		})
	}
}
