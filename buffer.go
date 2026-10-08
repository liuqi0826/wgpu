//go:build windows && amd64

package wgpu

import (
	"fmt"
	"sync/atomic"
	"time"
	"unsafe"

	"wgpu/native"
)

// Buffer is a WGPUBuffer.
type Buffer struct {
	dev  *Device
	ptr  atomic.Uintptr
	size uint64
}

// Native returns the raw buffer handle.
func (b *Buffer) Native() native.Buffer {
	if b == nil {
		return 0
	}
	return native.Buffer(b.ptr.Load())
}

// Size returns the buffer size in bytes.
func (b *Buffer) Size() uint64 { return b.size }

// MappedBytes returns the currently mapped writable range as a slice.
// The slice is invalid after Unmap or Release. A read-only mapping must use ReadMapped.
func (b *Buffer) MappedBytes(offset, size uint64) ([]byte, error) {
	if size == 0 {
		return nil, nil
	}
	ptr := native.BufferGetMappedRange(b.Native(), uintptr(offset), uintptr(size))
	if ptr == nil {
		return nil, fmt.Errorf("wgpu: mapped range is null")
	}
	return unsafe.Slice((*byte)(ptr), size), nil
}

// ReadMapped copies a read-only mapped range out of the buffer.
func (b *Buffer) ReadMapped(offset, size uint64) ([]byte, error) {
	if size == 0 {
		return nil, nil
	}
	ptr := native.BufferGetConstMappedRange(b.Native(), uintptr(offset), uintptr(size))
	if ptr == nil {
		return nil, fmt.Errorf("wgpu: const mapped range is null")
	}
	src := unsafe.Slice((*byte)(ptr), size)
	out := make([]byte, size)
	copy(out, src)
	return out, nil
}

// Map waits until the requested range is mapped.
func (b *Buffer) Map(mode native.MapMode, offset, size uint64) error {
	if b.dev.isLost() {
		return b.dev.Err()
	}
	ensureCallbacks()
	id, _ := newWaiter(&mapWaiters)
	info := native.BufferMapCallbackInfo{
		Mode:      native.CallbackModeWaitAnyOnly,
		Callback:  native.BufferMapCallback(mapCB),
		Userdata1: id,
	}
	native.BufferMapAsync(b.Native(), mode, uintptr(offset), uintptr(size), info)
	deadline := time.Now().Add(5 * time.Second)
	var (
		res callbackResult
		ok  bool
	)
	for {
		b.dev.Poll(false)
		res, ok = tryTake(&mapWaiters, id)
		if ok || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !ok {
		mapWaiters.Delete(id)
		return &Error{Op: "MapAsync", Status: "timed out"}
	}
	if native.MapAsyncStatus(res.status) != native.MapAsyncStatusSuccess {
		status := native.MapAsyncStatus(res.status)
		return &Error{Op: "MapAsync", Status: status.String(), Message: res.message}
	}
	return nil
}

// Unmap unmaps a mapped buffer.
func (b *Buffer) Unmap() {
	native.BufferUnmap(b.Native())
}

// Release destroys and drops the buffer.
func (b *Buffer) Release() {
	handle := native.Buffer(b.ptr.Swap(0))
	if handle != 0 {
		native.BufferDestroy(handle)
		native.BufferRelease(handle)
	}
}
